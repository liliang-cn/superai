package app

// Standing agents: agents that are never finished.
//
// A standing agent is told what should stay true and wakes to keep it true —
// on a clock, when an event reaches it, when it asked to be woken — runs one
// ordinary turn, keeps notes for next time, tells the person what they should
// know, and sleeps. The engine is agent-go's Standing (pkg/agent/standing.go):
// the wakes, the notes, the daily ceilings. This file is what SuperAI adds
// around it:
//
//   - who it is: a name and a look (a glyph on a coloured disc);
//   - three tiers of permission — what it does on its own, what it asks
//     about, what it may never do — and which connectors (MCP servers) it may
//     use at all, all enforced by the tool gate, not by asking the model;
//   - triggers agent-go does not have: a cron expression, and a webhook any
//     service can POST an event to;
//   - where it reports: the app's notices (and through them the phone), the
//     push webhook, Telegram.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
	"github.com/robfig/cron/v3"
)

// AgentSpec is a standing agent as the person describes it.
type AgentSpec struct {
	ID string `json:"id"`
	// Who it is.
	Name  string `json:"name"`
	Glyph string `json:"glyph,omitempty"` // one character or emoji on its disc
	Hue   string `json:"hue,omitempty"`   // cyan | blue | violet | rose | orange | green
	// What it is for.
	Goal      string   `json:"goal"`
	Watch     []string `json:"watch,omitempty"`
	Attention string   `json:"attention,omitempty"`
	Never     []string `json:"never,omitempty"` // in words, for the model
	// Permissions, as tool-name patterns ("bash", "fs_*", "mcp:github").
	Auto   []string `json:"auto,omitempty"`
	Ask    []string `json:"ask,omitempty"`
	Forbid []string `json:"forbid,omitempty"`
	// Connectors are the MCP servers it may use. AllConnectors overrides the
	// list; with neither, it has no MCP tools.
	AllConnectors bool     `json:"allConnectors"`
	Connectors    []string `json:"connectors,omitempty"`
	// When it wakes, besides when it asks to and when an event arrives.
	Cron             string `json:"cron,omitempty"`
	EveryMinutes     int    `json:"everyMinutes,omitempty"`
	ScanEveryMinutes int    `json:"scanEveryMinutes,omitempty"`
	// Where it reports.
	Report AgentReport `json:"report"`
	// Ceilings.
	MaxWakesPerDay int `json:"maxWakesPerDay,omitempty"`
	// HookSecret is the last part of the agent's webhook URL.
	HookSecret string    `json:"hookSecret,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// AgentReport is where an agent's messages go. The app always gets them.
type AgentReport struct {
	Push     bool `json:"push"`
	Telegram bool `json:"telegram"`
}

// AgentView is a standing agent with what it is doing.
type AgentView struct {
	AgentSpec
	Paused       bool        `json:"paused"`
	PausedReason string      `json:"pausedReason,omitempty"`
	Notes        string      `json:"notes,omitempty"`
	Running      *agent.Wake `json:"running,omitempty"`
	LastWake     *agent.Wake `json:"lastWake,omitempty"`
	NextDue      *time.Time  `json:"nextDue,omitempty"`
	NextDueKind  string      `json:"nextDueKind,omitempty"`
	WakesToday   int         `json:"wakesToday"`
	// HookPath is where an event can be POSTed, relative to this server.
	HookPath string `json:"hookPath,omitempty"`
	// WaitingFor names the tool its wake is waiting for you to approve.
	WaitingFor string `json:"waitingFor,omitempty"`
}

// AgentReportEntry is one thing an agent told the person.
type AgentReportEntry struct {
	Agent   string    `json:"agent"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

const agentReportsKept = 300

// agentWakesKept bounds the wake log: enough for a day of hourly wakes per
// agent across a handful of agents, which is what the watch track draws.
const agentWakesKept = 600

// WakeMark is one wake on an agent's watch track.
type WakeMark struct {
	Agent     string     `json:"agent"`
	Kind      string     `json:"kind"`
	Reason    string     `json:"reason,omitempty"`
	Started   time.Time  `json:"started"`
	Ended     *time.Time `json:"ended,omitempty"`
	ToolCalls int        `json:"toolCalls"`
	Error     string     `json:"error,omitempty"`
	Notified  int        `json:"notified"`
}

type standingHost struct {
	mu      sync.Mutex
	st      *agent.Standing
	specs   map[string]AgentSpec
	cron    *cron.Cron
	entries map[string]cron.EntryID
	reports []AgentReportEntry
	wakes   []WakeMark
	reason  string // why there is no engine, when there is none
}

func agentsPath() string  { return filepath.Join(backend.DataDir(), "agents.json") }
func reportsPath() string { return filepath.Join(backend.DataDir(), "agent-reports.json") }
func wakesPath() string   { return filepath.Join(backend.DataDir(), "agent-wakes.json") }

func (a *App) standing() *standingHost {
	a.standingOnce.Do(func() {
		h := &standingHost{specs: map[string]AgentSpec{}, entries: map[string]cron.EntryID{}}
		if raw, err := os.ReadFile(agentsPath()); err == nil {
			_ = json.Unmarshal(raw, &h.specs)
		}
		if raw, err := os.ReadFile(reportsPath()); err == nil {
			_ = json.Unmarshal(raw, &h.reports)
		}
		if raw, err := os.ReadFile(wakesPath()); err == nil {
			_ = json.Unmarshal(raw, &h.wakes)
		}
		a.standingHost = h
	})
	return a.standingHost
}

func (h *standingHost) saveSpecsLocked() error {
	raw, err := json.MarshalIndent(h.specs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(agentsPath(), raw, 0o600)
}

// startStanding builds the engine on the current service. Only the process
// that fires schedules runs standing agents, for the same reason: two
// processes would wake each one twice.
func (a *App) startStanding() {
	a.stopStanding()
	a.mu.Lock()
	svc := a.svc
	lock := a.scheduleLock
	a.mu.Unlock()
	h := a.standing()
	if svc == nil || svc.Agent() == nil {
		h.mu.Lock()
		h.reason = "the backend is not ready"
		h.mu.Unlock()
		return
	}
	if lock == nil {
		h.mu.Lock()
		h.reason = "another SuperAI process on this machine runs the standing agents"
		h.mu.Unlock()
		return
	}
	st, err := agent.NewStanding(svc.Agent(), agent.WithNotifier(agent.NotifierFunc(a.onStandingNotice)))
	if err != nil {
		h.mu.Lock()
		h.reason = err.Error()
		h.mu.Unlock()
		return
	}
	c := cron.New()
	h.mu.Lock()
	h.st, h.cron, h.reason = st, c, ""
	h.entries = map[string]cron.EntryID{}
	for id, sp := range h.specs {
		h.scheduleLocked(a, id, sp.Cron)
	}
	h.mu.Unlock()
	c.Start()
	svc.Gate().SetSessionRule(a.standingRule)
}

func (a *App) stopStanding() {
	h := a.standing()
	h.mu.Lock()
	st, c := h.st, h.cron
	h.st, h.cron = nil, nil
	h.mu.Unlock()
	if c != nil {
		c.Stop()
	}
	if st != nil {
		st.Close()
	}
}

// scheduleLocked (re)installs one agent's cron trigger.
func (h *standingHost) scheduleLocked(a *App, id, expr string) {
	if old, ok := h.entries[id]; ok && h.cron != nil {
		h.cron.Remove(old)
		delete(h.entries, id)
	}
	if strings.TrimSpace(expr) == "" || h.cron == nil {
		return
	}
	eid, err := h.cron.AddFunc(expr, func() {
		h.mu.Lock()
		st := h.st
		h.mu.Unlock()
		if st != nil {
			_ = st.WakeNow(context.Background(), id, "its schedule ("+expr+") came round")
		}
	})
	if err == nil {
		h.entries[id] = eid
	}
}

func (h *standingHost) engine() (*agent.Standing, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st == nil {
		if h.reason != "" {
			return nil, errors.New("standing agents are not running here: " + h.reason)
		}
		return nil, errors.New("standing agents are not running here")
	}
	return h.st, nil
}

// StandingAgents lists every standing agent with what it is doing.
func (a *App) StandingAgents() []AgentView {
	h := a.standing()
	h.mu.Lock()
	specs := make([]AgentSpec, 0, len(h.specs))
	for _, s := range h.specs {
		specs = append(specs, s)
	}
	st := h.st
	h.mu.Unlock()
	sort.Slice(specs, func(i, j int) bool { return specs[i].CreatedAt.Before(specs[j].CreatedAt) })
	waiting := a.waitingSessions()
	out := make([]AgentView, 0, len(specs))
	for _, sp := range specs {
		v := viewOf(sp, st)
		if v.Running != nil {
			v.WaitingFor = waiting[v.Running.SessionID]
		}
		out = append(out, v)
	}
	return out
}

// waitingSessions maps a session to the tool it is waiting on approval for.
func (a *App) waitingSessions() map[string]string {
	a.approvalMu.Lock()
	defer a.approvalMu.Unlock()
	out := map[string]string{}
	for _, p := range a.approvals {
		out[p.req.SessionID] = p.req.Tool
	}
	return out
}

func viewOf(sp AgentSpec, st *agent.Standing) AgentView {
	v := AgentView{AgentSpec: sp}
	v.HookSecret = ""
	if sp.HookSecret != "" {
		v.HookPath = agentHookPrefix + sp.ID + "/" + sp.HookSecret
	}
	if st == nil {
		return v
	}
	if s, ok := st.Get(sp.ID); ok {
		v.Paused, v.PausedReason, v.Notes = s.Responsibility.Paused, s.Responsibility.PausedReason, s.Responsibility.Notes
		v.Running, v.LastWake = s.Running, s.LastWake
		if !s.NextDue.IsZero() {
			t := s.NextDue
			v.NextDue, v.NextDueKind = &t, string(s.NextDueKind)
		}
		v.WakesToday = s.WakesToday
	}
	return v
}

// SaveStandingAgent creates or updates one.
func (a *App) SaveStandingAgent(sp AgentSpec) (AgentView, error) {
	sp.Name = strings.TrimSpace(sp.Name)
	sp.Goal = strings.TrimSpace(sp.Goal)
	if sp.Name == "" {
		return AgentView{}, errors.New("give it a name")
	}
	if sp.Goal == "" {
		return AgentView{}, errors.New("say what it should keep true")
	}
	if sp.Cron = strings.TrimSpace(sp.Cron); sp.Cron != "" {
		if _, err := cronParser.Parse(sp.Cron); err != nil {
			return AgentView{}, fmt.Errorf("the schedule %q is not a valid cron: %v", sp.Cron, err)
		}
	}
	if sp.EveryMinutes < 0 || sp.ScanEveryMinutes < 0 || sp.MaxWakesPerDay < 0 {
		return AgentView{}, errors.New("intervals and ceilings cannot be negative")
	}
	if err := tiersConflict(sp); err != nil {
		return AgentView{}, err
	}
	st, err := a.standing().engine()
	if err != nil {
		return AgentView{}, err
	}

	h := a.standing()
	h.mu.Lock()
	prev, exists := h.specs[sp.ID]
	h.mu.Unlock()
	if sp.ID == "" || !exists {
		sp.ID = ""
		sp.CreatedAt = time.Now()
	} else {
		sp.CreatedAt = prev.CreatedAt
		sp.HookSecret = prev.HookSecret
	}
	if sp.HookSecret == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		sp.HookSecret = hex.EncodeToString(b)
	}

	r := agent.Responsibility{ID: sp.ID}
	if exists {
		if cur, ok := st.Get(sp.ID); ok {
			if cur.Running != nil {
				return AgentView{}, errors.New("it is awake right now; change it once this wake is over")
			}
			r = cur.Responsibility // keeps its notes, its asked-for wake, its history
		}
	}
	r.Name, r.Goal, r.Watch, r.Attention = sp.Name, sp.Goal, sp.Watch, sp.Attention
	r.Never = append(append([]string{}, sp.Never...), forbiddenText(sp)...)
	r.ToolDenylist = exactNames(sp.Forbid)
	r.Every = time.Duration(sp.EveryMinutes) * time.Minute
	r.Scan = nil
	if sp.ScanEveryMinutes > 0 {
		r.Scan = &agent.ScanPolicy{Every: time.Duration(sp.ScanEveryMinutes) * time.Minute}
	}
	r.MaxWakesPerDay = sp.MaxWakesPerDay
	saved, err := st.Add(context.Background(), r)
	if err != nil {
		return AgentView{}, err
	}
	sp.ID = saved.ID

	h.mu.Lock()
	h.specs[sp.ID] = sp
	err = h.saveSpecsLocked()
	h.scheduleLocked(a, sp.ID, sp.Cron)
	h.mu.Unlock()
	if err != nil {
		return AgentView{}, err
	}
	v := viewOf(sp, st)
	a.emit("agent:update", map[string]any{"id": sp.ID})
	return v, nil
}

// DeleteStandingAgent forgets one; a wake in flight is cancelled.
func (a *App) DeleteStandingAgent(id string) error {
	h := a.standing()
	if st, err := h.engine(); err == nil {
		if err := st.Remove(context.Background(), id); err != nil && !errors.Is(err, agent.ErrNoResponsibility) {
			return err
		}
	}
	h.mu.Lock()
	delete(h.specs, id)
	keep := h.wakes[:0]
	for _, w := range h.wakes {
		if w.Agent != id {
			keep = append(keep, w)
		}
	}
	h.wakes = keep
	h.scheduleLocked(a, id, "")
	err := h.saveSpecsLocked()
	h.mu.Unlock()
	a.emit("agent:update", map[string]any{"id": id})
	return err
}

// PauseStandingAgent and ResumeStandingAgent stop and restart its wakes.
func (a *App) PauseStandingAgent(id string) error {
	st, err := a.standing().engine()
	if err != nil {
		return err
	}
	err = st.Pause(context.Background(), id, "paused by you")
	a.emit("agent:update", map[string]any{"id": id})
	return err
}

func (a *App) ResumeStandingAgent(id string) error {
	st, err := a.standing().engine()
	if err != nil {
		return err
	}
	err = st.Resume(context.Background(), id)
	a.emit("agent:update", map[string]any{"id": id})
	return err
}

// WakeStandingAgent wakes one now, optionally with something to look at.
func (a *App) WakeStandingAgent(id, message string) error {
	st, err := a.standing().engine()
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) != "" {
		_, err = st.Deliver(context.Background(), id, agent.StandingEvent{Source: "you", Kind: "message", Payload: message})
	} else {
		err = st.WakeNow(context.Background(), id, "you asked it to look now")
	}
	a.emit("agent:update", map[string]any{"id": id})
	return err
}

func (a *App) recordWake(id string, w *agent.Wake) {
	m := WakeMark{Agent: id, Kind: string(w.Kind), Reason: w.Reason, Started: w.StartedAt,
		ToolCalls: w.ToolCalls, Error: w.Error, Notified: w.Notified}
	if !w.EndedAt.IsZero() {
		t := w.EndedAt
		m.Ended = &t
	}
	h := a.standing()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.wakes = append(h.wakes, m)
	if len(h.wakes) > agentWakesKept {
		h.wakes = h.wakes[len(h.wakes)-agentWakesKept:]
	}
	if raw, err := json.Marshal(h.wakes); err == nil {
		_ = os.WriteFile(wakesPath(), raw, 0o600)
	}
}

// StandingWakes is every agent's wakes since a moment, oldest first.
func (a *App) StandingWakes(sinceHours int) []WakeMark {
	if sinceHours <= 0 {
		sinceHours = 24
	}
	from := time.Now().Add(-time.Duration(sinceHours) * time.Hour)
	h := a.standing()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []WakeMark{}
	for _, w := range h.wakes {
		if w.Started.After(from) {
			out = append(out, w)
		}
	}
	return out
}

// StandingReports is what the agents have told the person, newest first.
func (a *App) StandingReports(id string) []AgentReportEntry {
	h := a.standing()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []AgentReportEntry{}
	for i := len(h.reports) - 1; i >= 0; i-- {
		if id == "" || h.reports[i].Agent == id {
			out = append(out, h.reports[i])
		}
	}
	return out
}

// onStandingNotice is the engine's Notifier: a message, a pause, an error.
func (a *App) onStandingNotice(ctx context.Context, n agent.Notification) {
	// A wake starting or ending is the agent's state, not something it said.
	if n.Kind == agent.NotifyWakeStarted || n.Kind == agent.NotifyWakeEnded {
		if n.Kind == agent.NotifyWakeEnded && n.Wake != nil {
			a.recordWake(n.ResponsibilityID, n.Wake)
		}
		a.emit("agent:update", map[string]any{"id": n.ResponsibilityID})
		return
	}
	h := a.standing()
	h.mu.Lock()
	sp := h.specs[n.ResponsibilityID]
	e := AgentReportEntry{Agent: n.ResponsibilityID, Name: sp.Name, Kind: n.Kind, Message: n.Message, At: n.At}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	h.reports = append(h.reports, e)
	if len(h.reports) > agentReportsKept {
		h.reports = h.reports[len(h.reports)-agentReportsKept:]
	}
	if raw, err := json.Marshal(h.reports); err == nil {
		_ = os.WriteFile(reportsPath(), raw, 0o600)
	}
	h.mu.Unlock()

	a.emit("agent:report", map[string]any{
		"agent": e.Agent, "name": e.Name, "kind": e.Kind, "message": e.Message, "at": e.At.Format(time.RFC3339),
		"glyph": sp.Glyph, "hue": sp.Hue,
	})
	a.emit("agent:update", map[string]any{"id": e.Agent})

	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc != nil {
		level := backend.LevelInfo
		if n.Kind == "error" {
			level = backend.LevelError
		}
		name := sp.Name
		if name == "" {
			name = "Standing agent"
		}
		svc.Notices().Raise(ctx, backend.Notice{
			Level: level, Title: name, Message: n.Message, Source: "agent:" + e.Agent,
			Key: "agent:" + e.Agent + ":" + e.At.Format(time.RFC3339Nano), Push: sp.Report.Push,
		})
	}
	if sp.Report.Telegram {
		a.sendTelegram(fmt.Sprintf("%s %s\n%s", glyphOr(sp), sp.Name, n.Message))
	}
}

func glyphOr(sp AgentSpec) string {
	if sp.Glyph != "" {
		return sp.Glyph
	}
	return "●"
}

// standingRule is the tool gate's session rule: a call made by a standing
// agent's wake is judged by that agent's permissions.
func (a *App) standingRule(req agent.PermissionRequest) (backend.Verdict, string) {
	h := a.standing()
	h.mu.Lock()
	st := h.st
	h.mu.Unlock()
	if st == nil || req.SessionID == "" {
		return backend.VerdictNone, ""
	}
	var sp AgentSpec
	found := false
	if req.TaskID != "" {
		h.mu.Lock()
		sp, found = h.specs[req.TaskID]
		h.mu.Unlock()
	}
	for _, s := range st.Status() {
		if found {
			break
		}
		if s.Running != nil && s.Running.SessionID == req.SessionID {
			h.mu.Lock()
			sp, found = h.specs[s.Responsibility.ID]
			h.mu.Unlock()
			break
		}
	}
	if !found {
		return backend.VerdictNone, ""
	}
	return judge(sp, req.ToolName, a.mcpServerNames())
}

// judge applies one agent's permissions to one tool.
func judge(sp AgentSpec, tool string, servers []string) (backend.Verdict, string) {
	name := strings.ToLower(strings.TrimSpace(tool))
	if strings.HasPrefix(name, "standing_") {
		return backend.VerdictAllow, "a standing agent's own bookkeeping"
	}
	if matchAny(sp.Forbid, name, servers) {
		return backend.VerdictDeny, fmt.Sprintf("%s may never use %s", sp.Name, tool)
	}
	if server := mcpServerOf(name, servers); server != "" && !sp.AllConnectors && !containsFold(sp.Connectors, server) {
		return backend.VerdictDeny, fmt.Sprintf("%s is not connected to %s", sp.Name, server)
	}
	if matchAny(sp.Ask, name, servers) {
		return backend.VerdictAsk, fmt.Sprintf("%s asks before using %s", sp.Name, tool)
	}
	if matchAny(sp.Auto, name, servers) {
		return backend.VerdictAllow, fmt.Sprintf("%s may use %s on its own", sp.Name, tool)
	}
	return backend.VerdictNone, ""
}

// matchAny reads patterns as globs over the tool name, and "mcp:<server>" as
// every tool of that MCP server.
func matchAny(patterns []string, name string, servers []string) bool {
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if s, ok := strings.CutPrefix(p, "mcp:"); ok {
			if mcpServerOf(name, servers) == s {
				return true
			}
			continue
		}
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// mcpServerOf finds which configured MCP server a tool name belongs to:
// agent-go names them mcp_<server>_<tool>, and a server name may itself hold
// an underscore, so the longest configured name wins.
func mcpServerOf(name string, servers []string) string {
	rest, ok := strings.CutPrefix(name, "mcp_")
	if !ok {
		return ""
	}
	best := ""
	for _, s := range servers {
		s = strings.ToLower(s)
		if strings.HasPrefix(rest, s+"_") && len(s) > len(best) {
			best = s
		}
	}
	if best == "" {
		if i := strings.IndexByte(rest, '_'); i > 0 {
			return rest[:i]
		}
		return rest
	}
	return best
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), s) {
			return true
		}
	}
	return false
}

// exactNames are the forbidden patterns that name one tool exactly: agent-go
// withholds those from the model altogether.
func exactNames(patterns []string) []string {
	var out []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p != "" && !strings.ContainsAny(p, "*?[:") {
			out = append(out, p)
		}
	}
	return out
}

// forbiddenText says the forbidden tools in words, so the model does not try.
func forbiddenText(sp AgentSpec) []string {
	if len(sp.Forbid) == 0 {
		return nil
	}
	return []string{"use any of these tools: " + strings.Join(sp.Forbid, ", ")}
}

// mcpServerNames are the MCP servers this app is configured with.
func (a *App) mcpServerNames() []string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.MCPServerNames()
}

// sendTelegram reaches every allowed Telegram chat, when the bridge is up.
func (a *App) sendTelegram(text string) {
	a.mu.Lock()
	b := a.telegram
	a.mu.Unlock()
	if b != nil {
		go b.Broadcast(context.Background(), text)
	}
}

// tiersConflict refuses a rule written into two tiers: "on its own" and
// "asks first" both saying *bash* would leave the person guessing which wins.
func tiersConflict(sp AgentSpec) error {
	seen := map[string]string{}
	for _, tier := range []struct {
		name string
		list []string
	}{{"on its own", sp.Auto}, {"asks first", sp.Ask}, {"never", sp.Forbid}} {
		for _, p := range tier.list {
			k := strings.ToLower(strings.TrimSpace(p))
			if k == "" {
				continue
			}
			if prev, ok := seen[k]; ok && prev != tier.name {
				return fmt.Errorf("%s is both %q and %q; keep it in one", p, prev, tier.name)
			}
			seen[k] = tier.name
		}
	}
	return nil
}

// standingNameForSession names the agent whose wake runs in a session, for
// an approval card to say who is asking.
func (a *App) standingNameForSession(session string) string {
	h := a.standing()
	h.mu.Lock()
	st := h.st
	h.mu.Unlock()
	if st == nil || session == "" {
		return ""
	}
	for _, s := range st.Status() {
		if s.Running != nil && s.Running.SessionID == session {
			h.mu.Lock()
			defer h.mu.Unlock()
			sp := h.specs[s.Responsibility.ID]
			if sp.Glyph != "" {
				return sp.Glyph + " " + sp.Name
			}
			return sp.Name
		}
	}
	return ""
}
