package main

import (
	"sort"
	"strings"
	"testing"
)

// A preview must be the turn a send would make, and must not be a send.
//
// Both halves matter. The first is why it exists at all — a preview assembled
// from options the chat path does not use is a picture of a run nobody is
// about to start. The second is the promise on the button: looking at what the
// agent is about to be told must not write a session, and must not ask the
// model anything, including the constraint-extraction call the real run makes.
func TestPreviewPromptShowsTheTurnWithoutSendingIt(t *testing.T) {
	a, _ := newChatApp(t, 0)

	const session = "preview-session"
	const goal = "Write a haiku about prompt caches"

	p := a.PreviewPrompt(session, goal)
	if p.Error != "" {
		t.Fatalf("preview failed: %s", p.Error)
	}
	if p.SessionID != session {
		t.Errorf("preview is against session %q, want %q", p.SessionID, session)
	}
	if strings.TrimSpace(p.SystemPrompt) == "" {
		t.Error("no system prompt in the preview")
	}
	if len(p.Messages) == 0 {
		t.Fatal("no messages in the preview")
	}
	if p.Messages[0].Role != "system" {
		t.Errorf("first message is %q, want system", p.Messages[0].Role)
	}
	if p.EstimatedTokens <= 0 {
		t.Errorf("estimated tokens = %d, want a positive estimate", p.EstimatedTokens)
	}

	var sawGoal bool
	for _, m := range p.Messages {
		if m.Role == "user" && strings.Contains(m.Content, goal) {
			sawGoal = true
		}
	}
	if !sawGoal {
		t.Error("the goal is not in the previewed messages")
	}

	// The app puts its whole catalogue in the schema, so a preview with no
	// tools in it is a preview of a turn that could do nothing.
	if len(p.Tools) == 0 {
		t.Fatal("no tools in the preview")
	}
	if !sort.StringsAreSorted(p.Tools) {
		t.Errorf("tool list is not byte-stable across a run: %v", p.Tools)
	}

	// The real run resolves its constraints by asking the model; a preview
	// that reported them would have made the call it promised not to.
	if !p.ConstraintExtractionSkipped {
		t.Error("preview claims to know the run's extracted constraints")
	}
	if p.ConstraintsDeclared {
		t.Error("preview reports declared constraints for a plain chat turn")
	}

	// Nothing was persisted: no conversation, and a second preview assembles
	// exactly the same turn rather than one with the first ask in its history.
	if turns := a.ChatHistory(session); len(turns) != 0 {
		t.Errorf("preview wrote %d turn(s) of history", len(turns))
	}
	again := a.PreviewPrompt(session, goal)
	if again.Error != "" {
		t.Fatalf("second preview failed: %s", again.Error)
	}
	if len(again.Messages) != len(p.Messages) {
		t.Errorf("second preview has %d messages, first had %d — the first one left something behind",
			len(again.Messages), len(p.Messages))
	}
}

// With no backend there is nothing to assemble, and the reason belongs on the
// result: a preview is a read, so the panel shows why rather than throwing.
func TestPreviewPromptWithoutBackendExplainsItself(t *testing.T) {
	a := NewApp()
	a.buildErr = "no LLM configured"

	p := a.PreviewPrompt("s", "anything")
	if p.Error == "" {
		t.Fatal("no error reported for a preview with no service")
	}
	if !strings.Contains(p.Error, "no LLM configured") {
		t.Errorf("error %q does not carry the build failure", p.Error)
	}
	if p.Messages == nil || p.Tools == nil || p.Deliverables == nil {
		t.Error("empty preview has nil slices; the panel would have to guard every one")
	}
}
