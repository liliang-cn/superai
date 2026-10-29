package app

// Talking to SuperLeo from another agent.
//
// Claude Code or Codex on a laptop can reach this instance's /mcp (the queen,
// behind the same Bearer token as everything else). superleo_ask hands a
// message to SuperLeo exactly as the app's own chat box does — SendChat, same
// conversations, same tools, same approval gate — and waits for the answer.
// superleo_conversations and superleo_chat_history read back what was said.
//
// Waiting is bounded. A turn that outlives the caller's timeout keeps running
// here; the caller gets what has streamed so far, pending: true and the ids to
// look again with. A tool call that needs a person's yes is never answered
// from here: the caller is told which approvals are open, the person answers
// on the phone, and the wait goes on.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/superleo/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	askDefaultTimeout = 180 * time.Second
	askMaxTimeout     = 600 * time.Second
	// askNewConversationPrefix starts the id of a conversation an ask without
	// one opens. Each such ask gets its own, so two unrelated questions from
	// a coding agent do not read each other's context.
	askNewConversationPrefix = "mcp-"
)

type askIn struct {
	Message        string `json:"message" jsonschema:"what to say to SuperLeo, as a person would type it in the chat"`
	Conversation   string `json:"conversation,omitempty" jsonschema:"conversation id to continue (from a previous answer or superleo_conversations); omit to start a new one"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"how long to wait for the answer, default 180, at most 600; on timeout the turn keeps running and the answer so far comes back with pending=true"`
}

type askTool struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	// Worker is who ran it, when it was not the queen: a worker the queen
	// handed the work to (Name then reads "worker · tool").
	Worker string `json:"worker,omitempty"`
}

type askApproval struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
}

type askOut struct {
	Conversation string `json:"conversation"`
	RequestID    string `json:"request_id"`
	// Status is done, error, cancelled or pending.
	Status  string `json:"status"`
	Answer  string `json:"answer"`
	Pending bool   `json:"pending,omitempty"`
	Error   string `json:"error,omitempty"`
	// Tools are the tools SuperLeo called for this answer, in order.
	Tools []askTool `json:"tools,omitempty"`
	// NeedsApproval are tool calls waiting for the person to allow or deny
	// them in the SuperLeo app. Nothing here answers them.
	NeedsApproval []askApproval `json:"needs_approval,omitempty"`
}

// askWatch collects one ask's events from the moment before it starts.
type askWatch struct {
	mu        sync.Mutex
	conv      string
	requestID string // set once SendChat returns; events before that wait in buf
	buf       []askEvent
	wake      chan struct{}
}

type askEvent struct {
	name    string
	payload map[string]any
}

func (w *askWatch) add(name string, payload map[string]any) {
	switch {
	case strings.HasPrefix(name, "chat:"), strings.HasPrefix(name, "tool:approval"):
	default:
		return
	}
	w.mu.Lock()
	w.buf = append(w.buf, askEvent{name, payload})
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *askWatch) take() []askEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.buf
	w.buf = nil
	return out
}

// askState folds the events of one turn into an answer.
type askState struct {
	out       askOut
	streamed  strings.Builder
	approvals map[string]askApproval
	order     []string
	running   map[string]int // tool name → index in out.Tools of its latest unresolved call
}

func newAskState(conv, requestID string) *askState {
	return &askState{
		out:       askOut{Conversation: conv, RequestID: requestID, Status: "pending"},
		approvals: map[string]askApproval{},
		running:   map[string]int{},
	}
}

// apply folds one event in and says whether the turn is over, plus a line
// worth reporting as progress ("" for none).
func (s *askState) apply(ev askEvent) (finished bool, progress string) {
	p := ev.payload
	str := func(k string) string { v, _ := p[k].(string); return v }
	switch ev.name {
	case "tool:approval":
		if str("session") != s.out.Conversation {
			return false, ""
		}
		a := askApproval{ID: str("id"), Tool: str("tool"), Command: str("command")}
		if _, seen := s.approvals[a.ID]; !seen {
			s.order = append(s.order, a.ID)
		}
		s.approvals[a.ID] = a
		return false, fmt.Sprintf("waiting for the person to approve %s (approval %s) in the SuperLeo app", a.Tool, a.ID)
	case "tool:approval:closed":
		delete(s.approvals, str("id"))
		return false, ""
	}
	if str("requestId") != s.out.RequestID {
		return false, ""
	}
	switch ev.name {
	case "chat:event":
		switch str("type") {
		case "partial":
			s.streamed.WriteString(str("content"))
		case "tombstone":
			if str("debugType") == "retry" {
				s.streamed.Reset()
			}
		case "tool_call":
			name := str("tool")
			s.running[name] = len(s.out.Tools)
			s.out.Tools = append(s.out.Tools, askTool{Name: name, OK: true, Worker: str("worker")})
			return false, "calling " + name
		case "tool_result":
			if i, ok := s.running[str("tool")]; ok {
				s.out.Tools[i].OK = !toolResultFailed(p["result"])
				delete(s.running, str("tool"))
			}
		}
	case "chat:done":
		s.out.Status, s.out.Answer = "done", str("final")
		return true, ""
	case "chat:cancelled":
		s.out.Status, s.out.Answer = "cancelled", str("final")
		return true, ""
	case "chat:error":
		s.out.Status, s.out.Error = "error", str("error")
		s.out.Answer = s.streamed.String()
		return true, ""
	}
	return false, ""
}

// result is the answer as it stands: final when finished, what has streamed
// and pending otherwise.
func (s *askState) result() askOut {
	out := s.out
	if out.Status == "pending" {
		out.Pending = true
		out.Answer, _ = backend.SplitEmotion(s.streamed.String())
	}
	for _, id := range s.order {
		if a, open := s.approvals[id]; open {
			out.NeedsApproval = append(out.NeedsApproval, a)
		}
	}
	return out
}

// toolResultFailed reads a tool result the way the chat panel does.
func toolResultFailed(r any) bool { return backend.ToolResultFailed(r) }

// ask runs one superleo_ask. progress, when not nil, is told what the turn is
// doing while it is waited on.
func (a *App) ask(ctx context.Context, in askIn, progress func(string)) (askOut, error) {
	msg := strings.TrimSpace(in.Message)
	if msg == "" {
		return askOut{}, fmt.Errorf("message is empty")
	}
	timeout := askDefaultTimeout
	if in.TimeoutSeconds > 0 {
		timeout = min(time.Duration(in.TimeoutSeconds)*time.Second, askMaxTimeout)
	}
	conv := strings.TrimSpace(in.Conversation)
	if conv == "" {
		conv = askNewConversationPrefix + uuid.NewString()
	}

	// Listening starts before the turn does: a quick answer must not finish
	// between SendChat returning and the tap going in.
	w := &askWatch{conv: conv, wake: make(chan struct{}, 1)}
	tapID := uuid.NewString()
	a.taps.Store(tapID, func(name string, p map[string]any) { w.add(name, p) })
	defer a.taps.Delete(tapID)

	send := a.askFn
	if send == nil {
		// SuperLeo's own agent, not the queen engine: whoever asks over MCP is
		// as likely as not that engine's CLI, and would be answering itself.
		send = func(session, message string) string { return a.sendChat(session, message, nil, true) }
	}
	requestID := send(conv, msg)
	st := newAskState(conv, requestID)

	// Approvals already open in this conversation (an earlier ask's) count.
	for _, p := range a.PendingToolApprovals() {
		st.apply(askEvent{"tool:approval", p})
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		for _, ev := range w.take() {
			done, line := st.apply(ev)
			if line != "" && progress != nil {
				progress(line)
			}
			if done {
				return st.result(), nil
			}
		}
		select {
		case <-w.wake:
		case <-deadline.C:
			return st.result(), nil
		case <-ctx.Done():
			// The caller went away; the turn goes on regardless.
			return st.result(), nil
		}
	}
}

type historyIn struct {
	Conversation string `json:"conversation" jsonschema:"conversation id (from superleo_conversations or a superleo_ask answer)"`
	Limit        int    `json:"limit,omitempty" jsonschema:"most recent messages to return, default 20, at most 200"`
}

type historyMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
	Time string `json:"time,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// historyForMCP is the last limit visible messages, the memory SuperLeo was
// handed before answering left out.
func historyForMCP(turns []backend.ChatTurn, limit int) []historyMessage {
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	out := make([]historyMessage, 0, len(turns))
	for _, t := range turns {
		if t.Kind == backend.TurnKindContext {
			continue
		}
		out = append(out, historyMessage{Role: t.Role, Text: t.Content, Time: t.At, Kind: t.Kind})
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

type conversationsIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many, most recent first; default 20, at most 200"`
}

func conversationsForMCP(sessions []backend.ChatSessionInfo, limit int) []backend.ChatSessionInfo {
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions
}

// addAskMCPTools puts superleo_ask, superleo_conversations and
// superleo_chat_history on the MCP server.
func addAskMCPTools(s *mcp.Server, app *App) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "superleo_ask",
		Title: "Ask SuperLeo",
		Description: "Say something to SuperLeo (the queen) and wait for its answer, exactly as if typed in the SuperLeo app: " +
			"same conversations, tools, memory and approval rules. Returns the answer, the tools it called (name, ok) and the " +
			"conversation id — pass that back as conversation to continue. Without a conversation a new one is started. " +
			"If the answer takes longer than timeout_seconds the turn keeps running: you get the text so far with pending=true; " +
			"read the rest later with superleo_chat_history. Tool calls that need the person's approval are listed in " +
			"needs_approval; they are approved on the phone, never from here.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in askIn) (*mcp.CallToolResult, any, error) {
		var progress func(string)
		if req != nil && req.Params != nil && req.Session != nil {
			if token := req.Params.GetProgressToken(); token != nil {
				n := 0
				progress = func(line string) {
					n++
					_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
						ProgressToken: token, Progress: float64(n), Message: line,
					})
				}
			}
		}
		// The caller's flow (agent-go puts it in params._meta) is continued
		// by the turn, and the span it will be goes back in the result's
		// _meta, so the caller's trace reaches into this one (flow_link.go).
		if strings.TrimSpace(in.Conversation) == "" {
			in.Conversation = askNewConversationPrefix + uuid.NewString()
		}
		answered := acceptMCPFlow(req, in.Conversation)
		out, err := app.ask(ctx, in, progress)
		if err != nil {
			return toolErr(err.Error())
		}
		if answered == "" {
			return toolOK(out)
		}
		return &mcp.CallToolResult{Meta: mcp.Meta{"traceparent": answered}}, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "superleo_conversations",
		Title:       "List conversations",
		Description: "Recent SuperLeo conversations, most recent first: id, title, number of messages, last update.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in conversationsIn) (*mcp.CallToolResult, any, error) {
		return toolOK(map[string]any{"conversations": conversationsForMCP(app.ChatSessions(), in.Limit)})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "superleo_chat_history",
		Title:       "Read a conversation",
		Description: "The most recent messages of one SuperLeo conversation: role, text, time.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in historyIn) (*mcp.CallToolResult, any, error) {
		conv := strings.TrimSpace(in.Conversation)
		if conv == "" {
			return toolErr("conversation is required; superleo_conversations lists them")
		}
		return toolOK(map[string]any{"conversation": conv, "messages": historyForMCP(app.ChatHistory(conv), in.Limit)})
	})
}
