package backend

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// A forbidden tool is refused whatever the gate's switches say: with the gate
// off, with YOLO on, with nobody asked. Measured: agent-browser's close,
// sent to a person's own Chrome, crashed it with a dozen tabs open.
func TestAForbiddenToolIsRefusedWhateverTheSwitches(t *testing.T) {
	g := NewToolGate(false, filepath.Join(t.TempDir(), "audit.jsonl"))
	g.StartYolo()
	g.Forbid("mcp_browser_agent_browser_close", "it would quit the person's Chrome")
	req := agent.PermissionRequest{ToolName: "mcp_browser_agent_browser_close", SessionID: "s"}
	if !g.Policy()(req) {
		t.Fatal("a forbidden tool did not reach the gate")
	}
	res, err := g.Handler()(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Allowed {
		t.Fatal("a forbidden tool was allowed")
	}
	if res.Reason == "" {
		t.Fatal("refused without saying why")
	}
	other := agent.PermissionRequest{ToolName: "mcp_browser_agent_browser_open", SessionID: "s"}
	if g.Policy()(other) {
		t.Fatal("an ordinary browser tool was sent to the gate")
	}
}
