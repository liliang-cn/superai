package brand

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateDirsMovesAndLeavesALink(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".superai", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".superai", "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	MigrateDirs(home)

	if _, err := os.Stat(filepath.Join(home, ".superleo", "settings.json")); err != nil {
		t.Fatalf("data not at the new path: %v", err)
	}
	link, err := os.Readlink(filepath.Join(home, ".superai"))
	if err != nil || link != ".superleo" {
		t.Fatalf("old path is not a link to the new one: %q %v", link, err)
	}
	// Through the old path, the same file.
	if _, err := os.Stat(filepath.Join(home, ".superai", "settings.json")); err != nil {
		t.Fatalf("old path does not reach the data: %v", err)
	}
	// A second start changes nothing.
	MigrateDirs(home)
	if fi, err := os.Lstat(filepath.Join(home, ".superleo")); err != nil || !fi.IsDir() {
		t.Fatalf("second run disturbed the new dir: %v", err)
	}
	// Never created: nothing appears.
	if _, err := os.Lstat(filepath.Join(home, ".superleo-node")); err == nil {
		t.Fatal("a directory that never existed was created")
	}
}

func TestLegacyEnvFillsOnlyUnsetNames(t *testing.T) {
	got := map[string]string{"SUPERLEO_TOKEN": "new"}
	lookup := func(k string) (string, bool) { v, ok := got[k]; return v, ok }
	set := func(k, v string) error { got[k] = v; return nil }
	legacyEnv([]string{"SUPERAI_HOME=/data", "SUPERAI_TOKEN=old", "PATH=/bin"}, lookup, set)
	if got["SUPERLEO_HOME"] != "/data" {
		t.Fatalf("SUPERLEO_HOME = %q", got["SUPERLEO_HOME"])
	}
	if got["SUPERLEO_TOKEN"] != "new" {
		t.Fatalf("an explicitly set new name was overwritten: %q", got["SUPERLEO_TOKEN"])
	}
}
