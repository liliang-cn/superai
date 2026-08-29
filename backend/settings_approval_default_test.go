package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// The gate defaults on, and the whole reason the field is phrased as a
// *disable* is so that it stays on through the cases nobody thinks about: a
// settings.json written before this existed, a hand-edited file missing the
// key, a struct literal someone reorders. A security control that defaults off
// is decoration, and this one is the difference between a prompt and a silent
// `sh -c`.
func TestToolApprovalDefaultsOnEvenForOlderSettingsFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	if defaults().DisableToolApproval {
		t.Error("the approval gate must default to on")
	}

	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The upgrade case: a settings file from before the gate existed.
	write(`{"llm_model": "gpt-5.5"}`)
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.DisableToolApproval {
		t.Fatal("an existing settings file with no approval key came up ungated")
	}
	if !NewToolGate(!s.DisableToolApproval, "").Enabled() {
		t.Error("the gate built from those settings is not asking anyone")
	}

	// Turning it off has to actually work, or the escape hatch is a lie and
	// people will reach for something worse.
	write(`{"llm_model": "gpt-5.5", "disable_tool_approval": true}`)
	s, err = LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !s.DisableToolApproval {
		t.Error("an explicit disable_tool_approval=true was ignored")
	}
}
