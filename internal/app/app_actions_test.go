package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeNotedServer is a noted-like MCP server behind a bearer token. Its
// time_summary echoes the arguments it got so the test can see the range.
func fakeNotedServer(t *testing.T, token string) string {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "noted", Version: "test"}, nil)
	type idIn struct {
		ID string `json:"id"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "complete_task"}, func(_ context.Context, _ *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"kind": "task", "id": in.ID, "done": true}, nil
	})
	type overviewIn struct {
		Horizon string `json:"horizon"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "get_overview"}, func(_ context.Context, _ *mcp.CallToolRequest, in overviewIn) (*mcp.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"horizon": in.Horizon, "events": []any{map[string]any{"kind": "event", "id": "e1"}}, "runningTimer": map[string]any{"title": "写代码"}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "time_summary", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Text JSON only: the fallback path.
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"args":` + string(req.Params.Arguments) + `,"rows":[{"label":"写代码","minutes":90}]}`}}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "list_time_entries", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"args":` + string(req.Params.Arguments) + `,"items":[{"id":"e1","title":"游泳","minutes":30,"day":"2026-10-14","timed":true}]}`}}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "list_holdings", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"holdings":[{"id":"h1","symbol":"VOO"}]}`}}}, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp"
}

func configureNoted(t *testing.T, url string) {
	t.Helper()
	body := `{"mcpServers":{"noted":{"type":"http","url":"` + url + `","headers":{"Authorization":"Bearer ${NOTED_TOKEN}"}}}}`
	if err := os.MkdirAll(backend.DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend.DataDir(), "mcpServers.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rpc(t *testing.T, srv *httptest.Server, method, args string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/rpc/"+method, "application/json", strings.NewReader(args))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestCallToolRPC(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("NOTED_TOKEN", "sekrit")
	configureNoted(t, fakeNotedServer(t, "sekrit"))
	app, _, srv := serveTestApp(t)
	t.Cleanup(func() { app.actionsV.Close() })

	// Listen for chat:action like a phone would.
	resp, err := http.Get(srv.URL + "/api/events?only=chat:action")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := bufio.NewReader(resp.Body)
	if _, err := events.ReadString('\n'); err != nil { // ": connected"
		t.Fatal(err)
	}

	code, body := rpc(t, srv, "CallTool", `["noted","complete_task",{"id":"t9"},{"sessionId":"sess-1","label":"完成"}]`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var res backend.MCPCallResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	st, _ := res.Structured.(map[string]any)
	if !res.OK || st["id"] != "t9" || st["done"] != true {
		t.Fatalf("CallTool = %s", body)
	}

	deadline := time.Now().Add(3 * time.Second)
	var ev map[string]any
	for time.Now().Before(deadline) {
		line, err := events.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev)
			break
		}
	}
	p, _ := ev["payload"].(map[string]any)
	if ev["name"] != "chat:action" || p["sessionId"] != "sess-1" || p["ok"] != true || p["label"] != "完成" || p["tool"] != "complete_task" {
		t.Errorf("event = %v", ev)
	}
	if _, leaked := p["args"]; leaked {
		t.Error("event carries the arguments")
	}

	// The transcript keeps a line for it.
	turns := withChatActions("sess-1", []backend.ChatTurn{{Role: "user", Content: "今天有什么", At: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, {Role: "assistant", Content: "…"}})
	last := turns[len(turns)-1]
	if len(turns) != 3 || last.Kind != TurnKindAction || last.Content != "已完成：完成" {
		t.Errorf("history = %+v", turns)
	}

	// Refusals are answers, not HTTP errors.
	code, body = rpc(t, srv, "CallTool", `["noted","complete_task","t9",{}]`)
	if code != http.StatusOK || !strings.Contains(string(body), `"code":"bad_args"`) {
		t.Errorf("non-object args: %d %s", code, body)
	}
	code, body = rpc(t, srv, "CallTool", `["cortexdb","anything",{},{}]`)
	if code != http.StatusOK || !strings.Contains(string(body), `"code":"not_allowed"`) {
		t.Errorf("unlisted server: %d %s", code, body)
	}
}

func TestNotedReadRPCs(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	app, _, srv := serveTestApp(t)
	t.Cleanup(func() { app.actionsV.Close() })

	// Not configured: 503 with the reason.
	code, body := rpc(t, srv, "NotedToday", `[]`)
	if code != http.StatusServiceUnavailable || !strings.Contains(string(body), "not configured") {
		t.Fatalf("unconfigured NotedToday: %d %s", code, body)
	}

	t.Setenv("NOTED_TOKEN", "sekrit")
	configureNoted(t, fakeNotedServer(t, "sekrit"))

	code, body = rpc(t, srv, "NotedToday", `[]`)
	var today map[string]any
	_ = json.Unmarshal(body, &today)
	if code != http.StatusOK || today["horizon"] != "today" || today["runningTimer"] == nil {
		t.Errorf("NotedToday: %d %s", code, body)
	}

	code, body = rpc(t, srv, "NotedTimeWeek", `[]`)
	var week struct {
		Args map[string]any `json:"args"`
		Rows []any          `json:"rows"`
	}
	_ = json.Unmarshal(body, &week)
	from, err := time.Parse(time.RFC3339, week.Args["from"].(string))
	if code != http.StatusOK || err != nil || from.Weekday() != time.Monday || from.Hour() != 0 || week.Args["group_by"] != "title" || len(week.Rows) != 1 {
		t.Errorf("NotedTimeWeek: %d %s", code, body)
	}
	if time.Since(from) > 7*24*time.Hour || from.After(time.Now()) {
		t.Errorf("week starts %v", from)
	}

	code, body = rpc(t, srv, "NotedTimeEntries", `[]`)
	var entries struct {
		Args  map[string]any   `json:"args"`
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(body, &entries)
	efrom, err := time.Parse(time.RFC3339, fmt.Sprint(entries.Args["from"]))
	if code != http.StatusOK || err != nil || !efrom.Equal(from) || entries.Args["limit"] != float64(200) || len(entries.Items) != 1 || entries.Items[0]["timed"] != true {
		t.Errorf("NotedTimeEntries: %d %s", code, body)
	}

	code, body = rpc(t, srv, "NotedHoldings", `[]`)
	if code != http.StatusOK || !strings.Contains(string(body), `"symbol":"VOO"`) {
		t.Errorf("NotedHoldings: %d %s", code, body)
	}

	// The reads are not button presses: nothing in the approvals log.
	if raw, _ := os.ReadFile(backend.AuditLogPath()); len(raw) > 0 {
		t.Errorf("home tiles were audited:\n%s", raw)
	}

	// Wrong token on the server side: a 502, not a 503.
	t.Setenv("NOTED_TOKEN", "wrong")
	app.actionsV.Close()
	code, _ = rpc(t, srv, "NotedHoldings", `[]`)
	if code != http.StatusBadGateway {
		t.Errorf("rejected token: status %d, want 502", code)
	}
}

func TestWeekStart(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	for _, c := range []struct{ in, want string }{
		{"2026-10-13T15:04:00+08:00", "2026-10-12T00:00:00+08:00"}, // Tuesday
		{"2026-10-12T00:00:00+08:00", "2026-10-12T00:00:00+08:00"}, // Monday
		{"2026-10-18T23:59:00+08:00", "2026-10-12T00:00:00+08:00"}, // Sunday
	} {
		in, _ := time.ParseInLocation(time.RFC3339, c.in, loc)
		if got := weekStart(in).Format(time.RFC3339); got != c.want {
			t.Errorf("weekStart(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestIsReadTool(t *testing.T) {
	for tool, want := range map[string]bool{
		"list_notes": true, "get_note": true, "search_notes": true, "time_summary": true,
		"create_note": false, "complete_task": false, "delete_task": false, "update_alarm": false,
	} {
		if got := isReadTool(tool); got != want {
			t.Errorf("isReadTool(%q) = %v, want %v", tool, got, want)
		}
	}
}
