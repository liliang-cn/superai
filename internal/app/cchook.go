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
// PermissionRequest is the one exception to the budget (cchook_perm.go): when
// nobody is at the Mac it waits, up to its own bound, for an allow or deny
// from the phone, and prints that decision. It prints nothing — leaving the
// terminal's own dialog — on a timeout, an error, or a person at the desk.
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
	Event   string `json:"event"`             // Stop | StopFailure | Notification | SessionStart | SessionEnd | PermissionRequest | PermissionClosed
	Session string `json:"session"`           // Claude Code's session id
	Cwd     string `json:"cwd,omitempty"`     // where it runs
	Kind    string `json:"kind,omitempty"`    // Notification: permission_prompt | idle_prompt; StopFailure: the error
	Text    string `json:"text,omitempty"`    // Stop: the turn's text; Notification: its message; StopFailure: what was said; PermissionRequest: what the call does, in one line
	Source  string `json:"source,omitempty"`  // SessionStart: startup | resume | clear | compact; SessionEnd: the reason
	TTY     string `json:"tty,omitempty"`     // the agent's controlling terminal, /dev/ttys003
	Pane    string `json:"pane,omitempty"`    // $TMUX_PANE when it runs in tmux
	Resumed bool   `json:"resumed,omitempty"` // set by the receiver: a parked reply was handed back, the turn goes on

	// PermissionRequest only.
	Tool    string `json:"tool,omitempty"`    // the tool Claude wants to use
	Input   string `json:"input,omitempty"`   // its arguments, as JSON text
	Wait    int    `json:"wait,omitempty"`    // seconds the hook will wait for an answer
	Request string `json:"request,omitempty"` // set by the receiver: names this one request on its way to core and back
	Expires string `json:"expires,omitempty"` // set by the receiver: RFC 3339, when the hook stops waiting
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
	case "PermissionRequest":
		ev.Tool = strings.TrimSpace(hev.ToolName)
		if ev.Tool == "" {
			return ccHookEvent{}, false
		}
		if len(hev.ToolInput) > 0 {
			if raw, err := json.Marshal(hev.ToolInput); err == nil && len(raw) <= maxCCInput {
				ev.Input = string(raw)
			}
		}
		ev.Text = ccToolSummary(ev.Tool, hev.ToolInput)
	default:
		return ccHookEvent{}, false
	}
	ev.Text = clipFront(ev.Text, maxCCText)
	return ev, true
}

// maxCCInput bounds a permission request's arguments. A bigger one (a Write
// of a whole file) goes without them: the summary still says what it is.
const maxCCInput = 32 << 10

// ccToolSummary says in one line what a tool call does: the command, the
// file, the URL, else its arguments.
func ccToolSummary(tool string, input map[string]any) string {
	pick := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	s := pick("command", "file_path", "notebook_path", "url", "pattern", "query", "path", "prompt", "description")
	if s == "" && len(input) > 0 {
		if raw, err := json.Marshal(input); err == nil {
			s = string(raw)
		}
	}
	if s == "" {
		return tool
	}
	r := []rune(s)
	if len(r) > 300 {
		s = string(r[:299]) + "…"
	}
	return s
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
	hev, err := hooks.ParsePayload(io.LimitReader(os.Stdin, 8<<20))
	if err != nil {
		return
	}
	if name == "PermissionRequest" || (name == "" && hev.EventName == "PermissionRequest") {
		permissionHook(argv, hev)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()

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
	var out struct {
		Reply string `json:"reply"`
	}
	postCCHook(ctx, sockets, ccHookPath, ev, &out)
	return out.Reply
}

// postCCHook posts the event to path on the first socket that answers and
// decodes its answer into out. False when none did.
func postCCHook(ctx context.Context, sockets []string, path string, ev ccHookEvent, out any) bool {
	body, err := json.Marshal(ev)
	if err != nil {
		return false
	}
	for _, sock := range sockets {
		if _, err := os.Stat(sock); err != nil {
			continue
		}
		client := ccHookClient(sock)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://superai"+path, bytes.NewReader(body))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			continue // a stale socket from a process that is gone
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out) == nil
	}
	return false
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
