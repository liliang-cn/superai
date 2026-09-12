package backend

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// emitted records what the frontend would have been sent.
type emitted struct {
	mu     sync.Mutex
	events []map[string]any
	names  []string
}

func (e *emitted) sink() func(string, map[string]any) {
	return func(name string, payload map[string]any) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.names = append(e.names, name)
		e.events = append(e.events, payload)
	}
}

func (e *emitted) list() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any(nil), e.events...)
}

// The whole point of raising a notice once: the person watching the page and
// the person who walked away hear the same thing. They used to be two code
// paths and drifted — one learned to strip the persona's emotion tag and the
// other did not.
func TestRaiseReachesBothSurfaces(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	ui := &emitted{}
	n := NewNotices(NewNotifier(&Settings{WebhookURL: srv.URL}))
	n.SetEmitter(ui.sink())

	n.Raise(context.Background(), Notice{
		Level: LevelSuccess, Message: "all green", Source: "deploy check", Push: true,
	})

	events := ui.list()
	if len(events) != 1 {
		t.Fatalf("got %d UI events, want 1", len(events))
	}
	if ui.names[0] != NoticeEvent {
		t.Errorf("event name = %q, want %q", ui.names[0], NoticeEvent)
	}
	if events[0]["message"] != "all green" || events[0]["level"] != "success" {
		t.Errorf("UI payload = %#v", events[0])
	}

	calls := rec.waitForCalls(t, 1)
	if calls[0].Message != "all green" || calls[0].Source != "deploy check" {
		t.Errorf("webhook payload = %#v", calls[0])
	}
}

// A toast is not a reason to make someone's phone buzz. "Settings saved" is
// worth showing and is not worth interrupting anyone for, and no threshold on
// severity expresses that — only the caller knows.
func TestPushIsPerNoticeNotPerLevel(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	ui := &emitted{}
	n := NewNotices(NewNotifier(&Settings{WebhookURL: srv.URL}))
	n.SetEmitter(ui.sink())

	n.Raise(context.Background(), Notice{Level: LevelError, Message: "local only"})
	n.Raise(context.Background(), Notice{Level: LevelInfo, Message: "worth sending", Push: true})

	if got := len(ui.list()); got != 2 {
		t.Errorf("UI saw %d, want both", got)
	}
	calls := rec.waitForCalls(t, 1)
	// An error that was not marked Push stays local; an info that was, goes.
	if len(calls) != 1 || calls[0].Message != "worth sending" {
		t.Errorf("webhook got %#v, want only the pushed one", calls)
	}
}

// A receiver keys off the Error field to decide whether to raise the priority,
// while Message still carries the text so one that prints a single field says
// something useful.
func TestErrorLevelFillsTheWebhookErrorField(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotices(NewNotifier(&Settings{WebhookURL: srv.URL}))
	n.Raise(context.Background(), Notice{Level: LevelError, Message: "disk full", Push: true})

	call := rec.waitForCalls(t, 1)[0]
	if call.Error != "disk full" || call.Message != "disk full" {
		t.Errorf("payload = %#v, want the reason in both fields", call)
	}
}

// The scheduler cancels a run's context the instant the run ends, which is
// exactly when its notice is raised.
func TestRaiseSurvivesACancelledContext(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	NewNotices(NewNotifier(&Settings{WebhookURL: srv.URL})).
		Raise(ctx, Notice{Message: "after the run", Push: true})
	rec.waitForCalls(t, 1)
}

// The one-shot CLI has no UI at all, and a Service is built before whoever owns
// a window exists.
func TestRaiseWithoutAnEmitter(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotices(NewNotifier(&Settings{WebhookURL: srv.URL}))
	n.Raise(context.Background(), Notice{Message: "no window here", Push: true})
	rec.waitForCalls(t, 1)

	var nilNotices *Notices
	nilNotices.SetEmitter(func(string, map[string]any) {})
	nilNotices.Raise(context.Background(), Notice{Message: "x"}) // must not panic
}

func TestRaiseFillsDefaults(t *testing.T) {
	ui := &emitted{}
	n := NewNotices(nil)
	n.SetEmitter(ui.sink())
	n.Raise(context.Background(), Notice{Message: "  spaced  "})

	got := ui.list()[0]
	if got["level"] != string(LevelInfo) {
		t.Errorf("level = %v, want the info default", got["level"])
	}
	if got["message"] != "spaced" {
		t.Errorf("message = %q, want it trimmed", got["message"])
	}
	// Every notice carries a time, so a UI can order them without inventing one.
	if _, err := time.Parse(time.RFC3339, got["at"].(string)); err != nil {
		t.Errorf("at = %v: %v", got["at"], err)
	}
}

// A publisher, not a pair of hardcoded calls: a surface nobody anticipated can
// listen without the senders being found and changed again.
func TestSubscribersAreOpenEnded(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	record := func(name string) Subscriber {
		return func(_ context.Context, n Notice) {
			mu.Lock()
			defer mu.Unlock()
			seen[name] = n.Message
		}
	}

	p := NewNotices(nil)
	stopBanner := p.Subscribe("banner", record("banner"))
	p.Subscribe("audit", record("audit"))

	p.Raise(context.Background(), Notice{Message: "heard by both"})
	mu.Lock()
	if seen["banner"] != "heard by both" || seen["audit"] != "heard by both" {
		t.Errorf("subscribers saw %#v", seen)
	}
	mu.Unlock()

	stopBanner()
	p.Raise(context.Background(), Notice{Message: "audit only"})
	mu.Lock()
	if seen["banner"] != "heard by both" {
		t.Error("an unsubscribed surface kept receiving")
	}
	if seen["audit"] != "audit only" {
		t.Errorf("audit = %q", seen["audit"])
	}
	mu.Unlock()
}

// A settings save constructs a whole new Service and re-attaches the window. A
// second registration under the same name must displace the first, or every
// toast is drawn twice.
func TestSubscribingTwiceUnderOneNameReplaces(t *testing.T) {
	var mu sync.Mutex
	var calls int
	p := NewNotices(nil)
	for range 3 {
		p.Subscribe("ui", func(context.Context, Notice) {
			mu.Lock()
			calls++
			mu.Unlock()
		})
	}
	p.Raise(context.Background(), Notice{Message: "once"})
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("drawn %d times, want 1", calls)
	}
}

// A notice is the thing that reports trouble, so it is the worst place for a
// panic to escape: the failure being described would be replaced by its own.
func TestOneBrokenSubscriberDoesNotStopTheOthers(t *testing.T) {
	var mu sync.Mutex
	var got string
	p := NewNotices(nil)
	p.Subscribe("broken", func(context.Context, Notice) { panic("surface is on fire") })
	p.Subscribe("working", func(_ context.Context, n Notice) {
		mu.Lock()
		got = n.Message
		mu.Unlock()
	})

	p.Raise(context.Background(), Notice{Message: "still delivered"})
	mu.Lock()
	defer mu.Unlock()
	if got != "still delivered" {
		t.Errorf("working subscriber saw %q", got)
	}
}

// The webhook is subscribed by construction, since every build has one and it
// is configured rather than attached.
func TestWebhookIsSubscribedByDefault(t *testing.T) {
	p := NewNotices(NewNotifier(&Settings{}))
	if len(p.Subscribers()) != 1 || p.Subscribers()[0] != "webhook" {
		t.Errorf("subscribers = %v, want just the webhook", p.Subscribers())
	}
	// And a publisher with no notifier at all has none, rather than a sink that
	// silently swallows everything.
	if got := NewNotices(nil).Subscribers(); len(got) != 0 {
		t.Errorf("subscribers = %v, want none", got)
	}
}
