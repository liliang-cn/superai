package app

// The desktop app's first start. Someone who downloads it should be talking
// to it in a minute, so the window asks two things and SuperAI does the
// rest: may it look for the AI agents on this Mac (OnboardingNeeded,
// DetectAgents), and should one of them — Claude Code, Codex, pi or Hermes,
// signed in on the person's own account — be the one it thinks with
// (FinishOnboarding).
// Nothing is looked for, and nothing chosen, before the person says so; the
// question is asked once, and what is set afterwards in Settings stands.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	// CanBeDefault is whether SuperAI can think with it: Claude Code, Codex,
	// pi or Hermes (queenCLIs).
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
		out[i] = DetectedAgent{Name: c.Name, Label: agentLabel(c.Name), Path: c.Binary, CanBeDefault: canThinkForQueen(c.Name)}
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
// think with ("claude", "codex", "pi", "hermes", or "" for none), and agents whether SuperAI may hand
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
	if err := a.SaveSettings(next); err != nil {
		return err
	}
	// The coding CLIs' activity hooks go in once the person has said
	// SuperAI may use the agents here, not before (server.go).
	if next.ExternalAgents.Enabled {
		go autoInstallCLIHooks()
	}
	return nil
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

// ModelCheck is what CheckModel found at an endpoint.
type ModelCheck struct {
	// OK is whether the model answered (or, with no model named, whether the
	// endpoint listed any).
	OK bool `json:"ok"`
	// Models are the ones the endpoint lists, for picking one.
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

// CheckModel tries an OpenAI-compatible endpoint before it is saved: lists
// its models, and asks the named one for a single token, so a wrong key or
// model name is found while the person is still looking at the form.
func (a *App) CheckModel(baseURL, key, model string) ModelCheck {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return ModelCheck{Error: "the API address is empty"}
	}
	client := &http.Client{Timeout: 20 * time.Second}
	do := func(method, path string, body any) ([]byte, int, error) {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, base+path, r)
		if err != nil {
			return nil, 0, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if k := strings.TrimSpace(key); k != "" {
			req.Header.Set("Authorization", "Bearer "+k)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return raw, resp.StatusCode, nil
	}
	why := func(raw []byte, code int) string {
		var e struct {
			Error any `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		switch v := e.Error.(type) {
		case string:
			return fmt.Sprintf("HTTP %d: %s", code, v)
		case map[string]any:
			if m, ok := v["message"].(string); ok {
				return fmt.Sprintf("HTTP %d: %s", code, m)
			}
		}
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			return fmt.Sprintf("HTTP %d: the key was refused", code)
		}
		return fmt.Sprintf("HTTP %d", code)
	}

	out := ModelCheck{Models: []string{}}
	raw, code, err := do(http.MethodGet, "/models", nil)
	if err != nil {
		return ModelCheck{Models: []string{}, Error: "cannot reach " + base + ": " + err.Error()}
	}
	if code == http.StatusOK {
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(raw, &list)
		for _, m := range list.Data {
			if m.ID != "" {
				out.Models = append(out.Models, m.ID)
			}
		}
		slices.Sort(out.Models)
	} else if strings.TrimSpace(model) == "" {
		out.Error = why(raw, code)
		return out
	}
	if strings.TrimSpace(model) == "" {
		out.OK = len(out.Models) > 0
		if !out.OK {
			out.Error = "the endpoint lists no models; type the model's name"
		}
		return out
	}
	raw, code, err = do(http.MethodPost, "/chat/completions", map[string]any{
		"model":      strings.TrimSpace(model),
		"messages":   []map[string]string{{"role": "user", "content": "Reply with OK."}},
		"max_tokens": 8,
	})
	if err != nil {
		out.Error = "cannot reach " + base + ": " + err.Error()
		return out
	}
	if code != http.StatusOK {
		out.Error = why(raw, code)
		return out
	}
	out.OK = true
	return out
}

// mayInstallCLIHooks is whether this SuperAI may put its activity hooks into
// the coding CLIs on this machine at start: once a first start has allowed
// the agents here, or for one set up without a first start (a hive member).
func (a *App) mayInstallCLIHooks() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.settings
	return s != nil && (s.ExternalAgents.Enabled || s.Hive.Role != "")
}
