package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// PTC is ON by default since the final-reply fix (see the DisablePTC comment):
// it costs ~59% fewer prompt tokens. A default flips silently when someone
// reorders the struct literal, so pin it — and pin that an explicit choice
// still wins, because that setting was a no-op once already.
func TestPTCDefaultsOnButRemainsUserChoosable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	if defaults().DisablePTC {
		t.Error("PTC must default to on: it costs ~59% fewer prompt tokens and the reply is fixed")
	}

	// A fresh install gets the default.
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.DisablePTC {
		t.Error("a fresh install should have PTC on")
	}

	// Someone who turns it on keeps it on across a reload.
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"disable_ptc": true, "llm_model": "gpt-5.5"}`)
	s, err = LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !s.DisablePTC {
		t.Error("an explicit disable_ptc=true must be respected — otherwise the setting is a no-op again")
	}

	// A file written before this field existed falls back to the default.
	write(`{"llm_model": "gpt-5.5"}`)
	s, _ = LoadSettings()
	if s.DisablePTC {
		t.Error("a settings file with no disable_ptc key should take the default (on)")
	}

	// And the round trip keeps the choice.
	s.DisablePTC = false
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	var saved map[string]any
	_ = json.Unmarshal(raw, &saved)
	if saved["disable_ptc"] != false {
		t.Errorf("saved disable_ptc = %v, want false", saved["disable_ptc"])
	}
}
