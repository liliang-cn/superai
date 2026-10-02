package backend

import (
	"context"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

func ruleFor(session string, v Verdict) SessionRule {
	return func(req agent.PermissionRequest) (Verdict, string) {
		if req.SessionID == session {
			return v, "agent rule"
		}
		return VerdictNone, ""
	}
}

// "Never" holds whatever else is switched on.
func TestARuleRefusalHoldsWithTheGateOffAndYOLOOn(t *testing.T) {
	g, _ := gateForTest(t, false)
	g.StartYolo()
	g.SetSessionRule(ruleFor("sess-1", VerdictDeny))
	res, err := g.Handler()(context.Background(), bashReq("rm -rf /tmp/x"))
	if err != nil || res.Allowed {
		t.Fatalf("allowed: %+v %v", res, err)
	}
}

// A read-only tool the rule forbids still reaches the handler.
func TestARuleSeesCallsTheNameRulesWouldWaveThrough(t *testing.T) {
	g, _ := gateForTest(t, true)
	read := agent.PermissionRequest{ToolName: "mcp_github_list_issues", SessionID: "sess-1", ReadOnly: true}
	if g.Policy()(read) {
		t.Fatal("a read-only call reached the handler with no rule")
	}
	g.SetSessionRule(ruleFor("sess-1", VerdictDeny))
	if !g.Policy()(read) {
		t.Fatal("the rule's call did not reach the handler")
	}
	if g.Policy()(agent.PermissionRequest{ToolName: "mcp_github_list_issues", SessionID: "other", ReadOnly: true}) {
		t.Fatal("another session's call was caught by the rule")
	}
}

func TestARuleAllowsWithoutAskingAnyone(t *testing.T) {
	g, _ := gateForTest(t, true)
	asked := false
	g.SetApprover(func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
		asked = true
		return ApprovalDecision{}, nil
	})
	g.SetSessionRule(ruleFor("sess-1", VerdictAllow))
	res, _ := g.Handler()(context.Background(), bashReq("make test"))
	if !res.Allowed || asked {
		t.Fatalf("allowed %v asked %v", res.Allowed, asked)
	}
}

// Ask means ask: an unattended session is still asked, and on the long clock.
func TestARuleAskAsksEvenAnUnattendedRunAndWaitsLong(t *testing.T) {
	g, _ := gateForTest(t, true)
	g.Unattend("sess-1")
	var got ApprovalRequest
	g.SetApprover(func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
		got = req
		return ApprovalDecision{Allowed: true}, nil
	})
	g.SetSessionRule(ruleFor("sess-1", VerdictAsk))
	res, _ := g.Handler()(context.Background(), bashReq("git push"))
	if !res.Allowed || got.ID == "" {
		t.Fatalf("not asked: %+v", res)
	}
	if got.ExpiresAt.Sub(got.AskedAt) < RuleAskWait {
		t.Fatalf("waits only %s", got.ExpiresAt.Sub(got.AskedAt))
	}
}
