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
)

// ---- the machine: one record per session

func TestActivityFollowsASessionThroughItsTurn(t *testing.T) {
	a := newCCActivity()
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	step := func(i int, ev ccHookEvent) CCActivitySession {
		t.Helper()
		a.observe(ev, t0.Add(time.Duration(i)*time.Second))
		snap := a.snapshot(t0)
		for _, s := range snap.Sessions {
			if s.ID == ev.Session {
				return s
			}
		}
		return CCActivitySession{}
	}
	s := step(0, ccHookEvent{Event: "SessionStart", Session: "s1", Cwd: "/Users/me/code/superai/", Source: "startup"})
	if s.State != actIdle || s.Project != "superai" {
		t.Fatalf("a new session: %+v", s)
	}
	s = step(1, ccHookEvent{Event: "UserPromptSubmit", Session: "s1"})
	if s.State != actWorking || s.Tool != "" || !s.Since.Equal(t0.Add(time.Second)) {
		t.Fatalf("after a prompt: %+v", s)
	}
	step(2, ccHookEvent{Event: "PreToolUse", Session: "s1", Tool: "Read"})
	step(3, ccHookEvent{Event: "PostToolUse", Session: "s1", Tool: "Read"})
	s = step(4, ccHookEvent{Event: "PreToolUse", Session: "s1", Tool: "Bash"})
	if s.State != actWorking || s.Tool != "Bash" || s.Tools != 2 || !s.Since.Equal(t0.Add(time.Second)) {
		t.Fatalf("on its second tool: %+v", s)
	}
	s = step(5, ccHookEvent{Event: "PermissionRequest", Session: "s1", Tool: "Bash"})
	if s.State != actWaiting || s.Wait != actWaitPermission {
		t.Fatalf("at a permission prompt: %+v", s)
	}
	s = step(6, ccHookEvent{Event: "PostToolUse", Session: "s1", Tool: "Bash"})
	if s.State != actWorking || s.Wait != "" {
		t.Fatalf("approved and run: %+v", s)
	}
	s = step(7, ccHookEvent{Event: "Stop", Session: "s1"})
	if s.State != actIdle || s.Tool != "Bash" {
		t.Fatalf("after the turn: %+v", s)
	}
	s = step(8, ccHookEvent{Event: "Notification", Session: "s1", Kind: "idle_prompt"})
	if s.State != actWaiting || s.Wait != actWaitInput {
		t.Fatalf("waiting for input: %+v", s)
	}
	s = step(9, ccHookEvent{Event: "UserPromptSubmit", Session: "s1"})
	if s.State != actWorking || s.Tools != 0 || s.Tool != "" {
		t.Fatalf("a new turn starts clean: %+v", s)
	}
	// A parked answer handed back on Stop: the turn goes on.
	s = step(10, ccHookEvent{Event: "Stop", Session: "s1", Resumed: true})
	if s.State != actWorking {
		t.Fatalf("resumed: %+v", s)
	}
	step(11, ccHookEvent{Event: "UserPromptSubmit", Session: "s2", Cwd: "/w/other"})
	step(12, ccHookEvent{Event: "Notification", Session: "s3", Kind: "permission_prompt", Cwd: "/w/third"})
	snap := a.snapshot(t0)
	if snap.Working != 2 || snap.Waiting != 1 || snap.Idle != 0 || len(snap.Sessions) != 3 ||
		snap.Sessions[0].State != actWorking || snap.Sessions[2].State != actWaiting {
		t.Fatalf("three sessions: %+v", snap)
	}
	if !a.observe(ccHookEvent{Event: "SessionEnd", Session: "s1"}, t0) || len(a.snapshot(t0).Sessions) != 2 {
		t.Fatalf("an ended session stays: %+v", a.snapshot(t0))
	}
	if a.observe(ccHookEvent{Event: "SessionEnd", Session: "never"}, t0) {
		t.Fatal("ending a session never heard of changed something")
	}
}

func TestQuietSessionsSettleAndAreForgotten(t *testing.T) {
	a := newCCActivity()
	t0 := time.Now()
	a.observe(ccHookEvent{Event: "UserPromptSubmit", Session: "think", TTY: "/dev/ttys001"}, t0)
	a.observe(ccHookEvent{Event: "PreToolUse", Session: "build", Tool: "Bash", TTY: "/dev/ttys002"}, t0)
	a.observe(ccHookEvent{Event: "Notification", Session: "ask", Kind: "idle_prompt", TTY: "/dev/ttys003"}, t0)
	a.observe(ccHookEvent{Event: "PermissionRequest", Session: "perm", Tool: "Write", TTY: "/dev/ttys004"}, t0)
	state := func(now time.Time) map[string]string {
		out := map[string]string{}
		for _, s := range a.snapshot(now).Sessions {
			out[s.ID] = s.State
		}
		return out
	}
	if a.sweep(t0.Add(time.Minute), nil) {
		t.Fatal("a minute is not quiet")
	}
	if !a.sweep(t0.Add(ccActQuietThinking+time.Second), nil) {
		t.Fatal("nothing settled")
	}
	got := state(t0)
	if got["think"] != actIdle || got["build"] != actWorking || got["ask"] != actIdle || got["perm"] != actWaiting {
		t.Fatalf("after ten quiet minutes: %v", got)
	}
	a.sweep(t0.Add(ccActQuietTool+time.Second), nil)
	if got := state(t0); got["build"] != actIdle || got["perm"] != actWaiting {
		t.Fatalf("after an hour: %v", got)
	}
	// A closed terminal ends its session whatever it last said.
	a.sweep(t0.Add(ccActQuietTool+2*time.Second), map[string]bool{"/dev/ttys001": true, "/dev/ttys002": true, "/dev/ttys003": true})
	if got := state(t0); len(got) != 3 || got["perm"] != "" {
		t.Fatalf("after its terminal closed: %v", got)
	}
	a.sweep(t0.Add(ccActForget+time.Second), nil)
	if got := state(t0); len(got) != 0 {
		t.Fatalf("after three hours: %v", got)
	}
}

func TestActivitySnapshotsAreThrottled(t *testing.T) {
	a := newCCActivity()
	a.gap = 150 * time.Millisecond
	var mu sync.Mutex
	var sent []CCActivity
	a.send = func(s CCActivity) { mu.Lock(); sent = append(sent, s); mu.Unlock() }
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(sent) }

	tools := []string{"Read", "Grep", "Edit", "Bash", "Read", "Glob", "Write", "Bash"}
	start := time.Now()
	for i := 0; i < 80; i++ {
		if a.observe(ccHookEvent{Event: "PreToolUse", Session: "s1", Tool: tools[i%len(tools)]}, time.Now()) {
			a.kick()
		}
		time.Sleep(2 * time.Millisecond)
	}
	busy := time.Since(start)
	waitFor(t, "the last snapshot", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sent) > 0 && sent[len(sent)-1].Sessions[0].Tool == "Bash"
	})
	// One at once, then at most one per gap over the burst, plus the one
	// that carries its end.
	if max := int(busy/a.gap) + 2; count() > max {
		t.Fatalf("%d snapshots for %d events over %s; at most %d", count(), 80, busy, max)
	}
	// Nothing new to say: nothing said.
	n := count()
	a.kick()
	time.Sleep(3 * a.gap)
	if count() != n {
		t.Fatalf("an unchanged snapshot was sent again")
	}
	// A core that lost the link hears it again.
	a.resend()
	waitFor(t, "the resend", func() bool { return count() == n+1 })
}

func TestCoreBoundsWhatAMachineSays(t *testing.T) {
	in := CCActivity{Working: 99, Sessions: []CCActivitySession{
		{ID: "a", State: actWorking, Wait: "permission", Tool: strings.Repeat("x", 500)},
		{ID: "b", State: actWaiting, Wait: "nonsense"},
		{ID: "c", State: "exploded"},
		{ID: "", State: actIdle},
		{ID: "d", State: actIdle, Tools: -4},
	}}
	for i := 0; i < 50; i++ {
		in.Sessions = append(in.Sessions, CCActivitySession{ID: "x", State: actIdle})
	}
	out := sanitizeActivity(in)
	if len(out.Sessions) != maxActSessions || out.Working != 1 || out.Waiting != 1 || out.Idle != maxActSessions-2 {
		t.Fatalf("counted %d/%d/%d of %d", out.Working, out.Waiting, out.Idle, len(out.Sessions))
	}
	if s := out.Sessions[0]; s.Wait != "" || len([]rune(s.Tool)) != maxActField {
		t.Fatalf("working session %+v", s)
	}
	if s := out.Sessions[1]; s.Wait != actWaitInput {
		t.Fatalf("waiting session %+v", s)
	}
	for _, s := range out.Sessions {
		if s.ID == "c" || s.ID == "" || s.Tools < 0 {
			t.Fatalf("kept %+v", s)
		}
	}
}

// ---- end to end: the real hook command → the machine → core's screens

func TestTerminalSessionsShowOnCoreAndTheirPromptsDoNot(t *testing.T) {
	_, _, core, _ := postServer(t)
	resetCC(t)
	ccLocalV = testLocal(&fakeTerminals{live: map[string]bool{}})
	old := ccIdleSeconds
	ccIdleSeconds = func() int { return 0 } // somebody at the Mac: no notices
	t.Cleanup(func() { ccIdleSeconds = old })
	home, err := os.MkdirTemp("/tmp", "cca")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("SUPERAI_HOME", home)
	t.Setenv("SUPERAI_HOOK_SOCKET", filepath.Join(home, ccHookSocketName))
	t.Setenv("SUPERAI_CLI_RUN", "")

	// Everything core tells its screens, to look for the secrets in.
	var mu sync.Mutex
	var told []string
	core.taps.Store("test-tap", func(name string, payload map[string]any) {
		b, _ := json.Marshal(payload)
		mu.Lock()
		told = append(told, name+" "+string(b))
		mu.Unlock()
	})

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
	waitFor(t, "the hook socket", func() bool { _, err := os.Stat(filepath.Join(home, ccHookSocketName)); return err == nil })

	hook := func(event, payload string) string {
		t.Helper()
		inR, inW, _ := os.Pipe()
		outR, outW, _ := os.Pipe()
		oldIn, oldOut := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = inR, outW
		_, _ = inW.WriteString(payload)
		inW.Close()
		start := time.Now()
		HookMain([]string{event})
		if d := time.Since(start); ccActivityOnly[event] && d > ccActivityBudget {
			t.Errorf("%s hook took %s", event, d)
		}
		os.Stdin, os.Stdout = oldIn, oldOut
		outW.Close()
		b := make([]byte, 4096)
		n, _ := outR.Read(b)
		return string(b[:n])
	}
	activity := func() *CCActivity {
		for _, la := range core.LinkedAgents() {
			if la.Name == "mac" {
				return la.Activity
			}
		}
		return nil
	}

	hook("SessionStart", `{"session_id":"t1","cwd":"/Users/me/code/superai","source":"startup"}`)
	for _, ev := range []struct{ name, payload string }{
		{"UserPromptSubmit", `{"session_id":"t1","cwd":"/Users/me/code/superai","prompt":"PROMPT-SECRET deploy it"}`},
		{"PreToolUse", `{"session_id":"t1","cwd":"/Users/me/code/superai","tool_name":"Bash","tool_input":{"command":"echo ARG-SECRET"}}`},
		{"PostToolUse", `{"session_id":"t1","tool_name":"Bash","tool_input":{"command":"echo ARG-SECRET"},"tool_response":{"stdout":"OUTPUT-SECRET"}}`},
		{"PreToolUse", `{"session_id":"t1","tool_name":"Edit","tool_input":{"file_path":"/Users/me/code/superai/FILE-SECRET.go","new_string":"CONTENT-SECRET"}}`},
		{"UserPromptSubmit", `{"session_id":"t2","cwd":"/Users/me/code/noted","prompt":"PROMPT-SECRET two"}`},
	} {
		if out := hook(ev.name, ev.payload); out != "" {
			t.Fatalf("%s printed %q", ev.name, out)
		}
	}
	waitFor(t, "core to see both sessions working", func() bool {
		a := activity()
		return a != nil && a.Working == 2 && a.Sessions[0].Project != "" && (a.Sessions[0].Tool == "Edit" || a.Sessions[1].Tool == "Edit")
	})
	a := activity()
	byID := map[string]CCActivitySession{}
	for _, s := range a.Sessions {
		byID[s.ID] = s
	}
	if s := byID["t1"]; s.Project != "superai" || s.Tool != "Edit" || s.Tools != 2 {
		t.Fatalf("t1 on core: %+v", s)
	}
	if s := byID["t2"]; s.Project != "noted" || s.State != actWorking {
		t.Fatalf("t2 on core: %+v", s)
	}
	if hp := core.HivePulse(); hp.Total.CCWorking != 2 {
		t.Errorf("hive meter: %d working", hp.Total.CCWorking)
	}
	if st := core.HiveStatus(); st["cc_activity"].(map[string]CCActivity)["mac"].Working != 2 {
		t.Errorf("hive status: %+v", st["cc_activity"])
	}

	// Done and asked for the next prompt: back to idle and waiting.
	hook("Stop", `{"session_id":"t1","last_assistant_message":"done"}`)
	hook("Notification", `{"session_id":"t2","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`)
	waitFor(t, "core to see t1 idle and t2 waiting", func() bool {
		a := activity()
		return a != nil && a.Working == 0 && a.Waiting == 1 && a.Idle == 1
	})
	hook("SessionEnd", `{"session_id":"t1","reason":"prompt_input_exit"}`)
	hook("SessionEnd", `{"session_id":"t2","reason":"prompt_input_exit"}`)
	waitFor(t, "the sessions to go", func() bool { return activity() == nil })

	// Nothing a prompt, a tool's arguments or its output said went anywhere.
	mu.Lock()
	defer mu.Unlock()
	b, _ := json.Marshal(map[string]any{"linked": core.LinkedAgentsAll(), "sessions": core.CCSessions(), "told": told})
	for _, secret := range []string{"PROMPT-SECRET", "ARG-SECRET", "OUTPUT-SECRET", "FILE-SECRET", "CONTENT-SECRET"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("%s reached core", secret)
		}
	}
	saw := false
	for _, l := range told {
		saw = saw || strings.HasPrefix(l, ccActivityEvent+" ")
	}
	if !saw {
		t.Error("core never told its screens about the activity")
	}
}
