package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/extensiontest"
	"github.com/liliang-cn/superleo/internal/backend"
)

// beeApp is an app with the standing engine running and one bee on watch.
func beeApp(t *testing.T) (*App, AgentSpec) {
	t.Helper()
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	app := NewApp()
	// The gate is off: a bee's goal still waits for the person.
	app.settings = &backend.Settings{MaxRounds: 30, DisableToolApproval: true}
	svc := extensiontest.NewService(t, extensiontest.Script(extensiontest.Answer("ok")))
	st, err := agent.NewStanding(svc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	h := app.standing()
	h.mu.Lock()
	h.st = st
	h.mu.Unlock()
	v, err := app.SaveStandingAgent(AgentSpec{Name: "Disk keeper", Goal: "Every disk stays below 80%", Watch: []string{"all hosts"},
		Auto: []string{"bash"}, Cron: "0 * * * *"})
	if err != nil {
		t.Fatal(err)
	}
	return app, beeNow(app, v.ID)
}

func beeNow(app *App, id string) AgentSpec {
	h := app.standing()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.specs[id]
}

// The queen changes a bee's goal and scope through an approval card, as a
// boundary setting is changed: nothing moves until the person says yes, a no
// leaves it as it was, and a yes changes those two and nothing else.
func TestABeesGoalAndScopeChangeOnlyWithApproval(t *testing.T) {
	app, bee := beeApp(t)
	ss := selfSession(t, app)
	change := map[string]any{"id": bee.ID, "goal": "Every pod in namespace superleo is Running", "watch": []any{"pods in namespace superleo"}}

	out, isErr := callSelf(t, ss, "superleo_standing_agent_update", change)
	if isErr || !strings.Contains(out, "needs_approval") {
		t.Fatalf("not put to the person: %v %s", isErr, out)
	}
	id := requestID(t, out)
	waitFor(t, "the card", func() bool { return cardFor(app, id) != nil })
	if got := beeNow(app, bee.ID); got.Goal != bee.Goal || !slices.Equal(got.Watch, bee.Watch) {
		t.Fatalf("changed before approval: %+v", got)
	}
	app.ResolveToolApproval(id, false)
	waitFor(t, "denied", func() bool { r, _ := app.selfRequest(id); return r.Status == reqDenied })
	if got := beeNow(app, bee.ID); got.Goal != bee.Goal || !slices.Equal(got.Watch, bee.Watch) {
		t.Fatalf("changed though denied: %+v", got)
	}

	out, _ = callSelf(t, ss, "superleo_standing_agent_update", change)
	id = requestID(t, out)
	waitFor(t, "the card", func() bool { return cardFor(app, id) != nil })
	if r := app.ResolveToolApproval(id, true); r != "ok" {
		t.Fatal(r)
	}
	waitFor(t, "applied", func() bool { r, _ := app.selfRequest(id); return r.Status == reqApplied })
	got := beeNow(app, bee.ID)
	if got.Goal != "Every pod in namespace superleo is Running" || !slices.Equal(got.Watch, []string{"pods in namespace superleo"}) {
		t.Fatalf("after approval: %+v", got)
	}
	if got.Name != bee.Name || got.Cron != bee.Cron || !slices.Equal(got.Auto, bee.Auto) || !got.CreatedAt.Equal(bee.CreatedAt) || got.HookSecret != bee.HookSecret {
		t.Fatalf("more than goal and scope changed:\nbefore %+v\nafter  %+v", bee, got)
	}
	// The engine runs the bee by the new brief too.
	st, _ := app.standing().engine()
	if s, ok := st.Get(bee.ID); !ok || s.Responsibility.Goal != got.Goal || !slices.Equal(s.Responsibility.Watch, got.Watch) {
		t.Fatalf("engine: %+v", s.Responsibility)
	}
}

// A change that cannot apply is this call's answer, not a card.
func TestABadBeeChangeIsRefusedAtOnce(t *testing.T) {
	app, bee := beeApp(t)
	ss := selfSession(t, app)
	for _, bad := range []map[string]any{
		{"id": bee.ID, "goal": "  "},
		{"id": "no-such-bee", "goal": "x"},
		{"id": bee.ID},
		{"id": bee.ID, "watch": []any{1}},
	} {
		if out, isErr := callSelf(t, ss, "superleo_standing_agent_update", bad); !isErr {
			t.Errorf("accepted %v: %s", bad, out)
		}
	}
	if len(app.PendingToolApprovals()) != 0 {
		t.Fatalf("a refused change raised a card: %v", app.PendingToolApprovals())
	}
	// settings_set reaches the same keys, by the same rules.
	out, isErr := callSelf(t, ss, "superleo_settings_set", map[string]any{"key": "bees." + bee.ID + ".watch", "value": nil})
	if isErr || !strings.Contains(out, "needs_approval") {
		t.Fatalf("settings_set: %v %s", isErr, out)
	}
	app.ResolveToolApproval(requestID(t, out), false)
}

func TestBeeKeysSplit(t *testing.T) {
	for key, want := range map[string][2]string{
		"bees.6f1c-uuid.goal":  {"6f1c-uuid", "goal"},
		"bees.6f1c-uuid.watch": {"6f1c-uuid", "watch"},
	} {
		id, field, ok := beeKey(key)
		if !ok || id != want[0] || field != want[1] {
			t.Errorf("%s: %q %q %v", key, id, field, ok)
		}
	}
	for _, key := range []string{"bees.goal", "local_hive", "bees."} {
		if _, _, ok := beeKey(key); ok {
			t.Errorf("%s read as a bee key", key)
		}
	}
	if b := backend.SelfKeyTier("bees.x.goal", "y"); b != backend.TierNeedsApproval {
		t.Fatalf("bees.x.goal is %s", b)
	}
}
