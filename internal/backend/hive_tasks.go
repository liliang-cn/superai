package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The hive's task board.
//
// Every order that crosses the hive is a task, and this is where its life is
// written down: sent, thinking, calling a tool, writing the answer, done or
// not. The queen records the orders it gives; a worker records the ones it is
// given. Both publish each change as it happens, which is what lets the Hive
// panel show work in motion instead of a list that was true a few seconds ago.
//
// It is a board and not a log on purpose. Only the last few tasks are kept, in
// memory: a hive's history belongs to the sessions the workers ran, and a
// second copy here would be a second place for it to be wrong.

// HiveSessionPrefix marks a conversation a worker opened because the queen
// asked. It is how a worker knows an order came from the hive rather than from
// a person at its keyboard.
const HiveSessionPrefix = "hive:"

// HiveDirectSessionPrefix marks a conversation a worker opened because the
// person asked it something directly — its card on the phone, the desktop or
// the web — with the queen only carrying the words. It is a hive: session
// all the same (on the board, under the hive's tenant, kept out of the
// worker's own list), but the worker answers the person, not the queen.
const HiveDirectSessionPrefix = HiveSessionPrefix + "direct:"

// IsDirectAsk reports whether a worker's session is the person asking it
// directly rather than an order from the queen.
func IsDirectAsk(session string) bool {
	return strings.HasPrefix(session, HiveDirectSessionPrefix)
}

type directAskKey struct{}

// AskedDirectly marks ctx as the person asking an agent themselves, so a
// worker it reaches is told so (HiveDirectSessionPrefix) instead of being
// handed an order.
func AskedDirectly(ctx context.Context) context.Context {
	return context.WithValue(ctx, directAskKey{}, true)
}

func askedDirectly(ctx context.Context) bool {
	v, _ := ctx.Value(directAskKey{}).(bool)
	return v
}

// HiveTenant is the agent-go tenant every order from the hive runs under on a
// worker, so its runs can be told apart from a person's own turns — and, with
// CancelTenant, stopped together without touching those.
const HiveTenant = "hive"

const (
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"

	// Which way an order went, from this instance's side.
	TaskOut = "out" // this instance gave the order
	TaskIn  = "in"  // this instance received it
	// TaskPeer is an order one worker gave another. The queen sees these
	// because the worker reports them; neither end is the queen.
	TaskPeer = "peer"

	// What a running task is doing right now.
	PhaseThinking = "thinking"
	PhaseTool     = "tool"
	PhaseWriting  = "writing"
)

// boardKeep is how many finished tasks stay on the board.
const boardKeep = 60

// What a task keeps. Generous, because the detail page shows the whole order
// and the whole answer; still bounded, because the board is memory and an
// answer can be as long as a model can write.
const (
	promptKeep = 4000
	resultKeep = 20000
	errorKeep  = 2000
	eventsKeep = 200
	liveKeep   = 400
)

// liveEvery is the least time between two publishes of a task's Live text.
const liveEvery = 200 * time.Millisecond

// TaskEvent is one step in a task's life: when, and what it was doing.
type TaskEvent struct {
	At    time.Time `json:"at"`
	Phase string    `json:"phase"`
	Tool  string    `json:"tool,omitempty"`
}

// HiveTask is one order and where it stands.
//
// The ID is a UUID made once, by whoever gave the order, and kept everywhere the
// order goes: the queen's board, the worker's board, the worker's session
// ("hive:<id>") and the address of the detail page. One order, one name.
type HiveTask struct {
	ID     string `json:"id"`
	Worker string `json:"worker"`
	// From is who gave the order when it was not the queen: the asking worker,
	// for a peer task.
	From string `json:"from,omitempty"`
	Dir  string `json:"dir"`
	// Session is the conversation the order was given in, so the orders one
	// request fanned out into can be shown together.
	Session string `json:"session,omitempty"`
	Prompt  string `json:"prompt"`
	State   string `json:"state"`
	Phase   string `json:"phase,omitempty"`
	// Tool is the tool being called while Phase is "tool"; Tools counts how many
	// have been called so far.
	Tool      string      `json:"tool,omitempty"`
	Tools     int         `json:"tools"`
	StartedAt time.Time   `json:"started_at"`
	EndedAt   time.Time   `json:"ended_at,omitempty"`
	Result    string      `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	Events    []TaskEvent `json:"events,omitempty"`
	// Live is the tail of what the task is doing right now — the tool being
	// called with its arguments, how a call came out, the words being written —
	// for the hologram over the worker. Only while running, only the last
	// liveKeep characters, and published at most every liveEvery.
	Live string `json:"live,omitempty"`

	liveAt    time.Time
	liveTimer *time.Timer
}

// TaskBoard holds the recent tasks and reports each change.
type TaskBoard struct {
	mu      sync.Mutex
	tasks   []*HiveTask
	emit    func(HiveTask)
	pulseFn func(HivePulse)
	// stops are how the running tasks this instance can stop are stopped, by
	// id: an order it gave (the run's context) or one it is carrying out (the
	// turn). Dropped when the task settles.
	stops map[string]func()
}

// ErrTaskStopped is the cause a task's context carries when it was stopped by
// request, which is how a delegation tells "stop this" from "this worker
// failed" and does not hand the piece to someone else.
var ErrTaskStopped = errors.New("the order was stopped on request")

// NewTaskBoard builds a board; emit is called with a copy after every change,
// outside the lock.
func NewTaskBoard(emit func(HiveTask)) *TaskBoard { return &TaskBoard{emit: emit} }

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Start records a new running task and returns its id. Safe on a nil board, so
// callers do not have to know whether anyone is watching.
func (b *TaskBoard) Start(worker, dir, prompt string) string {
	return b.StartFrom(worker, "", dir, prompt)
}

// NewTaskID makes the UUID an order will be known by.
func NewTaskID() string { return uuid.NewString() }

// TaskIDFromSession recovers the order's id from a worker's session name, and
// says whether there was one. A session that merely starts with the prefix but
// carries something else is treated as having none, so a stray "hive:foo"
// cannot become a task's identity.
func TaskIDFromSession(session string) (string, bool) {
	id, ok := strings.CutPrefix(session, HiveDirectSessionPrefix)
	if !ok {
		id, ok = strings.CutPrefix(session, HiveSessionPrefix)
	}
	if !ok {
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

// StartFrom is Start for an order that came from somewhere other than the
// queen.
func (b *TaskBoard) StartFrom(worker, from, dir, prompt string) string {
	return b.StartAs("", worker, from, dir, prompt)
}

// StartAs is StartFrom with the id already decided: the one the order was given
// under, when this board is recording an order made elsewhere. An empty id makes
// a new one.
func (b *TaskBoard) StartAs(id, worker, from, dir, prompt string) string {
	if b == nil {
		return ""
	}
	if id == "" {
		id = uuid.NewString()
	}
	now := time.Now()
	t := &HiveTask{
		ID: id, Worker: worker, From: from, Dir: dir, Prompt: clip(prompt, promptKeep),
		State: TaskRunning, Phase: PhaseThinking, StartedAt: now,
		Events: []TaskEvent{{At: now, Phase: "sent"}, {At: now, Phase: PhaseThinking}},
	}
	b.mu.Lock()
	b.tasks = append(b.tasks, t)
	b.trimLocked()
	c := t.copy()
	b.mu.Unlock()
	b.publish(c)
	return t.ID
}

// Progress notes what a running task is doing. A tool call also counts it.
// InSession records the conversation an order was given in. "" is a no-op.
func (b *TaskBoard) InSession(id, session string) {
	if session == "" {
		return
	}
	b.update(id, func(t *HiveTask) bool {
		if t.Session == session {
			return false
		}
		t.Session = session
		return true
	})
}

type chatSessionKey struct{}

// WithChatSession marks a run's context with the conversation it serves;
// ChatSessionFrom reads it back in a tool that runs within it.
func WithChatSession(ctx context.Context, session string) context.Context {
	return context.WithValue(ctx, chatSessionKey{}, session)
}

// ChatSessionFrom is the conversation a tool call is part of, or "".
func ChatSessionFrom(ctx context.Context) string {
	s, _ := ctx.Value(chatSessionKey{}).(string)
	return s
}

func (b *TaskBoard) Progress(id, phase, tool string) {
	b.update(id, func(t *HiveTask) bool {
		if t.State != TaskRunning {
			return false
		}
		if phase == PhaseTool {
			t.Tools++
		}
		if t.Phase == phase && t.Tool == tool && phase != PhaseTool {
			return false // nothing new to say
		}
		t.Phase, t.Tool = phase, tool
		t.note(phase, tool)
		return true
	})
}

// Live appends to what a running task shows as it works, keeping the last few
// hundred characters. It publishes at most every liveEvery; a change inside
// that window goes out when the window ends.
func (b *TaskBoard) Live(id, s string) {
	if b == nil || id == "" || s == "" {
		return
	}
	b.mu.Lock()
	var t *HiveTask
	for _, x := range b.tasks {
		if x.ID == id {
			t = x
			break
		}
	}
	if t == nil || t.State != TaskRunning {
		b.mu.Unlock()
		return
	}
	r := []rune(t.Live + s)
	if len(r) > liveKeep {
		r = r[len(r)-liveKeep:]
	}
	t.Live = string(r)
	if wait := liveEvery - time.Since(t.liveAt); wait > 0 {
		if t.liveTimer == nil {
			t.liveTimer = time.AfterFunc(wait, func() { b.flushLive(id) })
		}
		b.mu.Unlock()
		return
	}
	t.liveAt = time.Now()
	c := t.copy()
	b.mu.Unlock()
	b.publish(c)
}

func (b *TaskBoard) flushLive(id string) {
	b.mu.Lock()
	var c *HiveTask
	for _, t := range b.tasks {
		if t.ID == id {
			t.liveTimer = nil
			if t.State == TaskRunning {
				t.liveAt = time.Now()
				cp := t.copy()
				c = &cp
			}
			break
		}
	}
	b.mu.Unlock()
	if c != nil {
		b.publish(*c)
	}
}

// Finish settles a task. Only the first call counts: a task that was cancelled
// and then reports done a moment later stays cancelled.
func (b *TaskBoard) Finish(id, state, result, errMsg string) {
	b.update(id, func(t *HiveTask) bool {
		delete(b.stops, id)
		if t.State != TaskRunning {
			return false
		}
		t.State, t.Phase, t.Tool, t.Live = state, "", "", ""
		if t.liveTimer != nil {
			t.liveTimer.Stop()
			t.liveTimer = nil
		}
		t.EndedAt = time.Now()
		t.Result, t.Error = clip(result, resultKeep), clip(errMsg, errorKeep)
		t.note(state, "")
		return true
	})
}

func (b *TaskBoard) update(id string, f func(*HiveTask) bool) {
	if b == nil || id == "" {
		return
	}
	b.mu.Lock()
	var c *HiveTask
	for _, t := range b.tasks {
		if t.ID == id {
			if f(t) {
				cp := t.copy()
				c = &cp
			}
			break
		}
	}
	b.mu.Unlock()
	if c != nil {
		b.publish(*c)
	}
}

func (b *TaskBoard) publish(t HiveTask) {
	if b.emit != nil {
		b.emit(t)
	}
}

// trimLocked drops the oldest finished tasks past the limit. A running task is
// never dropped, however old.
func (b *TaskBoard) trimLocked() {
	if len(b.tasks) <= boardKeep {
		return
	}
	kept := b.tasks[:0]
	over := len(b.tasks) - boardKeep
	for _, t := range b.tasks {
		if over > 0 && t.State != TaskRunning {
			over--
			continue
		}
		kept = append(kept, t)
	}
	b.tasks = kept
}

// Recent is the board, oldest first, for a panel that has just opened and has
// missed the events.
func (b *TaskBoard) Recent() []HiveTask {
	if b == nil {
		return []HiveTask{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]HiveTask, len(b.tasks))
	for i, t := range b.tasks {
		out[i] = t.copy()
	}
	return out
}

// Get finds one task by its id.
func (b *TaskBoard) Get(id string) (HiveTask, bool) {
	if b == nil {
		return HiveTask{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.tasks {
		if t.ID == id {
			return t.copy(), true
		}
	}
	return HiveTask{}, false
}

// OnStop says how a running task is stopped. Only the instance running the
// order (or giving it) can stop it, so a task reported from elsewhere has none.
func (b *TaskBoard) OnStop(id string, stop func()) {
	if b == nil || id == "" || stop == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.tasks {
		if t.ID == id && t.State == TaskRunning {
			if b.stops == nil {
				b.stops = map[string]func(){}
			}
			b.stops[id] = stop
			return
		}
	}
}

// Stoppable says whether Stop would stop the task.
func (b *TaskBoard) Stoppable(id string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stops[id] != nil
}

// Stop stops a running task, or says why it cannot. The task settles as
// cancelled through its own Finish, once whatever ran it has stopped.
func (b *TaskBoard) Stop(id string) error {
	if b == nil {
		return errors.New("there is no task board here")
	}
	b.mu.Lock()
	var found *HiveTask
	for _, t := range b.tasks {
		if t.ID == id {
			found = t
			break
		}
	}
	stop := b.stops[id]
	state := ""
	if found != nil {
		state = found.State
	}
	b.mu.Unlock()
	switch {
	case found == nil:
		return errors.New("no such task here: only recent orders are kept, and not across a restart")
	case state != TaskRunning:
		return fmt.Errorf("the task already ended: %s", state)
	case stop == nil:
		return errors.New("this instance neither gave nor is carrying out that order, so it cannot stop it")
	}
	stop()
	return nil
}

// note appends a step to the timeline, keeping the newest when it is full.
func (t *HiveTask) note(phase, tool string) {
	t.Events = append(t.Events, TaskEvent{At: time.Now(), Phase: phase, Tool: tool})
	if len(t.Events) > eventsKeep {
		t.Events = append([]TaskEvent(nil), t.Events[len(t.Events)-eventsKeep:]...)
	}
}

// copy is a value copy that does not share the timeline, so a task handed out
// is not changed by the next step.
func (t *HiveTask) copy() HiveTask {
	c := *t
	c.liveTimer = nil
	c.Events = append([]TaskEvent(nil), t.Events...)
	return c
}

// Ingest takes a task another instance reports, keyed by its own id, and
// publishes it. A worker's dealings with a peer reach the queen this way, so
// the queen's board — and the panel drawn from it — shows the whole hive and
// not only the orders the queen gave.
//
// A finished task is not reopened by a late report of it running.
func (b *TaskBoard) Ingest(in HiveTask) {
	if b == nil || in.ID == "" {
		return
	}
	if r := []rune(in.Live); len(r) > liveKeep {
		in.Live = string(r[len(r)-liveKeep:])
	}
	in.Prompt = clip(in.Prompt, promptKeep)
	in.Result, in.Error = clip(in.Result, resultKeep), clip(in.Error, errorKeep)
	b.mu.Lock()
	for i, t := range b.tasks {
		if t.ID != in.ID {
			continue
		}
		if t.State != TaskRunning {
			b.mu.Unlock()
			return
		}
		*b.tasks[i] = in
		b.mu.Unlock()
		b.publish(in)
		return
	}
	cp := in
	b.tasks = append(b.tasks, &cp)
	b.trimLocked()
	b.mu.Unlock()
	b.publish(in)
}

// HivePulse is one thing that happened while an order was being carried out: a
// tool called, a result back, the model thinking, some text written. It is not
// stored — the board keeps the story of a task, and this is the flicker on top
// of it — but it is published as it happens, and that is what the hive panel
// draws as light travelling between the two ends of the order.
type HivePulse struct {
	Task   string `json:"task"`
	Worker string `json:"worker"`
	From   string `json:"from,omitempty"`
	Dir    string `json:"dir"`
	// Kind is "thinking", "tool", "result" or "text".
	Kind  string `json:"kind"`
	Tool  string `json:"tool,omitempty"`
	Bytes int    `json:"bytes,omitempty"`
}

// SetPulse says where pulses go. Called once, before use.
func (b *TaskBoard) SetPulse(f func(HivePulse)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.pulseFn = f
	b.mu.Unlock()
}

// Pulse publishes one pulse for a task, filled in with who and where from the
// task itself. A task that is not on the board (or has finished) pulses nothing.
func (b *TaskBoard) Pulse(id, kind, tool string, bytes int) {
	if b == nil || id == "" {
		return
	}
	b.mu.Lock()
	fn := b.pulseFn
	var p HivePulse
	found := false
	for _, t := range b.tasks {
		if t.ID == id && t.State == TaskRunning {
			p = HivePulse{Task: id, Worker: t.Worker, From: t.From, Dir: t.Dir, Kind: kind, Tool: tool, Bytes: bytes}
			found = true
			break
		}
	}
	b.mu.Unlock()
	if found && fn != nil {
		fn(p)
	}
}

// Pulser turns the raw stream of an agent's events into pulses worth drawing.
//
// A model streams text a few characters at a time, many times a second, and a
// panel that drew one beam per chunk would be a blur that says nothing. So a
// tool call and its result are each a pulse, thinking is one at most every
// quarter second, and text is gathered into one pulse every tenth of a second
// that carries how much of it there was.
type Pulser struct {
	fn        func(kind, tool string, bytes int)
	mu        sync.Mutex
	lastThink time.Time
	lastText  time.Time
	pending   int
}

// NewPulser builds one that reports to fn.
func NewPulser(fn func(kind, tool string, bytes int)) *Pulser { return &Pulser{fn: fn} }

// Event takes one agent event: its type, the tool it names, and how many bytes
// of content and of tool result it carried.
func (p *Pulser) Event(typ, tool string, content, result int) {
	if p == nil || p.fn == nil {
		return
	}
	now := time.Now()
	switch typ {
	case "tool_call":
		p.fn("tool", tool, 0)
	case "tool_result":
		p.fn("result", tool, result)
	case "thinking":
		p.mu.Lock()
		ok := now.Sub(p.lastThink) >= 250*time.Millisecond
		if ok {
			p.lastThink = now
		}
		p.mu.Unlock()
		if ok {
			p.fn("thinking", "", 0)
		}
	case "partial":
		p.mu.Lock()
		p.pending += content
		n := 0
		if now.Sub(p.lastText) >= 120*time.Millisecond && p.pending > 0 {
			n, p.pending, p.lastText = p.pending, 0, now
		}
		p.mu.Unlock()
		if n > 0 {
			p.fn("text", "", n)
		}
	}
}

// LiveTool is the line a tool call adds to a task's Live text.
func LiveTool(tool string, args any) string {
	a := ""
	if args != nil {
		if b, err := json.Marshal(args); err == nil && string(b) != "null" && string(b) != "{}" {
			a = " " + string(b)
		}
	}
	return "\n▸ " + tool + clip(a, 120) + "\n"
}

// LiveResult is the line a tool result adds: how it came out, in a few words.
func LiveResult(result any) string {
	if ToolResultFailed(result) {
		return "◂ ✗ " + clip(toolResultError(result), 100) + "\n"
	}
	b, _ := json.Marshal(result)
	return "◂ " + clip(string(b), 100) + "\n"
}
