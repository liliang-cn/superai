package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchCommand(t *testing.T) {
	cases := []struct {
		name         string
		hint, regTyp string
		id, version  string
		runtimeArgs  []string
		pkgArgs      []string
		wantCommand  string
		wantArgs     []string
	}{
		{
			name: "npm package via npx", hint: "npx", regTyp: "npm",
			id: "server-filesystem", version: "1.2.3", runtimeArgs: []string{"-y"},
			wantCommand: "npx", wantArgs: []string{"-y", "server-filesystem@1.2.3"},
		},
		{
			name: "npx gets -y even when the registry omits it", hint: "npx", regTyp: "npm",
			id: "thing", version: "",
			wantCommand: "npx", wantArgs: []string{"-y", "thing"},
		},
		{
			name: "runtime hint missing, inferred from registry type", regTyp: "pypi",
			id: "mcp-server-git", version: "0.1.0",
			wantCommand: "uvx", wantArgs: []string{"mcp-server-git@0.1.0"},
		},
		{
			name: "package arguments follow the identifier", hint: "npx", regTyp: "npm",
			id: "fs", runtimeArgs: []string{"-y"}, pkgArgs: []string{"/tmp", "--readonly"},
			wantCommand: "npx", wantArgs: []string{"-y", "fs", "/tmp", "--readonly"},
		},
		{
			name: "docker images are not version-pinned with @", hint: "docker", regTyp: "oci",
			id: "ghcr.io/example/mcp:1.0", version: "1.0",
			wantCommand: "docker", wantArgs: []string{"ghcr.io/example/mcp:1.0"},
		},
		{
			name: "unknown registry type yields nothing runnable", regTyp: "cargo", id: "x",
			wantCommand: "", wantArgs: nil,
		},
		{
			name: "no identifier yields nothing runnable", hint: "npx", regTyp: "npm",
			wantCommand: "", wantArgs: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			command, args := launchCommand(tc.hint, tc.regTyp, tc.id, tc.version, tc.runtimeArgs, tc.pkgArgs)
			if command != tc.wantCommand {
				t.Errorf("command = %q, want %q", command, tc.wantCommand)
			}
			if strings.Join(args, " ") != strings.Join(tc.wantArgs, " ") {
				t.Errorf("args = %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

func TestArgValues(t *testing.T) {
	got := argValues([]registryArg{{Value: "-y"}, {Value: "  "}, {Name: "named-only"}, {Value: "pkg"}})
	if strings.Join(got, ",") != "-y,pkg" {
		t.Errorf("argValues = %v, want the non-empty values only", got)
	}
}

func TestReadSkillMeta(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	full := write("full.md", "---\nname: golang-pro\ndescription: Go patterns and gRPC.\nlicense: MIT\n---\n\n# Body\n")
	name, description := readSkillMeta(full)
	if name != "golang-pro" || description != "Go patterns and gRPC." {
		t.Errorf("got (%q, %q)", name, description)
	}

	quoted := write("quoted.md", "---\nname: \"quoted-name\"\ndescription: 'single'\n---\n")
	if n, d := readSkillMeta(quoted); n != "quoted-name" || d != "single" {
		t.Errorf("quotes should be stripped, got (%q, %q)", n, d)
	}

	none := write("none.md", "# Just a heading\n")
	if n, d := readSkillMeta(none); n != "" || d != "" {
		t.Errorf("a file without front matter has no metadata, got (%q, %q)", n, d)
	}

	if n, _ := readSkillMeta(filepath.Join(dir, "missing.md")); n != "" {
		t.Error("a missing file must not panic or invent a name")
	}
}

func TestMatchesAll(t *testing.T) {
	if !matchesAll(nil, "anything") {
		t.Error("an empty query matches everything")
	}
	if !matchesAll([]string{"go", "grpc"}, "golang-pro Go patterns and gRPC") {
		t.Error("all terms present should match, case-insensitively")
	}
	if matchesAll([]string{"go", "rust"}, "golang-pro Go patterns") {
		t.Error("a missing term must not match")
	}
}

func TestSearchSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	// Point HOME at a temp dir too: the search also looks under ~/.claude, and a
	// developer's real skills would otherwise decide what this test sees.
	t.Setenv("HOME", t.TempDir())

	// A directory laid out the way skills are found on a developer's machine.
	source := t.TempDir()
	mk := func(dir, name, description string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + description + "\n---\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(source, "golang-pro"), "golang-pro", "Concurrent Go, gRPC, benchmarks")
	mk(filepath.Join(source, "buffett"), "buffett", "Value investing analysis")
	// A plugin bundles its skills one level deeper.
	mk(filepath.Join(source, "someplugin", "skills", "nested"), "nested", "A bundled skill")
	// Not a skill: no SKILL.md.
	if err := os.MkdirAll(filepath.Join(source, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPERAI_SKILL_PATH", source)

	all := SearchSkills("", 0)
	names := make([]string, 0, len(all))
	for _, c := range all {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "buffett,golang-pro,nested" {
		t.Fatalf("empty query should list every skill, sorted; got %v", names)
	}

	hits := SearchSkills("go grpc", 0)
	if len(hits) != 1 || hits[0].Name != "golang-pro" {
		t.Errorf("query should match name and description; got %v", hits)
	}
	if hits[0].Installed {
		t.Error("nothing is installed yet")
	}
	if hits[0].Path == "" {
		t.Error("path is what install_skill needs")
	}

	// Once installed, the same search says so instead of offering it again.
	if err := os.MkdirAll(filepath.Join(home, "skills", "golang-pro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if again := SearchSkills("golang-pro", 0); len(again) != 1 || !again[0].Installed {
		t.Errorf("installed skill should be marked; got %v", again)
	}

	if limited := SearchSkills("", 2); len(limited) != 2 {
		t.Errorf("limit should cap results, got %d", len(limited))
	}
}

func TestCopySkillDir(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "references", "guide.md"), []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A checkout's .git must not be dragged along.
	if err := os.MkdirAll(filepath.Join(src, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "config"), []byte("[core]"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "installed")
	if err := copySkillDir(src, dst); err != nil {
		t.Fatalf("copySkillDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Error("SKILL.md should be copied")
	}
	if body, err := os.ReadFile(filepath.Join(dst, "references", "guide.md")); err != nil || string(body) != "deep" {
		t.Error("nested files should be copied")
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Error(".git should be skipped")
	}

	// Reinstalling replaces rather than merges.
	if err := os.WriteFile(filepath.Join(dst, "stale.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copySkillDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.md")); !os.IsNotExist(err) {
		t.Error("a reinstall should not leave files from the previous copy")
	}

	if err := copySkillDir(t.TempDir(), dst); err == nil {
		t.Error("a directory without SKILL.md is not a skill")
	}
	if err := copySkillDir(filepath.Join(src, "SKILL.md"), dst); err == nil {
		t.Error("a file is not a skill directory")
	}
}

// TestSearchMCPServersLive hits the real registry. Skipped without network:
// the point is to catch the registry changing shape under us, which a mocked
// response can never do.
func TestSearchMCPServersLive(t *testing.T) {
	if os.Getenv("SUPERAI_NET_TEST") != "1" {
		t.Skip("set SUPERAI_NET_TEST=1 to query the live MCP registry")
	}
	got, err := SearchMCPServers(context.Background(), "filesystem", 5)
	if err != nil {
		t.Fatalf("SearchMCPServers: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one filesystem server")
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.Name] {
			t.Errorf("duplicate server %q: versions should collapse to one row", c.Name)
		}
		seen[c.Name] = true
		if c.Command == "" && c.RemoteURL == "" {
			t.Errorf("%q is neither runnable nor remote", c.Name)
		}
		if c.Command == "npx" && len(c.Args) > 0 && c.Args[0] != "-y" {
			t.Errorf("%q: npx must not be able to prompt, got args %v", c.Name, c.Args)
		}
	}
	t.Logf("found %d servers, first: %+v", len(got), got[0])
}
