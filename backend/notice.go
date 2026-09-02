package backend

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// Notices: one thing worth saying, published once, drawn by whoever is
// listening.
//
// These used to be two unrelated code paths. A message for the person at the
// screen was an event the frontend happened to render; a message for the person
// who is away was a webhook posted from somewhere else entirely. So they drifted
// — the webhook learned to strip the persona's emotion tag and the in-page path
// did not, a scheduled run said one thing in the transcript and another on a
// phone, and adding a third surface meant finding every caller again.
//
// So it is a publisher rather than a pair of calls. The thing that has something
// to say raises a Notice and is finished; the surfaces subscribe. Which of them
// exists is not the publisher's business — the one-shot CLI has no window, the
// serve build has no desktop banner, and a webhook is configured or it is not.
//
// Policy lives with the subscriber, not with the publisher. Whether a notice is
// worth making a phone buzz is a question about phones, so the webhook
// subscriber is the one that reads Notice.Push; a future macOS banner would
// answer it differently and neither has to be taught about the other.

// Level is how loudly a notice is drawn. The names match what the frontend
// renders, so adding one here means adding a style there.
type Level string

const (
	LevelInfo    Level = "info"
	LevelSuccess Level = "success"
	LevelWarn    Level = "warn"
	LevelError   Level = "error"
)

// Notice is one message to the user.
type Notice struct {
	Level Level `json:"level"`
	// Title is optional; the frontend falls back to a label for the level.
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
	// Source is what produced this — a schedule's prompt, a tool's name. Shown
	// as a second line, and sent on as the webhook's `source`.
	Source string `json:"source,omitempty"`
	// Session is the conversation to open when a toast is clicked, if any.
	Session string `json:"session,omitempty"`
	// Key deduplicates: two notices with the same key replace rather than
	// stack. A run that reports twice should not leave two toasts behind.
	Key string `json:"key,omitempty"`
	// Push marks a notice as worth reaching someone who is not at the screen.
	//
	// Written by the author of the notice and read by the subscribers that
	// leave the machine. Deliberately not derived from the level: "settings
	// saved" is worth a toast and is not worth a phone buzzing in a pocket, and
	// no threshold on severity says that — the question is whether the message
	// is worth interrupting a person who is somewhere else, and only the caller
	// knows.
	Push bool `json:"-"`
	// At is stamped at publish time.
	At string `json:"at,omitempty"`
}

// NoticeEvent is the frontend event name. One name for every notice, because a
// UI that has to know which of six event names carries a message is a UI that
// will miss one.
const NoticeEvent = "notice"

// A Subscriber draws one notice on one surface.
//
// It is called on the publisher's goroutine and must not block: the callers are
// an agent finishing a turn and a scheduler on its own goroutine. A subscriber
// with network to do hands off to a goroutine of its own — see webhookSink.
type Subscriber func(ctx context.Context, n Notice)

// Notices is the publisher.
type Notices struct {
	mu   sync.RWMutex
	subs map[string]Subscriber
}

// NewNotices builds a publisher with the webhook already subscribed, since
// every build has one and it is configured rather than attached.
func NewNotices(n *Notifier) *Notices {
	p := &Notices{subs: map[string]Subscriber{}}
	if n != nil {
		p.Subscribe("webhook", webhookSink(n))
	}
	return p
}

// Subscribe registers a surface under a name, replacing any subscriber already
// under it, and returns a function that removes it.
//
// Named rather than anonymous because the interesting case is replacement: a
// settings save constructs a whole new Service, and a window that re-attached
// without displacing its previous self would draw every toast twice.
func (p *Notices) Subscribe(name string, sub Subscriber) func() {
	if p == nil || sub == nil {
		return func() {}
	}
	p.mu.Lock()
	p.subs[name] = sub
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.subs, name)
		p.mu.Unlock()
	}
}

// SetEmitter subscribes the frontend under a fixed name.
//
// A thin wrapper over Subscribe because this is the one surface the app
// re-attaches on every rebuild, and the name has to match each time.
func (p *Notices) SetEmitter(emit func(name string, payload map[string]any)) {
	if p == nil || emit == nil {
		return
	}
	p.Subscribe("ui", uiSink(emit))
}

// Raise publishes one notice.
func (p *Notices) Raise(ctx context.Context, n Notice) {
	if p == nil {
		return
	}
	if n.Level == "" {
		n.Level = LevelInfo
	}
	n.Message = strings.TrimSpace(n.Message)
	n.Source = strings.TrimSpace(n.Source)
	if n.At == "" {
		n.At = time.Now().Format(time.RFC3339)
	}

	p.mu.RLock()
	subs := make([]Subscriber, 0, len(p.subs))
	names := make([]string, 0, len(p.subs))
	for name, sub := range p.subs {
		subs = append(subs, sub)
		names = append(names, name)
	}
	p.mu.RUnlock()

	for i, sub := range subs {
		// One surface must not take the others down with it. A notice is the
		// thing that reports trouble, so it is the worst possible place for a
		// panic to escape — the failure it was describing would be replaced by
		// its own.
		func(name string, sub Subscriber) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("superai: notice subscriber %q panicked: %v", name, r)
				}
			}()
			sub(ctx, n)
		}(names[i], sub)
	}
}

// Subscribers names who is currently listening, for the dashboard and for
// tests that would otherwise assert on a silent no-op.
func (p *Notices) Subscribers() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.subs))
	for name := range p.subs {
		out = append(out, name)
	}
	return out
}

// uiSink draws a notice in whatever window is attached.
//
// Every notice, regardless of Push: a page that is open should never be the
// last to know, and the reader is already looking at it.
func uiSink(emit func(name string, payload map[string]any)) Subscriber {
	return func(_ context.Context, n Notice) {
		emit(NoticeEvent, map[string]any{
			"level":   string(n.Level),
			"title":   n.Title,
			"message": n.Message,
			"source":  n.Source,
			"session": n.Session,
			"key":     n.Key,
			"at":      n.At,
		})
	}
}

// webhookSink sends a notice onward to whoever is not at the screen.
func webhookSink(notifier *Notifier) Subscriber {
	return func(ctx context.Context, n Notice) {
		if !n.Push || !notifier.Enabled() {
			return
		}
		payload := WebhookPayload{
			Event:   webhookEventFor(n),
			Title:   n.Title,
			Message: n.Message,
			Source:  n.Source,
			SentAt:  n.At,
		}
		if n.Level == LevelError {
			// A receiver keys off this field to decide whether to raise the
			// priority; Message still carries the text, so one that prints a
			// single field still says something useful.
			payload.Error = n.Message
		}
		// Detached: the publisher's caller is a turn or a scheduler tick, and
		// the run's context is cancelled the instant it ends — which is exactly
		// when its notice goes out.
		go notifier.Send(context.WithoutCancel(ctx), payload)
	}
}

// webhookEventFor keeps the wire's `event` values stable. They were part of the
// contract before notices existed, and a receiver routing on them should not
// have to change because the sending side was refactored.
func webhookEventFor(n Notice) string {
	if n.Source != "" {
		return WebhookEventSchedule
	}
	return WebhookEventNotify
}
