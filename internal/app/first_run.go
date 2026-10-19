package app

// The desktop app's first start. Someone who downloads it and already has
// Claude Code signed in on this Mac should be able to type at once: no key,
// no model to pick. So the first start looks for a signed-in `claude` and,
// finding one, lets it think for the queen. It looks once; what the person
// sets afterwards stands.

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// engineThinks is whether a queen engine answers the person's words, so the
// app can be used before it has a model of its own.
func engineThinks(s *backend.Settings) bool {
	return s != nil && s.Hive.Role != backend.HiveRoleWorker && strings.TrimSpace(s.Hive.QueenEngine) != ""
}

// firstRun takes the login shell's PATH, and on the first start lets a
// signed-in Claude Code think for the queen when nothing else is set to.
func (a *App) firstRun() {
	usePathOfLoginShell()
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	}
	a.mu.Unlock()
	if next.Onboarded {
		return
	}
	next.Onboarded = true
	adopted := false
	if strings.TrimSpace(next.LLMModel) == "" && strings.TrimSpace(next.Hive.QueenEngine) == "" && next.Hive.Role == "" {
		if path := signedInClaude(); path != "" {
			next.ExternalAgents.Enabled = true
			binaries := map[string]string{}
			for k, v := range next.ExternalAgents.Binaries {
				binaries[k] = v
			}
			binaries["claude"] = path
			next.ExternalAgents.Binaries = binaries
			next.Hive.QueenEngine = "claude"
			fallback := true
			next.Hive.QueenFallback = &fallback
			adopted = true
		}
	}
	if err := a.SaveSettings(next); err != nil {
		log.Printf("first run: %v", err)
		return
	}
	if adopted {
		log.Printf("first run: Claude Code on this Mac thinks for the queen")
		a.emit("setup:engine", map[string]any{"engine": "claude", "label": engineLabel("claude")})
	}
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

// signedInClaude is the path of a `claude` that says it is signed in, or "".
// A variable so a test can stand in for the CLI.
var signedInClaude = func() string {
	path, err := exec.LookPath("claude")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "auth", "status")
	cmd.Env = append(os.Environ(), "SUPERAI_CLI_RUN=1")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var st struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if json.Unmarshal(out, &st) != nil || !st.LoggedIn {
		return ""
	}
	return path
}
