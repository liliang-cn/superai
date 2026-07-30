package backend

import (
	"strings"
	"testing"
	"time"
)

func TestUIRulesRoundTrip(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	if got := LoadUIRules(); got != "" {
		t.Fatalf("fresh install should have no rules, got %q", got)
	}
	if uiRulesSection() != "" {
		t.Error("persona must not gain a section when no rules are stored")
	}

	rules := "Charts (fenced): ```chart <ECharts option JSON>```."
	changed, err := SaveUIRules(rules)
	if err != nil {
		t.Fatalf("SaveUIRules: %v", err)
	}
	if !changed {
		t.Error("first save must report a change")
	}
	if got := LoadUIRules(); got != rules {
		t.Errorf("LoadUIRules = %q, want %q", got, rules)
	}

	// Re-announcing the same rules must not trigger an agent rebuild.
	changed, err = SaveUIRules(rules + "\n  ")
	if err != nil {
		t.Fatalf("SaveUIRules again: %v", err)
	}
	if changed {
		t.Error("identical rules (modulo surrounding space) must report no change")
	}

	changed, _ = SaveUIRules(rules + "\nSources (fenced JSON): ```sources```.")
	if !changed {
		t.Error("different rules must report a change")
	}

	persona := buildPersona(time.Now()) + uiRulesSection()
	if !strings.Contains(persona, "```chart") {
		t.Error("persona must carry the rendering rules")
	}
	if !strings.Contains(persona, "你是 SuperAI") {
		t.Error("persona must keep its original instructions")
	}
}
