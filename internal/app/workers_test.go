package app

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
)

// Every agent that can work is a worker, peers whatever they are: a SuperAI
// with a model, a coding CLI on a machine. A SuperAI with no model is not one,
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
	if got := strings.Join(names, " "); got != "claude.orange1/cli@orange1 mac/superai@mac" {
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
