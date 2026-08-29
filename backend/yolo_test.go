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
	g.StartYolo(30 * time.Minute)

	resp, err := g.decide(context.Background(), bashReq("rm -rf /tmp/x"))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !resp.Allowed {
		t.Fatalf("YOLO did not allow: %s", resp.Reason)
	}
}

// The one that matters. A YOLO that does not end is not a mode, it is a new
// default — and it would undo the gate entirely.
func TestYoloExpiresOnItsOwn(t *testing.T) {
	g, clk := yoloGate(t)
	g.StartYolo(30 * time.Minute)

	clk.add(29 * time.Minute)
	if resp, _ := g.decide(context.Background(), bashReq("rm -rf /tmp/x")); !resp.Allowed {
		t.Fatal("YOLO ended early")
	}

	clk.add(2 * time.Minute) // now past the window
	resp, _ := g.decide(context.Background(), bashReq("rm -rf /tmp/x"))
	if resp.Allowed {
		t.Fatal("YOLO outlived its window — the gate is off for good")
	}
}

// It must be possible to stop it before the clock does.
func TestYoloCanBeStopped(t *testing.T) {
	g, _ := yoloGate(t)
	g.StartYolo(time.Hour)
	g.StopYolo()
	if resp, _ := g.decide(context.Background(), bashReq("rm -rf /tmp/x")); resp.Allowed {
		t.Fatal("StopYolo did not end it")
	}
}

// An unbounded window is the whole failure mode, so it cannot be asked for.
func TestYoloRefusesAnUnboundedWindow(t *testing.T) {
	g, _ := yoloGate(t)
	for _, d := range []time.Duration{0, -time.Minute, 999 * time.Hour} {
		got := g.StartYolo(d)
		if got <= 0 || got > MaxYoloWindow {
			t.Errorf("StartYolo(%v) gave a window of %v, want it clamped into (0, %v]", d, got, MaxYoloWindow)
		}
	}
}

// Everything YOLO waves through still lands in the audit log — the whole
// reason to keep one is answering "what ran while I was not looking".
func TestYoloIsStillAudited(t *testing.T) {
	g, _ := yoloGate(t)
	g.StartYolo(time.Hour)
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
// is on.
func TestYoloStatusReportsTheRemainingTime(t *testing.T) {
	g, clk := yoloGate(t)
	g.StartYolo(30 * time.Minute)

	on, until := g.YoloStatus()
	if !on {
		t.Fatal("status says off right after starting")
	}
	if got := until.Sub(clk.at); got != 30*time.Minute {
		t.Errorf("remaining = %v, want 30m", got)
	}

	clk.add(31 * time.Minute)
	if on, _ := g.YoloStatus(); on {
		t.Error("status still says on after the window passed")
	}
}

// The gate being switched off in Settings is a different thing, and it wins:
// there is nothing for YOLO to relax.
func TestYoloIsIrrelevantWhenTheGateIsOff(t *testing.T) {
	g := NewToolGate(false, "")
	g.StartYolo(time.Hour)
	resp, _ := g.decide(context.Background(), bashReq("rm -rf /tmp/x"))
	if !resp.Allowed {
		t.Fatal("a disabled gate denied")
	}
	if resp.Reason == "" {
		t.Error("no reason given")
	}
}
