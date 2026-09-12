package backend

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Delegation is off unless someone said otherwise, and "someone" never
// includes an upgrade. A settings.json written before external_agents existed
// unmarshals with the zero value, and the zero value has to be the off one:
// the feature spends another subscription's money and lets a second agent
// write files with its own approval prompt bypassed, so an app that quietly
// switched it on during an update would be doing all of that on its own
// initiative.
func TestExternalAgentsStayOffThroughAnUpgrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	if defaults().ExternalAgents.Enabled {
		t.Error("external agents must default to off")
	}

	// The upgrade case: a settings file from before the section existed.
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"llm_model":"gpt-5.5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.ExternalAgents.Enabled || s.ExternalAgents.Unattended {
		t.Fatalf("an older settings file came up delegating: %+v", s.ExternalAgents)
	}
}

// Everything the section holds has to survive a save and a load, because the
// roots are the only bound on where a delegated run may write — a root list
// that silently did not persist would widen that bound on the next restart.
func TestExternalAgentsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.ExternalAgents = ExternalAgents{
		Enabled:        true,
		Roots:          []string{"/srv/code", "  ", "/srv/docs"},
		Unattended:     true,
		Binaries:       map[string]string{"claude": "/opt/bin/claude", "codex": "   "},
		Models:         map[string]string{"claude": "claude-opus-5"},
		TimeoutSeconds: 900,
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	ea := got.ExternalAgents
	if !ea.Enabled || !ea.Unattended || ea.TimeoutSeconds != 900 {
		t.Fatalf("flags did not survive the round trip: %+v", ea)
	}
	// The blank row the UI's "add root" button leaves behind is not a root,
	// and a cleared override is not an override — either one persisted would
	// shadow something real.
	if len(ea.Roots) != 2 || ea.Roots[0] != "/srv/code" || ea.Roots[1] != "/srv/docs" {
		t.Fatalf("roots = %v, want the two non-blank ones", ea.Roots)
	}
	if _, ok := ea.Binaries["codex"]; ok {
		t.Fatalf("a blank binary override persisted: %v", ea.Binaries)
	}
	if ea.Binary("claude") != "/opt/bin/claude" {
		t.Fatalf("Binary(claude) = %q", ea.Binary("claude"))
	}
	if ea.Binary("gemini") != "gemini" {
		t.Fatalf("an agent with no override must fall back to a PATH lookup, got %q", ea.Binary("gemini"))
	}
	if ea.Model("claude") != "claude-opus-5" || ea.Model("codex") != "" {
		t.Fatalf("model overrides = %v", ea.Models)
	}
	if ea.Timeout() != 15*time.Minute {
		t.Fatalf("Timeout() = %v, want the configured 900s", ea.Timeout())
	}
	if (ExternalAgents{}).Timeout() != DefaultExternalAgentTimeout {
		t.Error("an unset timeout must take the default, not zero — zero means already expired")
	}
	if (ExternalAgents{TimeoutSeconds: -1}).Timeout() != DefaultExternalAgentTimeout {
		t.Error("a negative timeout must take the default too")
	}
}

// Empty roots mean the workspace, never the whole disk, and the workspace is
// in the list even when roots are named: a delegated run still reports back
// through the deliverable directory every other tool writes to.
func TestExternalAgentRootsAlwaysIncludeTheWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if roots := s.ExternalAgentRoots(); len(roots) != 1 || roots[0] != s.WorkspaceDir {
		t.Fatalf("unconfigured roots = %v, want only the workspace %q", roots, s.WorkspaceDir)
	}

	s.ExternalAgents.Roots = []string{"/srv/code", s.WorkspaceDir}
	roots := s.ExternalAgentRoots()
	if len(roots) != 2 || roots[0] != s.WorkspaceDir || roots[1] != "/srv/code" {
		t.Fatalf("roots = %v, want the workspace once plus /srv/code", roots)
	}
}

// The agent must not be able to grant itself delegation. settings_set is a
// whitelist for exactly this reason, and external_agents belongs on the other
// side of it with disable_tool_approval: switching it on hands another CLI the
// machine with its own approval prompt bypassed.
func TestExternalAgentsAreNotWritableByTheAgent(t *testing.T) {
	for _, key := range []string{"external_agents", "external_agents.enabled", "unattended", "roots"} {
		if _, ok := settingsWritable[key]; ok {
			t.Errorf("%q is writable from settings_set; a model that can turn on delegation has no approval gate", key)
		}
	}
	svc := &Service{settings: &Settings{ExternalAgents: ExternalAgents{Enabled: true}}}
	if _, err := svc.applySetting("external_agents", map[string]any{"enabled": true}); err == nil {
		t.Error("applySetting accepted external_agents")
	}
	// It may still read the flag — "can you hand this to Claude Code" is a
	// fair question — but only the flag.
	snap := svc.settingsSnapshot()
	if snap["external_agents_enabled"] != true {
		t.Errorf("the snapshot should report the flag, got %v", snap["external_agents_enabled"])
	}
	for _, leak := range []string{"external_agents", "external_agents_roots", "external_agents_binaries"} {
		if _, ok := snap[leak]; ok {
			t.Errorf("the snapshot exposes %q; the roots and binaries are machine policy, not conversation material", leak)
		}
	}
}
