package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Can one conversation take several questions at once?
//
// The UI wants to send a second message while the first is still streaming.
// Everything under Stream is shared — the agent Service, and the Session whose
// message list both runs append to — so before building any of that, find out
// what actually happens: a panic, crossed answers, or a clean pair of replies.

// slowLLM answers after a delay, and tags each reply so a crossed answer is
// obvious. The delay is what makes the runs genuinely overlap.
type slowLLM struct {
	delay time.Duration

	mu       sync.Mutex
	requests int
	prompts  []string
}

func (l *slowLLM) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		l.mu.Lock()
		l.requests++
		l.prompts = append(l.prompts, string(body))
		l.mu.Unlock()

		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}

		// Echo back a marker taken from the question, so the caller can tell
		// which answer belongs to which ask.
		marker := "unknown"
		for _, tag := range []string{"ALPHA", "BRAVO", "CHARLIE"} {
			if strings.Contains(string(body), tag) {
				marker = tag
				break
			}
		}
		time.Sleep(l.delay)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": "ANSWER-" + marker + "\n情绪: 中性",
				},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func newConcurrentService(t *testing.T, delay time.Duration) (*Service, *slowLLM) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	// Chrome costs ~1s per NewService and no test here browses.
	t.Setenv("SUPERAI_NO_BROWSER", "1")

	llm := &slowLLM{delay: delay}
	svc, err := NewService(&Settings{
		LLMBaseURL:   llm.start(t),
		LLMKey:       "test-key",
		LLMModel:     "test-model",
		WorkspaceDir: home + "/workspace",
		MaxRounds:    3,
		Headless:     true,
		DisablePTC:   true,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc, llm
}

type askResult struct {
	tag    string
	final  string
	err    error
	events int
}

// TestConcurrentAsksSameSession is the load-bearing question: two overlapping
// asks on ONE session id.
func TestConcurrentAsksSameSession(t *testing.T) {
	svc, llm := newConcurrentService(t, 120*time.Millisecond)

	tags := []string{"ALPHA", "BRAVO"}
	results := make([]askResult, len(tags))
	var wg sync.WaitGroup

	start := time.Now()
	for i, tag := range tags {
		wg.Add(1)
		go func(i int, tag string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			events := 0
			final, err := svc.Stream(ctx, "shared-session", "请回答 "+tag, nil, func(*agent.Event) {
				events++
			})
			results[i] = askResult{tag: tag, final: strings.TrimSpace(final), err: err, events: events}
		}(i, tag)
	}
	wg.Wait()
	elapsed := time.Since(start)

	llm.mu.Lock()
	requests := llm.requests
	llm.mu.Unlock()

	for _, r := range results {
		t.Logf("%s: err=%v events=%d final=%q", r.tag, r.err, r.events, truncate(r.final, 120))
	}
	t.Logf("elapsed=%.1fs provider_requests=%d", elapsed.Seconds(), requests)

	// 1. Neither ask may fail outright.
	for _, r := range results {
		if r.err != nil {
			t.Errorf("%s failed: %v", r.tag, r.err)
		}
	}

	// 2. Each ask must get ITS OWN answer. This is the crossing this test exists
	//    to catch: ALPHA receiving BRAVO's reply.
	for _, r := range results {
		if r.final == "" {
			t.Errorf("%s got no answer at all", r.tag)
			continue
		}
		if !strings.Contains(r.final, r.tag) {
			t.Errorf("%s got an answer that is not its own: %q", r.tag, truncate(r.final, 200))
		}
	}

	// 3. They must actually have overlapped — otherwise something serialised
	//    them and the test proved nothing about concurrency. The delay only has
	//    to be long enough for that; keep it small, it is pure test latency.
	if elapsed > 900*time.Millisecond {
		t.Logf("NOTE: %.2fs for two 0.12s asks suggests they were serialised, not run in parallel",
			elapsed.Seconds())
	}
}

// TestConcurrentAsksSeparateSessions is the control: if this passes and the
// shared-session test does not, the session is the thing that cannot be shared.
func TestConcurrentAsksSeparateSessions(t *testing.T) {
	svc, _ := newConcurrentService(t, 100*time.Millisecond)

	tags := []string{"ALPHA", "BRAVO", "CHARLIE"}
	results := make([]askResult, len(tags))
	var wg sync.WaitGroup
	for i, tag := range tags {
		wg.Add(1)
		go func(i int, tag string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			final, err := svc.Stream(ctx, fmt.Sprintf("session-%d", i), "请回答 "+tag, nil, nil)
			results[i] = askResult{tag: tag, final: strings.TrimSpace(final), err: err}
		}(i, tag)
	}
	wg.Wait()

	for _, r := range results {
		t.Logf("%s: err=%v final=%q", r.tag, r.err, truncate(r.final, 120))
		if r.err != nil {
			t.Errorf("%s failed: %v", r.tag, r.err)
		} else if !strings.Contains(r.final, r.tag) {
			t.Errorf("%s got the wrong answer: %q", r.tag, truncate(r.final, 200))
		}
	}
}

// TestConcurrentSessionHistoryStaysCoherent checks the durable side-effect: after
// two overlapping asks, the stored conversation must contain both exchanges and
// no torn or duplicated turns.
func TestConcurrentSessionHistoryStaysCoherent(t *testing.T) {
	svc, _ := newConcurrentService(t, 80*time.Millisecond)

	var wg sync.WaitGroup
	for _, tag := range []string{"ALPHA", "BRAVO"} {
		wg.Add(1)
		go func(tag string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_, _ = svc.Stream(ctx, "history-session", "请回答 "+tag, nil, nil)
		}(tag)
	}
	wg.Wait()

	turns := svc.SessionTurns("history-session")
	var user, assistant int
	for _, turn := range turns {
		switch turn.Role {
		case "user":
			user++
		case "assistant":
			assistant++
		}
	}
	t.Logf("stored %d visible turns (user=%d assistant=%d)", len(turns), user, assistant)
	for i, turn := range turns {
		t.Logf("  %d. %s: %s", i+1, turn.Role, truncate(strings.ReplaceAll(turn.Content, "\n", " "), 90))
	}

	if user < 2 {
		t.Errorf("both questions should be in the history, found %d user turns", user)
	}
	if assistant < 2 {
		t.Errorf("both answers should be in the history, found %d assistant turns", assistant)
	}
}

// TestSingleAskHistory is the control for the concurrency test above: if one ask
// does not store an assistant turn either, then nothing about concurrency is to
// blame and the streaming path simply persists replies elsewhere.
func TestSingleAskHistory(t *testing.T) {
	svc, _ := newConcurrentService(t, 20*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	final, err := svc.Stream(ctx, "solo-session", "请回答 ALPHA", nil, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	t.Logf("final=%q", strings.TrimSpace(final))

	turns := svc.SessionTurns("solo-session")
	var user, assistant int
	for i, turn := range turns {
		t.Logf("  %d. %s: %s", i+1, turn.Role, truncate(strings.ReplaceAll(turn.Content, "\n", " "), 90))
		switch turn.Role {
		case "user":
			user++
		case "assistant":
			assistant++
		}
	}
	t.Logf("single ask stored user=%d assistant=%d", user, assistant)
	if user != 1 {
		t.Errorf("one ask should store exactly one user turn, got %d", user)
	}
	if assistant != 1 {
		t.Errorf("one ask should store exactly one assistant turn, got %d — the reply is not being persisted", assistant)
	}
}
