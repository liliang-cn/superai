package app

import (
	"testing"

	"github.com/liliang-cn/superai/internal/backend"
)

// firstRunApp is a desktop app on its first start, with signedInClaude
// answering bin (or "" for no signed-in claude).
func firstRunApp(t *testing.T, s backend.Settings, bin string) (*App, *engineEvents) {
	t.Helper()
	t.Setenv("SUPERAI_HOME", t.TempDir())
	was := signedInClaude
	signedInClaude = func() string { return bin }
	t.Cleanup(func() { signedInClaude = was })
	ev := &engineEvents{}
	a := &App{settings: &s, emitFn: ev.add}
	return a, ev
}

// A Mac with Claude Code signed in: the first start lets it think for the
// queen, so the person can type at once with no key and no model.
func TestTheFirstStartThinksWithASignedInClaudeCode(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeTurn, 0)
	a, ev := firstRunApp(t, backend.Settings{WorkspaceDir: t.TempDir()}, bin)
	a.firstRun()

	s := a.settings
	if !s.Onboarded || s.Hive.QueenEngine != "claude" || !s.ExternalAgents.Enabled || s.ExternalAgents.Binaries["claude"] != bin {
		t.Fatalf("settings after the first start: onboarded %v, engine %q, external %v, binaries %v",
			s.Onboarded, s.Hive.QueenEngine, s.ExternalAgents.Enabled, s.ExternalAgents.Binaries)
	}
	if st := a.GetStatus(); st["ready"] != true {
		t.Fatalf("not ready with Claude Code thinking: %v", st)
	}
	if got := a.Dashboard()["llm"].(map[string]any)["model"]; got != "Claude Code" {
		t.Fatalf("status bar says %v", got)
	}
	told := false
	ev.mu.Lock()
	for _, g := range ev.got {
		told = told || (g.name == "setup:engine" && g.p["engine"] == "claude")
	}
	ev.mu.Unlock()
	if !told {
		t.Fatal("the app was not told")
	}
}

// Someone who set a model up keeps it; the first start only marks itself done.
func TestTheFirstStartLeavesAModelThatIsSetUp(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeTurn, 0)
	a, _ := firstRunApp(t, backend.Settings{LLMModel: "some-model", WorkspaceDir: t.TempDir()}, bin)
	a.firstRun()
	if s := a.settings; !s.Onboarded || s.Hive.QueenEngine != "" || s.ExternalAgents.Enabled {
		t.Fatalf("a configured app was changed: engine %q, external %v", s.Hive.QueenEngine, s.ExternalAgents.Enabled)
	}
}

// It looks once: turning the engine off afterwards stands.
func TestTheFirstStartLooksOnce(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeTurn, 0)
	a, _ := firstRunApp(t, backend.Settings{Onboarded: true, WorkspaceDir: t.TempDir()}, bin)
	a.firstRun()
	if a.settings.Hive.QueenEngine != "" {
		t.Fatal("a later start chose the engine again")
	}
}

// No model of its own and the conversation busy with Claude Code: the next
// ask goes to Claude Code too, in a session of its own, not to an error.
func TestWithNoModelABusyEngineTakesTheNextAskToo(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.ownTurnFn = nil
	const conv = "c1"
	end := busyWith(h.a, "first", conv)
	defer end()

	id := h.a.SendChat(conv, "and this?", nil)
	if name, p := h.ev.wait(t, id); name != "chat:done" {
		t.Fatalf("ended with %s: %v", name, p)
	}
}
