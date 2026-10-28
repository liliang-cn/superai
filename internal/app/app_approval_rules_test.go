package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// Remembered answers, through the App: the card offers them, the answer saves
// one, the next call like it is answered without a card, and the gate's own
// precedence (YOLO first) still holds.

func rulesHome(t *testing.T) {
	t.Helper()
	t.Setenv("SUPERLEO_HOME", t.TempDir())
}

func relayedBash(id, cmd string) backend.ApprovalRequest {
	now := time.Now()
	return backend.ApprovalRequest{
		ID: id, Tool: "claude.mac · Bash", Command: cmd, Args: map[string]any{"command": cmd},
		AgentID: "claude.mac", Machine: "mac", Workdir: "/Users/l/Things/dev/apps/superleo",
		AskedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}

func rpcJSON(t *testing.T, a *App, name string, args ...any) any {
	t.Helper()
	raw := make([]json.RawMessage, len(args))
	for i, v := range args {
		b, _ := json.Marshal(v)
		raw[i] = b
	}
	out, err := callMethod(a, name, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestRememberViaRPCAnswersTheNextCallLikeIt(t *testing.T) {
	rulesHome(t)
	a := NewApp()
	read := captureEmits(a)

	cmd := "kubectl -n superleo rollout restart deployment/superai-worker"
	first := askInBackground(a, relayedBash("r-1", cmd))
	waitForPending(t, a, 1)

	// The card carries the candidates, narrowest first, in words.
	asked := waitForEvent(t, read, "tool:approval", "r-1")
	sug, _ := asked["rule_suggestions"].([]backend.RuleSuggestion)
	if len(sug) != 3 || sug[0].Scope != backend.RememberSameCommandPrefix ||
		sug[0].Label != "以后 claude 在 mac 上运行以 “kubectl -n superleo rollout restart” 开头的命令都不用问" {
		t.Fatalf("rule_suggestions = %+v", asked["rule_suggestions"])
	}

	res := rpcJSON(t, a, "ResolveToolApprovalRemember", "r-1", true,
		map[string]any{"scope": backend.RememberSameCommandPrefix}).(map[string]any)
	if res["result"] != "ok" {
		t.Fatalf("result = %+v", res)
	}
	rule, ok := res["rule"].(backend.ApprovalRule)
	if !ok || rule.Scope.CommandPrefix != "kubectl -n superleo rollout restart" || rule.Scope.Machine != "mac" ||
		rule.Scope.Agent != "claude" || rule.CreatedFromApprovalID != "r-1" || rule.Decision != backend.RuleAllow {
		t.Fatalf("rule = %+v", res["rule"])
	}
	if dec := <-first; !dec.Allowed || dec.By != backend.DecidedByUser {
		t.Fatalf("first answer = %+v", dec)
	}

	// The next one like it: no card.
	dec, _ := a.askToolApproval(context.Background(), relayedBash("r-2", "kubectl -n superleo rollout restart deployment/superai-queen"))
	if !dec.Allowed || dec.By != backend.DecidedByApprovalRule || !strings.HasPrefix(dec.Reason, "按规则自动同意：") {
		t.Fatalf("second answer = %+v", dec)
	}
	for _, ev := range read() {
		if ev.name == "tool:approval" && ev.payload["id"] == "r-2" {
			t.Fatal("a rule-answered call still raised a card")
		}
	}

	// Not like it: asked.
	other := askInBackground(a, relayedBash("r-3", "kubectl -n superleo delete pod x"))
	waitForPending(t, a, 1)
	if got := a.ResolveToolApproval("r-3", false); got != "ok" { // the old two-argument answer still works
		t.Fatalf("ResolveToolApproval = %q", got)
	}
	if dec := <-other; dec.Allowed {
		t.Fatal("deny was not delivered")
	}

	// Listed, deleted, and asked again.
	list := rpcJSON(t, a, "ListApprovalRules").([]backend.ApprovalRule)
	if len(list) != 1 || list[0].ID != rule.ID {
		t.Fatalf("ListApprovalRules = %+v", list)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("SUPERLEO_HOME"), "approval-rules.json")); err != nil {
		t.Fatalf("rules not persisted in the data dir: %v", err)
	}
	if del := rpcJSON(t, a, "DeleteApprovalRule", rule.ID).(map[string]any); del["deleted"] != true {
		t.Fatalf("DeleteApprovalRule = %+v", del)
	}
	again := askInBackground(a, relayedBash("r-4", cmd))
	waitForPending(t, a, 1)
	_ = a.ResolveToolApproval("r-4", true)
	<-again
}

func TestRememberAnswersOthersAlreadyWaiting(t *testing.T) {
	rulesHome(t)
	a := NewApp()
	a.emitFn = func(string, map[string]any) {}

	w1 := askInBackground(a, relayedBash("p-1", "go test ./internal/backend"))
	w2 := askInBackground(a, relayedBash("p-2", "go test ./internal/app"))
	w3 := askInBackground(a, relayedBash("p-3", "go vet ./..."))
	waitForPending(t, a, 3)

	res := a.ResolveToolApprovalRemember("p-1", true, backend.RememberOption{Scope: backend.RememberSameCommandPrefix})
	if res["error"] != nil {
		t.Fatalf("remember: %+v", res)
	}
	if d := <-w1; d.By != backend.DecidedByUser {
		t.Errorf("p-1 = %+v", d)
	}
	if d := <-w2; !d.Allowed || d.By != backend.DecidedByApprovalRule {
		t.Errorf("p-2, covered by the new rule, = %+v", d)
	}
	waitForPending(t, a, 1)
	_ = a.ResolveToolApproval("p-3", false)
	if d := <-w3; d.Allowed {
		t.Errorf("p-3 = %+v", d)
	}
}

func TestRememberDenyAndBadScope(t *testing.T) {
	rulesHome(t)
	a := NewApp()
	a.emitFn = func(string, map[string]any) {}

	w := askInBackground(a, relayedBash("d-1", "rm -rf build"))
	waitForPending(t, a, 1)
	// No safe prefix for this one: the answer still goes, the error says why.
	res := a.ResolveToolApprovalRemember("d-1", false, backend.RememberOption{Scope: backend.RememberSameCommandPrefix})
	if res["result"] != "ok" || res["error"] == nil || res["rule"] != nil {
		t.Fatalf("bad scope = %+v", res)
	}
	<-w

	w = askInBackground(a, relayedBash("d-2", "rm -rf build"))
	waitForPending(t, a, 1)
	res = a.ResolveToolApprovalRemember("d-2", false, backend.RememberOption{Scope: backend.RememberSameToolAndTarget})
	if r, ok := res["rule"].(backend.ApprovalRule); !ok || r.Decision != backend.RuleDeny || r.Label != "以后 claude 在 mac 上运行 “rm -rf build”都直接拒绝" {
		t.Fatalf("deny rule = %+v", res)
	}
	<-w
	dec, _ := a.askToolApproval(context.Background(), relayedBash("d-3", "rm -rf build"))
	if dec.Allowed || dec.By != backend.DecidedByApprovalRule || !strings.HasPrefix(dec.Reason, "按规则自动拒绝：") {
		t.Fatalf("deny rule answer = %+v", dec)
	}
	if got := a.ResolveToolApprovalRemember("gone", true, backend.RememberOption{Scope: backend.RememberSameTool}); got["rule"] != nil {
		t.Errorf("a rule was made from a request that is not waiting: %+v", got)
	}
}

// Through the real service and gate: an allow rule lets the model's bash run
// without a card and is audited as such; a deny rule stops it; YOLO beats a
// deny rule, as it beats everything.
func TestGateUsesRulesAndYoloStillWins(t *testing.T) {
	cases := []struct {
		name     string
		decision string
		yolo     bool
		ran      bool
		by       string
	}{
		{"allow-rule", backend.RuleAllow, false, true, backend.DecidedByApprovalRule},
		{"deny-rule", backend.RuleDeny, false, false, backend.DecidedByApprovalRule},
		{"yolo-over-deny", backend.RuleDeny, true, true, backend.DecidedByYolo},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := "touch " + c.name + ".txt"
			a, home := gateTestApp(t, cmd, false)
			if _, err := a.approvalRules().Add(backend.ApprovalRule{
				Tool: backend.KindShell, Decision: c.decision,
				Scope: backend.RuleScope{Command: cmd, Machine: backend.LocalMachine, Agent: "queen"},
			}); err != nil {
				t.Fatal(err)
			}
			if c.yolo {
				a.StartYoloMode()
			}
			id := a.SendChat("rules-"+c.name, "make a file", nil)
			waitForFinish(t, a, id)

			_, err := os.Stat(filepath.Join(home, "workspace", c.name+".txt"))
			if (err == nil) != c.ran {
				t.Fatalf("ran=%v, want %v", err == nil, c.ran)
			}
			entries := a.ToolApprovalInfo(10)["entries"].([]backend.AuditEntry)
			if len(entries) == 0 {
				t.Fatal("nothing audited")
			}
			last := entries[len(entries)-1]
			if last.DecidedBy != c.by || last.Allowed != c.ran {
				t.Errorf("audit = %+v", last)
			}
			if c.by == backend.DecidedByApprovalRule && !strings.Contains(last.Reason, "按规则自动") {
				t.Errorf("audit reason = %q", last.Reason)
			}
		})
	}
}

// A coding agent's prompt answered by a rule is in the log, which is what
// 最近处理 reads.
func TestRuleAnswerToACodingAgentIsAudited(t *testing.T) {
	a, _ := gateTestApp(t, "true", false)
	if _, err := a.approvalRules().Add(backend.ApprovalRule{
		Tool: backend.KindWriteFile, Decision: backend.RuleAllow,
		Scope: backend.RuleScope{PathPrefix: "/Users/l/Things/dev/apps/superleo", Machine: "mac", Agent: "claude"},
	}); err != nil {
		t.Fatal(err)
	}
	req := backend.ApprovalRequest{ID: "c-1", Tool: "claude.mac · Write", Machine: "mac",
		Args: map[string]any{"file_path": "/Users/l/Things/dev/apps/superleo/a.go", "content": "x"}}
	dec, _ := a.askToolApproval(context.Background(), req)
	if !dec.Allowed {
		t.Fatalf("dec = %+v", dec)
	}
	entries := a.ToolApprovalInfo(10)["entries"].([]backend.AuditEntry)
	last := entries[len(entries)-1]
	if last.Tool != "claude.mac · Write" || last.DecidedBy != backend.DecidedByApprovalRule ||
		last.Reason != "按规则自动同意：以后 claude 在 mac 上写入 ~/Things/dev/apps/superleo 下的文件都不用问" {
		t.Errorf("audit = %+v", last)
	}
}
