package app

// Buttons in chat blocks, and the home screen's noted tiles.
//
// A card / timeline block can carry an action that names one MCP tool call
// ({"call":{"server":"noted","tool":"complete_task","args":{...}}}). The
// client runs it through CallTool — no model turn — and the transcript gains a
// one-line "已完成：…" for it. NotedToday, NotedTimeWeek and NotedHoldings
// are the fixed reads behind the home tiles; they hand back noted's own
// structured result untouched.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// ActionOptions is CallTool's fourth argument.
type ActionOptions struct {
	// Confirmed: the person answered the action's confirm question with yes.
	// Required for destructive tools (delete_*).
	Confirmed bool `json:"confirmed"`
	// SessionID is the conversation the button sits in. With it the outcome is
	// announced to that conversation (chat:action) and kept in its history.
	SessionID string `json:"sessionId"`
	// Label is the button's text, for the transcript line.
	Label string `json:"label"`
}

// mcpActions is the direct caller, built on first use and kept across
// rebuilds: its sessions belong to the servers, not to the agent.
func (a *App) mcpActions() *backend.MCPActions {
	a.actionsOnce.Do(func() {
		a.actionsV = backend.NewMCPActions(func() []string {
			a.mu.Lock()
			s := a.settings
			a.mu.Unlock()
			return s.ActionServers()
		})
	})
	return a.actionsV
}

// CallTool runs one tool on an action-capable MCP server directly, for a
// button in a chat block. args must be a JSON object (null is {}).
//
//	POST /api/rpc/CallTool  ["noted","complete_task",{"id":"t_1"},{"sessionId":"…","label":"完成","confirmed":false}]
//	→ {"ok":true,"text":"…","structured":{…}}
//	→ {"ok":false,"error":"…","code":"needs_confirm"|"not_allowed"|"not_configured"|"bad_args"|"unknown_tool"|"unavailable"|"tool_error", …}
func (a *App) CallTool(server, tool string, args any, opts ActionOptions) backend.MCPCallResult {
	read := isReadTool(tool)
	res := a.mcpActions().Call(context.Background(), server, tool, args, backend.MCPCallOptions{
		Confirmed: opts.Confirmed,
		SessionID: opts.SessionID,
		// A screen loading its list is not something anyone decided; only
		// what changes something belongs in the approvals log.
		Quiet: read,
	})
	if read {
		return res
	}
	label := strings.TrimSpace(opts.Label)
	if label == "" {
		label = tool
	}
	ev := map[string]any{
		"sessionId": opts.SessionID,
		"server":    server,
		"tool":      tool,
		"label":     label,
		"ok":        res.OK,
	}
	if !res.OK {
		ev["error"] = res.Error
		ev["code"] = res.Code
	}
	a.emit("chat:action", ev)
	if res.OK && server == backend.NotedServer && strings.Contains(tool, "alarm") {
		a.alarmsMayHaveChanged()
	}
	if res.OK && strings.TrimSpace(opts.SessionID) != "" {
		recordChatAction(opts.SessionID, chatAction{At: time.Now().UTC(), Server: server, Tool: tool, Label: label})
	}
	return res
}

// isReadTool says whether an MCP tool only reads, by noted's naming:
// list_*, get_*, search_*, and the summaries.
func isReadTool(tool string) bool {
	for _, p := range []string{"list_", "get_", "search_"} {
		if strings.HasPrefix(tool, p) {
			return true
		}
	}
	return tool == "time_summary" || tool == "weekly_review"
}

// NotedToday is noted's overview for today — events, tasks, the running
// timer — as noted structures it (get_overview, horizon today).
func (a *App) NotedToday() (any, error) {
	return a.notedRead("get_overview", map[string]any{"horizon": "today"})
}

// NotedTimeWeek is this week's logged time (Monday 00:00 local until now) by
// activity (time_summary, group_by title).
func (a *App) NotedTimeWeek() (any, error) {
	return a.notedRead("time_summary", map[string]any{
		"from":     weekStart(time.Now()).Format(time.RFC3339),
		"group_by": "title",
	})
}

// NotedTimeEntries is this week's time entries one by one, newest first
// (list_time_entries), so the app can show each under its day.
func (a *App) NotedTimeEntries() (any, error) {
	return a.notedRead("list_time_entries", map[string]any{
		"from":  weekStart(time.Now()).Format(time.RFC3339),
		"limit": 200,
	})
}

// NotedHoldings is every investment with its position (list_holdings).
func (a *App) NotedHoldings() (any, error) {
	return a.notedRead("list_holdings", map[string]any{})
}

// notedRead runs one of the fixed reads. An unconfigured noted answers 503,
// a failing one 502, each with the reason as the body.
func (a *App) notedRead(tool string, args map[string]any) (any, error) {
	res := a.mcpActions().Call(context.Background(), backend.NotedServer, tool, args, backend.MCPCallOptions{
		Unlisted: true,
		Quiet:    true,
	})
	switch {
	case res.Code == backend.ActionNotConfigured:
		return nil, &callError{http.StatusServiceUnavailable, res.Error}
	case !res.OK:
		return nil, &callError{http.StatusBadGateway, res.Error}
	case res.Structured != nil:
		return res.Structured, nil
	}
	// A plain sentence ("no timer is running") rather than data.
	return map[string]any{"text": res.Text}, nil
}

// weekStart is Monday 00:00 of t's week, in t's zone.
func weekStart(t time.Time) time.Time {
	d := (int(t.Weekday()) + 6) % 7 // Monday = 0
	y, m, day := t.Date()
	return time.Date(y, m, day-d, 0, 0, 0, 0, t.Location())
}

// chatAction is one button press that did something, kept beside the
// conversation the way turn times are: agent-go owns the transcript and has
// nowhere to put a line nobody said.
type chatAction struct {
	At     time.Time `json:"at"`
	Server string    `json:"server"`
	Tool   string    `json:"tool"`
	Label  string    `json:"label"`
}

// TurnKindAction marks a transcript line for a button that ran a tool.
const TurnKindAction = "action"

var chatActionsMu sync.Mutex

func chatActionsPath(sessionID string) string {
	// Named like the turn-times file, in a directory beside it.
	return filepath.Join(backend.DataDir(), "chat-actions", filepath.Base(turnTimesPath(sessionID)))
}

func recordChatAction(sessionID string, act chatAction) {
	line, err := json.Marshal(act)
	if err != nil {
		return
	}
	chatActionsMu.Lock()
	defer chatActionsMu.Unlock()
	p := chatActionsPath(sessionID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

func readChatActions(sessionID string) []chatAction {
	chatActionsMu.Lock()
	defer chatActionsMu.Unlock()
	f, err := os.Open(chatActionsPath(sessionID))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []chatAction
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c chatAction
		if json.Unmarshal(sc.Bytes(), &c) == nil {
			out = append(out, c)
		}
	}
	return out
}

// withChatActions lays the button presses into the transcript, each after the
// last turn that happened before it (turns without a time count as earlier).
func withChatActions(sessionID string, turns []backend.ChatTurn) []backend.ChatTurn {
	acts := readChatActions(sessionID)
	if len(acts) == 0 {
		return turns
	}
	sort.SliceStable(acts, func(i, j int) bool { return acts[i].At.Before(acts[j].At) })
	out := make([]backend.ChatTurn, 0, len(turns)+len(acts))
	k := 0
	line := func(c chatAction) backend.ChatTurn {
		return backend.ChatTurn{Role: "assistant", Kind: TurnKindAction, Content: "已完成：" + c.Label, At: c.At.Format(time.RFC3339)}
	}
	for _, t := range turns {
		if at, err := time.Parse(time.RFC3339, t.At); err == nil {
			for k < len(acts) && acts[k].At.Before(at) {
				out = append(out, line(acts[k]))
				k++
			}
		}
		out = append(out, t)
	}
	for ; k < len(acts); k++ {
		out = append(out, line(acts[k]))
	}
	return out
}
