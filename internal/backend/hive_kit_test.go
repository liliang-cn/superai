package backend

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A skill given to a worker as its own is not marked as the queen's, so her
// syncs leave it; the worker's kit tells its own from hers, and its hello
// carries the skills to the queen's roster.
func TestAWorkersOwnSkillIsToldFromTheQueens(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	md := base64.StdEncoding.EncodeToString([]byte("---\nname: inventory\ndescription: \"stock counts\"\n---\nbody\n"))
	if err := InstallSkillFiles("inventory", map[string]string{"SKILL.md": md}); err != nil {
		t.Fatal(err)
	}
	if err := InstallSkillFiles("bare", map[string]string{"notes.txt": md}); err == nil {
		t.Fatal("a skill without a SKILL.md was installed")
	}
	// One the queen gave, as her sync writes it.
	if _, err := ApplyAbilities(HiveAbilities{Skills: map[string]map[string]string{"report": {"SKILL.md": md}},
		MCP: map[string]any{}}, nil); err != nil {
		t.Fatal(err)
	}
	// Her next sync, without inventory, leaves the worker's own alone.
	if _, err := ApplyAbilities(HiveAbilities{Skills: map[string]map[string]string{"report": {"SKILL.md": md}},
		MCP: map[string]any{}}, nil); err != nil {
		t.Fatal(err)
	}
	got := SkillFolders()
	if len(got) != 2 || got[0].Name != "inventory" || !got[0].Own || got[0].About != "stock counts" || got[1].Name != "report" || got[1].Own {
		t.Fatalf("skills %+v", got)
	}
	if _, err := os.Stat(filepath.Join(DataDir(), "skills", "inventory", fromQueenMark)); err == nil {
		t.Fatal("the worker's own skill was marked as the queen's")
	}

	if err := upsertMCPServer(mcpConfigPath(), "sqlite", "/bin/mcp-sqlite-server", []string{"/d.db"}, nil); err != nil {
		t.Fatal(err)
	}
	if own := OwnMCPServers(); !own["sqlite"] || len(own) != 1 {
		t.Fatalf("own servers %v", own)
	}
	// A worker offers its own servers' tools up front; a queen does not.
	worker := &Settings{Hive: HiveSettings{Role: HiveRoleWorker}}
	if got := eagerTools(worker, false); !slices.Contains(got, "mcp_sqlite_*") || !slices.Contains(got, eagerNotedTools[0]) {
		t.Fatalf("worker eager tools %v", got)
	}
	if got := eagerTools(&Settings{Hive: HiveSettings{Role: HiveRoleQueen}}, true); slices.Contains(got, "mcp_sqlite_*") || !slices.Contains(got, browserEagerTools[0]) {
		t.Fatalf("queen eager tools %v", got)
	}

	h := NewHive("q", 10*time.Second)
	if _, err := h.Join(HiveHello{Protocol: HiveProtocol, Name: "w1", Role: HiveRoleWorker, URL: "http://w1",
		MCP: []string{"sqlite"}, Skills: []string{"inventory"}}); err != nil {
		t.Fatal(err)
	}
	if m := h.Members(); len(m) != 1 || len(m[0].Skills) != 1 || m[0].Skills[0] != "inventory" {
		t.Fatalf("the roster lost the skills: %+v", m)
	}
}
