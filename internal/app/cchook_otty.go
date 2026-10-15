package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"time"
)

// Otty is a terminal that knows which agent session runs in each of its
// panes: its own Claude Code hook reports the session id, and `pane list`
// gives it back. That is the proof of ownership a tty number cannot give, so
// a pane found this way is typed into without the tty checks.

const ottyCLI = "/Applications/Otty.app/Contents/MacOS/otty-cli"

func ottyPath() string {
	if _, err := os.Stat(ottyCLI); err == nil {
		return ottyCLI
	}
	if p, err := exec.LookPath("otty-cli"); err == nil {
		return p
	}
	return ""
}

// ottyPaneFor is the Otty pane running that Claude Code session, or "".
func ottyPaneFor(ctx context.Context, sessionID string) string {
	cli := ottyPath()
	if cli == "" || sessionID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "pane", "list", "--json").Output()
	if err != nil {
		return ""
	}
	return ottyPaneIn(out, sessionID)
}

func ottyPaneIn(listJSON []byte, sessionID string) string {
	var r struct {
		Data []struct {
			ID      string `json:"id"`
			Session string `json:"agent_session_id"`
		} `json:"data"`
	}
	if json.Unmarshal(listJSON, &r) != nil {
		return ""
	}
	for _, p := range r.Data {
		if p.Session == sessionID && p.ID != "" {
			return p.ID
		}
	}
	return ""
}

// ottyType pastes text into the pane and presses Return. The text goes as a
// bracketed paste read verbatim from a file: a newline in the answer stays in
// the prompt instead of sending half of it, and a backslash is not an escape.
func ottyType(ctx context.Context, pane, text string) error {
	cli := ottyPath()
	if cli == "" {
		return errors.New("otty-cli not found")
	}
	f, err := os.CreateTemp("", "superai-otty-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString("\x1b[200~" + text + "\x1b[201~"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, cli, "pane", "send-text", "--pane", pane, "--from-file", f.Name()).CombinedOutput(); err != nil {
		return errors.New(err.Error() + ": " + string(out))
	}
	// The TUI takes the paste before the key; Return right behind it can land
	// inside the paste handling.
	time.Sleep(150 * time.Millisecond)
	if out, err := exec.CommandContext(ctx, cli, "pane", "send-keys", "--pane", pane, "key:Enter").CombinedOutput(); err != nil {
		return errors.New(err.Error() + ": " + string(out))
	}
	return nil
}
