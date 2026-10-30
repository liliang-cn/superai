package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// What hermes prints on its first launch in a home it has not seen: the
// dependency install, on stderr.
const hermesInstallChatter = `echo "hermes: preparing dependencies for this data root (the checkout's updates belong to /Users/x/.hermes)..." >&2
echo "Preparing the isolated Hermes runtime…" >&2
printf '  → Installing Python dependencies…\r' >&2
echo "  ✓ Installing Python dependencies" >&2
`

// fakeHermes is a stand-in for hermes: --version answers, -z is the warm-up
// (a turn is --oneshot), anything else is a turn. body runs for a turn, warm
// for the warm-up; every warm-up is counted in dir.
func fakeHermes(t *testing.T, body, warm string) (bin, dir string) {
	t.Helper()
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HERMES_HOME", t.TempDir()) // the person's own hermes: only pointed at, never written
	dir = t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "Hermes Agent v0.22.5"; exit 0; fi
d=%q
if [ "$1" = "-z" ]; then
  echo start >> "$d/warm.starts"
  printf '%%s' "$HERMES_HOME" > "$d/warm.home"
  %s
  exit 0
fi
printf '%%s' "$HERMES_HOME" > "$d/turn.home"
%s
`, dir, warm, body)
	bin = filepath.Join(dir, "hermes")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func hermesHarness(t *testing.T, body string) (*engineHarness, string) {
	t.Helper()
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	bin, dir := fakeHermes(t, body, "")
	h.a.settings.ExternalAgents.Binaries = map[string]string{"hermes": bin}
	h.a.settings.Hive.QueenEngine = "hermes"
	return h, dir
}

func hermesRun(t *testing.T, a *App) CLIRun {
	t.Helper()
	s := a.cliStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.Agent == "hermes" {
			out := *r
			out.Events = append([]CLIRunEvent(nil), r.Events...)
			return out
		}
	}
	t.Fatal("no hermes run")
	return CLIRun{}
}

// A first run of hermes installs its dependencies before it answers. The
// install is in the log and the run's events; the answer is the answer.
func TestHermesFirstRunInstallStaysOutOfTheAnswer(t *testing.T) {
	const answer = "蜂后在呢，今天有三件事。"
	h, dir := hermesHarness(t, hermesInstallChatter+"sleep 0.3\nprintf '"+answer+"'")

	id := h.a.SendChat("c1", "今天有什么安排？", nil)
	name, done := h.ev.wait(t, id)
	if name != "chat:done" || done["final"] != answer || done["engine"] != "hermes" {
		t.Fatalf("%s %v", name, done)
	}
	install := []string{"Installing", "preparing dependencies", "isolated Hermes runtime", "→", "✓"}
	for _, e := range h.ev.all("chat:event", id) {
		if e["type"] != "partial" {
			continue
		}
		for _, w := range install {
			if strings.Contains(fmt.Sprint(e["content"]), w) {
				t.Fatalf("install output in the answer as it was written: %v", e)
			}
		}
	}
	h.mu.Lock()
	stored := append([][2]string(nil), h.stored...)
	h.mu.Unlock()
	if len(stored) != 1 || stored[0][1] != answer {
		t.Fatalf("the conversation keeps %v", stored)
	}

	// Where it went instead: the run's events.
	run := hermesRun(t, h.a)
	var notes []string
	for _, e := range run.Events {
		if e.Kind == "note" {
			notes = append(notes, e.Text)
		}
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "→ Installing Python dependencies…") || !strings.Contains(joined, "preparing dependencies") {
		t.Fatalf("the run's events: %q", notes)
	}
	if strings.Contains(run.Summary, "Installing") {
		t.Fatalf("the run's answer: %q", run.Summary)
	}

	// And the home it ran in shares the warmed environment.
	home, _ := os.ReadFile(filepath.Join(dir, "turn.home"))
	if target, err := os.Readlink(filepath.Join(string(home), "installs")); err != nil || target != hermesInstallsDir() {
		t.Fatalf("the queen's hermes home %s: installs → %q (%v)", home, target, err)
	}
}

// A hermes that fails says why on stderr: that is the reason the turn is
// handed on with, not an answer.
func TestHermesFailureOnStderrIsTheReason(t *testing.T) {
	h, _ := hermesHarness(t, "echo 'hermes: no dependency environment is committed for this install; run `hermes pm repair`' >&2\nexit 1")
	h.a.settings.Hive.QueenEngines = []backend.QueenEngineEntry{{Engine: "hermes", Enabled: true}}

	id := h.a.SendChat("c1", "在吗", nil)
	name, end := h.ev.wait(t, id)
	if name == "chat:done" {
		t.Fatalf("a failed hermes answered: %v", end)
	}
	if !strings.Contains(fmt.Sprint(end["error"]), "no dependency environment is committed") {
		t.Fatalf("%s %v", name, end)
	}
	for _, e := range h.ev.all("chat:event", id) {
		if e["type"] == "partial" {
			t.Fatalf("a failure was written as an answer: %v", e)
		}
	}
}

// SuperLeo builds hermes's environment itself, once, in a home of its own
// whose installs every queen home shares; what hermes says goes to the log and
// hermes:warm, its status report nowhere.
func TestWarmHermesBuildsTheSharedEnvironmentOnce(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	bin, dir := fakeHermes(t, "printf never", hermesInstallChatter+`sleep 0.2
echo "Provider: anthropic  key sk-ant-secret-0000"`)
	ev := &engineEvents{}
	a := &App{settings: &backend.Settings{ExternalAgents: backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"hermes": bin}}}}
	a.emitFn = ev.add

	a.warmHermes("start")
	a.warmHermes("found") // already warming: nothing more
	deadline := time.Now().Add(10 * time.Second)
	for a.hermesWarming() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	a.warmHermes("found") // ready: nothing more
	time.Sleep(100 * time.Millisecond)

	// SuperLeo started again with the same hermes: warm already, no one-shot.
	again := &App{settings: a.settings}
	again.emitFn = func(string, map[string]any) {}
	again.warmHermes("start")
	for again.hermesWarming() {
		time.Sleep(20 * time.Millisecond)
	}

	starts, _ := os.ReadFile(filepath.Join(dir, "warm.starts"))
	if strings.Count(string(starts), "start") != 1 {
		t.Fatalf("hermes status ran %q", starts)
	}
	home, _ := os.ReadFile(filepath.Join(dir, "warm.home"))
	if string(home) != filepath.Join(hermesQueenRoot(), "warm") {
		t.Fatalf("warmed in %q", home)
	}
	if target, err := os.Readlink(filepath.Join(string(home), "installs")); err != nil || target != hermesInstallsDir() {
		t.Fatalf("the warm home's installs → %q (%v)", target, err)
	}
	var lines []string
	final := ""
	ev.mu.Lock()
	for _, g := range ev.got {
		if g.name != "hermes:warm" {
			continue
		}
		if l, ok := g.p["line"].(string); ok {
			lines = append(lines, l)
		}
		if s := g.p["state"].(string); s != "warming" {
			final = s
		}
		if strings.Contains(fmt.Sprint(g.p), "sk-ant") {
			t.Errorf("the status report was passed on: %v", g.p)
		}
	}
	ev.mu.Unlock()
	if final != "ready" || !strings.Contains(strings.Join(lines, "\n"), "→ Installing Python dependencies…") {
		t.Fatalf("hermes:warm said %q, ended %q", lines, final)
	}

	// No hermes here: nothing to warm.
	b := &App{settings: &backend.Settings{ExternalAgents: backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"hermes": "/nonexistent/hermes"}}}}
	b.warmHermes("start")
	if b.hermesWarming() {
		t.Fatal("warming a hermes that is not there")
	}
}

// Two queen homes, for two sets of servers, share one environment.
func TestHermesQueenHomesShareOneEnvironment(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	one, err := hermesQueenHome(map[string]any{"mcp_servers": map[string]any{"hive": map[string]any{"url": "http://127.0.0.1:1/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	two, err := hermesQueenHome(map[string]any{"mcp_servers": map[string]any{"noted": map[string]any{"url": "http://127.0.0.1:2/mcp"}}})
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("one home for two configs")
	}
	for _, h := range []string{one, two} {
		if target, err := os.Readlink(filepath.Join(h, "installs")); err != nil || target != hermesInstallsDir() {
			t.Fatalf("%s: installs → %q (%v)", h, target, err)
		}
	}
	// A home that built its own before keeps it.
	old := filepath.Join(hermesQueenRoot(), "0123456789ab")
	if err := os.MkdirAll(filepath.Join(old, "installs", "k"), 0o700); err != nil {
		t.Fatal(err)
	}
	hermesShareInstalls(old)
	if fi, err := os.Lstat(filepath.Join(old, "installs")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("an old home's own environment was replaced: %v %v", fi, err)
	}
}
