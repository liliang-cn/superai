package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Telegram: the way in.
//
// The webhook (webhook.go) carries messages out — a scheduled run finished, the
// agent called notify_user — and deliberately knows nothing about Telegram: one
// JSON POST, and a relay turns it into whatever the person actually reads.
//
// This is the other direction, and it is not the same shape wearing a different
// hat. An outbound notice is one message with no reply and no state; a turn
// started from Telegram is a conversation — it needs a session that remembers
// the last thing said, it runs the model and its tools, and it can take a
// minute, during which the sender is looking at a chat window with nothing in
// it. None of that is expressible as "POST some JSON somewhere", which is why
// this is a real integration and the outbound side still is not.
//
// It lives in the app rather than in a bridge of its own because the point is
// the *same* assistant: same settings, same memory, same sessions, so a
// conversation started on the phone is there on the desktop and the reverse.
// A separate process would have had to reach all of that back through an API
// and would still have got a second agent's worth of state wrong.
//
// Three things it refuses to do:
//
//   - Answer anyone not on the allow-list. A bot's username is public and
//     searchable, so "who can message it" is not a question with a comfortable
//     default; this agent runs shell commands. With no allow-list configured
//     the poller does not start at all, rather than starting open.
//   - Poll in two processes. See filelock.go: two long polls on one bot split
//     the updates between them, silently.
//   - Answer its own backlog on startup. Telegram keeps undelivered updates for
//     24 hours, so a bot that was off all night would wake up and work through
//     every message at once. The first poll discards what is queued and starts
//     from now.

const (
	// telegramLockName is the lock that keeps a second process from polling.
	telegramLockName = "telegram.lock"

	// telegramPollSeconds is the long-poll hold. Telegram holds the request
	// open until an update arrives or this expires, so a high number is not
	// laziness — it is one request per fifty seconds instead of fifty.
	telegramPollSeconds = 50

	// telegramMessageLimit is Telegram's own cap on a message, in UTF-16 code
	// units. Answers run past it often enough (a table, a listing) that
	// splitting is the normal path, not an edge case.
	telegramMessageLimit = 4096

	// telegramTypingRefresh is how often "typing…" is re-sent. Telegram clears
	// the indicator after five seconds, so a turn that thinks for a minute
	// looks dead without this.
	telegramTypingRefresh = 4 * time.Second
)

// TelegramBridge polls a bot for messages and answers them with the agent.
type TelegramBridge struct {
	svc     *Service
	token   string
	allowed map[int64]bool
	api     string // base URL, overridable in tests
	client  *http.Client
	logger  *log.Logger

	mu sync.Mutex
	// sessions maps a chat to the session its turns run in. Absent means the
	// default for that chat; /new replaces the entry rather than deleting the
	// history, so the old conversation is still in the History list.
	sessions map[int64]string

	stop context.CancelFunc
	done chan struct{}
	lock *FileLock
}

// NewTelegramBridge builds a bridge from settings. It returns (nil, nil) when
// Telegram is not configured, which is the default and not an error.
//
// A token with no allow-list is refused rather than defaulted: see the note
// above about who can find a bot.
func NewTelegramBridge(svc *Service, s *Settings) (*TelegramBridge, error) {
	if svc == nil || s == nil {
		return nil, nil
	}
	token := strings.TrimSpace(s.TelegramBotToken)
	if token == "" {
		return nil, nil
	}
	if len(s.TelegramAllowedChats) == 0 {
		return nil, errors.New("telegram_bot_token is set but telegram_allowed_chats is empty: a bot anyone can find would be an agent anyone can run commands with")
	}
	allowed := make(map[int64]bool, len(s.TelegramAllowedChats))
	for _, id := range s.TelegramAllowedChats {
		allowed[id] = true
	}
	return &TelegramBridge{
		svc:     svc,
		token:   token,
		allowed: allowed,
		api:     "https://api.telegram.org",
		// Longer than the long poll, or every poll would be cancelled from
		// this side just as Telegram was about to answer it.
		client:   &http.Client{Timeout: (telegramPollSeconds + 20) * time.Second},
		logger:   log.Default(),
		sessions: map[int64]string{},
	}, nil
}

// Start claims the poller and runs it until Stop. It returns (false, nil) when
// another process holds the lock — the ordinary case on a machine running both
// the app and the daemon, and not a failure.
func (b *TelegramBridge) Start() (bool, error) {
	if b == nil {
		return false, nil
	}
	lock, err := AcquireFileLock(telegramLockName)
	if err != nil {
		return false, err
	}
	if lock == nil {
		return false, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.stop, b.lock, b.done = cancel, lock, make(chan struct{})
	done := b.done
	b.mu.Unlock()

	go func() {
		defer close(done)
		b.poll(ctx)
	}()
	return true, nil
}

// Stop ends the poll and waits for the loop to leave. Safe on a nil bridge and
// on one that never started.
func (b *TelegramBridge) Stop() {
	if b == nil {
		return
	}
	b.mu.Lock()
	stop, done, lock := b.stop, b.done, b.lock
	b.stop, b.done, b.lock = nil, nil, nil
	b.mu.Unlock()

	if stop != nil {
		stop()
	}
	if done != nil {
		// Bounded: a turn in flight holds the loop, and with no deadline on a
		// turn that wait is unbounded — shutdown must not hang on a model
		// still talking. Cancelling ctx above is what actually ends it; this
		// only decides how long to be polite about it.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	lock.Release()
}

// poll is the long-poll loop.
func (b *TelegramBridge) poll(ctx context.Context) {
	// Zero means "whatever is queued". The first pass throws that away and only
	// then starts answering, so an app that was off overnight does not wake up
	// and reply to yesterday.
	offset, err := b.drainBacklog(ctx)
	if err != nil && ctx.Err() == nil {
		b.logf("telegram: could not reach the API, retrying: %v", err)
	}

	fails := 0
	for ctx.Err() == nil {
		updates, next, err := b.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			fails++
			// Backs off to half a minute and stays there. The network being
			// down is not a reason to stop, and it is not a reason to hammer.
			wait := time.Duration(min(fails, 6)) * 5 * time.Second
			b.logf("telegram: poll failed (%v), retrying in %s", err, wait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		fails = 0
		offset = next

		for _, u := range updates {
			if ctx.Err() != nil {
				return
			}
			b.handle(ctx, u)
		}
	}
}

// drainBacklog confirms everything queued without answering it, and reports the
// offset to carry on from.
func (b *TelegramBridge) drainBacklog(ctx context.Context) (int64, error) {
	updates, next, err := b.getUpdates(ctx, 0)
	if err != nil {
		return 0, err
	}
	if len(updates) > 0 {
		b.logf("telegram: discarded %d message(s) queued while this was not running", len(updates))
	}
	return next, nil
}

// telegramUpdate is the part of an update this cares about. Everything else
// Telegram sends — edits, joins, reactions — is ignored by not being here.
type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		From *struct {
			Username string `json:"username"`
		} `json:"from"`
		Chat *struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

// getUpdates makes one long poll and returns the updates plus the next offset.
func (b *TelegramBridge) getUpdates(ctx context.Context, offset int64) ([]telegramUpdate, int64, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	q.Set("timeout", strconv.Itoa(telegramPollSeconds))
	// Asking for only what is handled keeps the backlog from filling with
	// update types this will never read.
	q.Set("allowed_updates", `["message"]`)

	var out struct {
		Result []telegramUpdate `json:"result"`
	}
	if err := b.call(ctx, "getUpdates", q, &out); err != nil {
		return nil, offset, err
	}
	next := offset
	for _, u := range out.Result {
		if u.UpdateID >= next {
			next = u.UpdateID + 1
		}
	}
	return out.Result, next, nil
}

// handle answers one message.
func (b *TelegramBridge) handle(ctx context.Context, u telegramUpdate) {
	m := u.Message
	if m == nil || m.Chat == nil {
		return
	}
	chat := m.Chat.ID
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return
	}

	if !b.allowed[chat] {
		// Logged with the chat id because that is exactly what has to be added
		// to the allow-list if this was the owner messaging from somewhere new,
		// and exactly what is worth seeing if it was not.
		who := ""
		if m.From != nil && m.From.Username != "" {
			who = " (@" + m.From.Username + ")"
		}
		b.logf("telegram: ignored a message from chat %d%s — not in telegram_allowed_chats", chat, who)
		return
	}

	switch {
	case text == "/start":
		b.send(ctx, chat, "SuperAI is listening. Send anything; /new starts a fresh conversation.")
		return
	case text == "/new":
		b.mu.Lock()
		b.sessions[chat] = fmt.Sprintf("telegram:%d:%d", chat, time.Now().Unix())
		b.mu.Unlock()
		b.send(ctx, chat, "New conversation. The previous one is still in History.")
		return
	}

	// No deadline, the same as the UI. A turn that takes twenty minutes is a
	// turn that was worth twenty minutes; cutting it off would send "that turn
	// failed: context deadline exceeded" to someone who was waiting for the
	// answer, and throw the work away.
	//
	// The cost is that this loop answers one message at a time, so a genuinely
	// wedged provider call now blocks the bot until the process restarts,
	// where before it cleared itself after ten minutes. That is the trade the
	// deadline was making, and it is the caller's to make: shutdown still cuts
	// through it, because ctx is the poller's.
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stopTyping := b.keepTyping(turnCtx, chat)
	final, err := b.svc.Stream(turnCtx, b.sessionFor(chat), text, nil, nil)
	stopTyping()

	if err != nil {
		// Reported to the chat, not only to the log: the person is holding a
		// phone waiting for an answer, and silence is the one response that
		// tells them nothing.
		b.logf("telegram: turn failed for chat %d: %v", chat, err)
		b.send(context.WithoutCancel(ctx), chat, "That turn failed: "+err.Error())
		return
	}

	// The persona may end with a trailing MOOD tag that drives the avatar. It
	// is an internal marker; the chat transcript strips it and so must this.
	reply, _ := SplitEmotion(final)
	if strings.TrimSpace(reply) == "" {
		reply = "(no answer)"
	}
	b.send(context.WithoutCancel(ctx), chat, reply)
}

// sessionFor is the session this chat's turns run in.
func (b *TelegramBridge) sessionFor(chat int64) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.sessions[chat]; ok {
		return s
	}
	return fmt.Sprintf("telegram:%d", chat)
}

// keepTyping shows "typing…" until the returned function is called.
func (b *TelegramBridge) keepTyping(ctx context.Context, chat int64) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(telegramTypingRefresh)
		defer t.Stop()
		for {
			q := url.Values{}
			q.Set("chat_id", strconv.FormatInt(chat, 10))
			q.Set("action", "typing")
			// Failures are ignored on purpose: this is decoration, and an
			// error here must not be mistaken for the turn failing.
			_ = b.call(ctx, "sendChatAction", q, nil)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return cancel
}

// send delivers text, splitting it when Telegram would refuse the length.
func (b *TelegramBridge) send(ctx context.Context, chat int64, text string) {
	for _, part := range splitForTelegram(text, telegramMessageLimit) {
		q := url.Values{}
		q.Set("chat_id", strconv.FormatInt(chat, 10))
		q.Set("text", part)
		if err := b.call(ctx, "sendMessage", q, nil); err != nil {
			b.logf("telegram: send to chat %d failed: %v", chat, err)
			return
		}
	}
}

// splitForTelegram cuts text into sendable pieces, preferring a line break and
// then a space, so a split lands between words rather than inside one.
func splitForTelegram(text string, limit int) []string {
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	var out []string
	for len(runes) > limit {
		cut := limit
		if i := lastIndexRune(runes[:limit], '\n'); i > limit/2 {
			cut = i + 1
		} else if i := lastIndexRune(runes[:limit], ' '); i > limit/2 {
			cut = i + 1
		}
		out = append(out, strings.TrimRight(string(runes[:cut]), " \n"))
		runes = runes[cut:]
	}
	if rest := strings.TrimSpace(string(runes)); rest != "" {
		out = append(out, rest)
	}
	return out
}

func lastIndexRune(rs []rune, target rune) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == target {
			return i
		}
	}
	return -1
}

// call makes one Bot API request. out may be nil when the reply is not read.
func (b *TelegramBridge) call(ctx context.Context, method string, q url.Values, out any) error {
	endpoint := fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(b.api, "/"), b.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(q.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		// The body carries Telegram's own description ("chat not found",
		// "Unauthorized"), which is the whole diagnosis most of the time.
		return fmt.Errorf("%s: %s: %s", method, resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (b *TelegramBridge) logf(format string, args ...any) {
	if b.logger != nil {
		b.logger.Printf(format, args...)
	}
}
