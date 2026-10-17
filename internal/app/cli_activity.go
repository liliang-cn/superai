package app

// What every coding CLI on a machine is doing, not only Claude Code: the
// Mac's opencode, codex and pi sessions looked idle in the hive while they
// worked. Each CLI is known one of two ways:
//
//   - precisely, by events its own extension point sends to the node's hook
//     socket (cli set on the event): opencode's plugin, pi's extension,
//     codex's hooks (cli_hook_install.go). Source "plugin".
//   - roughly, by the node looking at the processes running on the machine
//     every few seconds (cli_procs.go): a CLI process using the CPU is
//     working, one that is not is idle. Source "process".
//
// A CLI with sessions from its plugin is shown by those; one without, by its
// processes. Claude Code keeps its own hook record (cchook_activity.go) and
// is shown from it; its processes count only when its hooks said nothing.
//
// What travels is what cc:activity carries, per CLI: a session's id, the
// last element of its working directory, its state, the name of the tool it
// is on, and when. Never a prompt, a file, or a tool's arguments.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// cliActivityEvent is the agent link event one CLI's snapshot travels as,
// and the event core tells the screens with: {agent, cli, activity}.
const cliActivityEvent = "cli:activity"

// Where a CLI's snapshot comes from.
const (
	cliSourcePlugin  = "plugin"  // the CLI's own plugin, extension or hooks
	cliSourceProcess = "process" // the node's look at running processes
)

// maxCLIs bounds how many CLIs one machine may report.
const maxCLIs = 24

// CLIActivity is one CLI's sessions on a machine, as CCActivity is Claude
// Code's, and where that comes from.
type CLIActivity struct {
	CCActivity
	Source string `json:"source"`
}

// cliActivityMsg is the agent link payload of cli:activity.
type cliActivityMsg struct {
	CLI      string       `json:"cli"`
	Activity *CLIActivity `json:"activity"`
}

// cliTracker is this machine's CLIs other than Claude Code's hook record.
type cliTracker struct {
	mu sync.Mutex
	// hooked is each CLI's sessions as its plugin told them.
	hooked map[string]*ccActivity
	// procs is each CLI's sessions as the last process look saw them; polled
	// says a look has been made, so a CLI with no process is known gone.
	procs  map[string][]CCActivitySession
	polled bool
	// ccHasSessions says whether Claude Code's hooks have any session: then
	// claude's processes are not shown.
	ccHasSessions func() bool

	gap      time.Duration
	lastSent map[string]time.Time
	timers   map[string]*time.Timer
	sentSig  map[string]string
	// send hands one CLI's snapshot on (nil: it has none); nil until set.
	send func(cli string, act *CLIActivity)
}

func newCLITracker() *cliTracker {
	return &cliTracker{
		hooked: map[string]*ccActivity{}, procs: map[string][]CCActivitySession{},
		gap: ccActGap, lastSent: map[string]time.Time{}, timers: map[string]*time.Timer{}, sentSig: map[string]string{},
	}
}

var (
	cliTrackOnce sync.Once
	cliTrackV    *cliTracker
)

// cliTrackerState is the process's one CLI tracker.
func cliTrackerState() *cliTracker {
	cliTrackOnce.Do(func() {
		cliTrackV = newCLITracker()
		cliTrackV.ccHasSessions = func() bool {
			t := ccActivityState()
			t.mu.Lock()
			defer t.mu.Unlock()
			return len(t.sessions) > 0
		}
	})
	return cliTrackV
}

// cliName is a CLI's name as it travels: lower case, a bounded word.
func cliName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 32 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return ""
		}
	}
	return s
}

// observe moves the CLI session ev is about, and says whether anything a
// snapshot shows changed.
func (t *cliTracker) observe(ev ccHookEvent, now time.Time) (string, bool) {
	cli := cliName(ev.CLI)
	if cli == "" || cli == "claude" {
		return "", false
	}
	t.mu.Lock()
	h := t.hooked[cli]
	if h == nil {
		if len(t.hooked) >= maxCLIs {
			t.mu.Unlock()
			return "", false
		}
		h = newCCActivity()
		t.hooked[cli] = h
	}
	t.mu.Unlock()
	return cli, h.observe(ev, now)
}

// setProcs takes a process look: every CLI's sessions, by name. It returns
// the CLIs whose sessions changed.
func (t *cliTracker) setProcs(procs map[string][]CCActivitySession) []string {
	return t.setProcsAlive(procs, nil)
}

// setProcsAlive is setProcs with every process the look saw, by pid: a
// plugin session whose process is not among them is over.
func (t *cliTracker) setProcsAlive(procs map[string][]CCActivitySession, alive map[int]bool) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var changed []string
	seen := map[string]bool{}
	for cli, ss := range procs {
		seen[cli] = true
		if sessionsSig(t.procs[cli]) != sessionsSig(ss) {
			changed = append(changed, cli)
		}
	}
	for cli := range t.procs {
		if !seen[cli] {
			changed = append(changed, cli)
		}
	}
	// A CLI with no process left has no plugin sessions either: it exited
	// without saying goodbye.
	for cli, h := range t.hooked {
		if len(procs[cli]) > 0 || !knownProcCLI(cli) {
			if alive != nil {
				h.mu.Lock()
				for id, s := range h.sessions {
					if s.pid > 0 && !alive[s.pid] {
						delete(h.sessions, id)
						changed = append(changed, cli)
					}
				}
				h.mu.Unlock()
			}
			continue
		}
		h.mu.Lock()
		if len(h.sessions) > 0 {
			h.sessions = map[string]*ccActSession{}
			if !seen[cli] {
				changed = append(changed, cli)
			}
		}
		h.mu.Unlock()
	}
	t.procs, t.polled = procs, true
	return dedupe(changed)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// sessionsSig is what a list of sessions says, without the clocks.
func sessionsSig(ss []CCActivitySession) string {
	a := CCActivity{Sessions: append([]CCActivitySession{}, ss...)}
	sortActivity(&a)
	return activitySig(a)
}

// current is one CLI's snapshot: its plugin's sessions when it has any,
// else its processes; nil for neither.
func (t *cliTracker) current(cli string, now time.Time) *CLIActivity {
	t.mu.Lock()
	h := t.hooked[cli]
	procs := append([]CCActivitySession{}, t.procs[cli]...)
	ccHas := t.ccHasSessions
	t.mu.Unlock()
	if h != nil {
		if snap := h.snapshot(now); len(snap.Sessions) > 0 {
			return &CLIActivity{CCActivity: snap, Source: cliSourcePlugin}
		}
	}
	if len(procs) == 0 || (cli == "claude" && ccHas != nil && ccHas()) {
		return nil
	}
	act := CLIActivity{CCActivity: CCActivity{Sessions: procs, At: now}, Source: cliSourceProcess}
	sortActivity(&act.CCActivity)
	return &act
}

// clis is every CLI the tracker knows anything about.
func (t *cliTracker) clis() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for c := range t.hooked {
		out = append(out, c)
	}
	for c := range t.procs {
		out = append(out, c)
	}
	return dedupe(out)
}

func cliSig(a *CLIActivity) string {
	if a == nil {
		return ""
	}
	return a.Source + "\n" + activitySig(a.CCActivity)
}

// kick asks for cli's snapshot to go out, at most one a gap apart.
func (t *cliTracker) kick(cli string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timers[cli] != nil {
		return
	}
	wait := t.gap - time.Since(t.lastSent[cli])
	if wait < 0 {
		wait = 0
	}
	t.timers[cli] = time.AfterFunc(wait, func() { t.flush(cli) })
}

// resend sends every CLI's snapshot again: a core that just took the link
// has not heard them.
func (t *cliTracker) resend() {
	t.mu.Lock()
	for k := range t.sentSig {
		t.sentSig[k] = "\x00"
	}
	t.mu.Unlock()
	for _, c := range t.clis() {
		t.kick(c)
	}
}

func (t *cliTracker) flush(cli string) {
	now := time.Now()
	act := t.current(cli, now)
	sig := cliSig(act)
	t.mu.Lock()
	t.timers[cli] = nil
	send := t.send
	prev, said := t.sentSig[cli]
	if send == nil || (said && sig == prev) || (!said && act == nil) {
		t.mu.Unlock()
		return
	}
	t.sentSig[cli], t.lastSent[cli] = sig, now
	t.mu.Unlock()
	send(cli, act)
}

// sweep settles plugin sessions gone quiet, like Claude Code's.
func (t *cliTracker) sweep(now time.Time) []string {
	t.mu.Lock()
	hooked := make(map[string]*ccActivity, len(t.hooked))
	for k, v := range t.hooked {
		hooked[k] = v
	}
	t.mu.Unlock()
	var changed []string
	for cli, h := range hooked {
		if h.sweep(now, nil) {
			changed = append(changed, cli)
		}
	}
	return changed
}

func (t *cliTracker) loop(ctx context.Context) {
	tick := time.NewTicker(ccActSweep)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, c := range t.sweep(time.Now()) {
				t.kick(c)
			}
		}
	}
}

// ---- the machine's side

// startCLIActivity wires the CLI tracker to the agent link (or this
// machine's own screens), and keeps it swept and the processes looked at
// until ctx ends.
func (a *App) startCLIActivity(ctx context.Context) {
	t := cliTrackerState()
	t.mu.Lock()
	t.send = a.sendCLIActivity
	t.mu.Unlock()
	go t.loop(ctx)
	go a.pollCLIProcs(ctx, t)
}

// sendCLIActivity hands one CLI's snapshot to core, or keeps it here when
// this SuperAI is not linked.
func (a *App) sendCLIActivity(cli string, act *CLIActivity) {
	if tap := a.agentTap.Load(); tap != nil {
		raw, err := json.Marshal(cliActivityMsg{CLI: cli, Activity: act})
		if err != nil {
			return
		}
		var payload map[string]any
		if json.Unmarshal(raw, &payload) == nil {
			(*tap)(cliActivityEvent, payload)
		}
		return
	}
	a.storeSelfCLI(cli, act)
}

// observeCLIActivity moves this machine's record of one CLI for one plugin
// event.
func observeCLIActivity(ev ccHookEvent) {
	t := cliTrackerState()
	if cli, changed := t.observe(ev, time.Now()); changed {
		t.kick(cli)
	}
}

// ---- core's side

// cliMap is a machine's CLIs by name; copied on write.
type cliMap = map[string]CLIActivity

// sanitizeCLI bounds what a machine sent about one CLI.
func sanitizeCLI(a CLIActivity) CLIActivity {
	out := CLIActivity{CCActivity: sanitizeActivity(a.CCActivity), Source: a.Source}
	if out.Source != cliSourceProcess {
		out.Source = cliSourcePlugin
	}
	return out
}

// putCLI stores one CLI's snapshot (nil or empty removes it) in m and
// returns the new map.
func putCLI(cur *cliMap, cli string, act *CLIActivity) (cliMap, *CLIActivity) {
	next := cliMap{}
	if cur != nil {
		for k, v := range *cur {
			next[k] = v
		}
	}
	var stored *CLIActivity
	if act != nil {
		s := sanitizeCLI(*act)
		if len(s.Sessions) > 0 && (len(next) < maxCLIs || next[cli].Source != "") {
			next[cli] = s
			stored = &s
		}
	}
	if stored == nil {
		delete(next, cli)
	}
	return next, stored
}

// noteCLIActivity takes one CLI's snapshot from a linked agent.
func (a *App) noteCLIActivity(c *agentConn, payloadJSON string) {
	var m cliActivityMsg
	if err := json.Unmarshal([]byte(payloadJSON), &m); err != nil {
		return
	}
	cli := cliName(m.CLI)
	if cli == "" {
		return
	}
	c.cliMu.Lock()
	next, stored := putCLI(c.clis.Load(), cli, m.Activity)
	c.clis.Store(&next)
	c.cliMu.Unlock()
	if cli == "claude" && c.activity.Load() != nil {
		return // its hooks speak for it
	}
	a.emit(cliActivityEvent, map[string]any{"agent": c.name(), "cli": cli, "activity": cliOrNil(stored)})
}

// storeSelfCLI keeps this machine's own CLI snapshot and tells the screens.
func (a *App) storeSelfCLI(cli string, act *CLIActivity) {
	a.cliSelfMu.Lock()
	next, stored := putCLI(a.cliSelf.Load(), cli, act)
	a.cliSelf.Store(&next)
	a.cliSelfMu.Unlock()
	if cli == "claude" && a.ccSelfActivity.Load() != nil {
		return
	}
	a.emit(cliActivityEvent, map[string]any{"agent": "core", "cli": cli, "activity": cliOrNil(stored)})
}

// emitClaudeCLI tells the screens claude's entry moved, from its hooks.
func (a *App) emitClaudeCLI(agent string, act *CCActivity, procs *cliMap) {
	var out *CLIActivity
	if act != nil {
		out = &CLIActivity{CCActivity: *act, Source: cliSourcePlugin}
	} else if procs != nil {
		if p, ok := (*procs)["claude"]; ok {
			out = &p
		}
	}
	a.emit(cliActivityEvent, map[string]any{"agent": agent, "cli": "claude", "activity": cliOrNil(out)})
}

func cliOrNil(p *CLIActivity) any {
	if p == nil {
		return nil
	}
	return *p
}

// mergeCLIs is a machine's CLIs with claude's hook record laid over its
// processes.
func mergeCLIs(procs *cliMap, cc *CCActivity) cliMap {
	out := cliMap{}
	if procs != nil {
		for k, v := range *procs {
			out[k] = v
		}
	}
	if cc != nil {
		out["claude"] = CLIActivity{CCActivity: *cc, Source: cliSourcePlugin}
	}
	return out
}

// cliActivity is this agent's CLIs, claude's from its hooks.
func (c *agentConn) cliActivity() cliMap {
	return mergeCLIs(c.clis.Load(), c.activity.Load())
}

// CLIActivityAll is every machine's (and relay's) CLIs that have a session,
// by machine name; "core" is this one.
func (a *App) CLIActivityAll() map[string]cliMap {
	out := map[string]cliMap{}
	if m := mergeCLIs(a.cliSelf.Load(), a.ccSelfActivity.Load()); len(m) > 0 {
		out["core"] = m
	}
	for _, c := range a.agents().list() {
		if m := c.cliActivity(); len(m) > 0 {
			out[c.name()] = m
		}
	}
	return out
}

// machineCLIs is one machine's CLIs, "core" or "" for this one; nil for none.
func (a *App) machineCLIs(name string) cliMap {
	var m cliMap
	if isCoreMachine(name) {
		m = mergeCLIs(a.cliSelf.Load(), a.ccSelfActivity.Load())
	} else if c := a.agents().get(strings.TrimSpace(name)); c != nil {
		m = c.cliActivity()
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// RelayAgent is an agent kept for the named agents it serves
// (agent_link.relay_only): not a machine, but what it serves and what is
// running there are drawn as their own group.
type RelayAgent struct {
	Name        string              `json:"name"`
	Host        string              `json:"host"`
	Agents      []map[string]string `json:"agents"`
	CLIActivity cliMap              `json:"cliActivity"`
}

// Relays lists the relay agents connected now.
func (a *App) Relays() []RelayAgent {
	out := []RelayAgent{}
	role := a.agentRoles()
	for _, c := range a.agents().list() {
		if role(c.name(), c.hello.GetRole()) != agentRoleRelay {
			continue
		}
		r := RelayAgent{Name: c.name(), Host: c.hello.GetHost(), Agents: []map[string]string{}, CLIActivity: c.cliActivity()}
		for _, n := range c.hello.GetAgents() {
			r.Agents = append(r.Agents, map[string]string{"name": n.GetName(), "about": n.GetAbout()})
		}
		out = append(out, r)
	}
	return out
}
