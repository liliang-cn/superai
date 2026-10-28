package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveMCPServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	path := mcpConfigPath()

	// Removing from a config that was never written is not an error: the caller
	// asked for a state that already holds.
	if err := RemoveMCPServer("anything"); err != nil {
		t.Errorf("missing config should be a no-op, got %v", err)
	}

	if err := upsertMCPServer(path, "keep", "npx", []string{"-y", "keep"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := upsertMCPServer(path, "drop", "npx", []string{"-y", "drop"}, nil); err != nil {
		t.Fatal(err)
	}

	if err := RemoveMCPServer("drop"); err != nil {
		t.Fatalf("RemoveMCPServer: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("config should stay valid JSON: %v", err)
	}
	if _, gone := cfg.Servers["drop"]; gone {
		t.Error("the removed server is still in the config")
	}
	if _, ok := cfg.Servers["keep"]; !ok {
		t.Error("the other server must survive")
	}

	// Removing what is not there leaves the rest alone.
	if err := RemoveMCPServer("drop"); err != nil {
		t.Errorf("second removal should be a no-op, got %v", err)
	}
	if err := RemoveMCPServer(""); err == nil {
		t.Error("an empty name is not a server")
	}
}

func TestRemoveSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)

	dir := filepath.Join(home, "skills", "doomed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: doomed\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveSkill("doomed"); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the skill directory should be gone")
	}

	if err := RemoveSkill("doomed"); err != nil {
		t.Errorf("removing an absent skill should be a no-op, got %v", err)
	}
	if err := RemoveSkill(""); err == nil {
		t.Error("an empty name is not a skill")
	}

	// A name must not be able to walk out of the skills folder.
	outside := filepath.Join(home, "settings.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = RemoveSkill("../settings.json")
	if _, err := os.Stat(outside); err != nil {
		t.Error("a traversing name must not delete anything outside the skills folder")
	}
}

func TestInstalledSkillNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)

	if names := InstalledSkillNames(); len(names) != 0 {
		t.Errorf("a fresh install has no skills, got %v", names)
	}

	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(home, "skills", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "skills", "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	names := InstalledSkillNames()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("only directories are skills, got %v", names)
	}
}

func TestInstallMCPServerValidates(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	var svc *Service

	if err := svc.InstallMCPServer(t.Context(), "", "npx", nil, nil); err == nil {
		t.Error("a server needs a name")
	}
	if err := svc.InstallMCPServer(t.Context(), "x", "", nil, nil); err == nil {
		t.Error("a server needs a command")
	}
	// With no running agent the config is still written, so it comes up next launch.
	if err := svc.InstallMCPServer(t.Context(), "later", "npx", []string{"-y", "pkg"}, nil); err != nil {
		t.Errorf("writing the config should succeed without a running agent, got %v", err)
	}
	if _, err := os.Stat(mcpConfigPath()); err != nil {
		t.Error("the config should have been written")
	}
}

// A server can be in either file this SuperLeo reads; it goes from both, and
// the rest of each file stays as it was.
func TestRemoveMCPServerFromEveryConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	data := filepath.Join(home, "data", "mcpServers.json")
	if err := os.MkdirAll(filepath.Dir(data), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte(`{"mcpServers":{"cortexdb":{"command":"x"}},"note":"kept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPServer("cortexdb"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(data)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	if string(top["note"]) != `"kept"` || string(top["mcpServers"]) != "{}" {
		t.Fatalf("file now %s", raw)
	}
	if fi, _ := os.Stat(data); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v", fi.Mode().Perm())
	}
}

// One the queen hands out is not this SuperLeo's to remove, and it says so.
func TestAServerFromTheQueenIsNotRemovedHere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	if err := os.WriteFile(HiveMCPFile(), []byte(`{"mcpServers":{"noted":{"type":"http","url":"http://x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPServer("noted"); err == nil {
		t.Fatal("a queen's server was reported removed")
	}
}
