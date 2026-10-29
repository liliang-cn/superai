package app

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
)

// Every agent that can work is a worker, peers whatever they are: a SuperLeo
// with a model, a coding CLI on a machine. A SuperLeo with no model is not one,
// though what it runs is; the queen hands work out and is not counted.
func TestEveryAgentThatCanWorkIsAWorker(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "queen"}}
	addr := startCore(t, core)
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "mac", Host: "mbp", Os: "darwin", Kind: "mac", Role: agentRoleWorker})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "orange1", Host: "orange1", Os: "linux", Kind: "vm", Clis: []string{"claude"}})

	var names []string
	for _, w := range core.HiveWorkers() {
		names = append(names, w.Name+"/"+w.Kind+"@"+w.Host)
		if !w.Up {
			t.Errorf("%s is not up", w.Name)
		}
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "claude.orange1/cli@orange1 mac/superleo@mac" {
		t.Fatalf("workers = %s", got)
	}
}

// A CLI that says it is signed out is known to be, after its answer; until
// then, and for a CLI with no way to ask, it counts as usable.
func TestASignedOutCLIIsKnownAfterItsAnswer(t *testing.T) {
	var asked atomic.Int32
	c := &signInCache{m: map[string]signInEntry{}, fresh: time.Hour, checks: map[string]func(string) bool{
		"claude": func(string) bool { asked.Add(1); return false },
	}}
	if c.signedOut("claude", "/bin/claude") {
		t.Fatal("signed out before anyone asked")
	}
	waitFor(t, "the answer", func() bool { return c.signedOut("claude", "/bin/claude") })
	if c.signedOut("goose", "/bin/goose") {
		t.Fatal("a CLI that cannot say counts as signed out")
	}
	c.signedOut("claude", "/bin/claude")
	if n := asked.Load(); n != 1 {
		t.Fatalf("asked %d times within the hour, want 1", n)
	}
}

// A machine whose agent has a model of its own is a worker: core counts it,
// and the queen hands it a piece of work over the link, as she would a hive
// worker over HTTP. The same agent without a model is only her hands there.
func TestAnAgentWithAModelTakesWorkOverItsLink(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}
	addr := startCore(t, core)
	box := NewApp()
	box.ownTurnFn = func(requestID, session, message, standIn string) {
		box.emit("chat:done", map[string]any{"requestId": requestID, "final": "did: " + message})
	}
	keyless := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	go func() {
		box.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "box", Version: "test"})
		done <- struct{}{}
	}()
	go func() {
		keyless.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "orange1", Version: "test"})
		done <- struct{}{}
	}()
	t.Cleanup(func() { cancel(); <-done; <-done })
	waitFor(t, "both to link", func() bool { return core.agents().get("box") != nil && core.agents().get("orange1") != nil })

	if r := core.agentRole(core.agents().get("box")); r != agentRoleWorker {
		t.Fatalf("an agent with a model is a %s", r)
	}
	if r := core.agentRole(core.agents().get("orange1")); r != agentRoleMachine {
		t.Fatalf("an agent with no model is a %s", r)
	}
	if c := core.delegateCandidates(nil); !slices.Contains(c, "box") || slices.Contains(c, "orange1") {
		t.Fatalf("who the queen can hand work to: %v", c)
	}
	var names []string
	for _, w := range core.HiveWorkers() {
		names = append(names, w.Name)
	}
	if !slices.Contains(names, "box") || slices.Contains(names, "orange1") {
		t.Fatalf("workers: %v", names)
	}

	res, err := core.remoteRunner().Run(context.Background(), "box", "count the disks")
	if err != nil || res.Failed || res.Text != "did: count the disks" {
		t.Fatalf("work over the link: %+v %v", res, err)
	}
	// Asked to think, the one with no model says it cannot.
	if err := core.agents().get("orange1").call(context.Background(), "SendChat", []any{"s", "hi", []string{}}, nil); err == nil || !strings.Contains(err.Error(), "no model") {
		t.Fatalf("a keyless agent asked to work: %v", err)
	}
}

// Every kind of worker takes a piece of work the same way: the queen pins
// one to openclaw, which a linked machine runs, and one to this machine's
// Claude Code, which does it as a coding run. Both come back as reports and
// go on the task board; a CLI takes one piece at a time.
func TestEveryKindOfWorkerTakesAPiece(t *testing.T) {
	bin, _ := fakeClaude(t, fakeClaudeTurn, 0)
	core := NewApp()
	core.settings = &backend.Settings{WorkspaceDir: t.TempDir(), Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"},
		ExternalAgents: backend.ExternalAgents{Enabled: true, Unattended: true, Binaries: map[string]string{"claude": bin}}}
	addr := startCore(t, core)
	stream := dialAs(t, addr, "tok")
	hello := &agentlinkpb.Hello{Name: "box", Agents: []*agentlinkpb.NamedAgent{{Name: "openclaw", About: "the gateway"}}}
	if err := stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: hello}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			f, err := stream.Recv()
			if err != nil {
				return
			}
			c := f.GetCall()
			if c == nil {
				continue
			}
			out := `{"ok":true}`
			if c.GetMethod() == "AskRemoteAgentAs" { // an agent from before it: core asks the old way
				_ = stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Reply{Reply: &agentlinkpb.Reply{Id: c.GetId(),
					Error: "AskRemoteAgentAs is not something core can ask an agent to do"}}})
				continue
			}
			if c.GetMethod() == "AskRemoteAgent" {
				var args []string
				_ = json.Unmarshal([]byte(c.GetArgsJson()), &args)
				b, _ := json.Marshal(map[string]any{"agent": args[0], "text": "openclaw did: " + args[1]})
				out = string(b)
			}
			_ = stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Reply{Reply: &agentlinkpb.Reply{Id: c.GetId(), ResultJson: out}}})
		}
	}()
	waitFor(t, "box to link", func() bool { return core.agents().get("box") != nil })

	cands := core.delegateCandidates(nil)
	if !slices.Contains(cands, "openclaw") || !slices.Contains(cands, "claude.q") {
		t.Fatalf("workers the queen can hand work to: %v", cands)
	}
	results, err := core.delegate(context.Background(), []backend.DelegateTask{
		{Task: "check the gateway", Worker: "openclaw"},
		{Task: "count the members", Worker: "claude.q"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := results[0]; r.Worker != "openclaw" || r.Fallback || r.Failed || !strings.Contains(r.Text, "openclaw did:") {
		t.Fatalf("openclaw's piece: %+v", r)
	}
	if r := results[1]; r.Worker != "claude.q" || r.Fallback || r.Failed || !strings.Contains(r.Text, "两个成员") {
		t.Fatalf("Claude Code's piece: %+v", r)
	}
	done := map[string]string{}
	for _, task := range core.tasks().Recent() {
		done[task.Worker] = task.State
	}
	if done["openclaw"] != backend.TaskDone || done["claude.q"] != backend.TaskDone {
		t.Fatalf("on the board: %v", done)
	}

	// With a run of its own going, Claude Code takes no more.
	st := core.cliStore()
	st.mu.Lock()
	st.runs["busy"] = &CLIRun{ID: "busy", Agent: "claude", State: "running"}
	st.mu.Unlock()
	if c := core.delegateCandidates(nil); slices.Contains(c, "claude.q") || !slices.Contains(c, "openclaw") {
		t.Fatalf("a busy CLI is offered: %v", c)
	}
}
