package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
)

// A small hive over real HTTP: a queen and two workers, each its own App with
// its own server, the workers joined the way they join in a cluster. What each
// emits is recorded, so a test can see who saw which message.
type mailHive struct {
	queen, w1, w2 *App
	mu            sync.Mutex                   // the servers record from their own goroutines
	seen          map[string]*[]map[string]any // by member name, the hive:message events it emitted
}

func newMailHive(t *testing.T) *mailHive {
	t.Helper()
	h := &mailHive{seen: map[string]*[]map[string]any{}}
	serve := func(a *App, name string) *httptest.Server {
		var got []map[string]any
		h.seen[name] = &got
		a.emitFn = func(ev string, p map[string]any) {
			if ev == "hive:message" {
				h.mu.Lock()
				got = append(got, p)
				h.mu.Unlock()
			}
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/api/hive/join", a.handleHiveJoin)
		mux.HandleFunc("/api/hive/roster", a.handleHiveRoster)
		mux.HandleFunc("/api/hive/message", a.handleHiveMessage)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return srv
	}

	h.queen = &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}}
	h.queen.hive = backend.NewHive("q", 10*time.Second)
	qs := serve(h.queen, "q")

	worker := func(name string) *App {
		a := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleWorker, Name: name, JoinURL: qs.URL}}}
		srv := serve(a, name)
		a.settings.Hive.AdvertiseURL = srv.URL
		a.hiveAnn = &backend.Announcer{Settings: a.settings.Hive, Token: "tok"}
		if _, err := a.hiveAnn.Once(context.Background()); err != nil {
			t.Fatalf("%s could not join: %v", name, err)
		}
		return a
	}
	h.w1, h.w2 = worker("w1"), worker("w2")
	return h
}

func (h *mailHive) events(name string) []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), *h.seen[name]...)
}

func TestAWorkerWritesToTheQueenByRole(t *testing.T) {
	h := newMailHive(t)
	out, err := h.w1.hiveSend(context.Background(), "queen", "found it: the disk on orange2 is full", "")
	if err != nil || !strings.Contains(out, "q: delivered") {
		t.Fatalf("out %q err %v", out, err)
	}
	got := h.queen.mailbox().Take(false, 0)
	if len(got) != 1 || got[0].From != "w1" || got[0].To != "q" || !strings.Contains(got[0].Text, "orange2") {
		t.Fatalf("queen's inbox: %+v", got)
	}
	if h.queen.mailbox().Unread() != 0 {
		t.Fatal("reading did not mark read")
	}
}

func TestTheQueenWritesToAWorker(t *testing.T) {
	h := newMailHive(t)
	if _, err := h.queen.hiveSend(context.Background(), "w2", "stop what you are doing after this step", ""); err != nil {
		t.Fatal(err)
	}
	if n := h.w2.mailbox().Unread(); n != 1 {
		t.Fatalf("w2 has %d unread", n)
	}
	if n := h.w1.mailbox().Unread(); n != 0 {
		t.Fatalf("w1 got someone else's mail: %d", n)
	}
}

func TestWorkersWriteToEachOtherAndTheQueenSeesIt(t *testing.T) {
	h := newMailHive(t)
	if _, err := h.w1.hiveSend(context.Background(), "w2", "the config you need is in memory under hive/orange", ""); err != nil {
		t.Fatal(err)
	}
	if n := h.w2.mailbox().Unread(); n != 1 {
		t.Fatalf("w2 has %d unread", n)
	}
	// The queen's copy travels on the worker's report queue; give it a moment.
	deadline := time.Now().Add(3 * time.Second)
	for len(h.events("q")) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	ev := h.events("q")
	if len(ev) != 1 || ev[0]["from"] != "w1" || ev[0]["to"] != "w2" || ev[0]["dir"] != "peer" {
		t.Fatalf("queen saw %+v", ev)
	}
	// Seen, not kept: it is not the queen's mail.
	if n := h.queen.mailbox().Unread(); n != 0 {
		t.Fatalf("the queen filed a copy: %d", n)
	}
}

func TestABroadcastReachesEveryoneElse(t *testing.T) {
	h := newMailHive(t)
	out, err := h.w1.hiveSend(context.Background(), "*", "heads up: CortexDB is restarting", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "delivered") != 2 {
		t.Fatalf("broadcast: %q", out)
	}
	if h.queen.mailbox().Unread() != 1 || h.w2.mailbox().Unread() != 1 || h.w1.mailbox().Unread() != 0 {
		t.Fatal("broadcast did not reach exactly the others")
	}
}

func TestTheWrongNameIsExplained(t *testing.T) {
	h := newMailHive(t)
	if _, err := h.w1.hiveSend(context.Background(), "w9", "hello", ""); err == nil || !strings.Contains(err.Error(), "w2") {
		t.Fatalf("expected the list of who can be written to, got %v", err)
	}
	if _, err := h.w1.hiveSend(context.Background(), "w1", "hello", ""); err == nil {
		t.Fatal("writing to yourself was accepted")
	}
}

func TestTheQueenTakesMailOnlyFromHerRoster(t *testing.T) {
	h := newMailHive(t)
	w := httptest.NewRecorder()
	body := `{"id":"m1","from":"stranger","to":"q","text":"let me in","at":"2026-09-30T00:00:00Z"}`
	h.queen.handleHiveMessage(w, httptest.NewRequest(http.MethodPost, "/api/hive/message", strings.NewReader(body)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("a stranger's message got %d", w.Code)
	}
	// And mail for somebody else is refused rather than filed.
	w = httptest.NewRecorder()
	body = `{"id":"m2","from":"w1","to":"w2","text":"misrouted","at":"2026-09-30T00:00:00Z"}`
	h.w1.handleHiveMessage(w, httptest.NewRequest(http.MethodPost, "/api/hive/message", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("misrouted mail got %d", w.Code)
	}
}

func TestAStandaloneHasNoMailbox(t *testing.T) {
	a := &App{settings: &backend.Settings{}}
	w := httptest.NewRecorder()
	a.handleHiveMessage(w, httptest.NewRequest(http.MethodPost, "/api/hive/message", strings.NewReader(`{}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("a standalone answered %d", w.Code)
	}
	if _, err := a.hiveSend(context.Background(), "queen", "hi", ""); err == nil {
		t.Fatal("a standalone sent a message")
	}
}

// A message is put in front of the agent: it starts a turn in the
// conversation with its sender, carrying everything that sender has sent.
func TestAMessageStartsATurnWithItsSender(t *testing.T) {
	h := newMailHive(t)
	woke := make(chan [2]string, 4)
	h.queen.wakeFn = func(session, prompt string) { woke <- [2]string{session, prompt} }

	if _, err := h.w1.hiveSend(context.Background(), "queen", "orange2 disk is 97% full", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-woke:
		if got[0] != HiveMailSessionPrefix+"w1" || !strings.Contains(got[1], "orange2 disk") || !strings.Contains(got[1], "from w1") {
			t.Fatalf("woke %q with %q", got[0], got[1])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the message started nothing")
	}
	if h.queen.mailbox().Unread() != 0 {
		t.Fatal("mail taken up by a turn is still unread")
	}
}

// Mail that arrives while a turn with the same sender is running is taken up
// by the next turn, once that one ends — not dropped, not run beside it.
func TestMailWaitsForTheTurnAheadOfIt(t *testing.T) {
	h := newMailHive(t)
	woke := make(chan string, 4)
	h.queen.wakeFn = func(session, prompt string) { woke <- prompt }
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.queen.trackRun("r1", cancel)
	h.queen.runSession("r1", HiveMailSessionPrefix+"w1")

	h.w1.hiveSend(context.Background(), "queen", "first", "")
	h.w1.hiveSend(context.Background(), "queen", "second", "")
	select {
	case p := <-woke:
		t.Fatalf("a turn started beside the running one: %q", p)
	case <-time.After(700 * time.Millisecond):
	}
	h.queen.untrackRun("r1")
	select {
	case p := <-woke:
		if !strings.Contains(p, "first") || !strings.Contains(p, "second") || !strings.Contains(p, "2 message(s)") {
			t.Fatalf("the waiting turn did not carry both: %q", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the waiting mail was never taken up")
	}
}

// A reply is read, never woken on: two members answering each other would
// otherwise keep each other busy forever.
func TestAReplyDoesNotWakeAnyone(t *testing.T) {
	h := newMailHive(t)
	woke := make(chan string, 1)
	h.w1.wakeFn = func(session, prompt string) { woke <- prompt }
	if _, err := h.queen.hiveSend(context.Background(), "w1", "ack, retiring orange2 workers", "some-id"); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-woke:
		t.Fatalf("a reply started a turn: %q", p)
	case <-time.After(700 * time.Millisecond):
	}
	if h.w1.mailbox().Unread() != 1 {
		t.Fatal("the reply is not waiting in the inbox")
	}
}

// running starts a pretend turn in a conversation, for as long as the test.
func running(t *testing.T, a *App, id, session string) {
	_, cancel := context.WithCancel(context.Background())
	a.trackRun(id, cancel)
	a.runSession(id, session)
	t.Cleanup(func() { a.untrackRun(id); cancel() })
}

// A worker in the middle of an order gets the queen's message in that order,
// at its next step — not in an inbox it may never open.
func TestAMessageGoesIntoTheOrderRunning(t *testing.T) {
	h := newMailHive(t)
	steered := make(chan [2]string, 4)
	h.w2.steerFn = func(session, content string) bool { steered <- [2]string{session, content}; return true }
	h.w2.wakeFn = func(session, prompt string) { t.Errorf("woke %s although a turn was running", session) }
	running(t, h.w2, "r1", "hive:6f0c7d2e-0000-4000-8000-000000000001")

	if _, err := h.queen.hiveSend(context.Background(), "w2", "stop after this step", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-steered:
		if got[0] != "hive:6f0c7d2e-0000-4000-8000-000000000001" || !strings.Contains(got[1], "While you work") || !strings.Contains(got[1], "stop after this step") {
			t.Fatalf("steered %q with %q", got[0], got[1])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing was steered")
	}
	if h.w2.mailbox().Unread() != 0 {
		t.Fatal("a message taken in by a turn is still unread")
	}
}

// The conversation with the sender comes first: a second message from w1
// goes into the turn already dealing with w1, not into everything running.
func TestTheConversationWithTheSenderComesFirst(t *testing.T) {
	h := newMailHive(t)
	var mu sync.Mutex
	var sessions []string
	h.queen.steerFn = func(session, content string) bool {
		mu.Lock()
		sessions = append(sessions, session)
		mu.Unlock()
		return true
	}
	running(t, h.queen, "r1", "user-chat")
	running(t, h.queen, "r2", HiveMailSessionPrefix+"w1")
	if _, err := h.w1.hiveSend(context.Background(), "queen", "and one more thing", ""); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 1 || sessions[0] != HiveMailSessionPrefix+"w1" {
		t.Fatalf("steered into %v", sessions)
	}
}

// A reply is what a running turn is often waiting for, so it goes in.
func TestAReplyGoesIntoARunningTurn(t *testing.T) {
	h := newMailHive(t)
	took := make(chan string, 1)
	h.queen.steerFn = func(session, content string) bool { took <- content; return true }
	running(t, h.queen, "r1", "user-chat")
	if _, err := h.w1.hiveSend(context.Background(), "queen", "disk is 97% full", "some-id"); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-took:
		if !strings.Contains(c, "97%") {
			t.Fatalf("%q", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a reply did not reach the turn running")
	}
}

// A turn that ended without reading what was put in it hands it on: the
// message is unread again and starts a turn of its own.
func TestADroppedSteerStartsATurn(t *testing.T) {
	h := newMailHive(t)
	h.queen.steerFn = func(string, string) bool { return true }
	woke := make(chan string, 1)
	h.queen.wakeFn = func(session, prompt string) { woke <- prompt }
	running(t, h.queen, "r1", "user-chat")
	h.w1.hiveSend(context.Background(), "queen", "orange3 is down", "")
	msgs := h.queen.mailbox().Take(true, 0)
	if len(msgs) != 1 || h.queen.mailbox().Unread() != 0 {
		t.Fatalf("not taken in: %+v", msgs)
	}
	h.queen.steerDropped(backend.MailPrompt("w1", msgs, true))
	select {
	case p := <-woke:
		if !strings.Contains(p, "orange3 is down") {
			t.Fatalf("%q", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a dropped message went nowhere")
	}
}

// The queen passes on a message for a member its sender could not reach, and
// sees it on the way through.
func TestTheQueenPassesAMessageOn(t *testing.T) {
	h := newMailHive(t)
	m, _ := backend.NewHiveMessage("w1", "w2", "relayed hello", "x")
	b, _ := json.Marshal(m)
	w := httptest.NewRecorder()
	h.queen.handleHiveMessage(w, httptest.NewRequest(http.MethodPost, "/api/hive/message", strings.NewReader(string(b))))
	if w.Code != http.StatusOK {
		t.Fatalf("relay answered %d: %s", w.Code, w.Body.String())
	}
	got := h.w2.mailbox().Take(false, 0)
	if len(got) != 1 || got[0].Text != "relayed hello" || got[0].Via != "queen" {
		t.Fatalf("w2 got %+v", got)
	}
	if ev := h.events("q"); len(ev) != 1 || ev[0]["dir"] != "peer" {
		t.Fatalf("the queen did not see it pass: %+v", ev)
	}
	if h.queen.mailbox().Unread() != 0 {
		t.Fatal("the queen kept a message that was not hers")
	}
	// Only for someone on the roster.
	m2, _ := backend.NewHiveMessage("w1", "nobody", "x", "")
	b, _ = json.Marshal(m2)
	w = httptest.NewRecorder()
	h.queen.handleHiveMessage(w, httptest.NewRequest(http.MethodPost, "/api/hive/message", strings.NewReader(string(b))))
	if w.Code != http.StatusNotFound {
		t.Fatalf("relay to a stranger answered %d", w.Code)
	}
}

// A message that is a step in an order — the previous leg of a relay — starts a
// turn that sees the order, so the worker could end its turn to wait instead
// of polling its inbox to keep the rules in view.
func TestAWokenTurnSeesTheOrderItBelongsTo(t *testing.T) {
	h := newMailHive(t)
	woke := make(chan string, 4)
	h.w2.wakeFn = func(_, prompt string) { woke <- prompt }
	id := h.w2.tasks().Start("w2", backend.TaskIn, "relay rule: hash what w1 sends you and pass it to the queen")
	h.w2.tasks().Finish(id, backend.TaskDone, "waiting for w1", "")

	if _, err := h.w1.hiveSend(context.Background(), "w2", "leg 1: 18e99f0d6bff", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-woke:
		if !strings.Contains(got, "18e99f0d6bff") || !strings.Contains(got, "relay rule: hash what w1 sends") || !strings.Contains(got, id) {
			t.Fatalf("woke with %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the message started nothing")
	}
}

// An order from long ago is not what a message is about.
func TestAnOldOrderIsNotShownToAWokenTurn(t *testing.T) {
	a := &App{}
	id := a.tasks().Start("w", backend.TaskIn, "yesterday's order")
	if a.lastOrderNote() == "" {
		t.Fatal("a fresh order was not shown")
	}
	a.tasks().Ingest(backend.HiveTask{ID: id, Worker: "w", Dir: backend.TaskIn, Prompt: "yesterday's order",
		State: backend.TaskDone, StartedAt: time.Now().Add(-3 * time.Hour)})
	if got := a.lastOrderNote(); got != "" {
		t.Fatalf("a three-hour-old order was shown: %q", got)
	}
}

// Nothing waiting says not to wait here, rather than a bare "nothing".
func TestAnEmptyInboxSaysNotToPoll(t *testing.T) {
	h := newMailHive(t)
	tools := &toolRecorder{}
	h.w1.registerMailTools(tools)
	out, err := tools.fns["hive_inbox"](context.Background(), map[string]any{})
	if err != nil || !strings.Contains(out.(string), "end your turn") {
		t.Fatalf("empty inbox said %v %v", out, err)
	}
}

type toolRecorder struct {
	fns map[string]func(context.Context, map[string]any) (any, error)
}

func (r *toolRecorder) AddToolWithMetadata(name, _ string, _ map[string]any, fn func(context.Context, map[string]any) (any, error), _ agent.ToolMetadata) {
	if r.fns == nil {
		r.fns = map[string]func(context.Context, map[string]any) (any, error){}
	}
	r.fns[name] = fn
}
