package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A server reached by URL is written with its transport and headers, the file
// kept to its owner for the token; adding a command server beside it leaves
// it as it was.
func TestInstallMCPURLServerKeepsItsEntry(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	var svc *Service
	for _, bad := range []struct{ name, transport, url string }{
		{"", "", "https://a.example/mcp"},
		{"x", "", ""},
		{"x", "", "ftp://a.example/mcp"},
		{"x", "", "not a url"},
		{"x", "websocket", "https://a.example/mcp"},
	} {
		if err := svc.InstallMCPURLServer(t.Context(), bad.name, bad.transport, bad.url, nil); err == nil {
			t.Errorf("%+v was taken", bad)
		}
	}
	if err := svc.InstallMCPURLServer(t.Context(), "linear", "", "https://mcp.linear.app/mcp", map[string]string{"Authorization": "Bearer lin_1234"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.InstallMCPURLServer(t.Context(), "events", "SSE", "https://events.example/sse", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.InstallMCPServer(t.Context(), "sqlite", "mcp-sqlite", []string{"/data/a.db"}, nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(mcpConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("a file with a token in it is %v", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(mcpConfigPath())
	var f struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	l := f.MCPServers["linear"]
	if l["type"] != "http" || l["url"] != "https://mcp.linear.app/mcp" || l["headers"].(map[string]any)["Authorization"] != "Bearer lin_1234" {
		t.Fatalf("linear after a command server was added: %v", l)
	}
	if e := f.MCPServers["events"]; e["type"] != "sse" {
		t.Fatalf("events: %v", e)
	}
	if s := f.MCPServers["sqlite"]; s["command"] != "mcp-sqlite" {
		t.Fatalf("sqlite: %v", s)
	}
	if own := OwnMCPServers(); !own["linear"] || !own["events"] || !own["sqlite"] {
		t.Fatalf("own servers: %v", own)
	}
}

// Added while SuperLeo runs, a server by URL is connected at once — over
// streamable HTTP and over SSE, with the token it wants, one named by a
// variable too — and its tools are there.
func TestInstallMCPURLServerConnects(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("ISSUES_TOKEN", "s3cret")
	server := func(tool string) *mcp.Server {
		s := mcp.NewServer(&mcp.Implementation{Name: tool, Version: "1"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: tool, Description: tool}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: tool + " ok"}}}, nil, nil
		})
		return s
	}
	guarded := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer s3cret" {
				http.Error(w, "no token", http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(w, r)
		})
	}
	issues := server("list_issues")
	streamable := httptest.NewServer(guarded(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return issues }, nil)))
	defer streamable.Close()
	events := server("recent_events")
	sse := httptest.NewServer(guarded(mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return events }, nil)))
	defer sse.Close()
	defer sse.CloseClientConnections()

	cfg := &config.Config{Home: t.TempDir(), RAG: config.RAGConfig{Enabled: false}, Memory: config.MemoryConfig{StoreType: "file"}}
	cfg.ApplyHomeLayout()
	agentSvc, err := agent.New("mcp-url-test").WithConfig(cfg).WithLLM(scriptedLLM{}).WithMCP().Build()
	if err != nil {
		t.Fatal(err)
	}
	defer agentSvc.Close()
	s := &Service{svc: agentSvc}

	if err := s.InstallMCPURLServer(t.Context(), "issues", "http", streamable.URL, map[string]string{"Authorization": "Bearer wrong"}); err == nil {
		t.Fatal("connected with the wrong token")
	}
	if err := s.InstallMCPURLServer(t.Context(), "issues", "http", streamable.URL, map[string]string{"Authorization": "Bearer ${ISSUES_TOKEN}"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InstallMCPURLServer(t.Context(), "events", "sse", sse.URL, map[string]string{"Authorization": "Bearer s3cret"}); err != nil {
		t.Fatal(err)
	}
	tools := map[string][]string{}
	for _, st := range agentSvc.MCP.ListServers() {
		if st.Running {
			for _, tl := range st.Tools {
				tools[st.Name] = append(tools[st.Name], tl.Name)
			}
		}
	}
	if !slices.Contains(tools["issues"], "list_issues") || !slices.Contains(tools["events"], "recent_events") {
		t.Fatalf("running servers and their tools: %v", tools)
	}
	// The variable is kept as a variable on disk, never the token it names.
	raw, _ := os.ReadFile(mcpConfigPath())
	var f struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal(raw, &f)
	if h, _ := f.MCPServers["issues"]["headers"].(map[string]any); h["Authorization"] != "Bearer ${ISSUES_TOKEN}" {
		t.Fatalf("issues on disk: %v", f.MCPServers["issues"])
	}
}
