package backend

import (
	"context"
	"errors"
	"log/slog"
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
// 08:00 was written to a log and to nobody. This is the third surface.
//
// It started as one JSON POST to one URL, deliberately not a set of
// per-service integrations: a relay was meant to turn the POST into whatever a
// phone shows. Nobody wanted to run the relay. What people had was a Feishu
// group bot, a WeCom bot, a DingTalk robot, a Slack incoming webhook, Bark on
// the phone — each a URL to paste, each with its own envelope and signature.
// So the one URL became a list of channels (channel.go), and the Notifier
// became the one dispatcher in front of them: every notice goes through Send,
// each channel takes the events it asked for, formats them its own way
// (channel_adapters.go), and its last success or failure is kept for the page
// (channel_status.go). The generic webhook is still there, with the payload
// below unchanged.

// WebhookEvent names the reason for the call, so a receiver can route or filter
// without parsing the body. These are part of the wire contract.
const (
	WebhookEventNotify   = "notify"    // notify_user, mid-task
	WebhookEventSchedule = "schedule"  // a scheduled prompt finished
	WebhookEventTest     = "test"      // the "send a test" button
	WebhookEventNeedsYou = "needs_you" // a tool call is waiting for approval
	WebhookEventReply    = "reply"     // the answer to a message received on the channel
)

// WebhookPayload is the JSON body posted to the configured URL.
//
// Field names are lower_snake and the shape is flat on purpose: most receivers
// are a three-line script or a no-code bridge, and every level of nesting is
// another thing for the person wiring it up to get wrong.
type WebhookPayload struct {
	Event string `json:"event"`
	// Kind is the channel event this belongs to: reminder, schedule,
	// needs_you, failed (ChannelEvent*), or reply. Added with channels;
	// event keeps its old values.
	Kind    string `json:"kind,omitempty"`
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
	// RequestID and Conversation tie a reply to the inbound message it
	// answers (channel inbound, reply mode "callback").
	RequestID    string `json:"request_id,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	SentAt       string `json:"sent_at"`
}

// eventOf is the channel event a payload belongs to.
func eventOf(p WebhookPayload) string {
	if p.Kind != "" {
		return p.Kind
	}
	if p.Error != "" {
		return ChannelEventFailed
	}
	switch p.Event {
	case WebhookEventSchedule:
		return ChannelEventSchedule
	case WebhookEventNeedsYou:
		return ChannelEventNeedsYou
	}
	return ChannelEventReminder
}

// Notifier is the dispatcher: it holds the channels and sends each payload to
// the ones that want it.
//
// Held by the Service and read by both the scheduler observers and the
// notify_user tool. A nil Notifier, or one with no channel to send to, is a
// no-op: not configuring one is the default, and the callers should not have
// to check.
type Notifier struct {
	mu       sync.RWMutex
	channels []Channel
	client   *http.Client
}

// NewNotifier builds a dispatcher for the settings' channels.
func NewNotifier(s *Settings) *Notifier {
	n := &Notifier{
		// A receiver that hangs must not hold a scheduler goroutine, and a
		// notification is worthless late, so the timeout is short and there is
		// no retry: the next reminder is a better use of the time than a
		// redelivery of one nobody saw.
		client: &http.Client{Timeout: 10 * time.Second},
	}
	n.Update(s)
	return n
}

// Update points the dispatcher at the current settings. Called on every
// rebuild and every channel save, so a change takes effect without a restart.
func (n *Notifier) Update(s *Settings) {
	if n == nil {
		return
	}
	chs := EffectiveChannels(s)
	n.mu.Lock()
	n.channels = chs
	n.mu.Unlock()
}

// Enabled reports whether any channel can be sent to.
func (n *Notifier) Enabled() bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, c := range n.channels {
		if c.Enabled && c.CanSend() && len(c.Events) > 0 {
			return true
		}
	}
	return false
}

// Send delivers one payload to every enabled channel that wants its event, in
// parallel, and returns when all have answered. It never returns an error: a
// notification is a side channel, and a failing receiver must not fail the
// turn that produced the message. Each failure is logged and recorded on its
// channel, which is where the settings page shows it.
func (n *Notifier) Send(ctx context.Context, p WebhookPayload) {
	if n == nil {
		return
	}
	event := eventOf(p)
	n.mu.RLock()
	var targets []Channel
	for _, c := range n.channels {
		if c.Enabled && c.CanSend() && c.Wants(event) {
			targets = append(targets, c)
		}
	}
	client := n.client
	n.mu.RUnlock()
	if len(targets) == 0 {
		return
	}
	p = preparePayload(p)
	if p.Kind == "" {
		p.Kind = event
	}

	// Detached from the caller's context on purpose. The scheduler cancels a
	// run's context as soon as the run ends, and the notification is sent at
	// exactly that moment — inheriting it would cancel every delivery.
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range targets {
		wg.Add(1)
		go func(c Channel) {
			defer wg.Done()
			err := deliverChannel(sendCtx, client, c, p)
			RecordChannelSend(c.ID, err)
			if err != nil {
				slog.WarnContext(ctx, "channel delivery failed", "channel", c.ID, "type", c.Type, "error", err)
			}
		}(c)
	}
	wg.Wait()
}

// SendTest sends a sample to the first webhook channel, for the settings
// screens that predate channels (TestWebhook). Unlike Send it reports the
// failure, because here the user is standing in front of the result.
func (n *Notifier) SendTest(ctx context.Context) error {
	if n == nil {
		return errors.New("no webhook URL configured")
	}
	n.mu.RLock()
	chs := cloneChannels(n.channels)
	n.mu.RUnlock()
	i := primaryIndex(chs, ChannelWebhook)
	if i < 0 || chs[i].URL == "" {
		return errors.New("no webhook URL configured")
	}
	return TestChannel(ctx, chs[i])
}

// TestChannel sends a sample message through one channel, enabled or not,
// records the outcome on it and reports it.
func TestChannel(ctx context.Context, c Channel) error {
	var err error
	if !c.CanSend() {
		err = errors.New(nothingToSendTo(c))
	} else {
		err = deliverChannel(ctx, &http.Client{Timeout: 10 * time.Second}, c, preparePayload(WebhookPayload{
			Event:   WebhookEventTest,
			Kind:    WebhookEventTest,
			Title:   "SuperLeo",
			Message: "Test notification from SuperLeo. Receiving this means the channel is configured correctly.",
		}))
	}
	RecordChannelSend(c.ID, err)
	return err
}

// Reply sends the answer to an inbound message back through the channel's
// own URL (reply mode "callback").
func Reply(ctx context.Context, c Channel, p WebhookPayload) error {
	if c.URL == "" {
		err := errors.New("no send URL to post the answer to")
		RecordChannelSend(c.ID, err)
		return err
	}
	p.Event, p.Kind = WebhookEventReply, WebhookEventReply
	err := deliverChannel(ctx, &http.Client{Timeout: 15 * time.Second}, c, preparePayload(p))
	RecordChannelSend(c.ID, err)
	return err
}

func nothingToSendTo(c Channel) string {
	switch c.Type {
	case ChannelTelegram:
		return "no bot token or no allowed chat"
	case ChannelBark:
		return "no device key"
	}
	return "no URL to send to"
}

func preparePayload(p WebhookPayload) WebhookPayload {
	if p.SentAt == "" {
		p.SentAt = time.Now().Format(time.RFC3339)
	}
	if p.Title == "" {
		p.Title = "SuperLeo"
	}
	p.Message = truncateRunes(p.Message, 4000)
	p.Source = truncateRunes(p.Source, 300)
	return p
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

// truncateBytes cuts to at most n bytes of UTF-8, on a character boundary,
// for the services whose limits are in bytes (WeCom, DingTalk, Feishu).
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const ell = "…"
	cut := n - len(ell)
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " \n") + ell
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
