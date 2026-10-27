package app

import (
	"log"
	"os"
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

// Every step of a model call on the wire is in the turn's timeline; the log
// keeps the ones worth reading.
func TestAModelCallsWireIsInTheTimelineAndItsTroubleInTheLog(t *testing.T) {
	var logs strings.Builder
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	j := &turnJournal{turns: map[string]*TurnTrace{}}
	id := "0123456789ab"
	j.record("chat:turn", map[string]any{"requestId": id, "message": "hi"})
	for _, w := range []map[string]any{
		{"kind": "request", "attempt": 1},
		{"kind": "response", "status": 429, "attempt": 1},
		{"kind": "retry", "wait_ms": 2000},
		{"kind": "request", "attempt": 2},
		{"kind": "response", "status": 200, "attempt": 2},
		{"kind": "slot", "wait_ms": 10},
		{"kind": "waiting", "wait_ms": 20000},
		{"kind": "first_byte"},
	} {
		j.record("chat:event", map[string]any{"requestId": id, "type": "model_wire", "content": "step " + w["kind"].(string), "wire": w})
	}
	if n := len(j.turns[id].Events); n != 9 {
		t.Fatalf("%d events in the timeline", n)
	}
	got := logs.String()
	if strings.Count(got, "model: step") != 3 || !strings.Contains(got, "step retry") || !strings.Contains(got, "step waiting") || strings.Contains(got, "step first_byte") {
		t.Fatal(got)
	}
}
