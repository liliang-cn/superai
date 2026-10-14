package backend

import (
	"strings"
	"testing"
	"time"
)

func TestUIRulesRoundTrip(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())

	if got := LoadUIRules(); got != "" {
		t.Fatalf("fresh install should have no rules, got %q", got)
	}
	// The backend's own blocks are named even before any frontend announced
	// its rules: the iOS app draws them and never announces anything.
	fresh := uiRulesSection()
	for _, name := range []string{"card", "timeline", "stat"} {
		if !strings.Contains(fresh, name) {
			t.Errorf("fresh persona section must name %s:\n%s", name, fresh)
		}
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

	// The prompt names what can be drawn and where the format is; the rules
	// themselves stay out of every turn and come from ui_block_rules.
	persona := buildPersona(time.Now(), true, false) + uiRulesSection()
	if !strings.Contains(persona, "chart") || !strings.Contains(persona, "sources") || !strings.Contains(persona, "ui_block_rules") {
		t.Errorf("persona must name the blocks and the tool that describes them:\n%s", persona)
	}
	for _, name := range []string{"card", "timeline", "stat"} {
		if !strings.Contains(persona, name) {
			t.Errorf("persona must still name the backend's %s block", name)
		}
	}
	if all := AllUIRules(); !strings.Contains(all, rules) || !strings.Contains(all, "```card") || !strings.Contains(all, "```stat") {
		t.Errorf("ui_block_rules must hand over the frontend's rules and the backend's blocks:\n%s", all)
	}
	if strings.Contains(persona, "```chart") {
		t.Error("the full rendering rules went into the prompt")
	}
	if !strings.Contains(persona, "You are SuperAI") {
		t.Error("persona must keep its original instructions")
	}
}

// The persona and the schema have to agree. Telling the model to call
// search_mcp_servers when no such tool is registered is an invitation to spend
// a round finding that out.
func TestPersonaOmitsSelfInstallWhenThoseToolsAreWithheld(t *testing.T) {
	with := buildPersona(time.Now(), true, false)
	without := buildPersona(time.Now(), false, false)

	for _, tool := range []string{"search_mcp_servers", "add_mcp_server", "search_skills", "install_skill"} {
		if !strings.Contains(with, tool) {
			t.Errorf("expected %s in the self-install persona", tool)
		}
		if strings.Contains(without, tool) {
			t.Errorf("persona still names %s after the tools were withheld", tool)
		}
	}
	// The rest of the persona survives.
	if !strings.Contains(without, "resolve_datetime") || !strings.Contains(without, "add_schedule") {
		t.Error("withholding the install tools stripped unrelated instructions")
	}
}
