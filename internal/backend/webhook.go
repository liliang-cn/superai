package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Outbound notifications.
//
// Everything the agent had to say used to arrive on one of two surfaces, and
// both of them require someone to already be looking: notify_user rides the SSE
// stream to an open page, and the desktop build posts a macOS notification. In
// serve mode on a headless box there is neither, so a reminder that fired at
// 08:00 was written to a log and to nobody. This is the third surface: one HTTP
// POST, to a URL the user configures, that any of Telegram, a WeCom bot, bark,
// ntfy or a home-made receiver can be pointed at.
//
// Deliberately not a set of per-service integrations. Each of those wants its
// own credential shape, its own message envelope and its own idea of failure,
// and the list is never finished. A JSON POST is the one thing they all accept
// behind a two-line adapter, and it keeps the agent's side to a single code
// path that can actually be tested.

// WebhookEvent names the reason for the call, so a receiver can route or filter
// without parsing the body. These are part of the wire contract.
const (
	WebhookEventNotify   = "notify"   // notify_user, mid-task
	WebhookEventSchedule = "schedule" // a scheduled prompt finished
	WebhookEventTest     = "test"     // the "send a test" button
)

// WebhookPayload is the JSON body posted to the configured URL.
//
// Field names are lower_snake and the shape is flat on purpose: most receivers
// are a three-line script or a no-code bridge, and every level of nesting is
// another thing for the person wiring it up to get wrong.
type WebhookPayload struct {
	Event   string `json:"event"`
	Title   string `json:"title"`
	Message string `json:"message"`
	// Source is the schedule's prompt for a scheduled run, empty otherwise.
	Source string `json:"source,omitempty"`
	// Error is set when the run failed; Message then carries the reason too, so
	// a receiver that only prints Message still tells the user something broke.
	Error string `json:"error,omitempty"`
	// Cancelled marks a run the user stopped, which is an outcome and not a
	// fault — the same distinction the chat transcript makes.
	Cancelled bool   `json:"cancelled,omitempty"`
	Host      string `json:"host,omitempty"`
	SentAt    string `json:"sent_at"`
}

// Notifier posts payloads to the configured webhook.
//
// Held by the Service and read by both the scheduler observers and the
// notify_user tool. A nil Notifier, or one with no URL, is a no-op: not
// configuring a webhook is the default, and the callers should not have to
// check.
type Notifier struct {
	mu     sync.RWMutex
	url    string
	secret string
	client *http.Client
}

// NewNotifier builds a notifier for the settings' webhook.
func NewNotifier(s *Settings) *Notifier {
	n := &Notifier{
		// A webhook that hangs must not hold a scheduler goroutine, and a
		// notification is worthless late, so the timeout is short and there is
		// no retry: the next reminder is a better use of the time than a
		// redelivery of one nobody saw.
		client: &http.Client{Timeout: 10 * time.Second},
	}
	n.Update(s)
	return n
}

// Update points the notifier at the current settings. Called on every rebuild,
// so changing the URL takes effect without a restart.
func (n *Notifier) Update(s *Settings) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if s == nil {
		n.url, n.secret = "", ""
		return
	}
	n.url = strings.TrimSpace(s.WebhookURL)
	n.secret = strings.TrimSpace(s.WebhookSecret)
}

// Enabled reports whether a URL is configured.
func (n *Notifier) Enabled() bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.url != ""
}

// Send posts one payload. It never returns an error: a notification is a
// side channel, and a failing webhook must not fail the turn that produced the
// message. Failures are logged, once, with the status — enough to debug a wrong
// URL without turning every offline minute into a wall of text.
func (n *Notifier) Send(ctx context.Context, p WebhookPayload) {
	if n == nil {
		return
	}
	n.mu.RLock()
	url, secret, client := n.url, n.secret, n.client
	n.mu.RUnlock()
	if url == "" || client == nil {
		return
	}

	if p.SentAt == "" {
		p.SentAt = time.Now().Format(time.RFC3339)
	}
	if p.Title == "" {
		p.Title = "SuperAI"
	}
	p.Message = truncateRunes(p.Message, 4000)
	p.Source = truncateRunes(p.Source, 300)

	body, err := json.Marshal(p)
	if err != nil {
		log.Printf("superai: webhook payload: %v", err)
		return
	}

	// Detached from the caller's context on purpose. The scheduler cancels a
	// run's context as soon as the run ends, and the notification is sent at
	// exactly that moment — inheriting it would cancel every delivery.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("superai: webhook request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		// Bearer rather than a signature: the receiver is usually a few lines
		// of script, and a shared secret it can compare is the thing it will
		// actually check. A signature nobody verifies is worse than a token.
		req.Header.Set("Authorization", "Bearer "+secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("superai: webhook post failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("superai: webhook returned %s", resp.Status)
	}
}

// SendTest posts a sample payload so the settings screen can prove the URL
// works. Unlike Send it reports the failure, because here the user is standing
// in front of the result and a silent no-op is the worst answer.
func (n *Notifier) SendTest(ctx context.Context) error {
	if !n.Enabled() {
		return fmt.Errorf("no webhook URL configured")
	}
	n.mu.RLock()
	url, secret, client := n.url, n.secret, n.client
	n.mu.RUnlock()

	body, _ := json.Marshal(WebhookPayload{
		Event:   WebhookEventTest,
		Title:   "SuperAI",
		Message: "Test notification from SuperAI. Receiving this means the webhook is configured correctly.",
		SentAt:  time.Now().Format(time.RFC3339),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

// truncateRunes cuts to n runes, never bytes: the messages are Chinese, and a
// byte cut lands mid-character and hands the receiver invalid UTF-8.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
