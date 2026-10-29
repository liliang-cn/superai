package app

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/agentexec"
	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
)

// Host is a box agents run on: a pod, a container, a Mac, a VM or a bare
// machine. What runs on it is a list of agents: SuperLeo itself (as queen,
// worker, machine agent or relay), the coding CLIs installed there, and the
// named agents (openclaw, hermes, pi) a SuperLeo on it serves.
//
// The hive used to be three lists that each knew part of this (the hive
// roster, the agent link, the configured remote agents), joined by name in
// every client. Hosts is the one list, built from all three.
type Host struct {
	// ID is unique in the list and is what Parent refers to: the host's own
	// name, lower case.
	ID string `json:"id"`
	// Name is what people call it: the name its SuperLeo links with when that
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
	State    string    `json:"state"`
	LastSeen time.Time `json:"lastSeen,omitempty"`
	// Addresses are the host's IP addresses where something has said them
	// (haify's node list), which is how an ssh destination such as
	// sds@192.168.123.212 is known to mean this host.
	Addresses []string    `json:"addresses,omitempty"`
	Agents    []HostAgent `json:"agents"`
	// On is, for an HA cluster (Kind "ha"), the host its agents run on now;
	// empty while they run on none.
	On string `json:"on,omitempty"`
}

// hostKindHA is the kind of the host the apps draw for an HA cluster: the
// agents an HA service keeps up, together, whichever node runs them now.
const hostKindHA = "ha"

// What an agent on a host is.
const (
	hostAgentSuperLeo = "superleo" // a SuperLeo process: core, a worker, superleo agent
	hostAgentCLI      = "cli"      // a coding CLI installed there: claude, codex, ...
	hostAgentNamed    = "agent"    // a named agent a SuperLeo serves: openclaw, hermes, pi
)

// HostAgent is one agent on a host.
type HostAgent struct {
	// Name is what to call it by with @: "superleo-worker-1", "claude.mac",
	// "openclaw".
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Role is a SuperLeo's part in the hive: queen, worker, machine, relay,
	// or standalone for a core that is in no hive.
	Role  string `json:"role,omitempty"`
	About string `json:"about,omitempty"`
	// State is live or lost for a SuperLeo; for a CLI working, waiting, idle
	// (a session open) or installed; configured for an agent only the
	// settings know.
	State string `json:"state"`
	// Activity is, for a named agent, what the coding CLI of the same name on
	// this host is doing (working, waiting, idle): the process detector sees
	// openclaw run as a CLI where a relay serves it as an agent, and the two
	// are one agent.
	Activity string `json:"activity,omitempty"`
	Version  string `json:"version,omitempty"`
	// Via is the SuperLeo on this host that runs it for core: a CLI's or a
	// named agent's.
	Via string `json:"via,omitempty"`
	// MCP is the MCP servers a SuperLeo can call, where it has said.
	MCP []string `json:"mcp,omitempty"`
	// Abilities is what that SuperLeo's machine can do for the queen: desktop,
	// browser, files, exec (abilities.go).
	Abilities []HostAbility `json:"abilities,omitempty"`
	// Service is the HA service it belongs to, when it is kept up as one:
	// it runs on the service's active host and stands by on the others
	// (state standby), and is reached at the service's IP.
	Service string `json:"service,omitempty"`
	// Unrunnable is a coding CLI seen running on its host that the SuperLeo
	// there cannot start: not installed for it, or its CLIs switched off.
	// Someone is using it; core cannot hand it work.
	Unrunnable bool `json:"unrunnable,omitempty"`
	// spare is a named agent that follows one host at a time, on a host of
	// its the apps do not draw it on: while it is not known to run anywhere
	// it is drawn once, on its first host, not on all of them.
	spare bool
}

// HiveTopology is the hive in one answer: the hosts with what runs on each,
// and the HA services that move between them.
type HiveTopology struct {
	Hosts    []Host              `json:"hosts"`
	Services []backend.HAService `json:"services"`
}

// HiveTopology is the hive as the apps draw it: every host with what runs on
// it. The agents an HA service keeps up are a cluster of their own (Kind
// "ha"), once each, saying which node runs them now; standbys and service
// IPs are left out. The queen's hive_hosts and the hosts tool read
// fullTopology.
func (a *App) HiveTopology() HiveTopology { return onlyWhatWorks(withoutHA(a.fullTopology())) }

// fullTopology lists the hosts and the HA services together, read at the
// same moment, with every HA detail.
func (a *App) fullTopology() HiveTopology {
	svcs := a.haServices()
	return HiveTopology{Hosts: a.hostsWith(svcs), Services: svcs}
}

// Hosts lists every host as the apps draw it (HiveTopology's hosts),
// parents before the hosts inside them.
func (a *App) Hosts() []Host { return a.HiveTopology().Hosts }

// withoutHA gathers the agents an HA service keeps up into one cluster host
// per service, each agent once, live when it runs on any node, and drops the
// rest of what only HA knows: standby and spare copies, a host known only as
// an HA node that is left with nothing on it, and the services.
func withoutHA(t HiveTopology) HiveTopology {
	haNode := map[string]bool{}
	for _, s := range t.Services {
		haNode[hostID(s.Active)] = true
		for _, b := range s.Standbys {
			haNode[hostID(b.Host)] = true
		}
	}
	parents := map[string]bool{}
	for _, h := range t.Hosts {
		if h.Parent != "" {
			parents[h.Parent] = true
		}
	}
	clouds := map[string]*Host{}
	var order []string
	hosts := make([]Host, 0, len(t.Hosts))
	for _, h := range t.Hosts {
		kept := make([]HostAgent, 0, len(h.Agents))
		for _, ag := range h.Agents {
			if ag.Kind == hostAgentNamed && ag.Service != "" {
				c := clouds[ag.Service]
				if c == nil {
					c = &Host{ID: hostKindHA + ":" + hostID(ag.Service), Name: ag.Service, Kind: hostKindHA, State: "configured", LastSeen: h.LastSeen}
					clouds[ag.Service] = c
					order = append(order, ag.Service)
				}
				if ag.State == "live" {
					c.State, c.On = "live", h.Name
				}
				c.joinHA(ag)
				continue
			}
			if ag.State != "standby" && ag.State != "standby-unready" && !ag.spare {
				kept = append(kept, ag)
			}
		}
		// Nothing left on it, and nothing inside it: a node that only held
		// HA agents, or one only HA knows of.
		if len(kept) == 0 && !parents[h.ID] && (len(h.Agents) > 0 || h.State == "configured" && (haNode[h.ID] || haNode[hostID(h.Hostname)])) {
			continue
		}
		h.Agents = kept
		hosts = append(hosts, h)
	}
	sort.Strings(order)
	for _, n := range order {
		c := clouds[n]
		// What HA says outranks who reported the agent: the service runs on
		// its active node, and so does every agent in it.
		for _, sv := range t.Services {
			if sv.Name == n && sv.Active != "" && sv.State != "down" {
				c.State, c.On = "live", sv.Active
				for i := range c.Agents {
					c.Agents[i].State = "live"
				}
			}
		}
		hosts = append(hosts, *c)
	}
	return HiveTopology{Hosts: hosts, Services: []backend.HAService{}}
}

// joinHA puts an HA agent on its cluster once: live wins over a standby or
// configured copy, and a copy that is not running shows as configured.
func (c *Host) joinHA(ag HostAgent) {
	ag.spare = false
	if ag.State != "live" {
		ag.State, ag.Activity = "configured", ""
	}
	for i := range c.Agents {
		e := &c.Agents[i]
		if e.Name != ag.Name {
			continue
		}
		if ag.State == "live" && e.State != "live" {
			about := e.About
			*e = ag
			if e.About == "" {
				e.About = about
			}
		} else if e.About == "" {
			e.About = ag.About
		}
		return
	}
	c.Agents = append(c.Agents, ag)
}

// onlyWhatWorks keeps the agents that can do work, and the hosts only as the
// places they are. A SuperLeo with no model of its own (a machine or relay
// agent) thinks nothing: it is how the queen reaches that machine, to look at
// it or run what is installed there, not a member. So it is not drawn, and a
// host with nothing left on it, nor inside it, is not either. The CLIs and
// named agents it runs stay: claude on that box works on its own account.
// The queen's tools read fullTopology and still see everything.
func onlyWhatWorks(t HiveTopology) HiveTopology {
	parent := map[string]string{}
	running := map[string]bool{} // named agents seen running somewhere
	for _, h := range t.Hosts {
		parent[h.ID] = h.Parent
		for _, ag := range h.Agents {
			if ag.Kind == hostAgentNamed && ag.State != "configured" {
				running[ag.Name] = true
			}
		}
	}
	keep := map[string]bool{}
	hosts := make([]Host, 0, len(t.Hosts))
	for _, h := range t.Hosts {
		kept := make([]HostAgent, 0, len(h.Agents))
		for _, ag := range h.Agents {
			if !canWork(ag, running) {
				continue
			}
			kept = append(kept, ag)
		}
		h.Agents = kept
		if len(kept) > 0 {
			for id := h.ID; id != "" && !keep[id]; id = parent[id] {
				keep[id] = true
			}
		}
		hosts = append(hosts, h)
	}
	out := hosts[:0]
	for _, h := range hosts {
		if keep[h.ID] {
			out = append(out, h)
		}
	}
	t.Hosts = out
	return t
}

// canWork is whether an agent on a host is one that can take work.
//   - A SuperLeo that is a machine or a relay agent has no model: no.
//   - A coding CLI the SuperLeo on its host cannot start is no worker, however
//     busy someone keeps it there.
//   - A coding CLI that is only installed counts when it is one that can say
//     it is signed in (claude, codex): a signed-out one is never reported
//     (localAgentNames). One that cannot say counts once it is seen in use.
//   - A named agent only the settings know of, on a host where it is not
//     running, is a spare address for one running elsewhere: no.
func canWork(ag HostAgent, running map[string]bool) bool {
	switch ag.Kind {
	case hostAgentSuperLeo:
		return ag.Role != agentRoleMachine && ag.Role != agentRoleRelay
	case hostAgentCLI:
		if ag.Unrunnable {
			return false
		}
		if ag.State != "installed" {
			return true
		}
		return signedInChecks[agentCLIName(ag)] != nil
	case hostAgentNamed:
		return !(ag.State == "configured" && running[ag.Name])
	}
	return true
}

// selfAgentName is what this process is called among the hosts' agents, and
// what its own CLIs are addressed by ("claude.<it>"): its hive name, the host
// it runs on when it is in a hive under none, "core" alone.
func selfAgentName(s *backend.Settings, self backend.HostIdentity) string {
	if s == nil {
		return "core"
	}
	if n := strings.TrimSpace(s.Hive.Name); n != "" {
		return n
	}
	if s.Hive.Role != "" {
		return self.Name
	}
	return "core"
}

// agentCLIName is a CLI agent's own name: "claude.mac" is claude.
func agentCLIName(ag HostAgent) string {
	if ag.Via != "" && strings.HasSuffix(ag.Name, "."+ag.Via) {
		return strings.TrimSuffix(ag.Name, "."+ag.Via)
	}
	name, _, _ := strings.Cut(ag.Name, ".")
	return name
}

func (a *App) hostsWith(services []backend.HAService) []Host {
	a.mu.Lock()
	s, h := a.settings, a.hive
	a.mu.Unlock()
	b := newHostBook()

	// This process, where it runs.
	self := backend.DetectHost()
	selfName, selfRole := selfAgentName(s, self), "standalone"
	if s != nil && s.Hive.Role != "" {
		selfRole = s.Hive.Role
	}
	me := b.host(self.Name, "", self.Kind, self.OS, self.Arch, self.Parent)
	me.live(time.Now())
	var mcp []string
	if svc := a.svc; svc != nil {
		mcp = svc.MCPServersWithTools()
	}
	b.agent(me, HostAgent{Name: selfName, Kind: hostAgentSuperLeo, Role: selfRole, State: "live", Version: buildVersion(), MCP: mcp, Abilities: a.localAbilities()})
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
			b.agent(x, HostAgent{Name: m.Name, Kind: hostAgentSuperLeo, Role: backend.HiveRoleWorker, About: about,
				State: m.State, Version: m.Version, MCP: m.MCP, Abilities: abilitiesOfMember(m)})
		}
	}

	// Everything on the agent link: machines, relays, and the worker pods
	// that also link (the same host and the same agent as their roster row).
	role := a.agentRoles()
	for _, c := range a.agents().list() {
		hello := c.hello
		r := role(c.name(), helloRole(hello))
		x := b.linked(c.name(), hello, r)
		x.live(time.UnixMilli(c.lastSeen.Load()))
		b.agent(x, HostAgent{Name: c.name(), Kind: hostAgentSuperLeo, Role: r, State: "live", Version: hello.GetVersion(), Abilities: abilitiesFrom(hello)})
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
		b.agent(x, HostAgent{Name: name, Kind: hostAgentSuperLeo, Role: r, State: "lost", Version: hello.GetVersion()})
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
	// What haify calls each node's address, on the hosts already drawn.
	for _, sv := range services {
		for hn, addr := range sv.Addresses {
			if x := b.find(hn); x != nil {
				x.addAddress(addr)
			}
		}
	}

	// Agents the settings reach over ssh. A destination is matched to a host
	// already drawn by its name or address, or by the hostname its machine
	// gave (sds@192.168.123.227 is sds-b); one that matches none is a host of
	// its own, unless an HA service runs the agent: the service says which
	// hosts it is on, and an unmatched destination is one of those by
	// another name.
	//
	// An agent installed on several machines (one host each, or no probe) is
	// on each of them. One that follows one host at a time (several hosts and
	// a probe: the openclaw cluster's) runs on the host its probe succeeds on
	// and stands by on the others; while nobody knows where, or it runs on
	// none, the apps draw it once, on its first host. Every destination stays
	// on the full list: "pi.sds-a" asks pi there whatever runs where.
	if s != nil && s.RemoteAgents.Enabled {
		where := a.remoteSpotsNow(s, services)
		for _, n := range s.RemoteAgents.Names() {
			ra := s.RemoteAgents.Agents[n]
			if ra.URL != "" {
				continue
			}
			ha := haServes(services, n)
			follows := !ha && ra.FollowsOneHost()
			for _, h := range ra.Hosts {
				// Installed beside this SuperLeo: on this machine, and up as
				// long as this SuperLeo is.
				if backend.IsLocalHost(h) && !me.hasNamed(n) {
					b.agent(me, HostAgent{Name: n, Kind: hostAgentNamed, About: ra.About, State: "live"})
				}
			}
			at, known := where.active[n]
			drawn := false
			for _, dest := range sshDests(ra) {
				hn, name := sshHost(dest), where.names[dest]
				x := b.sshHostOf(hn, name)
				if x == nil {
					if ha {
						continue
					}
					if name != "" && isIP(hn) {
						x = b.host(name, "", "", "", "", "")
					} else {
						x = b.host(hn, "", "", "", "", "")
					}
				}
				if name != "" && isIP(hn) {
					x.addAddress(strings.Trim(hn, "[]"))
				}
				g := HostAgent{Name: n, Kind: hostAgentNamed, About: ra.About, State: "configured"}
				switch {
				case follows && known && at == dest:
					x.live(where.at)
					g.State = "live"
				case follows && known && at != "":
					x.configured()
					g.State = "standby"
				default:
					x.configured()
					g.spare = follows && drawn
					drawn = true
				}
				if follows {
					g.Service = haCluster(ra, n)
				}
				if x.hasNamed(n) {
					x.placeNamed(g)
					if follows {
						setService(x, n, g.Service)
					}
					continue
				}
				b.agent(x, g)
			}
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

// sshHost is the machine part of an ssh destination, the target being the
// last word: "sds@192.168.123.212" is 192.168.123.212, and
// "-F ~/.lima/sds-a/ssh.config lima-sds-a" is lima-sds-a.
func sshHost(dest string) string {
	f := strings.Fields(dest)
	if len(f) == 0 {
		return ""
	}
	dest = f[len(f)-1]
	if i := strings.LastIndex(dest, "@"); i >= 0 {
		dest = dest[i+1:]
	}
	return dest
}

func loopback(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// hostKeys is what a host name can be matched by: itself, lower case, and
// for a name that is not an address, its first label ("sds-e.lan" is
// sds-e) and, for a Lima VM, the instance ("lima-sds-a" is sds-a).
func hostKeys(name string) []string {
	n := hostID(name)
	if n == "" {
		return nil
	}
	out := []string{n}
	if net.ParseIP(n) == nil {
		if i := strings.IndexByte(n, '.'); i > 0 {
			out = append(out, n[:i])
		}
		if t := strings.TrimPrefix(n, "lima-"); t != n && t != "" {
			out = append(out, t)
		}
	}
	return out
}

// hostIs says whether name (an ssh destination's machine, an address, a
// hostname) means x: exactly, when exact is set, else by any of their keys.
func hostIs(x *Host, name string, exact bool) bool {
	own := append([]string{x.ID, x.Name, x.Hostname}, x.Addresses...)
	if exact {
		n := hostID(name)
		for _, o := range own {
			if n != "" && hostID(o) == n {
				return true
			}
		}
		return false
	}
	have := map[string]bool{}
	for _, o := range own {
		for _, k := range hostKeys(o) {
			have[k] = true
		}
	}
	for _, k := range hostKeys(name) {
		if have[k] {
			return true
		}
	}
	return false
}

// findHost is the host in hs that name means: an exact match first, then
// one by the looser keys.
func findHost(hs []*Host, name string) *Host {
	for _, exact := range []bool{true, false} {
		for _, x := range hs {
			if hostIs(x, name, exact) {
				return x
			}
		}
	}
	return nil
}

// find is the host already in the book that name means, by name or address.
func (b *hostBook) find(name string) *Host {
	hs := make([]*Host, 0, len(b.order))
	for _, id := range b.order {
		hs = append(hs, b.byID[id])
	}
	return findHost(hs, name)
}

func (x *Host) addAddress(a string) {
	for _, e := range x.Addresses {
		if e == a {
			return
		}
	}
	x.Addresses = append(x.Addresses, a)
}

// hasNamed says whether a named agent of this name is on x already, in any
// state.
func (x *Host) hasNamed(name string) bool {
	for _, g := range x.Agents {
		if g.Kind == hostAgentNamed && g.Name == name {
			return true
		}
	}
	return false
}

// placeNamed sets what the settings' lookup found for the named agent of g's
// name already on x (a relay's, or an HA service's): running here wins over
// any other word, standing by here wins over a relay's, and a spare stays
// one only while nothing says it runs here.
func (x *Host) placeNamed(g HostAgent) {
	for i := range x.Agents {
		e := &x.Agents[i]
		if e.Kind != hostAgentNamed || e.Name != g.Name {
			continue
		}
		switch g.State {
		case "live":
			e.State = "live"
		case "standby":
			if e.State == "live" || e.State == "configured" {
				e.State, e.Activity = "standby", ""
			}
		default:
			e.spare = g.spare && e.State == "configured"
		}
		if e.About == "" {
			e.About = g.About
		}
	}
}

// mergeCLIs folds a coding CLI into the named agent of the same name on x:
// the process detector reports openclaw (and hermes, pi) as a CLI where a
// relay serves it as a named agent, and it is one agent. The named agent
// stays, carrying what the CLI is doing.
func (x *Host) mergeCLIs() {
	named := map[string]int{}
	for i, g := range x.Agents {
		if g.Kind == hostAgentNamed {
			named[g.Name] = i
		}
	}
	if len(named) == 0 {
		return
	}
	out := make([]HostAgent, 0, len(x.Agents))
	for _, g := range x.Agents {
		if g.Kind == hostAgentCLI {
			bare := strings.TrimSuffix(g.Name, "."+g.Via)
			if i, ok := named[bare]; ok {
				n := &x.Agents[i]
				if n.State != "standby" && n.State != "standby-unready" {
					switch g.State {
					case actWorking, actWaiting, actIdle:
						n.Activity = g.State
					}
				}
				if n.Via == "" {
					n.Via = g.Via
				}
				continue
			}
		}
		out = append(out, g)
	}
	// The named agents were updated in place above, before the copy.
	for i := range out {
		if out[i].Kind == hostAgentNamed {
			out[i] = x.Agents[named[out[i].Name]]
		}
	}
	x.Agents = out
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

// linked is the host a linked SuperLeo runs on. A machine agent's link name is
// the name people gave that box, so the host goes by it.
func (b *hostBook) linked(name string, hello *agentlinkpb.Hello, role string) *Host {
	kind := hello.GetKind()
	if kind == "" && hello.GetOs() == "darwin" {
		kind = backend.HostKindMac
	}
	x := b.host(hello.GetHost(), name, kind, hello.GetOs(), hello.GetArch(), hello.GetParent())
	// A box its own agent names (the Mac, orange1) goes by that name, even
	// when it also joined the hive as a worker under its hostname first.
	if machineToo(role, hello) {
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
		if e.Abilities == nil {
			e.Abilities = g.Abilities
		}
		return
	}
	x.Agents = append(x.Agents, g)
}

// clis adds the coding CLIs installed on x, run for core by via, with what
// each is doing.
func (b *hostBook) clis(x *Host, via string, names []string, act cliMap) {
	seen := map[string]bool{}
	add := func(cli string, runnable bool) {
		// An older agent still reports what SuperLeo no longer offers.
		if seen[cli] || retiredCLIs[cli] {
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
		b.agent(x, HostAgent{Name: cli + "." + via, Kind: hostAgentCLI, State: st, Via: via, Unrunnable: !runnable})
	}
	for _, n := range names {
		add(n, true)
	}
	// Running but not found on PATH (a CLI started from elsewhere) is still here.
	extra := make([]string, 0, len(act))
	for n := range act {
		extra = append(extra, n)
	}
	sort.Strings(extra)
	for _, n := range extra {
		add(n, false)
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

// haCluster names the HA cluster an agent that follows one host belongs to:
// the unit its probe asks systemd about ("openclaw" for systemctl is-active
// --quiet openclaw), else the agent itself.
func haCluster(ra backend.RemoteAgent, name string) string {
	f := strings.Fields(ra.Probe)
	if len(f) >= 3 && f[0] == "systemctl" && f[1] == "is-active" {
		unit := f[len(f)-1]
		if !strings.HasPrefix(unit, "-") {
			return strings.TrimSuffix(unit, ".service")
		}
	}
	return name
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
		x.mergeCLIs()
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
// agents under it. A host inside another says which one in words: the list
// is sorted by depth, so the pods come after every top-level host and an
// indent alone reads as "inside the host above".
func hostsText(hs []Host) string {
	if len(hs) == 0 {
		return "No hosts."
	}
	names := map[string]string{}
	for _, h := range hs {
		names[h.ID] = h.Name
	}
	var b strings.Builder
	for _, h := range hs {
		pad := ""
		kind := h.Kind
		if kind == "" {
			kind = "host"
		}
		fmt.Fprintf(&b, "%s (%s", h.Name, kind)
		if h.Parent != "" {
			p := names[h.Parent]
			if p == "" {
				p = h.Parent
			}
			fmt.Fprintf(&b, " on %s", p)
		}
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
			if g.Activity != "" {
				fmt.Fprintf(&b, " (%s)", g.Activity)
			}
			if len(g.MCP) > 0 {
				fmt.Fprintf(&b, ", mcp %s", strings.Join(g.MCP, " "))
			}
			if len(g.Abilities) > 0 {
				fmt.Fprintf(&b, ", can %s", strings.Join(abilityLine(g.Abilities), " "))
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
