package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/apns"
	"github.com/liliang-cn/superleo/internal/backend"
)

// ---- the hook: what it reads, and what it prints

func TestAPermissionRequestIsReadWithItsTool(t *testing.T) {
	ev, ok := ccEventFromHook("PermissionRequest", hookEvent(t, `{"session_id":"s1","cwd":"/w/superleo","hook_event_name":"PermissionRequest",
		"tool_name":"Bash","tool_input":{"command":"rm -rf node_modules","description":"Remove node_modules"},
		"permission_suggestions":[{"type":"addRules","behavior":"allow"}]}`), nil)
	if !ok || ev.Tool != "Bash" || ev.Text != "rm -rf node_modules" || ev.Session != "s1" || ev.Cwd != "/w/superleo" {
		t.Fatalf("got %+v (%v)", ev, ok)
	}
	var in map[string]any
	if err := json.Unmarshal([]byte(ev.Input), &in); err != nil || in["command"] != "rm -rf node_modules" {
		t.Errorf("input %q", ev.Input)
	}
	if ev, _ := ccEventFromHook("PermissionRequest", hookEvent(t, `{"session_id":"s1","tool_name":"Edit","tool_input":{"file_path":"/w/a.go","old_string":"x","new_string":"y"}}`), nil); ev.Text != "/w/a.go" {
		t.Errorf("edit summary %q", ev.Text)
	}
	if _, ok := ccEventFromHook("PermissionRequest", hookEvent(t, `{"session_id":"s1"}`), nil); ok {
		t.Error("a request without a tool was forwarded")
	}
	big := strings.Repeat("x", maxCCInput)
	if ev, _ := ccEventFromHook("PermissionRequest", hookEvent(t, `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"/w/big","content":"`+big+`"}}`), nil); ev.Input != "" || ev.Text != "/w/big" {
		t.Errorf("a huge input travelled: %d bytes, summary %q", len(ev.Input), ev.Text)
	}
}

func TestThePermissionDecisionIsClaudeCodesSchema(t *testing.T) {
	var got map[string]map[string]any
	if err := json.Unmarshal(permissionOutput(ccPermAnswer{Behavior: "allow"}), &got); err != nil {
		t.Fatal(err)
	}
	hso := got["hookSpecificOutput"]
	dec, _ := hso["decision"].(map[string]any)
	if hso["hookEventName"] != "PermissionRequest" || dec["behavior"] != "allow" || len(dec) != 1 {
		t.Errorf("allow: %v", got)
	}
	_ = json.Unmarshal(permissionOutput(ccPermAnswer{Behavior: "deny"}), &got)
	dec, _ = got["hookSpecificOutput"]["decision"].(map[string]any)
	if dec["behavior"] != "deny" || !strings.Contains(dec["message"].(string), "phone") {
		t.Errorf("deny: %v", got)
	}
	for _, b := range []string{"", "pass", "asking", "ALLOW"} {
		if out := permissionOutput(ccPermAnswer{Behavior: b}); out != nil {
			t.Errorf("%q printed %s", b, out)
		}
	}
}

func TestThePermissionWaitIsBounded(t *testing.T) {
	t.Setenv("SUPERLEO_HOOK_PERMISSION_WAIT", "")
	cases := map[string]struct {
		argv []string
		env  string
		want time.Duration
	}{
		"default":      {[]string{"PermissionRequest"}, "", ccPermDefaultWait},
		"flag":         {[]string{"PermissionRequest", "--wait", "45"}, "", 45 * time.Second},
		"flag=":        {[]string{"PermissionRequest", "--wait=60"}, "30", 60 * time.Second},
		"env":          {[]string{"PermissionRequest"}, "30", 30 * time.Second},
		"too short":    {[]string{"PermissionRequest", "--wait", "1"}, "", ccPermMinWait},
		"too long":     {[]string{"PermissionRequest", "--wait", "99999"}, "", ccPermMaxWait},
		"not a number": {[]string{"PermissionRequest", "--wait", "soon"}, "", ccPermDefaultWait},
	}
	for name, c := range cases {
		t.Setenv("SUPERLEO_HOOK_PERMISSION_WAIT", c.env)
		if got := permissionWait(c.argv); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

// ---- the machine: waiting fails open, to the terminal

func TestWaitingForThePhoneFailsOpen(t *testing.T) {
	old := ccPermAckWait
	ccPermAckWait = 50 * time.Millisecond
	t.Cleanup(func() { ccPermAckWait = old })
	far := time.Now().Add(time.Minute)
	verdicts := func(v ...string) chan ccPermVerdict {
		ch := make(chan ccPermVerdict, len(v))
		for _, x := range v {
			ch <- ccPermVerdict{Verdict: x}
		}
		return ch
	}
	ctx := context.Background()

	if a, w := waitCCPermission(ctx, verdicts(), far); a.Behavior != "" || !w {
		t.Errorf("core silent: %+v withdraw=%v", a, w)
	}
	if a, w := waitCCPermission(ctx, verdicts("pass"), far); a.Behavior != "" || w {
		t.Errorf("pass: %+v withdraw=%v", a, w)
	}
	if a, w := waitCCPermission(ctx, verdicts("asking", "allow"), far); a.Behavior != "allow" || w {
		t.Errorf("allow: %+v withdraw=%v", a, w)
	}
	if a, w := waitCCPermission(ctx, verdicts("asking", "deny"), far); a.Behavior != "deny" || w {
		t.Errorf("deny: %+v withdraw=%v", a, w)
	}
	start := time.Now()
	if a, w := waitCCPermission(ctx, verdicts("asking"), time.Now().Add(200*time.Millisecond)); a.Behavior != "" || !w || time.Since(start) < 150*time.Millisecond {
		t.Errorf("asked, nobody answered: %+v withdraw=%v after %s", a, w, time.Since(start))
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if a, w := waitCCPermission(cctx, verdicts(), far); a.Behavior != "" || !w {
		t.Errorf("hook gone: %+v withdraw=%v", a, w)
	}
	if err := (&App{}).AnswerCCPermission("nope", "allow", ""); err == nil {
		t.Error("an answer for nothing waiting was taken")
	}
	if err := (&App{}).AnswerCCPermission("x", "maybe", ""); err == nil {
		t.Error("an unknown verdict was taken")
	}
}

// ---- core: the gate, the card, the push, the collapse

type pushedAlerts struct {
	mu  sync.Mutex
	got []apns.Alert
}

func (p *pushedAlerts) list() []apns.Alert {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]apns.Alert(nil), p.got...)
}

func capturePushes(t *testing.T) *pushedAlerts {
	t.Helper()
	p := &pushedAlerts{}
	old := ccPushApproval
	ccPushApproval = func(_ *App, a apns.Alert) { p.mu.Lock(); p.got = append(p.got, a); p.mu.Unlock() }
	t.Cleanup(func() { ccPushApproval = old })
	return p
}

// permReport raises a permission request on core a as if from this machine
// (agent ""), and returns the slot its verdicts land in.
func permReport(a *App, away bool, session, request string) chan ccPermVerdict {
	ch := ccLocalState().awaitPerm(request)
	raw, _ := json.Marshal(ccReport{ccHookEvent: ccHookEvent{Event: "PermissionRequest", Session: session, Cwd: "/w/superleo",
		Tool: "Bash", Input: `{"command":"git push"}`, Text: "git push", Request: request,
		Expires: time.Now().Add(time.Minute).Format(time.RFC3339)}, Host: "mbp", Away: away})
	a.noteCCSession("", string(raw))
	return ch
}

func next(t *testing.T, ch chan ccPermVerdict) string {
	t.Helper()
	select {
	case v := <-ch:
		return v.Verdict
	case <-time.After(5 * time.Second):
		t.Fatal("no verdict")
		return ""
	}
}

func TestAPersonAtTheMachineIsAskedThere(t *testing.T) {
	_, _, a, _ := postServer(t)
	resetCC(t)
	pushes := capturePushes(t)
	ch := permReport(a, false, "s1", "r1")
	if v := next(t, ch); v != ccPermPass {
		t.Fatalf("verdict %q, want pass", v)
	}
	if len(a.PendingToolApprovals()) != 0 || len(pushes.list()) != 0 {
		t.Fatal("asked the phone while somebody is at the Mac")
	}
	// The phone's away switch asks the phone even so.
	if _, err := a.SetAway(true); err != nil {
		t.Fatal(err)
	}
	ch = permReport(a, false, "s1", "r2")
	if v := next(t, ch); v != ccPermAsking {
		t.Fatalf("away switch: verdict %q, want asking", v)
	}
	waitFor(t, "the card", func() bool { return len(a.PendingToolApprovals()) == 1 })
	a.ResolveToolApproval(a.PendingToolApprovals()[0]["id"].(string), false)
	if v := next(t, ch); v != ccPermDeny {
		t.Fatalf("verdict %q, want deny", v)
	}
}

func TestAnAwayPersonGetsAPushedCardAndTheAnswerGoesBack(t *testing.T) {
	_, _, a, _ := postServer(t)
	resetCC(t)
	pushes := capturePushes(t)
	ch := permReport(a, true, "s1", "r1")
	if v := next(t, ch); v != ccPermAsking {
		t.Fatalf("verdict %q", v)
	}
	waitFor(t, "the card", func() bool { return len(a.PendingToolApprovals()) == 1 })
	card := a.PendingToolApprovals()[0]
	if card["origin"] != "terminal" || card["by"] != "claude · mbp · superleo" || card["tool"] != "Bash" ||
		card["command"] != "git push" || card["project"] != "superleo" || card["ccSession"] != "s1" || card["machine"] != "mbp" {
		t.Errorf("card %v", card)
	}
	if _, ok := card["rule_suggestions"]; ok {
		t.Error("a terminal card offers to remember the answer, which it would never use")
	}
	got := pushes.list()
	if len(got) != 1 {
		t.Fatalf("pushes %+v", got)
	}
	p := got[0]
	data := p.Data.(map[string]any)
	if p.Category != ccApprovalCategory || p.CollapseID != "cc:s1" || p.Title != "claude · mbp · superleo" ||
		!strings.Contains(p.Body, "git push") || !p.Sound || data["kind"] != "approval" || data["id"] != card["id"] {
		t.Errorf("push %+v", p)
	}

	// The session's own "needs your permission" notification is the card.
	raw, _ := json.Marshal(ccReport{ccHookEvent: ccHookEvent{Event: "Notification", Kind: "permission_prompt", Session: "s1", Text: "Claude needs your permission to use Bash"}, Away: true})
	a.noteCCSession("", string(raw))
	if n := a.Notifications(); len(n) != 0 {
		t.Fatalf("a second notification for the same prompt: %+v", n)
	}

	if r := a.ResolveToolApproval(card["id"].(string), true); r != "ok" {
		t.Fatal(r)
	}
	if v := next(t, ch); v != ccPermAllow {
		t.Fatalf("verdict %q, want allow", v)
	}
	// Answered a moment ago: its late notification stays quiet too, while
	// another session's is told.
	a.noteCCSession("", string(raw))
	raw2, _ := json.Marshal(ccReport{ccHookEvent: ccHookEvent{Event: "Notification", Kind: "permission_prompt", Session: "s2", Text: "needs Edit"}, Away: true})
	a.noteCCSession("", string(raw2))
	if n := a.Notifications(); len(n) != 1 || n[0].ID != "cc:s2" {
		t.Fatalf("notifications %+v", n)
	}

	lines := readAudit(t, ccPermAuditPath())
	if len(lines) != 1 || lines[0].Outcome != "allowed" || lines[0].Tool != "Bash" || lines[0].Session != "s1" {
		t.Errorf("audit %+v", lines)
	}
	if b, _ := os.ReadFile(ccPermAuditPath()); strings.Contains(string(b), "git push") {
		t.Error("the audit line keeps the command")
	}
}

func TestACardWhoseHookStoppedWaitingIsWithdrawn(t *testing.T) {
	_, _, a, _ := postServer(t)
	resetCC(t)
	capturePushes(t)
	ch := permReport(a, true, "s1", "r1")
	if v := next(t, ch); v != ccPermAsking {
		t.Fatal(v)
	}
	waitFor(t, "the card", func() bool { return len(a.PendingToolApprovals()) == 1 })
	// Another machine cannot take it down.
	raw, _ := json.Marshal(ccReport{ccHookEvent: ccHookEvent{Event: "PermissionClosed", Session: "s1", Request: "r1"}})
	a.noteCCSession("someone-else", string(raw))
	time.Sleep(50 * time.Millisecond)
	if len(a.PendingToolApprovals()) != 1 {
		t.Fatal("another agent withdrew this machine's card")
	}
	a.noteCCSession("", string(raw))
	waitFor(t, "the card to go", func() bool { return len(a.PendingToolApprovals()) == 0 })
	select {
	case v := <-ch:
		t.Fatalf("a withdrawn card answered %q", v.Verdict)
	case <-time.After(100 * time.Millisecond):
	}
	waitFor(t, "the audit line", func() bool { return len(readAudit(t, ccPermAuditPath())) == 1 })
	if l := readAudit(t, ccPermAuditPath()); l[0].Outcome != "unanswered" {
		t.Errorf("audit %+v", l)
	}
}

func readAudit(t *testing.T, path string) []ccPermAudit {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []ccPermAudit
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e ccPermAudit
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func TestOnlyTheOwnerMayAnswerATerminalCard(t *testing.T) {
	h, c, a, _ := postServer(t)
	resetCC(t)
	capturePushes(t)
	ch := permReport(a, true, "s1", "r1")
	next(t, ch)
	waitFor(t, "the card", func() bool { return len(a.PendingToolApprovals()) == 1 })
	id := a.PendingToolApprovals()[0]["id"].(string)
	minted, err := a.CreateIntegrationToken("openclaw")
	if err != nil {
		t.Fatal(err)
	}
	body := `["` + id + `", true]`
	if rec := call(h, http.MethodPost, "/api/rpc/ResolveToolApproval", minted.Token, body); rec.Code != http.StatusForbidden {
		t.Errorf("integration token: %d", rec.Code)
	}
	if rec := call(h, http.MethodPost, "/api/rpc/ResolveToolApproval", "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	if len(a.PendingToolApprovals()) != 1 {
		t.Fatal("answered without the owner's token")
	}
	code, _ := c.devices.newCode()
	_, phone, ok, err := c.devices.claim(code, "iPhone")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	if rec := call(h, http.MethodPost, "/api/rpc/ResolveToolApproval", phone, body); rec.Code != http.StatusOK {
		t.Fatalf("paired phone: %d %s", rec.Code, rec.Body)
	}
	if v := next(t, ch); v != ccPermAllow {
		t.Fatalf("verdict %q", v)
	}
	// Core gives an agent its verdicts; an agent cannot hand core one.
	if !agentCallable["AnswerCCPermission"] {
		t.Error("core cannot answer the machine")
	}
}

func TestATerminalTurnPushCarriesItsReplyTarget(t *testing.T) {
	n := ccNotice("mac", ccReport{ccHookEvent: ccHookEvent{Event: "Stop", Session: "s-1", Cwd: "/w/superleo", Text: "done"}}, "", time.Now())
	a, ok := alertFor(n)
	if !ok || a.Category != ccReplyCategory {
		t.Fatalf("alert %+v", a)
	}
	reply, _ := a.Data.(map[string]any)["reply"].(map[string]any)
	if reply["agent"] != "mac" || reply["session"] != "s-1" {
		t.Errorf("data %v", a.Data)
	}
	plain, _ := alertFor(backend.Notice{Title: "该出发接三宝了", Message: "17:10", Kind: backend.KindReminder})
	if plain.Category != "" || plain.Data.(map[string]any)["reply"] != nil {
		t.Errorf("a plain notice has reply parts: %+v", plain)
	}
}

// ---- end to end: hook → this machine → agent link → core card → phone → hook

func TestATerminalPermissionPromptIsAnsweredFromThePhone(t *testing.T) {
	_, _, core, _ := postServer(t)
	resetCC(t)
	ccLocalV = testLocal(&fakeTerminals{})
	capturePushes(t)
	idle := 600
	var idleMu sync.Mutex
	old := ccIdleSeconds
	ccIdleSeconds = func() int { idleMu.Lock(); defer idleMu.Unlock(); return idle }
	t.Cleanup(func() { ccIdleSeconds = old })

	home, err := os.MkdirTemp("/tmp", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("SUPERLEO_HOME", home)

	addr := startCore(t, core)
	agent := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "mac", Version: "test"})
		close(done)
	}()
	go agent.serveCCHookSocket(ctx)
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the agent to link", func() bool { return core.agents().get("mac") != nil && agent.agentTap.Load() != nil })
	sock := filepath.Join(home, ccHookSocketName)
	waitFor(t, "the hook socket", func() bool { _, err := os.Stat(sock); return err == nil })

	ev := ccHookEvent{Event: "PermissionRequest", Session: "s9", Cwd: "/w/superleo", Tool: "Bash", Input: `{"command":"make deploy"}`, Text: "make deploy", Wait: 30}
	type result struct{ a ccPermAnswer }
	ask := func() chan result {
		out := make(chan result, 1)
		go func() {
			hctx, hcancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer hcancel()
			out <- result{askCCPermission(hctx, []string{sock}, ev)}
		}()
		return out
	}

	// Away: the card is on core, the phone says yes, the hook prints allow.
	got := ask()
	waitFor(t, "the card on core", func() bool { return len(core.PendingToolApprovals()) == 1 })
	card := core.PendingToolApprovals()[0]
	if card["by"] != "claude · mac · superleo" || card["command"] != "make deploy" {
		t.Fatalf("card %v", card)
	}
	core.ResolveToolApproval(card["id"].(string), true)
	select {
	case r := <-got:
		if r.a.Behavior != "allow" {
			t.Fatalf("hook got %+v", r.a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hook never heard")
	}

	// At the desk: answered at once with nothing, and no card.
	idleMu.Lock()
	idle = 5
	idleMu.Unlock()
	start := time.Now()
	r := <-ask()
	if r.a.Behavior != "" || time.Since(start) > 3*time.Second || len(core.PendingToolApprovals()) != 0 {
		t.Fatalf("at the desk: %+v after %s, cards %d", r.a, time.Since(start), len(core.PendingToolApprovals()))
	}

	// YOLO covers a terminal nobody is at: allowed at once, no card. (One
	// with somebody at it still asks them there, as it always did.)
	core.gateForMCP = backend.NewToolGate(true, filepath.Join(home, "audit.jsonl"))
	core.gateForMCP.StartYolo()
	idleMu.Lock()
	idle = 600
	idleMu.Unlock()
	start = time.Now()
	r = <-ask()
	if r.a.Behavior != "allow" || time.Since(start) > 3*time.Second || len(core.PendingToolApprovals()) != 0 {
		t.Fatalf("yolo: %+v after %s, cards %d", r.a, time.Since(start), len(core.PendingToolApprovals()))
	}
}

// The command itself, with no SuperLeo to ask: silent, and quick.
func TestThePermissionHookIsSilentWithNobodyToAsk(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("SUPERLEO_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("SUPERLEO_HOOK_SOCKET", filepath.Join(home, "nobody.sock"))
	t.Setenv("SUPERLEO_CLI_RUN", "")
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	_, _ = inW.WriteString(`{"session_id":"h1","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	inW.Close()
	start := time.Now()
	HookMain([]string{"PermissionRequest", "--wait", "30"})
	os.Stdin, os.Stdout = oldIn, oldOut
	outW.Close()
	b := make([]byte, 4096)
	n, _ := outR.Read(b)
	if n != 0 || time.Since(start) > 3*time.Second {
		t.Fatalf("printed %q after %s", b[:n], time.Since(start))
	}
}
