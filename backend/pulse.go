package backend

import (
	"context"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// The pulse.
//
// The run wall next door narrates one long task in detail. This is the other
// question, the one a person asks by glancing at a screen rather than reading
// it: is the thing working right now, and how hard. Tokens are being spent and
// tools are being called somewhere in this process at every moment, and until
// this nothing added them up while it happened — the numbers existed only per
// task, and only for tasks that went through LongRunStart.
//
// So the Pulse watches every run the agent makes, chat turns included, and
// keeps two things: a per-second histogram of the last two minutes, and a tail
// of what just happened. Both are bounded and both are cheap to write, because
// these callbacks sit in the hot path of every model turn and every tool call
// and must never be the reason a run is slow.
//
// Nothing here is persisted. A meter is about now; the durable record of what
// a run did is the checkpoint store, and the durable record of what it cost is
// the usage store.

const (
	// pulseWindow is how many seconds of histogram are kept. Two minutes is
	// long enough to see the shape of a turn — a burst of tool calls, a long
	// quiet model turn, another burst — and short enough that the bars stay
	// wide enough to read on a phone.
	pulseWindow = 120
	// pulseTail is how many recent events are kept for the ticker.
	pulseTail = 90
	// pulseIdle is how long a run may say nothing before it stops being
	// listed as in flight. There is no OnRunEnd to key this off: the Observer
	// brackets model turns and tool calls, not runs, so a finished run is one
	// that stopped producing either.
	pulseIdle = 45 * time.Second
	// pulseThinkTail bounds the reasoning buffer per run, in bytes. Deltas
	// arrive a token at a time and a long turn reasons for thousands of them;
	// this is a window on the writing, not a copy of it.
	pulseThinkTail = 400
	// pulseThinkEvery is the least time between two thought lines from one
	// run. Without it a fast model puts a line on the ticker per token and
	// nothing else on it can be read.
	pulseThinkEvery = 900 * time.Millisecond
)

// PulseBin is one second of activity.
type PulseBin struct {
	// Sec is the Unix second, so a client can tell a gap from a zero.
	Sec    int64 `json:"sec"`
	Tokens int   `json:"tokens"`
	Cached int   `json:"cached"`
	Calls  int   `json:"calls"`
	Rounds int   `json:"rounds"`
	// Reads, Writes and Shells split Calls by what the tool does to the
	// world: looked at a file, changed one, or ran a command. Judged from
	// the name, which is the only thing the observer is given — see
	// toolVerb for what counts as which.
	Reads  int `json:"reads"`
	Writes int `json:"writes"`
	Shells int `json:"shells"`
	// Fails is tool calls that returned an error this second.
	Fails int `json:"fails"`
	// MCP is calls that went to an MCP server, and Memory is calls the model
	// made to the memory tools — recall, remember, knowledge. The recall the
	// runtime does on its own before each turn is not a tool call and the
	// Observer does not see it; counting it needs a hook agent-go does not
	// have yet.
	MCP    int `json:"mcp"`
	Memory int `json:"memory"`
	// Think is bytes of reasoning written this second. Not tokens: the deltas
	// arrive as text and counting runes per fragment would cost more than the
	// number is worth. It is an intensity, and it is read as one.
	Think int `json:"think"`
	// CPU is this process's share of one core over the second, in percent —
	// user and system time together, from getrusage, so a tool that shells
	// out is counted only while the shell is ours. 0 for a second nobody
	// sampled; the sample is what tells the two apart.
	CPU float64 `json:"cpu"`
	// Heap is the process heap in bytes at this second, or 0 for a second
	// nobody sampled. Unlike everything else here it is a level rather than a
	// count, and it is sampled by Snapshot rather than by a callback — the
	// runtime is not going to tell us when it allocates, and a meter is only
	// worth reading while someone is reading it.
	Heap uint64 `json:"heap"`
}

// PulseEvent is one line of the ticker.
type PulseEvent struct {
	// Seq is this event's place in the process's whole stream, so a client
	// that has been away knows which lines it has not seen. The tail is
	// capped; the counter is not.
	Seq int64  `json:"seq"`
	At  string `json:"at"`
	// Kind is "tool", "model", "think", "error" or "compact".
	Kind string `json:"kind"`
	Name string `json:"name"`
	Text string `json:"text"`
	// N is tokens for a model turn, and unset otherwise.
	N   int   `json:"n,omitempty"`
	Ms  int64 `json:"ms,omitempty"`
	Bad bool  `json:"bad,omitempty"`
	// Inner marks a tool a sub-agent called rather than the top-level loop.
	Inner bool `json:"inner,omitempty"`
}

// PulseTool is one tool's share of the traffic.
type PulseTool struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
	LastAt string `json:"lastAt"`
}

// PulseRun is a run with something open right now.
type PulseRun struct {
	RunID   string `json:"runId"`
	Model   string `json:"model"`
	Round   int    `json:"round"`
	Session string `json:"session,omitempty"`
	// Doing is "thinking" while a model span is open, the tool's name while a
	// tool span is, and "waiting" between the two.
	Doing string `json:"doing"`
	// Since is when Doing started, so the page can run its own clock rather
	// than being told the elapsed time sixty times a minute.
	Since string `json:"since"`
	Seen  string `json:"seen"`
	// Think is the reasoning being written right now — the tail of it, not
	// the whole thing. What a person wants from a page like this is the
	// sentence in progress, and the transcript already keeps the rest.
	Think string `json:"think,omitempty"`
}

// PulseSnapshot is everything the meter knows, at one instant.
type PulseSnapshot struct {
	Now   string `json:"now"`
	Since string `json:"since"`
	// Live is true when at least one model turn or tool call is open.
	Live bool       `json:"live"`
	Runs []PulseRun `json:"runs"`
	// Bins is exactly pulseWindow entries, oldest first, ending at Now.
	Bins   []PulseBin   `json:"bins"`
	Tools  []PulseTool  `json:"tools"`
	Events []PulseEvent `json:"events"`

	Tokens int `json:"tokens"`
	Cached int `json:"cached"`
	Rounds int `json:"rounds"`
	Calls  int `json:"calls"`
	Reads  int `json:"reads"`
	Writes int `json:"writes"`
	Shells int `json:"shells"`
	// Fails is tool calls that errored; Errors is everything that did,
	// model turns and runtime included.
	Fails  int `json:"fails"`
	MCP    int `json:"mcp"`
	Memory int `json:"memory"`
	Errors int `json:"errors"`
	// Peak is the busiest second in the window, which is what the histogram
	// scales against — a fixed ceiling would flatten a quiet minute to nothing
	// and clip a busy one.
	Peak int `json:"peak"`
	// PeakThink is the same, for the reasoning band.
	PeakThink int `json:"peakThink"`
	// Heap, HeapLow and HeapHigh are the process heap now and the range over
	// the window, because a level drawn against zero is a flat line and a
	// level drawn against its own range is a reading.
	Heap     uint64 `json:"heap"`
	HeapLow  uint64 `json:"heapLow"`
	HeapHigh uint64 `json:"heapHigh"`
	// CPU is the latest sample, in percent of one core.
	CPU float64 `json:"cpu"`
	// Goroutines is the other number that says how busy the process is in a
	// way tokens cannot: a run that has stalled on forty open calls looks
	// idle by every other measure here.
	Goroutines int `json:"goroutines"`
}

// PulseFrame is what is pushed, as opposed to what is fetched.
//
// A snapshot is the whole window; a frame is only what changed since the last
// one — the second in progress and the one before it (in case the clock
// rolled over between frames), the totals, the runs, the tools and the events
// not yet sent. A client keeps the window and folds frames into it. Four or
// five of these a second cost a few kilobytes; four or five snapshots would
// cost a hundred.
type PulseFrame struct {
	At    string      `json:"at"`
	Live  bool        `json:"live"`
	Runs  []PulseRun  `json:"runs"`
	Bins  []PulseBin  `json:"bins"`
	Tools []PulseTool `json:"tools"`
	// New is every event with Seq above what the client last saw. Frames go
	// to every listener at once, so "last saw" is the meter's own high-water
	// mark from the previous frame; a client that joined late resyncs with a
	// snapshot, which carries the whole tail.
	New        []PulseEvent `json:"new"`
	Tokens     int          `json:"tokens"`
	Cached     int          `json:"cached"`
	Rounds     int          `json:"rounds"`
	Calls      int          `json:"calls"`
	Reads      int          `json:"reads"`
	Writes     int          `json:"writes"`
	Shells     int          `json:"shells"`
	Fails      int          `json:"fails"`
	MCP        int          `json:"mcp"`
	Memory     int          `json:"memory"`
	Errors     int          `json:"errors"`
	Heap       uint64       `json:"heap"`
	CPU        float64      `json:"cpu"`
	Goroutines int          `json:"goroutines"`
}

// pulseSlot is one ring entry. Sec identifies which second it holds, so a slot
// the ring has lapped past reads as empty rather than as old traffic.
type pulseSlot struct {
	sec    int64
	tokens int
	cached int
	calls  int
	rounds int
	think  int
	heap   uint64
	cpu    float64
	reads  int
	writes int
	shells int
	fails  int
	mcp    int
	memory int
}

type pulseRun struct {
	model   string
	session string
	round   int
	doing   string
	since   time.Time
	seen    time.Time
	// think is the reasoning buffer, in bytes rather than a string because
	// this is appended to once per token and a string would copy the whole
	// thing every time.
	think []byte
	// said is where the buffer stood when the last thought line was cut, and
	// saidAt when. Together they are what stops the ticker being a token
	// stream.
	said   int
	saidAt time.Time
}

// Pulse implements agent.Observer and aggregates every run at once.
type Pulse struct {
	agent.BaseObserver

	mu    sync.Mutex
	start time.Time
	ring  [pulseWindow]pulseSlot
	runs  map[string]*pulseRun
	// spans maps a model span to the run it belongs to. ModelDelta carries a
	// SpanID and nothing else, so without this a reasoning fragment has no
	// way home.
	spans map[string]string
	tools map[string]*PulseTool
	tail  []PulseEvent
	// seq counts every event ever noted, capped tail or not.
	seq int64
	// dirty says something has happened since the last frame went out. A
	// meter with nothing to report should not be sending frames.
	dirty bool
	// sent is the Seq of the last event that went out in a frame.
	sent int64
	// heap, cpu and goroutines are the last samples the ticker took; cpuAt
	// and cpuTime are what the next CPU sample is a delta against.
	heap       uint64
	cpu        float64
	goroutines int
	cpuAt      time.Time
	cpuTime    time.Duration
	// open counts spans currently bracketed, which is what makes the light
	// green. Model and tool are counted apart so a tool that never returns
	// cannot make a run look like it is thinking.
	openModel int
	openTool  int

	tokens int
	cached int
	rounds int
	calls  int
	reads  int
	writes int
	shells int
	fails  int
	mcp    int
	memory int
	errors int
}

// NewPulse returns a meter that starts counting now.
func NewPulse() *Pulse {
	return &Pulse{
		start: time.Now(),
		runs:  map[string]*pulseRun{},
		spans: map[string]string{},
		tools: map[string]*PulseTool{},
	}
}

// bin returns the slot for a moment, resetting it if the ring has lapped.
// Caller holds p.mu.
func (p *Pulse) bin(at time.Time) *pulseSlot {
	sec := at.Unix()
	s := &p.ring[((sec%pulseWindow)+pulseWindow)%pulseWindow]
	if s.sec != sec {
		*s = pulseSlot{sec: sec}
	}
	return s
}

// note appends to the ticker tail. Caller holds p.mu.
func (p *Pulse) note(e PulseEvent) {
	p.seq++
	e.Seq = p.seq
	p.dirty = true
	p.tail = append(p.tail, e)
	if len(p.tail) > pulseTail {
		p.tail = p.tail[len(p.tail)-pulseTail:]
	}
}

// run returns the tracked run for an id, creating it. Caller holds p.mu.
//
// The key is the RunID rather than the TaskID: two chat turns in the same
// session share a task id and would otherwise be one row that flickered
// between them.
func (p *Pulse) run(id string, now time.Time) *pulseRun {
	r, ok := p.runs[id]
	if !ok {
		r = &pulseRun{since: now}
		p.runs[id] = r
	}
	r.seen = now
	return r
}

// sweep forgets runs that have gone quiet. Caller holds p.mu.
func (p *Pulse) sweep(now time.Time) {
	for id, r := range p.runs {
		if now.Sub(r.seen) > pulseIdle {
			delete(p.runs, id)
			for span, run := range p.spans {
				if run == id {
					delete(p.spans, span)
				}
			}
		}
	}
}

// toolVerb says what a tool does to the world, from its name.
//
// The observer is handed a name and arguments and nothing else, and the
// arguments are the tool's own business. Names are a good enough witness: the
// built-ins are read_file / write_file / glob / grep / bash, the filesystem
// MCP is mcp_filesystem_read_file and so on, and a server prefix is just that
// — stripped before looking. Anything unrecognised is a call and nothing more,
// which is the honest answer for a stock quote.
func toolVerb(name string) string {
	n := strings.ToLower(name)
	if strings.HasPrefix(n, "mcp_") {
		// mcp_<server>_<tool>: the server's name is not the verb.
		if i := strings.Index(n[4:], "_"); i >= 0 {
			n = n[4+i+1:]
		}
	}
	switch {
	case n == "bash" || n == "shell" || n == "exec" || strings.HasPrefix(n, "run_") || strings.Contains(n, "command"):
		return "shell"
	case strings.Contains(n, "write") || strings.Contains(n, "edit") || strings.Contains(n, "create") ||
		strings.Contains(n, "delete") || strings.Contains(n, "remove") || strings.Contains(n, "move") ||
		strings.Contains(n, "rename") || strings.Contains(n, "append") || strings.Contains(n, "mkdir"):
		return "write"
	case strings.Contains(n, "read") || strings.Contains(n, "list") || strings.Contains(n, "glob") ||
		strings.Contains(n, "grep") || strings.Contains(n, "search_files") || strings.Contains(n, "directory") ||
		strings.Contains(n, "stat") || strings.Contains(n, "find") || strings.Contains(n, "cat"):
		return "read"
	}
	return ""
}

// isMemoryTool says whether a tool is one of the brain's. The names come from
// the memory tools agent-go registers and the CortexDB MCP: memory_*,
// knowledge_*, *recall*, *remember*.
func isMemoryTool(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "memory") || strings.Contains(n, "knowledge") ||
		strings.Contains(n, "recall") || strings.Contains(n, "remember") || strings.Contains(n, "cortex")
}

// cutThought turns the reasoning buffer into a ticker line, if enough has been
// written and enough time has passed. Caller holds p.mu.
//
// force is the end of a turn, where whatever is left is the last thing it
// thought and the throttle no longer matters.
func (p *Pulse) cutThought(r *pulseRun, now time.Time, force bool) {
	if r.said > len(r.think) {
		r.said = 0
	}
	fresh := strings.TrimSpace(string(r.think[r.said:]))
	if fresh == "" {
		return
	}
	if !force {
		if now.Sub(r.saidAt) < pulseThinkEvery {
			return
		}
		// A fragment that is not a sentence yet is not worth a line; waiting
		// for one costs nothing, because the next delta is milliseconds away.
		if len(fresh) < 24 && !strings.ContainsAny(fresh, ".。!?！？\n") {
			return
		}
	}
	r.said = len(r.think)
	r.saidAt = now
	p.note(PulseEvent{
		At: now.Format(time.RFC3339Nano), Kind: "think", Name: r.model,
		Text: oneLine(fresh, 180),
	})
}

// --- Observer ---

func (p *Pulse) OnModelStart(_ context.Context, info agent.ModelInfo) {
	now := time.Now()
	p.mu.Lock()
	r := p.run(info.RunID, now)
	r.model = info.Model
	r.session = info.SessionID
	r.round = info.Round
	r.doing = "thinking"
	r.since = now
	r.think = r.think[:0]
	r.said = 0
	p.spans[info.SpanID] = info.RunID
	p.openModel++
	p.mu.Unlock()
}

func (p *Pulse) OnModelEnd(_ context.Context, info agent.ModelInfo, res *agent.ModelResult, err error) {
	now := time.Now()
	p.mu.Lock()
	if p.openModel > 0 {
		p.openModel--
	}
	delete(p.spans, info.SpanID)
	r := p.run(info.RunID, now)
	r.model = info.Model
	r.round = info.Round
	r.doing = "waiting"
	r.since = now
	// Whatever was left in the buffer is the last thing it thought, and
	// dropping it would lose the end of every turn's reasoning.
	p.cutThought(r, now, true)
	r.think = r.think[:0]
	r.said = 0
	s := p.bin(now)
	s.rounds++
	p.rounds++
	if res != nil {
		s.tokens += res.TokensUsed
		s.cached += res.CachedTokens
		p.tokens += res.TokensUsed
		p.cached += res.CachedTokens
		p.note(PulseEvent{
			At: now.Format(time.RFC3339Nano), Kind: "model",
			Name: info.Model, N: res.TokensUsed, Ms: res.DurationMs,
			Text: "round " + itoa(info.Round),
		})
	}
	if err != nil {
		p.errors++
		p.note(PulseEvent{
			At: now.Format(time.RFC3339Nano), Kind: "error", Name: info.Model,
			Text: oneLine(err.Error(), 140), Bad: true,
		})
	}
	p.sweep(now)
	p.mu.Unlock()
}

// OnModelDelta collects the reasoning as it is written.
//
// This is the hottest callback in the interface — once per streamed fragment,
// which for a reasoning model is thousands of times a turn. So it does three
// things and no more: find the run, append bytes to a capped buffer, and once
// in a while cut a line off it. Everything expensive about turning that into
// something readable happens in Snapshot, once a second, on the reader's side.
func (p *Pulse) OnModelDelta(_ context.Context, d agent.ModelDelta) {
	if d.Kind != "reasoning" || d.Text == "" {
		return
	}
	now := time.Now()
	p.mu.Lock()
	runID, ok := p.spans[d.SpanID]
	if !ok {
		p.mu.Unlock()
		return
	}
	r, ok := p.runs[runID]
	if !ok {
		p.mu.Unlock()
		return
	}
	r.seen = now
	p.bin(now).think += len(d.Text)
	r.think = append(r.think, d.Text...)
	// Keep the tail, drop the head. said is an index into the buffer, so it
	// has to travel with it or the next cut repeats what was already said.
	if len(r.think) > pulseThinkTail {
		drop := len(r.think) - pulseThinkTail
		r.think = append(r.think[:0], r.think[drop:]...)
		r.said -= drop
		if r.said < 0 {
			r.said = 0
		}
	}
	p.cutThought(r, now, false)
	p.mu.Unlock()
}

func (p *Pulse) OnToolStart(_ context.Context, info agent.ToolInfo) {
	now := time.Now()
	p.mu.Lock()
	p.openTool++
	p.calls++
	b := p.bin(now)
	b.calls++
	switch toolVerb(info.Tool) {
	case "read":
		b.reads++
		p.reads++
	case "write":
		b.writes++
		p.writes++
	case "shell":
		b.shells++
		p.shells++
	}
	if strings.HasPrefix(strings.ToLower(info.Tool), "mcp_") {
		b.mcp++
		p.mcp++
	}
	if isMemoryTool(info.Tool) {
		b.memory++
		p.memory++
	}
	r := p.run(info.RunID, now)
	r.doing = info.Tool
	r.since = now
	t, ok := p.tools[info.Tool]
	if !ok {
		t = &PulseTool{Name: info.Tool}
		p.tools[info.Tool] = t
	}
	t.Calls++
	t.LastAt = now.Format(time.RFC3339Nano)
	p.note(PulseEvent{
		At: t.LastAt, Kind: "tool", Name: info.Tool,
		Text: formatArgs(info.Args), Inner: info.Inner,
	})
	p.mu.Unlock()
}

func (p *Pulse) OnToolEnd(_ context.Context, info agent.ToolInfo, _ any, err error) {
	now := time.Now()
	p.mu.Lock()
	if p.openTool > 0 {
		p.openTool--
	}
	r := p.run(info.RunID, now)
	if r.doing == info.Tool {
		r.doing = "waiting"
		r.since = now
	}
	if err != nil {
		p.errors++
		p.fails++
		p.bin(now).fails++
		if t, ok := p.tools[info.Tool]; ok {
			t.Errors++
		}
		p.note(PulseEvent{
			At: now.Format(time.RFC3339Nano), Kind: "error", Name: info.Tool,
			Text: oneLine(err.Error(), 140), Bad: true, Inner: info.Inner,
		})
	}
	p.mu.Unlock()
}

func (p *Pulse) OnCompaction(_ context.Context, info agent.CompactionInfo) {
	now := time.Now()
	p.mu.Lock()
	p.note(PulseEvent{
		At: now.Format(time.RFC3339Nano), Kind: "compact", Name: "context",
		Text: itoa(info.MessagesBefore) + " → " + itoa(info.MessagesAfter) + " messages",
	})
	p.mu.Unlock()
}

func (p *Pulse) OnError(_ context.Context, info agent.ErrorInfo) {
	now := time.Now()
	p.mu.Lock()
	p.errors++
	marker := info.Marker
	if marker == "" {
		marker = "error"
	}
	p.note(PulseEvent{
		At: now.Format(time.RFC3339Nano), Kind: "error", Name: marker,
		Text: oneLine(info.Message, 140), Bad: true,
	})
	p.mu.Unlock()
}

// --- Push ---

const (
	// pulseFrameEvery is how often a frame can go out while something is
	// happening. Five a second is faster than any eye needs and slower than
	// a reasoning model streams, which is the point: the stream is folded
	// into frames here rather than on the wire.
	pulseFrameEvery = 200 * time.Millisecond
	// pulseIdleEvery is the beat while nothing is happening — enough that the
	// heap keeps moving and a page that just opened sees the light come on.
	pulseIdleEvery = 2 * time.Second
	// pulseSampleEvery is how often the heap is read. ReadMemStats stops the
	// world for a moment; once a second is a reading, five times is a tax.
	pulseSampleEvery = time.Second
)

// Start pushes frames to emit until ctx ends. Frames go out only when there
// is something in them, and never more often than pulseFrameEvery.
func (p *Pulse) Start(ctx context.Context, emit func(name string, payload map[string]any)) {
	go func() {
		tick := time.NewTicker(pulseFrameEvery)
		defer tick.Stop()
		var lastFrame, lastSample time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				if now.Sub(lastSample) >= pulseSampleEvery {
					lastSample = now
					p.sample(now)
				}
				p.mu.Lock()
				due := p.dirty || p.openModel > 0 || p.openTool > 0 || now.Sub(lastFrame) >= pulseIdleEvery
				if !due {
					p.mu.Unlock()
					continue
				}
				f := p.frameLocked(now)
				p.mu.Unlock()
				lastFrame = now
				emit("pulse:frame", map[string]any{"frame": f})
			}
		}
	}()
}

// sample reads the runtime and records it in the current second.
func (p *Pulse) sample(now time.Time) {
	// Read outside the lock: ReadMemStats stops the world briefly, and holding
	// the mutex every callback wants across that would put the pause in the
	// run rather than in the meter.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	n := runtime.NumGoroutine()
	// CPU is a rate, so it needs two readings: this one and the last. The
	// first sample has no last and reads 0, which is right for a process
	// that has only just started watching itself.
	var ru syscall.Rusage
	var used time.Duration
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		used = time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	}
	p.mu.Lock()
	if !p.cpuAt.IsZero() && used > 0 {
		if wall := now.Sub(p.cpuAt); wall > 0 {
			p.cpu = 100 * float64(used-p.cpuTime) / float64(wall)
			if p.cpu < 0 {
				p.cpu = 0
			}
		}
	}
	p.cpuAt, p.cpuTime = now, used
	p.heap = ms.HeapAlloc
	p.goroutines = n
	b := p.bin(now)
	b.heap = ms.HeapAlloc
	b.cpu = p.cpu
	p.mu.Unlock()
}

// frameLocked builds a frame and marks its contents as sent. Caller holds p.mu.
func (p *Pulse) frameLocked(now time.Time) PulseFrame {
	p.sweep(now)
	f := PulseFrame{
		At:         now.Format(time.RFC3339Nano),
		Live:       p.openModel > 0 || p.openTool > 0,
		Runs:       p.runsLocked(),
		Tools:      p.toolsLocked(),
		New:        []PulseEvent{},
		Tokens:     p.tokens,
		Cached:     p.cached,
		Rounds:     p.rounds,
		Calls:      p.calls,
		Reads:      p.reads,
		Writes:     p.writes,
		Shells:     p.shells,
		Fails:      p.fails,
		MCP:        p.mcp,
		Memory:     p.memory,
		Errors:     p.errors,
		Heap:       p.heap,
		CPU:        p.cpu,
		Goroutines: p.goroutines,
	}
	// This second and the last: a callback that landed at :59.999 is in the
	// previous slot by the time the frame goes out at :00.050.
	for _, sec := range []int64{now.Unix() - 1, now.Unix()} {
		b := PulseBin{Sec: sec}
		if s := &p.ring[((sec%pulseWindow)+pulseWindow)%pulseWindow]; s.sec == sec {
			b.Tokens, b.Cached, b.Calls, b.Rounds = s.tokens, s.cached, s.calls, s.rounds
			b.Think, b.Heap, b.CPU = s.think, s.heap, s.cpu
			b.Reads, b.Writes, b.Shells, b.Fails = s.reads, s.writes, s.shells, s.fails
			b.MCP, b.Memory = s.mcp, s.memory
		}
		f.Bins = append(f.Bins, b)
	}
	for _, e := range p.tail {
		if e.Seq > p.sent {
			f.New = append(f.New, e)
		}
	}
	p.sent = p.seq
	p.dirty = false
	return f
}

// runsLocked lists the runs in flight, newest first. Caller holds p.mu.
func (p *Pulse) runsLocked() []PulseRun {
	out := []PulseRun{}
	for id, r := range p.runs {
		out = append(out, PulseRun{
			RunID: id, Model: r.model, Round: r.round, Session: r.session,
			Doing: r.doing,
			Since: r.since.Format(time.RFC3339Nano),
			Seen:  r.seen.Format(time.RFC3339Nano),
			Think: oneLine(strings.TrimSpace(string(r.think)), 220),
		})
	}
	// Newest first: the run that just did something is the one being watched.
	sort.Slice(out, func(i, j int) bool { return out[i].Seen > out[j].Seen })
	return out
}

// toolsLocked lists the tools, busiest first. Caller holds p.mu.
func (p *Pulse) toolsLocked() []PulseTool {
	out := []PulseTool{}
	for _, t := range p.tools {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// --- Snapshot ---

// Snapshot copies the meter. The histogram is always pulseWindow bars ending
// at this second, gaps filled with zeroes, so a client can draw it without
// knowing how long the process has been quiet. This is what a page asks for
// once, on opening; frames carry it from there.
func (p *Pulse) Snapshot() PulseSnapshot {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweep(now)

	out := PulseSnapshot{
		Now:        now.Format(time.RFC3339Nano),
		Since:      p.start.Format(time.RFC3339Nano),
		Live:       p.openModel > 0 || p.openTool > 0,
		Tokens:     p.tokens,
		Cached:     p.cached,
		Rounds:     p.rounds,
		Calls:      p.calls,
		Reads:      p.reads,
		Writes:     p.writes,
		Shells:     p.shells,
		Fails:      p.fails,
		MCP:        p.mcp,
		Memory:     p.memory,
		Errors:     p.errors,
		Heap:       p.heap,
		CPU:        p.cpu,
		Goroutines: p.goroutines,
		Bins:       make([]PulseBin, 0, pulseWindow),
		Runs:       p.runsLocked(),
		Tools:      p.toolsLocked(),
		Events:     append([]PulseEvent{}, p.tail...),
	}

	end := now.Unix()
	for sec := end - pulseWindow + 1; sec <= end; sec++ {
		b := PulseBin{Sec: sec}
		if s := &p.ring[((sec%pulseWindow)+pulseWindow)%pulseWindow]; s.sec == sec {
			b.Tokens, b.Cached, b.Calls, b.Rounds = s.tokens, s.cached, s.calls, s.rounds
			b.Think, b.Heap, b.CPU = s.think, s.heap, s.cpu
			b.Reads, b.Writes, b.Shells, b.Fails = s.reads, s.writes, s.shells, s.fails
			b.MCP, b.Memory = s.mcp, s.memory
		}
		if b.Tokens > out.Peak {
			out.Peak = b.Tokens
		}
		if b.Think > out.PeakThink {
			out.PeakThink = b.Think
		}
		// A zero heap is a second nobody sampled, not a second with no heap:
		// including it in the range would peg the low at zero and flatten the
		// band to a straight line at the top.
		if b.Heap > 0 {
			if out.HeapLow == 0 || b.Heap < out.HeapLow {
				out.HeapLow = b.Heap
			}
			if b.Heap > out.HeapHigh {
				out.HeapHigh = b.Heap
			}
		}
		out.Bins = append(out.Bins, b)
	}
	return out
}

// itoa is strconv.Itoa without the import, kept here because these callbacks
// format two or three small numbers and nothing else.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
