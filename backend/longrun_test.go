package backend

import (
	"context"
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
