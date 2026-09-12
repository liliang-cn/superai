package backend

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func yoloGate(t *testing.T) (*ToolGate, *fakeClock) {
	t.Helper()
	clk := &fakeClock{at: time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)}
	g := NewToolGate(true, filepath.Join(t.TempDir(), "audit.jsonl"))
	g.now = clk.now
	return g, clk
}

type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time      { return c.at }
func (c *fakeClock) add(d time.Duration) { c.at = c.at.Add(d) }

// The point of YOLO: a run of commands goes through without a prompt each time.
func TestYoloAllowsWithoutAsking(t *testing.T) {
	g, _ := yoloGate(t)
	// No approver at all — without YOLO this denies immediately.
	g.StartYolo()

	resp, err := g.decide(context.Background(), bashReq("rm -rf /tmp/x"))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !resp.Allowed {
		t.Fatalf("YOLO did not allow: %s", resp.Reason)
	}
}

// It must be possible to stop it before the clock does.
func TestYoloCanBeStopped(t *testing.T) {
	g, _ := yoloGate(t)
	g.StartYolo()
	g.StopYolo()
	if resp, _ := g.decide(context.Background(), bashReq("rm -rf /tmp/x")); resp.Allowed {
		t.Fatal("StopYolo did not end it")
	}
}

// Everything YOLO waves through still lands in the audit log — the whole
// reason to keep one is answering "what ran while I was not looking".
func TestYoloIsStillAudited(t *testing.T) {
	g, _ := yoloGate(t)
	g.StartYolo()
	if _, err := g.decide(context.Background(), bashReq("rm -rf /tmp/x")); err != nil {
		t.Fatalf("decide: %v", err)
	}
	entries := g.Log(10)
	if len(entries) != 1 {
		t.Fatalf("audit has %d entries, want 1", len(entries))
	}
	if !entries[0].Allowed {
		t.Error("the allowed call was logged as denied")
	}
	if entries[0].DecidedBy != DecidedByYolo {
		t.Errorf("decided_by = %q, want %q — the log must say it was YOLO and not a person",
			entries[0].DecidedBy, DecidedByYolo)
	}
}

// Status is what the UI shows. A mode you cannot see is on is a mode you forget
// is on, and this one has no deadline to end it.
func TestYoloStatusStaysOnUntilStopped(t *testing.T) {
	g, clk := yoloGate(t)
	g.StartYolo()

	on, since := g.YoloStatus()
	if !on {
		t.Fatal("status says off right after starting")
	}
	if !since.Equal(clk.at) {
		t.Errorf("since = %v, want %v", since, clk.at)
	}

	clk.add(9 * time.Hour)
	if on, _ := g.YoloStatus(); !on {
		t.Error("status went off on its own — YOLO ends when it is stopped, not when a clock says so")
	}
	if resp, _ := g.decide(context.Background(), bashReq("rm -rf /tmp/x")); !resp.Allowed {
		t.Error("the gate started asking again on its own")
	}

	g.StopYolo()
	if on, _ := g.YoloStatus(); on {
		t.Error("status still says on after being stopped")
	}
}

// The gate being switched off in Settings is a different thing, and it wins:
// there is nothing for YOLO to relax.
func TestYoloIsIrrelevantWhenTheGateIsOff(t *testing.T) {
	g := NewToolGate(false, "")
	g.StartYolo()
	resp, _ := g.decide(context.Background(), bashReq("rm -rf /tmp/x"))
	if !resp.Allowed {
		t.Fatal("a disabled gate denied")
	}
	if resp.Reason == "" {
		t.Error("no reason given")
	}
}
