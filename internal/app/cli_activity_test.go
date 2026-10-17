package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
)

// resetCLI gives the test a fresh CLI tracker.
func resetCLI(t *testing.T) {
	t.Helper()
	cliTrackOnce.Do(func() {})
	cliTrackV = newCLITracker()
	cliTrackV.ccHasSessions = func() bool {
		t := ccActivityState()
		t.mu.Lock()
		defer t.mu.Unlock()
		return len(t.sessions) > 0
	}
}

func TestAPluginSpeaksForItsCLIAndProcessesForTheRest(t *testing.T) {
	tr := newCLITracker()
	has := false
	tr.ccHasSessions = func() bool { return has }
	now := time.Now()
	procs := map[string][]CCActivitySession{
		"opencode": {{ID: "opencode-500", Project: "web", State: actIdle, Since: now, Started: now, Last: now}},
		"gemini":   {{ID: "gemini-600", Project: "api", State: actWorking, Since: now, Started: now, Last: now}},
		"claude":   {{ID: "claude-700", Project: "x", State: actIdle, Since: now, Started: now, Last: now}},
	}
	if ch := tr.setProcs(procs); len(ch) != 3 {
		t.Fatalf("changed %v", ch)
	}
	if a := tr.current("opencode", now); a == nil || a.Source != cliSourceProcess || a.Idle != 1 {
		t.Fatalf("opencode by process %+v", a)
	}
	// opencode's plugin speaks: its sessions, not its processes.
	for _, ev := range []ccHookEvent{
		{CLI: "opencode", Event: "SessionStart", Session: "ses_1", Cwd: "web"},
		{CLI: "opencode", Event: "UserPromptSubmit", Session: "ses_1"},
		{CLI: "opencode", Event: "PreToolUse", Session: "ses_1", Tool: "bash"},
		{CLI: "OpenCode", Event: "PostToolUse", Session: "ses_2", Cwd: "docs"},
	} {
		tr.observe(ev, now)
	}
	a := tr.current("opencode", now)
	if a == nil || a.Source != cliSourcePlugin || a.Working != 2 || a.Sessions[0].Tool == "" && a.Sessions[1].Tool == "" {
		t.Fatalf("opencode by plugin %+v", a)
	}
	tr.observe(ccHookEvent{CLI: "opencode", Event: "PermissionRequest", Session: "ses_1", Tool: "edit"}, now)
	tr.observe(ccHookEvent{CLI: "opencode", Event: "WaitInput", Session: "ses_2"}, now)
	if a := tr.current("opencode", now); a.Waiting != 2 || a.Sessions[0].Wait == a.Sessions[1].Wait {
		t.Fatalf("waiting %+v", a)
	}
	tr.observe(ccHookEvent{CLI: "opencode", Event: "PermissionAnswered", Session: "ses_1"}, now)
	tr.observe(ccHookEvent{CLI: "opencode", Event: "Stop", Session: "ses_2"}, now)
	if a := tr.current("opencode", now); a.Working != 1 || a.Idle != 1 {
		t.Fatalf("answered %+v", a)
	}
	// claude's processes are shown only while its hooks have nothing.
	if a := tr.current("claude", now); a == nil || a.Source != cliSourceProcess {
		t.Fatalf("claude by process %+v", a)
	}
	has = true
	if a := tr.current("claude", now); a != nil {
		t.Fatalf("claude doubled %+v", a)
	}
	// Claude Code's own events never land here.
	if _, ok := tr.observe(ccHookEvent{CLI: "claude", Event: "Stop", Session: "c"}, now); ok {
		t.Fatal("claude event taken")
	}
	// opencode exits without a word: its plugin sessions go with it.
	delete(procs, "opencode")
	ch := tr.setProcs(procs)
	if strings.Join(ch, ",") != "opencode" || tr.current("opencode", now) != nil {
		t.Fatalf("after exit: changed %v, %+v", ch, tr.current("opencode", now))
	}
}

func TestOneSnapshotPerCLIASecond(t *testing.T) {
	tr := newCLITracker()
	tr.gap = 150 * time.Millisecond
	var mu sync.Mutex
	got := map[string][]*CLIActivity{}
	tr.send = func(cli string, a *CLIActivity) { mu.Lock(); got[cli] = append(got[cli], a); mu.Unlock() }
	count := func(cli string) int { mu.Lock(); defer mu.Unlock(); return len(got[cli]) }
	for i := 0; i < 20; i++ {
		ev := ccHookEvent{CLI: "pi", Event: "PreToolUse", Session: "p1", Tool: "bash"}
		if i%2 == 1 {
			ev.Event = "PostToolUse"
		}
		if c, ok := tr.observe(ev, time.Now()); ok {
			tr.kick(c)
		}
		tr.observe(ccHookEvent{CLI: "codex", Event: "UserPromptSubmit", Session: "x"}, time.Now())
		tr.kick("codex")
	}
	waitFor(t, "both snapshots", func() bool { return count("pi") >= 1 && count("codex") >= 1 })
	time.Sleep(400 * time.Millisecond)
	if count("pi") > 2 || count("codex") != 1 {
		t.Fatalf("pi %d, codex %d snapshots", count("pi"), count("codex"))
	}
	// Nothing new: nothing sent; gone: one nil.
	tr.kick("codex")
	time.Sleep(200 * time.Millisecond)
	tr.observe(ccHookEvent{CLI: "codex", Event: "SessionEnd", Session: "x"}, time.Now())
	tr.kick("codex")
	waitFor(t, "codex gone", func() bool { mu.Lock(); defer mu.Unlock(); l := got["codex"]; return len(l) == 2 && l[1] == nil })
}

// The Mac's opencode, codex and gemini reach core's LinkedAgents, HiveStatus
// and screens beside its Claude Code, by plugin, by codex's hooks and by
// process; what their prompts and tool arguments said does not.
func TestOtherCLIsShowOnCoreAndTheirPromptsDoNot(t *testing.T) {
	_, _, core, _ := postServer(t)
	resetCC(t)
	resetCLI(t)
	ccLocalV = testLocal(&fakeTerminals{live: map[string]bool{}})
	old := ccIdleSeconds
	ccIdleSeconds = func() int { return 0 }
	t.Cleanup(func() { ccIdleSeconds = old })
	home, err := os.MkdirTemp("/tmp", "cli")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("SUPERAI_HOME", home)
	t.Setenv("SUPERAI_HOOK_SOCKET", filepath.Join(home, ccHookSocketName))
	t.Setenv("SUPERAI_CLI_RUN", "")

	var mu sync.Mutex
	var told []string
	core.taps.Store("test-tap", func(name string, payload map[string]any) {
		b, _ := json.Marshal(payload)
		mu.Lock()
		told = append(told, name+" "+string(b))
		mu.Unlock()
	})

	// The machine's processes, as ps would list them; a test changes them.
	var psMu sync.Mutex
	ps := "  500     1   0.0 10:00   0:01.00 opencode\n" +
		"  600     1  80.0 10:00   5:00.00 node /Users/me/.local/bin/gemini\n" +
		"  610     1   0.0 10:00   0:01.00 node /Users/me/.local/bin/codex\n"
	setPS := func(s string) { psMu.Lock(); ps = s; psMu.Unlock() }

	addr := startCore(t, core)
	agent := NewApp()
	agent.cliPoll = &cliPollConfig{every: 50 * time.Millisecond, ps: func(context.Context) ([]byte, error) {
		psMu.Lock()
		defer psMu.Unlock()
		return []byte(ps), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "mac", Version: "test"})
		close(done)
	}()
	go agent.serveCCHookSocket(ctx)
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the agent to link", func() bool { return core.agents().get("mac") != nil && agent.agentTap.Load() != nil })
	waitFor(t, "the hook socket", func() bool { _, err := os.Stat(filepath.Join(home, ccHookSocketName)); return err == nil })

	mac := func() LinkedAgent {
		for _, la := range core.LinkedAgents() {
			if la.Name == "mac" {
				return la
			}
		}
		return LinkedAgent{}
	}
	// By process: gemini busy, opencode and codex idle.
	waitFor(t, "the processes", func() bool {
		m := mac().CLIActivity
		return m["gemini"].Working == 1 && m["opencode"].Idle == 1 && m["codex"].Idle == 1 && m["gemini"].Source == cliSourceProcess
	})

	// opencode's plugin, as it posts to the socket.
	plugin := func(ev ccHookEvent) {
		ev.CLI = "opencode"
		var out struct{}
		if !postCCHook(context.Background(), ccHookSockets(), ccHookPath, ev, &out) {
			t.Fatalf("socket refused %+v", ev)
		}
	}
	plugin(ccHookEvent{Event: "SessionStart", Session: "ses_a", Cwd: "superai"})
	plugin(ccHookEvent{Event: "UserPromptSubmit", Session: "ses_a", Cwd: "superai"})
	plugin(ccHookEvent{Event: "PreToolUse", Session: "ses_a", Tool: "bash"})
	// codex's hooks, through `superai hook --cli codex`.
	hook := func(argv []string, payload string) {
		t.Helper()
		inR, inW, _ := os.Pipe()
		outR, outW, _ := os.Pipe()
		oldIn, oldOut := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = inR, outW
		_, _ = inW.WriteString(payload)
		inW.Close()
		HookMain(argv)
		os.Stdin, os.Stdout = oldIn, oldOut
		outW.Close()
		b := make([]byte, 4096)
		if n, _ := outR.Read(b); n > 0 {
			t.Fatalf("%v printed %q", argv, b[:n])
		}
	}
	hook([]string{"--cli", "codex", "UserPromptSubmit"}, `{"session_id":"cx1","turn_id":"t","cwd":"/Users/me/code/noted","hook_event_name":"UserPromptSubmit","prompt":"PROMPT-SECRET ship"}`)
	hook([]string{"--cli", "codex", "PreToolUse"}, `{"session_id":"cx1","turn_id":"t","cwd":"/Users/me/code/noted","hook_event_name":"PreToolUse","tool_name":"exec_command","tool_input":{"cmd":"echo ARG-SECRET"}}`)
	// And Claude Code's own hooks, for claude's entry.
	hook([]string{"UserPromptSubmit"}, `{"session_id":"cc1","cwd":"/Users/me/code/superai","prompt":"PROMPT-SECRET two"}`)

	waitFor(t, "the plugins on core", func() bool {
		m := mac().CLIActivity
		oc, cx, cl := m["opencode"], m["codex"], m["claude"]
		return oc.Source == cliSourcePlugin && oc.Working == 1 && len(oc.Sessions) == 1 && oc.Sessions[0].Tool == "bash" &&
			cx.Source == cliSourcePlugin && cx.Working == 1 && len(cx.Sessions) == 1 && cx.Sessions[0].Tool == "exec_command" &&
			cl.Source == cliSourcePlugin && cl.Working == 1
	})
	la := mac()
	if s := la.CLIActivity["opencode"].Sessions[0]; s.ID != "ses_a" || s.Project != "superai" {
		t.Fatalf("opencode session %+v", s)
	}
	if s := la.CLIActivity["codex"].Sessions[0]; s.Project != "noted" {
		t.Fatalf("codex session %+v", s)
	}
	if la.Activity == nil || la.Activity.Working != 1 {
		t.Fatalf("claude's activity %+v", la.Activity)
	}
	st := core.HiveStatus()
	if all := st["cli_activity"].(map[string]cliMap); all["mac"]["opencode"].Working != 1 || all["mac"]["gemini"].Working != 1 {
		t.Fatalf("hive status %+v", all)
	}
	if h, _ := core.MachineStatus("mac"); h.CLIActivity["opencode"].Working != 1 {
		// MachineStatus asks the agent for its host, which the test agent
		// answers; the activity is core's own.
		t.Fatalf("machine status %+v", h.CLIActivity)
	}

	// The turn ends: opencode idle. Then it exits: gone.
	plugin(ccHookEvent{Event: "PostToolUse", Session: "ses_a", Tool: "bash"})
	plugin(ccHookEvent{Event: "Stop", Session: "ses_a"})
	waitFor(t, "opencode idle", func() bool { oc := mac().CLIActivity["opencode"]; return oc.Idle == 1 && oc.Working == 0 })
	setPS("  600     1  80.0 10:00   5:00.00 node /Users/me/.local/bin/gemini\n" +
		"  610     1   0.0 10:00   0:01.00 node /Users/me/.local/bin/codex\n")
	waitFor(t, "opencode gone", func() bool { _, ok := mac().CLIActivity["opencode"]; return !ok })

	mu.Lock()
	defer mu.Unlock()
	b, _ := json.Marshal(map[string]any{"linked": core.LinkedAgentsAll(), "hive": st, "told": told})
	for _, secret := range []string{"PROMPT-SECRET", "ARG-SECRET", "/Users/me"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("%s reached core", secret)
		}
	}
	saw := map[string]bool{}
	for _, l := range told {
		if strings.HasPrefix(l, cliActivityEvent+" ") {
			var p struct{ Agent, CLI string }
			_ = json.Unmarshal([]byte(strings.TrimPrefix(l, cliActivityEvent+" ")), &p)
			saw[p.Agent+"/"+p.CLI] = true
		}
	}
	for _, want := range []string{"mac/opencode", "mac/codex", "mac/gemini", "mac/claude"} {
		if !saw[want] {
			t.Errorf("core never told the screens %s (told %v)", want, saw)
		}
	}
}

// A relay's agents and what runs there are the hive's to show, though the
// relay is not a machine.
func TestRelaysCarryTheirAgentsAndCLIs(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{AgentLink: backend.AgentLinkSettings{RelayOnly: []string{"sds-e"}}}
	addr := startCore(t, core)
	stream := dialAs(t, addr, "tok")
	h := &agentlinkpb.Hello{Name: "sds-e", Host: "sds-e", Os: "linux", Clis: []string{"pi"},
		Agents: []*agentlinkpb.NamedAgent{{Name: "openclaw", About: "the cluster's agent"}, {Name: "hermes"}, {Name: "pi"}}}
	if err := stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: h}}); err != nil {
		t.Fatal(err)
	}
	if f, err := stream.Recv(); err != nil || f.GetWelcome() == nil {
		t.Fatalf("welcome: %v %v", f, err)
	}
	now := time.Now()
	send := func(cli string, act *CLIActivity) {
		raw, _ := json.Marshal(cliActivityMsg{CLI: cli, Activity: act})
		if err := stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Event{Event: &agentlinkpb.Event{Name: cliActivityEvent, PayloadJson: string(raw)}}}); err != nil {
			t.Fatal(err)
		}
	}
	send("openclaw", &CLIActivity{Source: cliSourceProcess, CCActivity: CCActivity{At: now, Sessions: []CCActivitySession{
		{ID: "openclaw-812", Project: "openclaw", State: actWorking, Since: now, Started: now, Last: now}}}})
	send("hermes", &CLIActivity{Source: "nonsense", CCActivity: CCActivity{At: now, Sessions: []CCActivitySession{
		{ID: "hermes-900", State: actIdle, Since: now, Started: now, Last: now}, {ID: "", State: actIdle}, {ID: "bad", State: "dancing"}}}})
	send("Not A CLI!", &CLIActivity{CCActivity: CCActivity{Sessions: []CCActivitySession{{ID: "x", State: actIdle}}}})

	relays := func() []RelayAgent { return core.HiveStatus()["relays"].([]RelayAgent) }
	waitFor(t, "the relay's CLIs", func() bool {
		r := relays()
		return len(r) == 1 && r[0].CLIActivity["openclaw"].Working == 1 && r[0].CLIActivity["hermes"].Idle == 1
	})
	r := relays()[0]
	if r.Name != "sds-e" || len(r.Agents) != 3 || r.Agents[0]["name"] != "openclaw" || r.Agents[0]["about"] != "the cluster's agent" {
		t.Fatalf("relay %+v", r)
	}
	if hm := r.CLIActivity["hermes"]; hm.Source != cliSourcePlugin || len(hm.Sessions) != 1 || len(r.CLIActivity) != 2 {
		t.Fatalf("not sanitized: %+v", r.CLIActivity)
	}
	if len(core.LinkedAgents()) != 0 {
		t.Fatalf("a relay among the machines: %+v", core.LinkedAgents())
	}
	if all := core.CLIActivityAll(); all["sds-e"]["openclaw"].Working != 1 {
		t.Fatalf("cli_activity %+v", all)
	}
	// Its snapshot gone: gone from the relay.
	send("openclaw", nil)
	waitFor(t, "openclaw gone", func() bool { _, ok := relays()[0].CLIActivity["openclaw"]; return !ok })
	b, _ := json.Marshal(relays())
	if !strings.Contains(string(b), `"cliActivity":{"hermes":{"working":0,"waiting":0,"idle":1,"sessions":[`) {
		t.Fatalf("relay json %s", b)
	}
}
