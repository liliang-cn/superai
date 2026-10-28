package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superleo/internal/backend"
)

// A queen on a scripted model, with workers that are fakes over real HTTP: the
// whole route of a delegated piece — the queen's tool surface, hive_delegate,
// the order on the worker, its tool events back on the queen's turn, the
// report, the transcript and superleo_ask — with nothing in it mocked but the
// two ends.

// llmRequest is the part of a chat completion request the script reads.
type llmRequest struct {
	Tools []string
	// Self is a run the queen makes to do a piece herself.
	Self bool
	// Results is how many tool results the conversation already holds.
	Results int
}

type scriptedLLM struct {
	mu   sync.Mutex
	seen []llmRequest
	// step decides the reply to a request that offers tools: a tool call
	// (name, args) or, with name "", a text answer in args["text"].
	step func(r llmRequest) (string, map[string]any)
}

func (l *scriptedLLM) start(t *testing.T) string {
	t.Helper()
	calls := int64(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.Unmarshal(raw, &body)
		req := llmRequest{Self: strings.Contains(string(raw), "No worker could take this piece")}
		for _, tl := range body.Tools {
			req.Tools = append(req.Tools, tl.Function.Name)
		}
		for _, m := range body.Messages {
			if m.Role == "tool" {
				req.Results++
			}
		}
		msg := map[string]any{"role": "assistant", "content": "ok"}
		finish := "stop"
		if len(req.Tools) > 0 {
			l.mu.Lock()
			l.seen = append(l.seen, req)
			l.mu.Unlock()
			name, args := l.step(req)
			if name == "" {
				msg["content"] = args["text"]
			} else {
				a, _ := json.Marshal(args)
				msg["content"] = ""
				msg["tool_calls"] = []map[string]any{{
					"id": fmt.Sprintf("call_%d", atomic.AddInt64(&calls, 1)), "type": "function",
					"function": map[string]any{"name": name, "arguments": string(a)},
				}}
				finish = "tool_calls"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func (l *scriptedLLM) requests() []llmRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]llmRequest(nil), l.seen...)
}

// stubWorker is a worker's HTTP surface: it takes an order, calls one tool
// (as its event stream says) and answers — or fails, when told to.
type stubWorker struct {
	name   string
	srv    *httptest.Server
	events chan string
	mu     sync.Mutex
	orders []string
	fail   bool
	// hold, when set, is waited on before answering.
	hold chan struct{}
}

func newStubWorker(t *testing.T, name string) *stubWorker {
	w := &stubWorker{name: name, events: make(chan string, 64)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(rw, ": connected\n\n")
		rw.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-w.events:
				fmt.Fprintf(rw, "data: %s\n\n", e)
				rw.(http.Flusher).Flush()
			}
		}
	})
	mux.HandleFunc("/api/rpc/SendChat", func(rw http.ResponseWriter, r *http.Request) {
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		w.mu.Lock()
		w.orders = append(w.orders, args[1].(string))
		n := len(w.orders)
		fail, hold := w.fail, w.hold
		w.mu.Unlock()
		id := fmt.Sprintf("%s-req-%d", name, n)
		go func() {
			time.Sleep(20 * time.Millisecond)
			if fail {
				w.emit("chat:error", map[string]any{"requestId": id, "error": "the model provider is down"})
				return
			}
			w.emit("chat:event", map[string]any{"requestId": id, "type": "tool_call", "tool": "mcp_noted_list_events",
				"args": map[string]any{"from": "2026-10-19", "to": "2026-10-25"}})
			w.emit("chat:event", map[string]any{"requestId": id, "type": "tool_result", "tool": "mcp_noted_list_events",
				"result": map[string]any{"events": []any{map[string]any{"title": "周会"}}}})
			if hold != nil {
				<-hold
			}
			w.emit("chat:done", map[string]any{"requestId": id, "final": "REPORT-FROM-" + name})
		}()
		_ = json.NewEncoder(rw).Encode(id)
	})
	mux.HandleFunc("/api/rpc/CancelChat", func(rw http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(rw).Encode("ok") })
	w.srv = httptest.NewServer(mux)
	t.Cleanup(w.srv.Close)
	return w
}

func (w *stubWorker) emit(name string, payload map[string]any) {
	b, _ := json.Marshal(map[string]any{"name": name, "payload": payload})
	w.events <- string(b)
}

func (w *stubWorker) received() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.orders...)
}

// delegateHive is a queen App on the scripted model, with a work tool of her
// own (a stand-in for noted's) whose calls are counted.
type delegateHive struct {
	a        *App
	log      *eventLog
	llm      *scriptedLLM
	ownCalls atomic.Int64
}

func newDelegateHive(t *testing.T, mode string, step func(r llmRequest) (string, map[string]any)) *delegateHive {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	t.Setenv("SUPERLEO_NO_BROWSER", "1")
	t.Setenv("SUPERLEO_HIVE_DELEGATION", "")
	h := &delegateHive{log: &eventLog{}, llm: &scriptedLLM{step: step}}
	a := NewApp()
	a.emitFn = h.log.record
	a.settings = &backend.Settings{
		LLMBaseURL: h.llm.start(t), LLMKey: "test-key", LLMModel: "test-model",
		WorkspaceDir: home + "/workspace", MaxRounds: 4, Headless: true, DisablePTC: true,
		Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q", Delegation: mode, DelegateTimeoutSeconds: 20},
	}
	a.rebuild()
	if a.svc == nil {
		t.Fatalf("rebuild: %s", a.buildErr)
	}
	t.Cleanup(func() { _ = a.svc.Close() })
	a.svc.Agent().AddToolWithMetadata("mcp_noted_list_events", "List calendar events.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(context.Context, map[string]any) (any, error) {
			h.ownCalls.Add(1)
			return map[string]any{"events": []any{map[string]any{"title": "周会"}}}, nil
		}, agent.ToolMetadata{ReadOnly: true})
	h.a = a
	return h
}

func (h *delegateHive) join(t *testing.T, w *stubWorker, mcp []string) {
	t.Helper()
	if _, err := h.a.hive.Join(backend.HiveHello{Protocol: backend.HiveProtocol, Name: w.name, Role: backend.HiveRoleWorker,
		URL: w.srv.URL, MCP: mcp, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// delegateThen is the queen's script for one delegation: hand out tasks,
// then answer from the report.
func delegateThen(tasks []map[string]any) func(r llmRequest) (string, map[string]any) {
	return func(r llmRequest) (string, map[string]any) {
		switch {
		case r.Self && r.Results == 0:
			return "mcp_noted_list_events", map[string]any{}
		case r.Self:
			return "", map[string]any{"text": "SELF-REPORT: 周会"}
		case r.Results == 0 && slices.Contains(r.Tools, backend.DelegateToolName):
			return backend.DelegateToolName, map[string]any{"tasks": tasks}
		case r.Results == 0:
			return "mcp_noted_list_events", map[string]any{}
		}
		return "", map[string]any{"text": "FINAL: 下周有周会。"}
	}
}

var nextWeek = []map[string]any{{"task": "List the events from 2026-10-19 to 2026-10-25 in noted.", "needs": []string{"noted"}}}

func chatEvents(l *eventLog, typ string) []map[string]any {
	var out []map[string]any
	for _, e := range l.snapshot() {
		if e.name == "chat:event" && e.kind() == typ {
			out = append(out, e.payload)
		}
	}
	return out
}

func TestMaxDelegationHasAWorkerDoTheWork(t *testing.T) {
	h := newDelegateHive(t, "max", delegateThen(nextWeek))
	w1 := newStubWorker(t, "w1")
	h.join(t, w1, []string{"noted"})

	out, err := h.a.ask(context.Background(), askIn{Message: "下周有哪些例会？", TimeoutSeconds: 60}, nil)
	if err != nil || out.Status != "done" || !strings.Contains(out.Answer, "FINAL") {
		t.Fatalf("ask: %+v %v", out, err)
	}
	// The queen's own noted tool was neither offered nor called.
	first := h.llm.requests()[0]
	if slices.Contains(first.Tools, "mcp_noted_list_events") || !slices.Contains(first.Tools, backend.DelegateToolName) {
		t.Fatalf("the queen's tools: %v", first.Tools)
	}
	if n := h.ownCalls.Load(); n != 0 {
		t.Fatalf("the queen called her work tool %d time(s)", n)
	}
	if got := w1.received(); len(got) != 1 || !strings.Contains(got[0], "2026-10-19") {
		t.Fatalf("w1 got %q", got)
	}
	// Who did what: on the stream, in superleo_ask, and in the transcript.
	var attributed bool
	for _, p := range chatEvents(h.log, "tool_call") {
		if p["tool"] == "w1 · mcp_noted_list_events" && p["worker"] == "w1" && p["workerTool"] == "mcp_noted_list_events" {
			attributed = true
		}
	}
	if !attributed {
		t.Fatalf("no attributed tool call on the queen's stream: %v", chatEvents(h.log, "tool_call"))
	}
	if !slices.Contains(out.Tools, askTool{Name: "w1 · mcp_noted_list_events", OK: true, Worker: "w1"}) {
		t.Fatalf("superleo_ask tools: %+v", out.Tools)
	}
	turns := h.a.ChatHistory(out.Conversation)
	last := turns[len(turns)-1]
	if !slices.Contains(last.Steps, "w1 · mcp_noted_list_events") {
		t.Fatalf("transcript steps: %q", last.Steps)
	}
	// The order is on the board under the worker's name, which is what the
	// panel and the 3D view draw.
	var onBoard bool
	for _, tk := range h.a.tasks().Recent() {
		if tk.Worker == "w1" && tk.Dir == backend.TaskOut && tk.State == backend.TaskDone {
			onBoard = true
		}
	}
	if !onBoard {
		t.Fatalf("board: %+v", h.a.tasks().Recent())
	}
}

func TestParallelPiecesGoToTwoWorkersAndMerge(t *testing.T) {
	tasks := []map[string]any{
		{"task": "List this week's events in noted.", "needs": []string{"noted"}},
		{"task": "List next week's events in noted.", "needs": []string{"noted"}},
	}
	h := newDelegateHive(t, "max", delegateThen(tasks))
	hold := make(chan struct{})
	w1, w2 := newStubWorker(t, "w1"), newStubWorker(t, "w2")
	w1.hold, w2.hold = hold, hold
	h.join(t, w1, []string{"noted"})
	h.join(t, w2, []string{"noted"})

	// Both orders must be out before either is answered: only a parallel
	// hand-out gets past this.
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) && (len(w1.received()) == 0 || len(w2.received()) == 0) {
			time.Sleep(10 * time.Millisecond)
		}
		close(hold)
	}()
	id := h.a.SendChat("par-1", "这周和下周的例会？", nil)
	h.log.waitForTerminals(t, 1, 30*time.Second)
	if len(w1.received()) != 1 || len(w2.received()) != 1 {
		t.Fatalf("w1 %q, w2 %q", w1.received(), w2.received())
	}
	// The report, as the queen's model got it, is the delegate call's result.
	var report string
	for _, e := range h.log.snapshot() {
		if e.name == "chat:event" && e.requestID() == id && e.kind() == "tool_result" && e.payload["tool"] == backend.DelegateToolName {
			b, _ := json.Marshal(e.payload["result"])
			report = string(b)
		}
	}
	for _, want := range []string{"2 of 2", "REPORT-FROM-w1", "REPORT-FROM-w2"} {
		if !strings.Contains(report, want) {
			t.Fatalf("merged report lacks %q: %s", want, report)
		}
	}
	if h.ownCalls.Load() != 0 {
		t.Fatal("the queen did the work herself")
	}
}

func TestAFailingWorkerFallsBackToTheQueen(t *testing.T) {
	h := newDelegateHive(t, "max", delegateThen(nextWeek))
	w1 := newStubWorker(t, "w1")
	w1.fail = true
	h.join(t, w1, []string{"noted"})

	h.a.SendChat("fb-1", "下周有哪些例会？", nil)
	for _, e := range h.log.waitForTerminals(t, 1, 30*time.Second) {
		if e.name == "chat:error" {
			t.Fatalf("turn failed: %v", e.payload)
		}
	}
	if len(w1.received()) != 1 {
		t.Fatalf("the worker was not tried: %q", w1.received())
	}
	if n := h.ownCalls.Load(); n != 1 {
		t.Fatalf("the queen's own run called the tool %d time(s), want 1", n)
	}
	var said, attributed bool
	for _, p := range chatEvents(h.log, "state_update") {
		if c, _ := p["content"].(string); strings.Contains(c, "the queen does it herself") {
			said = true
		}
	}
	for _, p := range chatEvents(h.log, "tool_call") {
		if p["tool"] == "q · mcp_noted_list_events" {
			attributed = true
		}
	}
	if !said || !attributed {
		t.Fatalf("fallback not visible: said=%v attributed=%v", said, attributed)
	}
	turns := h.a.ChatHistory("fb-1")
	if steps := turns[len(turns)-1].Steps; !slices.Contains(steps, "q · mcp_noted_list_events") {
		t.Fatalf("transcript steps %q", steps)
	}
	// Her own run for the piece is not a conversation of the person's.
	for _, c := range h.a.ChatSessions() {
		if strings.HasPrefix(c.ID, backend.HiveSessionPrefix) {
			t.Fatalf("the queen's own run is listed: %+v", c)
		}
	}
}

func TestWithNoWorkerTheQueenKeepsHerTools(t *testing.T) {
	h := newDelegateHive(t, "max", delegateThen(nextWeek))
	h.a.SendChat("none-1", "下周有哪些例会？", nil)
	h.log.waitForTerminals(t, 1, 30*time.Second)
	first := h.llm.requests()[0]
	if !slices.Contains(first.Tools, "mcp_noted_list_events") || slices.Contains(first.Tools, backend.DelegateToolName) {
		t.Fatalf("tools with no worker: %v", first.Tools)
	}
	if h.ownCalls.Load() != 1 {
		t.Fatalf("own calls %d", h.ownCalls.Load())
	}
	var said bool
	for _, p := range chatEvents(h.log, "state_update") {
		if c, _ := p["content"].(string); strings.Contains(c, "no worker is live") {
			said = true
		}
	}
	if !said {
		t.Fatal("the turn did not say the queen is doing it herself")
	}
}

func TestOffKeepsTheQueenAsSheWas(t *testing.T) {
	h := newDelegateHive(t, "off", delegateThen(nextWeek))
	w1 := newStubWorker(t, "w1")
	h.join(t, w1, []string{"noted"})
	h.a.SendChat("off-1", "下周有哪些例会？", nil)
	h.log.waitForTerminals(t, 1, 30*time.Second)
	first := h.llm.requests()[0]
	if !slices.Contains(first.Tools, "mcp_noted_list_events") || slices.Contains(first.Tools, backend.DelegateToolName) {
		t.Fatalf("tools in off mode: %v", first.Tools)
	}
	if h.ownCalls.Load() != 1 || len(w1.received()) != 0 {
		t.Fatalf("own %d, worker %q", h.ownCalls.Load(), w1.received())
	}
	if h.a.svc.Agent().GetToolRegistry().Has(backend.DelegateToolName) {
		t.Fatal("hive_delegate registered in off mode")
	}
}

func TestAutoOffersDelegationButWithholdsNothing(t *testing.T) {
	h := newDelegateHive(t, "auto", delegateThen(nextWeek))
	w1 := newStubWorker(t, "w1")
	h.join(t, w1, []string{"noted"})
	h.a.SendChat("auto-1", "下周有哪些例会？", nil)
	h.log.waitForTerminals(t, 1, 30*time.Second)
	first := h.llm.requests()[0]
	if !slices.Contains(first.Tools, "mcp_noted_list_events") || !slices.Contains(first.Tools, backend.DelegateToolName) {
		t.Fatalf("tools in auto mode: %v", first.Tools)
	}
	if len(w1.received()) != 1 || h.ownCalls.Load() != 0 {
		t.Fatalf("own %d, worker %q", h.ownCalls.Load(), w1.received())
	}
}

func TestNotedWorkGoesOnlyWhereNotedIs(t *testing.T) {
	h := newDelegateHive(t, "max", delegateThen(nextWeek))
	bare, full := newStubWorker(t, "w1"), newStubWorker(t, "w2")
	h.join(t, bare, []string{"cortexdb"})
	h.join(t, full, []string{"cortexdb", "noted"})
	h.a.SendChat("caps-1", "下周有哪些例会？", nil)
	h.log.waitForTerminals(t, 1, 30*time.Second)
	if len(bare.received()) != 0 || len(full.received()) != 1 {
		t.Fatalf("w1 (no noted) %q, w2 %q", bare.received(), full.received())
	}
}
