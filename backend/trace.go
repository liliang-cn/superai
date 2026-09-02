package backend

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// A run's trace, kept for something other than a person.
//
// RunWall (runwall.go) narrates a run into state a window can draw: counts,
// sparklines, a couple of hundred lines of prose. That is the right shape for
// a control room and the wrong one the moment the question is "what exactly
// happened in round 34, and how long did that tool take". agent-go's
// TraceWriter answers that — the same Observer seams, emitted as JSONL, one
// object per event with its own ids, durations and token split.
//
// This is the fan-out that gives each task its own file. It sits where the
// other observers are registered, on the Service, because a trace is about
// the run and not about whichever window happens to be open.
//
// Only tasks that went through Begin are traced. That is the same rule the
// wall enforces and it is here for the same reason: the observer sees every
// run the service makes, chat turns included, and one file per chat message
// would bury the runs worth keeping under thousands of two-line traces.

const (
	// traceKeepFiles bounds how many task traces are kept on disk. Pruned
	// oldest-first when a new task starts, never while its own run is open.
	traceKeepFiles = 64
	// traceMaxBytes bounds one task's file. A run that loops for hours can
	// emit a great deal, and a trace is a record of what happened, not a
	// place to discover the disk is full. Past the cap a single
	// trace_truncated line is written and the rest is dropped.
	traceMaxBytes = 8 << 20
)

// TracesDir is where per-task JSONL traces live: <home>/data/traces.
func TracesDir() string { return filepath.Join(DataDir(), "data", "traces") }

// TraceStore is an Observer that writes one JSONL trace per task.
type TraceStore struct {
	agent.BaseObserver

	dir string

	mu   sync.Mutex
	open map[string]*taskTrace
}

// taskTrace is one task's open file and the writer over it.
type taskTrace struct {
	f *os.File
	w *agent.TraceWriter
}

// NewTraceStore returns a store writing into dir. The directory is created on
// first use, so a store nobody ever traces through leaves nothing behind.
func NewTraceStore(dir string) *TraceStore {
	return &TraceStore{dir: dir, open: map[string]*taskTrace{}}
}

// Path is where a task's trace is, whether or not it exists.
func (t *TraceStore) Path(taskID string) string {
	if t == nil || !validTraceID(taskID) {
		return ""
	}
	return filepath.Join(t.dir, taskID+".jsonl")
}

// Begin opens a task's trace and starts recording it.
//
// A task resumed under its own id appends to the file it already had, which is
// what makes a trace of a segmented run one document rather than one per
// segment.
func (t *TraceStore) Begin(taskID string) {
	if t == nil || !validTraceID(taskID) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.open[taskID]; ok {
		return
	}
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return
	}
	t.pruneLocked(taskID)

	path := filepath.Join(t.dir, taskID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	size := int64(0)
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	t.open[taskID] = &taskTrace{
		f: f,
		w: agent.NewTraceWriter(&capWriter{w: f, n: size, max: traceMaxBytes}),
	}
}

// Finish closes a task's trace. A task that was never begun is not an error.
func (t *TraceStore) Finish(taskID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	tt := t.open[taskID]
	delete(t.open, taskID)
	t.mu.Unlock()
	if tt != nil {
		_ = tt.f.Close()
	}
}

// Close releases every open trace. Called when the Service is torn down; a
// rebuild reopens whatever is still running under its own id.
func (t *TraceStore) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	open := t.open
	t.open = map[string]*taskTrace{}
	t.mu.Unlock()
	for _, tt := range open {
		_ = tt.f.Close()
	}
	return nil
}

// writer returns the writer for a task, or nil when it is not being traced.
func (t *TraceStore) writer(taskID string) *agent.TraceWriter {
	if t == nil || taskID == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if tt := t.open[taskID]; tt != nil {
		return tt.w
	}
	return nil
}

// pruneLocked keeps the newest traces, never touching one that is open nor the
// one about to be. The budget counts those too, so the cap is a cap on the
// directory rather than on the part of it this happens to be looking at.
// Caller holds t.mu.
func (t *TraceStore) pruneLocked(incoming string) {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return
	}
	type aged struct {
		path string
		mod  int64
	}
	files := make([]aged, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if _, live := t.open[id]; live || id == incoming {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, aged{filepath.Join(t.dir, e.Name()), info.ModTime().UnixNano()})
	}
	// The open traces and the incoming one hold slots of their own.
	budget := traceKeepFiles - len(t.open) - 1
	if budget < 0 {
		budget = 0
	}
	if len(files) <= budget {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	for _, f := range files[budget:] {
		_ = os.Remove(f.path)
	}
}

// --- Observer: every callback routed to its task's writer ---

func (t *TraceStore) OnModelStart(ctx context.Context, info agent.ModelInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnModelStart(ctx, info)
	}
}

func (t *TraceStore) OnModelEnd(ctx context.Context, info agent.ModelInfo, res *agent.ModelResult, err error) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnModelEnd(ctx, info, res, err)
	}
}

func (t *TraceStore) OnToolStart(ctx context.Context, info agent.ToolInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnToolStart(ctx, info)
	}
}

func (t *TraceStore) OnToolEnd(ctx context.Context, info agent.ToolInfo, result any, err error) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnToolEnd(ctx, info, result, err)
	}
}

func (t *TraceStore) OnSubAgentStart(ctx context.Context, info agent.SubAgentInfo) {
	if w := t.writer(info.ParentTaskID); w != nil {
		w.OnSubAgentStart(ctx, info)
	}
}

func (t *TraceStore) OnSubAgentEnd(ctx context.Context, info agent.SubAgentInfo, result any, err error) {
	if w := t.writer(info.ParentTaskID); w != nil {
		w.OnSubAgentEnd(ctx, info, result, err)
	}
}

func (t *TraceStore) OnCheckpoint(ctx context.Context, info agent.CheckpointInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnCheckpoint(ctx, info)
	}
}

func (t *TraceStore) OnLint(ctx context.Context, info agent.LintInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnLint(ctx, info)
	}
}

func (t *TraceStore) OnModelRetry(ctx context.Context, info agent.ModelRetryInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnModelRetry(ctx, info)
	}
}

func (t *TraceStore) OnCompaction(ctx context.Context, info agent.CompactionInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnCompaction(ctx, info)
	}
}

func (t *TraceStore) OnError(ctx context.Context, info agent.ErrorInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnError(ctx, info)
	}
}

func (t *TraceStore) OnSegment(ctx context.Context, info agent.SegmentInfo) {
	if w := t.writer(info.TaskID); w != nil {
		w.OnSegment(ctx, info)
	}
}

// OnModelDelta is deliberately not forwarded. A delta carries no task id, so
// there is nothing to route it by — and TraceWriter has them off by default
// anyway, because a reasoning model emits thousands per turn and a trace that
// is mostly deltas is a firehose nothing reads twice.

// --- reading a trace back ---

// TraceLines returns the last limit lines of a task's trace, oldest first.
//
// Raw JSONL: one object per line, as agent-go wrote it. The caller parses,
// which keeps this from having to grow a field every time the framework adds
// one to a trace line.
func TraceLines(dir, taskID string, limit int) []string {
	out := []string{}
	if !validTraceID(taskID) {
		return out
	}
	if limit <= 0 {
		limit = 200
	}
	data, err := os.ReadFile(filepath.Join(dir, taskID+".jsonl"))
	if err != nil {
		return out
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return out
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return append(out, lines...)
}

// validTraceID keeps a task id from naming a file outside the traces
// directory. Task ids are UUIDs, but one can be typed into the resume field,
// and a path separator in it would be a write wherever it pointed.
func validTraceID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// capWriter stops writing past max, once, with a line saying so. It needs no
// lock of its own: there is one per task and TraceWriter serialises every
// write through its own mutex.
type capWriter struct {
	w    io.Writer
	n    int64
	max  int64
	done bool
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.done {
		return len(p), nil
	}
	if c.max > 0 && c.n+int64(len(p)) > c.max {
		c.done = true
		_, _ = c.w.Write([]byte(`{"event":"trace_truncated","reason":"per-task size cap reached"}` + "\n"))
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
