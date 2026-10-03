package app

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// TakeOver opens Terminal on this Mac in cwd, resuming a coding agent's
// session there, so the person can carry on where the agent is. The desktop
// window's "Take over" on a coding run.
func (a *App) TakeOver(agent, session, cwd string) error {
	if a.ctx == nil {
		return errOnlyInTheWindow
	}
	if runtime.GOOS != "darwin" {
		return errors.New("taking over opens Terminal, which needs macOS")
	}
	if !sessionIDPattern.MatchString(session) {
		return errors.New("this run has no session to resume")
	}
	var resume string
	switch {
	case strings.HasPrefix(agent, "claude"):
		resume = "claude --resume " + session
	case strings.HasPrefix(agent, "codex"):
		resume = "codex resume " + session
	default:
		return fmt.Errorf("%s sessions cannot be resumed from here", agent)
	}
	cmd := resume
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		cmd = "cd " + shellQuote(cwd) + " && " + resume
	}
	script := `tell application "Terminal"
	activate
	do script "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(cmd) + `"
end tell`
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("opening Terminal: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
