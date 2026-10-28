package backend

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	agentmcp "github.com/liliang-cn/agent-go/v3/pkg/mcp"
)

// TestNotedLive runs against a real noted MCP endpoint — a local `noted serve`
// with a throwaway user, never the production one:
//
//	SUPERLEO_NOTED_LIVE_URL=http://127.0.0.1:<mcp port>/mcp NOTED_TOKEN=<token> \
//	  go test ./internal/backend/ -run TestNotedLive -v
//
// It checks both routes into noted: the agent's (agent-go reading the
// resolved mcpServers.json, bearer header from ${NOTED_TOKEN}) and the
// buttons' (MCPActions). It creates one task and deletes it again.
func TestNotedLive(t *testing.T) {
	url := os.Getenv("SUPERLEO_NOTED_LIVE_URL")
	if url == "" || os.Getenv("NOTED_TOKEN") == "" {
		t.Skip("set SUPERLEO_NOTED_LIVE_URL and NOTED_TOKEN to run against a local noted")
	}
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	writeMCPConfig(t, NotedServer, url)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// The agent's route.
	resolved, err := resolveMCPConfig(mcpConfigPath(), os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentmcp.DefaultConfig()
	cfg.Enabled = true
	cfg.Servers = []string{resolved}
	svc, err := agentmcp.NewService(&cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.StartServers(ctx, []string{NotedServer}); err != nil {
		t.Fatalf("agent-go could not start noted: %v", err)
	}
	var tools int
	for _, st := range svc.ListServers() {
		if st.Name == NotedServer && st.Running {
			tools = st.ToolCount
		}
	}
	if tools < 10 {
		t.Fatalf("agent-go sees %d noted tools", tools)
	}
	r, err := svc.CallTool(ctx, "mcp_noted_get_overview", map[string]any{"horizon": "today"})
	if err != nil || !r.Success || r.IsError {
		t.Fatalf("agent route get_overview: %v %+v", err, r)
	}
	t.Logf("agent-go: %d noted tools, get_overview ok", tools)

	// The buttons' route.
	m := NewMCPActions(func() []string { return []string{NotedServer} })
	defer m.Close()
	created := m.Call(ctx, NotedServer, "create_task", map[string]any{"title": "superleo live test"}, MCPCallOptions{Quiet: true})
	if !created.OK {
		t.Fatalf("create_task: %+v", created)
	}
	task, _ := created.Structured.(map[string]any)
	if task == nil {
		t.Fatalf("create_task gave no structure: %s", created.Text)
	}
	if inner, ok := task["task"].(map[string]any); ok {
		task = inner
	}
	id, _ := task["id"].(string)
	if id == "" {
		t.Fatalf("no id in %v", created.Structured)
	}
	t.Logf("create_task structured keys: %v", keys(created.Structured))

	for _, tool := range []string{"get_overview", "time_summary", "list_holdings"} {
		res := m.Call(ctx, NotedServer, tool, map[string]any{}, MCPCallOptions{Quiet: true, Unlisted: true})
		if !res.OK {
			t.Errorf("%s: %+v", tool, res)
			continue
		}
		t.Logf("%s: structured=%v keys=%v", tool, res.Structured != nil, keys(res.Structured))
	}
	if res := m.Call(ctx, NotedServer, "complete_task", map[string]any{"id": id}, MCPCallOptions{Quiet: true}); !res.OK {
		t.Errorf("complete_task: %+v", res)
	}
	if res := m.Call(ctx, NotedServer, "delete_task", map[string]any{"id": id}, MCPCallOptions{Quiet: true}); res.Code != ActionNeedsConfirm {
		t.Errorf("unconfirmed delete_task: %+v", res)
	}
	if res := m.Call(ctx, NotedServer, "delete_task", map[string]any{"id": id}, MCPCallOptions{Quiet: true, Confirmed: true}); !res.OK {
		t.Errorf("confirmed delete_task: %+v", res)
	}
}

func keys(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ",")
}
