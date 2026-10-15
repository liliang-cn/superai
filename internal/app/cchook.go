package app

// `superai hook <Event>`: Claude Code's hooks, handed to the SuperAI on this
// machine so a turn that finished in a terminal reaches the phone, and an
// answer typed on the phone reaches the terminal.
//
// Claude Code runs the hook command for every matching event and waits for
// it, so the command is held to three rules: it exits 0 whatever happens, it
// gives up within hookBudget, and it writes nothing to stdout except a parked
// reply on Stop — the one output Claude Code acts on ({"decision":"block"}
// hands the reply back as the next instruction). Anything else would either
// stall the person at the keyboard or put words in the agent's mouth.
//
// The hook itself only parses and forwards. Everything with state — which
// sessions exist, where their terminals are, whether anyone is at the Mac,
// what is parked for whom — lives in the running `superai serve` / `superai
// agent`, reached over a unix socket in its data directory (cchook_local.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/agentexec/hooks"
	"github.com/liliang-cn/superai/internal/backend"
)

// hookBudget is everything the hook may spend, reading the transcript
// included. Claude Code is waiting on it.
const hookBudget = 3 * time.Second

// ccHookSocketName is the socket a serving SuperAI listens for hooks on, in
// its data directory.
const ccHookSocketName = "cc-hook.sock"

// ccHookEvent is one Claude Code hook event, distilled: what the hook sends
// to the local SuperAI.
type ccHookEvent struct {
	Event   string `json:"event"`             // Stop | StopFailure | Notification | SessionStart | SessionEnd
	Session string `json:"session"`           // Claude Code's session id
	Cwd     string `json:"cwd,omitempty"`     // where it runs
	Kind    string `json:"kind,omitempty"`    // Notification: permission_prompt | idle_prompt; StopFailure: the error
	Text    string `json:"text,omitempty"`    // Stop: the turn's text; Notification: its message; StopFailure: what was said
	Source  string `json:"source,omitempty"`  // SessionStart: startup | resume | clear | compact; SessionEnd: the reason
	TTY     string `json:"tty,omitempty"`     // the agent's controlling terminal, /dev/ttys003
	Pane    string `json:"pane,omitempty"`    // $TMUX_PANE when it runs in tmux
	Resumed bool   `json:"resumed,omitempty"` // set by the receiver: a parked reply was handed back, the turn goes on
}

// ccNotificationKinds are the Notification hooks worth forwarding: Claude is
// stopped on a question only a person can answer. The rest (auth_success,
// elicitation, push_notification, …) are not news on a phone.
var ccNotificationKinds = map[string]bool{"permission_prompt": true, "idle_prompt": true}

// maxCCText bounds what one event carries. A long turn is cut from the front:
// the end of a turn is where its conclusion is.
const maxCCText = 4000

// ccEventFromHook turns a parsed hook payload into the event to forward, or
// false when this event is not one SuperAI forwards. readTurn reads a
// transcript's text for the turn just finished.
func ccEventFromHook(name string, hev hooks.HookEvent, readTurn func(path string) string) (ccHookEvent, bool) {
	if name == "" {
		name = hev.EventName
	}
	ev := ccHookEvent{Event: name, Session: hev.SessionID, Cwd: hev.Cwd}
	if ev.Session == "" {
		return ccHookEvent{}, false
	}
	str := func(k string) string { s, _ := hev.Raw[k].(string); return strings.TrimSpace(s) }
	switch name {
	case "Stop":
		ev.Text = strings.TrimSpace(hev.LastAssistant)
		if ev.Text == "" && readTurn != nil && hev.TranscriptPath != "" {
			ev.Text = strings.TrimSpace(readTurn(hev.TranscriptPath))
		}
	case "StopFailure":
		ev.Kind = str("error")
		parts := []string{}
		if d := str("error_details"); d != "" {
			parts = append(parts, d)
		}
		if m := str("last_assistant_message"); m != "" {
			parts = append(parts, m)
		}
		ev.Text = strings.Join(parts, "\n\n")
	case "Notification":
		ev.Kind = str("notification_type")
		if !ccNotificationKinds[ev.Kind] {
			return ccHookEvent{}, false
		}
		ev.Text = str("message")
	case "SessionStart":
		ev.Source = str("source")
		// Compaction keeps the session; nothing about the list changed.
		if ev.Source == "compact" {
			return ccHookEvent{}, false
		}
	case "SessionEnd":
		ev.Source = str("reason")
	default:
		return ccHookEvent{}, false
	}
	ev.Text = clipFront(ev.Text, maxCCText)
	return ev, true
}

// clipFront keeps the last max runes of s, marking the cut.
func clipFront(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return "…" + strings.TrimSpace(string(r[len(r)-max+1:]))
}

// HookMain is `superai hook <Event>`. It never fails and never blocks for
// long: an error only means the phone does not hear about this event.
func HookMain(argv []string) {
	defer func() { _ = recover() }() // a hook must not crash the agent's turn
	// A run SuperAI started itself already reports through cli:run.
	if os.Getenv("SUPERAI_CLI_RUN") != "" {
		return
	}
	name := ""
	if len(argv) > 0 {
		name = argv[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()

	hev, err := hooks.ParsePayload(io.LimitReader(os.Stdin, 8<<20))
	if err != nil {
		return
	}
	ev, ok := ccEventFromHook(name, hev, transcriptTurn(ctx))
	if !ok {
		return
	}
	ev.TTY = controllingTTY(ctx)
	ev.Pane = os.Getenv("TMUX_PANE")

	reply := sendCCHook(ctx, ccHookSockets(), ev)
	if reply = strings.TrimSpace(reply); reply != "" && ev.Event == "Stop" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"decision": "block", "reason": reply})
	}
}

// transcriptTurn reads the turn's text, but not past the hook's budget: a
// huge transcript is not worth stalling the terminal over.
func transcriptTurn(ctx context.Context) func(string) string {
	return func(path string) string {
		ch := make(chan string, 1)
		go func() {
			txt, _ := hooks.TurnText(path)
			if strings.TrimSpace(txt) == "" {
				txt, _ = hooks.LastAssistantText(path)
			}
			ch <- txt
		}()
		// Leave a second of the budget for handing the event over.
		wait := 2 * time.Second
		if d, ok := ctx.Deadline(); ok {
			wait = time.Until(d) - time.Second
		}
		if wait <= 0 {
			return ""
		}
		select {
		case t := <-ch:
			return t
		case <-time.After(wait):
			return ""
		}
	}
}

// ccHookSockets lists where a serving SuperAI may be listening, most specific
// first: an explicit socket, this data directory, the node install this
// binary belongs to (~/.superai-node/bin/superai → ~/.superai-node), then the
// two usual homes.
func ccHookSockets() []string {
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
	add(filepath.Join(backend.DataDir(), ccHookSocketName))
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		home := filepath.Dir(filepath.Dir(exe))
		if _, err := os.Stat(filepath.Join(home, "node.json")); err == nil {
			add(filepath.Join(home, ccHookSocketName))
		}
	}
	if h, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(h, ".superai-node", ccHookSocketName))
		add(filepath.Join(h, ".superai", ccHookSocketName))
	}
	return out
}

// sendCCHook hands the event to the first socket that answers and returns
// the reply parked for this session, if any.
func sendCCHook(ctx context.Context, sockets []string, ev ccHookEvent) string {
	body, err := json.Marshal(ev)
	if err != nil {
		return ""
	}
	for _, sock := range sockets {
		if _, err := os.Stat(sock); err != nil {
			continue
		}
		client := ccHookClient(sock)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://superai"+ccHookPath, bytes.NewReader(body))
		if err != nil {
			return ""
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ""
			}
			continue // a stale socket from a process that is gone
		}
		var out struct {
			Reply string `json:"reply"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
		resp.Body.Close()
		return out.Reply
	}
	return ""
}

func ccHookClient(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}

// controllingTTY is the terminal the agent is attached to, /dev/ttys003, or
// "" for none. The hook's own stdio are pipes, so it is the parent's — the
// agent process, which owns the terminal.
func controllingTTY(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(os.Getppid())).Output()
	if err != nil {
		return ""
	}
	return normalizeTTY(string(out))
}

func normalizeTTY(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "??" || name == "-" || name == "?" {
		return ""
	}
	if strings.HasPrefix(name, "/dev/") {
		return name
	}
	return "/dev/" + name
}
