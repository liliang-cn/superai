package backend

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

func testBrowserQueue(t *testing.T) *browserQueue {
	t.Helper()
	q := newBrowserQueue("test", filepath.Join(t.TempDir(), "browser-test.lock"))
	q.idle, q.maxHold, q.maxWait = time.Minute, time.Hour, time.Minute
	return q
}

// waitFor polls cond until it holds or a second has passed.
func waitForQueue(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitingCount(q *browserQueue) int { return len(q.status().Waiting) }

// Two orders that both want the browser at once take it one after the
// other: the second starts only once the first has let go, and is told it
// is queued meanwhile.
func TestBrowserQueueRunsTwoOrdersOneAfterTheOther(t *testing.T) {
	q := testBrowserQueue(t)
	ctx := context.Background()

	var mu sync.Mutex
	var log []string
	note := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
	inUse := 0
	use := func(owner string, notify func(string)) {
		if err := q.acquire(ctx, owner, owner, notify); err != nil {
			t.Errorf("%s: %v", owner, err)
			return
		}
		mu.Lock()
		inUse++
		if inUse > 1 {
			t.Errorf("%s drives the browser while another order does", owner)
		}
		log = append(log, owner+" start")
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		// A second call of the same order goes straight through.
		if err := q.acquire(ctx, owner, owner, nil); err != nil {
			t.Errorf("%s again: %v", owner, err)
		}
		mu.Lock()
		inUse--
		log = append(log, owner+" end")
		mu.Unlock()
		q.release(owner)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); use("A", nil) }()
	waitForQueue(t, "A to hold the browser", func() bool { return q.status().Holder == "A" })
	wg.Add(1)
	go func() { defer wg.Done(); use("B", note) }()
	waitForQueue(t, "B to queue", func() bool { return waitingCount(q) == 1 })
	if st := q.status(); st.Holder != "A" || st.Waiting[0] != "B" {
		t.Fatalf("queue = %+v, want A holding and B waiting", st)
	}
	wg.Wait()

	var order []string
	queued := false
	for _, l := range log {
		if strings.HasPrefix(l, "排队中") {
			queued = true
			if !strings.Contains(l, "A") {
				t.Errorf("queued notice does not name the holder: %q", l)
			}
			continue
		}
		if strings.HasSuffix(l, "start") || strings.HasSuffix(l, "end") {
			order = append(order, l)
		}
	}
	if !queued {
		t.Errorf("B was never told it was queued: %q", log)
	}
	if got := strings.Join(order, ", "); got != "A start, A end, B start, B end" {
		t.Fatalf("order = %s", got)
	}
	if st := q.status(); st.Holder != "" || len(st.Waiting) != 0 {
		t.Fatalf("queue not empty afterwards: %+v", st)
	}
}

// An order cancelled while it waits leaves the line: the order behind it
// moves up, and it is never handed the browser.
func TestBrowserQueueCancelledOrderLeavesTheLine(t *testing.T) {
	q := testBrowserQueue(t)
	if err := q.acquire(context.Background(), "A", "A", nil); err != nil {
		t.Fatal(err)
	}
	bctx, cancelB := context.WithCancel(context.Background())
	bErr := make(chan error, 1)
	go func() { bErr <- q.acquire(bctx, "B", "B", nil) }()
	waitForQueue(t, "B to queue", func() bool { return waitingCount(q) == 1 })
	cGot := make(chan error, 1)
	go func() { cGot <- q.acquire(context.Background(), "C", "C", nil) }()
	waitForQueue(t, "C to queue", func() bool { return waitingCount(q) == 2 })

	cancelB()
	if err := <-bErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled B got %v", err)
	}
	if st := q.status(); len(st.Waiting) != 1 || st.Waiting[0] != "C" {
		t.Fatalf("after cancelling B the line is %+v, want only C", st)
	}

	q.release("A")
	if err := <-cGot; err != nil {
		t.Fatal(err)
	}
	if h := q.status().Holder; h != "C" {
		t.Fatalf("holder = %q, want C", h)
	}
	// B releasing after it left is harmless.
	q.release("B")
	if h := q.status().Holder; h != "C" {
		t.Fatalf("a cancelled order's release took the browser from C (holder %q)", h)
	}
}

// An order that stops using the browser without letting go — stuck, or its
// release lost — loses it once it has been idle too long and someone waits;
// its late release does not take the browser from the next one.
func TestBrowserQueueStuckHolderIsReleasedByTimeout(t *testing.T) {
	q := testBrowserQueue(t)
	q.idle = 80 * time.Millisecond
	if err := q.acquire(context.Background(), "stuck", "stuck", nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := q.acquire(context.Background(), "B", "B", nil); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited < q.idle/2 {
		t.Fatalf("B went ahead after %s, before the stuck holder went idle", waited)
	}
	if h := q.status().Holder; h != "B" {
		t.Fatalf("holder = %q, want B", h)
	}
	q.release("stuck")
	if h := q.status().Holder; h != "B" {
		t.Fatalf("the stuck order's late release took the browser from B (holder %q)", h)
	}
	// Should the stuck order come back, it queues behind B like anyone.
	again := make(chan error, 1)
	go func() { again <- q.acquire(context.Background(), "stuck", "stuck", nil) }()
	waitForQueue(t, "the stuck order to queue again", func() bool { return waitingCount(q) == 1 })
	q.release("B")
	if err := <-again; err != nil {
		t.Fatal(err)
	}
}

// An order that keeps using the browser is not cut off for being busy, but
// it is once it has held the browser past the longest hold.
func TestBrowserQueueLongestHold(t *testing.T) {
	q := testBrowserQueue(t)
	q.idle, q.maxHold = time.Minute, 120*time.Millisecond
	if err := q.acquire(context.Background(), "A", "A", nil); err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() { got <- q.acquire(context.Background(), "B", "B", nil) }()
	stop := time.After(400 * time.Millisecond)
	for {
		select {
		case err := <-got:
			if err != nil {
				t.Fatal(err)
			}
			if h := q.status().Holder; h != "B" {
				t.Fatalf("holder = %q, want B", h)
			}
			return
		case <-stop:
			t.Fatal("B never got the browser from an order past its longest hold")
		case <-time.After(10 * time.Millisecond):
			// A keeps busy: a touch, as its next call would be, but never a
			// place in line once B has the browser.
			q.mu.Lock()
			if q.holder != nil && q.holder.owner == "A" {
				q.holder.touched = time.Now()
			}
			q.mu.Unlock()
		}
	}
}

// Nobody waits for ever: past the longest wait the call is refused with the
// holder's name, and the waiter is out of the line.
func TestBrowserQueueGivesUpAfterLongestWait(t *testing.T) {
	q := testBrowserQueue(t)
	q.maxWait = 60 * time.Millisecond
	if err := q.acquire(context.Background(), "A", "order A", nil); err != nil {
		t.Fatal(err)
	}
	err := q.acquire(context.Background(), "B", "B", nil)
	if err == nil || !strings.Contains(err.Error(), "order A") {
		t.Fatalf("err = %v, want a refusal naming order A", err)
	}
	if n := waitingCount(q); n != 0 {
		t.Fatalf("%d still waiting after giving up", n)
	}
}

// Two processes share a browser through its lock file: two queues on the
// same file stand in for them here.
func TestBrowserQueueKeepsTwoProcessesInLine(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "browser-chrome.lock")
	p1, p2 := newBrowserQueue("chrome", lock), newBrowserQueue("chrome", lock)
	for _, q := range []*browserQueue{p1, p2} {
		q.idle, q.maxHold, q.maxWait = time.Minute, time.Hour, 5*time.Second
	}
	if err := p1.acquire(context.Background(), "A", "A", nil); err != nil {
		t.Fatal(err)
	}
	var notes []string
	var mu sync.Mutex
	got := make(chan error, 1)
	go func() {
		got <- p2.acquire(context.Background(), "B", "B", func(s string) { mu.Lock(); notes = append(notes, s); mu.Unlock() })
	}()
	select {
	case err := <-got:
		t.Fatalf("B took the browser while another process held it (%v)", err)
	case <-time.After(400 * time.Millisecond):
	}
	p1.release("A")
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B never got the browser after the other process let go")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notes) == 0 || !strings.Contains(notes[0], "another SuperLeo process") {
		t.Fatalf("B was not told it waits on another process: %q", notes)
	}
	p2.release("B")
}

// Through the gate: two turns calling browser tools at once are serialised
// by the guard, the waiting turn hears 排队中 on its event stream, and the
// turn ending is what lets the next one in.
func TestBrowserGuardQueuesTurnsThroughTheGate(t *testing.T) {
	q := testBrowserQueue(t)
	b := &browserRuntime{mode: BrowserModeOwn, queue: q}
	s := &Service{browser: b}
	gate := NewToolGate(false, "")
	gate.Guard(BrowserTool(""), b.takeBrowser)
	decide := gate.Handler()
	call := func(ctx context.Context) (*agent.PermissionResponse, error) {
		return decide(ctx, agent.PermissionRequest{ToolName: BrowserTool("open"), ToolArgs: map[string]any{"url": "https://example.com"}})
	}

	ctxA, endA := s.withBrowserTurn(context.Background(), "chat-A", nil)
	if r, err := call(ctxA); err != nil || !r.Allowed {
		t.Fatalf("A: %+v %v", r, err)
	}

	var mu sync.Mutex
	var events []string
	ctxB, endB := s.withBrowserTurn(context.Background(), "hive-task-B", func(ev *agent.Event) {
		mu.Lock()
		events = append(events, string(ev.Type)+": "+ev.Content)
		mu.Unlock()
	})
	defer endB()
	got := make(chan *agent.PermissionResponse, 1)
	go func() { r, _ := call(ctxB); got <- r }()
	waitForQueue(t, "B to queue", func() bool { return waitingCount(q) == 1 })
	select {
	case r := <-got:
		t.Fatalf("B's browser call went through while A's turn held the browser: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}

	endA()
	r := <-got
	if r == nil || !r.Allowed {
		t.Fatalf("B after A ended: %+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 || !strings.Contains(events[0], "state_update: 排队中") || !strings.Contains(events[0], "chat-A") {
		t.Fatalf("B's events = %q, want a 排队中 state update naming chat-A", events)
	}

	// A non-browser call never queues.
	ctxC, endC := s.withBrowserTurn(context.Background(), "chat-C", nil)
	defer endC()
	if r, err := decide(ctxC, agent.PermissionRequest{ToolName: "fetch_url", ReadOnly: true}); err != nil || !r.Allowed {
		t.Fatalf("fetch_url: %+v %v", r, err)
	}
}

// A turn cancelled while its browser call waits gets a refusal for that call,
// and is out of the line.
func TestBrowserGuardCancelledTurnLeavesTheLine(t *testing.T) {
	q := testBrowserQueue(t)
	b := &browserRuntime{mode: BrowserModeOwn, queue: q}
	s := &Service{browser: b}
	gate := NewToolGate(false, "")
	gate.Guard(BrowserTool(""), b.takeBrowser)
	decide := gate.Handler()

	ctxA, endA := s.withBrowserTurn(context.Background(), "A", nil)
	defer endA()
	if r, _ := decide(ctxA, agent.PermissionRequest{ToolName: BrowserTool("open")}); !r.Allowed {
		t.Fatalf("A refused: %+v", r)
	}
	base, cancel := context.WithCancel(context.Background())
	ctxB, endB := s.withBrowserTurn(base, "B", nil)
	defer endB()
	got := make(chan *agent.PermissionResponse, 1)
	go func() { r, _ := decide(ctxB, agent.PermissionRequest{ToolName: BrowserTool("open")}); got <- r }()
	waitForQueue(t, "B to queue", func() bool { return waitingCount(q) == 1 })
	cancel()
	if r := <-got; r == nil || r.Allowed {
		t.Fatalf("a cancelled turn's browser call was allowed: %+v", r)
	}
	if n := waitingCount(q); n != 0 {
		t.Fatalf("%d still waiting after B was cancelled", n)
	}
}

// A holder in another process that stopped using the browser lets go once a
// process here says it wants it, though nobody in its own process waits:
// otherwise the two would wait on each other until the longest wait.
func TestBrowserQueueStuckHolderInAnotherProcessLetsGo(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "browser-chrome.lock")
	p1, p2 := newBrowserQueue("chrome", lock), newBrowserQueue("chrome", lock)
	p1.idle, p1.maxHold, p1.maxWait = 80*time.Millisecond, time.Hour, 5*time.Second
	p2.idle, p2.maxHold, p2.maxWait = time.Minute, time.Hour, 5*time.Second
	if err := p1.acquire(context.Background(), "stuck", "stuck", nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := make(chan error, 1)
	go func() { got <- p2.acquire(context.Background(), "B", "B", nil) }()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the other process's idle holder never let go")
	}
	if waited := time.Since(start); waited < p1.idle/2 {
		t.Fatalf("B went ahead after %s, before the holder went idle", waited)
	}
	if h := p1.status().Holder; h != "" {
		t.Fatalf("the idle holder still holds it in its own process: %q", h)
	}
	p2.release("B")
}

// A worker's order waiting for its browser shows as queued on the queen's
// board, and as working again once it has it.
func TestQueuedAndFreeStateUpdatesReadAsPhases(t *testing.T) {
	if !strings.HasPrefix(BrowserQueued+": browser chrome is in use by chat-A; waiting for it", BrowserQueued) {
		t.Fatal("the queued notice does not start with its marker")
	}
	q := testBrowserQueue(t)
	if err := q.acquire(context.Background(), "A", "chat-A", nil); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var notes []string
	done := make(chan error, 1)
	go func() {
		done <- q.acquire(context.Background(), "B", "B", func(s string) { mu.Lock(); notes = append(notes, s); mu.Unlock() })
	}()
	waitForQueue(t, "B to queue", func() bool { return waitingCount(q) == 1 })
	q.release("A")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 2 || !strings.HasPrefix(notes[0], BrowserQueued) || !strings.HasPrefix(notes[1], BrowserFree) {
		t.Fatalf("notes %q, want a %s notice then a %s one", notes, BrowserQueued, BrowserFree)
	}
}
