package backend

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeBotAPI stands in for api.telegram.org and records what was asked of it.
type fakeBotAPI struct {
	*httptest.Server
	mu    sync.Mutex
	calls []url.Values
	reply func(method string, q url.Values) string
}

func newFakeBotAPI(t *testing.T) *fakeBotAPI {
	t.Helper()
	f := &fakeBotAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q, _ := url.ParseQuery(string(body))
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

		f.mu.Lock()
		q.Set("_method", method)
		f.calls = append(f.calls, q)
		reply := f.reply
		f.mu.Unlock()

		out := `{"ok":true,"result":[]}`
		if reply != nil {
			if s := reply(method, q); s != "" {
				out = s
			}
		}
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeBotAPI) sent() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]url.Values, 0, len(f.calls))
	for _, c := range f.calls {
		if c.Get("_method") == "sendMessage" {
			out = append(out, c)
		}
	}
	return out
}

// bridgeForTest wires a bridge to the fake API without a Service: the tests
// here cover everything that happens before a turn runs.
func bridgeForTest(t *testing.T, api *fakeBotAPI, allowed ...int64) *TelegramBridge {
	t.Helper()
	m := map[int64]bool{}
	for _, id := range allowed {
		m[id] = true
	}
	return &TelegramBridge{
		token:    "test-token",
		allowed:  m,
		api:      api.URL,
		client:   api.Client(),
		logger:   log.New(io.Discard, "", 0),
		sessions: map[int64]string{},
	}
}

func TestATokenWithNoAllowListIsRefused(t *testing.T) {
	_, err := NewTelegramBridge(&Service{}, &Settings{TelegramBotToken: "t"})
	if err == nil {
		t.Fatal("a bot anyone can find was accepted with no allow-list")
	}
	if !strings.Contains(err.Error(), "telegram_allowed_chats") {
		t.Fatalf("the error does not name the setting to fix: %v", err)
	}
}

func TestNoTokenMeansNoBridgeAndNoError(t *testing.T) {
	b, err := NewTelegramBridge(&Service{}, &Settings{})
	if err != nil || b != nil {
		t.Fatalf("not configuring Telegram should be silent, got (%v, %v)", b, err)
	}
}

func TestAMessageFromAnUnknownChatIsNotAnswered(t *testing.T) {
	api := newFakeBotAPI(t)
	b := bridgeForTest(t, api, 111)

	b.handle(context.Background(), updateFrom(999, "hello"))

	if got := api.sent(); len(got) != 0 {
		t.Fatalf("a stranger got a reply: %v", got)
	}
}

func TestSlashNewMovesTheChatToAFreshSession(t *testing.T) {
	api := newFakeBotAPI(t)
	b := bridgeForTest(t, api, 111)

	before := b.sessionFor(111)
	b.handle(context.Background(), updateFrom(111, "/new"))
	after := b.sessionFor(111)

	if before == after {
		t.Fatalf("/new left the chat in the same session %q", before)
	}
	if !strings.HasPrefix(after, "telegram:111:") {
		t.Fatalf("the new session is not tied to the chat: %q", after)
	}
	if got := api.sent(); len(got) != 1 {
		t.Fatalf("/new should be acknowledged once, got %d messages", len(got))
	}
}

func TestTheDefaultSessionIsStableForAChat(t *testing.T) {
	b := bridgeForTest(t, newFakeBotAPI(t), 111)
	if a, c := b.sessionFor(111), b.sessionFor(111); a != c {
		t.Fatalf("a chat changed session without being asked: %q then %q", a, c)
	}
	if b.sessionFor(111) == b.sessionFor(222) {
		t.Fatal("two chats share one session, so they would read each other's history")
	}
}

func TestTheBacklogIsDiscardedRatherThanAnswered(t *testing.T) {
	api := newFakeBotAPI(t)
	api.reply = func(method string, q url.Values) string {
		if method == "getUpdates" {
			return `{"ok":true,"result":[{"update_id":7,"message":{"text":"hi","chat":{"id":111}}}]}`
		}
		return ""
	}
	b := bridgeForTest(t, api, 111)

	next, err := b.drainBacklog(context.Background())
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if next != 8 {
		t.Fatalf("the next poll would re-read the backlog: offset %d, want 8", next)
	}
	if got := api.sent(); len(got) != 0 {
		t.Fatalf("a message queued while the app was down was answered: %v", got)
	}
}

func TestALongAnswerIsSplitIntoSendablePieces(t *testing.T) {
	api := newFakeBotAPI(t)
	b := bridgeForTest(t, api, 111)

	// Lines, so the split has a break to prefer.
	long := strings.TrimRight(strings.Repeat("a line of text\n", 500), "\n")
	b.send(context.Background(), 111, long)

	sent := api.sent()
	if len(sent) < 2 {
		t.Fatalf("a %d-character answer was sent as %d message(s)", len([]rune(long)), len(sent))
	}
	var rebuilt []string
	for _, m := range sent {
		text := m.Get("text")
		if n := len([]rune(text)); n > telegramMessageLimit {
			t.Fatalf("a piece is %d characters, over Telegram's %d", n, telegramMessageLimit)
		}
		rebuilt = append(rebuilt, text)
	}
	// Nothing lost: the pieces put back together are the answer again. The
	// joiner is the newline the split consumed.
	if got := strings.Join(rebuilt, "\n"); got != long {
		t.Fatalf("splitting changed the text\n got %d chars\nwant %d chars", len(got), len(long))
	}
}

func TestAShortAnswerIsOneMessage(t *testing.T) {
	if got := splitForTelegram("hello", telegramMessageLimit); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("a short answer was not left alone: %v", got)
	}
}

func TestSplittingDoesNotCutInsideAWord(t *testing.T) {
	// One long line of words: no newline to prefer, so the space rule decides.
	text := strings.TrimSpace(strings.Repeat("word ", 2000))
	for _, part := range splitForTelegram(text, 100) {
		if strings.HasPrefix(part, "ord") || strings.HasSuffix(part, "wor") {
			t.Fatalf("a split landed inside a word: %q", part)
		}
	}
}

func TestTelegramErrorsCarryTheAPIsOwnReason(t *testing.T) {
	api := newFakeBotAPI(t)
	api.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ok":false,"description":"Bad Request: chat not found"}`)
	})
	b := bridgeForTest(t, api, 111)

	err := b.call(context.Background(), "sendMessage", url.Values{}, nil)
	if err == nil {
		t.Fatal("a 400 was reported as success")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("the reason was dropped on the way out: %v", err)
	}
}

func TestTwoBridgesDoNotBothPoll(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	first, err := AcquireFileLock(telegramLockName)
	if err != nil || first == nil {
		t.Fatalf("first claim failed: (%v, %v)", first, err)
	}
	defer first.Release()

	second, err := AcquireFileLock(telegramLockName)
	if err != nil {
		t.Fatalf("second claim errored instead of declining: %v", err)
	}
	if second != nil {
		second.Release()
		t.Fatal("two processes both took the poll, which silently splits the updates between them")
	}
}

func updateFrom(chat int64, text string) telegramUpdate {
	u := telegramUpdate{UpdateID: 1}
	u.Message = new(struct {
		Text string `json:"text"`
		From *struct {
			Username string `json:"username"`
		} `json:"from"`
		Chat *struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	})
	u.Message.Text = text
	u.Message.Chat = &struct {
		ID int64 `json:"id"`
	}{ID: chat}
	return u
}
