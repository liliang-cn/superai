package app

// The desktop app's first start. Someone who downloads it should be talking
// to it in a minute, so the window asks two things and SuperAI does the
// rest: may it look for the AI agents on this Mac (OnboardingNeeded,
// DetectAgents), and should one of them — Claude Code, signed in on the
// person's own subscription — be the one it thinks with (FinishOnboarding).
// Nothing is looked for, and nothing chosen, before the person says so; the
// question is asked once, and what is set afterwards in Settings stands.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// engineThinks is whether a queen engine answers the person's words, so the
// app can be used before it has a model of its own.
func engineThinks(s *backend.Settings) bool {
	return s != nil && s.Hive.Role != backend.HiveRoleWorker && strings.TrimSpace(s.Hive.QueenEngine) != ""
}

// firstRun takes the login shell's PATH, so the agents a person installed
// are found and run even when the app was opened from the Finder.
func (a *App) firstRun() { loginPath.Do(usePathOfLoginShell) }

var loginPath sync.Once

// OnboardingNeeded is whether the first start's questions are still to be
// asked: never answered, and nothing set up to think with yet.
func (a *App) OnboardingNeeded() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.settings
	return s != nil && !s.Onboarded && strings.TrimSpace(s.LLMModel) == "" && !engineThinks(s) && s.Hive.Role == ""
}

// DetectedAgent is an AI agent found on this Mac.
type DetectedAgent struct {
	Name  string `json:"name"`  // claude, codex, …
	Label string `json:"label"` // Claude Code, Codex, …
	Path  string `json:"path"`
	// SignedIn is whether it says it is signed in; nil when it cannot say.
	SignedIn *bool `json:"signedIn,omitempty"`
	// CanBeDefault is whether SuperAI can think with it: Claude Code, so far.
	CanBeDefault bool `json:"canBeDefault"`
}

// DetectAgents looks for the AI agents installed on this Mac and asks the
// ones that can tell whether they are signed in. Asked for only once the
// person said SuperAI may look.
func (a *App) DetectAgents() []DetectedAgent {
	loginPath.Do(usePathOfLoginShell)
	a.mu.Lock()
	var overrides map[string]string
	if a.settings != nil {
		overrides = a.settings.ExternalAgents.Binaries
	}
	a.mu.Unlock()
	found := installedCLIs(overrides)
	out := make([]DetectedAgent, len(found))
	var wg sync.WaitGroup
	for i, c := range found {
		out[i] = DetectedAgent{Name: c.Name, Label: agentLabel(c.Name), Path: c.Binary, CanBeDefault: c.Name == "claude"}
		if check := signedInChecks[c.Name]; check != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				in := check(c.Binary)
				out[i].SignedIn = &in
			}()
		}
	}
	wg.Wait()
	// The ones SuperAI can think with first, then by name.
	slices.SortStableFunc(out, func(x, y DetectedAgent) int {
		if x.CanBeDefault != y.CanBeDefault {
			if x.CanBeDefault {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Label, y.Label)
	})
	return out
}

// FinishOnboarding records the person's answers: engine is the agent to
// think with ("claude", or "" for none), and agents whether SuperAI may hand
// work to the agents it found. The questions are not asked again.
func (a *App) FinishOnboarding(engine string, agents bool) error {
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	}
	a.mu.Unlock()
	next.Onboarded = true
	engine = strings.TrimSpace(engine)
	if agents || engine != "" {
		next.ExternalAgents.Enabled = true
	}
	if engine != "" {
		for _, c := range installedCLIs(next.ExternalAgents.Binaries) {
			if c.Name == engine {
				binaries := map[string]string{}
				for k, v := range next.ExternalAgents.Binaries {
					binaries[k] = v
				}
				binaries[engine] = c.Binary
				next.ExternalAgents.Binaries = binaries
			}
		}
		next.Hive.QueenEngine = engine
		fallback := true
		next.Hive.QueenFallback = &fallback
	}
	return a.SaveSettings(next)
}

// agentLabels are the names people know the agents by.
var agentLabels = map[string]string{
	"claude": "Claude Code", "codex": "Codex", "gemini": "Gemini CLI", "opencode": "opencode", "pi": "pi",
	"goose": "Goose", "copilot": "GitHub Copilot", "cursor-agent": "Cursor", "kimi": "Kimi", "agy": "Antigravity",
	"hermes": "Hermes", "openclaw": "OpenClaw",
}

func agentLabel(name string) string {
	if l := agentLabels[name]; l != "" {
		return l
	}
	return name
}

// usePathOfLoginShell puts the login shell's PATH in front of this
// process's: opened from the Finder, the app gets launchd's short PATH, and
// claude, codex and node live where the shell looks (~/.local/bin, Homebrew,
// npm's prefix).
func usePathOfLoginShell() {
	login := loginEnv()["PATH"]
	if login == "" {
		return
	}
	seen := map[string]bool{}
	var dirs []string
	for _, d := range filepath.SplitList(login + string(os.PathListSeparator) + os.Getenv("PATH")) {
		if d != "" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	_ = os.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// signedInChecks ask an agent whether it is signed in, for the agents that
// can say. A variable so a test can stand in for them.
var signedInChecks = map[string]func(bin string) bool{
	// {"loggedIn": true, …}
	"claude": func(bin string) bool {
		out, err := askAgent(bin, false, "auth", "status")
		var st struct {
			LoggedIn bool `json:"loggedIn"`
		}
		return err == nil && json.Unmarshal(out, &st) == nil && st.LoggedIn
	},
	// "Logged in using ChatGPT", on stderr.
	"codex": func(bin string) bool {
		out, err := askAgent(bin, true, "login", "status")
		return err == nil && strings.Contains(strings.ToLower(string(out)), "logged in")
	},
}

// askAgent runs one short command of an agent's CLI and returns what it
// said: its stdout, or with both its stderr too.
func askAgent(bin string, both bool, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "SUPERAI_CLI_RUN=1")
	if both {
		return cmd.CombinedOutput()
	}
	return cmd.Output()
}
