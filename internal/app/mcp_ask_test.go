package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubQueen stands in for SendChat: it records what it was asked and plays
// script back as the events a real turn would emit.
type stubQueen struct {
	mu    sync.Mutex
	asked []string // session ids
}

func (q *stubQueen) install(a *App, script func(emit func(string, map[string]any), requestID, session string)) {
	n := 0
	a.askFn = func(session, message string) string {
		q.mu.Lock()
		q.asked = append(q.asked, session)
		n++
		id := "req-" + string(rune('0'+n))
		q.mu.Unlock()
		go script(a.emit, id, session)
		return id
	}
}

func askResult(t *testing.T, res *mcp.CallToolResult) askOut {
	t.Helper()
	if res.IsError {
		t.Fatalf("superleo_ask failed: %v", res.Content)
	}
	var out askOut
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func TestSuperleoAskReturnsTheAnswer(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	app := NewApp()
	q := &stubQueen{}
	q.install(app, func(emit func(string, map[string]any), id, session string) {
		ev := func(typ, tool string, extra map[string]any) {
			p := map[string]any{"requestId": id, "type": typ, "tool": tool}
			for k, v := range extra {
				p[k] = v
			}
			emit("chat:event", p)
		}
		// Another conversation's turn going on at the same time.
		emit("chat:event", map[string]any{"requestId": "someone-else", "type": "partial", "content": "噪音"})
		ev("tool_call", "mcp_noted_get_overview", nil)
		ev("tool_result", "mcp_noted_get_overview", map[string]any{"result": map[string]any{"items": []any{}}})
		ev("tool_call", "web_search", nil)
		ev("tool_result", "web_search", map[string]any{"result": map[string]any{"error": "quota"}})
		ev("partial", "", map[string]any{"content": "今天有两件事"})
		emit("chat:done", map[string]any{"requestId": id, "final": "今天有两件事：站会和交报告。"})
	})
	ss := selfSession(t, app)

	res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superleo_ask", Arguments: map[string]any{"message": "今天有什么安排"}})
	if err != nil {
		t.Fatal(err)
	}
	out := askResult(t, res)
	if out.Status != "done" || out.Pending || out.Answer != "今天有两件事：站会和交报告。" {
		t.Errorf("answer = %+v", out)
	}
	if !strings.HasPrefix(out.Conversation, askNewConversationPrefix) || out.RequestID != "req-1" {
		t.Errorf("ids = %q %q", out.Conversation, out.RequestID)
	}
	want := []askTool{{Name: "mcp_noted_get_overview", OK: true}, {Name: "web_search", OK: false}}
	if len(out.Tools) != 2 || out.Tools[0] != want[0] || out.Tools[1] != want[1] {
		t.Errorf("tools = %+v, want %+v", out.Tools, want)
	}

	// Continuing passes the same conversation to the queen.
	res, err = ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superleo_ask", Arguments: map[string]any{"message": "第二件几点？", "conversation": out.Conversation}})
	if err != nil {
		t.Fatal(err)
	}
	if again := askResult(t, res); again.Conversation != out.Conversation || q.asked[1] != out.Conversation {
		t.Errorf("continued in %q, queen asked in %q, want %q", again.Conversation, q.asked[1], out.Conversation)
	}
}

// A turn still running when the ask stops waiting answers with what has
// streamed — which must not be the notes the model wrote beside its tool calls
// (session mcp-802c0ad3…: "Need to find the mac worker. Call hive_members?…").
func TestAPendingAskLeavesOutTheTextBesideToolCalls(t *testing.T) {
	st := newAskState("c", "r")
	for _, ev := range []map[string]any{
		{"type": "partial", "content": "Need to find the mac worker. "},
		{"type": "partial", "content": "Call hive_members?"},
		{"type": "tool_call", "tool": "hive_members"},
		{"type": "tool_result", "tool": "hive_members", "result": map[string]any{"members": []any{"mac"}}},
		{"type": "partial", "content": "Use hive_command. Try it anyway."},
		{"type": "tool_call", "tool": "hive_command"},
	} {
		ev["requestId"] = "r"
		st.apply(askEvent{"chat:event", ev})
	}
	if out := st.result(); !out.Pending || out.Answer != "" {
		t.Fatalf("pending answer while tools run = %q", out.Answer)
	}

	st.apply(askEvent{"chat:event", map[string]any{"requestId": "r", "type": "tool_result", "tool": "hive_command", "result": "ok"}})
	st.apply(askEvent{"chat:event", map[string]any{"requestId": "r", "type": "partial", "content": "已经派给 mac 了。"}})
	if out := st.result(); out.Answer != "已经派给 mac 了。" {
		t.Fatalf("pending answer after the last tool = %q", out.Answer)
	}
}

func TestSuperleoAskTimesOutPendingAndReportsApprovals(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	app := NewApp()
	(&stubQueen{}).install(app, func(emit func(string, map[string]any), id, session string) {
		emit("chat:event", map[string]any{"requestId": id, "type": "partial", "content": "我先查一下"})
		emit("chat:event", map[string]any{"requestId": id, "type": "tool_call", "tool": "shell"})
		emit("tool:approval", map[string]any{"id": "ap-1", "tool": "shell", "command": "rm -rf build", "session": session})
		// An approval in some other conversation is not this caller's.
		emit("tool:approval", map[string]any{"id": "ap-2", "tool": "shell", "session": "elsewhere"})
		// …and the turn never finishes within the caller's timeout.
	})

	srv := httptest.NewServer(newMCPHandler(app, "test"))
	t.Cleanup(srv.Close)
	var progMu sync.Mutex
	var progress []string
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			progMu.Lock()
			progress = append(progress, req.Params.Message)
			progMu.Unlock()
		},
	})
	ss, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })

	params := &mcp.CallToolParams{Name: "superleo_ask", Arguments: map[string]any{"message": "清理一下构建目录", "timeout_seconds": 1}}
	params.SetProgressToken("tok-1")
	start := time.Now()
	res, err := ss.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	out := askResult(t, res)
	if el := time.Since(start); el < time.Second || el > 5*time.Second {
		t.Errorf("waited %v, want about the 1s timeout", el)
	}
	// "我先查一下" came before the tool call: the working, not the answer.
	if !out.Pending || out.Status != "pending" || out.Answer != "" || out.RequestID == "" {
		t.Errorf("pending answer = %+v", out)
	}
	if len(out.NeedsApproval) != 1 || out.NeedsApproval[0].ID != "ap-1" || out.NeedsApproval[0].Tool != "shell" {
		t.Errorf("needs_approval = %+v", out.NeedsApproval)
	}
	// Nothing was answered on the person's behalf.
	if got := app.ResolveToolApproval("ap-1", true); got == "ok" {
		t.Error("an approval was left resolvable as if from here")
	}

	time.Sleep(100 * time.Millisecond) // notifications are delivered asynchronously
	progMu.Lock()
	defer progMu.Unlock()
	joined := strings.Join(progress, "\n")
	if !strings.Contains(joined, "calling shell") || !strings.Contains(joined, "ap-1") {
		t.Errorf("progress = %q", joined)
	}
}

func TestSuperleoAskReportsErrors(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	app := NewApp()
	(&stubQueen{}).install(app, func(emit func(string, map[string]any), id, _ string) {
		emit("chat:event", map[string]any{"requestId": id, "type": "partial", "content": "写到一半"})
		emit("chat:error", map[string]any{"requestId": id, "error": "model unavailable"})
	})
	ss := selfSession(t, app)
	res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superleo_ask", Arguments: map[string]any{"message": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	out := askResult(t, res)
	if out.Status != "error" || out.Error != "model unavailable" || out.Answer != "写到一半" || out.Pending {
		t.Errorf("error answer = %+v", out)
	}
	if text, isErr := callSelf(t, ss, "superleo_ask", map[string]any{"message": "  "}); !isErr || !strings.Contains(text, "empty") {
		t.Errorf("blank message: %q %v", text, isErr)
	}
}

func TestSuperleoAskWithNoBackendSaysSo(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	ss := selfSession(t, NewApp()) // the real SendChat, nothing built
	res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superleo_ask", Arguments: map[string]any{"message": "hi", "timeout_seconds": 5}})
	if err != nil {
		t.Fatal(err)
	}
	if out := askResult(t, res); out.Status != "error" || !strings.Contains(out.Error, "not ready") {
		t.Errorf("unbuilt backend: %+v", out)
	}
}

func TestHistoryAndConversationsShaping(t *testing.T) {
	turns := []backend.ChatTurn{
		{Role: "user", Content: "q1", At: "2026-10-13T08:00:00Z"},
		{Role: "user", Content: "memory", Kind: backend.TurnKindContext},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "已完成：完成", Kind: TurnKindAction},
	}
	got := historyForMCP(turns, 2)
	if len(got) != 2 || got[0].Text != "q2" || got[1].Kind != TurnKindAction {
		t.Errorf("last two = %+v", got)
	}
	all := historyForMCP(turns, 0)
	if len(all) != 4 || all[0].Time != "2026-10-13T08:00:00Z" {
		t.Errorf("default = %+v (memory context must be left out)", all)
	}
	sessions := make([]backend.ChatSessionInfo, 30)
	if n := len(conversationsForMCP(sessions, 0)); n != 20 {
		t.Errorf("default limit gave %d", n)
	}
	if n := len(conversationsForMCP(sessions, 500)); n != 30 {
		t.Errorf("limit 500 gave %d", n)
	}

	t.Setenv("SUPERLEO_HOME", t.TempDir())
	ss := selfSession(t, NewApp())
	if text, isErr := callSelf(t, ss, "superleo_chat_history", map[string]any{}); !isErr || !strings.Contains(text, "required") {
		t.Errorf("history without conversation: %q", text)
	}
	if text, isErr := callSelf(t, ss, "superleo_conversations", map[string]any{"limit": 5}); isErr || !strings.Contains(text, "conversations") {
		t.Errorf("conversations: %q %v", text, isErr)
	}
}
