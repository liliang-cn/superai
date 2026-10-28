package app

import (
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// onboardingApp is a desktop app on its first start, with the login shell's
// PATH already taken (a test keeps its own) and the sign-in checks answering
// signedIn.
func onboardingApp(t *testing.T, s backend.Settings, signedIn bool) *App {
	t.Helper()
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	loginPath.Do(func() {})
	was := signedInChecks
	signedInChecks = map[string]func(string) bool{"claude": func(string) bool { return signedIn }}
	t.Cleanup(func() { signedInChecks = was })
	return &App{settings: &s}
}

// The questions are asked on a first start with nothing to think with, and
// not once they are answered or a model is set up.
func TestOnboardingIsAskedOnceAndOnlyWhenNothingThinks(t *testing.T) {
	for _, c := range []struct {
		name string
		s    backend.Settings
		want bool
	}{
		{"first start", backend.Settings{}, true},
		{"answered", backend.Settings{Onboarded: true}, false},
		{"a model set up", backend.Settings{LLMModel: "m"}, false},
		{"an engine set up", backend.Settings{Hive: backend.HiveSettings{QueenEngine: "claude"}}, false},
	} {
		if got := onboardingApp(t, c.s, true).OnboardingNeeded(); got != c.want {
			t.Errorf("%s: asked %v, want %v", c.name, got, c.want)
		}
	}
}

// Looking finds Claude Code, says it is signed in and that SuperLeo can think
// with it, and lists it first.
func TestLookingFindsASignedInClaudeCodeFirst(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeTurn, 0)
	a := onboardingApp(t, backend.Settings{ExternalAgents: backend.ExternalAgents{Binaries: map[string]string{"claude": bin}}}, true)
	got := a.DetectAgents()
	if len(got) == 0 || got[0].Name != "claude" || got[0].Label != "Claude Code" || got[0].Path != bin ||
		got[0].SignedIn == nil || !*got[0].SignedIn || !got[0].CanBeDefault {
		t.Fatalf("found %+v", got)
	}
}

// "Use Claude Code": it thinks for the queen, and the app is ready with no
// model of its own.
func TestChoosingClaudeCodeMakesItTheOneThatThinks(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeTurn, 0)
	a := onboardingApp(t, backend.Settings{WorkspaceDir: t.TempDir(),
		ExternalAgents: backend.ExternalAgents{Binaries: map[string]string{"claude": bin}}}, true)
	if err := a.FinishOnboarding("claude", true); err != nil {
		t.Fatal(err)
	}
	s := a.settings
	if !s.Onboarded || s.Hive.QueenEngine != "claude" || !s.ExternalAgents.Enabled || s.ExternalAgents.Binaries["claude"] != bin {
		t.Fatalf("onboarded %v, engine %q, external %v, binaries %v", s.Onboarded, s.Hive.QueenEngine, s.ExternalAgents.Enabled, s.ExternalAgents.Binaries)
	}
	if a.OnboardingNeeded() {
		t.Fatal("asked again")
	}
	if st := a.GetStatus(); st["ready"] != true {
		t.Fatalf("not ready with Claude Code thinking: %v", st)
	}
	if got := a.Dashboard()["llm"].(map[string]any)["model"]; got != "Claude Code" {
		t.Fatalf("status bar says %v", got)
	}
}

// "Not now": nothing is chosen and no agent is let in, and it is not asked
// again.
func TestNotNowChoosesNothing(t *testing.T) {
	a := onboardingApp(t, backend.Settings{WorkspaceDir: t.TempDir()}, true)
	if err := a.FinishOnboarding("", false); err != nil {
		t.Fatal(err)
	}
	if s := a.settings; !s.Onboarded || s.Hive.QueenEngine != "" || s.ExternalAgents.Enabled {
		t.Fatalf("engine %q, external %v", s.Hive.QueenEngine, s.ExternalAgents.Enabled)
	}
	if a.OnboardingNeeded() {
		t.Fatal("asked again")
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
