package app

// Claude Code's permission prompts in a terminal, answered from the phone.
//
// Claude Code runs the PermissionRequest hook the moment it is about to ask
// at the terminal. `superai hook PermissionRequest` hands the request to the
// SuperAI on this machine (/cc/permission on the hook socket), which sends it
// up the agent link to core. Core weighs the same presence gate the turn
// notices use — nobody at the machine, or the phone said away — and either
// says "pass" at once (the terminal's own dialog does its job) or raises the
// ordinary approval card, pushes it to the phones and says "asking". The
// answer comes back down the link (AnswerCCPermission) and out of the hook as
// Claude Code's decision JSON.
//
// It fails open to the terminal, never to "allow": a timeout, a core that
// never answered, a dropped link, a hook that was killed because the person
// answered at the keyboard — each leaves the hook silent, and the dialog in
// the terminal stands. A card whose hook stopped waiting is withdrawn.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agentexec/hooks"
	"github.com/liliang-cn/superai/internal/apns"
	"github.com/liliang-cn/superai/internal/backend"
)

// ccPermissionPath is the hook socket's route for a permission request.
const ccPermissionPath = "/cc/permission"

const (
	// ccPermDefaultWait is how long the hook waits for the phone unless told
	// otherwise (--wait, SUPERAI_HOOK_PERMISSION_WAIT). The hook entry's
	// timeout in settings.json must be longer.
	ccPermDefaultWait = 100 * time.Second
	ccPermMinWait     = 5 * time.Second
	ccPermMaxWait     = 10 * time.Minute
	// ccPermMargin is how much sooner than the hook the machine gives up, so
	// its "no answer" reaches the hook before the hook stops listening.
	ccPermMargin = 2 * time.Second
	// ccPermQuiet is how long after a card for a session its permission_prompt
	// notification stays quiet: the card already said so.
	ccPermQuiet = 2 * time.Minute
)

// ccPermAckWait is how long the machine waits for core to say whether it is
// asking. A core that says nothing (unreachable, or too old to know) must not
// hold a person at the desk for the whole wait. A variable for tests.
var ccPermAckWait = 5 * time.Second

// Notification categories the iOS app registers: a text field on a terminal
// turn, Allow and Deny on a terminal permission prompt.
const (
	ccReplyCategory    = "superai.cc.reply"
	ccApprovalCategory = "superai.cc.approval"
)

// Verdicts core sends down for a permission request.
const (
	ccPermPass   = "pass"   // nobody is being asked: the terminal's dialog stands
	ccPermAsking = "asking" // a card is up; wait for the answer
	ccPermAllow  = "allow"
	ccPermDeny   = "deny"
)

// ccPermAnswer is what the machine tells the hook: a behavior to print, or
// nothing.
type ccPermAnswer struct {
	Behavior string `json:"behavior,omitempty"`
	Message  string `json:"message,omitempty"`
}

// ---- the hook

// permissionWait is the hook's bound: `--wait 100` / `--wait=100` after the
// event name, else SUPERAI_HOOK_PERMISSION_WAIT, else the default; seconds,
// held between the minimum and the maximum.
func permissionWait(argv []string) time.Duration {
	val := strings.TrimSpace(os.Getenv("SUPERAI_HOOK_PERMISSION_WAIT"))
	for i := 1; i < len(argv); i++ {
		switch a := argv[i]; {
		case a == "--wait" && i+1 < len(argv):
			val = argv[i+1]
		case strings.HasPrefix(a, "--wait="):
			val = strings.TrimPrefix(a, "--wait=")
		}
	}
	d := ccPermDefaultWait
	if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
		d = time.Duration(n) * time.Second
	}
	return min(max(d, ccPermMinWait), ccPermMaxWait)
}

// ccAwayHintV is core's away switch as last pushed to this machine.
var ccAwayHintV atomic.Bool

// CCAwayHint is core telling this machine where its away switch stands.
func (a *App) CCAwayHint(away bool) map[string]any {
	ccAwayHintV.Store(away)
	return map[string]any{"ok": true}
}

// ccAway is the away switch as this process knows it: its own when it is the
// core, else the last hint from core.
func (a *App) ccAway() bool {
	if a.agentTap.Load() == nil {
		return a.Presence().Away
	}
	return ccAwayHintV.Load()
}

// permissionHook is `superai hook PermissionRequest`.
func permissionHook(argv []string, hev hooks.HookEvent) {
	ev, ok := ccEventFromHook("PermissionRequest", hev, nil)
	if !ok {
		return
	}
	wait := permissionWait(argv)
	ev.Wait = int(wait / time.Second)
	tctx, tcancel := context.WithTimeout(context.Background(), time.Second)
	ev.TTY = controllingTTY(tctx)
	tcancel()
	ev.Pane = os.Getenv("TMUX_PANE")
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if out := permissionOutput(askCCPermission(ctx, ccHookSockets(), ev)); out != nil {
		_, _ = os.Stdout.Write(out)
	}
}

// askCCPermission hands the request to the local SuperAI and waits for its
// answer. Nothing comes back from a SuperAI that is not running or cannot
// ask.
func askCCPermission(ctx context.Context, sockets []string, ev ccHookEvent) ccPermAnswer {
	var out ccPermAnswer
	if !postCCHook(ctx, sockets, ccPermissionPath, ev, &out) {
		return ccPermAnswer{}
	}
	return out
}

// permissionOutput is Claude Code's PermissionRequest decision, or nil for
// "no decision" — which leaves the terminal's own dialog.
//
//	{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}
//	{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"…"}}}
func permissionOutput(a ccPermAnswer) []byte {
	dec := map[string]any{}
	switch a.Behavior {
	case ccPermAllow:
		dec["behavior"] = ccPermAllow
	case ccPermDeny:
		dec["behavior"] = ccPermDeny
		msg := strings.TrimSpace(a.Message)
		if msg == "" {
			msg = ccDeniedMessage
		}
		dec["message"] = msg
	default:
		return nil
	}
	b, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PermissionRequest",
		"decision":      dec,
	}})
	if err != nil {
		return nil
	}
	return append(b, '\n')
}

// ccDeniedMessage is what Claude is told when the phone says no.
const ccDeniedMessage = "The user denied this from their phone (SuperAI). Do not retry the same call; ask what to do instead or try something else."

// ---- the machine

// awaitPerm opens the slot core's verdicts for request id arrive in.
func (l *ccLocal) awaitPerm(id string) chan ccPermVerdict {
	ch := make(chan ccPermVerdict, 4)
	l.mu.Lock()
	if l.perms == nil {
		l.perms = map[string]chan ccPermVerdict{}
	}
	l.perms[id] = ch
	l.mu.Unlock()
	return ch
}

func (l *ccLocal) dropPerm(id string) {
	l.mu.Lock()
	delete(l.perms, id)
	l.mu.Unlock()
}

// ccPermVerdict is one word from core about one request.
type ccPermVerdict struct {
	Verdict string
	Message string
}

// AnswerCCPermission takes core's word on a permission request this machine
// is holding: pass, asking, allow or deny. Core calls it over the agent link;
// it is not offered to anything else.
func (a *App) AnswerCCPermission(id, verdict, message string) error {
	switch verdict {
	case ccPermPass, ccPermAsking, ccPermAllow, ccPermDeny:
	default:
		return fmt.Errorf("unknown verdict %q", verdict)
	}
	l := ccLocalState()
	l.mu.Lock()
	ch := l.perms[id]
	l.mu.Unlock()
	if ch == nil {
		return errors.New("that permission request is no longer waiting")
	}
	select {
	case ch <- ccPermVerdict{Verdict: verdict, Message: message}:
	default:
	}
	return nil
}

// handleCCPermission holds one PermissionRequest hook until core answers it,
// says nobody is asked, or the wait is over. It answers 200 with a behavior
// or with none.
func (a *App) handleCCPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var ev ccHookEvent
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&ev); err != nil ||
		strings.TrimSpace(ev.Session) == "" || strings.TrimSpace(ev.Tool) == "" {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	ev.Event = "PermissionRequest"
	wait := min(max(time.Duration(ev.Wait)*time.Second, ccPermMinWait), ccPermMaxWait)
	if ev.Wait <= 0 {
		wait = ccPermDefaultWait
	}
	now := time.Now()
	l := ccLocalState()
	l.observe(&ev, now)
	// Somebody at this machine, and the phone has not said away: the
	// terminal's own dialog is the answer, at once — asking core first cost
	// every prompt a round trip.
	if !a.ccAway() {
		if idle := ccIdleSeconds(); idle >= 0 && time.Duration(idle)*time.Second < ccAwayAfter {
			writeJSON(w, ccPermAnswer{})
			return
		}
	}
	ev.Request = "ccp_" + randomSecret(8)
	deadline := now.Add(wait - ccPermMargin)
	ev.Expires = deadline.Format(time.RFC3339)
	ch := l.awaitPerm(ev.Request)
	defer l.dropPerm(ev.Request)

	a.forwardCCEvent(ev)
	ans, withdraw := waitCCPermission(r.Context(), ch, deadline)
	if withdraw {
		// Whatever core put on the phones is moot now.
		go a.forwardCCEvent(ccHookEvent{Event: "PermissionClosed", Session: ev.Session, Request: ev.Request})
	}
	if ans.Behavior != "" {
		l.setState(ev.Session, ccWorking)
		log.Printf("cc relay: %s in %s: %s from the phone", ev.Tool, ev.Session, ans.Behavior)
	}
	writeJSON(w, ans)
}

// waitCCPermission waits for core's answer. withdraw says the request ended
// without one, so a card core may have raised must go.
func waitCCPermission(ctx context.Context, ch <-chan ccPermVerdict, deadline time.Time) (ans ccPermAnswer, withdraw bool) {
	ack := time.NewTimer(ccPermAckWait)
	defer ack.Stop()
	end := time.NewTimer(time.Until(deadline))
	defer end.Stop()
	asking := false
	for {
		select {
		case v := <-ch:
			switch v.Verdict {
			case ccPermAsking:
				asking = true
			case ccPermAllow, ccPermDeny:
				return ccPermAnswer{Behavior: v.Verdict, Message: v.Message}, false
			default:
				return ccPermAnswer{}, false
			}
		case <-ack.C:
			if !asking {
				return ccPermAnswer{}, true
			}
		case <-end.C:
			return ccPermAnswer{}, true
		case <-ctx.Done():
			return ccPermAnswer{}, true // the hook is gone: answered at the terminal, or killed
		}
	}
}

// ---- core

// ccPerm is one permission request core is asking the phones about.
type ccPerm struct {
	session string
	cancel  context.CancelFunc
}

func (c *ccCore) addPerm(agent, request string, p *ccPerm) {
	c.mu.Lock()
	if c.perms == nil {
		c.perms = map[string]*ccPerm{}
	}
	c.perms[ccKey(agent, request)] = p
	if c.asked == nil {
		c.asked = map[string]time.Time{}
	}
	c.asked[ccKey(agent, p.session)] = time.Now()
	c.mu.Unlock()
}

// endPerm forgets a request, remembering when its session was last asked.
func (c *ccCore) endPerm(agent, request, session string, now time.Time) {
	c.mu.Lock()
	delete(c.perms, ccKey(agent, request))
	if c.asked == nil {
		c.asked = map[string]time.Time{}
	}
	c.asked[ccKey(agent, session)] = now
	c.mu.Unlock()
}

// withdrawPerm takes down the card for a request whose hook stopped waiting.
// Only the agent that raised a request can withdraw it.
func (c *ccCore) withdrawPerm(agent, request string) {
	c.mu.Lock()
	p := c.perms[ccKey(agent, request)]
	c.mu.Unlock()
	if p != nil {
		p.cancel()
	}
}

// permAsking says a card for this session is up, or was a moment ago: its
// permission_prompt notification would say the same thing twice.
func (c *ccCore) permAsking(agent, session string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, p := range c.perms {
		if p.session == session && strings.HasPrefix(k, agent+"\x00") {
			return true
		}
	}
	at, ok := c.asked[ccKey(agent, session)]
	return ok && now.Sub(at) < ccPermQuiet
}

// ccTitle is how a terminal session is named on the phone:
// "claude · mac · superai".
func ccTitle(machine, cwd string) string {
	parts := []string{"claude"}
	if machine != "" {
		parts = append(parts, machine)
	}
	if p := projectName(cwd); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, " · ")
}

// ccApprovalRequest is the approval card for a terminal permission prompt.
// It is always asked: neither YOLO nor a remembered rule answers a terminal
// on someone's behalf.
func ccApprovalRequest(agent string, r ccReport, now, expires time.Time) backend.ApprovalRequest {
	machine := agent
	if machine == "" {
		machine = r.Host
	}
	var input map[string]any
	if r.Input != "" {
		_ = json.Unmarshal([]byte(r.Input), &input)
	}
	cmd := ""
	if r.Tool == "Bash" {
		cmd, _ = input["command"].(string)
	}
	return backend.ApprovalRequest{
		ID:        uuid.NewString(),
		Tool:      r.Tool,
		Command:   cmd,
		Args:      input,
		SessionID: "cc:" + r.Session,
		AgentID:   agent,
		By:        ccTitle(machine, r.Cwd),
		Machine:   machine,
		Workdir:   r.Cwd,
		AskedAt:   now,
		ExpiresAt: expires,
		MustAsk:   true,
		Origin:    "terminal",
		Project:   projectName(r.Cwd),
		Summary:   r.Text,
		CCSession: r.Session,
	}
}

// ccApprovalAlert is the push for a terminal approval card: Allow and Deny
// on the lock screen (the app's superai.cc.approval category). It replaces
// the session's last notification there.
func ccApprovalAlert(req backend.ApprovalRequest) apns.Alert {
	body := "Allow " + req.Tool + "?"
	if s := plainText(req.Summary, alertBodyChars); s != "" && s != req.Tool {
		body += " " + s
	}
	return apns.Alert{
		Title:             req.By,
		Body:              body,
		Sound:             true,
		ThreadID:          "approval",
		InterruptionLevel: "time-sensitive",
		Priority:          10,
		CollapseID:        "cc:" + req.CCSession,
		Category:          ccApprovalCategory,
		Data: map[string]any{
			"kind":      "approval",
			"id":        req.ID,
			"agent":     req.AgentID,
			"session":   req.CCSession,
			"expiresAt": req.ExpiresAt.Format(time.RFC3339),
		},
	}
}

// ccPushApproval puts the card on the phones. A variable so a test can stand
// in for Apple.
var ccPushApproval = func(a *App, alert apns.Alert) {
	go a.phonePush().deliver(context.Background(), alert, false)
}

// answerCCPermission sends a verdict to the machine holding the request.
func (a *App) answerCCPermission(agent, request, verdict, message string) error {
	if agent == "" {
		return a.AnswerCCPermission(request, verdict, message)
	}
	c := a.agents().get(agent)
	if c == nil {
		return fmt.Errorf("%s is not connected", agent)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.call(ctx, "AnswerCCPermission", []any{request, verdict, message}, nil)
}

// askCCPermissionCore asks the phones about one terminal permission prompt,
// when nobody is at the machine, and tells the machine what they said.
func (a *App) askCCPermissionCore(agent string, r ccReport) {
	if r.Request == "" || r.Tool == "" {
		return
	}
	say := func(verdict, msg string) {
		if err := a.answerCCPermission(agent, r.Request, verdict, msg); err != nil {
			log.Printf("cc relay: answering %s's permission request: %v", orThis(agent), err)
		}
	}
	if !(r.Away || a.Presence().Away) {
		say(ccPermPass, "") // somebody is at the machine; the terminal asks them
		return
	}
	now := time.Now()
	expires := now.Add(ccPermDefaultWait - ccPermMargin)
	if t, err := time.Parse(time.RFC3339, r.Expires); err == nil && t.After(now) && t.Before(now.Add(ccPermMaxWait)) {
		expires = t
	}
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	defer cancel()
	req := ccApprovalRequest(agent, r, now, expires)
	cc := ccCoreState()
	cc.addPerm(agent, r.Request, &ccPerm{session: r.Session, cancel: cancel})
	defer func() { cc.endPerm(agent, r.Request, r.Session, time.Now()) }()

	say(ccPermAsking, "")
	ccPushApproval(a, ccApprovalAlert(req))
	dec, _ := a.askToolApproval(ctx, req)

	outcome := "denied"
	switch {
	case dec.By != backend.DecidedByUser:
		outcome = "unanswered" // timed out, or withdrawn: the terminal asks
	case dec.Allowed:
		outcome = "allowed"
	}
	ccPermAuditLine(ccPermAudit{At: time.Now(), Agent: agent, Session: r.Session, Tool: r.Tool,
		Project: projectName(r.Cwd), Outcome: outcome, WaitedMS: time.Since(now).Milliseconds()})
	if outcome == "unanswered" {
		return
	}
	cc.mu.Lock()
	if s := cc.sessions[ccKey(agent, r.Session)]; s != nil {
		s.State = ccWorking
	}
	cc.mu.Unlock()
	if dec.Allowed {
		say(ccPermAllow, "")
	} else {
		say(ccPermDeny, ccDeniedMessage)
	}
}

func orThis(agent string) string {
	if agent == "" {
		return "this machine"
	}
	return agent
}

// ccPermAudit is one terminal permission prompt put to the phones. The
// command is not kept here (the gate's own log has it, redacted); what was
// asked, of which session, and how it ended is.
type ccPermAudit struct {
	At       time.Time `json:"at"`
	Agent    string    `json:"agent,omitempty"`
	Session  string    `json:"session"`
	Tool     string    `json:"tool"`
	Project  string    `json:"project,omitempty"`
	Outcome  string    `json:"outcome"` // allowed | denied | unanswered
	WaitedMS int64     `json:"waited_ms"`
}

func ccPermAuditPath() string {
	return filepath.Join(backend.DataDir(), "audit", "cc-permissions.jsonl")
}

var ccPermAuditMu sync.Mutex

func ccPermAuditLine(e ccPermAudit) {
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	ccPermAuditMu.Lock()
	defer ccPermAuditMu.Unlock()
	p := ccPermAuditPath()
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
