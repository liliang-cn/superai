package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
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
// of them also on the agent link), a VM's own superleo agent, a Mac with its
// coding CLIs, and a relay serving openclaw. Hosts puts each on the box it
// runs on, each box once, and every agent on it.
func TestHostsPutsAgentsOnTheirHosts(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{
		Hive:      backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "queen"},
		AgentLink: backend.AgentLinkSettings{RelayOnly: []string{"sds-e"}},
		RemoteAgents: backend.RemoteAgents{Enabled: true, Agents: map[string]backend.RemoteAgent{
			// Served by the relay, and configured on an address nothing
			// says is the relay's box: on both, each its own placement.
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

	hs := core.fullTopology().Hosts
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
	if g := agentNamed(mac, "mac"); g == nil || g.Kind != hostAgentSuperLeo || g.Role != agentRoleMachine {
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
		if g.Kind == hostAgentSuperLeo {
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
	if g := agentNamed(hostByName(hs, "192.168.123.212"), "openclaw"); g == nil || g.State != "configured" {
		t.Fatalf("openclaw where the settings reach it: %+v", g)
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

	mac := hostByName(core.fullTopology().Hosts, "mac")
	if mac == nil || mac.State != "lost" || mac.Kind != "mac" {
		t.Fatalf("lost mac: %+v", mac)
	}
	if g := agentNamed(mac, "mac"); g == nil || g.State != "lost" {
		t.Fatalf("its agent: %+v", g)
	}
}

// openclaw kept up by haify: it belongs to its HA service, runs on the active
// node the relay is on, stands by on the other, and is not listed again from
// the ssh settings that point at a node.
func TestHostsPlaceHAServices(t *testing.T) {
	haify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/nodes":
			w.Write([]byte(`{"nodes":[{"name":"node-b","hostname":"sds-b"},{"name":"node-e","hostname":"sds-e"}]}`))
		case "/v1/ha":
			w.Write([]byte(`{"configs":[{"resource":"openclaw","vip":"192.168.123.252/24","services":["openclaw.service"]},
				{"resource":"cortexdb","vip":"192.168.123.253/24","services":["cortexdb-grpc.service"]}]}`))
		case "/v1/ha/openclaw/status":
			w.Write([]byte(`{"promoters":[{"activeNode":"node-e","standbys":[{"node":"node-b","ready":true}]}]}`))
		case "/v1/ha/cortexdb/status":
			w.Write([]byte(`{"promoters":[{"activeNode":"node-b","standbys":[{"node":"node-e","ready":true}]}]}`))
		}
	}))
	defer haify.Close()

	core := NewApp()
	core.settings = &backend.Settings{
		AgentLink: backend.AgentLinkSettings{RelayOnly: []string{"sds-e"}},
		HA:        backend.HASettings{Haify: backend.HaifySource{URL: haify.URL}},
		RemoteAgents: backend.RemoteAgents{Enabled: true, Agents: map[string]backend.RemoteAgent{
			"openclaw": {Hosts: []string{"sds@192.168.123.212"}, Command: []string{"openclaw", "{prompt}"}},
		}},
	}
	addr := startCore(t, core)
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "sds-e", Host: "sds-e", Kind: "vm",
		Agents: []*agentlinkpb.NamedAgent{{Name: "openclaw", About: "the gateway"}}})

	core.haServices() // starts the first read
	waitFor(t, "the HA services", func() bool { return len(core.haServices()) == 2 })
	top := core.fullTopology()

	active := hostByName(top.Hosts, "sds-e")
	if g := agentNamed(active, "openclaw"); g == nil || g.State != "live" || g.Service != "openclaw" || g.Via != "sds-e" {
		t.Fatalf("openclaw on its active node: %+v", g)
	}
	standby := hostByName(top.Hosts, "sds-b")
	if g := agentNamed(standby, "openclaw"); standby == nil || g == nil || g.State != "standby" || g.Service != "openclaw" {
		t.Fatalf("openclaw standing by: %+v in %+v", g, standby)
	}
	if h := hostByName(top.Hosts, "192.168.123.212"); h != nil {
		t.Fatalf("openclaw listed again from the settings: %+v", h)
	}
	// cortexdb runs no agent anyone knows: a service, not an agent on a host.
	if g := agentNamed(standby, "cortexdb-grpc"); g != nil {
		t.Fatalf("cortexdb as an agent: %+v", g)
	}
	if len(top.Services) != 2 || top.Services[1].Name != "openclaw" || top.Services[1].VIP != "192.168.123.252" {
		t.Fatalf("services: %+v", top.Services)
	}
	if txt := topologyText(top); !strings.Contains(txt, "openclaw: active, on sds-e, service IP 192.168.123.252, standby sds-b (ready)") {
		t.Fatalf("the queen's view:\n%s", txt)
	}

	// The apps draw the HA cluster: openclaw once, running, on sds-e now;
	// its standby and the node known only for that are gone.
	apps := core.HiveTopology()
	c := hostByName(apps.Hosts, "openclaw")
	if g := agentNamed(c, "openclaw"); c == nil || c.Kind != hostKindHA || c.On != "sds-e" || g == nil || g.State != "live" || g.Via != "sds-e" {
		t.Fatalf("the openclaw cluster: %+v", c)
	}
	if g := agentNamed(hostByName(apps.Hosts, "sds-e"), "openclaw"); g != nil {
		t.Fatalf("openclaw on its node as well: %+v", g)
	}
	if h := hostByName(apps.Hosts, "sds-b"); h != nil {
		t.Fatalf("an HA node with nothing on it: %+v", h)
	}
	for _, h := range apps.Hosts {
		for _, g := range h.Agents {
			if g.State == "standby" || g.State == "standby-unready" {
				t.Fatalf("a standby on %s: %+v", h.Name, g)
			}
		}
	}
	if len(apps.Services) != 0 {
		t.Fatalf("services in the apps' view: %+v", apps.Services)
	}
}

// pi and hermes are installed on several VMs. Each is drawn on every host it
// is known on: from each linked relay's hello, and on every ssh host the
// settings give it, matched to a host already drawn by haify's addresses or
// by name. Each placement answers to name.host; bare @pi still works.
func TestHostsPutAnAgentOnEveryHostItIsOn(t *testing.T) {
	haify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/nodes":
			w.Write([]byte(`{"nodes":[{"name":"node-b","hostname":"sds-b","address":"192.168.123.213"},
				{"name":"node-e","hostname":"sds-e","address":"192.168.123.212:3366"}]}`))
		case "/v1/ha":
			w.Write([]byte(`{"configs":[{"resource":"openclaw","vip":"192.168.123.252/24","services":["openclaw.service"]}]}`))
		case "/v1/ha/openclaw/status":
			w.Write([]byte(`{"promoters":[{"activeNode":"node-e","standbys":[{"node":"node-b","ready":true}]}]}`))
		}
	}))
	defer haify.Close()

	core := NewApp()
	core.settings = &backend.Settings{
		AgentLink: backend.AgentLinkSettings{RelayOnly: []string{"sds-e"}},
		HA:        backend.HASettings{Haify: backend.HaifySource{URL: haify.URL}},
		RemoteAgents: backend.RemoteAgents{Enabled: true, Agents: map[string]backend.RemoteAgent{
			"pi": {Hosts: []string{"sds@192.168.123.212", "-F ~/.lima/sds-a/ssh.config lima-sds-a", "sds@192.168.123.213", "-p 52848 me@127.0.0.1"},
				Probe: "command -v pi", Command: []string{"pi", "{prompt}"}},
			"hermes": {Hosts: []string{"sds@192.168.123.212", "sds@10.0.0.9"}, Command: []string{"hermes", "{prompt}"}},
			// HA keeps it: an address that is no host drawn is not a new one.
			"openclaw": {Hosts: []string{"sds@192.168.123.212", "sds@192.168.123.77"}, Command: []string{"openclaw", "{prompt}"}},
		}},
	}
	addr := startCore(t, core)
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "sds-e", Host: "sds-e", Kind: "vm", Clis: []string{"codex", "openclaw"},
		Agents: []*agentlinkpb.NamedAgent{{Name: "openclaw"}, {Name: "hermes"}, {Name: "pi"}}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "orange1", Host: "orange1", Kind: "vm",
		Agents: []*agentlinkpb.NamedAgent{{Name: "pi"}}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "sds-a", Host: "lima-sds-a", Kind: "vm"})
	// The process detector sees openclaw at work on sds-e, as a CLI.
	core.agents().get("sds-e").clis.Store(&cliMap{"openclaw": {CCActivity: CCActivity{Working: 1}, Source: cliSourceProcess}})

	core.haServices()
	waitFor(t, "the HA services", func() bool { return len(core.haServices()) == 1 })
	hs := core.fullTopology().Hosts

	// pi: live on both relays that serve it, configured on the Lima VM (by
	// its hostname) and on sds-b (by haify's address), never on an address.
	for host, state := range map[string]string{"sds-e": "live", "orange1": "live", "sds-a": "configured", "sds-b": "configured"} {
		if g := agentNamed(hostByName(hs, host), "pi"); g == nil || g.State != state {
			t.Fatalf("pi on %s: %+v", host, g)
		}
	}
	for _, ip := range []string{"192.168.123.212", "192.168.123.213", "192.168.123.77", "127.0.0.1"} {
		if h := hostByName(hs, ip); h != nil {
			t.Fatalf("a host for %s: %+v", ip, h)
		}
	}
	// hermes: on the relay, and on the one host nothing else knows.
	if g := agentNamed(hostByName(hs, "10.0.0.9"), "hermes"); g == nil || g.State != "configured" {
		t.Fatalf("hermes on its second host: %+v", g)
	}
	// openclaw on sds-e is one agent, the named one, doing what the CLI does.
	e := hostByName(hs, "sds-e")
	n := 0
	for _, g := range e.Agents {
		if strings.HasPrefix(g.Name, "openclaw") {
			n++
		}
	}
	if g := agentNamed(e, "openclaw"); n != 1 || g == nil || g.Kind != hostAgentNamed || g.Activity != actWorking || g.Service != "openclaw" {
		t.Fatalf("openclaw on sds-e: %d of them, %+v", n, e.Agents)
	}
	if agentNamed(e, "codex.sds-e") == nil {
		t.Fatalf("codex left out: %+v", e.Agents)
	}
	if b := hostByName(hs, "sds-b"); len(b.Addresses) != 1 || b.Addresses[0] != "192.168.123.213" {
		t.Fatalf("sds-b's address: %+v", b)
	}

	// Every placement is addressable as name.host.
	hub := core.agents()
	if c, as := hub.servingAs("pi.orange1"); c == nil || c.name() != "orange1" || as != "pi" {
		t.Fatalf("pi.orange1 reached %v as %q", c, as)
	}
	if c, _ := hub.servingAs("pi.sds-e"); c == nil || c.name() != "sds-e" {
		t.Fatalf("pi.sds-e reached %v", c)
	}
	if hub.servingOn("pi", "sds-a") != nil {
		t.Fatal("sds-a's own SuperLeo does not serve pi")
	}
	for machine, dest := range map[string]string{
		"sds-a":           "-F ~/.lima/sds-a/ssh.config lima-sds-a",
		"lima-sds-a":      "-F ~/.lima/sds-a/ssh.config lima-sds-a",
		"sds-b":           "sds@192.168.123.213",
		"192.168.123.213": "sds@192.168.123.213",
		"sds-e":           "sds@192.168.123.212",
		"orange1":         "",
	} {
		if got := core.sshDestOn("pi", machine); got != dest {
			t.Fatalf("pi on %s goes to %q, want %q", machine, got, dest)
		}
	}
	for _, name := range []string{"pi", "pi.sds-a", "pi.sds-b", "pi.orange1", "hermes.10.0.0.9"} {
		if !core.addressable(name) {
			t.Fatalf("%s is not addressable", name)
		}
	}
}

// A SuperLeo with no model of its own is how the queen reaches a machine, not
// a member: it is not drawn, and a machine with nothing else working on it is
// not either. What it runs that works on its own (a signed-in claude, a named
// agent) stays, and so does a pod's node that carries a worker.
func TestOnlyWhatWorksIsDrawn(t *testing.T) {
	in := HiveTopology{Hosts: []Host{
		{ID: "orange1", Name: "orange1", Agents: []HostAgent{{Name: "orange1", Kind: "superai", Role: agentRoleMachine}}},
		{ID: "superai-worker-1", Name: "superai-worker-1", Parent: "orange1", Agents: []HostAgent{{Name: "superai-worker-1", Kind: "superai", Role: "worker"}}},
		{ID: "win11", Name: "win11", Agents: []HostAgent{{Name: "win11", Kind: "superai", Role: agentRoleMachine}}},
		{ID: "sds-e", Name: "sds-e", Agents: []HostAgent{
			{Name: "sds-e", Kind: "superai", Role: agentRoleRelay},
			{Name: "openclaw", Kind: "agent", Via: "sds-e", State: "live"},
		}},
		// Only the settings know openclaw here, and it runs on sds-e.
		{ID: "lima-sds-a", Name: "lima-sds-a", State: "configured", Agents: []HostAgent{
			{Name: "openclaw", Kind: "agent", State: "configured"},
		}},
		{ID: "pro.local", Name: "mac", Agents: []HostAgent{
			{Name: "mac", Kind: "superai", Role: "worker"},
			{Name: "claude.mac", Kind: "cli", Via: "mac"},
			{Name: "codex.mac", Kind: "cli", Via: "mac", State: "installed"},                  // can say it is signed in
			{Name: "goose.mac", Kind: "cli", Via: "mac", State: "installed"},                  // cannot, and is not in use
			{Name: "opencode.mac", Kind: "cli", Via: "mac", State: "working"},                 // cannot say, but is in use
			{Name: "gemini.mac", Kind: "cli", Via: "mac", State: "working", Unrunnable: true}, // in use, but not one SuperLeo can start
		}},
	}}
	got := map[string][]string{}
	for _, h := range onlyWhatWorks(in).Hosts {
		for _, a := range h.Agents {
			got[h.ID] = append(got[h.ID], a.Name)
		}
		if got[h.ID] == nil {
			got[h.ID] = []string{}
		}
	}
	want := map[string][]string{
		"orange1":          {}, // kept as the place its worker's pod is
		"superai-worker-1": {"superai-worker-1"},
		"sds-e":            {"openclaw"},
		"pro.local":        {"mac", "claude.mac", "codex.mac", "opencode.mac"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("drawn = %v\nwant    %v", got, want)
	}
}

// On the hive every worker says it reaches openclaw, and hermes and pi are
// only known from the settings. HA's word decides: the cluster runs on the
// service's active node, and every agent in it runs there.
func TestTheHAServiceSaysWhereItsClusterRuns(t *testing.T) {
	top := withoutHA(HiveTopology{
		Hosts: []Host{
			{ID: "superai-worker-2", Name: "superai-worker-2", State: "live", Agents: []HostAgent{
				{Name: "superai-worker-2", Kind: hostAgentSuperLeo, Role: "worker", State: "live"},
				{Name: "openclaw", Kind: hostAgentNamed, State: "live", Via: "superai-worker-2", Service: "openclaw"},
			}},
			{ID: "sds-e", Name: "sds-e", State: "live", Agents: []HostAgent{
				{Name: "hermes", Kind: hostAgentNamed, State: "configured", Service: "openclaw"},
				{Name: "pi", Kind: hostAgentNamed, State: "configured", Service: "openclaw"},
			}},
		},
		Services: []backend.HAService{{Name: "openclaw", Active: "sds-b", State: "active"}},
	})
	c := hostByName(top.Hosts, "openclaw")
	if c == nil || c.On != "sds-b" || c.State != "live" || len(c.Agents) != 3 {
		t.Fatalf("the cluster: %+v", c)
	}
	for _, g := range c.Agents {
		if g.State != "live" {
			t.Fatalf("%s is %s on a running cluster", g.Name, g.State)
		}
	}
	if w := hostByName(top.Hosts, "superai-worker-2"); w == nil || agentNamed(w, "openclaw") != nil {
		t.Fatalf("the worker keeps its SuperLeo and loses the copy: %+v", w)
	}
}
