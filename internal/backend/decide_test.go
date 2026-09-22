package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// fakeLaya answers with a fixed probability and records what it was asked.
func fakeLaya(t *testing.T, p float64, seen *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(body, &req)
		if seen != nil {
			*seen = req.Text
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"destructive": map[string]any{"type": "noul", "noul": p, "confidence": 0.9},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDeciderOnlyEverAddsAnAsk(t *testing.T) {
	// The model is confidently wrong on negation, so it must never be able to
	// wave a call through. A "not destructive" answer leaves the existing
	// rules exactly as they were.
	srv := fakeLaya(t, 0.01, nil)
	policy := policyWith(newLayaDecider(srv.URL, 0.6, 2*time.Second))

	if !policy(agent.PermissionRequest{ToolName: "shell", ToolArgs: map[string]any{"command": "rm -rf /"}}) {
		t.Error("a rule-flagged call must still ask even when the model says it is safe")
	}
	if !policy(agent.PermissionRequest{ToolName: "delete_file"}) {
		t.Error("delete_file must still ask")
	}
}

func TestDeciderWidensPastTheNameRules(t *testing.T) {
	// The rules match on the tool's name, so a destructive command carried by
	// a tool named something else goes through unasked today.
	srv := fakeLaya(t, 0.95, nil)
	policy := policyWith(newLayaDecider(srv.URL, 0.6, 2*time.Second))

	req := agent.PermissionRequest{
		ToolName: "write_file",
		ToolArgs: map[string]any{"path": "/Users/me/.ssh/authorized_keys", "content": "ssh-rsa AAAA..."},
	}
	if toolNeedsApproval(req) {
		t.Fatal("precondition: the name rules do not flag this today")
	}
	if !policy(req) {
		t.Error("the decider should have widened the gate to ask about it")
	}
}

func TestReadOnlyIsNeverSentToTheModel(t *testing.T) {
	// A read-only call cannot change anything, so asking about it is noise and
	// paying for a decision on it is waste.
	var seen string
	srv := fakeLaya(t, 0.99, &seen)
	policy := policyWith(newLayaDecider(srv.URL, 0.6, 2*time.Second))

	if policy(agent.PermissionRequest{ToolName: "read_file", ReadOnly: true}) {
		t.Error("a read-only call must not ask")
	}
	if seen != "" {
		t.Errorf("the model was consulted about a read-only call: %q", seen)
	}
}

func TestAnUnreachableServiceFallsBackToTheRules(t *testing.T) {
	// The gate is a security surface: losing the decision service must leave
	// today's behaviour, not open or close the gate wholesale.
	policy := policyWith(newLayaDecider("http://127.0.0.1:1", 0.6, 200*time.Millisecond))

	if !policy(agent.PermissionRequest{ToolName: "shell", ToolArgs: map[string]any{"command": "rm -rf /"}}) {
		t.Error("rule-flagged call must still ask")
	}
	if policy(agent.PermissionRequest{ToolName: "write_file"}) {
		t.Error("an unreachable service must not start flagging everything")
	}
}

func TestNoDeciderIsTodaysBehaviour(t *testing.T) {
	policy := policyWith(nil)
	for _, tc := range []struct {
		req  agent.PermissionRequest
		want bool
	}{
		{agent.PermissionRequest{ToolName: "bash"}, true},
		{agent.PermissionRequest{ToolName: "write_file"}, false},
	} {
		if got := policy(tc.req); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.req.ToolName, got, tc.want)
		}
	}
	_ = context.Background
}

func TestGatePolicyUsesTheDeciderOnceSet(t *testing.T) {
	srv := fakeLaya(t, 0.95, nil)
	g := NewToolGate(true, "")

	req := agent.PermissionRequest{
		ToolName: "write_file",
		ToolArgs: map[string]any{"path": "/etc/hosts", "content": "127.0.0.1 evil"},
	}
	if g.Policy()(req) {
		t.Fatal("precondition: unset, the gate is the name rules")
	}

	g.SetDecider(newLayaDecider(srv.URL, 0.6, 2*time.Second))
	if !g.Policy()(req) {
		t.Error("the gate did not consult the decider")
	}

	g.SetDecider(nil)
	if g.Policy()(req) {
		t.Error("clearing the decider must restore the name rules")
	}
}

func TestDeciderIsOffByDefault(t *testing.T) {
	// The zero value must be today's behaviour: an upgrade cannot quietly put
	// a model in front of every tool call, and a settings.json that predates
	// the field gets the same gate it had yesterday.
	var s Settings
	if s.ToolApprovalDeciderURL != "" {
		t.Error("no decider URL by default")
	}
	if d := s.toolApprovalDecider(); d != nil {
		t.Error("an empty URL must yield no decider")
	}
}

func TestDeciderThresholdFallsBackToTheMeasuredDefault(t *testing.T) {
	s := Settings{ToolApprovalDeciderURL: "http://127.0.0.1:43711"}
	d, ok := s.toolApprovalDecider().(*layaDecider)
	if !ok {
		t.Fatal("expected a laya decider")
	}
	if d.threshold != DefaultDeciderThreshold {
		t.Errorf("threshold = %v, want the measured default %v", d.threshold, DefaultDeciderThreshold)
	}

	s.ToolApprovalDeciderThreshold = 0.42
	d2 := s.toolApprovalDecider().(*layaDecider)
	if d2.threshold != 0.42 {
		t.Errorf("an explicit threshold must win, got %v", d2.threshold)
	}
}
