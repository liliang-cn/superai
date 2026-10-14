package backend

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

// UI rendering rules.
//
// The chat transcript renders the assistant's answer through AIGUI, so blocks
// like ```chart, ```mermaid, ```table or a registered card become real UI
// instead of raw text. The model only emits them if it is told they exist —
// and the authoritative description of what exists lives in the frontend, next
// to the registry and plugin list that render them.
//
// So the frontend generates the rules (buildSystemPrompt over the same registry
// and plugins) and hands them here; NewService appends them to the persona.
// Keeping the file on disk means the agent is built with the right rules on the
// very first turn after a restart, before the UI has had a chance to call in.
//
// The card / timeline / stat blocks are the exception: every client renders
// them natively (the iOS app above all, which never announces rules of its
// own), so their spec ships with the backend (uirules_app.md) and is added to
// whatever the frontend announced. See AllUIRules.

//go:embed uirules_app.md
var appBlockRules string

func uiRulesPath() string { return filepath.Join(DataDir(), "ui-rules.md") }

// LoadUIRules returns the stored rendering rules, or "" when none are stored.
func LoadUIRules() string {
	raw, err := os.ReadFile(uiRulesPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// AppBlockRules is the spec of the blocks the backend itself defines: card,
// timeline and stat, with their actions.
func AppBlockRules() string { return strings.TrimSpace(appBlockRules) }

// AllUIRules is what the model is told can be drawn: the frontend's rules
// followed by the backend's own blocks.
func AllUIRules() string {
	front := LoadUIRules()
	if front == "" {
		return AppBlockRules()
	}
	return front + "\n\n" + AppBlockRules()
}

// SaveUIRules persists the rules and reports whether they actually changed, so
// callers can skip rebuilding the agent when the UI simply re-announced them.
func SaveUIRules(rules string) (changed bool, err error) {
	rules = strings.TrimSpace(rules)
	if rules == LoadUIRules() {
		return false, nil
	}
	if err = os.MkdirAll(DataDir(), 0o755); err != nil {
		return false, err
	}
	if err = os.WriteFile(uiRulesPath(), []byte(rules), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
