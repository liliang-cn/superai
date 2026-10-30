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

// goCacheEnv points go's caches at fresh directories and returns them as the
// real paths codex is handed.
func goCacheEnv(t *testing.T) (cache, mod string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	cache, mod = filepath.Join(base, "go-build"), filepath.Join(base, "mod")
	t.Setenv("GOCACHE", cache)
	t.Setenv("GOMODCACHE", mod)
	return cache, mod
}

// In a Go module, go's build and module caches are writable too; anywhere
// else nothing is added.
func TestCodexMayWriteGoCachesInAGoModule(t *testing.T) {
	cache, mod := goCacheEnv(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	module := filepath.Join(base, "superai")
	sub := filepath.Join(module, "internal", "app")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(module, "go.mod"), []byte("module example.com/superai\n\ngo 1.22\n"), 0o644)

	if got := goCaches(sub); !slices.Equal(got, []string{cache, mod}) {
		t.Fatalf("got %v, want %v", got, []string{cache, mod})
	}
	for _, p := range []string{cache, mod} {
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			t.Errorf("%s was not made: %v", p, err)
		}
	}

	plain := filepath.Join(base, "plain")
	os.MkdirAll(plain, 0o755)
	if got := goCaches(plain); len(got) != 0 {
		t.Fatalf("outside a module: %v", got)
	}

	// GOCACHE=off is no directory to write.
	t.Setenv("GOCACHE", "off")
	if got := goCaches(sub); !slices.Equal(got, []string{mod}) {
		t.Fatalf("GOCACHE=off: got %v, want %v", got, []string{mod})
	}
}

// Without go on PATH the caches still come from the environment.
func TestGoCachesWithoutGoOnPath(t *testing.T) {
	cache, mod := goCacheEnv(t)
	t.Setenv("PATH", t.TempDir())
	module, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(module, "go.work"), []byte("go 1.22\n"), 0o644)
	if got := goCaches(module); !slices.Equal(got, []string{cache, mod}) {
		t.Fatalf("got %v, want %v", got, []string{cache, mod})
	}
}

// A sandboxed codex run in a Go repository is started with go's caches
// writable, beside the roots and the repository's .git.
func TestASandboxedCodexRunInAGoModuleMayBuild(t *testing.T) {
	cache, mod := goCacheEnv(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	bin := fakeCLI(t, "codex", `for a in "$@"; do printf '%s\n' "$a"; done > `+argsFile)
	noDiscovery(t, agentexec.Installed{Name: "codex", Binary: bin})
	root, _ := filepath.EvalSymlinks(t.TempDir())
	a := codingApp(t, backend.ExternalAgents{Roots: []string{root}})
	repo := mkRepo(t, filepath.Join(root, "superai"))
	os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/superai\n\ngo 1.22\n"), 0o644)

	done := make(chan CLIRun, 1)
	if _, err := a.startCLIRun(cliStart{Agent: "codex", Prompt: "fix it and run the tests", Cwd: repo, Ask: true, Done: func(r CLIRun) { done <- r }}); err != nil {
		t.Fatal(err)
	}
	<-done
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var roots string
	for l := range strings.SplitSeq(string(raw), "\n") {
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
	for _, want := range []string{root, filepath.Join(repo, ".git"), cache, mod} {
		if !slices.Contains(cfg.V, want) {
			t.Errorf("%s is not writable: %v", want, cfg.V)
		}
	}
}
