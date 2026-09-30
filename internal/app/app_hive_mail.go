package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/superai/internal/backend"
)

// Messages between members, from this instance's side. The wire format and
// why messages exist next to orders are in backend/hive_mail.go.

// mailbox is this member's inbox, built on first use.
func (a *App) mailbox() *backend.Mailbox {
	a.hiveMailOnce.Do(func() { a.hiveMail = backend.NewMailbox(200) })
	return a.hiveMail
}

// hiveContacts is everyone this member can write to, and its own name. A
// queen writes to her workers; a worker to the queen and to its peers. ok is
// false on an instance that is in no hive.
func (a *App) hiveContacts(ctx context.Context) (me string, contacts map[string]backend.HiveContact, ok bool) {
	a.mu.Lock()
	h, ann, s := a.hive, a.hiveAnn, a.settings
	a.mu.Unlock()
	switch {
	case h != nil:
		contacts = map[string]backend.HiveContact{}
		for n, ag := range h.Agents() {
			contacts[n] = backend.HiveContact{Name: n, URL: ag.URL, Token: ag.Token}
		}
		return a.hiveNameOf(s), contacts, true
	case ann != nil:
		return ann.Name(), ann.Contacts(ctx), true
	}
	return "", nil, false
}

// hiveSend delivers text to one member, or to every other one with "*", and
// says what became of each copy.
func (a *App) hiveSend(ctx context.Context, to, text, replyTo string) (string, error) {
	me, contacts, ok := a.hiveContacts(ctx)
	if !ok {
		return "", fmt.Errorf("this instance is not in a hive")
	}
	a.mu.Lock()
	ann := a.hiveAnn
	a.mu.Unlock()
	var toQueen func(context.Context, backend.HiveMessage) error
	if ann != nil {
		toQueen = ann.SendToQueen
	}
	return backend.SendHive(ctx, me, contacts, to, text, replyTo, toQueen, func(c backend.HiveContact, m backend.HiveMessage) {
		a.emitMessage(m, "out")
		// Between two workers the queen was not part of it; she gets a copy
		// to see, the same way she sees their orders — unless she carried it.
		if ann != nil && !c.Queen && m.Via == "" {
			m.Observed = true
			a.reportToQueen(m)
		}
	})
}

// emitMessage puts a message on this instance's event stream for the panel.
func (a *App) emitMessage(m backend.HiveMessage, dir string) {
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	var p map[string]any
	if json.Unmarshal(raw, &p) == nil {
		p["dir"] = dir
		a.emit("hive:message", p)
	}
}

// handleHiveMessage is POST /api/hive/message: a note for this member, or on
// a queen the copy of one between two workers. Behind the credential gate like
// the rest; a queen also takes mail only from those on her roster.
func (a *App) handleHiveMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	a.mu.Lock()
	h, ann, s := a.hive, a.hiveAnn, a.settings
	a.mu.Unlock()
	if h == nil && ann == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not in a hive"})
		return
	}
	var m backend.HiveMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&m); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "unreadable message: " + err.Error()})
		return
	}
	if err := m.Valid(); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if h != nil && !onRoster(h, m.From) {
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "not on the roster: " + m.From})
		return
	}
	if m.Observed {
		if h == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "only a queen keeps watch"})
			return
		}
		a.emitMessage(m, "peer")
		writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	me := a.hiveNameOf(s)
	if h != nil && m.To != me && m.To != backend.HiveQueenAlias {
		a.relayMessage(w, r, h, m)
		return
	}
	if m.To != me && !(h != nil && m.To == backend.HiveQueenAlias) {
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("this is %s, not %s", me, m.To)})
		return
	}
	if a.mailbox().Put(m) {
		a.emitMessage(m, "in")
		a.deliverToLoop(m)
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true})
}

// HiveMailSessionPrefix names the conversation a member keeps with one other
// member: every message from w1 is taken up in "hive-mail:w1", so the second
// message from someone is read with the first one in view.
const HiveMailSessionPrefix = "hive-mail:"

// wakeTimeout bounds how long a message waits for the turn ahead of it.
const wakeTimeout = 30 * time.Minute

// deliverToLoop puts a message in front of the agent instead of leaving it for
// the agent to go looking.
//
// A turn that is already running takes it in at its next step: the one in the
// conversation with the sender if there is one, otherwise every turn running
// here — the order a worker is in the middle of, the turn the queen is giving
// orders from. That is the case the message is usually about.
//
// With nothing running, a message starts a turn in the conversation with its
// sender. A reply does not: it is the answer to something this member asked,
// and it waits in the inbox — otherwise two members that answer each other
// would wake each other forever.
func (a *App) deliverToLoop(m backend.HiveMessage) {
	if a.steerMail(m) {
		return
	}
	if m.ReplyTo != "" {
		return
	}
	a.wakeFor(m)
}

// steerMail puts a message into the turns running here, and says whether any
// took it.
func (a *App) steerMail(m backend.HiveMessage) bool {
	steer := a.steerFn
	if steer == nil {
		a.mu.Lock()
		svc := a.svc
		a.mu.Unlock()
		if svc == nil || svc.Agent() == nil {
			return false
		}
		inner := svc.Agent()
		steer = func(session, content string) bool {
			return inner.Steer(session, domain.Message{Role: "user", Content: content})
		}
	}
	targets := a.runningSessions()
	own := HiveMailSessionPrefix + m.From
	for _, s := range targets {
		if s == own {
			targets = []string{own}
			break
		}
	}
	content := backend.MailPrompt(m.From, []backend.HiveMessage{m}, true)
	took := false
	for _, s := range targets {
		if steer(s, content) {
			took = true
		}
	}
	if took {
		a.mailbox().Mark(m.ID, true)
	}
	return took
}

// steerDropped is a run ending without taking in the messages put into it.
// They go back to unread, and each that is not a reply starts a turn of its
// own, the way it would have had nothing been running.
func (a *App) steerDropped(content string) {
	for _, id := range mailIDs.FindAllStringSubmatch(content, -1) {
		m, ok := a.mailbox().Get(id[1])
		if !ok {
			continue
		}
		a.mailbox().Mark(m.ID, false)
		if m.ReplyTo == "" {
			a.wakeFor(m)
		}
	}
}

// mailIDs finds the message ids backend.MailPrompt writes.
var mailIDs = regexp.MustCompile(`— id ([0-9a-f-]{36}),`)

// wakeFor starts a turn in the conversation with the sender, once any turn
// already there has ended, carrying everything that sender has sent unread.
func (a *App) wakeFor(m backend.HiveMessage) {
	wake := a.wakeFn
	if wake == nil {
		a.mu.Lock()
		ready := a.svc != nil
		a.mu.Unlock()
		if !ready {
			return // nothing here can run a turn; the inbox keeps it
		}
		wake = func(session, prompt string) { a.SendChat(session, prompt, nil) }
	}
	session := HiveMailSessionPrefix + m.From
	a.mailWakeMu.Lock()
	if a.mailWaking == nil {
		a.mailWaking = map[string]bool{}
	}
	if a.mailWaking[m.From] {
		// Someone is already waiting to take up this sender's mail, and will
		// take this one with the rest.
		a.mailWakeMu.Unlock()
		return
	}
	a.mailWaking[m.From] = true
	a.mailWakeMu.Unlock()

	go func() {
		defer func() {
			a.mailWakeMu.Lock()
			delete(a.mailWaking, m.From)
			a.mailWakeMu.Unlock()
		}()
		deadline := time.Now().Add(wakeTimeout)
		for a.sessionBusy(session) {
			if time.Now().After(deadline) {
				return // still in the inbox; hive_inbox finds it
			}
			time.Sleep(500 * time.Millisecond)
		}
		msgs := a.mailbox().TakeFrom(m.From)
		if len(msgs) == 0 {
			return // read in the meantime
		}
		wake(session, backend.MailPrompt(m.From, msgs, false)+a.lastOrderNote())
	}()
}

// orderContextWindow is how recent an order has to be for a woken turn to be
// shown it.
const orderContextWindow = 2 * time.Hour

// lastOrderNote is the order this member was last given, for a turn a message
// has started. That turn is a new conversation with the sender, and a message
// is often a step in an order given elsewhere — "the previous leg's value is
// 18e99f0d6bff" means nothing without the relay's rules. With the order in
// view, a worker can end its turn while it waits instead of looping on its
// inbox to keep the order in its context.
func (a *App) lastOrderNote() string {
	var last *backend.HiveTask
	tasks := a.tasks().Recent()
	for i := len(tasks) - 1; i >= 0; i-- {
		if tasks[i].Dir == backend.TaskIn && time.Since(tasks[i].StartedAt) < orderContextWindow {
			last = &tasks[i]
			break
		}
	}
	if last == nil {
		return ""
	}
	return fmt.Sprintf("\n\nFor context, the last order you were given (task %s, %s, %s ago), which this may be part of:\n%s",
		last.ID, last.State, time.Since(last.StartedAt).Round(time.Second), last.Prompt)
}

// relayMessage passes a message on for a member that could not reach the
// recipient itself. Only between two on the roster; what the recipient says
// about it is what the sender is told.
func (a *App) relayMessage(w http.ResponseWriter, r *http.Request, h *backend.Hive, m backend.HiveMessage) {
	to, ok := h.Agents()[m.To]
	if !ok {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "nobody live called " + m.To})
		return
	}
	m.Via = "queen"
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := backend.PostMessage(ctx, to.URL, to.Token, m); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": "could not pass it on: " + err.Error()})
		return
	}
	a.emitMessage(m, "peer")
	writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true, "via": "queen"})
}

func onRoster(h *backend.Hive, name string) bool {
	for _, m := range h.Members() {
		if m.Name == name {
			return true
		}
	}
	return false
}

// registerMailTools gives a member of a hive hive_send and hive_inbox. The
// queen and the workers get the same two: a message goes either way.
func (a *App) registerMailTools(inner interface {
	AddToolWithMetadata(name, description string, params map[string]any, fn func(context.Context, map[string]any) (any, error), meta agent.ToolMetadata)
}) {
	inner.AddToolWithMetadata("hive_send",
		"Send a short message to another member of the hive — the queen, a worker, or everyone — and carry on."+
			"\n\nA message does not start any work on the other side and nothing waits for an answer: it lands in the"+
			" recipient's inbox, to be read with hive_inbox. Use it to report progress or a finding mid-task, to warn"+
			" others about something, or to answer a message (set reply_to). To have someone do something and wait for the"+
			" result, give an order instead (the queen) or ask (hive_ask, workers). Anything long belongs in the shared"+
			" memory; send a pointer to it.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":       map[string]any{"type": "string", "description": `A member's name, "queen" for the queen, or "*" for every other member.`},
				"text":     map[string]any{"type": "string", "description": "The message, standing on its own."},
				"reply_to": map[string]any{"type": "string", "description": "The id of the message this answers, if any."},
			},
			"required": []string{"to", "text"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			return a.hiveSend(ctx, str(args["to"]), str(args["text"]), str(args["reply_to"]))
		},
		agent.ToolMetadata{ConcurrencySafe: true})

	inner.AddToolWithMetadata("hive_inbox",
		"Read the messages other members of the hive sent you. Returns the unread ones and marks them read;"+
			" with all, the last few whether read or not. Check it at the start of an order and when you are told mail is waiting.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"all": map[string]any{"type": "boolean", "description": "The last 20 messages, read or not."},
			},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			all, _ := args["all"].(bool)
			limit := 50
			if all {
				limit = 20
			}
			msgs := a.mailbox().Take(all, limit)
			if len(msgs) == 0 {
				if all {
					return "No messages.", nil
				}
				return backend.InboxEmpty, nil
			}
			b, err := json.Marshal(msgs)
			return string(b), err
		},
		agent.ToolMetadata{ConcurrencySafe: true, OutputLimit: -1})
}
