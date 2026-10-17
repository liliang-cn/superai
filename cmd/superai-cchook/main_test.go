package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseKeepsOnlyWhatTheNodeNeeds(t *testing.T) {
	cases := []struct {
		name, event, in string
		want            *event
	}{
		{"a tool call", "PreToolUse", `{"session_id":"s1","cwd":"/Users/me/code/superai/","tool_name":"Bash","tool_input":{"command":"cat ARG-SECRET"}}`,
			&event{Event: "PreToolUse", Session: "s1", Cwd: "superai", Tool: "Bash"}},
		{"its end", "PostToolUse", `{"session_id":"s1","cwd":"/w/x","tool_name":"Edit","tool_input":{"new_string":"ARG-SECRET"},"tool_response":{"ok":"OUT-SECRET"}}`,
			&event{Event: "PostToolUse", Session: "s1", Cwd: "x", Tool: "Edit"}},
		{"a prompt", "UserPromptSubmit", `{"session_id":"s1","cwd":"/w/x","prompt":"PROMPT-SECRET"}`,
			&event{Event: "UserPromptSubmit", Session: "s1", Cwd: "x"}},
		{"named by the payload", "", `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Read"}`,
			&event{Event: "PreToolUse", Session: "s1", Tool: "Read"}},
		{"not one of ours", "Stop", `{"session_id":"s1"}`, nil},
		{"no session", "PreToolUse", `{"tool_name":"Bash"}`, nil},
		{"not JSON", "PreToolUse", `{"session_id":`, nil},
		{"empty", "PreToolUse", ``, nil},
	}
	for _, c := range cases {
		got, ok := parse(c.event, []byte(c.in))
		if c.want == nil {
			if ok {
				t.Errorf("%s: sent %+v", c.name, got)
			}
			continue
		}
		if !ok || got != *c.want {
			t.Errorf("%s: got %+v (%v), want %+v", c.name, got, ok, *c.want)
		}
	}
}

// node is a stand-in for the SuperAI on this machine: it answers like
// handleCCHook and keeps the bodies it got.
func node(t *testing.T, answer bool) (string, chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, socketName)
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lis.Close() })
	got := make(chan string, 16)
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil {
					return
				}
				b, _ := io.ReadAll(req.Body)
				if req.Method != http.MethodPost || req.URL.Path != hookPath || req.Header.Get("Content-Type") != "application/json" {
					b = []byte("bad request: " + req.Method + " " + req.URL.Path)
				}
				got <- string(b)
				if !answer {
					time.Sleep(2 * time.Second) // a node that has hung
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 12\r\n\r\n{\"reply\":\"\"}")
			}(c)
		}
	}()
	return sock, got
}

func runWith(t *testing.T, args []string, in string) time.Duration {
	t.Helper()
	start := time.Now()
	run(args, strings.NewReader(in))
	return time.Since(start)
}

func TestTheNodeHearsTheToolAndNothingElse(t *testing.T) {
	sock, got := node(t, true)
	t.Setenv("SUPERAI_HOOK_SOCKET", sock)
	t.Setenv("SUPERAI_CLI_RUN", "")
	t.Setenv("TMUX_PANE", "%3")
	runWith(t, []string{"PreToolUse"}, `{"session_id":"s1","cwd":"/Users/me/code/superai","tool_name":"Bash","tool_input":{"command":"echo ARG-SECRET"}}`)
	runWith(t, []string{"UserPromptSubmit"}, `{"session_id":"s1","cwd":"/Users/me/code/superai","prompt":"PROMPT-SECRET"}`)
	for _, want := range []event{
		{Event: "PreToolUse", Session: "s1", Cwd: "superai", Tool: "Bash", Pane: "%3"},
		{Event: "UserPromptSubmit", Session: "s1", Cwd: "superai", Pane: "%3"},
	} {
		select {
		case b := <-got:
			var ev event
			if err := json.Unmarshal([]byte(b), &ev); err != nil || ev != want {
				t.Fatalf("node got %s, want %+v", b, want)
			}
			for _, s := range []string{"SECRET", "/Users/me", "tool_input", "prompt"} {
				if strings.Contains(b, s) {
					t.Fatalf("%q reached the node: %s", s, b)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the node heard nothing")
		}
	}
}

func TestItFailsOpenAndFast(t *testing.T) {
	t.Setenv("SUPERAI_CLI_RUN", "")
	t.Setenv("SUPERAI_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	payload := `{"session_id":"s1","tool_name":"Bash"}`

	// No SuperAI at all, and a socket file nobody listens on.
	stale := filepath.Join(t.TempDir(), socketName)
	_ = os.WriteFile(stale, nil, 0o600)
	t.Setenv("SUPERAI_HOOK_SOCKET", stale)
	if d := runWith(t, []string{"PreToolUse"}, payload); d > 100*time.Millisecond {
		t.Errorf("with no SuperAI it took %s", d)
	}
	// A node that takes the event and hangs: given up within the budget.
	sock, got := node(t, false)
	t.Setenv("SUPERAI_HOOK_SOCKET", sock)
	if d := runWith(t, []string{"PreToolUse"}, payload); d > budget+150*time.Millisecond {
		t.Errorf("a hung node held the hook for %s", d)
	}
	<-got
	// Garbage in: nothing sent.
	runWith(t, []string{"PreToolUse"}, "not json")
	// A run SuperAI started itself: nothing sent.
	t.Setenv("SUPERAI_CLI_RUN", "run-1")
	runWith(t, []string{"PreToolUse"}, payload)
	select {
	case b := <-got:
		t.Fatalf("sent %s", b)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSocketsAreTriedInTheNodesOrder(t *testing.T) {
	t.Setenv("SUPERAI_HOOK_SOCKET", "/x/explicit.sock")
	t.Setenv("SUPERAI_HOME", "/y/home")
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := sockets()
	if len(s) < 4 || s[0] != "/x/explicit.sock" || s[1] != "/y/home/"+socketName ||
		s[len(s)-2] != filepath.Join(home, ".superai-node", socketName) || s[len(s)-1] != filepath.Join(home, ".superai", socketName) {
		t.Fatalf("sockets %v", s)
	}
}
