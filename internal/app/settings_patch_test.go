package app

import (
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// A patch changes what it names and nothing else: the field another card
// saved a moment before is still there afterwards.
func TestAPatchChangesOnlyWhatItNames(t *testing.T) {
	cur := backend.Settings{
		LLMModel:           "claude-opus-5-5",
		DesktopControl:     true,
		DesktopBlockedApps: []string{"Mail"},
	}
	cur.Hive.QueenEngine = "claude"
	cur.Hive.Role = "queen"
	next, err := mergeSettingsPatch(cur, map[string]any{
		"llm_model": "claude-sonnet-5-5",
		"hive":      map[string]any{"queen_engine": ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.LLMModel != "claude-sonnet-5-5" || next.Hive.QueenEngine != "" {
		t.Fatalf("the patch did not land: %q %q", next.LLMModel, next.Hive.QueenEngine)
	}
	if next.Hive.Role != "queen" {
		t.Fatalf("a nested field the patch did not name changed: %q", next.Hive.Role)
	}
	if !next.DesktopControl || len(next.DesktopBlockedApps) != 1 {
		t.Fatalf("fields the patch did not name changed: %v %v", next.DesktopControl, next.DesktopBlockedApps)
	}
}

func TestAPatchNullRemovesAndEmptyKeepsEmpty(t *testing.T) {
	cur := backend.Settings{DesktopBlockedApps: []string{"Mail"}}
	next, err := mergeSettingsPatch(cur, map[string]any{"desktop_blocked_apps": nil})
	if err != nil {
		t.Fatal(err)
	}
	if next.DesktopBlockedApps != nil {
		t.Fatalf("null did not remove: %v", next.DesktopBlockedApps)
	}
	next, err = mergeSettingsPatch(cur, map[string]any{"desktop_blocked_apps": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if next.DesktopBlockedApps == nil || len(next.DesktopBlockedApps) != 0 {
		t.Fatalf("an emptied list came back as %v", next.DesktopBlockedApps)
	}
}
