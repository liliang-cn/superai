// superai-cchook is Claude Code's hook for the events that fire on every
// prompt and every tool call: UserPromptSubmit, PreToolUse, PostToolUse.
//
// Claude Code waits for a PreToolUse hook before it runs the tool, and runs
// PostToolUse after, so the hook's own cost is paid twice per tool call in
// every session. `superai hook` is the whole SuperAI binary, and starting it
// is most of 50 ms before it reads a byte; this one imports the standard
// library's JSON and a socket, and nothing else.
//
// It reads the payload, keeps the session id, the working directory's last
// element and the tool's name — never the prompt, the tool's arguments or
// its result — and hands them to the SuperAI serving on this machine over its
// unix socket (internal/app/cchook_local.go), giving up after a short while.
// It prints nothing and exits 0 whatever happens: a hook that fails must not
// cost the session anything but the event.
//
// The rest of the hook events (SessionStart, Stop, Notification,
// PermissionRequest, ...) stay with `superai hook`: they are rare, and some of
// them read a transcript or wait on the phone.
//
// `superai-cchook --cli codex <Event>` is the same for another CLI whose
// hooks take Claude Code's shape (codex's hooks.json): every event it has
// goes this way, since for that CLI they only move its activity record.
package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// budget is everything one call may spend talking to the socket.
const budget = 300 * time.Millisecond

// maxPayload bounds what is read from stdin (a Write of a large file).
const maxPayload = 8 << 20

// socketName and hookPath are the node's (cchook.go, cchook_local.go).
const (
	socketName = "cc-hook.sock"
	hookPath   = "/cc/hook"
)

// events are the ones this binary handles; any other is ignored.
var events = map[string]bool{"UserPromptSubmit": true, "PreToolUse": true, "PostToolUse": true}

// cliEvents are the events taken for another CLI (--cli), and the name each
// travels as: an interrupted turn is over, as a stopped one is.
var cliEvents = map[string]string{
	"SessionStart": "SessionStart", "SessionEnd": "SessionEnd", "UserPromptSubmit": "UserPromptSubmit",
	"PreToolUse": "PreToolUse", "PostToolUse": "PostToolUse", "PermissionRequest": "PermissionRequest",
	"Stop": "Stop", "Interrupt": "Stop",
}

// payload is the little of Claude Code's hook input this reads.
type payload struct {
	Event     string `json:"hook_event_name"`
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
}

// event is what goes to the node: the shape of ccHookEvent, these fields only.
type event struct {
	CLI     string `json:"cli,omitempty"`
	Event   string `json:"event"`
	Session string `json:"session"`
	Cwd     string `json:"cwd,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Pane    string `json:"pane,omitempty"`
}

// parse turns the hook input into the event to send, or false for one not
// to send. name is the event named on the command line, else the payload's.
func parse(name string, in []byte) (event, bool) {
	return parseFor("", name, in)
}

// parseFor is parse for a CLI other than Claude Code when cli is set.
func parseFor(cli, name string, in []byte) (event, bool) {
	var p payload
	if json.Unmarshal(in, &p) != nil {
		return event{}, false
	}
	if name == "" {
		name = p.Event
	}
	ev := event{CLI: cli, Event: name, Session: strings.TrimSpace(p.SessionID)}
	if cli != "" {
		ev.Event = cliEvents[name]
		if ev.Event == "" || ev.Session == "" {
			return event{}, false
		}
	} else if !events[name] || ev.Session == "" {
		return event{}, false
	}
	if cwd := strings.TrimRight(strings.TrimSpace(p.Cwd), "/"); cwd != "" {
		ev.Cwd = filepath.Base(cwd)
	}
	if name == "PreToolUse" || name == "PostToolUse" || name == "PermissionRequest" {
		ev.Tool = strings.TrimSpace(p.ToolName)
	}
	return ev, true
}

// sockets are where a serving SuperAI may listen, in the order `superai hook`
// tries them: an explicit socket, $SUPERAI_HOME, the node install this
// binary sits in (~/.superai-node/bin/superai-cchook → ~/.superai-node), then
// the two usual homes.
func sockets() []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, have := range out {
			if have == p {
				return
			}
		}
		out = append(out, p)
	}
	add(strings.TrimSpace(os.Getenv("SUPERAI_HOOK_SOCKET")))
	if h := strings.TrimSpace(os.Getenv("SUPERAI_HOME")); h != "" {
		if strings.HasPrefix(h, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				h = filepath.Join(home, h[2:])
			}
		}
		add(filepath.Join(h, socketName))
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(filepath.Dir(exe)), socketName))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".superai-node", socketName))
		add(filepath.Join(home, ".superai", socketName))
	}
	return out
}

// send posts the event to the first socket that takes it, by hand: an HTTP
// client would cost more to start than the whole exchange takes.
func send(socks []string, ev event, deadline time.Time) bool {
	body, err := json.Marshal(ev)
	if err != nil {
		return false
	}
	req := "POST " + hookPath + " HTTP/1.1\r\nHost: superai\r\nContent-Type: application/json\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + string(body)
	for _, s := range socks {
		if time.Now().After(deadline) {
			return false
		}
		c, err := net.DialTimeout("unix", s, time.Until(deadline))
		if err != nil {
			continue // no such socket, or one left by a process that is gone
		}
		_ = c.SetDeadline(deadline)
		_, err = io.WriteString(c, req)
		if err == nil {
			// Wait for the status line: the node has the event once it answers.
			buf := make([]byte, 64)
			n, _ := c.Read(buf)
			err = nil
			if n < 12 || !strings.HasPrefix(string(buf[:n]), "HTTP/1.1 200") {
				err = io.ErrUnexpectedEOF
			}
		}
		c.Close()
		return err == nil
	}
	return false
}

// run is the whole hook; main only supplies the process.
func run(args []string, stdin io.Reader) {
	// A run SuperAI started itself already reports through cli:run.
	if os.Getenv("SUPERAI_CLI_RUN") != "" {
		return
	}
	cli := ""
	if len(args) >= 2 && args[0] == "--cli" {
		cli, args = strings.ToLower(strings.TrimSpace(args[1])), args[2:]
		if cli == "" || cli == "claude" {
			return
		}
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	in, err := io.ReadAll(io.LimitReader(stdin, maxPayload))
	if err != nil {
		return
	}
	ev, ok := parseFor(cli, name, in)
	if !ok {
		return
	}
	ev.Pane = os.Getenv("TMUX_PANE")
	send(sockets(), ev, time.Now().Add(budget))
}

func main() {
	defer func() { _ = recover(); os.Exit(0) }()
	run(os.Args[1:], os.Stdin)
}
