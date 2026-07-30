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

// Two questions at once, end to end through App.
//
// The unit tests cover the tagging contract with a stub; this drives the real
// SendChat against a provider that answers slowly, so the two asks genuinely
// overlap, and checks what a UI would actually receive: two ids, two streams
// that never carry each other's text, one terminal event each, and progress
// events for both while they run.

type overlappingProvider struct {
	delay time.Duration
}

func (p *overlappingProvider) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		tag := "NONE"
		for _, candidate := range []string{"ALPHA", "BRAVO"} {
			if strings.Contains(string(body), candidate) {
				tag = candidate
				break
			}
		}
		time.Sleep(p.delay)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": "REPLY-" + tag + "\n情绪: 中性"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

// captured is one event as the frontend would see it.
type captured struct {
	name    string
	payload map[string]any
}

func (c captured) reqID() string {
	id, _ := c.payload["requestId"].(string)
	return id
}

func (c captured) str(key string) string {
	v, _ := c.payload[key].(string)
	return v
}

func TestTwoAsksAtOnceStaySeparate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	t.Setenv("SUPERAI_NO_BROWSER", "1")

	provider := &overlappingProvider{delay: 200 * time.Millisecond}
	app := NewApp()
	app.settings = &backend.Settings{
		LLMBaseURL:   provider.start(t),
		LLMKey:       "k",
		LLMModel:     "test-model",
		WorkspaceDir: home + "/workspace",
		MaxRounds:    3,
		Headless:     true,
		DisablePTC:   true,
	}
	app.rebuild()
	if app.svc == nil {
		t.Fatalf("backend did not build: %s", app.buildErr)
	}
	t.Cleanup(func() { _ = app.svc.Close() })

	var mu sync.Mutex
	var events []captured
	app.emitFn = func(name string, payload map[string]any) {
		mu.Lock()
		events = append(events, captured{name: name, payload: payload})
		mu.Unlock()
	}

	// Both sent before either can finish — the situation the UI could not
	// previously represent.
	idA := app.SendChat("one-conversation", "请回答 ALPHA", nil)
	idB := app.SendChat("one-conversation", "请回答 BRAVO", nil)

	if idA == "" || idB == "" || idA == idB {
		t.Fatalf("want two distinct non-empty ids, got %q and %q", idA, idB)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		done := map[string]bool{}
		for _, e := range events {
			if e.name == "chat:done" || e.name == "chat:error" {
				done[e.reqID()] = true
			}
		}
		mu.Unlock()
		if done[idA] && done[idB] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("both asks should have finished")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	byID := map[string][]captured{}
	types := map[string]map[string]int{}
	for _, e := range events {
		id := e.reqID()
		if id == "" {
			t.Errorf("event %s carried no requestId: %v", e.name, e.payload)
			continue
		}
		if id != idA && id != idB {
			t.Errorf("event %s carried an unknown requestId %q", e.name, id)
			continue
		}
		byID[id] = append(byID[id], e)
		if e.name == "chat:event" {
			if types[id] == nil {
				types[id] = map[string]int{}
			}
			types[id][e.str("type")]++
		}
	}

	for id, want := range map[string]string{idA: "ALPHA", idB: "BRAVO"} {
		other := "BRAVO"
		if want == "BRAVO" {
			other = "ALPHA"
		}

		var final string
		terminals := 0
		for _, e := range byID[id] {
			switch e.name {
			case "chat:done":
				terminals++
				final = e.str("final")
			case "chat:error":
				terminals++
				t.Errorf("%s failed: %s", want, e.str("error"))
			}
			// No event of one ask may carry the other's answer.
			if body := e.str("content") + e.str("final"); strings.Contains(body, "REPLY-"+other) {
				t.Errorf("%s received %s's text: %q", want, other, body)
			}
		}
		if terminals != 1 {
			t.Errorf("%s had %d terminal events, want exactly 1", want, terminals)
		}
		if !strings.Contains(final, "REPLY-"+want) {
			t.Errorf("%s final = %q, want its own reply", want, final)
		}
		// The progress block needs something to show while an ask runs.
		if types[id]["thinking"]+types[id]["state_update"] == 0 {
			t.Errorf("%s produced no thinking/state_update events, so the UI has no progress to render", want)
		}
		t.Logf("%s (%s): %d events, types=%v", want, id[:8], len(byID[id]), types[id])
	}
}
