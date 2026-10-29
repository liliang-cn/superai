package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// mkRepo makes a git repository at dir (a .git folder is all inference looks for).
func mkRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

// An order that names a repository, by path or by name, is run there; one
// that names nothing, or something outside the roots, keeps the default.
func TestACodingOrderRunsInTheRepoItNames(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	superai := mkRepo(t, filepath.Join(root, "Things", "dev", "apps", "superai"))
	android := mkRepo(t, filepath.Join(root, "Things", "dev", "apps", "superai-android"))
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	mkRepo(t, filepath.Join(outside, "elsewhere"))
	if err := os.MkdirAll(filepath.Join(superai, "internal", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	allowed := []string{root}

	for prompt, want := range map[string]string{
		"在 " + superai + " 里修一下构建":                                       superai,
		"look at " + filepath.Join(superai, "internal", "app") + ".":     superai, // the repo, not a folder in it
		"write " + filepath.Join(superai, "notes", "new.md") + " please": superai, // a file still to be made
		"fix the gradle build in superai-android":                        android,
		"给 superai-android 加个按钮":                                         android,
		"superai: bump agent-go":                                         superai,
		"compare superai with superai-android":                           superai, // the one said first
		"写一首诗":                                                           "",
		"open " + filepath.Join(outside, "elsewhere") + " and fix it":    "",
		"the superaix build":                                             "",
	} {
		if got := inferCLICwd(prompt, allowed); got != want {
			t.Errorf("%q: got %q, want %q", prompt, got, want)
		}
	}
	if got := inferCLICwd("fix superai", nil); got != "" {
		t.Errorf("no roots, yet %q", got)
	}
}

// "~/…" in an order is the home folder of the machine the CLI runs on.
func TestACodingOrderMayNameAPathUnderHome(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	repo := mkRepo(t, filepath.Join(home, "Things", "dev", "apps", "superai"))
	if got := inferCLICwd("改 ~/Things/dev/apps/superai 的 README", []string{home}); got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

// A fresh run with no cwd starts in the repository its order names; one
// given a cwd keeps it.
func TestAFreshRunStartsInTheNamedRepo(t *testing.T) {
	noDiscovery(t)
	pwd := fakeCLI(t, "pwder", `printf '%s' "$PWD"`)
	a := codingApp(t, backend.ExternalAgents{Custom: []backend.CodingTool{{ID: "pwder", Name: "Pwder", Command: pwd + " {prompt}"}}})
	ws, _ := filepath.EvalSymlinks(a.settings.WorkspaceDir)
	repo := mkRepo(t, filepath.Join(ws, "apps", "superai-android"))

	if r := runTo(t, a, "pwder", "fix the build in superai-android"); r.State != "done" || r.Summary != repo {
		t.Fatalf("%s %s: ran in %q, want %q", r.State, r.Error, r.Summary, repo)
	}
	if r := runTo(t, a, "pwder", "写一首诗"); r.Summary != ws {
		t.Fatalf("an order naming nothing ran in %q, want the workspace", r.Summary)
	}
	done := make(chan CLIRun, 1)
	if _, err := a.startCLIRun(cliStart{Agent: "pwder", Prompt: "fix superai-android", Cwd: ws, Done: func(r CLIRun) { done <- r }}); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.Summary != ws {
		t.Fatalf("a cwd given was overridden: %q", r.Summary)
	}
}
