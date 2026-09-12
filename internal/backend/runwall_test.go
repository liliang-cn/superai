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

// Two tasks at once are two tasks on the wall, each with its own figures.
// The task id is the identity; nothing about one may leak into the other.
func TestRunWallTracksTwoTasksAtOnce(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	wall := NewRunWall(nil, nil)
	svc.Agent().RegisterObserver(wall)

	ids := []string{"fleet-a", "fleet-b"}
	var wg sync.WaitGroup
	for _, id := range ids {
		wall.Begin(id, "Task "+id, "m", 2)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			r, err := svc.StreamLong(context.Background(), "Task "+id,
				LongRunOptions{MaxSegments: 2, RoundsPerSegment: 1, TaskID: id}, nil)
			if err != nil {
				t.Errorf("%s: %v", id, err)
				return
			}
			wall.Finish(id, r.Done, r.Stop, r.Text, nil)
		}(id)
	}
	wg.Wait()

	list := wall.List()
	if len(list) != 2 {
		t.Fatalf("wall holds %d tasks, want 2: %+v", len(list), list)
	}
	for _, s := range list {
		if s.Rounds == 0 || s.Segments == 0 {
			t.Errorf("%s recorded nothing: %+v", s.TaskID, s)
		}
		if s.Running {
			t.Errorf("%s still running after Finish", s.TaskID)
		}
		if s.Goal != "Task "+s.TaskID {
			t.Errorf("%s carries another task's goal: %q", s.TaskID, s.Goal)
		}
	}
}
