package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/agentexec"
	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
)

// Host is a box agents run on: a pod, a container, a Mac, a VM or a bare
// machine. What runs on it is a list of agents: SuperAI itself (as queen,
// worker, machine agent or relay), the coding CLIs installed there, and the
// named agents (openclaw, hermes, pi) a SuperAI on it serves.
//
// The hive used to be three lists that each knew part of this (the hive
// roster, the agent link, the configured remote agents), joined by name in
// every client. Hosts is the one list, built from all three.
type Host struct {
	// ID is unique in the list and is what Parent refers to: the host's own
	// name, lower case.
	ID string `json:"id"`
	// Name is what people call it: the name its SuperAI links with when that
	// is a machine agent ("mac", "orange1"), else the host's own name.
	Name     string `json:"name"`
	Hostname string `json:"hostname,omitempty"`
	// Kind is pod, container, mac, vm or metal; empty when nothing on it has
	// said (an older agent, or a host only known from the settings).
	Kind string `json:"kind"`
	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
	// Parent is the ID of the host this one runs inside: a pod's k3s node.
	Parent string `json:"parent,omitempty"`
	// State is live (something on it answers), lost (it did and stopped) or
	// configured (only the settings know of it; nobody has looked).
	State    string      `json:"state"`
	LastSeen time.Time   `json:"lastSeen,omitempty"`
	Agents   []HostAgent `json:"agents"`
}

// What an agent on a host is.
const (
	hostAgentSuperAI = "superai" // a SuperAI process: core, a worker, superai agent
	hostAgentCLI     = "cli"     // a coding CLI installed there: claude, codex, ...
	hostAgentNamed   = "agent"   // a named agent a SuperAI serves: openclaw, hermes, pi
)

// HostAgent is one agent on a host.
type HostAgent struct {
	// Name is what to call it by with @: "superai-worker-1", "claude.mac",
	// "openclaw".
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Role is a SuperAI's part in the hive: queen, worker, machine, relay,
	// or standalone for a core that is in no hive.
	Role  string `json:"role,omitempty"`
	About string `json:"about,omitempty"`
	// State is live or lost for a SuperAI; for a CLI working, waiting, idle
	// (a session open) or installed; configured for an agent only the
	// settings know.
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	// Via is the SuperAI on this host that runs it for core: a CLI's or a
	// named agent's.
	Via string `json:"via,omitempty"`
	// MCP is the MCP servers a SuperAI can call, where it has said.
	MCP []string `json:"mcp,omitempty"`
	// Service is the HA service it belongs to, when it is kept up as one:
	// it runs on the service's active host and stands by on the others
	// (state standby), and is reached at the service's IP.
	Service string `json:"service,omitempty"`
}

// HiveTopology is the hive in one answer: the hosts with what runs on each,
// and the HA services that move between them.
type HiveTopology struct {
	Hosts    []Host              `json:"hosts"`
	Services []backend.HAService `json:"services"`
}

// HiveTopology lists the hosts and the HA services together, read at the
// same moment.
func (a *App) HiveTopology() HiveTopology {
	svcs := a.haServices()
	return HiveTopology{Hosts: a.hostsWith(svcs), Services: svcs}
}

// Hosts lists every host this core knows of, parents before the hosts
// inside them, with what runs on each.
func (a *App) Hosts() []Host { return a.hostsWith(a.haServices()) }

func (a *App) hostsWith(services []backend.HAService) []Host {
	a.mu.Lock()
	s, h := a.settings, a.hive
	a.mu.Unlock()
	b := newHostBook()

	// This process, where it runs.
	self := backend.DetectHost()
	selfName, selfRole := "core", "standalone"
	if s != nil {
		if s.Hive.Role != "" {
			selfRole = s.Hive.Role
		}
		if n := strings.TrimSpace(s.Hive.Name); n != "" {
			selfName = n
		} else if s.Hive.Role != "" {
			selfName = self.Name
		}
	}
	me := b.host(self.Name, "", self.Kind, self.OS, self.Arch, self.Parent)
	me.live(time.Now())
	var mcp []string
	if svc := a.svc; svc != nil {
		mcp = svc.MCPServersWithTools()
	}
	b.agent(me, HostAgent{Name: selfName, Kind: hostAgentSuperAI, Role: selfRole, State: "live", Version: buildVersion(), MCP: mcp})
	b.clis(me, selfName, cliNames(a.localAgentNames()), a.CLIActivityAll()["core"])

	// Hive workers, by what they said when they joined.
	if h != nil {
		for _, m := range h.Members() {
			kind := m.Kind
			if kind == "" && m.Node != "" {
				kind = backend.HostKindPod // a worker from before kinds, placed on a node
			}
			hn := m.Host
			if hn == "" {
				hn = m.Name
			}
			x := b.host(hn, "", kind, "", "", m.Node)
			if m.State == "live" {
				x.live(m.LastSeen)
			} else {
				x.lost(m.LastSeen)
			}
			about := m.Engine
			b.agent(x, HostAgent{Name: m.Name, Kind: hostAgentSuperAI, Role: backend.HiveRoleWorker, About: about,
				State: m.State, Version: m.Version, MCP: m.MCP})
		}
	}

	// Everything on the agent link: machines, relays, and the worker pods
	// that also link (the same host and the same agent as their roster row).
	role := a.agentRoles()
	for _, c := range a.agents().list() {
		hello := c.hello
		r := role(c.name(), hello.GetRole())
		x := b.linked(c.name(), hello, r)
		x.live(time.UnixMilli(c.lastSeen.Load()))
		b.agent(x, HostAgent{Name: c.name(), Kind: hostAgentSuperAI, Role: r, State: "live", Version: hello.GetVersion()})
		b.clis(x, c.name(), hello.GetClis(), c.cliActivity())
		for _, n := range hello.GetAgents() {
			b.agent(x, HostAgent{Name: n.GetName(), Kind: hostAgentNamed, About: n.GetAbout(), State: "live", Via: c.name()})
		}
	}
	hub := a.agents()
	for name, seen := range hub.lostAgents(time.Now()) {
		hello := hub.lastHello(name)
		r := role(name, hub.declaredRole(name))
		x := b.linked(name, hello, r)
		x.lost(seen)
		b.agent(x, HostAgent{Name: name, Kind: hostAgentSuperAI, Role: r, State: "lost", Version: hello.GetVersion()})
	}

	// HA services: an agent one of them runs is that service's, live on its
	// active host and standing by on the others, wherever it was found.
	var agentNames []string
	if s != nil {
		agentNames = s.RemoteAgents.Names()
	}
	for _, sv := range services {
		b.service(sv, agentNames)
	}

	// Agents only the settings know, reached over ssh, where no linked
	// SuperAI or HA service serves them already.
	if s != nil && s.RemoteAgents.Enabled {
		for _, n := range s.RemoteAgents.Names() {
			ra := s.RemoteAgents.Agents[n]
			if ra.URL != "" || len(ra.Hosts) == 0 || onlyLocal(ra.Hosts) || b.served(n) || haServes(services, n) {
				continue
			}
			x := b.host(sshHost(ra.Hosts[0]), "", "", "", "", "")
			x.configured()
			b.agent(x, HostAgent{Name: n, Kind: hostAgentNamed, About: ra.About, State: "configured"})
		}
	}
	return b.list()
}

func cliNames(in []agentexec.Installed) []string {
	out := make([]string, 0, len(in))
	for _, c := range in {
		out = append(out, c.Name)
	}
	return out
}

// sshHost is the machine part of an ssh destination: "sds@192.168.123.212"
// is 192.168.123.212.
func sshHost(dest string) string {
	if i := strings.LastIndex(dest, "@"); i >= 0 {
		dest = dest[i+1:]
	}
	return dest
}

// hostBook builds the list: one Host per box, however many sources name it.
type hostBook struct {
	byID  map[string]*Host
	order []string
	names map[string]bool // agent names placed, for b.served
}

func newHostBook() *hostBook {
	return &hostBook{byID: map[string]*Host{}, names: map[string]bool{}}
}

func hostID(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// host finds or adds the host called hostname (or, without one, fallback)
// and fills in what was not known yet.
func (b *hostBook) host(hostname, fallback, kind, goos, arch, parent string) *Host {
	name := hostname
	if strings.TrimSpace(name) == "" {
		name = fallback
	}
	id := hostID(name)
	x := b.byID[id]
	if x == nil {
		x = &Host{ID: id, Name: name, Agents: []HostAgent{}}
		b.byID[id] = x
		b.order = append(b.order, id)
	}
	if x.Hostname == "" && hostname != "" {
		x.Hostname = hostname
	}
	if x.Kind == "" {
		x.Kind = kind
	}
	if x.OS == "" {
		x.OS = goos
	}
	if x.Arch == "" {
		x.Arch = arch
	}
	if x.Parent == "" && parent != "" && hostID(parent) != id {
		x.Parent = hostID(parent)
	}
	return x
}

// linked is the host a linked SuperAI runs on. A machine agent's link name is
// the name people gave that box, so the host goes by it.
func (b *hostBook) linked(name string, hello *agentlinkpb.Hello, role string) *Host {
	kind := hello.GetKind()
	if kind == "" && hello.GetOs() == "darwin" {
		kind = backend.HostKindMac
	}
	x := b.host(hello.GetHost(), name, kind, hello.GetOs(), hello.GetArch(), hello.GetParent())
	if role == agentRoleMachine {
		x.Name = name
	}
	return x
}

// agent adds g to x, or fills in the one of that name already there: a
// worker pod is on the roster and on the link, and is one agent.
func (b *hostBook) agent(x *Host, g HostAgent) {
	b.names[g.Name] = true
	for i := range x.Agents {
		e := &x.Agents[i]
		if e.Name != g.Name || e.Kind != g.Kind {
			continue
		}
		if e.State != "live" && g.State == "live" {
			e.State = g.State
		}
		if e.Role == "" || (e.Role == agentRoleMachine && g.Role == backend.HiveRoleWorker) {
			e.Role = g.Role
		}
		if e.About == "" {
			e.About = g.About
		}
		if e.Version == "" {
			e.Version = g.Version
		}
		if e.MCP == nil {
			e.MCP = g.MCP
		}
		return
	}
	x.Agents = append(x.Agents, g)
}

// clis adds the coding CLIs installed on x, run for core by via, with what
// each is doing.
func (b *hostBook) clis(x *Host, via string, names []string, act cliMap) {
	seen := map[string]bool{}
	add := func(cli string) {
		if seen[cli] {
			return
		}
		seen[cli] = true
		st := "installed"
		if a, ok := act[cli]; ok {
			switch {
			case a.Working > 0:
				st = actWorking
			case a.Waiting > 0:
				st = actWaiting
			case a.Idle > 0 || len(a.Sessions) > 0:
				st = actIdle
			}
		}
		b.agent(x, HostAgent{Name: cli + "." + via, Kind: hostAgentCLI, State: st, Via: via})
	}
	for _, n := range names {
		add(n)
	}
	// Running but not found on PATH (a CLI started from elsewhere) is still here.
	extra := make([]string, 0, len(act))
	for n := range act {
		extra = append(extra, n)
	}
	sort.Strings(extra)
	for _, n := range extra {
		add(n)
	}
}

// service places sv's agents: on the active host as running (filling in the
// one a linked relay already reported), on each standby as standing by.
// Its agents are the named agents it runs a unit for, among those seen on
// the link or in the settings.
func (b *hostBook) service(sv backend.HAService, configured []string) {
	names := map[string]bool{}
	for n := range b.names {
		names[n] = true
	}
	for _, n := range configured {
		names[n] = true
	}
	var its []string
	for n := range names {
		if sv.Serves(n) {
			its = append(its, n)
		}
	}
	sort.Strings(its)
	if len(its) == 0 {
		return
	}
	// Whatever host already reported the agent but is not the active one
	// is out of date (it moved); the service's word wins.
	for _, x := range b.byID {
		for i := range x.Agents {
			g := &x.Agents[i]
			if g.Kind == hostAgentNamed && sv.Serves(g.Name) {
				g.Service = sv.Name
			}
		}
	}
	if sv.Active != "" {
		x := b.host(sv.Active, "", "", "", "", "")
		if x.State == "" {
			x.State = "live" // haify sees it running there
		}
		for _, n := range its {
			b.agent(x, HostAgent{Name: n, Kind: hostAgentNamed, State: "live", Service: sv.Name})
			setService(x, n, sv.Name)
		}
	}
	for _, sb := range sv.Standbys {
		x := b.host(sb.Host, "", "", "", "", "")
		x.configured()
		state := "standby"
		if !sb.Ready {
			state = "standby-unready"
		}
		for _, n := range its {
			b.agent(x, HostAgent{Name: n, Kind: hostAgentNamed, State: state, Service: sv.Name})
		}
	}
}

func setService(x *Host, name, service string) {
	for i := range x.Agents {
		if x.Agents[i].Name == name && x.Agents[i].Kind == hostAgentNamed {
			x.Agents[i].Service = service
		}
	}
}

func haServes(services []backend.HAService, name string) bool {
	for _, sv := range services {
		if sv.Serves(name) {
			return true
		}
	}
	return false
}

// served says whether an agent of this name is already on some host.
func (b *hostBook) served(name string) bool { return b.names[name] }

func (x *Host) live(at time.Time) {
	x.State = "live"
	if at.After(x.LastSeen) {
		x.LastSeen = at
	}
}

func (x *Host) lost(at time.Time) {
	if x.State != "live" {
		x.State = "lost"
	}
	if at.After(x.LastSeen) {
		x.LastSeen = at
	}
}

func (x *Host) configured() {
	if x.State == "" {
		x.State = "configured"
	}
}

// list is the hosts with a stand-in for any parent nobody reported (a k3s
// node whose own agent is not linked), parents first, then by name.
func (b *hostBook) list() []Host {
	for _, id := range append([]string{}, b.order...) {
		x := b.byID[id]
		if x.Parent != "" && b.byID[x.Parent] == nil {
			p := b.host(x.Parent, "", "", "", "", "")
			p.State = "configured"
		}
		if x.State == "" {
			x.State = "configured"
		}
	}
	depth := func(x *Host) int {
		d := 0
		for p := x.Parent; p != "" && d < 8; d++ {
			px := b.byID[p]
			if px == nil {
				break
			}
			p = px.Parent
		}
		return d
	}
	out := make([]Host, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, *b.byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := depth(&out[i]), depth(&out[j])
		if di != dj {
			return di < dj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// topologyText is the hive as the queen reads it: the hosts, then the HA
// services with where each runs and can move to.
func topologyText(t HiveTopology) string {
	out := hostsText(t.Hosts)
	if len(t.Services) == 0 {
		return out
	}
	var b strings.Builder
	b.WriteString(out)
	b.WriteString("\nHA services (one copy runs, standbys take over; reach them at the service IP):\n")
	for _, sv := range t.Services {
		fmt.Fprintf(&b, "- %s: %s, on %s", sv.Name, sv.State, orNone(sv.Active))
		if sv.VIP != "" {
			fmt.Fprintf(&b, ", service IP %s", sv.VIP)
		}
		for _, sb := range sv.Standbys {
			r := "ready"
			if !sb.Ready {
				r = "not ready"
			}
			fmt.Fprintf(&b, ", standby %s (%s)", sb.Host, r)
		}
		fmt.Fprintf(&b, " [%s, %s]\n", sv.Provider, strings.Join(sv.Units, " "))
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "nowhere"
	}
	return s
}

// hostsText is the host list as the queen reads it: one line per host, its
// agents under it, indented by how deep the host sits.
func hostsText(hs []Host) string {
	if len(hs) == 0 {
		return "No hosts."
	}
	depth := map[string]int{}
	var b strings.Builder
	for _, h := range hs {
		d := 0
		if h.Parent != "" {
			d = depth[h.Parent] + 1
		}
		depth[h.ID] = d
		pad := strings.Repeat("  ", d)
		kind := h.Kind
		if kind == "" {
			kind = "host"
		}
		fmt.Fprintf(&b, "%s%s (%s", pad, h.Name, kind)
		if h.OS != "" {
			fmt.Fprintf(&b, ", %s/%s", h.OS, h.Arch)
		}
		fmt.Fprintf(&b, ") %s\n", h.State)
		for _, g := range h.Agents {
			fmt.Fprintf(&b, "%s  - %s: %s", pad, g.Name, g.Kind)
			if g.Role != "" {
				fmt.Fprintf(&b, " %s", g.Role)
			}
			fmt.Fprintf(&b, ", %s", g.State)
			if len(g.MCP) > 0 {
				fmt.Fprintf(&b, ", mcp %s", strings.Join(g.MCP, " "))
			}
			if g.Service != "" {
				fmt.Fprintf(&b, ", HA service %s", g.Service)
			}
			if g.About != "" {
				fmt.Fprintf(&b, " (%s)", g.About)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
