package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Stopping a turn.
//
// The registry is the whole mechanism: SendChat owns a context nobody else can
// reach, so unless the cancel is parked somewhere a Wails call can find it, a
// running turn cannot be stopped at all. These tests pin the three things that
// makes it: the id has to work, it has to stop working once the turn is over,
// and neither may be racy — a stop is pressed while the run's own goroutine is
// tearing itself down, which is precisely when the two touch the same entry.

func TestCancelChatStopsTheRunAndSaysSo(t *testing.T) {
	a := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := "req-1"
	a.trackRun(id, cancel)

	if got := a.CancelChat(id); got != "ok" {
		t.Fatalf("CancelChat(%q) = %q, want ok", id, got)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("CancelChat answered ok without cancelling the context the turn runs on")
	}
	// The flag is what SendChat reads to decide between chat:cancelled and
	// chat:error, so it has to survive until the turn unregisters itself.
	if !a.runCancelled(id) {
		t.Error("a cancelled run does not report itself as cancelled, so its turn would be reported as a failure")
	}

	a.untrackRun(id)
	if a.runCancelled(id) {
		t.Error("a forgotten run still reports as cancelled")
	}
}

func TestCancelChatOnUnknownIDExplainsItself(t *testing.T) {
	a := NewApp()

	// Never started.
	got := a.CancelChat("nobody")
	if got == "ok" {
		t.Fatal("CancelChat claimed to stop a request that was never started")
	}
	if !strings.Contains(got, "not running") {
		t.Errorf("CancelChat on an unknown id = %q, want a sentence saying it is not running", got)
	}

	// Started and already over: the same answer, because from the caller's
	// side it is the same situation — nothing to stop.
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.trackRun("done", cancel)
	a.untrackRun("done")
	if got := a.CancelChat("done"); got == "ok" {
		t.Error("CancelChat claimed to stop a finished request")
	}
}

func TestCancelAllChatsStopsEveryRun(t *testing.T) {
	a := NewApp()

	if got := a.CancelAllChats(); got != "nothing is running" {
		t.Errorf("CancelAllChats with an empty registry = %q", got)
	}

	ctxs := make([]context.Context, 3)
	for i := range ctxs {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctxs[i] = ctx
		a.trackRun(string(rune('a'+i)), cancel)
	}

	if got := a.CancelAllChats(); !strings.HasPrefix(got, "ok") {
		t.Fatalf("CancelAllChats = %q, want an ok", got)
	}
	for i, ctx := range ctxs {
		select {
		case <-ctx.Done():
		default:
			t.Errorf("run %d was left running by CancelAllChats", i)
		}
	}
}

// TestSendChatCancelledEndsAsCancelledNotError is the contract the transcript
// is written against. A stopped turn does not fail — agent-go closes the stream
// with a nil error, exactly as a finished one does — so a backend that infers
// the outcome from the error would paint a red failure over an answer the user
// deliberately interrupted.
func TestSendChatCancelledEndsAsCancelledNotError(t *testing.T) {
	// Long enough that the stop always lands while the provider is still
	// thinking, which is the only interesting moment.
	a, log := newChatApp(t, 3*time.Second)

	id := a.SendChat("s", "请回答 ALPHA", nil)
	if id == "" {
		t.Fatal("SendChat started nothing")
	}
	// Wait until the turn is actually in flight before stopping it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(log.snapshot()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := a.CancelChat(id); got != "ok" {
		t.Fatalf("CancelChat = %q, want ok", got)
	}

	var terminal chatEvent
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range log.snapshot() {
			switch e.name {
			case "chat:cancelled", "chat:done", "chat:error":
				terminal = e
			}
		}
		if terminal.name != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if terminal.name != "chat:cancelled" {
		t.Fatalf("a stopped turn ended with %q, want chat:cancelled", terminal.name)
	}
	if got := terminal.requestID(); got != id {
		t.Errorf("chat:cancelled requestId = %q, want %q", got, id)
	}
	// And the registry must be clean, or the next stop for a reused id would
	// cancel a context belonging to nothing.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.runMu.Lock()
		n := len(a.runs)
		a.runMu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the stopped turn never unregistered itself")
}

// TestCancelChatRegistryIsRaceFree runs the real interleaving: turns registering
// and unregistering themselves while stops arrive for ids that may or may not
// still exist. Meaningful under -race; a plain run only proves it does not
// deadlock or panic on a nil map.
func TestCancelChatRegistryIsRaceFree(t *testing.T) {
	a := NewApp()

	const n = 64
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "req-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}

	for _, id := range ids {
		wg.Add(2)
		go func(id string) { // the turn
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			a.trackRun(id, cancel)
			<-ctx.Done()
			_ = a.runCancelled(id)
			a.untrackRun(id)
			cancel()
		}(id)
		go func(id string) { // the user, pressing stop
			defer wg.Done()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if a.CancelChat(id) == "ok" {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Errorf("never managed to stop %s", id)
		}(id)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stopping runs concurrently deadlocked")
	}

	a.runMu.Lock()
	left := len(a.runs)
	a.runMu.Unlock()
	if left != 0 {
		t.Errorf("%d entries left in the registry; a finished turn must not keep its cancel alive", left)
	}
}
