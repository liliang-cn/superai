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

// The new name already in use by another program's data — the SuperLeo
// project before this one — is set aside, not run on.
func TestMigrateDirsSetsAForeignDirectoryAside(t *testing.T) {
	home := t.TempDir()
	mk := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(home, ".superai", "settings.json"), "{}")
	mk(filepath.Join(home, ".superleo", "recall.db"), "old project")
	MigrateDirs(home)

	if _, err := os.Stat(filepath.Join(home, ".superleo", "settings.json")); err != nil {
		t.Fatalf("this program's data is not at the new path: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".superleo-legacy", "recall.db")); err != nil || string(b) != "old project" {
		t.Fatalf("the other program's data was not set aside: %q %v", b, err)
	}
	if link, _ := os.Readlink(filepath.Join(home, ".superai")); link != ".superleo" {
		t.Fatalf("old path is not a link: %q", link)
	}
}
