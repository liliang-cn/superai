package backend

import (
	"context"
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

// Stopping the push loop writes the totals once more and then nothing: a
// shutdown keeps the last seconds, and a removed data directory stays removed.
func TestStoppingThePulseWritesOnceAndThenNever(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pulse-today.json")
	p := NewPulse()
	p.Keep(path)
	stop := p.Start(context.Background(), func(string, map[string]any) {})
	p.mu.Lock()
	p.tokens = 42
	p.mu.Unlock()
	stop()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no totals written on stop: %v", err)
	}
	var kept struct {
		Tokens int `json:"tokens"`
	}
	if json.Unmarshal(raw, &kept); kept.Tokens != 42 {
		t.Fatalf("kept %s; want tokens 42", raw)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * pulseFrameEvery)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("something wrote after stop: %v", err)
	}
}
