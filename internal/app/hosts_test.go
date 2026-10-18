package app

import (
	"testing"

	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
)

func hostByName(hs []Host, name string) *Host {
	for i := range hs {
		if hs[i].Name == name {
			return &hs[i]
		}
	}
	return nil
}

func agentNamed(x *Host, name string) *HostAgent {
	if x == nil {
		return nil
	}
	for i := range x.Agents {
		if x.Agents[i].Name == name {
			return &x.Agents[i]
		}
	}
	return nil
}

// The hive as it runs today: a queen pod, worker pods on the orange VMs (one
// of them also on the agent link), a VM's own superai agent, a Mac with its
// coding CLIs, and a relay serving openclaw. Hosts puts each on the box it
// runs on, each box once, and every agent on it.
func TestHostsPutsAgentsOnTheirHosts(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{
		Hive:      backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "queen"},
		AgentLink: backend.AgentLinkSettings{RelayOnly: []string{"sds-e"}},
		RemoteAgents: backend.RemoteAgents{Enabled: true, Agents: map[string]backend.RemoteAgent{
			// Served by the relay already: not listed twice.
			"openclaw": {About: "the gateway", Hosts: []string{"sds@192.168.123.212"}, Command: []string{"openclaw", "{prompt}"}},
			// Known only from the settings.
			"pi": {About: "pi", Hosts: []string{"sds@192.168.123.99"}, Command: []string{"pi", "{prompt}"}},
		}},
	}
	core.hive = backend.NewHive("queen", 0)
	for _, w := range []backend.HiveHello{
		{Name: "superai-worker-0", Node: "orange2", Kind: "pod", Host: "superai-worker-0", MCP: []string{"noted", "cortexdb"}},
		// From before kinds: placed on its node all the same.
		{Name: "superai-worker-1", Node: "orange1"},
	} {
		w.Protocol, w.Role, w.URL = backend.HiveProtocol, backend.HiveRoleWorker, "http://"+w.Name+":1"
		if _, err := core.hive.Join(w); err != nil {
			t.Fatal(err)
		}
	}
	addr := startCore(t, core)
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "mac", Host: "Liangs-MacBook-Pro", Os: "darwin", Arch: "arm64",
		Kind: "mac", Clis: []string{"claude", "codex"}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "orange1", Host: "orange1", Os: "linux", Arch: "amd64", Kind: "vm"})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "superai-worker-0", Host: "superai-worker-0", Role: "worker",
		Kind: "pod", Parent: "orange2", Clis: []string{"claude"}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "sds-e", Host: "node-e", Os: "linux", Kind: "vm",
		Agents: []*agentlinkpb.NamedAgent{{Name: "openclaw", About: "the gateway"}, {Name: "hermes"}}})

	hs := core.Hosts()
	seen := map[string]bool{}
	for _, h := range hs {
		if seen[h.ID] {
			t.Fatalf("host %s listed twice", h.ID)
		}
		seen[h.ID] = true
	}

	mac := hostByName(hs, "mac")
	if mac == nil || mac.Kind != "mac" || mac.Hostname != "Liangs-MacBook-Pro" || mac.State != "live" {
		t.Fatalf("mac: %+v", mac)
	}
	if g := agentNamed(mac, "mac"); g == nil || g.Kind != hostAgentSuperAI || g.Role != agentRoleMachine {
		t.Fatalf("mac's own agent: %+v", g)
	}
	if g := agentNamed(mac, "codex.mac"); g == nil || g.Kind != hostAgentCLI || g.State != "installed" || g.Via != "mac" {
		t.Fatalf("codex on the mac: %+v", g)
	}

	// The worker on the roster and on the link is one pod with one agent,
	// inside its VM, which is listed before it.
	w0 := hostByName(hs, "superai-worker-0")
	if w0 == nil || w0.Kind != "pod" || w0.Parent != "orange2" {
		t.Fatalf("worker-0's pod: %+v", w0)
	}
	n := 0
	for _, g := range w0.Agents {
		if g.Kind == hostAgentSuperAI {
			n++
			if g.Role != backend.HiveRoleWorker || len(g.MCP) != 2 {
				t.Fatalf("worker-0's agent: %+v", g)
			}
		}
	}
	if n != 1 || agentNamed(w0, "claude.superai-worker-0") == nil {
		t.Fatalf("worker-0's agents: %+v", w0.Agents)
	}
	w1 := hostByName(hs, "superai-worker-1")
	if w1 == nil || w1.Kind != "pod" || w1.Parent != "orange1" {
		t.Fatalf("worker-1's pod: %+v", w1)
	}
	if o1 := hostByName(hs, "orange1"); o1 == nil || o1.Kind != "vm" || o1.State != "live" {
		t.Fatalf("orange1: %+v", o1)
	}
	// orange2's agent is not linked: it stands in, so the pod has somewhere to be.
	o2 := hostByName(hs, "orange2")
	if o2 == nil || o2.State != "configured" {
		t.Fatalf("orange2: %+v", o2)
	}
	index := map[string]int{}
	for i, h := range hs {
		index[h.ID] = i
	}
	if index["orange2"] > index["superai-worker-0"] || index["orange1"] > index["superai-worker-1"] {
		t.Fatalf("parents after children: %v", index)
	}

	// The relay's box carries what it serves; pi is only in the settings.
	relay := hostByName(hs, "node-e")
	if g := agentNamed(relay, "openclaw"); g == nil || g.Kind != hostAgentNamed || g.Via != "sds-e" {
		t.Fatalf("openclaw: %+v in %+v", g, relay)
	}
	if g := agentNamed(relay, "sds-e"); g == nil || g.Role != agentRoleRelay {
		t.Fatalf("the relay itself: %+v", g)
	}
	if h := hostByName(hs, "192.168.123.212"); h != nil {
		t.Fatalf("openclaw listed again from the settings: %+v", h)
	}
	pi := hostByName(hs, "192.168.123.99")
	if g := agentNamed(pi, "pi"); pi == nil || pi.State != "configured" || g == nil || g.State != "configured" {
		t.Fatalf("pi from the settings: %+v", pi)
	}

	// And the core itself is on the list, as the queen.
	found := false
	for _, h := range hs {
		if g := agentNamed(&h, "queen"); g != nil && g.Role == backend.HiveRoleQueen && g.State == "live" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no queen on any host: %+v", hs)
	}
}

// An agent that stopped answering keeps its host for what it was.
func TestHostsKeepALostMachinesKind(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{}
	addr := startCore(t, core)
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "mac", Host: "mbp", Os: "darwin", Kind: "mac"})
	hub := core.agents()
	c := hub.get("mac")
	// What the stream's end does for an agent that vanished without a goodbye.
	hub.mu.Lock()
	delete(hub.agents, "mac")
	hub.lost["mac"] = c.connected
	hub.lostRole["mac"] = ""
	hub.lostHello["mac"] = c.hello
	hub.mu.Unlock()

	mac := hostByName(core.Hosts(), "mac")
	if mac == nil || mac.State != "lost" || mac.Kind != "mac" {
		t.Fatalf("lost mac: %+v", mac)
	}
	if g := agentNamed(mac, "mac"); g == nil || g.State != "lost" {
		t.Fatalf("its agent: %+v", g)
	}
}
