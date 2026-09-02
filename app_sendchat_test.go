package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
)

// chatEvent is one payload SendChat handed to the frontend.
type chatEvent struct {
	name    string
	payload map[string]any
}

func (e chatEvent) requestID() string {
	id, _ := e.payload["requestId"].(string)
	return id
}

func (e chatEvent) kind() string {
	k, _ := e.payload["type"].(string)
	return k
}

func (e chatEvent) text() string {
	var sb strings.Builder
	for _, key := range []string{"content", "final", "result", "error"} {
		if v, ok := e.payload[key].(string); ok {
			sb.WriteString(v)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// eventLog collects the event stream in place of the Wails runtime.
type eventLog struct {
	mu     sync.Mutex
	events []chatEvent
}

func (l *eventLog) record(name string, payload map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, chatEvent{name: name, payload: payload})
}

func (l *eventLog) snapshot() []chatEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]chatEvent, len(l.events))
	copy(out, l.events)
	return out
}

// waitForTerminals blocks until n asks have finished (chat:done or chat:error).
func (l *eventLog) waitForTerminals(t *testing.T, n int, timeout time.Duration) []chatEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := l.snapshot()
		var terminals []chatEvent
		for _, e := range events {
			if e.name == "chat:done" || e.name == "chat:error" {
				terminals = append(terminals, e)
			}
		}
		if len(terminals) >= n {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("only %d of %d asks finished within %s", len(l.snapshot()), n, timeout)
	return nil
}

// markerLLM is an OpenAI-compatible endpoint that echoes back a tag taken from
// the question, so a reply can be traced to the ask it belongs to. The delay is
// what makes two SendChat calls genuinely overlap.
func startMarkerLLM(t *testing.T, delay time.Duration) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		marker := "UNKNOWN"
		// The newest question is the last one in the body, so scan from the end:
		// the second ask carries the first one's text in its history.
		if i, j := strings.LastIndex(string(body), "ALPHA"), strings.LastIndex(string(body), "BRAVO"); i >= 0 || j >= 0 {
			if j > i {
				marker = "BRAVO"
			} else {
				marker = "ALPHA"
			}
		}
		time.Sleep(delay)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": "ANSWER-" + marker + "\nMOOD: neutral",
				},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

// newChatApp builds a real App on a fake provider, with its event stream
// captured instead of emitted.
func newChatApp(t *testing.T, delay time.Duration) (*App, *eventLog) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	// Chrome costs ~1s per NewService and no test here browses.
	t.Setenv("SUPERAI_NO_BROWSER", "1")

	log := &eventLog{}
	a := NewApp()
	a.emitFn = log.record
	a.settings = &backend.Settings{
		LLMBaseURL:   startMarkerLLM(t, delay),
		LLMKey:       "test-key",
		LLMModel:     "test-model",
		WorkspaceDir: home + "/workspace",
		MaxRounds:    3,
		Headless:     true,
		DisablePTC:   true,
	}
	a.rebuild()
	if a.svc == nil {
		t.Fatalf("rebuild did not produce a Service: %s", a.buildErr)
	}
	t.Cleanup(func() { _ = a.svc.Close() })
	return a, log
}

// TestSendChatTagsEventsPerRequest is the contract the frontend is written
// against: two asks in flight on one conversation, each stream identifiable.
func TestSendChatTagsEventsPerRequest(t *testing.T) {
	a, log := newChatApp(t, 150*time.Millisecond)

	// SendChat is non-blocking, so the second ask starts while the first is
	// still waiting on the provider — the situation that used to merge both
	// streams into one bubble.
	first := a.SendChat("shared-session", "请回答 ALPHA", nil)
	second := a.SendChat("shared-session", "请回答 BRAVO", nil)

	if first == "" || second == "" {
		t.Fatalf("SendChat must return a request id, got %q and %q", first, second)
	}
	if first == second {
		t.Fatalf("both asks were given the same request id %q", first)
	}

	events := log.waitForTerminals(t, 2, 30*time.Second)

	markers := map[string]string{first: "ALPHA", second: "BRAVO"}
	kinds := map[string]map[string]bool{first: {}, second: {}}
	terminals := map[string]string{}

	for _, e := range events {
		id := e.requestID()
		if id == "" {
			t.Errorf("%s event carries no requestId: %v", e.name, e.payload)
			continue
		}
		mine, known := markers[id]
		if !known {
			t.Errorf("%s event carries an unknown requestId %q", e.name, id)
			continue
		}
		kinds[id][e.kind()] = true

		// A reply tagged with one ask's id must not be the other ask's answer.
		for other, tag := range markers {
			if other != id && strings.Contains(e.text(), "ANSWER-"+tag) {
				t.Errorf("%s event for %s (%s) carried %s's answer: %s",
					e.name, id, mine, tag, strings.TrimSpace(e.text()))
			}
		}
		if e.name == "chat:done" || e.name == "chat:error" {
			if prev, dup := terminals[id]; dup {
				t.Errorf("%s finished twice (%s then %s)", id, prev, e.name)
			}
			terminals[id] = e.name
		}
	}

	// The other half of the crossing check: each id must have received its own
	// answer, which is also what proves the two ids name two real streams.
	for _, e := range events {
		if e.name != "chat:done" {
			continue
		}
		want := "ANSWER-" + markers[e.requestID()]
		if final, _ := e.payload["final"].(string); !strings.Contains(final, want) {
			t.Errorf("chat:done for %s = %q, want %s", e.requestID(), strings.TrimSpace(final), want)
		}
	}

	for id, marker := range markers {
		if terminals[id] != "chat:done" {
			t.Errorf("%s (%s) ended with %q, want chat:done", id, marker, terminals[id])
		}
	}

	// Both asks must have produced progress the UI can render while PTC works;
	// without these the bubble sits empty until the final answer lands.
	for id, marker := range markers {
		t.Logf("%s (%s) event types: %v", id, marker, sortedKeys(kinds[id]))
		if !kinds[id]["thinking"] && !kinds[id]["state_update"] {
			t.Errorf("%s (%s) got no thinking/state_update progress events", id, marker)
		}
	}
}

// TestSendChatWithoutBackendReturnsNoID pins the early-failure half of the
// contract: no id to stream against, but a tagged error so the pending bubble
// can still be resolved.
func TestSendChatWithoutBackendStillIdentifiesTheAsk(t *testing.T) {
	log := &eventLog{}
	a := NewApp()
	a.emitFn = log.record
	a.buildErr = "no account yet"

	id := a.SendChat("s", "hi", nil)
	if id == "" {
		t.Fatal("SendChat must return its id even when it starts nothing, or the caller cannot bind the failure event to the ask")
	}

	events := log.snapshot()
	if len(events) != 1 || events[0].name != "chat:error" {
		t.Fatalf("want exactly one chat:error, got %v", events)
	}
	if got := events[0].requestID(); got != id {
		t.Errorf("failure event requestId = %q, want the id SendChat returned (%q)", got, id)
	}
	if msg, _ := events[0].payload["error"].(string); !strings.Contains(msg, "no account yet") {
		t.Errorf("error payload = %q, want the build error", msg)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
