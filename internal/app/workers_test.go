package app

import (
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
