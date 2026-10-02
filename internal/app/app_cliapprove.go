package app

// Claude Code's permission prompts, answered through SuperAI's approval cards.
//
// claude --permission-prompt-tool names an MCP tool that Claude calls with
// {tool_name, input} whenever it would otherwise ask at the terminal, and that
// answers {"behavior":"allow","updatedInput":…} or {"behavior":"deny",…}. This
// file serves that tool on a loopback port, one URL per run carrying a secret,
// and turns each call into askToolApproval — the same card, the same phone
// prompt, the same audit as SuperAI's own tools.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/superai/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cliApproveWait is how long a CLI permission prompt waits for a person.
// Long: the run is paused on it, and the person may be walking to their desk.
const cliApproveWait = 15 * time.Minute

type cliApprover struct {
	mu      sync.Mutex
	base    string            // http://127.0.0.1:port
	secrets map[string]string // run id → secret in its URL
}

type cliApproveIn struct {
	ToolName  string         `json:"tool_name" jsonschema:"the tool Claude wants to use"`
	Input     map[string]any `json:"input" jsonschema:"the arguments it wants to use it with"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
}

// cliApproveURL starts the loopback server on first use and returns the
// MCP URL for one run.
func (a *App) cliApproveURL(runID string) (string, error) {
	a.cliApproveOnce.Do(func() {
		ap := &cliApprover{secrets: map[string]string{}}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return
		}
		ap.base = "http://" + ln.Addr().String()
		mux := http.NewServeMux()
		mux.HandleFunc("/approve/", func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/approve/"), "/")
			if len(parts) < 2 {
				http.NotFound(w, r)
				return
			}
			run, secret := parts[0], parts[1]
			ap.mu.Lock()
			ok := secret != "" && ap.secrets[run] == secret
			ap.mu.Unlock()
			if !ok {
				http.Error(w, "unknown run", http.StatusForbidden)
				return
			}
			a.cliApproveHandler(run).ServeHTTP(w, r)
		})
		go func() { _ = http.Serve(ln, mux) }()
		a.cliApprove = ap
	})
	ap := a.cliApprove
	if ap == nil {
		return "", fmt.Errorf("could not listen on loopback")
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	secret := hex.EncodeToString(b)
	ap.mu.Lock()
	ap.secrets[runID] = secret
	ap.mu.Unlock()
	return fmt.Sprintf("%s/approve/%s/%s", ap.base, runID, secret), nil
}

func (a *App) cliApproveHandler(runID string) http.Handler {
	s := mcp.NewServer(&mcp.Implementation{Name: "superai", Version: "1"}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "approve",
		Description: "Asks the SuperAI user whether a tool call may run.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in cliApproveIn) (*mcp.CallToolResult, any, error) {
		return a.cliApprove1(ctx, runID, in), nil, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
}

func (a *App) cliApprove1(ctx context.Context, runID string, in cliApproveIn) *mcp.CallToolResult {
	s := a.cliStore()
	s.mu.Lock()
	run := s.runs[runID]
	s.mu.Unlock()
	answer := func(v map[string]any) *mcp.CallToolResult {
		raw, _ := json.Marshal(v)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
	}
	if run == nil || run.State != "running" {
		return answer(map[string]any{"behavior": "deny", "message": "this run is no longer active"})
	}
	_, detail, _ := toolCallParts(map[string]any{"name": in.ToolName, "input": in.Input})
	a.addCLIEvent(run, CLIRunEvent{Kind: "note", Tool: in.ToolName, Detail: detail, Text: "waiting for approval"})

	now := time.Now()
	req := backend.ApprovalRequest{
		ID:        uuid.NewString(),
		Tool:      run.Agent + " · " + in.ToolName,
		Command:   pickStr(in.Input, "command"),
		Args:      in.Input,
		SessionID: run.Thread,
		AgentID:   run.Agent,
		AskedAt:   now,
		ExpiresAt: now.Add(cliApproveWait),
	}
	wait, cancel := context.WithTimeout(ctx, cliApproveWait)
	defer cancel()
	dec, _ := a.askToolApproval(wait, req)
	if dec.Allowed {
		a.addCLIEvent(run, CLIRunEvent{Kind: "note", Tool: in.ToolName, Text: "approved"})
		return answer(map[string]any{"behavior": "allow", "updatedInput": in.Input})
	}
	why := "the user said no"
	if wait.Err() != nil {
		why = "nobody answered in time"
	}
	a.addCLIEvent(run, CLIRunEvent{Kind: "note", Tool: in.ToolName, Text: "denied: " + why, Failed: true})
	return answer(map[string]any{"behavior": "deny", "message": "Denied by the SuperAI user (" + why + "). Do not retry the same call; ask what to do instead or try something else."})
}
