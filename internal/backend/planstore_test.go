package backend

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

func newTestPlanStore(t *testing.T) *GraphPlanStore {
	t.Helper()
	ps := NewGraphPlanStore(filepath.Join(t.TempDir(), "plans"))
	t.Cleanup(func() { _ = ps.Close() })
	return ps
}

// The point of the whole thing: a plan written now is there after the process
// that wrote it is gone.
func TestPlanRoundTripsThroughTheGraph(t *testing.T) {
	ps := newTestPlanStore(t)
	ctx := context.Background()

	want := []agent.PlanItem{
		{Text: "find the gateway port", Done: true, Note: "47821, from settings.json"},
		{Text: "write the client", Done: true, Note: "client.go, uses grpc.NewClient"},
		{Text: "run the tests", Done: false},
	}
	if err := ps.SavePlan(ctx, "build-the-thing", want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := ps.LoadPlan(ctx, "build-the-thing")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d items, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Order is the plan. A store that returns the steps shuffled has lost it.
func TestPlanKeepsItsOrder(t *testing.T) {
	ps := newTestPlanStore(t)
	ctx := context.Background()
	items := make([]agent.PlanItem, 12)
	for i := range items {
		items[i] = agent.PlanItem{Text: string(rune('a' + i))}
	}
	if err := ps.SavePlan(ctx, "k", items); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ps.LoadPlan(ctx, "k")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range items {
		if got[i].Text != items[i].Text {
			t.Fatalf("position %d holds %q, want %q — the order was lost", i, got[i].Text, items[i].Text)
		}
	}
}

// Two steps can legitimately read the same. Keying nodes on their text would
// silently merge them and lose a step.
func TestPlanKeepsDuplicateSteps(t *testing.T) {
	ps := newTestPlanStore(t)
	ctx := context.Background()
	items := []agent.PlanItem{
		{Text: "run the tests", Done: true, Note: "3 failures"},
		{Text: "fix the failures"},
		{Text: "run the tests"},
	}
	if err := ps.SavePlan(ctx, "k", items); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ps.LoadPlan(ctx, "k")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d items, want 3 — a duplicate step was merged away: %+v", len(got), got)
	}
	if !got[0].Done || got[2].Done {
		t.Errorf("the two identical steps share a done flag: %+v", got)
	}
}

// A save replaces the plan. A shorter one must not leave the old tail behind.
func TestAShorterPlanDropsTheOldTail(t *testing.T) {
	ps := newTestPlanStore(t)
	ctx := context.Background()
	if err := ps.SavePlan(ctx, "k", []agent.PlanItem{{Text: "one"}, {Text: "two"}, {Text: "three"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ps.SavePlan(ctx, "k", []agent.PlanItem{{Text: "one"}}); err != nil {
		t.Fatalf("resave: %v", err)
	}
	got, err := ps.LoadPlan(ctx, "k")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("loaded %d items after shrinking to 1: %+v", len(got), got)
	}
}

// A task nobody has planned is not an error; it is an empty plan.
func TestAnUnknownKeyIsAnEmptyPlan(t *testing.T) {
	ps := newTestPlanStore(t)
	got, err := ps.LoadPlan(context.Background(), "never-seen")
	if err != nil {
		t.Fatalf("load of an unknown key errored: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want empty", got)
	}
}

// Two tasks must not share a plan — that is the bug the per-service scratchpad
// fixed in memory, and it must not come back through the store.
func TestTwoKeysAreTwoPlans(t *testing.T) {
	ps := newTestPlanStore(t)
	ctx := context.Background()
	if err := ps.SavePlan(ctx, "task-a", []agent.PlanItem{{Text: "a only"}}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := ps.SavePlan(ctx, "task-b", []agent.PlanItem{{Text: "b only"}, {Text: "and more"}}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	a, err := ps.LoadPlan(ctx, "task-a")
	if err != nil {
		t.Fatalf("load a: %v", err)
	}
	if len(a) != 1 || a[0].Text != "a only" {
		t.Errorf("task-a = %+v — the two tasks share a plan", a)
	}
}

// The key comes from a session or task id, which is not constrained to be a
// safe filename. It becomes one here.
func TestAwkwardKeysAreStillUsable(t *testing.T) {
	ps := newTestPlanStore(t)
	ctx := context.Background()
	for _, key := range []string{"../escape", "Session ID/42", "", "日程", "UPPER-Case"} {
		if err := ps.SavePlan(ctx, key, []agent.PlanItem{{Text: "x"}}); err != nil {
			t.Errorf("save with key %q: %v", key, err)
			continue
		}
		got, err := ps.LoadPlan(ctx, key)
		if err != nil {
			t.Errorf("load with key %q: %v", key, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("key %q round-tripped %d items, want 1", key, len(got))
		}
	}
}

// Distinct keys must not collide after sanitising, or two tasks silently merge.
func TestKeysThatSanitiseAlikeStayApart(t *testing.T) {
	ps := newTestPlanStore(t)
	ctx := context.Background()
	if err := ps.SavePlan(ctx, "a/b", []agent.PlanItem{{Text: "first"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := ps.SavePlan(ctx, "a_b", []agent.PlanItem{{Text: "second"}, {Text: "extra"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ps.LoadPlan(ctx, "a/b")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Text != "first" {
		t.Errorf("a/b came back as %+v — it collided with a_b", got)
	}
}
