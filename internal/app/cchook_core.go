package app

// Core's side of the Claude Code relay. A linked agent's terminal sessions
// report here (cc:session); core keeps the list, turns what is worth a
// person's attention into a notice addressed back to the session, and puts an
// answer typed on that notice through to the agent the session runs on.
//
// Quiet by default. A notice is raised only when the person is away — the
// machine the session runs on has had no input for ccAwayAfter, or the phone
// said "away" (SetAway) — and then it is pushed. Present, nothing is raised:
// they are looking at the terminal. One notice per session, replaced by the
// next (key "cc:<session>"), so a long day of turns is one line in the inbox,
// not a column.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liliang-cn/superai/internal/backend"
)

// CCSession is one Claude Code session a linked agent reported, as the
// screens list it.
type CCSession struct {
	Agent   string    `json:"agent"`
	Session string    `json:"session"`
	Host    string    `json:"host,omitempty"`
	Project string    `json:"project,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	State   string    `json:"state"`
	Text    string    `json:"text,omitempty"` // the last turn's text
	At      time.Time `json:"at"`
}

// ccCore is the sessions core has heard of and what it last told the phone
// about each.
type ccCore struct {
	mu       sync.Mutex
	sessions map[string]*CCSession // agent + "\x00" + session
	raised   map[string]string     // same key -> the event last raised as a notice
	perms    map[string]*ccPerm    // agent + "\x00" + request -> a card up for it
	asked    map[string]time.Time  // agent + "\x00" + session -> when a card was last up for it
}

var (
	ccCoreOnce sync.Once
	ccCoreV    *ccCore
)

func ccCoreState() *ccCore {
	ccCoreOnce.Do(func() {
		ccCoreV = &ccCore{sessions: map[string]*CCSession{}, raised: map[string]string{},
			perms: map[string]*ccPerm{}, asked: map[string]time.Time{}}
	})
	return ccCoreV
}

func ccKey(agent, session string) string { return agent + "\x00" + session }

// projectName is the last element of a working directory.
func projectName(cwd string) string {
	cwd = strings.TrimRight(strings.TrimSpace(cwd), "/")
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// note records a report and says what the session's previous turn text was
// and what was last raised for it.
func (c *ccCore) note(agent string, r ccReport, now time.Time) (prevText, lastRaised string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := ccKey(agent, r.Session)
	s := c.sessions[k]
	if s == nil {
		s = &CCSession{Agent: agent, Session: r.Session}
		c.sessions[k] = s
	}
	prevText, lastRaised = s.Text, c.raised[k]
	if r.Event == "SessionEnd" {
		delete(c.sessions, k)
		delete(c.raised, k)
		delete(c.asked, k)
		return prevText, lastRaised
	}
	s.At = now
	if r.Host != "" {
		s.Host = r.Host
	}
	if r.Cwd != "" {
		s.Cwd, s.Project = r.Cwd, projectName(r.Cwd)
	}
	switch r.Event {
	case "SessionStart":
		s.State = ccWorking
	case "Stop":
		s.State = ccFinished
		if r.Resumed {
			s.State = ccWorking
		}
		if r.Text != "" {
			s.Text = r.Text
		}
	case "StopFailure":
		s.State = ccFailed
	case "Notification":
		s.State = ccWaiting
		if r.Kind == "permission_prompt" {
			s.State = ccPermission
		}
	case "PermissionRequest":
		s.State = ccPermission
	}
	return prevText, lastRaised
}

func (c *ccCore) markRaised(agent, session, event string) {
	c.mu.Lock()
	c.raised[ccKey(agent, session)] = event
	c.mu.Unlock()
}

func (c *ccCore) list() []CCSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CCSession, 0, len(c.sessions))
	for _, s := range c.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// ccShouldNotify decides whether a report becomes a notice. lastRaised is
// the event last raised for the same session.
func ccShouldNotify(r ccReport, coreAway bool, lastRaised string) bool {
	if !(r.Away || coreAway) {
		return false // somebody is at the machine; the terminal says it
	}
	switch r.Event {
	case "Stop":
		return !r.Resumed // a parked answer was handed back: the turn goes on
	case "StopFailure":
		return true
	case "Notification":
		switch r.Kind {
		case "permission_prompt":
			return true
		case "idle_prompt":
			// Claude says it is waiting about a minute after a turn ends; the
			// turn's own notice already said so.
			return lastRaised != "Stop"
		}
	}
	return false
}

// ccNotice is how a report reads on the phone. prevText is the session's
// last turn, for a prompt that carries no text of its own.
func ccNotice(agent string, r ccReport, prevText string, now time.Time) backend.Notice {
	machine := agent
	if machine == "" {
		machine = r.Host
	}
	n := backend.Notice{
		Level:    backend.LevelInfo,
		Title:    ccTitle(machine, r.Cwd),
		Source:   "claude-code",
		Key:      "cc:" + r.Session,
		Kind:     backend.KindInfo, // not a brief: the home card for the day's brief is not where a terminal turn goes
		At:       now.Local().Format(time.RFC3339),
		Push:     true,
		External: true, // the phone, not the chat webhook: a turn is not a broadcast
		Reply:    &backend.NoticeReply{Agent: agent, Session: r.Session},
	}
	text := strings.TrimSpace(r.Text)
	switch {
	case r.Event == "StopFailure":
		n.Kind, n.Level = backend.KindAlert, backend.LevelWarn
		head := "The turn stopped on an API error"
		if r.Kind != "" {
			head += ": " + r.Kind
		}
		text = strings.TrimSpace(head + "\n\n" + text)
	case r.Event == "Notification" && r.Kind == "permission_prompt":
		n.Kind = backend.KindReminder
	case r.Event == "Notification" && r.Kind == "idle_prompt":
		if strings.TrimSpace(prevText) != "" {
			text = strings.TrimSpace(prevText)
		}
	}
	n.Message = clipFront(text, maxPostBody)
	if n.Message == "" {
		n.Message = "(no text)"
	}
	return n
}

// noteCCSession takes one report from a linked agent (agent "" is this
// machine) and raises a notice when it is worth one.
func (a *App) noteCCSession(agent, payloadJSON string) {
	var r ccReport
	if err := json.Unmarshal([]byte(payloadJSON), &r); err != nil || strings.TrimSpace(r.Session) == "" {
		return
	}
	now := time.Now()
	cc := ccCoreState()
	switch r.Event {
	case "PermissionClosed":
		cc.withdrawPerm(agent, r.Request)
		return
	case "PermissionRequest":
		cc.note(agent, r, now)
		go a.askCCPermissionCore(agent, r) // it waits for a person; the link must not
		return
	}
	prev, last := cc.note(agent, r, now)
	if r.Event == "Notification" && r.Kind == "permission_prompt" && cc.permAsking(agent, r.Session, now) {
		return // the approval card for this prompt is already on the phone
	}
	if !ccShouldNotify(r, a.Presence().Away, last) {
		return
	}
	cc.markRaised(agent, r.Session, r.Event)
	a.notices().Raise(context.Background(), ccNotice(agent, r, prev, now))
}

// CCSessions lists the Claude Code sessions the linked agents reported,
// newest first.
func (a *App) CCSessions() []CCSession { return ccCoreState().list() }

// CCPresence is whether the person said they are away from their machines.
type CCPresence struct {
	Away bool   `json:"away"`
	At   string `json:"at,omitempty"`
}

var presenceMu sync.Mutex

func presencePath() string { return filepath.Join(backend.DataDir(), "data", "presence.json") }

// Presence is the away switch: on, every session's notices push whether or
// not anyone is at its machine.
func (a *App) Presence() CCPresence {
	presenceMu.Lock()
	defer presenceMu.Unlock()
	var p CCPresence
	if b, err := os.ReadFile(presencePath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

// SetAway flips the away switch.
func (a *App) SetAway(away bool) (CCPresence, error) {
	presenceMu.Lock()
	defer presenceMu.Unlock()
	p := CCPresence{Away: away, At: time.Now().Format(time.RFC3339)}
	b, _ := json.Marshal(p)
	if err := os.MkdirAll(filepath.Dir(presencePath()), 0o700); err != nil {
		return CCPresence{}, err
	}
	tmp := presencePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return CCPresence{}, err
	}
	if err := os.Rename(tmp, presencePath()); err != nil {
		return CCPresence{}, err
	}
	go a.hintCCAway(away)
	return p, nil
}

// hintCCAway tells every linked machine where the away switch stands.
func (a *App) hintCCAway(away bool) {
	h := a.agents()
	h.mu.Lock()
	conns := make([]*agentConn, 0, len(h.agents))
	for _, c := range h.agents {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		go hintCCAwayTo(c, away)
	}
}

func hintCCAwayTo(c *agentConn, away bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.call(ctx, "CCAwayHint", []any{away}, nil); err != nil {
		log.Printf("cc relay: away hint to %s: %v", c.name(), err)
	}
}

// CCReplyRequest is an answer typed on a session's notice.
type CCReplyRequest struct {
	Agent   string `json:"agent"`
	Session string `json:"session"`
	Text    string `json:"text"`
}

// ccReplyTimeout bounds a reply's round trip to the agent.
const ccReplyTimeout = 30 * time.Second

// ReplyToCCSession sends an answer to the Claude Code session a notice came
// from. The master token and paired phones may call it; an integration token
// may not (requireAuth lets those reach PostNotification only), and core does
// not offer it to agents. Every attempt is written to the audit log.
func (a *App) ReplyToCCSession(req CCReplyRequest) (CCDelivery, error) {
	req.Agent, req.Session, req.Text = strings.TrimSpace(req.Agent), strings.TrimSpace(req.Session), strings.TrimSpace(req.Text)
	var (
		out CCDelivery
		err error
	)
	switch {
	case req.Session == "":
		err = errors.New("which session?")
	case req.Text == "":
		err = errors.New("the answer is empty")
	case utf8.RuneCountInString(req.Text) > maxCCReply:
		err = fmt.Errorf("the answer is longer than %d characters", maxCCReply)
	case req.Agent == "":
		out, err = a.ReplyToSession(req.Session, req.Text)
	default:
		c := a.agents().get(req.Agent)
		if c == nil {
			err = fmt.Errorf("%s is not connected", req.Agent)
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), ccReplyTimeout)
		err = c.call(ctx, "ReplyToSession", []any{req.Session, req.Text}, &out)
		cancel()
	}
	ccAudit(ccAuditEntry{At: time.Now(), Agent: req.Agent, Session: req.Session,
		Chars: utf8.RuneCountInString(req.Text), Delivered: err == nil, Via: out.Via, Error: errString(err)})
	if err == nil {
		cc := ccCoreState()
		cc.mu.Lock()
		if s := cc.sessions[ccKey(req.Agent, req.Session)]; s != nil && out.Immediate {
			s.State = ccWorking
		}
		cc.mu.Unlock()
	}
	return out, err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ccAuditEntry is one answer sent to a session. The text itself is not kept:
// the log outlives the conversation, and what was typed is the session's own
// business; how long it was and where it went is what an audit needs.
type ccAuditEntry struct {
	At        time.Time `json:"at"`
	Agent     string    `json:"agent,omitempty"`
	Session   string    `json:"session"`
	Chars     int       `json:"chars"`
	Delivered bool      `json:"delivered"`
	Via       string    `json:"via,omitempty"`
	Error     string    `json:"error,omitempty"`
}

var ccAuditMu sync.Mutex

func ccAuditPath() string { return filepath.Join(backend.DataDir(), "audit", "cc-replies.jsonl") }

// ccAudit appends one line. A log that cannot be written does not stop the
// delivery it describes; it says so in the process log.
func ccAudit(e ccAuditEntry) {
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	ccAuditMu.Lock()
	defer ccAuditMu.Unlock()
	p := ccAuditPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		log.Printf("cc relay: audit: %v", err)
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("cc relay: audit: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
