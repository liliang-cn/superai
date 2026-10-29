package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// A shell command a turn started does not outlive the turn: the person pressing
// Stop (or any other end of the turn's context) takes the command and every
// process under it away.
func TestTurnCancelKillsTheShellCommandAndItsChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are unix")
	}
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	t.Setenv("SUPERLEO_NO_BROWSER", "1")
	pidfile := filepath.Join(home, "child.pid")
	command := fmt.Sprintf("sleep 60 & echo $! > %s; sleep 60", pidfile)

	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		if !strings.Contains(string(body), `"tools"`) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			return
		}
		asked.Add(1)
		args, _ := json.Marshal(map[string]any{"command": command, "timeout_seconds": 300})
		chunk, _ := json.Marshal(map[string]any{"id": "c", "object": "chat.completion.chunk", "choices": []map[string]any{{
			"index": 0, "finish_reason": nil,
			"delta": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "bash", "arguments": string(args)},
			}}},
		}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	svc, err := NewService(&Settings{
		LLMBaseURL: srv.URL + "/v1", LLMKey: "k", LLMModel: "test-model",
		WorkspaceDir: home + "/workspace", MaxRounds: 3, Headless: true, DisablePTC: true,
		DisableToolApproval: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var sawCall atomic.Bool
	go func() {
		_, err := svc.StreamTurn(ctx, "shell-cancel", "run it", nil, func(ev *agent.Event) {
			if ev.Type == agent.EventTypeToolCall && ev.ToolName == "bash" {
				sawCall.Store(true)
			}
		})
		done <- err
	}()

	var child int
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && child == 0 {
		if b, err := os.ReadFile(pidfile); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if child == 0 {
		t.Fatalf("the command never started (model asked %d times, saw call %v)", asked.Load(), sawCall.Load())
	}
	if syscall.Kill(child, 0) != nil {
		t.Fatal("the child is not running before the cancel")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not end after the cancel")
	}
	// The turn is over; a couple of seconds later nothing of the command is left.
	gone := false
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(child, 0) == syscall.ESRCH {
			gone = true
			break
		}
	}
	if !gone {
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatalf("pid %d, a child of the turn's shell command, outlived the turn", child)
	}
}
