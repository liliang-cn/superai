package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/trace"
)

// Flows: one trace id for one thing somebody asked for.
//
// agent-go makes every run, model call and tool call a span of a W3C trace
// and carries the ids on the context. What it cannot know is where a request
// entered SuperLeo — a chat turn typed in the window, a phone's question, an
// order from the queen, a coding run — so each entry point opens a flow here
// (StartFlow) and every run started under it joins that flow.
//
// The flow's root is written as a run_start / run_end pair in the same JSONL
// the TraceWriter writes, with agent "superleo.<kind>", so agentgo-trace
// draws the entry point at the top of the tree:
//
//	agentgo-trace ~/.superleo/data/flows --trace <id>
//
// Every run of every Service in the process is traced into one file per
// component under <home>/data/flows, rotated by size. That is separate from
// the per-conversation traces in data/traces (trace.go), which keep the whole
// request of every model call for the trace panel; these keep the flow.

const (
	flowFileMax  = 32 << 20
	flowFileKeep = 6
	// flowReadMax bounds how much of the flow files one FlowLines reads.
	flowReadMax = 96 << 20
	// flowPayloadLen bounds each message of a recorded request: the flow is
	// for the shape of a request, the conversation file has it whole.
	flowPayloadLen = 400
)

// FlowEventType is the agent event a turn emits first, with the traceparent
// of its flow as Content, so whoever streams the turn can show and link it.
const FlowEventType agent.EventType = "flow"

// FlowsDir is where the flow traces live: <home>/data/flows.
func FlowsDir() string { return filepath.Join(DataDir(), "data", "flows") }

var (
	flowComponentMu sync.Mutex
	flowComponent   = "superleo"
)

// SetFlowComponent names the file this process writes its flows to.
func SetFlowComponent(name string) {
	name = strings.TrimSpace(name)
	if name == "" || !validTraceID(name) {
		return
	}
	flowComponentMu.Lock()
	flowComponent = name
	flowComponentMu.Unlock()
}

// flowFile is the process's flow writer. It resolves its path on every
// write, so a test that moves SUPERLEO_HOME (or a process that is told its
// home late) writes where the home now is.
type flowFile struct {
	mu   sync.Mutex
	path string
	r    *rotatingFile
}

func (f *flowFile) Write(p []byte) (int, error) {
	flowComponentMu.Lock()
	name := flowComponent
	flowComponentMu.Unlock()
	path := filepath.Join(FlowsDir(), "flows-"+name+".jsonl")
	f.mu.Lock()
	if f.r == nil || f.path != path {
		if f.r != nil {
			_ = f.r.Close()
		}
		f.r, f.path = nil, path
		if r, err := openRotating(path, flowFileMax, flowFileKeep); err == nil {
			f.r = r
		}
	}
	r := f.r
	f.mu.Unlock()
	if r == nil {
		return len(p), nil
	}
	return r.Write(p)
}

var (
	flowOut       = &flowFile{}
	flowTraceOnce sync.Once
	flowTrace     *agent.TraceWriter
)

// FlowTraceWriter is the observer every Service registers: one TraceWriter
// for the process, so runs of rebuilt services land in the same file.
func FlowTraceWriter() *agent.TraceWriter {
	flowTraceOnce.Do(func() {
		flowTrace = agent.NewTraceWriter(flowOut)
		flowTrace.MaxPayloadLen = flowPayloadLen
	})
	return flowTrace
}

// flowLine is a line SuperLeo writes into the flow file itself, in the
// TraceWriter's schema: a flow's root, a peer it handed work to, a call to
// CortexDB.
type flowLine struct {
	TS           string   `json:"ts"`
	Event        string   `json:"event"`
	TraceID      string   `json:"trace_id,omitempty"`
	SpanID       string   `json:"span_id,omitempty"`
	ParentSpanID string   `json:"parent_span_id,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	CallID       string   `json:"call_id,omitempty"`
	ElapsedMs    int64    `json:"elapsed_ms"`
	DurationMs   int64    `json:"duration_ms,omitempty"`
	Goal         string   `json:"goal,omitempty"`
	Host         string   `json:"host,omitempty"`
	PID          int      `json:"pid,omitempty"`
	Outcome      string   `json:"outcome,omitempty"`
	Tool         string   `json:"tool,omitempty"`
	Error        string   `json:"error,omitempty"`
	Peer         string   `json:"peer,omitempty"`
	Remote       []string `json:"remote,omitempty"`
}

func writeFlowLine(l flowLine) {
	if l.TS == "" {
		l.TS = time.Now().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	_, _ = flowOut.Write(append(b, '\n'))
}

var flowHost = func() string { h, _ := os.Hostname(); return h }()

// Flow is one entry point's root span (or, from StartSpan, a span inside a
// flow that is SuperLeo's own: a coding run, a peer's order).
type Flow struct {
	sc      trace.SpanContext
	kind    string
	session string
	started time.Time
	owned   bool
	once    sync.Once
}

type flowKey struct{}

// FlowFrom is the flow ctx is in, if an entry point opened one.
func FlowFrom(ctx context.Context) *Flow {
	if ctx == nil {
		return nil
	}
	f, _ := ctx.Value(flowKey{}).(*Flow)
	return f
}

// StartFlow opens the flow of an entry point of kind ("chat", "device",
// "hive.order", "schedule", …), label being what was asked. When ctx is
// already in a flow an entry point opened, that flow is returned unowned —
// its End does nothing — so an entry point inside another (a dashboard
// refresh started by a schedule) does not split the request in two. When ctx
// carries a remote span (traceparent from a caller), the root is its child.
func StartFlow(ctx context.Context, kind, label string) (context.Context, *Flow) {
	if f := FlowFrom(ctx); f != nil {
		return ctx, &Flow{sc: f.sc, kind: f.kind}
	}
	return startFlowSpan(ctx, kind, label, trace.SpanContext{})
}

// StartSpan opens a span of SuperLeo's own inside whatever flow ctx is in
// (or a flow of its own when it is in none): a coding agent's run, an order
// handed to a peer.
func StartSpan(ctx context.Context, kind, label string) (context.Context, *Flow) {
	return startFlowSpan(ctx, kind, label, trace.SpanContext{})
}

func startFlowSpan(ctx context.Context, kind, label string, preset trace.SpanContext) (context.Context, *Flow) {
	if ctx == nil {
		ctx = context.Background()
	}
	var sc trace.SpanContext
	if preset.Valid() {
		sc = preset
		ctx = trace.ContextWithSpan(ctx, sc)
	} else {
		ctx, sc = trace.StartSpan(ctx)
	}
	f := &Flow{sc: sc, kind: kind, session: ChatSessionFrom(ctx), started: time.Now(), owned: true}
	ctx = context.WithValue(ctx, flowKey{}, f)
	writeFlowLine(flowLine{Event: "run_start", TraceID: sc.TraceID, SpanID: sc.SpanID, ParentSpanID: sc.ParentSpanID,
		SessionID: f.session, Agent: "superleo." + kind, Goal: clipFlow(label, 200), Host: flowHost, PID: os.Getpid()})
	Logger("flow").InfoContext(ctx, "flow start", "kind", kind, "session", f.session)
	return ctx, f
}

// End closes the span: outcome is completed, failed or cancelled. Safe on
// nil and on an unowned flow, and only the first call counts.
func (f *Flow) End(outcome string, err error) {
	if f == nil || !f.owned {
		return
	}
	f.once.Do(func() {
		if outcome == "" {
			outcome = "completed"
			if err != nil {
				outcome = "failed"
			}
		}
		l := flowLine{Event: "run_end", TraceID: f.sc.TraceID, SpanID: f.sc.SpanID, ParentSpanID: f.sc.ParentSpanID,
			SessionID: f.session, Agent: "superleo." + f.kind, Host: flowHost, PID: os.Getpid(),
			Outcome: outcome, DurationMs: time.Since(f.started).Milliseconds()}
		if err != nil {
			l.Error = clipFlow(err.Error(), 400)
		}
		writeFlowLine(l)
		ctx := trace.ContextWithSpan(context.Background(), f.sc)
		args := []any{"kind", f.kind, "outcome", outcome, "ms", l.DurationMs}
		if err != nil {
			args = append(args, "error", l.Error)
			Logger("flow").WarnContext(ctx, "flow end", args...)
			return
		}
		Logger("flow").InfoContext(ctx, "flow end", args...)
	})
}

// TraceID is the flow's trace id, "" on nil.
func (f *Flow) TraceID() string {
	if f == nil {
		return ""
	}
	return f.sc.TraceID
}

// Traceparent names the flow's span, for a caller to make itself a child of.
func (f *Flow) Traceparent() string {
	if f == nil {
		return ""
	}
	return f.sc.Traceparent()
}

func clipFlow(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// --- a flow arriving from elsewhere ---
//
// A turn asked for over /api/rpc or the agent link starts on a goroutine of
// its own, long after the call that asked for it returned — and the method
// it is asked through (SendChat) has no context to carry the caller's span.
// So the call's traceparent is accepted against the conversation it names:
// the span the turn will run as is minted at once, answered to the caller
// (which records it as its child), and taken by the turn when it starts.

type acceptedFlow struct {
	sc   trace.SpanContext
	kind string
	at   time.Time
}

var (
	acceptedMu sync.Mutex
	accepted   = map[string]acceptedFlow{}
)

const acceptedTTL = 2 * time.Minute

// AcceptFlow notes that the next turn in sessionID was asked for by kind
// ("device", "hive.order", …) under traceparent (either may be empty), and
// returns the traceparent of the span that turn will be, or "" when there is
// nothing to continue and no kind to say.
func AcceptFlow(sessionID, kind, traceparent, tracestate string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || (kind == "" && traceparent == "") {
		return ""
	}
	ctx := context.Background()
	if traceparent != "" {
		ctx = trace.Extract(ctx, func(k string) string {
			if k == trace.TracestateHeader {
				return tracestate
			}
			return traceparent
		})
	}
	_, sc := trace.StartSpan(ctx)
	now := time.Now()
	acceptedMu.Lock()
	for id, a := range accepted {
		if now.Sub(a.at) > acceptedTTL {
			delete(accepted, id)
		}
	}
	accepted[sessionID] = acceptedFlow{sc: sc, kind: kind, at: now}
	acceptedMu.Unlock()
	return sc.Traceparent()
}

func takeAcceptedFlow(sessionID string) (acceptedFlow, bool) {
	acceptedMu.Lock()
	defer acceptedMu.Unlock()
	a, ok := accepted[sessionID]
	if ok {
		delete(accepted, sessionID)
		if time.Since(a.at) > acceptedTTL {
			return acceptedFlow{}, false
		}
	}
	return a, ok
}

// beginTurnFlow is StreamTurn's entry point: the flow ctx is already in, the
// one a caller handed over for this conversation, or a new one.
func beginTurnFlow(ctx context.Context, sessionID, message string) (context.Context, *Flow) {
	if f := FlowFrom(ctx); f != nil {
		return ctx, &Flow{sc: f.sc, kind: f.kind}
	}
	kind := turnKind(sessionID)
	if a, ok := takeAcceptedFlow(sessionID); ok {
		if a.kind != "" {
			kind = a.kind
		}
		return startFlowSpan(ctx, kind, message, a.sc)
	}
	return startFlowSpan(ctx, kind, message, trace.SpanContext{})
}

// turnKind names the entry point a turn came through by its conversation.
func turnKind(sessionID string) string {
	switch {
	case IsDirectAsk(sessionID):
		return "hive.direct"
	case strings.HasPrefix(sessionID, HiveSessionPrefix):
		return "hive.order"
	case strings.HasPrefix(sessionID, "dashboard"):
		return "dashboard"
	case strings.HasPrefix(sessionID, "schedule"):
		return "schedule"
	}
	return "chat"
}

// --- peers ---
//
// An order handed to a worker runs in the worker's process and is traced in
// the worker's files. The queen notes which peer took which call, so the
// timeline can ask that peer for its half of the flow while it is reachable.

// notePeer records that the call on ctx's span went to peer, answered by the
// remote span tp (may be empty).
func notePeer(ctx context.Context, peer, tp string) {
	sc, ok := trace.SpanFromContext(ctx)
	if !ok || strings.TrimSpace(peer) == "" {
		return
	}
	l := flowLine{Event: "peer", TraceID: sc.TraceID, SpanID: sc.SpanID, ParentSpanID: sc.ParentSpanID, Peer: peer}
	if tp != "" {
		l.Remote = []string{tp}
	}
	writeFlowLine(l)
}

// --- reading a flow back ---

// FlowLines is every line of traceID in this machine's flow files, oldest
// first, with each model call's request cut to its size. Newest files are
// read first and the read is bounded, so a flow from long ago may come back
// partial or empty.
func FlowLines(traceID string) []string {
	traceID = strings.ToLower(strings.TrimSpace(traceID))
	out := []string{}
	if !isHexID(traceID) {
		return out
	}
	entries, err := os.ReadDir(FlowsDir())
	if err != nil {
		return out
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var files []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, cand{filepath.Join(FlowsDir(), e.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	want := `"trace_id":"` + traceID + `"`
	type stamped struct {
		ts   string
		line string
	}
	var got []stamped
	var read int64
	for _, f := range files {
		if read > flowReadMax {
			break
		}
		fh, err := os.Open(f.path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
		for sc.Scan() {
			line := sc.Text()
			read += int64(len(line))
			if !strings.Contains(line, want) {
				continue
			}
			got = append(got, stamped{ts: lineTS(line), line: withoutRequest(line)})
		}
		fh.Close()
	}
	sort.SliceStable(got, func(i, j int) bool { return got[i].ts < got[j].ts })
	for _, g := range got {
		out = append(out, g.line)
	}
	return out
}

// FlowTrace is the whole flow as far as it can be had: this machine's lines,
// and those of each peer the flow handed work to, asked through fetch while
// ctx lasts.
// A peer that cannot be reached is skipped; its calls still show here, as
// the calls the queen made.
func FlowTrace(ctx context.Context, traceID string, fetch func(ctx context.Context, peer, traceID string) ([]string, error)) []string {
	traceID = strings.ToLower(strings.TrimSpace(traceID))
	local := FlowLines(traceID)
	if fetch == nil {
		return local
	}
	peers := map[string]bool{}
	for _, l := range local {
		if !strings.Contains(l, `"event":"peer"`) {
			continue
		}
		var p flowLine
		if json.Unmarshal([]byte(l), &p) == nil && p.Peer != "" {
			peers[p.Peer] = true
		}
	}
	if len(peers) == 0 {
		return local
	}
	seen := make(map[string]bool, len(local))
	for _, l := range local {
		seen[l] = true
	}
	type stamped struct {
		ts   string
		line string
	}
	all := make([]stamped, 0, len(local))
	for _, l := range local {
		all = append(all, stamped{lineTS(l), l})
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for p := range peers {
		wg.Add(1)
		go func(peer string) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			lines, err := fetch(pctx, peer, traceID)
			if err != nil {
				Logger("flow").DebugContext(ctx, "peer flow unavailable", "peer", peer, "error", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, l := range lines {
				if seen[l] || !strings.Contains(l, traceID) {
					continue
				}
				seen[l] = true
				all = append(all, stamped{lineTS(l), l})
			}
		}(p)
	}
	wg.Wait()
	sort.SliceStable(all, func(i, j int) bool { return all[i].ts < all[j].ts })
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, s.line)
	}
	return out
}

// lineTS pulls a line's ts out without decoding the rest: RFC 3339 with
// nanoseconds, which sorts as text within one zone.
func lineTS(line string) string {
	i := strings.Index(line, `"ts":"`)
	if i < 0 {
		return ""
	}
	rest := line[i+6:]
	if j := strings.IndexByte(rest, '"'); j >= 0 {
		if t, err := time.Parse(time.RFC3339Nano, rest[:j]); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05.000000000")
		}
		return rest[:j]
	}
	return ""
}

func isHexID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
