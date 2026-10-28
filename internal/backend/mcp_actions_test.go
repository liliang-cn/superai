package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeTaskIn struct {
	ID string `json:"id"`
}

type fakeTaskOut struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Done bool   `json:"done"`
	Link string `json:"link"`
}

// fakeNoted serves a noted-like MCP server over streamable HTTP behind a
// bearer token: complete_task answers structuredContent, list_holdings only
// JSON text (the noted of today), delete_task is destructive, fail_task
// reports a tool error. calls counts tool calls that reached the server.
func fakeNoted(t *testing.T, token string) (url string, calls *atomic.Int64) {
	t.Helper()
	calls = &atomic.Int64{}
	s := mcp.NewServer(&mcp.Implementation{Name: "noted", Version: "test"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "complete_task"}, func(_ context.Context, _ *mcp.CallToolRequest, in fakeTaskIn) (*mcp.CallToolResult, fakeTaskOut, error) {
		calls.Add(1)
		return nil, fakeTaskOut{Kind: "task", ID: in.ID, Done: true, Link: "noted://task/" + in.ID}, nil
	})
	s.AddTool(&mcp.Tool{Name: "list_holdings", InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"holdings":[{"id":"h1","symbol":"VOO"}]}`}}}, nil
		})
	s.AddTool(&mcp.Tool{Name: "get_running_timer", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "no timer is running"}}}, nil
		})
	yes := true
	mcp.AddTool(s, &mcp.Tool{Name: "delete_task", Annotations: &mcp.ToolAnnotations{DestructiveHint: &yes}}, func(_ context.Context, _ *mcp.CallToolRequest, in fakeTaskIn) (*mcp.CallToolResult, any, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deleted " + in.ID}}}, nil, nil
	})
	s.AddTool(&mcp.Tool{Name: "fail_task", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "no such task"}}}, nil
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
	return srv.URL + "/mcp", calls
}

// writeMCPConfig writes the user mcpServers.json with one http server whose
// token comes from ${NOTED_TOKEN}.
func writeMCPConfig(t *testing.T, name, url string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		name: map[string]any{"type": "http", "url": url, "headers": map[string]any{"Authorization": "Bearer ${NOTED_TOKEN}"}},
	}})
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpConfigPath(), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newTestActions(t *testing.T, allowed ...string) *MCPActions {
	t.Helper()
	m := NewMCPActions(func() []string { return allowed })
	t.Cleanup(m.Close)
	return m
}

func TestMCPActionsCallsThroughWithStructuredContent(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("NOTED_TOKEN", "sekrit")
	url, calls := fakeNoted(t, "sekrit")
	writeMCPConfig(t, "noted", url)
	m := newTestActions(t, "noted")

	res := m.Call(context.Background(), "noted", "complete_task", map[string]any{"id": "t1"}, MCPCallOptions{SessionID: "s1"})
	if !res.OK {
		t.Fatalf("complete_task: %+v", res)
	}
	got, _ := res.Structured.(map[string]any)
	if got["id"] != "t1" || got["done"] != true || got["link"] != "noted://task/t1" {
		t.Errorf("structured = %#v, want the tool's structuredContent", res.Structured)
	}

	// Text-only JSON (noted before structured output) is parsed as a fallback.
	res = m.Call(context.Background(), "noted", "list_holdings", nil, MCPCallOptions{})
	hs, _ := res.Structured.(map[string]any)
	if !res.OK || hs["holdings"] == nil {
		t.Errorf("list_holdings = %+v, want parsed text JSON in structured", res)
	}
	// A sentence stays a sentence.
	res = m.Call(context.Background(), "noted", "get_running_timer", nil, MCPCallOptions{})
	if !res.OK || res.Structured != nil || res.Text != "no timer is running" {
		t.Errorf("get_running_timer = %+v", res)
	}
	// A tool error is a failed call with the tool's own message.
	res = m.Call(context.Background(), "noted", "fail_task", map[string]any{}, MCPCallOptions{})
	if res.OK || res.Code != ActionToolError || res.Error != "no such task" {
		t.Errorf("fail_task = %+v", res)
	}
	if n := calls.Load(); n != 4 {
		t.Errorf("server saw %d calls, want 4", n)
	}

	// Logged: tool and outcome, never the arguments.
	raw, _ := os.ReadFile(AuditLogPath())
	log := string(raw)
	if !strings.Contains(log, `"tool":"mcp_noted_complete_task"`) || !strings.Contains(log, `"decided_by":"ui-action"`) || !strings.Contains(log, `"session_id":"s1"`) {
		t.Errorf("audit log missing the call:\n%s", log)
	}
	if strings.Contains(log, "t1") && strings.Contains(log, `"id"`) {
		t.Errorf("audit log carries arguments:\n%s", log)
	}
}

func TestMCPActionsRefusals(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("NOTED_TOKEN", "sekrit")
	url, calls := fakeNoted(t, "sekrit")
	writeMCPConfig(t, "noted", url)
	m := newTestActions(t, "noted")
	ctx := context.Background()

	cases := []struct {
		name, server, tool string
		args               any
		opts               MCPCallOptions
		code               string
	}{
		{"server not on the list", "other", "complete_task", map[string]any{}, MCPCallOptions{}, ActionNotAllowed},
		{"args not an object", "noted", "complete_task", []any{"t1"}, MCPCallOptions{}, ActionBadArgs},
		{"args a string", "noted", "complete_task", "t1", MCPCallOptions{}, ActionBadArgs},
		{"unknown tool", "noted", "drop_database", map[string]any{}, MCPCallOptions{}, ActionUnknownTool},
		{"destructive unconfirmed", "noted", "delete_task", map[string]any{"id": "t1"}, MCPCallOptions{}, ActionNeedsConfirm},
	}
	for _, c := range cases {
		res := m.Call(ctx, c.server, c.tool, c.args, c.opts)
		if res.OK || res.Code != c.code {
			t.Errorf("%s: got %+v, want code %s", c.name, res, c.code)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("a refused call reached the server (%d calls)", n)
	}
	res := m.Call(ctx, "noted", "delete_task", map[string]any{"id": "t1"}, MCPCallOptions{Confirmed: true})
	if !res.OK || res.Text != "deleted t1" {
		t.Errorf("confirmed delete = %+v", res)
	}
}

func TestMCPActionsNotConfigured(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	m := newTestActions(t, "noted")
	res := m.Call(context.Background(), "noted", "list_holdings", nil, MCPCallOptions{})
	if res.Code != ActionNotConfigured || !strings.Contains(res.Error, "not configured") {
		t.Errorf("no config: %+v", res)
	}

	// Configured but the token variable is not set: still not configured, and
	// nothing is sent with an empty bearer.
	url, calls := fakeNoted(t, "sekrit")
	writeMCPConfig(t, "noted", url)
	t.Setenv("NOTED_TOKEN", "")
	res = m.Call(context.Background(), "noted", "list_holdings", nil, MCPCallOptions{})
	if res.Code != ActionNotConfigured || !strings.Contains(res.Error, "NOTED_TOKEN") {
		t.Errorf("unset token: %+v", res)
	}
	if calls.Load() != 0 {
		t.Error("called noted without a token")
	}
}

// The config handed to agent-go has the secret filled in; the file the queen
// syncs to her workers does not.
func TestResolveMCPConfigExpandsEnv(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	writeMCPConfig(t, "noted", "https://noted.example/mcp")
	src := mcpConfigPath()
	env := map[string]string{"NOTED_TOKEN": "sekrit"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	path, err := resolveMCPConfig(src, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if path == src || !strings.HasPrefix(path, filepath.Join(DataDir(), "run")) {
		t.Fatalf("resolved path = %s", path)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "Bearer sekrit") {
		t.Errorf("resolved copy lacks the token:\n%s", raw)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("resolved copy mode %v, want 0600", st.Mode().Perm())
	}
	orig, _ := os.ReadFile(src)
	if strings.Contains(string(orig), "sekrit") {
		t.Error("source file was rewritten with the secret")
	}
	if ab := CollectAbilities(); strings.Contains(mustJSON(ab.MCP), "sekrit") || !strings.Contains(mustJSON(ab.MCP), "${NOTED_TOKEN}") {
		t.Errorf("queen hands workers %s, want the reference not the value", mustJSON(ab.MCP))
	}

	// Unset: the server is left out of the copy instead of sent "Bearer ".
	delete(env, "NOTED_TOKEN")
	path, err = resolveMCPConfig(src, lookup)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "noted") {
		t.Errorf("server with an unset variable kept:\n%s", raw)
	}

	// A file without references is used as it is.
	plain := filepath.Join(t.TempDir(), "plain.json")
	_ = os.WriteFile(plain, []byte(`{"mcpServers":{"x":{"command":"x"}}}`), 0o600)
	if p, _ := resolveMCPConfig(plain, lookup); p != plain {
		t.Errorf("plain file resolved to %s", p)
	}
}

func TestApplyAbilitiesSkipsServersMissingEnv(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("NOTED_TOKEN", "")
	ab := HiveAbilities{MCP: map[string]any{
		"noted": map[string]any{"type": "http", "url": "https://n/mcp", "headers": map[string]any{"Authorization": "Bearer ${NOTED_TOKEN}"}},
	}}
	out, err := ApplyAbilities(ab, func(string) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Skipped["noted"], "NOTED_TOKEN") || len(out.MCP) != 0 {
		t.Errorf("unset token: %+v", out)
	}
	t.Setenv("NOTED_TOKEN", "w")
	out, _ = ApplyAbilities(ab, func(string) (string, error) { return "", nil })
	if len(out.MCP) != 1 {
		t.Fatalf("set token: %+v", out)
	}
	raw, _ := os.ReadFile(HiveMCPFile())
	if !strings.Contains(string(raw), "${NOTED_TOKEN}") {
		t.Errorf("worker's copy should keep the reference:\n%s", raw)
	}
}

func TestExpandMCPEnv(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == "A" {
			return "1", true
		}
		return "", false
	}
	got, missing := expandMCPEnv("x${A}y${B}$C $", lookup)
	if got != "x1y$C $" || len(missing) != 1 || missing[0] != "B" {
		t.Errorf("got %q missing %v", got, missing)
	}
}

func TestNotedSectionOnlyWhenConfigured(t *testing.T) {
	if notedSection(false) != "" {
		t.Error("noted guidance without noted")
	}
	s := notedSection(true)
	for _, want := range []string{"mcp_noted_", "resolve_datetime", "card", "timeline", "stat", "noted://",
		"still ahead", "already over is left out", "create_alarm", `"alarm"`, `"reminder"`, "interval 2", "anchor", "nextFires", "update_alarm", "enabled false", "delete_alarm", "confirm"} {
		if !strings.Contains(s, want) {
			t.Errorf("noted section lacks %q", want)
		}
	}
}

func TestAWorkerWithoutNotedSaysItCannotSeeTheSchedule(t *testing.T) {
	if notedSectionFor(false, "") != "" || notedSectionFor(false, HiveRoleQueen) != "" {
		t.Error("a SuperLeo of its own, or a queen, without noted is told about noted")
	}
	s := notedSectionFor(false, HiveRoleWorker)
	for _, want := range []string{"not connected on this worker", "never say there is nothing", "the queen"} {
		if !strings.Contains(s, want) {
			t.Errorf("the worker's note lacks %q", want)
		}
	}
	if notedSectionFor(true, HiveRoleWorker) != notedSection(true) {
		t.Error("a worker with noted is not told what anyone with noted is")
	}
}

func TestVitaSectionOnlyWhenConfigured(t *testing.T) {
	if vitaSection(false) != "" {
		t.Error("vita guidance without vita")
	}
	s := vitaSection(true)
	for _, want := range []string{"mcp_vita_", "health_today", "health_days", "health_trend", "not a zero", "not medical advice"} {
		if !strings.Contains(s, want) {
			t.Errorf("vita section lacks %q", want)
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
