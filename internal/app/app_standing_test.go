package app

import (
	"testing"

	"github.com/liliang-cn/superai/internal/backend"
)

func TestAStandingAgentsPermissionsAreItsOwn(t *testing.T) {
	servers := []string{"github", "home_assistant"}
	sp := AgentSpec{
		Name:       "Ops",
		Auto:       []string{"fs_read*", "mcp:github"},
		Ask:        []string{"bash", "mcp_github_create_*"},
		Forbid:     []string{"*delete*"},
		Connectors: []string{"github"},
	}
	for tool, want := range map[string]backend.Verdict{
		"fs_read_file":               backend.VerdictAllow, // auto
		"mcp_github_list_issues":     backend.VerdictAllow, // auto, whole server
		"mcp_github_create_issue":    backend.VerdictAsk,   // ask outranks auto
		"bash":                       backend.VerdictAsk,
		"mcp_github_delete_repo":     backend.VerdictDeny, // forbid outranks everything
		"mcp_home_assistant_turn_on": backend.VerdictDeny, // not a connector it has
		"web_search":                 backend.VerdictNone, // the gate's own rules
		"standing_notify":            backend.VerdictAllow,
	} {
		if got, _ := judge(sp, tool, servers); got != want {
			t.Errorf("%s: got %v want %v", tool, got, want)
		}
	}
	sp.AllConnectors = true
	if got, _ := judge(sp, "mcp_home_assistant_turn_on", servers); got != backend.VerdictNone {
		t.Fatalf("all connectors still refused: %v", got)
	}
}

func TestAnMCPToolIsMatchedToTheLongestServerName(t *testing.T) {
	servers := []string{"home", "home_assistant"}
	if got := mcpServerOf("mcp_home_assistant_turn_on", servers); got != "home_assistant" {
		t.Fatal(got)
	}
	if got := mcpServerOf("mcp_home_lights", servers); got != "home" {
		t.Fatal(got)
	}
	if got := mcpServerOf("bash", servers); got != "" {
		t.Fatal(got)
	}
}

func TestOnlyExactForbiddenNamesAreWithheldFromTheModel(t *testing.T) {
	got := exactNames([]string{"bash", "fs_*", "mcp:github", " web_fetch "})
	if len(got) != 2 || got[0] != "bash" || got[1] != "web_fetch" {
		t.Fatalf("%v", got)
	}
}

func TestAStandingAgentNeedsANameAGoalAndAValidSchedule(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := &App{}
	for _, sp := range []AgentSpec{
		{Goal: "keep CI green"},
		{Name: "CI"},
		{Name: "CI", Goal: "keep CI green", Cron: "every morning"},
		{Name: "CI", Goal: "keep CI green", EveryMinutes: -5},
	} {
		if _, err := a.SaveStandingAgent(sp); err == nil {
			t.Fatalf("accepted %+v", sp)
		}
	}
}

func TestOneRuleCannotSitInTwoTiers(t *testing.T) {
	if err := tiersConflict(AgentSpec{Auto: []string{"*bash*"}, Ask: []string{"*BASH*"}}); err == nil {
		t.Fatal("accepted *bash* in two tiers")
	}
	if err := tiersConflict(AgentSpec{Auto: []string{"fs_*"}, Ask: []string{"*bash*"}, Forbid: []string{"*delete*"}}); err != nil {
		t.Fatal(err)
	}
}
