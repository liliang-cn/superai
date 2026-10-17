package app

import (
	"context"
	"testing"
	"time"
)

// busyWith tracks a turn running in a conversation, the way a call turn on the
// queen is, and returns what ends it.
func busyWith(a *App, requestID, sessionID string) func() {
	_, cancel := context.WithCancel(context.Background())
	a.trackRun(requestID, cancel)
	a.runSession(requestID, sessionID)
	return func() { cancel(); a.untrackRun(requestID) }
}

func (e *engineEvents) said(requestID, content string) bool {
	for _, p := range e.all("chat:event", requestID) {
		if p["type"] == "state_update" && p["content"] == content {
			return true
		}
	}
	return false
}

// The live case: a call turn running on the queen, and a typed message in the
// same conversation. It used to go to an idle worker, which has no agent link
// and told the person the Mac was not connected. It waits for the queen now,
// says so, and is answered by her.
func TestATypedTurnWaitsForTheQueenRatherThanGoingToAWorker(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.QueenEngine = ""
	const conv = "hive-console-web"
	end := busyWith(h.a, "call-1", conv)

	id := h.a.SendChat(conv, "顺便把 Mac 上的 opencode 也更新一下", nil)
	if !h.ev.said(id, queuedNote) {
		t.Fatalf("no %q for the waiting turn: %v", queuedNote, h.ev.all("chat:event", id))
	}
	time.Sleep(100 * time.Millisecond)
	if len(h.owned()) != 0 || h.ev.find("chat:done", id) != nil {
		t.Fatalf("started while the conversation was busy: %v", h.owned())
	}
	if h.a.queuedTurns(conv) != 1 {
		t.Fatalf("queued: %d", h.a.queuedTurns(conv))
	}

	end()
	if name, _ := h.ev.wait(t, id); name != "chat:done" {
		t.Fatalf("ended with %s", name)
	}
	if got := h.owned(); len(got) != 1 || got[0].session != conv || got[0].standIn != "" {
		t.Fatalf("not the queen's own turn: %v", got)
	}
	waitFor(t, "the line to empty", func() bool { return h.a.queuedTurns(conv) == 0 })
	h.a.runMu.Lock()
	_, left := h.a.runs[id]
	h.a.runMu.Unlock()
	if left {
		t.Fatal("the waiting entry outlived the turn")
	}
}

// With a queen engine (claude.mac live; claude here), the turn that waited
// goes to the engine, as it would have with the conversation free.
func TestAWaitingTurnGoesToTheQueenEngineOnceTheConversationIsFree(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	const conv = "hive-console-web"
	end := busyWith(h.a, "call-1", conv)

	id := h.a.SendChat(conv, "现在 hive 里有哪些成员？", nil)
	if !h.ev.said(id, queuedNote) {
		t.Fatal("not queued behind the running turn")
	}
	end()
	name, done := h.ev.wait(t, id)
	if name != "chat:done" || done["engine"] != "claude" {
		t.Fatalf("%s %v", name, done)
	}
	if len(h.owned()) != 0 {
		t.Fatalf("SuperAI's model answered instead: %v", h.owned())
	}
}

// Turns wait in the order they were asked, a call turn among them, and one
// stopped while it waits ends there without running.
func TestWaitingTurnsGoInOrderAndAStoppedOneNeverRuns(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.QueenEngine = ""
	const conv = "c1"
	end := busyWith(h.a, "first", conv)

	a := h.a.SendChat(conv, "two", nil)
	b := h.a.SendChat(conv, "three", nil)
	c := h.a.SendCallChat(conv, "four", nil)
	for _, id := range []string{a, b, c} {
		if !h.ev.said(id, queuedNote) {
			t.Fatalf("%s not queued", id)
		}
	}
	if got := h.a.CancelChat(b); got != "ok" {
		t.Fatalf("stop a waiting turn: %q", got)
	}
	if name, _ := h.ev.wait(t, b); name != "chat:cancelled" {
		t.Fatalf("stopped turn ended with %s", name)
	}

	// An own turn here is not tracked (ownTurnFn), so each one frees the
	// conversation as soon as it has answered.
	end()
	h.ev.wait(t, a)
	h.ev.wait(t, c)
	got := h.owned()
	if len(got) != 2 || got[0].message != "two" || got[1].message != "four" {
		t.Fatalf("order: %v", got)
	}
	waitFor(t, "the line to empty", func() bool { return h.a.queuedTurns(conv) == 0 })
}

// With the conversation free, nothing waits; and a turn in another
// conversation does not wait for this one.
func TestAFreeConversationStartsATurnAtOnce(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.QueenEngine = ""
	end := busyWith(h.a, "elsewhere", "c2")
	defer end()

	id := h.a.SendChat("c1", "hi", nil)
	if h.ev.said(id, queuedNote) {
		t.Fatal("queued with the conversation free")
	}
	if name, _ := h.ev.wait(t, id); name != "chat:done" {
		t.Fatalf("ended with %s", name)
	}
	if h.a.queueTurn("x", "", func() {}) {
		t.Fatal("a turn with no conversation waited")
	}
}
