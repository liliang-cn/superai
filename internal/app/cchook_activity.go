package app

// What the interactive Claude Code sessions on a machine are doing, for the
// hive to see. The Mac's claude used to look idle in the 3D world while
// several terminal sessions were hard at work: only runs SuperAI started
// itself counted. Every hook event now also moves a small per-session
// record on the machine — working, waiting for the person, idle; the tool it
// is on — and the machine sends core a snapshot of those records, at most
// once a second, over the agent link ("cc:activity").
//
// What travels is deliberately thin: a session's id, the last element of its
// working directory, its state, the name of the tool it is on and how many
// it has used this turn, and when. Never a prompt, a file, a tool's
// arguments or a turn's text — the hook does not even read them for the
// events that only move this record.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ccActivityEvent is the agent link event a machine's activity snapshot
// travels as, and the event core tells the screens with.
const ccActivityEvent = "cc:activity"

// ccPermissionAnswered is the phone's allow or deny reaching a held
// permission prompt: the turn goes on. Not a hook; the machine's own.
const ccPermissionAnswered = "PermissionAnswered"

// Events only other CLIs' plugins send (cli_activity.go).
const (
	cliWorking   = "Working"   // a turn is running
	cliWaitInput = "WaitInput" // it asked the person a question
)

// A session's state, as the hive shows it.
const (
	actWorking = "working" // a turn is running: thinking, or a tool
	actWaiting = "waiting" // stopped on the person: a permission prompt, or its input
	actIdle    = "idle"    // the turn ended; nothing asked of anyone
)

// Why a waiting session waits.
const (
	actWaitPermission = "permission"
	actWaitInput      = "input"
)

const (
	// ccActGap is the least time between two snapshots: tool events come
	// many a second in a busy turn, and the line wants one update at most.
	ccActGap = time.Second
	// ccActQuietThinking is how long a working session with no tool running
	// may go without a hook event before it is taken as idle. Stop does not
	// fire on an interrupt (Esc), and a closed terminal sends nothing.
	ccActQuietThinking = 10 * time.Minute
	// ccActQuietTool is the same for a session inside a tool call: a build or
	// a test run can take a while, but not this long without a word.
	ccActQuietTool = time.Hour
	// ccActInputFor is how long "waiting for your input" stays news; after
	// that the session is simply idle.
	ccActInputFor = 10 * time.Minute
	// ccActForget is how long a session nothing was heard from is kept.
	ccActForget = 3 * time.Hour
	// ccActSweep is how often the machine looks for sessions gone quiet.
	ccActSweep = 30 * time.Second
	// maxActSessions bounds one snapshot.
	maxActSessions = 32
	// maxActField bounds a project or tool name.
	maxActField = 64
)

// CCActivity is a machine's interactive Claude Code sessions, in brief: how
// many are working and waiting for the person, and each one's state.
type CCActivity struct {
	Working  int                 `json:"working"`
	Waiting  int                 `json:"waiting"`
	Idle     int                 `json:"idle"`
	Sessions []CCActivitySession `json:"sessions"`
	// At is when the machine made this snapshot.
	At time.Time `json:"at"`
}

// CCActivitySession is one session in a snapshot.
type CCActivitySession struct {
	ID string `json:"id"`
	// Project is the last element of its working directory, nothing more.
	Project string `json:"project,omitempty"`
	// State is working, waiting or idle; Wait says why a waiting one waits:
	// permission or input.
	State string `json:"state"`
	Wait  string `json:"wait,omitempty"`
	// Tool is the tool it is on (or last used, this turn), by name.
	Tool string `json:"tool,omitempty"`
	// Tools is how many tool calls this turn has made.
	Tools int `json:"tools"`
	// Since is when it came to this state; Started when it was first heard
	// of; Last when it was last heard from.
	Since   time.Time `json:"since"`
	Started time.Time `json:"started"`
	Last    time.Time `json:"last"`
}

// ccActSession is a session as the machine tracks it.
type ccActSession struct {
	CCActivitySession
	tty    string
	inTool bool // a PreToolUse has not had its PostToolUse yet
}

// ccActivity is this machine's sessions and the throttle on telling core.
type ccActivity struct {
	mu       sync.Mutex
	sessions map[string]*ccActSession
	gap      time.Duration
	lastSent time.Time
	timer    *time.Timer
	sentSig  string
	// send hands a snapshot on; nil until the app sets it.
	send func(CCActivity)
	// alive is the terminals that still hold an agent, for dropping a session
	// whose terminal closed; nil skips the check.
	alive func() map[string]bool
}

func newCCActivity() *ccActivity {
	return &ccActivity{sessions: map[string]*ccActSession{}, gap: ccActGap}
}

var (
	ccActOnce sync.Once
	ccActV    *ccActivity
)

// ccActivityState is the process's one activity tracker.
func ccActivityState() *ccActivity {
	ccActOnce.Do(func() { ccActV = newCCActivity() })
	return ccActV
}

// clipField trims s to a bounded single-line name.
func clipField(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxActField {
		r := []rune(s)
		s = string(r[:maxActField-1]) + "…"
	}
	return s
}

// observe moves the session ev is about and says whether anything a
// snapshot shows changed.
func (t *ccActivity) observe(ev ccHookEvent, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := strings.TrimSpace(ev.Session)
	if id == "" {
		return false
	}
	if ev.Event == "SessionEnd" {
		if _, ok := t.sessions[id]; !ok {
			return false
		}
		delete(t.sessions, id)
		return true
	}
	s := t.sessions[id]
	if s == nil {
		s = &ccActSession{CCActivitySession: CCActivitySession{ID: id, State: actIdle, Since: now, Started: now}}
		t.sessions[id] = s
	}
	before := s.CCActivitySession
	s.Last = now
	if p := projectName(ev.Cwd); p != "" {
		s.Project = clipField(p)
	}
	if ev.TTY != "" {
		s.tty = ev.TTY
	}
	set := func(state, wait string) {
		if s.State != state || s.Wait != wait {
			s.State, s.Wait, s.Since = state, wait, now
		}
	}
	switch ev.Event {
	case "SessionStart":
		// A new, resumed or cleared session waits for its first prompt.
		if s.State != actWorking {
			set(actIdle, "")
		}
	case "UserPromptSubmit":
		set(actWorking, "")
		s.Tool, s.Tools, s.inTool = "", 0, false
	case "PreToolUse":
		set(actWorking, "")
		if tool := clipField(ev.Tool); tool != "" {
			s.Tool = tool
		}
		s.Tools++
		s.inTool = true
	case "PostToolUse":
		set(actWorking, "")
		s.inTool = false
	case "PermissionRequest":
		set(actWaiting, actWaitPermission)
		if tool := clipField(ev.Tool); tool != "" {
			s.Tool = tool
		}
		s.inTool = true
	case ccPermissionAnswered:
		set(actWorking, "")
	case cliWorking:
		// Another CLI's plugin: the session is busy (a turn running), with
		// nothing said about a prompt or a tool.
		set(actWorking, "")
	case cliWaitInput:
		// Another CLI's plugin: it asked the person a question.
		set(actWaiting, actWaitInput)
		s.inTool = false
	case "Notification":
		switch ev.Kind {
		case "permission_prompt":
			set(actWaiting, actWaitPermission)
		case "idle_prompt":
			// Claude has waited a while for the next prompt. An interrupted
			// turn fires no Stop; this is where it shows it is over.
			set(actWaiting, actWaitInput)
			s.inTool = false
		}
	case "Stop", "StopFailure":
		s.inTool = false
		if ev.Resumed {
			// A parked answer was handed back: the turn goes straight on.
			set(actWorking, "")
			s.Tool, s.Tools = "", 0
			break
		}
		set(actIdle, "")
	}
	t.trim()
	return s.CCActivitySession != before
}

// trim keeps the most recently heard sessions when there are too many.
func (t *ccActivity) trim() {
	if len(t.sessions) <= maxActSessions {
		return
	}
	all := make([]*ccActSession, 0, len(t.sessions))
	for _, s := range t.sessions {
		all = append(all, s)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Last.After(all[j].Last) })
	for _, s := range all[maxActSessions:] {
		delete(t.sessions, s.ID)
	}
}

// sweep settles sessions that have gone quiet and forgets the ones long
// gone, and says whether anything changed. live, when not nil, is the
// terminals that still hold an agent: a session whose terminal closed is
// over, SessionEnd or not.
func (t *ccActivity) sweep(now time.Time, live map[string]bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := false
	for id, s := range t.sessions {
		quiet := now.Sub(s.Last)
		switch {
		case quiet > ccActForget, live != nil && s.tty != "" && !live[s.tty]:
			delete(t.sessions, id)
			changed = true
			continue
		case s.State == actWorking && !s.inTool && quiet > ccActQuietThinking,
			s.State == actWorking && s.inTool && quiet > ccActQuietTool,
			s.State == actWaiting && s.Wait == actWaitInput && now.Sub(s.Since) > ccActInputFor:
			s.State, s.Wait, s.Since, s.inTool = actIdle, "", now, false
			changed = true
		}
	}
	return changed
}

// hasTTYs says whether any session has a terminal a sweep can check.
func (t *ccActivity) hasTTYs() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.sessions {
		if s.tty != "" {
			return true
		}
	}
	return false
}

// snapshot is the sessions as they travel: working ones first, then waiting,
// then idle, each group most recently heard first.
func (t *ccActivity) snapshot(now time.Time) CCActivity {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotLocked(now)
}

func (t *ccActivity) snapshotLocked(now time.Time) CCActivity {
	out := CCActivity{Sessions: []CCActivitySession{}, At: now}
	for _, s := range t.sessions {
		out.Sessions = append(out.Sessions, s.CCActivitySession)
	}
	sortActivity(&out)
	return out
}

var actRank = map[string]int{actWorking: 0, actWaiting: 1, actIdle: 2}

// sortActivity orders the sessions and counts them.
func sortActivity(a *CCActivity) {
	sort.Slice(a.Sessions, func(i, j int) bool {
		x, y := a.Sessions[i], a.Sessions[j]
		if actRank[x.State] != actRank[y.State] {
			return actRank[x.State] < actRank[y.State]
		}
		return x.Last.After(y.Last)
	})
	a.Working, a.Waiting, a.Idle = 0, 0, 0
	for _, s := range a.Sessions {
		switch s.State {
		case actWorking:
			a.Working++
		case actWaiting:
			a.Waiting++
		default:
			a.Idle++
		}
	}
}

// activitySig is what a snapshot says, leaving out the clocks that move on
// every event: two snapshots with one sig look the same on every screen.
func activitySig(a CCActivity) string {
	var b strings.Builder
	for _, s := range a.Sessions {
		b.WriteString(s.ID + "|" + s.Project + "|" + s.State + "|" + s.Wait + "|" + s.Tool + "|" + s.Since.Format(time.RFC3339Nano) + "\n")
	}
	return b.String()
}

// kick asks for a snapshot to go out: at once when the last one left more
// than a gap ago, else at the end of the gap, carrying everything that
// happened in it. Many events in a second are one snapshot.
func (t *ccActivity) kick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		return // one is already due; it will carry this change too
	}
	wait := t.gap - time.Since(t.lastSent)
	if wait < 0 {
		wait = 0
	}
	t.timer = time.AfterFunc(wait, t.flush)
}

// resend sends the current snapshot even when it says what the last one
// did: core lost it (the link dropped and came back).
func (t *ccActivity) resend() {
	t.mu.Lock()
	t.sentSig = "\x00"
	t.mu.Unlock()
	t.kick()
}

func (t *ccActivity) flush() {
	t.mu.Lock()
	t.timer = nil
	now := time.Now()
	snap := t.snapshotLocked(now)
	sig := activitySig(snap)
	send := t.send
	if sig == t.sentSig || send == nil {
		t.mu.Unlock()
		return
	}
	t.sentSig, t.lastSent = sig, now
	t.mu.Unlock()
	send(snap)
}

// ccActivityLoop settles quiet sessions every ccActSweep until ctx ends.
func (t *ccActivity) loop(ctx context.Context) {
	tick := time.NewTicker(ccActSweep)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			var live map[string]bool
			if t.alive != nil && t.hasTTYs() {
				live = t.alive()
			}
			if t.sweep(time.Now(), live) {
				t.kick()
			}
		}
	}
}

// ---- the machine's side: where a snapshot goes

// startCCActivity wires this process's tracker to the agent link, or, on a
// SuperAI not linked to a core, to its own screens, and keeps it swept until
// ctx ends.
func (a *App) startCCActivity(ctx context.Context) {
	t := ccActivityState()
	t.mu.Lock()
	t.send = a.sendCCActivity
	if t.alive == nil {
		t.alive = ccLocalState().liveTTYs
	}
	t.mu.Unlock()
	go t.loop(ctx)
}

// sendCCActivity hands a snapshot to core over the link, or keeps it here as
// this machine's own when there is no link.
func (a *App) sendCCActivity(act CCActivity) {
	if tap := a.agentTap.Load(); tap != nil {
		raw, err := json.Marshal(act)
		if err != nil {
			return
		}
		var payload map[string]any
		if json.Unmarshal(raw, &payload) == nil {
			(*tap)(ccActivityEvent, payload)
		}
		return
	}
	a.storeCCActivity("", act)
}

// observeCCActivity moves this machine's activity for one hook event.
func observeCCActivity(ev ccHookEvent) {
	t := ccActivityState()
	if t.observe(ev, time.Now()) {
		t.kick()
	}
}

// ---- core's side: what each machine said

// sanitizeActivity bounds what a machine sent and counts it again, so a
// confused or old agent cannot put more on the screens than a snapshot holds.
func sanitizeActivity(a CCActivity) CCActivity {
	out := CCActivity{At: a.At, Sessions: []CCActivitySession{}}
	for _, s := range a.Sessions {
		if len(out.Sessions) >= maxActSessions {
			break
		}
		if strings.TrimSpace(s.ID) == "" {
			continue
		}
		switch s.State {
		case actWorking, actIdle:
			s.Wait = ""
		case actWaiting:
			if s.Wait != actWaitPermission && s.Wait != actWaitInput {
				s.Wait = actWaitInput
			}
		default:
			continue
		}
		s.ID, s.Project, s.Tool = clipField(s.ID), clipField(s.Project), clipField(s.Tool)
		if s.Tools < 0 {
			s.Tools = 0
		}
		out.Sessions = append(out.Sessions, s)
	}
	sortActivity(&out)
	return out
}

// noteCCActivity takes a snapshot from a linked agent.
func (a *App) noteCCActivity(c *agentConn, payloadJSON string) {
	var act CCActivity
	if err := json.Unmarshal([]byte(payloadJSON), &act); err != nil {
		return
	}
	act = sanitizeActivity(act)
	if len(act.Sessions) == 0 {
		c.activity.Store(nil)
	} else {
		c.activity.Store(&act)
	}
	a.emit(ccActivityEvent, map[string]any{"agent": c.name(), "activity": activityOrNil(c.activity.Load())})
	a.emitClaudeCLI(c.name(), c.activity.Load(), c.clis.Load())
}

// storeCCActivity keeps this machine's own snapshot (agent "") and tells the
// screens.
func (a *App) storeCCActivity(agent string, act CCActivity) {
	act = sanitizeActivity(act)
	if len(act.Sessions) == 0 {
		a.ccSelfActivity.Store(nil)
	} else {
		a.ccSelfActivity.Store(&act)
	}
	name := agent
	if name == "" {
		name = "core"
	}
	a.emit(ccActivityEvent, map[string]any{"agent": name, "activity": activityOrNil(a.ccSelfActivity.Load())})
	a.emitClaudeCLI(name, a.ccSelfActivity.Load(), a.cliSelf.Load())
}

func activityOrNil(p *CCActivity) any {
	if p == nil {
		return nil
	}
	return *p
}

// CCActivityAll is every machine's Claude Code activity that has a session
// in it, by machine name; "core" is this one.
func (a *App) CCActivityAll() map[string]CCActivity {
	out := map[string]CCActivity{}
	if p := a.ccSelfActivity.Load(); p != nil {
		out["core"] = *p
	}
	for _, c := range a.agents().list() {
		if p := c.activity.Load(); p != nil {
			out[c.name()] = *p
		}
	}
	return out
}

// machineActivity is one machine's activity, "core" or "" for this one.
func (a *App) machineActivity(name string) *CCActivity {
	if isCoreMachine(name) {
		return a.ccSelfActivity.Load()
	}
	if c := a.agents().get(strings.TrimSpace(name)); c != nil {
		return c.activity.Load()
	}
	return nil
}
