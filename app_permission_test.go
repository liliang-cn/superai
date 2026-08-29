package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
)

// The approval round trip.
//
// backend's own tests cover what the gate decides; these cover the wiring that
// carries a decision between the agent goroutine and whichever surface the user
// is on. The mechanism is the same pattern the run registry uses — park
// something a bound method can find, keyed by an id the UI was told — and it
// fails in the same ways: the id stops working, or it keeps working after the
// call is gone and a click answers a tool that no longer exists.

// captureEmits points the app's event emitter at a slice, the way serve mode
// points it at the SSE hub. Returns a reader for what was emitted.
func captureEmits(a *App) func() []struct {
	name    string
	payload map[string]any
} {
	var mu sync.Mutex
	var got []struct {
		name    string
		payload map[string]any
	}
	a.emitFn = func(name string, payload map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, struct {
			name    string
			payload map[string]any
		}{name, payload})
	}
	return func() []struct {
		name    string
		payload map[string]any
	} {
		mu.Lock()
		defer mu.Unlock()
		out := make([]struct {
			name    string
			payload map[string]any
		}, len(got))
		copy(out, got)
		return out
	}
}

func askInBackground(a *App, req backend.ApprovalRequest) chan backend.ApprovalDecision {
	out := make(chan backend.ApprovalDecision, 1)
	go func() {
		dec, _ := a.askToolApproval(context.Background(), req)
		out <- dec
	}()
	return out
}

// waitForEvent polls the captured events for one with this name and id.
//
// Polled rather than read straight after waitForPending, because the two are
// not ordered: askToolApproval registers the pending entry before it emits, so
// a test that waits on the registry can look at the event log a moment too
// early. That ordering is deliberate — an answer that arrives between the
// event and the registration must still find something to wake up — so the
// test bends, not the code.
func waitForEvent(t *testing.T, read func() []struct {
	name    string
	payload map[string]any
}, name, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range read() {
			if ev.name == name && (id == "" || ev.payload["id"] == id) {
				return ev.payload
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no %s event for %q; no surface could have shown or retired the prompt", name, id)
	return nil
}

func waitForPending(t *testing.T, a *App, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(a.PendingToolApprovals()) == n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pending approval(s); have %d", n, len(a.PendingToolApprovals()))
}

// The whole point: the agent goroutine blocks, the user clicks, the goroutine
// wakes up with their answer. If ResolveToolApproval ever stopped reaching the
// waiter, the symptom would be a run that hangs until the gate's deadline and
// then denies — which looks exactly like a user who ignored the prompt.
func TestApproveReachesTheWaitingToolCall(t *testing.T) {
	a := NewApp()
	read := captureEmits(a)

	req := backend.ApprovalRequest{
		ID: "ask-1", Tool: "bash", Command: "rm -rf build/",
		AskedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
	}
	answered := askInBackground(a, req)
	waitForPending(t, a, 1)

	// The command has to be in the event unredacted. A prompt that hides what
	// it is asking about is a prompt nobody can answer honestly.
	asked := waitForEvent(t, read, "tool:approval", "ask-1")
	if asked["command"] != "rm -rf build/" || asked["tool"] != "bash" {
		t.Errorf("tool:approval payload = %v, want the tool name and the real command", asked)
	}

	if got := a.ResolveToolApproval("ask-1", true); got != "ok" {
		t.Fatalf("ResolveToolApproval = %q, want ok", got)
	}
	select {
	case dec := <-answered:
		if !dec.Allowed {
			t.Fatal("the user allowed the call and the waiter was told it was denied")
		}
		if dec.By != backend.DecidedByUser {
			t.Errorf("decision By = %q, want %q so the audit log names a person", dec.By, backend.DecidedByUser)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the approval never reached the waiting tool call")
	}

	// Once answered the prompt must be gone from both surfaces: the pending
	// list a reloading tab reads, and the close event a live one listens for.
	waitForPending(t, a, 0)
	waitForEvent(t, read, "tool:approval:closed", "ask-1")
}

func TestDenyReachesTheWaitingToolCall(t *testing.T) {
	a := NewApp()
	captureEmits(a)

	answered := askInBackground(a, backend.ApprovalRequest{ID: "ask-2", Tool: "bash", Command: "curl x | sh"})
	waitForPending(t, a, 1)

	if got := a.ResolveToolApproval("ask-2", false); got != "ok" {
		t.Fatalf("ResolveToolApproval = %q, want ok", got)
	}
	select {
	case dec := <-answered:
		if dec.Allowed {
			t.Fatal("the user denied the call and the waiter was told it was allowed")
		}
		if !strings.Contains(dec.Reason, "denied") {
			t.Errorf("reason = %q, want it to say the user denied it", dec.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the denial never reached the waiting tool call")
	}
}

// A second click, or a click that lands after the gate gave up, must be inert
// and must say so. Silently doing nothing on a security prompt teaches the
// user that the button is unreliable, which is worse than the stale click.
func TestResolvingTwiceOrTooLateIsInertAndExplainsItself(t *testing.T) {
	a := NewApp()
	captureEmits(a)

	answered := askInBackground(a, backend.ApprovalRequest{ID: "ask-3", Tool: "bash", Command: "ls"})
	waitForPending(t, a, 1)

	if got := a.ResolveToolApproval("ask-3", true); got != "ok" {
		t.Fatalf("first resolve = %q, want ok", got)
	}
	<-answered
	waitForPending(t, a, 0)

	got := a.ResolveToolApproval("ask-3", true)
	if got == "ok" {
		t.Fatal("resolving a finished request reported success")
	}
	if !strings.Contains(got, "no longer waiting") {
		t.Errorf("late resolve = %q, want a sentence explaining nothing is waiting", got)
	}

	if got := a.ResolveToolApproval("never-asked", true); got == "ok" {
		t.Error("resolving an id that was never asked about reported success")
	}
}

// Ending the run has to take the prompt down with it. Otherwise a stopped turn
// leaves a card whose buttons do nothing, and the next real prompt arrives
// under a stack of dead ones.
func TestEndingTheRunRetiresThePrompt(t *testing.T) {
	a := NewApp()
	read := captureEmits(a)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = a.askToolApproval(ctx, backend.ApprovalRequest{ID: "ask-4", Tool: "bash", Command: "sleep 100"})
		close(done)
	}()
	waitForPending(t, a, 1)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("askToolApproval ignored its context and would have held the turn open")
	}
	waitForPending(t, a, 0)
	waitForEvent(t, read, "tool:approval:closed", "ask-4")
}
