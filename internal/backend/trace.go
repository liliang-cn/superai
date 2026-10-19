package backend

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
// A task that went through Begin has a file of its own. Every other run is
// traced into its conversation's file, named by the session id, so a chat is
// one document however many turns it takes: a file per message would bury
// the runs worth keeping under thousands of two-line traces.
//
// The service is built WithObserverPayloads, so each model_end line carries
// what the model was sent: the messages and the tools. That is what makes a
// trace answer "why did it do that" and not only "how long did it take", and
// it is why it stays here, on this machine, in no one else's service.

const (
	// traceKeepFiles and traceKeepBytes bound the traces kept on disk, task
	// and conversation files together. Pruned oldest-first when a new file
	// starts, never one that is open.
	traceKeepFiles = 256
	traceKeepBytes = 512 << 20
	// traceMaxBytes bounds one file. Every model_end repeats the request, the
	// whole conversation so far, so a long run grows fast; and a trace is a
	// record of what happened, not a place to discover the disk is full. Past
	// the cap a single trace_truncated line is written and the rest dropped.
	traceMaxBytes = 32 << 20
	// traceMaxText bounds a tool's result, an error, an answer. agent-go's
	// default, 512, cuts most results off before they say anything.
	traceMaxText = 4000
	// traceMaxMessage bounds each message in a recorded request. A file a
	// tool read is in the history of every later round; whole once is
	// enough, and it is whole in that tool's own result line up to
	// traceMaxText.
	traceMaxMessage = 16000
	// traceIdle is how long a conversation's file stays open with nothing
	// written to it.
	traceIdle = 15 * time.Minute
)

// TracesDir is where per-task JSONL traces live: <home>/data/traces.
func TracesDir() string { return filepath.Join(DataDir(), "data", "traces") }

// TraceStore is an Observer that writes one JSONL trace per task.
type TraceStore struct {
	agent.BaseObserver

	dir string

	mu       sync.Mutex
	open     map[string]*taskTrace // tasks, between Begin and Finish
	sessions map[string]*taskTrace // conversations, opened on their first event
}

// taskTrace is one open file and the writer over it.
type taskTrace struct {
	f    *os.File
	w    *agent.TraceWriter
	used time.Time
}

// NewTraceStore returns a store writing into dir. The directory is created on
// first use, so a store nobody ever traces through leaves nothing behind.
func NewTraceStore(dir string) *TraceStore {
	return &TraceStore{dir: dir, open: map[string]*taskTrace{}, sessions: map[string]*taskTrace{}}
}

// openTrace opens (or appends to) the file for id. Caller holds t.mu.
func (t *TraceStore) openTrace(id string) *taskTrace {
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return nil
	}
	t.pruneLocked(id)
	f, err := os.OpenFile(filepath.Join(t.dir, id+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	size := int64(0)
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	w := agent.NewTraceWriter(&capWriter{w: f, n: size, max: traceMaxBytes})
	w.MaxTextLen = traceMaxText
	w.MaxPayloadLen = traceMaxMessage
	return &taskTrace{f: f, w: w, used: time.Now()}
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
	if tt := t.openTrace(taskID); tt != nil {
		t.open[taskID] = tt
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
	open, sessions := t.open, t.sessions
	t.open, t.sessions = map[string]*taskTrace{}, map[string]*taskTrace{}
	t.mu.Unlock()
	for _, tt := range open {
		_ = tt.f.Close()
	}
	for _, tt := range sessions {
		_ = tt.f.Close()
	}
	return nil
}

// writer returns where an event goes: its task's file when the task was
// begun, else its conversation's, opened on first use. Nil when it has
// neither, which a run made outside any session is.
func (t *TraceStore) writer(taskID, sessionID string) *agent.TraceWriter {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if tt := t.open[taskID]; taskID != "" && tt != nil {
		return tt.w
	}
	if !validTraceID(sessionID) {
		return nil
	}
	now := time.Now()
	if tt := t.sessions[sessionID]; tt != nil {
		tt.used = now
		return tt.w
	}
	// A conversation is open for as long as it is talked to. Opening one is
	// the moment to let go of those nobody has written to in a while.
	for id, tt := range t.sessions {
		if now.Sub(tt.used) > traceIdle {
			_ = tt.f.Close()
			delete(t.sessions, id)
		}
	}
	tt := t.openTrace(sessionID)
	if tt == nil {
		return nil
	}
	t.sessions[sessionID] = tt
	return tt.w
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
		size int64
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
		if _, live := t.sessions[id]; live {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, aged{filepath.Join(t.dir, e.Name()), info.ModTime().UnixNano(), info.Size()})
	}
	// The open traces and the incoming one hold slots of their own.
	budget := traceKeepFiles - len(t.open) - len(t.sessions) - 1
	if budget < 0 {
		budget = 0
	}
	// Newest first; keep while both the count and the bytes allow.
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod })
	var bytes int64
	for i, f := range files {
		bytes += f.size
		if i >= budget || bytes > traceKeepBytes {
			_ = os.Remove(f.path)
		}
	}
}

// --- Observer: every callback routed to its task's or conversation's writer ---

func (t *TraceStore) OnModelStart(ctx context.Context, info agent.ModelInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnModelStart(ctx, info)
	}
}

func (t *TraceStore) OnModelEnd(ctx context.Context, info agent.ModelInfo, res *agent.ModelResult, err error) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnModelEnd(ctx, info, res, err)
	}
}

func (t *TraceStore) OnToolStart(ctx context.Context, info agent.ToolInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnToolStart(ctx, info)
	}
}

func (t *TraceStore) OnToolEnd(ctx context.Context, info agent.ToolInfo, result any, err error) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnToolEnd(ctx, info, result, err)
	}
}

func (t *TraceStore) OnSubAgentStart(ctx context.Context, info agent.SubAgentInfo) {
	if w := t.writer(info.ParentTaskID, info.SessionID); w != nil {
		w.OnSubAgentStart(ctx, info)
	}
}

func (t *TraceStore) OnSubAgentEnd(ctx context.Context, info agent.SubAgentInfo, result any, err error) {
	if w := t.writer(info.ParentTaskID, info.SessionID); w != nil {
		w.OnSubAgentEnd(ctx, info, result, err)
	}
}

func (t *TraceStore) OnCheckpoint(ctx context.Context, info agent.CheckpointInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnCheckpoint(ctx, info)
	}
}

func (t *TraceStore) OnLint(ctx context.Context, info agent.LintInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnLint(ctx, info)
	}
}

func (t *TraceStore) OnModelRetry(ctx context.Context, info agent.ModelRetryInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnModelRetry(ctx, info)
	}
}

func (t *TraceStore) OnCompaction(ctx context.Context, info agent.CompactionInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnCompaction(ctx, info)
	}
}

func (t *TraceStore) OnError(ctx context.Context, info agent.ErrorInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
		w.OnError(ctx, info)
	}
}

func (t *TraceStore) OnSegment(ctx context.Context, info agent.SegmentInfo) {
	if w := t.writer(info.TaskID, info.SessionID); w != nil {
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
	for _, l := range lines {
		out = append(out, withoutRequest(l))
	}
	return out
}

// withoutRequest replaces a model_end line's request, the whole conversation
// up to that call, with its size. A list of a long run would otherwise carry
// that conversation once per round; TraceCall fetches one in full.
func withoutRequest(line string) string {
	if !strings.Contains(line, `"request":`) {
		return line
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &m) != nil {
		return line
	}
	var req struct {
		Prompt   string            `json:"prompt"`
		Messages []json.RawMessage `json:"messages"`
		Tools    []string          `json:"tools"`
	}
	_ = json.Unmarshal(m["request"], &req)
	sum, _ := json.Marshal(map[string]int{
		"messages": len(req.Messages),
		"tools":    len(req.Tools),
		"bytes":    len(m["request"]),
	})
	m["request"] = sum
	b, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return string(b)
}

// TraceCall returns one model call's model_end line whole, request included:
// the one with that span id, latest first. Empty when there is none.
func TraceCall(dir, id, spanID string) string {
	if !validTraceID(id) || spanID == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		return ""
	}
	want := `"span_id":` + strconv.Quote(spanID)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], want) && strings.Contains(lines[i], `"event":"model_end"`) {
			return lines[i]
		}
	}
	return ""
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
