package app

// Hermes made ready before a turn needs it.
//
// A hermes home of SuperLeo's (queen_engine_cli.go) is one hermes has never
// seen, and hermes builds a dependency environment for a home on its first
// launch, in two goes: the base one as the command starts (eight minutes on
// the Mac), and every extra as the gateway a one-shot starts comes up (ten
// more, while the turn waits on it). All of it was inside the person's first
// turn, and what it printed came back as the answer. Now every such home
// shares one environment, and SuperLeo builds it when it starts or finds
// hermes installed: one short one-shot in a home of its own, which goes
// through both, its stderr to the log and to hermes:warm events. The hermes
// version it warmed is kept beside the environment, so a start with the same
// hermes asks no model at all. A turn after that starts in seconds.

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// hermesWarmState is where the warm-up stands: "" not tried, "warming",
// "ready", "failed".
type hermesWarmState struct {
	mu    sync.Mutex
	state string
	at    time.Time
}

const (
	// hermesWarmTimeout bounds the warm-up; a dependency install on a slow
	// link takes minutes, not this long.
	hermesWarmTimeout = 30 * time.Minute
	// hermesWarmRetry is how long a failed warm-up waits to be tried again.
	hermesWarmRetry = 10 * time.Minute
)

// hermesBinary is the hermes this app would run, when it may run one.
func (a *App) hermesBinary() string {
	a.mu.Lock()
	var ext backend.ExternalAgents
	if a.settings != nil {
		ext = a.settings.ExternalAgents
	}
	a.mu.Unlock()
	if !ext.Enabled {
		return ""
	}
	for _, d := range ext.Disabled {
		if strings.EqualFold(strings.TrimSpace(d), "hermes") {
			return ""
		}
	}
	for _, c := range installedCLIs(ext.Binaries) {
		if c.Name == "hermes" {
			return c.Binary
		}
	}
	return ""
}

// warmHermes builds the shared hermes environment in the background, once:
// a call while it is warming or once it is ready does nothing, and one after
// a failure waits hermesWarmRetry.
func (a *App) warmHermes(why string) {
	w := &a.hermesWarm
	w.mu.Lock()
	busy := w.state == "warming" || w.state == "ready" || (w.state == "failed" && time.Since(w.at) < hermesWarmRetry)
	w.mu.Unlock()
	if busy {
		return
	}
	bin := a.hermesBinary()
	if bin == "" {
		return
	}
	w.mu.Lock()
	if w.state == "warming" || w.state == "ready" {
		w.mu.Unlock()
		return
	}
	w.state, w.at = "warming", time.Now()
	w.mu.Unlock()
	go a.runHermesWarm(bin, why)
}

// hermesWarming is whether the warm-up is going on now.
func (a *App) hermesWarming() bool {
	w := &a.hermesWarm
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state == "warming"
}

func (a *App) runHermesWarm(bin, why string) {
	started := time.Now()
	state, errText := "ready", ""
	defer func() {
		w := &a.hermesWarm
		w.mu.Lock()
		w.state, w.at = state, time.Now()
		w.mu.Unlock()
		secs := int(time.Since(started).Seconds())
		if state == "ready" {
			slog.Info("hermes warm-up: ready", "seconds", secs, "why", why)
		} else {
			slog.Warn("hermes warm-up failed", "seconds", secs, "why", why, "error", errText)
		}
		a.emit("hermes:warm", map[string]any{"state": state, "seconds": secs, "error": errText})
	}()

	home := filepath.Join(hermesQueenRoot(), "warm")
	if err := os.MkdirAll(home, 0o700); err != nil {
		state, errText = "failed", err.Error()
		return
	}
	hermesShareInstalls(home)
	ctx, cancel := context.WithTimeout(context.Background(), hermesWarmTimeout)
	defer cancel()

	version := hermesVersion(ctx, bin)
	stamp := filepath.Join(hermesInstallsDir(), hermesWarmStamp)
	if old, err := os.ReadFile(stamp); err == nil && version != "" && strings.TrimSpace(string(old)) == version {
		slog.Info("hermes warm-up: already warm for this hermes", "version", version, "why", why)
		return
	}
	slog.Info("hermes warm-up: preparing its environment", "binary", bin, "home", home, "version", version, "why", why)
	a.emit("hermes:warm", map[string]any{"state": "warming"})

	// What a queen turn does, so it builds what a turn needs: the command's
	// own dependencies, then the gateway's. The answer is not wanted, only
	// that it came.
	cmd := exec.CommandContext(ctx, bin, "-z", hermesWarmPrompt)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HERMES_HOME="+home)
	if own := personHermesHome(); own != "" {
		cmd.Env = append(cmd.Env, "HERMES_MANAGED_DIR="+own)
	}
	// Its downloads go out the way this machine's own requests do.
	for k, v := range backend.DetachedProxyEnv() {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		state, errText = "failed", err.Error()
		return
	}
	if err := cmd.Start(); err != nil {
		state, errText = "failed", err.Error()
		return
	}
	last := ""
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 16<<10), 1<<20)
	sc.Split(scanLinesOrReturns)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		last = line
		slog.Info("hermes warm-up", "line", cliClip(line, 500))
		a.emit("hermes:warm", map[string]any{"state": "warming", "line": cliClip(line, 300)})
	}
	if err := cmd.Wait(); err != nil {
		state, errText = "failed", firstNonEmpty(last, err.Error())
		return
	}
	if version != "" {
		_ = os.WriteFile(stamp, []byte(version+"\n"), 0o600)
	}
}

// hermesWarmStamp, beside the shared environment, names the hermes it was
// warmed for.
const hermesWarmStamp = ".superleo-warmed"

// hermesWarmPrompt is the one-shot the warm-up asks: the shortest answer.
const hermesWarmPrompt = "Reply with the single word OK."

// hermesVersion is the first line hermes --version prints, "" when it says
// nothing.
func hermesVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// scanLinesOrReturns splits on a newline or a carriage return: a progress
// line redrawn in place is a line each time.
func scanLinesOrReturns(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
