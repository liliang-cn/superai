package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
)

// linkHello connects a hand-played agent that says hello and answers nothing.
func linkHello(t *testing.T, core *App, addr string, h *agentlinkpb.Hello) {
	t.Helper()
	stream := dialAs(t, addr, "tok")
	if err := stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: h}}); err != nil {
		t.Fatal(err)
	}
	if f, err := stream.Recv(); err != nil || f.GetWelcome() == nil {
		t.Fatalf("welcome for %s: %v %v", h.GetName(), f, err)
	}
	waitFor(t, h.GetName()+" to register", func() bool { return core.agents().get(h.GetName()) != nil })
}

func names(l []LinkedAgent) string {
	out := []string{}
	for _, a := range l {
		out = append(out, a.Name)
	}
	return strings.Join(out, ",")
}

// The hive's worker pods dial the agent link too, and a relay is kept for
// the agents it serves: neither is a machine, wherever machines are shown or
// looked after, while what they offer stays reachable.
func TestWorkersAndRelaysAreNotMachines(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{
		Hive:      backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"},
		AgentLink: backend.AgentLinkSettings{RelayOnly: []string{"sds-e"}},
	}
	core.hive = backend.NewHive("q", 0)
	// A worker from before Hello.role: known by being on the roster.
	if _, err := core.hive.Join(backend.HiveHello{Protocol: backend.HiveProtocol, Name: "superleo-worker-1",
		Role: backend.HiveRoleWorker, URL: "http://w1:1"}); err != nil {
		t.Fatal(err)
	}
	addr := startCore(t, core)

	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "mac", Os: "darwin", Clis: []string{"claude"}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "superleo-worker-0", Role: "worker", Clis: []string{"claude"}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "superleo-worker-1", Clis: []string{"claude"}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "sds-e", Clis: []string{"pi"},
		Agents: []*agentlinkpb.NamedAgent{{Name: "openclaw"}, {Name: "hermes"}}})

	// The machine views have the machine only.
	if got := names(core.LinkedAgents()); got != "mac" {
		t.Fatalf("machines: %s", got)
	}
	for _, la := range core.LinkedAgents() {
		if !la.Machine || la.Role != "machine" {
			t.Fatalf("a machine without its flag: %+v", la)
		}
	}
	// The complete list says what each one is.
	want := map[string]string{"mac": "machine", "superleo-worker-0": "worker", "superleo-worker-1": "worker", "sds-e": "relay"}
	all := core.LinkedAgentsAll()
	if len(all) != len(want) {
		t.Fatalf("all: %s", names(all))
	}
	for _, la := range all {
		if la.Role != want[la.Name] || la.Machine != (want[la.Name] == "machine") {
			t.Fatalf("%s: role %q machine %v", la.Name, la.Role, la.Machine)
		}
	}
	// And so does the queen's linked_agents tool.
	raw, _ := json.Marshal(core.selfLinkedAgents())
	if !strings.Contains(string(raw), `"name":"sds-e"`) || !strings.Contains(string(raw), `"role":"relay"`) {
		t.Fatalf("linked_agents: %s", raw)
	}

	// The machine tools refuse them, and say why.
	for name, why := range map[string]string{"superleo-worker-0": "hive worker", "superleo-worker-1": "hive worker", "sds-e": "relay"} {
		_, err := core.MachineStatus(name)
		if err == nil || !strings.Contains(err.Error(), why) || !strings.Contains(err.Error(), "the machines are core, mac") {
			t.Fatalf("MachineStatus(%s): %v", name, err)
		}
		if _, err := core.machineAct(map[string]any{"machine": name, "action": "exec", "command": "true"}); err == nil {
			t.Fatalf("machine_act on %s went through", name)
		}
	}

	// What the relay serves is still routable.
	if !core.addressable("openclaw") || !core.addressable("hermes") || !core.isRemoteCLI("pi.sds-e") {
		t.Fatal("the relay's agents are not addressable")
	}
	if c, bare := core.agents().servingAs("openclaw.sds-e"); c == nil || c.name() != "sds-e" || bare != "openclaw" {
		t.Fatalf("openclaw.sds-e reaches %v as %q", c, bare)
	}
}

// A worker pod that drops is the roster's to report; a machine or a relay
// that drops is Needs you's.
func TestALostWorkerPodIsNotAlsoALostMachine(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{AgentLink: backend.AgentLinkSettings{RelayOnly: []string{"sds-e"}}}
	h := core.agents()
	now := time.Now()
	h.mu.Lock()
	h.lost["superleo-worker-2"], h.lostRole["superleo-worker-2"] = now, "worker"
	h.lost["sds-e"] = now
	h.lost["orange1"] = now
	h.mu.Unlock()

	lost := map[string]bool{}
	for _, it := range core.Attention() {
		if it.Kind == "lost" {
			lost[it.Ref] = true
		}
	}
	if lost["superleo-worker-2"] || !lost["sds-e"] || !lost["orange1"] {
		t.Fatalf("lost: %v", lost)
	}
}

// A process that is a hive worker says so in its Hello.
func TestAWorkerSaysItIsOneOnTheLink(t *testing.T) {
	a := NewApp()
	a.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleWorker}}
	if r := a.agentHello(AgentLinkOptions{Name: "superleo-worker-0"}).GetRole(); r != "worker" {
		t.Fatalf("worker hello role %q", r)
	}
	a.settings = &backend.Settings{}
	if r := a.agentHello(AgentLinkOptions{Name: "mac"}).GetRole(); r != "" {
		t.Fatalf("machine hello role %q", r)
	}
}

// agent_link.relay_only goes out with GetSettings and comes back with
// SaveSettings over /api/rpc, survives the hive's entrypoint rewriting
// settings.json, and is one of the settings settings_set may change.
func TestRelayOnlyRoundTripsThroughTheSettingsRPC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	a := NewApp()
	s, err := backend.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.AgentLink.RelayOnly) != 0 {
		t.Fatalf("default relay_only: %v", s.AgentLink.RelayOnly)
	}
	s.CLIProxyEnabled = false
	a.settings = s
	defer a.Shutdown(context.Background())

	got, err := callMethod(a, "GetSettings", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["agent_link"] = map[string]any{"relay_only": []any{" sds-e ", "", "SDS-E"}}
	body, _ := json.Marshal(m)
	if _, err := callMethod(a, "SaveSettings", []json.RawMessage{body}); err != nil {
		t.Fatal(err)
	}
	back, _ := callMethod(a, "GetSettings", nil)
	if r := back.(backend.Settings).AgentLink.RelayOnly; strings.Join(r, ",") != "sds-e" {
		t.Fatalf("after save: %v", r)
	}

	// The hive's entrypoint writes settings.json from the Secret on every start.
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"llm_model":"m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	again, _ := backend.LoadSettings()
	if !again.AgentLink.IsRelay("sds-e") || again.AgentLink.IsRelay("orange1") {
		t.Fatalf("after the rewrite: %v", again.AgentLink.RelayOnly)
	}

	// settings_set takes a list, or names in one string, and none clears it.
	for in, want := range map[any]string{"sds-e, sds-f": "sds-e,sds-f", `["sds-e"]`: "sds-e", "": ""} {
		cfg := *again
		if _, err := backend.SetWritableSetting(&cfg, "agent_link.relay_only", in); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(cfg.AgentLink.RelayOnly, ","); got != want {
			t.Fatalf("settings_set %q: %q", in, got)
		}
	}
	cfg := *again
	if _, err := backend.SetWritableSetting(&cfg, "agent_link.relay_only", []any{"sds-e", "orange9"}); err != nil || len(cfg.AgentLink.RelayOnly) != 2 {
		t.Fatalf("list: %v %v", cfg.AgentLink.RelayOnly, err)
	}
	// Cleared and saved, the file goes, and the Secret's word is back.
	cfg.AgentLink.RelayOnly = nil
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backend.DataDir(), "agentlink.json")); !os.IsNotExist(err) {
		t.Fatalf("agentlink.json kept: %v", err)
	}
}

// A Mac that joins the hive as a worker is still the Mac: listed and looked
// after as a machine, while a worker pod is not. And work that needs a
// coding CLI goes to that CLI, or to the worker where it is installed.
func TestAMacWorkerIsStillAMachine(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}
	core.hive = backend.NewHive("q", 0)
	for _, w := range []backend.HiveHello{
		{Name: "mac", Kind: "mac", MCP: []string{"noted"}, CLIs: []string{"claude", "codex"}},
		{Name: "superleo-worker-0", Kind: "pod", Node: "orange2", MCP: []string{"noted"}},
	} {
		w.Protocol, w.Role, w.URL = backend.HiveProtocol, backend.HiveRoleWorker, "http://"+w.Name+":1"
		if _, err := core.hive.Join(w); err != nil {
			t.Fatal(err)
		}
	}
	addr := startCore(t, core)
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "mac", Os: "darwin", Kind: "mac", Role: "worker", Clis: []string{"claude", "codex"}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "superleo-worker-0", Kind: "pod", Role: "worker"})

	if got := names(core.LinkedAgents()); got != "mac" {
		t.Fatalf("machines: %s", got)
	}
	for _, la := range core.LinkedAgentsAll() {
		if la.Name == "mac" && (la.Role != "worker" || !la.Machine) {
			t.Fatalf("the mac: %+v", la)
		}
	}
	// Codex itself first, a worker of its own; the Mac's SuperLeo, which can
	// run it, next.
	if got := core.delegateCandidates([]string{"codex"}); strings.Join(got, " ") != "codex.mac mac" {
		t.Fatalf("codex work goes to %v", got)
	}
	if got := core.delegateCandidates([]string{"noted"}); len(got) != 2 {
		t.Fatalf("noted work goes to %v", got)
	}
}
