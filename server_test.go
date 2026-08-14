package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
)

// serveTestApp returns an App that has never been through startup: no Service,
// no scheduler, no Wails context. Every method the tests call must be safe in
// that state — that is itself part of the contract, since an RPC can arrive
// before the backend finishes building.
func serveTestApp(t *testing.T) (*App, *eventHub, *httptest.Server) {
	t.Helper()
	hub := newEventHub()
	app := NewApp()
	app.emitFn = hub.broadcast
	mux := http.NewServeMux()
	mux.HandleFunc("/api/rpc/", func(w http.ResponseWriter, r *http.Request) { rpcCall(app, w, r) })
	mux.HandleFunc("/api/events", hub.serveSSE)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return app, hub, srv
}

func TestRPCDispatchesArgsAndResult(t *testing.T) {
	_, _, srv := serveTestApp(t)

	// CancelChat exercises one string arg in, one string out, and is safe with
	// no runs in flight.
	resp, err := http.Post(srv.URL+"/api/rpc/CancelChat", "application/json", strings.NewReader(`["nope"]`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "not running") {
		t.Fatalf("CancelChat(nope) = %q, want the not-running message", got)
	}
}

func TestRPCStructResult(t *testing.T) {
	app, _, srv := serveTestApp(t)
	app.settings = &backend.Settings{LLMModel: "test-model"}

	resp, err := http.Post(srv.URL+"/api/rpc/GetSettings", "application/json", strings.NewReader(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got backend.Settings
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.LLMModel != "test-model" {
		t.Fatalf("LLMModel = %q, want test-model", got.LLMModel)
	}
}

func TestRPCRejectsBadCalls(t *testing.T) {
	_, _, srv := serveTestApp(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"unknown method", http.MethodPost, "/api/rpc/NoSuchMethod", `[]`, http.StatusNotFound},
		{"denied dialog method", http.MethodPost, "/api/rpc/PickFiles", `[]`, http.StatusNotFound},
		{"unexported is invisible", http.MethodPost, "/api/rpc/emit", `["x",{}]`, http.StatusNotFound},
		{"wrong arity", http.MethodPost, "/api/rpc/CancelChat", `[]`, http.StatusBadRequest},
		{"not an array", http.MethodPost, "/api/rpc/CancelChat", `{"id":"x"}`, http.StatusBadRequest},
		{"GET refused", http.MethodGet, "/api/rpc/GetSettings", ``, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
}

func TestSSEDeliversBroadcasts(t *testing.T) {
	_, hub, srv := serveTestApp(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	r := bufio.NewReader(resp.Body)
	// First frame is the ": connected" comment.
	if line, err := r.ReadString('\n'); err != nil || !strings.HasPrefix(line, ":") {
		t.Fatalf("opening frame = %q, %v", line, err)
	}

	// The subscription exists as soon as the handler is running; broadcast
	// after the opening frame has been read so we know it is.
	hub.broadcast("chat:event", map[string]any{"type": "tool_call", "tool": "fetch_url"})

	deadline := time.After(3 * time.Second)
	got := make(chan string, 1)
	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: ") {
				got <- strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				return
			}
		}
	}()
	select {
	case data := <-got:
		var env struct {
			Name    string         `json:"name"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal([]byte(data), &env); err != nil {
			t.Fatalf("bad envelope %q: %v", data, err)
		}
		if env.Name != "chat:event" || env.Payload["tool"] != "fetch_url" {
			t.Fatalf("envelope = %+v", env)
		}
	case <-deadline:
		t.Fatal("no event arrived over SSE")
	}
}

func TestHubDropsSlowSubscriberWithoutBlocking(t *testing.T) {
	hub := newEventHub()
	ch, off := hub.subscribe()
	defer off()
	_ = ch // never read: the subscriber is maximally slow

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ { // more than the channel buffer
			hub.broadcast("chat:event", map[string]any{"i": i})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("broadcast blocked on a subscriber that never reads")
	}
}
