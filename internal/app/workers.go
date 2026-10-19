package app

import (
	"strings"
	"sync"
	"time"
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
}

// HiveWorkers is every agent that can work, the queen's own SuperAI aside:
// she hands work out, she is not handed it. Read without the HA services, so
// a status bar can ask often.
func (a *App) HiveWorkers() []HiveWorker {
	t := onlyWhatWorks(withoutHA(HiveTopology{Hosts: a.hostsWith(nil)}))
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
			out = append(out, HiveWorker{Name: ag.Name, Kind: ag.Kind, Host: h.Name, State: ag.State, Up: workerUp(ag.State)})
		}
	}
	return out
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
