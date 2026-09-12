package backend

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// StreamLong must forward events as they happen, not hand back a transcript
// when the task is over — a window has nothing to draw otherwise, and on the
// tasks this exists for "over" is hours away.
func TestStreamLongForwardsEventsWhileItRuns(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	var seen int
	report, err := svc.StreamLong(context.Background(), "Say hello.",
		LongRunOptions{MaxSegments: 1, RoundsPerSegment: 1, TaskID: "long-1"},
		func(ev *agent.Event) { seen++ })
	if err != nil {
		t.Fatalf("StreamLong: %v", err)
	}
	if seen == 0 {
		t.Fatal("no events reached the caller")
	}
	if report == nil || report.TaskID != "long-1" {
		t.Fatalf("report did not carry the task id: %+v", report)
	}
	if report.Segments == 0 {
		t.Error("report claims no segments ran")
	}
}

// Cancelling stops it rather than hanging.
func TestStreamLongStopsOnCancel(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.StreamLong(ctx, "Work forever.",
			LongRunOptions{MaxSegments: 20, RoundsPerSegment: 2}, nil)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("StreamLong did not return after its context was cancelled")
	}
}

// A run nobody is watching cannot be asked. Its tool calls go through the
// gate without a prompt, are recorded as such, and the allowance ends with
// the segment.
func TestUnattendedRunsPassTheGate(t *testing.T) {
	// An audit path, because the assertion below reads the audit log: a gate
	// without one records nothing and would pass the test with an empty log.
	g := NewToolGate(true, filepath.Join(t.TempDir(), "audit.jsonl"))
	g.SetWait(50 * time.Millisecond)
	// No approver: an attended request is denied fast, which is the point of
	// the comparison below.
	req := agent.PermissionRequest{ToolName: "bash", ToolArgs: map[string]any{"command": "wc -w x"}, SessionID: "seg-1"}

	if res, _ := g.decide(context.Background(), req); res != nil && res.Allowed {
		t.Fatal("an attended request with nobody to ask must not be allowed")
	}
	g.Unattend("seg-1")
	res, err := g.decide(context.Background(), req)
	if err != nil || res == nil || !res.Allowed {
		t.Fatalf("unattended request denied: res=%+v err=%v", res, err)
	}
	audited := false
	for _, e := range g.Log(10) {
		if e.DecidedBy == DecidedByUnattended && e.Allowed {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("unattended allow not audited under its own name: %+v", g.Log(10))
	}
	g.Attend("seg-1")
	if res, _ := g.decide(context.Background(), req); res != nil && res.Allowed {
		t.Fatal("the allowance must end with the segment")
	}
}

// The observer marks only its own task's sessions, for as long as they run.
func TestUnattendedObserverScopesToItsTask(t *testing.T) {
	g := NewToolGate(true, "")
	o := &unattendedObserver{gate: g, taskID: "mine"}
	o.OnSegment(context.Background(), agent.SegmentInfo{TaskID: "mine", SessionID: "s1"})
	o.OnSegment(context.Background(), agent.SegmentInfo{TaskID: "theirs", SessionID: "s2"})
	if !g.isUnattended("s1") || g.isUnattended("s2") {
		t.Fatalf("scope wrong: s1=%v s2=%v", g.isUnattended("s1"), g.isUnattended("s2"))
	}
	o.OnSegment(context.Background(), agent.SegmentInfo{TaskID: "mine", SessionID: "s1", Ending: true})
	if g.isUnattended("s1") {
		t.Fatal("mark must clear when the segment ends")
	}
	o.OnSegment(context.Background(), agent.SegmentInfo{TaskID: "mine", SessionID: "s3"})
	o.release()
	if g.isUnattended("s3") {
		t.Fatal("release must clear marks left by a task that stopped mid-segment")
	}
}
