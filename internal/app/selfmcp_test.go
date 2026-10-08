package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// selfSession serves app's MCP endpoint and connects a client to it.
func selfSession(t *testing.T, app *App) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(newMCPHandler(app, "test"))
	t.Cleanup(srv.Close)
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	ss, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	return ss
}

func callSelf(t *testing.T, ss *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// Every tool the agent has about itself is on /mcp too, prefixed, and the
// readers say they only read.
func TestSelfToolsAreServedOverMCP(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	ss := selfSession(t, NewApp())
	res, err := ss.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool
	}
	for _, st := range selfTools() {
		tool := got["superai_"+st.Name]
		if tool == nil {
			t.Errorf("superai_%s is not served", st.Name)
			continue
		}
		if ro := tool.Annotations != nil && tool.Annotations.ReadOnlyHint; ro != st.ReadOnly {
			t.Errorf("superai_%s read-only hint %v, want %v", st.Name, ro, st.ReadOnly)
		}
	}
	if out, isErr := callSelf(t, ss, "superai_status", nil); isErr || !strings.Contains(out, `"ready"`) {
		t.Fatalf("status: %v %s", isErr, out)
	}
}

// Reading the settings never shows a secret, and writing them is the
// whitelist with its ranges.
func TestSelfSettingsHideSecretsAndKeepTheWhitelist(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	app := NewApp()
	app.settings = &backend.Settings{LLMModel: "m1", LLMKey: "sk-very-secret", TelegramBotToken: "tg-secret", MaxRounds: 30}
	ss := selfSession(t, app)

	out, isErr := callSelf(t, ss, "superai_settings_get", nil)
	if isErr || strings.Contains(out, "very-secret") || strings.Contains(out, "tg-secret") {
		t.Fatalf("settings_get leaked or failed: %v %s", isErr, out)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(out), &snap); err != nil || snap["llm_key_set"] != true || snap["llm_model"] != "m1" {
		t.Fatalf("snapshot: %v %s", err, out)
	}

	for _, bad := range []map[string]any{
		{"key": "disable_tool_approval", "value": true},
		{"key": "llm_key", "value": "sk-other"},
		{"key": "max_rounds", "value": 0},
	} {
		if out, isErr := callSelf(t, ss, "superai_settings_set", bad); !isErr {
			t.Errorf("settings_set accepted %v: %s", bad, out)
		}
	}
}

// A change waits while a turn is running — applying it restarts the agent
// under that turn — and shows as pending meanwhile.
func TestSelfSettingWaitsForTheTurnToFinish(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	app := NewApp()
	app.settings = &backend.Settings{LLMModel: "m1", MaxRounds: 30}
	app.openTurns.Store("turn-1", struct{}{})
	ss := selfSession(t, app)

	if out, isErr := callSelf(t, ss, "superai_settings_set", map[string]any{"key": "max_rounds", "value": 60}); isErr {
		t.Fatalf("settings_set: %s", out)
	}
	time.Sleep(700 * time.Millisecond)
	if app.GetSettings().MaxRounds != 30 {
		t.Fatal("the change was applied under a running turn")
	}
	if out, _ := callSelf(t, ss, "superai_settings_get", nil); !strings.Contains(out, `"pending":{"max_rounds":60}`) {
		t.Fatalf("the waiting change is not shown: %s", out)
	}
	app.openTurns.Delete("turn-1")
	waitFor(t, "the change to be applied", func() bool { return app.GetSettings().MaxRounds == 60 })
	waitFor(t, "pending to clear", func() bool { return len(app.pendingSettings()) == 0 })
}
