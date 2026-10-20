package app

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// Who can work.
//
// A worker is any agent that can take a piece of work and do it: a SuperAI
// with a model of its own, a coding CLI that is signed in, a named agent
// (openclaw, hermes, pi). They are peers; the host each runs on is where it
// is, not what it is. A SuperAI with no model is how the queen reaches a
// machine and is no worker, and neither is a CLI that is installed but signed
// out. The same rule counts a laptop on its own and a hive of machines: alone,
// the workers are the agents on this one.

// HiveWorker is one agent that can work, as the screens count it.
type HiveWorker struct {
	Name string `json:"name"`
	// Kind is superai, cli or agent, as on the host it is on.
	Kind string `json:"kind"`
	// Host is the machine it runs on, by name: where it is, not what it is.
	Host string `json:"host"`
	// State is the agent's own: live or lost for a SuperAI; working,
	// waiting, idle or installed for a CLI; live or configured for a named
	// agent.
	State string `json:"state"`
	// Up is whether it can take work now.
	Up bool `json:"up"`
	// Via is the SuperAI that runs a CLI or a named agent for core.
	Via string `json:"via,omitempty"`
}

// HiveWorkers is every agent that can work, the queen's own SuperAI aside:
// she hands work out, she is not handed it. The same topology the world is
// drawn from, so a count and a picture never disagree.
func (a *App) HiveWorkers() []HiveWorker {
	t := a.HiveTopology()
	out := []HiveWorker{}
	seen := map[string]bool{}
	for _, h := range t.Hosts {
		for _, ag := range h.Agents {
			if ag.Kind == hostAgentSuperAI && ag.Role != agentRoleWorker {
				continue // the queen, or a core on its own
			}
			if seen[ag.Name] {
				continue
			}
			seen[ag.Name] = true
			out = append(out, HiveWorker{Name: ag.Name, Kind: ag.Kind, Host: h.Name, State: ag.State, Up: workerUp(ag.State), Via: ag.Via})
		}
	}
	return out
}

// linkWire reaches the model of a linked agent that has one, over the agent
// link: the same SendChat and event stream a hive worker serves over HTTP,
// on the line the agent holds open to core.
type linkWire struct{ c *agentConn }

func (w linkWire) Where() string { return "agent link · " + w.c.name() }

func (w linkWire) Events(ctx context.Context, _ string) (io.ReadCloser, error) {
	return w.c.eventStream(ctx), nil
}

func (w linkWire) Call(ctx context.Context, method string, args []any) (json.RawMessage, error) {
	var raw json.RawMessage
	err := w.c.call(ctx, method, args, &raw)
	return raw, err
}

// linkedThinker is a linked agent with a model of its own that is not on the
// hive's roster: a worker reached over the link alone.
type linkedThinker struct {
	conn *agentConn
	// have is the MCP servers and CLIs it said it has, for routing.
	have []string
}

// linkedThinkers are the linked agents that think and are not already on
// roster, by name.
func (a *App) linkedThinkers(roster map[string]bool) map[string]linkedThinker {
	out := map[string]linkedThinker{}
	for _, c := range a.agents().list() {
		if !c.hello.GetThinks() || roster[c.name()] {
			continue
		}
		have := append(append([]string{}, c.hello.GetMcp()...), c.hello.GetClis()...)
		out[c.name()] = linkedThinker{conn: c, have: have}
	}
	return out
}

// withLinkedThinkers adds the linked agents that think to a roster as the
// runner sees it, each reached over its link.
func (a *App) withLinkedThinkers(roster map[string]backend.RemoteAgent) map[string]backend.RemoteAgent {
	on := map[string]bool{}
	for n := range roster {
		on[n] = true
	}
	thinkers := a.linkedThinkers(on)
	if len(thinkers) == 0 {
		return roster
	}
	out := make(map[string]backend.RemoteAgent, len(roster)+len(thinkers))
	for n, ag := range roster {
		out[n] = ag
	}
	for n, t := range thinkers {
		out[n] = backend.RemoteAgent{About: "SuperAI on " + t.conn.hello.GetHost(), Wire: linkWire{t.conn}}
	}
	return out
}

// agentWorkers are the workers that are not SuperAIs and can take work now:
// the named agents and the signed-in coding CLIs.
func (a *App) agentWorkers() []HiveWorker {
	var out []HiveWorker
	for _, w := range a.HiveWorkers() {
		if w.Kind != hostAgentSuperAI && w.Up {
			out = append(out, w)
		}
	}
	return out
}

// agentWorker is the named agent or coding CLI called name, if it is one.
func (a *App) agentWorker(name string) (HiveWorker, bool) {
	for _, w := range a.agentWorkers() {
		if w.Name == name {
			return w, true
		}
	}
	return HiveWorker{}, false
}

// selfName is what this process's own CLIs are addressed by.
func (a *App) selfName() string {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	return selfAgentName(s, backend.DetectHost())
}

// cliRunsGoing is how many coding runs the CLI worker called name has going,
// whoever started them.
func (a *App) cliRunsGoing(name string) int {
	self := a.selfName()
	n := 0
	for _, r := range a.CLIRuns() {
		if r.State != "running" {
			continue
		}
		full := r.Agent
		if r.Remote == "" {
			full = r.Agent + "." + self
		}
		if full == name {
			n++
		}
	}
	return n
}

// runOnAgent gives one piece of delegated work to a named agent or a coding
// CLI and waits for its answer, on the task board like a SuperAI's order so
// the screens show it going out. A named agent is asked; a CLI does it as a
// coding run, its permission prompts going to the person.
func (a *App) runOnAgent(ctx context.Context, w HiveWorker, prompt string) backend.RemoteResult {
	board := a.tasks()
	id := backend.NewTaskID()
	board.StartAs(id, w.Name, "", backend.TaskOut, prompt)
	board.InSession(id, backend.ChatSessionFrom(ctx))
	var res backend.RemoteResult
	if w.Kind == hostAgentCLI {
		res = a.runCLIPiece(ctx, w, prompt)
	} else {
		res = a.askAgent(ctx, w.Name, prompt)
	}
	res.Agent, res.TaskID = w.Name, id
	switch {
	case ctx.Err() != nil:
		board.Finish(id, backend.TaskCancelled, res.Text, res.Reason)
	case res.Failed:
		board.Finish(id, backend.TaskFailed, res.Text, res.Reason)
	default:
		board.Finish(id, backend.TaskDone, res.Text, "")
	}
	return res
}

// runCLIPiece runs prompt as a coding run of the CLI worker w and waits for it.
func (a *App) runCLIPiece(ctx context.Context, w HiveWorker, prompt string) backend.RemoteResult {
	started := time.Now()
	res := backend.RemoteResult{Agent: w.Name, Host: w.Host}
	agent := w.Name
	if w.Via == a.selfName() {
		agent = agentCLIName(HostAgent{Name: w.Name, Via: w.Via}) // one of this machine's own
	}
	done := make(chan CLIRun, 1)
	run, err := a.startCLIRun(cliStart{Agent: agent, Prompt: prompt, Ask: true, Done: func(r CLIRun) {
		select {
		case done <- r:
		default:
		}
	}})
	if err != nil {
		res.Failed, res.Reason = true, err.Error()
		return res
	}
	select {
	case r := <-done:
		run = r
	case <-ctx.Done():
		a.CancelCLIRun(run.ID)
		res.Failed, res.Reason = true, "stopped: "+ctx.Err().Error()
		res.MS = time.Since(started).Milliseconds()
		return res
	}
	res.Text, res.MS = strings.TrimSpace(run.Summary), time.Since(started).Milliseconds()
	if run.State != "done" || res.Text == "" {
		res.Failed = true
		res.Reason = run.Error
		if res.Reason == "" {
			res.Reason = w.Name + " ended " + run.State + " without an answer"
		}
	}
	return res
}

// workerUp is whether an agent in this state can take work now.
func workerUp(state string) bool {
	switch state {
	case "lost", "down", "":
		return false
	}
	return true
}

// cliSignIn remembers whether each coding CLI here is signed in. Asking
// takes a second or more, so it is asked in the background and kept for a
// while; until the first answer a CLI counts as usable, which is what it was
// before anyone asked.
var cliSignIn = &signInCache{m: map[string]signInEntry{}, fresh: 10 * time.Minute, checks: signedInChecks}

type signInCache struct {
	mu     sync.Mutex
	m      map[string]signInEntry
	fresh  time.Duration
	checks map[string]func(bin string) bool
}

type signInEntry struct {
	in       bool
	known    bool
	at       time.Time
	checking bool
}

// signedOut says whether the CLI name, at bin, is known to be signed out.
// False for a CLI that cannot say, and for one not asked yet.
func (c *signInCache) signedOut(name, bin string) bool {
	check := c.checks[strings.ToLower(name)]
	if check == nil {
		return false
	}
	key := name + "\x00" + bin
	c.mu.Lock()
	e := c.m[key]
	if !e.checking && (!e.known || time.Since(e.at) > c.fresh) {
		e.checking = true
		c.m[key] = e
		go func() {
			in := check(bin)
			c.mu.Lock()
			c.m[key] = signInEntry{in: in, known: true, at: time.Now()}
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	return e.known && !e.in
}
