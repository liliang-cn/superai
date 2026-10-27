package app

import (
	"strings"
	"testing"
	"time"
)

// A turn that stops saying anything is logged once as quiet, with what it
// last did, and again only after it has said something and gone quiet anew.
func TestATurnThatGoesQuietIsSaidOnce(t *testing.T) {
	j := &turnJournal{turns: map[string]*TurnTrace{}}
	j.record("chat:turn", map[string]any{"requestId": "abcdef123456", "sessionId": "s", "message": "hi"})
	j.record("chat:event", map[string]any{"requestId": "abcdef123456", "type": "state_update", "engine": "claude", "content": "starting Claude Code…"})
	later := time.Now().Add(2 * journalQuietFor)
	got := j.sweepQuiet(later)
	if len(got) != 1 || !strings.Contains(got[0], "turn abcdef12 quiet for") || !strings.Contains(got[0], "starting Claude Code…") || !strings.Contains(got[0], "[claude]") {
		t.Fatalf("%q", got)
	}
	if again := j.sweepQuiet(later.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("said twice: %q", again)
	}
	j.record("chat:done", map[string]any{"requestId": "abcdef123456", "final": "ok", "engine": "claude"})
	if done := j.sweepQuiet(later.Add(time.Hour)); len(done) != 0 {
		t.Fatalf("a finished turn called quiet: %q", done)
	}
	if tr := (&App{turnLog: turnJournal{}}).TurnTraces(5); len(tr) != 0 {
		t.Fatalf("a fresh app has turns: %+v", tr)
	}
}
