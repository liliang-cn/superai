package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRing stands in for a SuperAI: /api/events streams what the test pushes,
// /api/rpc/SendChat records the command and replies with a request id.
type fakeRing struct {
	srv       *httptest.Server
	mu        sync.Mutex
	events    chan string
	sent      []string
	cancelled []string
	token     string
	onSend    func(session, prompt string, r *fakeRing) string
}

func newFakeRing(t *testing.T, token string) *fakeRing {
	f := &fakeRing{events: make(chan string, 16), token: token}
	mux := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if f.token != "" && r.Header.Get("Authorization") != "Bearer "+f.token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/events", guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": connected\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case e := <-f.events:
				fmt.Fprintf(w, "data: %s\n\n", e)
				w.(http.Flusher).Flush()
			}
		}
	}))
	mux.HandleFunc("/api/rpc/SendChat", guard(func(w http.ResponseWriter, r *http.Request) {
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		f.mu.Lock()
		f.sent = append(f.sent, args[1].(string))
		f.mu.Unlock()
		id := "req-1"
		if f.onSend != nil {
			id = f.onSend(args[0].(string), args[1].(string), f)
		}
		_ = json.NewEncoder(w).Encode(id)
	}))
	mux.HandleFunc("/api/rpc/CancelChat", guard(func(w http.ResponseWriter, r *http.Request) {
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		f.mu.Lock()
		f.cancelled = append(f.cancelled, args[0].(string))
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode("ok")
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRing) emit(name string, payload map[string]any) {
	b, _ := json.Marshal(map[string]any{"name": name, "payload": payload})
	f.events <- string(b)
}

func TestARingAnswersACommand(t *testing.T) {
	f := newFakeRing(t, "tok")
	f.onSend = func(_, _ string, r *fakeRing) string {
		go func() {
			// Another conversation's end must not be mistaken for ours, and
			// it arrives first on purpose.
			r.emit("chat:done", map[string]any{"requestId": "someone-else", "final": "not for you"})
			r.emit("chat:done", map[string]any{"requestId": "req-1", "final": " deployed "})
		}()
		return "req-1"
	}
	res := askRing(context.Background(), ringTarget{name: "ring-1", url: f.srv.URL, token: "tok"}, "deploy it")
	if res.Failed || res.Text != "deployed" {
		t.Fatalf("got %+v", res)
	}
	if len(f.sent) != 1 || f.sent[0] != "deploy it" {
		t.Fatalf("the ring was sent %q", f.sent)
	}
}

func TestAnAnswerThatBeatsTheResponseIsNotLost(t *testing.T) {
	// The terminal event can be emitted before SendChat has even returned its
	// id. The stream is open first, so it is buffered rather than missed.
	f := newFakeRing(t, "")
	f.onSend = func(_, _ string, r *fakeRing) string {
		r.emit("chat:done", map[string]any{"requestId": "req-1", "final": "fast"})
		time.Sleep(150 * time.Millisecond)
		return "req-1"
	}
	res := askRing(context.Background(), ringTarget{name: "r", url: f.srv.URL}, "x")
	if res.Failed || res.Text != "fast" {
		t.Fatalf("got %+v", res)
	}
}

func TestARingErrorIsAFailureNotAnAnswer(t *testing.T) {
	f := newFakeRing(t, "")
	f.onSend = func(_, _ string, r *fakeRing) string {
		go r.emit("chat:error", map[string]any{"requestId": "req-1", "error": "model unavailable"})
		return "req-1"
	}
	res := askRing(context.Background(), ringTarget{name: "r", url: f.srv.URL}, "x")
	if !res.Failed || !strings.Contains(res.Reason, "model unavailable") {
		t.Fatalf("got %+v", res)
	}
}

func TestAWrongTokenIsRefusedAndSaidSo(t *testing.T) {
	f := newFakeRing(t, "right")
	res := askRing(context.Background(), ringTarget{name: "r", url: f.srv.URL, token: "wrong"}, "x")
	if !res.Failed || !strings.Contains(res.Reason, "401") {
		t.Fatalf("got %+v", res)
	}
	if len(f.sent) != 0 {
		t.Fatal("a command reached a ring that refused the token")
	}
}

func TestStoppingTheCommanderStopsTheRing(t *testing.T) {
	f := newFakeRing(t, "")
	f.onSend = func(_, _ string, _ *fakeRing) string { return "req-9" } // never finishes
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	res := askRing(ctx, ringTarget{name: "r", url: f.srv.URL}, "long job")
	if !res.Failed {
		t.Fatalf("got %+v", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cancelled) != 1 || f.cancelled[0] != "req-9" {
		t.Fatalf("the ring was not told to stop: %v", f.cancelled)
	}
}

func TestAnUnreachableRingFailsWithAReason(t *testing.T) {
	res := askRing(context.Background(), ringTarget{name: "r", url: "http://127.0.0.1:1"}, "x")
	if !res.Failed || res.Reason == "" {
		t.Fatalf("got %+v", res)
	}
}

func TestARingEntryNeedsNoHostsOrCommand(t *testing.T) {
	r := RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{
		"ring-1": {URL: "http://x:1", Token: "t"},
		"broken": {},
	}}
	r.normalize()
	if !r.Has("ring-1") {
		t.Fatal("a ring was dropped for having no ssh hosts")
	}
	if r.Has("broken") {
		t.Fatal("an entry with nothing to call was kept")
	}
}

func TestRunRoutesAURLEntryToTheRing(t *testing.T) {
	f := newFakeRing(t, "tok")
	f.onSend = func(_, _ string, r *fakeRing) string {
		go r.emit("chat:done", map[string]any{"requestId": "req-1", "final": "ok"})
		return "req-1"
	}
	run := NewRemoteRunner(RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{
		"ring-1": {URL: f.srv.URL, Token: "tok"},
	}})
	res, err := run.Run(context.Background(), "ring-1", "go")
	if err != nil || res.Failed || res.Text != "ok" {
		t.Fatalf("got %+v, %v", res, err)
	}
}
