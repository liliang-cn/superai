package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superleo/internal/backend"
)

// Codex may write every allowed root and the .git of the repository it works
// in — a worktree's own git dir and the main repository's too — so a run can
// commit where it changed the code.
func TestCodexMayWriteTheRootsAndTheRepositorysGit(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := mkRepo(t, filepath.Join(base, "superai"))
	sub := filepath.Join(repo, "internal", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "other")

	got := codexWritable(sub, []string{base, other})
	if want := []string{base, other, filepath.Join(repo, ".git")}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// A worktree: .git is a file naming .git/worktrees/wt in the main repo.
	wtGit := filepath.Join(repo, ".git", "worktrees", "wt")
	if err := os.MkdirAll(wtGit, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644)
	wt := filepath.Join(base, "wt")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644)
	got = codexWritable(wt, nil)
	if want := []string{wtGit, filepath.Join(repo, ".git")}; !slices.Equal(got, want) {
		t.Fatalf("worktree: got %v, want %v", got, want)
	}

	if got := codexWritable(filepath.Join(base, "plain"), []string{base}); !slices.Equal(got, []string{base}) {
		t.Fatalf("outside a repository: %v", got)
	}
}

// The list reaches codex as a TOML array it can read back, quotes and all.
func TestCodexWritableRootsAreValidTOML(t *testing.T) {
	paths := []string{`/Users/me/My "Repos"/a`, `C:\work\b`, "/tmp/ü"}
	var cfg struct {
		V []string `toml:"v"`
	}
	if _, err := toml.Decode("v = "+tomlStrings(paths), &cfg); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.V, paths) {
		t.Fatalf("read back %q", cfg.V)
	}
}

// A sandboxed codex run is started with the roots and the repository's .git
// writable.
func TestASandboxedCodexRunMayCommit(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	bin := fakeCLI(t, "codex", `for a in "$@"; do printf '%s\n' "$a"; done > `+argsFile)
	noDiscovery(t, agentexec.Installed{Name: "codex", Binary: bin})
	root, _ := filepath.EvalSymlinks(t.TempDir())
	a := codingApp(t, backend.ExternalAgents{Roots: []string{root}})
	repo := mkRepo(t, filepath.Join(root, "superai"))

	done := make(chan CLIRun, 1)
	if _, err := a.startCLIRun(cliStart{Agent: "codex", Prompt: "fix it", Cwd: repo, Ask: true, Done: func(r CLIRun) { done <- r }}); err != nil {
		t.Fatal(err)
	}
	<-done
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var roots string
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(l, "sandbox_workspace_write.writable_roots="); ok {
			roots = v
		}
	}
	var cfg struct {
		V []string `toml:"v"`
	}
	if _, err := toml.Decode("v = "+roots, &cfg); err != nil {
		t.Fatalf("%q: %v\nargs:\n%s", roots, err, raw)
	}
	ws, _ := filepath.EvalSymlinks(a.settings.WorkspaceDir)
	for _, want := range []string{ws, root, filepath.Join(repo, ".git")} {
		if !slices.Contains(cfg.V, want) {
			t.Errorf("%s is not writable: %v", want, cfg.V)
		}
	}
}
