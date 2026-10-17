package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/agentexec/hooks"
	"github.com/liliang-cn/superai/internal/backend"
)

// ---- the hook: what it forwards, and what it leaves alone

func hookEvent(t *testing.T, payload string) hooks.HookEvent {
	t.Helper()
	ev, err := hooks.ParsePayload(strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestTheHookForwardsOnlyWhatAPhoneShouldHear(t *testing.T) {
	transcript := func(string) string { return "read from the transcript" }
	cases := []struct {
		name, event, payload string
		want                 *ccHookEvent
	}{
		{"stop with the text inline", "Stop", `{"session_id":"s1","cwd":"/w/superai","last_assistant_message":"done: 3 files"}`,
			&ccHookEvent{Event: "Stop", Session: "s1", Cwd: "/w/superai", Text: "done: 3 files"}},
		{"stop reads the transcript", "Stop", `{"session_id":"s1","transcript_path":"/t.jsonl"}`,
			&ccHookEvent{Event: "Stop", Session: "s1", Text: "read from the transcript"}},
		{"the payload names the event", "", `{"hook_event_name":"Stop","session_id":"s1","last_assistant_message":"x"}`,
			&ccHookEvent{Event: "Stop", Session: "s1", Text: "x"}},
		{"a permission prompt", "Notification", `{"session_id":"s1","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`,
			&ccHookEvent{Event: "Notification", Session: "s1", Kind: "permission_prompt", Text: "Claude needs your permission to use Bash"}},
		{"an idle prompt", "Notification", `{"session_id":"s1","notification_type":"idle_prompt","message":"Claude is waiting for your input"}`,
			&ccHookEvent{Event: "Notification", Session: "s1", Kind: "idle_prompt", Text: "Claude is waiting for your input"}},
		{"auth success is not news", "Notification", `{"session_id":"s1","notification_type":"auth_success","message":"ok"}`, nil},
		{"a failed turn", "StopFailure", `{"session_id":"s1","error":"rate_limit","error_details":"429 from the API","last_assistant_message":"half done"}`,
			&ccHookEvent{Event: "StopFailure", Session: "s1", Kind: "rate_limit", Text: "429 from the API\n\nhalf done"}},
		{"session start", "SessionStart", `{"session_id":"s1","cwd":"/w/x","source":"startup"}`,
			&ccHookEvent{Event: "SessionStart", Session: "s1", Cwd: "/w/x", Source: "startup"}},
		{"compaction keeps the session", "SessionStart", `{"session_id":"s1","source":"compact"}`, nil},
		{"session end", "SessionEnd", `{"session_id":"s1","reason":"prompt_input_exit"}`,
			&ccHookEvent{Event: "SessionEnd", Session: "s1", Source: "prompt_input_exit"}},
		{"a tool call says only which tool", "PreToolUse", `{"session_id":"s1","cwd":"/w/x","tool_name":"Bash","tool_input":{"command":"cat secrets.env"}}`,
			&ccHookEvent{Event: "PreToolUse", Session: "s1", Cwd: "/w/x", Tool: "Bash"}},
		{"a finished tool call too", "PostToolUse", `{"session_id":"s1","tool_name":"Edit","tool_input":{"file_path":"/w/x/a.go"},"tool_response":{"ok":true}}`,
			&ccHookEvent{Event: "PostToolUse", Session: "s1", Tool: "Edit"}},
		{"a prompt says only that a turn began", "UserPromptSubmit", `{"session_id":"s1","cwd":"/w/x","prompt":"the launch codes are 0000"}`,
			&ccHookEvent{Event: "UserPromptSubmit", Session: "s1", Cwd: "/w/x"}},
		{"subagent stops are not hooked", "SubagentStop", `{"session_id":"s1"}`, nil},
		{"no session, nothing to address", "Stop", `{"last_assistant_message":"x"}`, nil},
	}
	for _, c := range cases {
		got, ok := ccEventFromHook(c.event, hookEvent(t, c.payload), transcript)
		if c.want == nil {
			if ok {
				t.Errorf("%s: forwarded %+v", c.name, got)
			}
			continue
		}
		if !ok || got != *c.want {
			t.Errorf("%s:\n got %+v (%v)\nwant %+v", c.name, got, ok, *c.want)
		}
	}
}

func TestALongTurnKeepsItsEnd(t *testing.T) {
	long := strings.Repeat("a", maxCCText) + "THE END"
	ev, ok := ccEventFromHook("Stop", hookEvent(t, `{"session_id":"s","last_assistant_message":"`+long+`"}`), nil)
	if !ok || len([]rune(ev.Text)) != maxCCText || !strings.HasSuffix(ev.Text, "THE END") || !strings.HasPrefix(ev.Text, "…") {
		t.Fatalf("clipped to %d runes: …%s", len([]rune(ev.Text)), ev.Text[len(ev.Text)-10:])
	}
}

func TestTTYAndIdleParsing(t *testing.T) {
	for in, want := range map[string]string{"ttys003\n": "/dev/ttys003", "??": "", " - ": "", "/dev/pts/1": "/dev/pts/1", "": ""} {
		if got := normalizeTTY(in); got != want {
			t.Errorf("normalizeTTY(%q) = %q, want %q", in, got, want)
		}
	}
	out := "+-o IOHIDSystem  <class IOHIDSystem>\n    | |     \"HIDIdleTime\" = 519995983791\n"
	if got := parseHIDIdle(out); got != 519 {
		t.Errorf("parseHIDIdle = %d, want 519", got)
	}
	if got := parseHIDIdle("nothing"); got != -1 {
		t.Errorf("parseHIDIdle(nothing) = %d, want -1", got)
	}
}

// ---- the machine: delivering an answer to one session

type fakeTerminals struct {
	live     map[string]bool
	panes    map[string]bool
	tmuxErr  error
	itermErr error
	tmuxGot  []string
	itermGot []string
	otty     map[string]string // session id -> Otty pane
	ottyGot  []string
}

func testLocal(f *fakeTerminals) *ccLocal {
	l := newCCLocal()
	l.liveTTYs = func() map[string]bool { return f.live }
	l.paneAlive = func(p string) bool { return f.panes[p] }
	l.typeTmux = func(pane, text string) error {
		if f.tmuxErr != nil {
			return f.tmuxErr
		}
		f.tmuxGot = append(f.tmuxGot, pane+"|"+text)
		return nil
	}
	l.typeITerm = func(_ context.Context, tty, text string) error {
		if f.itermErr != nil {
			return f.itermErr
		}
		f.itermGot = append(f.itermGot, tty+"|"+text)
		return nil
	}
	// Never the real Otty: a test must not type into this machine's panes.
	l.ottyPane = func(_ context.Context, id string) string { return f.otty[id] }
	l.typeOtty = func(_ context.Context, pane, text string) error {
		f.ottyGot = append(f.ottyGot, pane+"|"+text)
		return nil
	}
	return l
}

func TestAnIdleOttySessionIsTypedInto(t *testing.T) {
	ctx := context.Background()
	f := &fakeTerminals{otty: map[string]string{"s-otty": "p_1"}}
	l := testLocal(f)
	see(l, ccHookEvent{Event: "SessionStart", Session: "s-otty", Cwd: "/tmp/x"})
	see(l, ccHookEvent{Event: "Stop", Session: "s-otty", Cwd: "/tmp/x"})
	d, err := l.deliver(ctx, "s-otty", "keep going")
	if err != nil || d.Via != "otty" || !d.Immediate {
		t.Fatalf("deliver = %+v, %v; want typed into otty", d, err)
	}
	if len(f.ottyGot) != 1 || f.ottyGot[0] != "p_1|keep going" {
		t.Errorf("typed %v", f.ottyGot)
	}
}

func TestOttyPaneIsFoundBySessionID(t *testing.T) {
	list := []byte(`{"ok":true,"data":[{"id":"p_a","agent_session_id":"aaa"},{"id":"p_b","agent_session_id":"bbb"},{"id":"p_c"}]}`)
	if got := ottyPaneIn(list, "bbb"); got != "p_b" {
		t.Errorf("pane for bbb = %q", got)
	}
	if got := ottyPaneIn(list, "zzz"); got != "" {
		t.Errorf("pane for an unknown session = %q, want none", got)
	}
	if got := ottyPaneIn([]byte("not json"), "aaa"); got != "" {
		t.Errorf("pane from garbage = %q", got)
	}
}

func see(l *ccLocal, ev ccHookEvent) string { return l.observe(&ev, time.Now()) }

func TestAnAnswerGoesToTheSessionItNames(t *testing.T) {
	ctx := context.Background()
	f := &fakeTerminals{live: map[string]bool{"/dev/ttys003": true, "/dev/ttys004": true}, panes: map[string]bool{"%3": true}}
	l := testLocal(f)

	if _, err := l.deliver(ctx, "nobody", "hi"); !errors.Is(err, errCCUnknown) {
		t.Errorf("unknown session: %v", err)
	}

	// In tmux: typed into the pane.
	see(l, ccHookEvent{Event: "Stop", Session: "in-tmux", TTY: "/dev/ttys009", Pane: "%3"})
	if d, err := l.deliver(ctx, "in-tmux", "go on"); err != nil || d.Via != "tmux" || !d.Immediate || f.tmuxGot[0] != "%3|go on" {
		t.Errorf("tmux: %+v %v %v", d, err, f.tmuxGot)
	}

	// In an iTerm2 tab: typed into the tab that owns its tty.
	see(l, ccHookEvent{Event: "Stop", Session: "a", TTY: "/dev/ttys003"})
	see(l, ccHookEvent{Event: "Stop", Session: "b", TTY: "/dev/ttys004"})
	if d, err := l.deliver(ctx, "a", "for a"); err != nil || d.Via != "terminal" || len(f.itermGot) != 1 || f.itermGot[0] != "/dev/ttys003|for a" {
		t.Errorf("terminal: %+v %v %v", d, err, f.itermGot)
	}

	// The tab a was in now belongs to c: refused, never typed into c's tab.
	see(l, ccHookEvent{Event: "SessionStart", Session: "c", TTY: "/dev/ttys003"})
	if _, err := l.deliver(ctx, "a", "for a again"); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("reused tty: %v", err)
	}
	// b's tab has no agent in it any more: the process is gone.
	delete(f.live, "/dev/ttys004")
	if _, err := l.deliver(ctx, "b", "for b"); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("dead tty: %v", err)
	}
	if len(f.itermGot) != 1 {
		t.Fatalf("typed into a terminal that was not the session's: %v", f.itermGot)
	}

	// Ended is said plainly.
	see(l, ccHookEvent{Event: "SessionEnd", Session: "in-tmux"})
	if _, err := l.deliver(ctx, "in-tmux", "x"); !errors.Is(err, errCCEnded) {
		t.Errorf("ended: %v", err)
	}
}

func TestAPermissionPromptIsNeverTypedInto(t *testing.T) {
	f := &fakeTerminals{live: map[string]bool{"/dev/ttys003": true}, panes: map[string]bool{"%1": true}}
	l := testLocal(f)
	see(l, ccHookEvent{Event: "Notification", Kind: "permission_prompt", Session: "p", TTY: "/dev/ttys003", Pane: "%1"})
	d, err := l.deliver(context.Background(), "p", "use the other file")
	if err != nil || d.Via != "queued" || len(f.tmuxGot)+len(f.itermGot) != 0 {
		t.Fatalf("permission prompt: %+v %v tmux=%v iterm=%v", d, err, f.tmuxGot, f.itermGot)
	}
	// The turn's Stop hands it back and says the turn goes on.
	ev := ccHookEvent{Event: "Stop", Session: "p"}
	if reply := l.observe(&ev, time.Now()); reply != "use the other file" || !ev.Resumed {
		t.Fatalf("Stop got %q resumed=%v", reply, ev.Resumed)
	}
	if reply := see(l, ccHookEvent{Event: "Stop", Session: "p"}); reply != "" {
		t.Fatalf("handed back twice: %q", reply)
	}
}

func TestUnreachableTerminalsParkOnlyMidTurn(t *testing.T) {
	f := &fakeTerminals{live: map[string]bool{"/dev/ttys003": true}, itermErr: errors.New("iTerm2 is not running")}
	l := testLocal(f)
	see(l, ccHookEvent{Event: "SessionStart", Session: "w", TTY: "/dev/ttys003"})
	if d, err := l.deliver(context.Background(), "w", "next"); err != nil || d.Via != "queued" {
		t.Errorf("mid-turn: %+v %v", d, err)
	}
	// The Stop hands it back and the turn goes on; the next Stop ends it.
	if got := see(l, ccHookEvent{Event: "Stop", Session: "w"}); got != "next" {
		t.Fatalf("Stop got %q", got)
	}
	see(l, ccHookEvent{Event: "Stop", Session: "w"})
	if _, err := l.deliver(context.Background(), "w", "later"); err == nil || !strings.Contains(err.Error(), "no turn end") {
		t.Errorf("idle and unreachable: %v", err)
	}
}

// ---- core: what becomes a notice

func TestOnlyAnAbsentPersonIsInterrupted(t *testing.T) {
	stop := ccReport{ccHookEvent: ccHookEvent{Event: "Stop", Session: "s"}}
	away := func(r ccReport) ccReport { r.Away = true; return r }
	perm := ccReport{ccHookEvent: ccHookEvent{Event: "Notification", Kind: "permission_prompt", Session: "s"}}
	idle := ccReport{ccHookEvent: ccHookEvent{Event: "Notification", Kind: "idle_prompt", Session: "s"}}
	resumed := away(stop)
	resumed.Resumed = true
	cases := []struct {
		name     string
		r        ccReport
		coreAway bool
		last     string
		want     bool
	}{
		{"at the desk", stop, false, "", false},
		{"idle Mac", away(stop), false, "", true},
		{"the phone said away", stop, true, "", true},
		{"a parked answer resumed the turn", resumed, false, "", false},
		{"permission prompt while away", away(perm), false, "", true},
		{"permission prompt at the desk", perm, false, "", false},
		{"idle prompt after a told Stop", away(idle), false, "Stop", false},
		{"idle prompt otherwise", away(idle), false, "", true},
		{"failed turn", away(ccReport{ccHookEvent: ccHookEvent{Event: "StopFailure", Session: "s"}}), false, "", true},
		{"session start is list-only", away(ccReport{ccHookEvent: ccHookEvent{Event: "SessionStart", Session: "s"}}), true, "", false},
		{"session end is list-only", away(ccReport{ccHookEvent: ccHookEvent{Event: "SessionEnd", Session: "s"}}), true, "", false},
	}
	for _, c := range cases {
		if got := ccShouldNotify(c.r, c.coreAway, c.last); got != c.want {
			t.Errorf("%s: notify = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestANoticeIsAddressedBackToItsSession(t *testing.T) {
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	r := ccReport{ccHookEvent: ccHookEvent{Event: "Stop", Session: "s-1", Cwd: "/Users/me/superai", Text: "  all green  "}, Host: "mbp"}
	n := ccNotice("mac", r, "", now)
	if n.Title != "claude · mac · superai" || n.Key != "cc:s-1" || n.Message != "all green" || !n.Push ||
		n.Reply == nil || n.Reply.Agent != "mac" || n.Reply.Session != "s-1" || n.Kind != backend.KindInfo {
		t.Errorf("stop notice %+v", n)
	}
	perm := ccNotice("mac", ccReport{ccHookEvent: ccHookEvent{Event: "Notification", Kind: "permission_prompt", Session: "s-1", Text: "needs Bash"}}, "", now)
	if perm.Kind != backend.KindReminder || perm.Message != "needs Bash" || perm.Key != "cc:s-1" {
		t.Errorf("permission notice %+v", perm)
	}
	idle := ccNotice("mac", ccReport{ccHookEvent: ccHookEvent{Event: "Notification", Kind: "idle_prompt", Session: "s-1", Text: "Claude is waiting"}}, "the last turn", now)
	if idle.Message != "the last turn" {
		t.Errorf("idle notice should carry the last turn: %q", idle.Message)
	}
	fail := ccNotice("", ccReport{ccHookEvent: ccHookEvent{Event: "StopFailure", Kind: "rate_limit", Session: "s-2"}, Host: "mbp"}, "", now)
	if fail.Title != "claude · mbp" || fail.Kind != backend.KindAlert || !strings.Contains(fail.Message, "rate_limit") || fail.Reply.Agent != "" {
		t.Errorf("failure notice %+v", fail)
	}
}

func resetCC(t *testing.T) {
	t.Helper()
	ccCoreV = &ccCore{sessions: map[string]*CCSession{}, raised: map[string]string{}}
	ccCoreOnce.Do(func() {})
	ccLocalV = newCCLocal()
	ccLocalOnce.Do(func() {})
	ccActV = newCCActivity()
	ccActOnce.Do(func() {})
}

func TestNoticesCollapsePerSessionAndHonourTheAwaySwitch(t *testing.T) {
	_, _, a, events := postServer(t)
	resetCC(t)
	report := func(away bool, ev ccHookEvent) {
		raw, _ := json.Marshal(ccReport{ccHookEvent: ev, Away: away})
		a.noteCCSession("mac", string(raw))
	}
	report(false, ccHookEvent{Event: "SessionStart", Session: "s1", Cwd: "/w/superai"})
	report(false, ccHookEvent{Event: "Stop", Session: "s1", Text: "turn one"})
	if got := a.Notifications(); len(got) != 0 {
		t.Fatalf("raised while present: %+v", got)
	}
	if s := a.CCSessions(); len(s) != 1 || s[0].Project != "superai" || s[0].State != ccFinished || s[0].Text != "turn one" {
		t.Fatalf("session list %+v", s)
	}

	report(true, ccHookEvent{Event: "Stop", Session: "s1", Text: "turn two"})
	report(true, ccHookEvent{Event: "Stop", Session: "s1", Text: "turn three"})
	got := a.Notifications()
	if len(got) != 1 || got[0].ID != "cc:s1" || got[0].Message != "turn three" || got[0].Reply == nil || got[0].Reply.Agent != "mac" {
		t.Fatalf("collapsed notices %+v", got)
	}
	var sawReply bool
	for _, e := range events.list() {
		if e["key"] == "cc:s1" && e["reply"] != nil {
			sawReply = true
		}
	}
	if !sawReply {
		t.Error("the notice event carries no reply target")
	}

	// The phone says away: a present machine's turn now pushes too.
	if _, err := a.SetAway(true); err != nil || !a.Presence().Away {
		t.Fatal(err)
	}
	report(false, ccHookEvent{Event: "Stop", Session: "s2", Cwd: "/w/anywhere", Text: "other"})
	if len(a.Notifications()) != 2 {
		t.Fatalf("away switch ignored: %+v", a.Notifications())
	}
	_, _ = a.SetAway(false)

	report(false, ccHookEvent{Event: "SessionEnd", Session: "s1"})
	if s := a.CCSessions(); len(s) != 1 || s[0].Session != "s2" {
		t.Fatalf("an ended session stayed listed: %+v", s)
	}
}

// ---- replying: who may, and that it is written down

func TestOnlyTheOwnerMayAnswerASession(t *testing.T) {
	h, c, a, _ := postServer(t)
	resetCC(t)
	minted, err := a.CreateIntegrationToken("openclaw")
	if err != nil {
		t.Fatal(err)
	}
	body := `[{"agent":"mac","session":"s1","text":"go on"}]`
	if rec := call(h, http.MethodPost, "/api/rpc/ReplyToCCSession", minted.Token, body); rec.Code != http.StatusForbidden {
		t.Errorf("integration token: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, http.MethodPost, "/api/rpc/ReplyToCCSession", "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	if credentialsCheck(c)(minted.Token) {
		t.Error("an integration token links an agent")
	}
	// The master token and a paired phone get through to the method, which
	// answers that the agent is not connected.
	code, _ := c.devices.newCode()
	_, phone, ok, err := c.devices.claim(code, "iPhone")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	for who, tok := range map[string]string{"master": c.Token, "phone": phone} {
		rec := call(h, http.MethodPost, "/api/rpc/ReplyToCCSession", tok, body)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "mac is not connected") {
			t.Errorf("%s: %d %s", who, rec.Code, rec.Body)
		}
	}
	// Core asks agents for this, never the other way round.
	if !agentCallable["ReplyToSession"] || agentCallable["ReplyToCCSession"] || agentCallable["SetAway"] {
		t.Error("agent allowlist is wrong")
	}

	// Every attempt is in the audit log, without the text.
	f, err := os.Open(filepath.Join(backend.DataDir(), "audit", "cc-replies.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lines := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines++
		var e ccAuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Session != "s1" || e.Delivered || e.Chars != 5 || strings.Contains(sc.Text(), "go on") {
			t.Errorf("audit line %s", sc.Text())
		}
	}
	if lines != 2 {
		t.Errorf("audit lines = %d, want 2", lines)
	}
}

// ---- end to end: hook → agent → core notice → phone answer → the terminal

func TestATerminalTurnReachesThePhoneAndTheAnswerComesBack(t *testing.T) {
	_, _, core, _ := postServer(t)
	resetCC(t)
	f := &fakeTerminals{live: map[string]bool{"/dev/ttys007": true}}
	ccLocalV = testLocal(f)
	old := ccIdleSeconds
	ccIdleSeconds = func() int { return 600 } // nobody at the Mac
	t.Cleanup(func() { ccIdleSeconds = old })

	// The socket lives in the data directory; a unix socket path must stay
	// short, so the home is under /tmp for this test.
	home, err := os.MkdirTemp("/tmp", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("SUPERAI_HOME", home)

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
	if st, _ := os.Stat(sock); st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v", st.Mode().Perm())
	}

	hctx, hcancel := context.WithTimeout(context.Background(), hookBudget)
	defer hcancel()
	stale := filepath.Join(home, "gone.sock")
	_ = os.WriteFile(stale, nil, 0o600)
	reply := sendCCHook(hctx, []string{stale, sock}, ccHookEvent{Event: "Stop", Session: "s9", Cwd: "/w/superai", Text: "built and tested", TTY: "/dev/ttys007"})
	if reply != "" {
		t.Fatalf("nothing was parked, got %q", reply)
	}
	waitFor(t, "the notice on core", func() bool { return len(core.Notifications()) == 1 })
	n := core.Notifications()[0]
	if n.Title != "claude · mac · superai" || n.Message != "built and tested" || n.Reply == nil || n.Reply.Agent != "mac" || n.Reply.Session != "s9" {
		t.Fatalf("notice %+v", n)
	}

	d, err := core.ReplyToCCSession(CCReplyRequest{Agent: n.Reply.Agent, Session: n.Reply.Session, Text: "now ship it"})
	if err != nil || d.Via != "terminal" || len(f.itermGot) != 1 || f.itermGot[0] != "/dev/ttys007|now ship it" {
		t.Fatalf("reply: %+v %v %v", d, err, f.itermGot)
	}

	// No terminal to type into, mid-turn: parked, and the next Stop hook gets it.
	f.itermErr = errors.New("iTerm2 is not running")
	if d, err := core.ReplyToCCSession(CCReplyRequest{Agent: "mac", Session: "s9", Text: "and tag it"}); err != nil || d.Via != "queued" {
		t.Fatalf("parked reply: %+v %v", d, err)
	}
	if reply := sendCCHook(hctx, []string{sock}, ccHookEvent{Event: "Stop", Session: "s9", Text: "shipped"}); reply != "and tag it" {
		t.Fatalf("Stop hook got %q", reply)
	}

	// A session the machine never heard of is a clear error, not a guess.
	if _, err := core.ReplyToCCSession(CCReplyRequest{Agent: "mac", Session: "nope", Text: "x"}); err == nil || !strings.Contains(err.Error(), "has not heard") {
		t.Fatalf("unknown session: %v", err)
	}
}

// The command itself: silent unless a parked answer is handed back on Stop.
func TestHookMainSpeaksOnlyToHandBackAnAnswer(t *testing.T) {
	_, _, _, _ = postServer(t)
	resetCC(t)
	ccIdleSeconds = func() int { return 0 }
	t.Cleanup(func() { ccIdleSeconds = hidIdleSeconds })
	home, err := os.MkdirTemp("/tmp", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("SUPERAI_HOME", home)
	t.Setenv("SUPERAI_HOOK_SOCKET", filepath.Join(home, ccHookSocketName))
	t.Setenv("SUPERAI_CLI_RUN", "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go NewApp().serveCCHookSocket(ctx)
	waitFor(t, "the hook socket", func() bool { _, err := os.Stat(filepath.Join(home, ccHookSocketName)); return err == nil })

	run := func(event, payload string) string {
		t.Helper()
		inR, inW, _ := os.Pipe()
		outR, outW, _ := os.Pipe()
		oldIn, oldOut := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = inR, outW
		_, _ = inW.WriteString(payload)
		inW.Close()
		HookMain([]string{event})
		os.Stdin, os.Stdout = oldIn, oldOut
		outW.Close()
		b := make([]byte, 4096)
		n, _ := outR.Read(b)
		return string(b[:n])
	}
	if out := run("SessionStart", `{"session_id":"h1","source":"startup"}`); out != "" {
		t.Fatalf("SessionStart printed %q", out)
	}
	if out := run("Stop", `{"session_id":"h1","last_assistant_message":"done"}`); out != "" {
		t.Fatalf("Stop with nothing parked printed %q", out)
	}
	ccLocalState().queue.Push("h1", "one more thing")
	out := run("Stop", `{"session_id":"h1","last_assistant_message":"done"}`)
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["decision"] != "block" || got["reason"] != "one more thing" {
		t.Fatalf("Stop with a parked answer printed %q", out)
	}
	// A run SuperAI started itself stays out of it entirely.
	ccLocalState().queue.Push("h1", "not for a cli run")
	t.Setenv("SUPERAI_CLI_RUN", "run-1")
	if out := run("Stop", `{"session_id":"h1"}`); out != "" {
		t.Fatalf("a SuperAI run's hook printed %q", out)
	}
}
