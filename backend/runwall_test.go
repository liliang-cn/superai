package backend

import (
	"context"
	"sync"
	"testing"
)

// The wall must fill from a real segmented run through the observer — not
// from calls the page makes up. If the observer is not registered, or a
// callback is not wired, this is the test that goes red.
func TestRunWallNarratesASegmentedRun(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	var mu sync.Mutex
	ticks := map[string]int{}
	wall := NewRunWall(
		func(ctx context.Context, taskID string) []agentPlanItem { return nil },
		func(taskID, kind string) { mu.Lock(); ticks[kind]++; mu.Unlock() },
	)
	svc.Agent().RegisterObserver(wall)

	const id = "wall-test"
	wall.Begin(id, "Say hello.", "test-model", 2)
	report, err := svc.StreamLong(context.Background(), "Say hello.",
		LongRunOptions{MaxSegments: 2, RoundsPerSegment: 1, TaskID: id}, nil)
	if err != nil {
		t.Fatalf("StreamLong: %v", err)
	}
	wall.Finish(id, report.Done, report.Stop, report.Text, nil)

	s := wall.Snapshot(context.Background(), id)
	if s == nil {
		t.Fatal("wall has no state for the task it just watched")
	}
	if len(s.Segments) == 0 {
		t.Error("no segments recorded; OnSegment did not reach the wall")
	}
	if len(s.Rounds) == 0 {
		t.Error("no rounds recorded; OnModelEnd did not reach the wall")
	}
	if len(s.Log) == 0 {
		t.Error("no narration; nothing wrote to the log")
	}
	if s.Running {
		t.Error("finished task still marked running")
	}
	if s.Goal != "Say hello." || s.Model != "test-model" {
		t.Errorf("Begin did not land: goal=%q model=%q", s.Goal, s.Model)
	}
	mu.Lock()
	defer mu.Unlock()
	if ticks["round"] == 0 || ticks["segment"] == 0 || ticks["finish"] == 0 {
		t.Errorf("ticks missing: %v", ticks)
	}

	list := wall.List()
	if len(list) != 1 || list[0].TaskID != id {
		t.Errorf("list = %+v, want the one task", list)
	}
}

// A run the wall was not told about — every chat turn, for instance — must
// leave nothing on it and push nothing at the page.
func TestRunWallIgnoresRunsItWasNotToldAbout(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	var mu sync.Mutex
	ticks := 0
	wall := NewRunWall(nil, func(string, string) { mu.Lock(); ticks++; mu.Unlock() })
	svc.Agent().RegisterObserver(wall)

	// No Begin. This is a chat turn as far as the wall is concerned.
	if _, err := svc.StreamLong(context.Background(), "Say hello.",
		LongRunOptions{MaxSegments: 1, RoundsPerSegment: 1, TaskID: "not-mine"}, nil); err != nil {
		t.Fatalf("StreamLong: %v", err)
	}
	if got := wall.List(); len(got) != 0 {
		t.Fatalf("wall picked up a task it was never told about: %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if ticks != 0 {
		t.Fatalf("wall pushed %d ticks for a run it should have ignored", ticks)
	}
}
