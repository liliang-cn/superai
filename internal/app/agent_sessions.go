package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liliang-cn/agentexec/reach"
)

// Every coding agent conversation, wherever it runs: the ones SuperLeo
// started (CLIRuns, continued with FollowUpCLIRun) and the ones somebody
// started in a terminal (Claude Code, codex, pi, … as their hooks report
// them). Each one can be read — its own session log on its machine — and
// answered: a run by resuming its session, a terminal one by typing into the
// terminal it is in.

// CodingSession is one conversation in the list.
type CodingSession struct {
	// Machine is the linked machine it runs on, "" for this one.
	Machine string `json:"machine,omitempty"`
	CLI     string `json:"cli"`
	// Session is the CLI's own id; Run the newest SuperLeo run of a thread.
	Session string `json:"session,omitempty"`
	Run     string `json:"run,omitempty"`
	// Source is terminal or superleo.
	Source  string    `json:"source"`
	Project string    `json:"project,omitempty"`
	Title   string    `json:"title,omitempty"`
	State   string    `json:"state"`
	Tool    string    `json:"tool,omitempty"`
	At      time.Time `json:"at"`
	// Readable says its log can be opened; Replyable that an answer has a
	// way in (a run always; a terminal one when its terminal is reachable).
	Readable  bool `json:"readable"`
	Replyable bool `json:"replyable"`
}

// sessionLogCLIs are the CLIs whose logs readSessionLog understands.
var sessionLogCLIs = map[string]bool{"claude": true, "codex": true, "pi": true}

// CodingSessions lists every coding agent conversation, newest first.
func (a *App) CodingSessions() []CodingSession {
	out := []CodingSession{}
	seen := map[string]bool{} // CLI session ids a SuperLeo run owns
	for _, r := range a.cliThreads() {
		out = append(out, r)
		if r.Session != "" {
			seen[r.CLI+"\x00"+r.Session] = true
		}
	}
	add := func(machine, cli string, act CCActivity) {
		for _, s := range act.Sessions {
			if s.ID == "" || seen[cli+"\x00"+s.ID] || strings.HasPrefix(s.ID, "pid:") {
				continue
			}
			seen[cli+"\x00"+s.ID] = true
			at := s.Since
			if at.IsZero() {
				at = act.At
			}
			out = append(out, CodingSession{
				Machine: machine, CLI: cli, Session: s.ID, Source: "terminal", Project: s.Project,
				State: s.State, Tool: s.Tool, At: at,
				Readable: sessionLogCLIs[cli], Replyable: s.State != "ended",
			})
		}
	}
	for machine, clis := range a.CLIActivityAll() {
		m := machine
		if isCoreMachine(m) {
			m = ""
		}
		for cli, act := range clis {
			if act.Source == cliSourceProcess {
				continue // a process seen running: no session, no log
			}
			add(m, cli, act.CCActivity)
		}
	}
	for machine, act := range a.CCActivityAll() {
		m := machine
		if isCoreMachine(m) {
			m = ""
		}
		add(m, "claude", act)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// cliThreads is SuperLeo's own runs, one entry per thread (its newest run).
func (a *App) cliThreads() []CodingSession {
	newest := map[string]CLIRun{}
	for _, r := range a.CLIRuns() {
		if r.Queen {
			continue // the queen thinking through a CLI is her conversation, not a session
		}
		if cur, ok := newest[r.Thread]; !ok || r.Started.After(cur.Started) {
			newest[r.Thread] = r
		}
	}
	out := make([]CodingSession, 0, len(newest))
	for _, r := range newest {
		at := r.Started
		if r.Ended != nil {
			at = *r.Ended
		}
		out = append(out, CodingSession{
			Machine: r.Remote, CLI: r.Agent, Session: r.Session, Run: r.ID, Source: "superai",
			Project: lastElem(r.Cwd), Title: cliClip(r.Prompt, 120), State: r.State, At: at,
			Readable: true, Replyable: r.State != "running",
		})
	}
	return out
}

func lastElem(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// sessionCallTimeout bounds a read or a reply's round trip to a machine.
const sessionCallTimeout = 20 * time.Second

// CodingSessionLog reads a terminal session's recent events on the machine
// it runs on ("" this one).
func (a *App) CodingSessionLog(machine, cli, session string) (SessionLog, error) {
	machine, cli, session = strings.TrimSpace(machine), strings.TrimSpace(cli), strings.TrimSpace(session)
	if machine == "" || isCoreMachine(machine) {
		return readSessionLog(cli, session)
	}
	c := a.agents().get(machine)
	if c == nil {
		return SessionLog{}, fmt.Errorf("%s is not connected", machine)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionCallTimeout)
	defer cancel()
	var out SessionLog
	err := c.call(ctx, "CodingSessionLog", []any{"", cli, session}, &out)
	return out, err
}

// ReplyToCodingSession types an answer into a terminal session on the
// machine it runs on. Claude Code's go by its own route (ReplyToCCSession:
// tmux, Otty, iTerm2, or held for its turn's end); the others by tmux or
// iTerm2, through the terminal their hooks reported.
func (a *App) ReplyToCodingSession(machine, cli, session, text string) (CCDelivery, error) {
	machine, cli, session, text = strings.TrimSpace(machine), strings.TrimSpace(cli), strings.TrimSpace(session), strings.TrimSpace(text)
	if cli == "claude" || cli == "" {
		return a.ReplyToCCSession(CCReplyRequest{Agent: machine, Session: session, Text: text})
	}
	switch {
	case session == "":
		return CCDelivery{}, errors.New("which session?")
	case text == "":
		return CCDelivery{}, errors.New("the answer is empty")
	case utf8.RuneCountInString(text) > maxCCReply:
		return CCDelivery{}, fmt.Errorf("the answer is longer than %d characters", maxCCReply)
	}
	var (
		out CCDelivery
		err error
	)
	if machine == "" || isCoreMachine(machine) {
		out, err = cliReachState().deliver(context.Background(), cli, session, text)
	} else if c := a.agents().get(machine); c == nil {
		err = fmt.Errorf("%s is not connected", machine)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), sessionCallTimeout)
		err = c.call(ctx, "ReplyToCodingSession", []any{"", cli, session, text}, &out)
		cancel()
	}
	ccAudit(ccAuditEntry{At: time.Now(), Agent: machine, Session: cli + ":" + session,
		Chars: utf8.RuneCountInString(text), Delivered: err == nil, Via: out.Via, Error: errString(err)})
	return out, err
}

// cliReach remembers the terminal each non-Claude session's hooks reported,
// for typing an answer into it.
type cliReach struct {
	mu        sync.Mutex
	at        map[string]cliReachSpot // cli + "\x00" + session
	sightings []reach.Sighting
	paneAlive func(string) bool
	typeTmux  func(pane, text string) error
	typeITerm func(ctx context.Context, tty, text string) error
	liveTTYs  func() map[string]bool
}

type cliReachSpot struct {
	TTY, Pane string
}

var (
	cliReachOnce sync.Once
	cliReachV    *cliReach
)

func cliReachState() *cliReach {
	cliReachOnce.Do(func() {
		l := ccLocalState()
		cliReachV = &cliReach{at: map[string]cliReachSpot{}, paneAlive: l.paneAlive, typeTmux: l.typeTmux,
			typeITerm: l.typeITerm, liveTTYs: l.liveTTYs}
	})
	return cliReachV
}

// maxCLIReach bounds how many sessions' terminals are remembered.
const maxCLIReach = 256

func (r *cliReach) observe(ev ccHookEvent) {
	cli := cliName(ev.CLI)
	if cli == "" || ev.Session == "" {
		return
	}
	if ev.TTY == "" && ev.PID > 0 && (ev.Event == "SessionStart" || ev.Event == "UserPromptSubmit") {
		// A plugin inside the CLI knows its process, not its terminal.
		ev.TTY = ttyOfPID(ev.PID)
	}
	if ev.TTY == "" && ev.Pane == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := cli + "\x00" + ev.Session
	if _, ok := r.at[key]; !ok && len(r.at) >= maxCLIReach {
		for k := range r.at {
			delete(r.at, k)
			break
		}
	}
	r.at[key] = cliReachSpot{TTY: ev.TTY, Pane: ev.Pane}
	if ev.TTY != "" {
		r.sightings = append(r.sightings, reach.Sighting{SessionID: key, TTY: ev.TTY})
		if len(r.sightings) > 4*maxCLIReach {
			r.sightings = r.sightings[len(r.sightings)-2*maxCLIReach:]
		}
	}
}

func (r *cliReach) deliver(ctx context.Context, cli, session, text string) (CCDelivery, error) {
	key := cli + "\x00" + session
	r.mu.Lock()
	spot, ok := r.at[key]
	sightings := append([]reach.Sighting(nil), r.sightings...)
	r.mu.Unlock()
	if !ok {
		return CCDelivery{}, fmt.Errorf("%s did not say which terminal that session is in, so there is no way to type into it", cli)
	}
	if spot.Pane != "" && r.paneAlive(spot.Pane) {
		if err := r.typeTmux(spot.Pane, text); err == nil {
			return CCDelivery{Via: "tmux", Immediate: true}, nil
		} else {
			log.Printf("session reply: typing into tmux pane %s: %v", spot.Pane, err)
		}
	}
	at := reach.For(sightings, r.liveTTYs(), key)
	if at.Gone {
		return CCDelivery{}, fmt.Errorf("that %s session is gone: %s", cli, at.Reason)
	}
	if at.TTY != "" {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := r.typeITerm(ctx, at.TTY, text); err == nil {
			return CCDelivery{Via: "terminal", Immediate: true}, nil
		} else {
			log.Printf("session reply: typing into %s: %v", at.TTY, err)
		}
	}
	return CCDelivery{}, errors.New("cannot type into that session's terminal (tmux and iTerm2 can be reached; this one is neither)")
}

// ttyOfPID is a process's controlling terminal, /dev/ttys003, or "".
func ttyOfPID(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return normalizeTTY(string(out))
}
