package backend

import (
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// -1 is a choice and 0 is an unset field, so backfill has to tell them apart.
// Getting this backwards is silent: an unlimited setting would be quietly
// rewritten to the default and nothing would say so.

func TestUnlimitedRoundsSurvivesBackfill(t *testing.T) {
	s := &Settings{MaxRounds: agent.UnlimitedRounds}
	s.backfill(defaults())
	if s.MaxRounds != agent.UnlimitedRounds {
		t.Fatalf("an unlimited round budget was rewritten to %d", s.MaxRounds)
	}
}

func TestAnUnsetRoundBudgetStillGetsTheDefault(t *testing.T) {
	s := &Settings{}
	s.backfill(defaults())
	if s.MaxRounds != defaultMaxRds {
		t.Fatalf("an untouched max_rounds became %d, want the default %d", s.MaxRounds, defaultMaxRds)
	}
}

func TestANegativeBelowTheSentinelIsTreatedAsUnset(t *testing.T) {
	s := &Settings{MaxRounds: -42}
	s.backfill(defaults())
	if s.MaxRounds != defaultMaxRds {
		t.Fatalf("max_rounds=-42 became %d, want the default %d", s.MaxRounds, defaultMaxRds)
	}
}

func TestAChosenRoundBudgetIsLeftAlone(t *testing.T) {
	s := &Settings{MaxRounds: 500}
	s.backfill(defaults())
	if s.MaxRounds != 500 {
		t.Fatalf("max_rounds=500 became %d", s.MaxRounds)
	}
}

func TestTheAgentCannotGiveItselfAnUnlimitedBudget(t *testing.T) {
	// The person may set it; the model may not. See the comment on
	// selfMaxRoundsCeiling.
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	svc := &Service{}

	if _, err := svc.applySetting("max_rounds", agent.UnlimitedRounds); err == nil {
		t.Fatal("the agent removed its own round budget")
	}
	if _, err := svc.applySetting("max_rounds", selfMaxRoundsCeiling+1); err == nil {
		t.Fatalf("the agent set its round budget above the %d ceiling", selfMaxRoundsCeiling)
	}
	if _, err := svc.applySetting("max_rounds", 500); err != nil {
		t.Fatalf("the agent could not raise its budget to a value it is allowed: %v", err)
	}
	after, err := LoadSettings()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.MaxRounds != 500 {
		t.Fatalf("the allowed change did not land: %d", after.MaxRounds)
	}
}
