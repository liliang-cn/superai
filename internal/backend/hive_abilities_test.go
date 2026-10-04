package backend

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWorkerTakesTheQueensAbilities(t *testing.T) {
	queen, worker := t.TempDir(), t.TempDir()

	// The queen: one skill with a script beside it, and two MCP servers.
	t.Setenv("SUPERAI_HOME", queen)
	sk := filepath.Join(queen, "skills", "glossary")
	if err := os.MkdirAll(filepath.Join(sk, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("---\nname: glossary\n---\nHoneycomb volume: three nodes."), 0o644)
	os.WriteFile(filepath.Join(sk, "bin", "look.sh"), []byte("echo hi"), 0o755)
	os.WriteFile(filepath.Join(queen, "mcpServers.json"), []byte(`{"mcpServers":{
		"remote":{"type":"http","url":"https://mcp.example/x"},
		"local":{"type":"stdio","command":"some-tool","args":["mcp"]}}}`), 0o600)
	ab := CollectAbilities()
	if len(ab.Skills["glossary"]) != 2 || len(ab.MCP) != 2 || ab.Hash == "" {
		t.Fatalf("collected %+v", ab)
	}

	// The worker has a skill of its own, and no some-tool.
	t.Setenv("SUPERAI_HOME", worker)
	mine := filepath.Join(worker, "skills", "mine")
	os.MkdirAll(mine, 0o755)
	os.WriteFile(filepath.Join(mine, "SKILL.md"), []byte("mine"), 0o644)
	missing := func(string) (string, error) { return "", errors.New("not found") }

	res, err := ApplyAbilities(ab, missing)
	if err != nil {
		t.Fatal(err)
	}
	if !res.SkillsChanged || !res.MCPChanged || len(res.MCP) != 1 || res.MCP[0] != "remote" || !strings.Contains(res.Skipped["local"], "some-tool") {
		t.Fatalf("applied %+v", res)
	}
	got, _ := os.ReadFile(filepath.Join(worker, "skills", "glossary", "bin", "look.sh"))
	if string(got) != "echo hi" {
		t.Fatalf("the skill's script did not come along: %q", got)
	}
	hive, _ := os.ReadFile(HiveMCPFile())
	if !strings.Contains(string(hive), "mcp.example") || strings.Contains(string(hive), "some-tool") {
		t.Fatalf("hive MCP file: %s", hive)
	}

	// The same again changes nothing.
	if res, _ := ApplyAbilities(ab, missing); res.SkillsChanged || res.MCPChanged {
		t.Fatalf("a repeat sync changed things: %+v", res)
	}

	// The queen drops her skill: it goes; the worker's own stays.
	ab.Skills = map[string]map[string]string{}
	if res, _ := ApplyAbilities(ab, missing); !res.SkillsChanged {
		t.Fatal("removing a skill changed nothing")
	}
	if _, err := os.Stat(filepath.Join(worker, "skills", "glossary")); !os.IsNotExist(err) {
		t.Fatal("the queen's removed skill is still on the worker")
	}
	if _, err := os.Stat(filepath.Join(mine, "SKILL.md")); err != nil {
		t.Fatal("the worker's own skill was removed")
	}
}
