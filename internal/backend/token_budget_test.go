package backend

import (
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

func runConfigOf(s *Service) *agent.RunConfig {
	cfg := agent.DefaultRunConfig()
	for _, o := range s.chatRunOptions("chat-1", nil) {
		o(cfg)
	}
	return cfg
}

// The token budget is optional: a turn carries one only when settings give
// one, and none otherwise.
func TestTurnTokenBudgetIsOptional(t *testing.T) {
	if got := runConfigOf(&Service{settings: &Settings{MaxRounds: 10}}).MaxBudgetTokens; got != 0 {
		t.Fatalf("no budget in settings, but the turn has %d", got)
	}
	if got := runConfigOf(&Service{settings: &Settings{MaxRounds: 10, TurnMaxTokens: 200000}}).MaxBudgetTokens; got != 200000 {
		t.Fatalf("turn budget = %d, want the settings' 200000", got)
	}
}
