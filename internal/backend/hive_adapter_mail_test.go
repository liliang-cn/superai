package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A stand-in for Claude Code in stream-input mode: it reads the order, works
// for a moment, reads anything else it was sent meanwhile, and answers with
// all of it — which is what the real one was seen to do.
const fakeClaude = `#!/bin/bash
read -r first
sleep 1
rest=""
while read -r -t 1 more; do rest="$rest $more"; done
printf '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}\n'
ans=$(printf '%s %s' "$first" "$rest" | tr -d '"\\' | tr '\n' ' ')
printf '{"type":"result","result":"%s","is_error":false}\n' "$ans"
# The real one waits for more input until stdin closes.
while read -r x; do :; done
`

func TestClaudeStreamInputTakesAMessageMidTurn(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(bin, []byte(fakeClaude), 0o755)
	e := &ExecEngine{Argv: []string{bin}, Mode: "claude", StreamInput: true}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	var inject func(string) bool
	got := make(chan func(string) bool, 1)
	done := make(chan string, 1)
	go func() {
		out, err := e.RunSteerable(context.Background(), "the order", func(AdapterEvent) {}, func(f func(string) bool) { got <- f })
		if err != nil {
			t.Error(err)
		}
		done <- out
	}()
	inject = <-got
	if !inject("the password is BANANA") {
		t.Fatal("the running turn refused the message")
	}
	select {
	case out := <-done:
		if !strings.Contains(out, "the order") || !strings.Contains(out, "BANANA") {
			t.Fatalf("answer did not take the message in: %q", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never ended: stdin was not closed after the answer")
	}
	if inject("too late") {
		t.Fatal("a finished turn accepted a message")
	}
}

func TestStreamInputNeedsClaudeMode(t *testing.T) {
	if err := (&ExecEngine{Argv: []string{"x"}, StreamInput: true}).Validate(); err == nil {
		t.Fatal("stream input without mode claude was accepted")
	}
	if err := (&ExecEngine{Argv: []string{"x", "{prompt}"}, Mode: "claude", StreamInput: true}).Validate(); err == nil {
		t.Fatal("stream input with a {prompt} argument was accepted")
	}
}

// recordingEngine remembers what it was asked, and can hold a turn open.
type recordingEngine struct {
	mu       sync.Mutex
	prompts  []string
	injected []string
	hold     chan struct{}
	steer    bool
}

func (r *recordingEngine) Describe() string { return "test" }
func (r *recordingEngine) Steerable() bool  { return r.steer }
func (r *recordingEngine) Run(ctx context.Context, prompt string, emit func(AdapterEvent)) (string, error) {
	return r.RunSteerable(ctx, prompt, emit, nil)
}
func (r *recordingEngine) RunSteerable(ctx context.Context, prompt string, _ func(AdapterEvent), ready func(func(string) bool)) (string, error) {
	r.mu.Lock()
	r.prompts = append(r.prompts, prompt)
	r.mu.Unlock()
	if ready != nil {
		ready(func(s string) bool { r.mu.Lock(); r.injected = append(r.injected, s); r.mu.Unlock(); return true })
	}
	if r.hold != nil {
		<-r.hold
	}
	return "ok", nil
}
func (r *recordingEngine) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.prompts...), append([]string(nil), r.injected...)
}

func postMessage(t *testing.T, srv *httptest.Server, token string, m HiveMessage) int {
	t.Helper()
	b, _ := json.Marshal(m)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/hive/message", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAnIdleAgentIsWokenByAMessageAndNotByAReply(t *testing.T) {
	eng := &recordingEngine{}
	ad := &Adapter{Token: "tok", Engine: eng, Name: "claude-mac", Tell: true}
	srv := httptest.NewServer(ad.Handler())
	defer srv.Close()

	reply, _ := NewHiveMessage("queen", "claude-mac", "noted, thanks", "some-id")
	if c := postMessage(t, srv, "tok", reply); c != http.StatusOK {
		t.Fatalf("reply got %d", c)
	}
	m, _ := NewHiveMessage("queen", "claude-mac", "what is your hostname?", "")
	if c := postMessage(t, srv, "tok", m); c != http.StatusOK {
		t.Fatalf("message got %d", c)
	}
	waitFor(t, "the message started nothing", func() bool { p, _ := eng.snapshot(); return len(p) == 1 })
	p, _ := eng.snapshot()
	if !strings.Contains(p[0], "hostname") || !strings.Contains(p[0], "You are claude-mac") {
		t.Fatalf("turn prompt %q", p[0])
	}
	// The reply was not woken on, but it was taken along with the other
	// message from the same sender — both are unread mail from the queen.
	time.Sleep(200 * time.Millisecond)
	if p, _ := eng.snapshot(); len(p) != 1 {
		t.Fatalf("%d turns started", len(p))
	}

	if c := postMessage(t, srv, "wrong", m); c != http.StatusUnauthorized {
		t.Fatalf("a message without the bearer got %d", c)
	}
	other, _ := NewHiveMessage("queen", "somebody-else", "x", "")
	if c := postMessage(t, srv, "tok", other); c != http.StatusBadRequest {
		t.Fatalf("misrouted mail got %d", c)
	}
}

func TestAMessageGoesIntoTheRunningOrder(t *testing.T) {
	eng := &recordingEngine{hold: make(chan struct{}), steer: true}
	ad := &Adapter{Token: "tok", Engine: eng, Name: "claude-mac"}
	srv := httptest.NewServer(ad.Handler())
	defer srv.Close()

	ad.start(func() string { return "a long order" })
	waitFor(t, "the order never started", func() bool { p, _ := eng.snapshot(); return len(p) == 1 })
	waitFor(t, "the order never offered an injector", func() bool {
		ad.mu.Lock()
		defer ad.mu.Unlock()
		return len(ad.injectors) == 1
	})
	m, _ := NewHiveMessage("queen", "claude-mac", "stop after this step", "")
	postMessage(t, srv, "tok", m)
	_, inj := eng.snapshot()
	if len(inj) != 1 || !strings.Contains(inj[0], "While you work") || !strings.Contains(inj[0], "stop after this step") {
		t.Fatalf("injected %q", inj)
	}
	close(eng.hold)
	time.Sleep(200 * time.Millisecond)
	if p, _ := eng.snapshot(); len(p) != 1 {
		t.Fatalf("a steered message also started a turn: %d prompts", len(p))
	}
}

func TestTheHiveToolsOverMCP(t *testing.T) {
	// A queen that knows one other worker.
	var got []HiveMessage
	var mu sync.Mutex
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m HiveMessage
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		w.Write([]byte(`{"ok":true}`))
	}))
	defer peer.Close()
	queen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/hive/roster":
			json.NewEncoder(w).Encode([]RosterEntry{{Name: "claude-mac", URL: "x"}, {Name: "superai-worker-1", URL: peer.URL}})
		case "/api/hive/message":
			var m HiveMessage
			json.NewDecoder(r.Body).Decode(&m)
			mu.Lock()
			got = append(got, m)
			mu.Unlock()
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer queen.Close()

	ann := &Announcer{Settings: HiveSettings{Role: HiveRoleWorker, Name: "claude-mac", JoinURL: queen.URL}, Token: "tok"}
	ad := &Adapter{Token: "tok", Engine: &recordingEngine{}, Name: "claude-mac", Announcer: ann}
	srv := httptest.NewServer(ad.Handler())
	defer srv.Close()

	ctx := context.Background()
	cl := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	sess, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{"tok"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	call := func(name string, args map[string]any) string {
		r, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return r.Content[0].(*mcp.TextContent).Text
	}

	if out := call("hive_peers", map[string]any{}); !strings.Contains(out, "You are claude-mac") || !strings.Contains(out, "superai-worker-1") {
		t.Fatalf("peers: %q", out)
	}
	if out := call("hive_send", map[string]any{"to": "superai-worker-1", "text": "the config is in /tmp/x"}); !strings.Contains(out, "delivered") {
		t.Fatalf("send: %q", out)
	}
	// The peer got it, and the queen her observed copy.
	waitFor(t, "the queen did not get her copy", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 2 })
	mu.Lock()
	observed := got[0].Observed || got[1].Observed
	mu.Unlock()
	if !observed {
		t.Fatal("no observed copy reached the queen")
	}

	m, _ := NewHiveMessage("queen", "claude-mac", "a note", "x")
	ad.mail.Put(m)
	if out := call("hive_inbox", map[string]any{}); !strings.Contains(out, "a note") {
		t.Fatalf("inbox: %q", out)
	}
	if out := call("hive_inbox", map[string]any{}); !strings.Contains(out, "No messages") {
		t.Fatalf("read mail came back unread: %q", out)
	}

	// Without the bearer there is no MCP either.
	resp, _ := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("MCP without the bearer: %d", resp.StatusCode)
	}
}

type bearer struct{ tok string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return http.DefaultTransport.RoundTrip(r)
}
