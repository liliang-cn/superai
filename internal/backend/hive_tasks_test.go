package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func boardWithLog() (*TaskBoard, func() []HiveTask) {
	var mu sync.Mutex
	var seen []HiveTask
	b := NewTaskBoard(func(t HiveTask) {
		mu.Lock()
		seen = append(seen, t)
		mu.Unlock()
	})
	return b, func() []HiveTask { mu.Lock(); defer mu.Unlock(); return append([]HiveTask(nil), seen...) }
}

func TestATaskIsFollowedFromSendToDone(t *testing.T) {
	b, seen := boardWithLog()
	id := b.Start("w0", TaskOut, "do the thing")
	b.Progress(id, PhaseTool, "shell")
	b.Progress(id, PhaseTool, "shell")
	b.Progress(id, PhaseWriting, "")
	b.Finish(id, TaskDone, "  all done ", "")

	got := b.Recent()
	if len(got) != 1 {
		t.Fatalf("%d tasks", len(got))
	}
	k := got[0]
	if k.State != TaskDone || k.Tools != 2 || k.Result != "all done" || k.EndedAt.IsZero() || k.Phase != "" {
		t.Fatalf("%+v", k)
	}
	// One event for the start, two tool calls, one phase change, one finish:
	// each is what the panel animates.
	if n := len(seen()); n != 5 {
		t.Fatalf("%d events: %+v", n, seen())
	}
}

func TestARepeatedPhaseIsNotRepeatedNews(t *testing.T) {
	b, seen := boardWithLog()
	id := b.Start("w", TaskOut, "x")
	before := len(seen())
	b.Progress(id, PhaseThinking, "") // already thinking
	if len(seen()) != before {
		t.Fatal("an unchanged phase was published again")
	}
}

func TestAFinishedTaskStaysFinished(t *testing.T) {
	// Cancelled, then the worker's late answer arrives: the cancellation is
	// what happened.
	b, _ := boardWithLog()
	id := b.Start("w", TaskOut, "x")
	b.Finish(id, TaskCancelled, "", "")
	b.Finish(id, TaskDone, "late", "")
	b.Progress(id, PhaseTool, "shell")
	k := b.Recent()[0]
	if k.State != TaskCancelled || k.Result != "" || k.Tools != 0 {
		t.Fatalf("%+v", k)
	}
}

func TestTheBoardKeepsRunningTasksAndDropsOldFinishedOnes(t *testing.T) {
	b, _ := boardWithLog()
	first := b.Start("w", TaskOut, "still going")
	for i := 0; i < boardKeep+20; i++ {
		id := b.Start("w", TaskOut, "x")
		b.Finish(id, TaskDone, "", "")
	}
	got := b.Recent()
	if len(got) > boardKeep+1 {
		t.Fatalf("%d tasks kept", len(got))
	}
	found := false
	for _, k := range got {
		if k.ID == first {
			found = true
		}
	}
	if !found {
		t.Fatal("a task that was still running was dropped")
	}
}

func TestANilBoardIsHarmless(t *testing.T) {
	var b *TaskBoard
	id := b.Start("w", TaskOut, "x")
	b.Progress(id, PhaseTool, "t")
	b.Finish(id, TaskDone, "", "")
	if len(b.Recent()) != 0 {
		t.Fatal("a nil board has tasks")
	}
}

func TestALongPromptIsClippedToWhatABoardKeeps(t *testing.T) {
	b, _ := boardWithLog()
	b.Start("w", TaskOut, strings.Repeat("字", promptKeep*2))
	if n := len([]rune(b.Recent()[0].Prompt)); n > promptKeep+1 {
		t.Fatalf("prompt is %d runes", n)
	}
}

func TestATaskKeepsItsTimeline(t *testing.T) {
	b, _ := boardWithLog()
	id := b.Start("w", TaskOut, "x")
	b.Progress(id, PhaseTool, "shell")
	b.Progress(id, PhaseWriting, "")
	b.Finish(id, TaskDone, "answer", "")
	got, ok := b.Get(id)
	if !ok {
		t.Fatal("not found")
	}
	var steps []string
	for _, e := range got.Events {
		steps = append(steps, e.Phase+":"+e.Tool)
	}
	if strings.Join(steps, " ") != "sent: thinking: tool:shell writing: done:" {
		t.Fatalf("timeline %v", steps)
	}
	if _, ok := b.Get("nope"); ok {
		t.Fatal("found a task that does not exist")
	}
	// A copy, not the board's own slice.
	got.Events[0].Phase = "tampered"
	if again, _ := b.Get(id); again.Events[0].Phase != "sent" {
		t.Fatal("Get handed out the board's own timeline")
	}
}

func TestTheOrderKeepsOneUUIDOnBothSides(t *testing.T) {
	var session string
	f := newFakeWorker(t, "")
	f.onSend = func(sess, _ string, w *fakeWorker) string {
		session = sess
		go w.emit("chat:done", map[string]any{"requestId": "req-1", "final": "ok"})
		return "req-1"
	}
	b, _ := boardWithLog()
	run := NewRemoteRunner(RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{"w0": {URL: f.srv.URL}}})
	run.SetBoard(b)
	run.Run(context.Background(), "w0", "go")
	k := b.Recent()[0]
	if session != HiveSessionPrefix+k.ID {
		t.Fatalf("the worker's session %q is not the order's id %q", session, k.ID)
	}
	id, ok := TaskIDFromSession(session)
	if !ok || id != k.ID {
		t.Fatalf("could not recover the id: %q %v", id, ok)
	}
	// And the worker's own board, given that session, records it under the same.
	w := NewTaskBoard(nil)
	w.StartAs(id, "w0", "", TaskIn, "go")
	if got, ok := w.Get(k.ID); !ok || got.Dir != TaskIn {
		t.Fatalf("%+v %v", got, ok)
	}
}

func TestOnlyAUUIDSessionNamesATask(t *testing.T) {
	for _, s := range []string{"hive:foo", "hive:", "chat:abc", "hive:1234", "telegram:1:2"} {
		if _, ok := TaskIDFromSession(s); ok {
			t.Errorf("%q was taken as an order id", s)
		}
	}
	id := NewTaskID()
	if got, ok := TaskIDFromSession(HiveSessionPrefix + id); !ok || got != id {
		t.Fatalf("%q %v", got, ok)
	}
}

func TestWorkProgressReachesTheBoardThroughTheRunner(t *testing.T) {
	f := newFakeWorker(t, "tok")
	f.onSend = func(_, _ string, w *fakeWorker) string {
		go func() {
			w.emit("chat:event", map[string]any{"requestId": "req-1", "type": "thinking"})
			w.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_call", "tool": "shell"})
			w.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_call", "tool": "kubectl"})
			// A stream of partials is one "writing".
			for i := 0; i < 20; i++ {
				w.emit("chat:event", map[string]any{"requestId": "req-1", "type": "partial", "content": "x"})
			}
			time.Sleep(100 * time.Millisecond)
			w.emit("chat:done", map[string]any{"requestId": "req-1", "final": "ok"})
		}()
		return "req-1"
	}
	b, seen := boardWithLog()
	run := NewRemoteRunner(RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{
		"w0": {URL: f.srv.URL, Token: "tok"},
	}})
	run.SetBoard(b)
	res, _ := run.Run(context.Background(), "w0", "go")
	if res.Failed || res.Text != "ok" {
		t.Fatalf("%+v", res)
	}
	k := b.Recent()[0]
	if k.State != TaskDone || k.Dir != TaskOut || k.Worker != "w0" || k.Tools != 2 {
		t.Fatalf("%+v", k)
	}
	writing := 0
	for _, e := range seen() {
		if e.Phase == PhaseWriting {
			writing++
		}
	}
	if writing != 1 {
		t.Fatalf("%d writing events for a stream of partials", writing)
	}
}

func TestAFailedOrderIsMarkedFailed(t *testing.T) {
	b, _ := boardWithLog()
	run := NewRemoteRunner(RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{"w0": {URL: "http://127.0.0.1:1"}}})
	run.SetBoard(b)
	run.Run(context.Background(), "w0", "go")
	if k := b.Recent()[0]; k.State != TaskFailed || k.Error == "" {
		t.Fatalf("%+v", k)
	}
}

func TestAPeerTaskCarriesWhoAsked(t *testing.T) {
	f := newFakeWorker(t, "tok")
	f.onSend = func(_, _ string, w *fakeWorker) string {
		go w.emit("chat:done", map[string]any{"requestId": "req-1", "final": "42"})
		return "req-1"
	}
	b, _ := boardWithLog()
	run := NewRemoteRunner(RemoteAgents{})
	run.SetRoster(func() map[string]RemoteAgent { return map[string]RemoteAgent{"w3": {URL: f.srv.URL, Token: "tok"}} })
	run.SetBoard(b)
	run.SetOrigin("w1")
	res, _ := run.Run(context.Background(), "w3", "what is it?")
	if res.Failed || res.Text != "42" {
		t.Fatalf("%+v", res)
	}
	k := b.Recent()[0]
	if k.Dir != TaskPeer || k.From != "w1" || k.Worker != "w3" {
		t.Fatalf("%+v", k)
	}
}

func TestAReportedTaskReachesTheQueensBoardAndStaysFinished(t *testing.T) {
	q, seen := boardWithLog()
	running := HiveTask{ID: "p1", Worker: "w3", From: "w1", Dir: TaskPeer, State: TaskRunning, StartedAt: time.Now()}
	q.Ingest(running)
	done := running
	done.State, done.Result = TaskDone, "42"
	q.Ingest(done)
	q.Ingest(running) // a late report of it running must not reopen it
	got := q.Recent()
	if len(got) != 1 || got[0].State != TaskDone || got[0].Result != "42" {
		t.Fatalf("%+v", got)
	}
	if len(seen()) != 2 {
		t.Fatalf("%d events", len(seen()))
	}
}

func TestAWorkerFetchesItsPeersFromTheQueenAndLeavesItselfOut(t *testing.T) {
	h := NewHive("q", time.Minute)
	for _, n := range []string{"w0", "w1", "w2"} {
		h.Join(hello(n))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hive/roster" || r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(h.Roster())
	}))
	defer srv.Close()
	a := &Announcer{Settings: HiveSettings{Role: HiveRoleWorker, Name: "w1", JoinURL: srv.URL}, Token: "secret"}
	p := a.Peers(context.Background())
	if len(p) != 2 || p["w1"].URL != "" {
		t.Fatalf("%+v", p)
	}
	if p["w0"].Token != "secret" || p["w0"].URL != "http://w0:43117" {
		t.Fatalf("%+v", p["w0"])
	}
	// A different peer credential, when the hive does not share one.
	b := &Announcer{Settings: HiveSettings{Role: HiveRoleWorker, Name: "w1", JoinURL: srv.URL, JoinToken: "secret", PeerToken: "peers"}, Token: "own"}
	if got := b.Peers(context.Background())["w2"].Token; got != "peers" {
		t.Fatalf("peer token %q", got)
	}
}

type pulseLog struct {
	mu sync.Mutex
	ps []HivePulse
}

func (l *pulseLog) add(p HivePulse) { l.mu.Lock(); l.ps = append(l.ps, p); l.mu.Unlock() }
func (l *pulseLog) all() []HivePulse {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]HivePulse(nil), l.ps...)
}

func TestAToolCallAndItsResultAreEachAPulse(t *testing.T) {
	var got []string
	p := NewPulser(func(kind, tool string, n int) { got = append(got, fmt.Sprintf("%s:%s:%d", kind, tool, n)) })
	p.Event("tool_call", "shell", 0, 0)
	p.Event("tool_result", "shell", 0, 1234)
	if strings.Join(got, " ") != "tool:shell:0 result:shell:1234" {
		t.Fatalf("%v", got)
	}
}

func TestAStreamOfTextIsGatheredNotDrawnChunkByChunk(t *testing.T) {
	var kinds []string
	total := 0
	p := NewPulser(func(kind, tool string, n int) { kinds = append(kinds, kind); total += n })
	// A hundred chunks inside a few milliseconds: at most a pulse or two.
	for i := 0; i < 100; i++ {
		p.Event("partial", "", 5, 0)
	}
	if len(kinds) > 2 {
		t.Fatalf("%d pulses for one burst of text", len(kinds))
	}
	time.Sleep(150 * time.Millisecond)
	p.Event("partial", "", 5, 0)
	if total < 5*100 {
		t.Fatalf("the bytes were not carried: %d", total)
	}
}

func TestThinkingIsThrottled(t *testing.T) {
	n := 0
	p := NewPulser(func(kind, tool string, _ int) { n++ })
	for i := 0; i < 50; i++ {
		p.Event("thinking", "", 0, 0)
	}
	if n != 1 {
		t.Fatalf("%d thinking pulses in an instant", n)
	}
}

func TestPulsesReachTheBoardWithWhoAndWhere(t *testing.T) {
	f := newFakeWorker(t, "")
	f.onSend = func(_, _ string, w *fakeWorker) string {
		go func() {
			time.Sleep(100 * time.Millisecond) // after the id is known
			w.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_call", "tool": "kubectl"})
			w.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_result", "tool": "kubectl", "result": "abcdefghij"})
			// Another conversation on the same worker must not leak in.
			w.emit("chat:event", map[string]any{"requestId": "someone-else", "type": "tool_call", "tool": "secret"})
			time.Sleep(50 * time.Millisecond)
			w.emit("chat:done", map[string]any{"requestId": "req-1", "final": "ok"})
		}()
		return "req-1"
	}
	b, _ := boardWithLog()
	var log pulseLog
	b.SetPulse(log.add)
	run := NewRemoteRunner(RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{"w0": {URL: f.srv.URL}}})
	run.SetBoard(b)
	run.Run(context.Background(), "w0", "go")

	ps := log.all()
	var kinds []string
	for _, p := range ps {
		kinds = append(kinds, p.Kind+":"+p.Tool)
		if p.Worker != "w0" || p.Dir != TaskOut || p.Task == "" {
			t.Fatalf("a pulse without its context: %+v", p)
		}
	}
	if strings.Join(kinds, " ") != "tool:kubectl result:kubectl" {
		t.Fatalf("pulses: %v", kinds)
	}
	if ps[1].Bytes == 0 {
		t.Fatal("a result pulse carries no size")
	}
}

func TestAFinishedTaskPulsesNothing(t *testing.T) {
	b, _ := boardWithLog()
	var log pulseLog
	b.SetPulse(log.add)
	id := b.Start("w", TaskOut, "x")
	b.Pulse(id, "tool", "a", 0)
	b.Finish(id, TaskDone, "", "")
	b.Pulse(id, "tool", "late", 0)
	if len(log.all()) != 1 {
		t.Fatalf("%+v", log.all())
	}
}

// An order remembers the conversation it was given in, read from the run's
// context, so a request's orders can be shown together.
func TestAnOrderRemembersItsConversation(t *testing.T) {
	b := NewTaskBoard(nil)
	id := b.Start("w1", TaskOut, "find the market size")
	b.InSession(id, ChatSessionFrom(WithChatSession(context.Background(), "chat-42")))
	got, _ := b.Get(id)
	if got.Session != "chat-42" {
		t.Fatalf("session = %q", got.Session)
	}
	if ChatSessionFrom(context.Background()) != "" {
		t.Fatal("a context without a conversation named one")
	}
}
