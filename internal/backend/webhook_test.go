package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// recorder is a webhook receiver that keeps what it was sent.
type recorder struct {
	mu      sync.Mutex
	bodies  []WebhookPayload
	headers []http.Header
	status  int
}

func (r *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var p WebhookPayload
		_ = json.Unmarshal(raw, &p)
		r.mu.Lock()
		r.bodies = append(r.bodies, p)
		r.headers = append(r.headers, req.Header.Clone())
		status := r.status
		r.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}
}

// waitForCalls polls until n payloads have arrived. The fan-out posts the
// webhook on its own goroutine so a chat turn never waits on a chat API, which
// means every assertion about it has to wait rather than read immediately.
func (r *recorder) waitForCalls(t *testing.T, n int) []WebhookPayload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.calls(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d webhook call(s), got %d", n, len(r.calls()))
	return nil
}

func (r *recorder) calls() []WebhookPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]WebhookPayload(nil), r.bodies...)
}

func TestNotifierPostsPayload(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier(&Settings{WebhookURL: srv.URL, WebhookSecret: "s3cret"})
	n.Send(context.Background(), WebhookPayload{Event: WebhookEventNotify, Message: "halfway there"})

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].Message != "halfway there" || calls[0].Event != WebhookEventNotify {
		t.Errorf("payload = %#v", calls[0])
	}
	// A title and a timestamp are always present, so a receiver can render
	// something without checking for absent fields.
	if calls[0].Title != "SuperAI" {
		t.Errorf("title = %q, want a default", calls[0].Title)
	}
	if _, err := time.Parse(time.RFC3339, calls[0].SentAt); err != nil {
		t.Errorf("sent_at = %q, not RFC3339: %v", calls[0].SentAt, err)
	}
	if got := rec.headers[0].Get("Authorization"); got != "Bearer s3cret" {
		t.Errorf("Authorization = %q", got)
	}
	if got := rec.headers[0].Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
}

// Not configuring a webhook is the default, so every caller must be able to
// send unconditionally.
func TestNotifierIsANoOpWhenUnconfigured(t *testing.T) {
	for name, n := range map[string]*Notifier{
		"nil":          nil,
		"no url":       NewNotifier(&Settings{}),
		"nil settings": NewNotifier(nil),
		"blank url":    NewNotifier(&Settings{WebhookURL: "   "}),
	} {
		t.Run(name, func(t *testing.T) {
			if n.Enabled() {
				t.Error("Enabled() = true")
			}
			n.Send(context.Background(), WebhookPayload{Message: "x"}) // must not panic
		})
	}
}

// A notification is a side channel: a receiver that is down, slow or angry must
// never take down the turn that produced the message.
func TestNotifierSwallowsReceiverFailures(t *testing.T) {
	rec := &recorder{status: http.StatusInternalServerError}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	n := NewNotifier(&Settings{WebhookURL: srv.URL})
	n.Send(context.Background(), WebhookPayload{Message: "still fine"})
	if len(rec.calls()) != 1 {
		t.Fatal("the call was not made")
	}

	// An unreachable host is the same story.
	dead := NewNotifier(&Settings{WebhookURL: "http://127.0.0.1:1/nowhere"})
	dead.Send(context.Background(), WebhookPayload{Message: "still fine"})
}

// The scheduler cancels a run's context the moment the run ends, which is
// exactly when the notification is sent. Inheriting it would cancel every
// delivery — the bug this test exists to catch.
func TestNotifierSendsOnAnAlreadyCancelledContext(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	NewNotifier(&Settings{WebhookURL: srv.URL}).Send(ctx, WebhookPayload{Message: "after the run"})
	if got := len(rec.calls()); got != 1 {
		t.Fatalf("got %d calls, want 1 — the run's cancellation killed the delivery", got)
	}
}

func TestNotifierUpdateChangesTarget(t *testing.T) {
	first, second := &recorder{}, &recorder{}
	s1 := httptest.NewServer(first.handler())
	defer s1.Close()
	s2 := httptest.NewServer(second.handler())
	defer s2.Close()

	n := NewNotifier(&Settings{WebhookURL: s1.URL})
	n.Send(context.Background(), WebhookPayload{Message: "one"})
	n.Update(&Settings{WebhookURL: s2.URL})
	n.Send(context.Background(), WebhookPayload{Message: "two"})

	if len(first.calls()) != 1 || len(second.calls()) != 1 {
		t.Errorf("calls split %d/%d, want 1/1", len(first.calls()), len(second.calls()))
	}
}

// Messages are Chinese as often as not, and a byte cut lands mid-character and
// hands the receiver invalid UTF-8.
func TestSendTruncatesOnRuneBoundaries(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	long := strings.Repeat("待", 5000)
	NewNotifier(&Settings{WebhookURL: srv.URL}).Send(context.Background(), WebhookPayload{Message: long})

	got := rec.calls()[0].Message
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("message was not truncated")
	}
	if n := len([]rune(got)); n != 4001 {
		t.Errorf("got %d runes, want 4000 plus the ellipsis", n)
	}
	if !strings.HasPrefix(got, "待待") {
		t.Errorf("truncation corrupted the text: %q", got[:12])
	}
}

func TestSendTestReportsFailures(t *testing.T) {
	if err := NewNotifier(&Settings{}).SendTest(context.Background()); err == nil {
		t.Error("an unconfigured webhook must report why the test did nothing")
	}

	rec := &recorder{status: http.StatusForbidden}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	err := NewNotifier(&Settings{WebhookURL: srv.URL}).SendTest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want the status in it", err)
	}

	ok := &recorder{}
	okSrv := httptest.NewServer(ok.handler())
	defer okSrv.Close()
	if err := NewNotifier(&Settings{WebhookURL: okSrv.URL}).SendTest(context.Background()); err != nil {
		t.Errorf("SendTest: %v", err)
	}
	if got := ok.calls()[0].Event; got != WebhookEventTest {
		t.Errorf("event = %q, want %q", got, WebhookEventTest)
	}
}

func TestNotifyScheduledRunShapesTheMessage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     agent.PromptRun
		want    string
		wantErr string
		push    bool
	}{
		{
			name: "answer",
			run:  agent.PromptRun{Prompt: "check the deploy", Answer: "  all green  "},
			want: "all green",
			push: true,
		},
		{
			// A stop is the user's own doing, so it is an outcome and not a
			// fault — and not worth pushing anywhere.
			name: "cancelled",
			run:  agent.PromptRun{Prompt: "long one", Cancelled: true},
			want: "Cancelled",
		},
		{
			name:    "failed",
			run:     agent.PromptRun{Prompt: "check the deploy", Err: errors.New("no route to host")},
			want:    "Run failed: no route to host",
			wantErr: "Run failed: no route to host",
			push:    true,
		},
		{
			// A run that answered nothing still has to say it happened, or a
			// silent webhook looks the same as a schedule that never fired.
			name: "empty answer",
			run:  agent.PromptRun{Prompt: "tidy up"},
			want: "Scheduled task finished",
			push: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			srv := httptest.NewServer(rec.handler())
			defer srv.Close()

			s := testNoticeService(srv.URL)
			s.NotifyScheduledRun(context.Background(), tc.run)

			if !tc.push {
				// A stop is the user's own doing and stays local: they were at
				// the machine, they pressed the button, and a message telling
				// them what they just did is noise.
				time.Sleep(150 * time.Millisecond)
				if got := len(rec.calls()); got != 0 {
					t.Fatalf("got %d calls for a cancelled run, want 0", got)
				}
				return
			}
			calls := rec.waitForCalls(t, 1)
			if calls[0].Message != tc.want {
				t.Errorf("message = %q, want %q", calls[0].Message, tc.want)
			}
			if calls[0].Error != tc.wantErr {
				t.Errorf("error = %q, want %q", calls[0].Error, tc.wantErr)
			}
			if calls[0].Event != WebhookEventSchedule {
				t.Errorf("event = %q", calls[0].Event)
			}
			if calls[0].Source != strings.TrimSpace(tc.run.Prompt) {
				t.Errorf("source = %q, want the schedule's prompt", calls[0].Source)
			}
		})
	}
}

// The scheduler observers call this on every run, configured or not.
func TestNotifyScheduledRunIsSafeWithoutAWebhook(t *testing.T) {
	var nilService *Service
	nilService.NotifyScheduledRun(context.Background(), agent.PromptRun{Answer: "x"})

	s := testNoticeService("")
	s.NotifyScheduledRun(context.Background(), agent.PromptRun{Answer: "x"})
}

// testNoticeService wires the fan-out the way NewService does, without building
// a whole agent.
func testNoticeService(webhookURL string) *Service {
	n := NewNotifier(&Settings{WebhookURL: webhookURL})
	return &Service{notifier: n, notices: NewNotices(n)}
}

// The persona may end an answer with a trailing "MOOD: x" tag for the avatar.
// The chat transcript peels it off; a webhook that did not would put an
// internal marker at the bottom of every message the user reads elsewhere.
func TestNotifyScheduledRunStripsTheEmotionTag(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	s := testNoticeService(srv.URL)
	s.NotifyScheduledRun(context.Background(), agent.PromptRun{
		Prompt: "check the deploy",
		Answer: "all green\n\nMOOD: neutral",
	})

	got := rec.waitForCalls(t, 1)[0].Message
	if got != "all green" {
		t.Errorf("message = %q, want the answer without the emotion tag", got)
	}
}
