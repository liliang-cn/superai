package backend

import (
	"context"
	"errors"
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

// A guard refuses the call when it fails, and when it passes the call goes on
// as it would have: an ordinary browser call is not turned into a question.
func TestAGuardRefusesOnFailureAndAsksNothingOnSuccess(t *testing.T) {
	g := NewToolGate(true, filepath.Join(t.TempDir(), "audit.jsonl"))
	fail := true
	g.Guard("mcp_browser_", func(context.Context) error {
		if fail {
			return errors.New("not in its own tab")
		}
		return nil
	})
	req := agent.PermissionRequest{ToolName: "mcp_browser_agent_browser_open", SessionID: "s"}
	if !g.Policy()(req) {
		t.Fatal("a guarded tool did not reach the gate")
	}
	res, err := g.Handler()(context.Background(), req)
	if err != nil || res.Allowed {
		t.Fatalf("a failed guard let the call through: %+v %v", res, err)
	}
	fail = false
	res, err = g.Handler()(context.Background(), req)
	if err != nil || !res.Allowed {
		t.Fatalf("a passed guard did not allow an ordinary call: %+v %v", res, err)
	}
}
