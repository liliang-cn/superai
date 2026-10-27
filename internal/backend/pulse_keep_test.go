package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Today's tokens and rounds survive a restart; yesterday's do not, and the
// count begins again when the day changes under a running process.
func TestTheMetersTotalsAreTodaysAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pulse-today.json")
	first := NewPulse()
	first.Keep(path)
	first.mu.Lock()
	first.tokens, first.rounds, first.calls = 14500, 2, 7
	first.mu.Unlock()
	first.keep(false)

	again := NewPulse()
	again.Keep(path)
	again.mu.Lock()
	f := again.frameLocked(time.Now())
	again.mu.Unlock()
	if f.Tokens != 14500 || f.Rounds != 2 || f.Calls != 7 {
		t.Fatalf("after a restart: %d tokens, %d rounds, %d calls", f.Tokens, f.Rounds, f.Calls)
	}

	raw, _ := json.Marshal(pulseTotals{Day: "2001-01-01", Tokens: 99})
	_ = os.WriteFile(path, raw, 0o644)
	stale := NewPulse()
	stale.Keep(path)
	if stale.tokens != 0 {
		t.Fatal("yesterday's tokens counted today")
	}

	again.mu.Lock()
	again.day = "2001-01-01"
	again.rollLocked(time.Now())
	n := again.tokens
	again.mu.Unlock()
	if n != 0 {
		t.Fatal("the count did not begin again at midnight")
	}
}
