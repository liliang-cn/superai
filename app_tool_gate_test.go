package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
)

// The gate, end to end, against the service the app actually builds.
//
// Everything else about approvals is tested against the pieces: the gate's own
// decisions, the App's routing, the RPC bridge. None of that proves the one
// claim that matters — that a `bash` call the model made really does stop, in
// the real NewService wiring, and that the shell command does not run. That is
// exactly the bug being fixed here: the authorization seam existed, was wired
// to nothing, and every test of every piece around it still passed.

// startToolCallingLLM is an OpenAI-compatible endpoint that asks for one bash
// call and then, whatever came back, finishes. Two rounds is enough: the point
// is what happens between them.
func startToolCallingLLM(t *testing.T, command string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")

		// The tool result is echoed back into the history on the second call,
		// which is how this tells the rounds apart without keeping state.
		if strings.Contains(string(body), `"role":"tool"`) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "chatcmpl", "object": "chat.completion", "created": 1, "model": "test-model",
				"choices": []map[string]any{{
					"index":         0,
					"message":       map[string]string{"role": "assistant", "content": "done\n情绪: 中性"},
					"finish_reason": "stop",
				}},
			})
			return
		}
		args, _ := json.Marshal(map[string]string{"command": command})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []map[string]any{{
						"id": "call-1", "type": "function",
						"function": map[string]string{"name": "bash", "arguments": string(args)},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

// gateTestApp builds the real App — real backend.NewService, real gate — on a
// model that always reaches for bash.
func gateTestApp(t *testing.T, command string, disable bool) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	t.Setenv("SUPERAI_NO_BROWSER", "1")

	a := NewApp()
	a.emitFn = func(string, map[string]any) {}
	a.settings = &backend.Settings{
		LLMBaseURL:          startToolCallingLLM(t, command),
		LLMKey:              "test-key",
		LLMModel:            "test-model",
		WorkspaceDir:        filepath.Join(home, "workspace"),
		MaxRounds:           3,
		Headless:            true,
		DisablePTC:          true,
		DisableSelfInstall:  true,
		DisableToolApproval: disable,
	}
	a.rebuild()
	if a.svc == nil {
		t.Fatalf("rebuild did not produce a Service: %s", a.buildErr)
	}
	t.Cleanup(func() { _ = a.svc.Close() })
	return a, home
}

// waitForApproval blocks until exactly one prompt is waiting, and returns it.
func waitForApproval(t *testing.T, a *App) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if p := a.PendingToolApprovals(); len(p) == 1 {
			return p[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the agent called bash and nothing was ever asked; the gate is not installed on the real service")
	return nil
}

func waitForFinish(t *testing.T, a *App, id string) {
	t.Helper()
	// The run registry is the only thing that knows a turn is over: SendChat
	// returns as soon as the goroutine is started, and unregisters itself last.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		a.runMu.Lock()
		_, running := a.runs[id]
		a.runMu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the turn never finished")
}

func TestDeniedBashNeverRuns(t *testing.T) {
	a, home := gateTestApp(t, "touch pwned.txt", false)

	id := a.SendChat("gate-deny", "clean up my machine", nil)
	if id == "" {
		t.Fatal("SendChat returned no request id")
	}

	ask := waitForApproval(t, a)
	// The prompt has to name the tool and show the command, or the user is
	// approving a shape rather than an action.
	if ask["tool"] != "bash" || ask["command"] != "touch pwned.txt" {
		t.Fatalf("approval prompt = %v, want the bash call with its command", ask)
	}

	if got := a.ResolveToolApproval(ask["id"].(string), false); got != "ok" {
		t.Fatalf("ResolveToolApproval = %q", got)
	}
	waitForFinish(t, a, id)

	if _, err := os.Stat(filepath.Join(home, "workspace", "pwned.txt")); err == nil {
		t.Fatal("the command ran despite being denied")
	}

	entries := a.ToolApprovalInfo(10)["entries"].([]backend.AuditEntry)
	if len(entries) == 0 {
		t.Fatal("nothing was written to the audit log")
	}
	last := entries[len(entries)-1]
	if last.Tool != "bash" || last.Allowed || last.Summary != "touch pwned.txt" {
		t.Errorf("audit entry = %+v, want a denied bash carrying the command", last)
	}
}

func TestApprovedBashRuns(t *testing.T) {
	a, home := gateTestApp(t, "touch approved.txt", false)

	id := a.SendChat("gate-allow", "make a file", nil)
	ask := waitForApproval(t, a)
	if got := a.ResolveToolApproval(ask["id"].(string), true); got != "ok" {
		t.Fatalf("ResolveToolApproval = %q", got)
	}
	waitForFinish(t, a, id)

	// The other half of the contract: an approved call must actually happen.
	// A gate that blocks everything would pass the test above and be useless.
	if _, err := os.Stat(filepath.Join(home, "workspace", "approved.txt")); err != nil {
		t.Fatalf("an approved command did not run: %v", err)
	}
}

// Turning the gate off is meant to be a deliberate choice with consequences,
// not a broken switch. It must let the command through and still record it.
func TestDisabledGateRunsTheCommandAndStillAudits(t *testing.T) {
	a, home := gateTestApp(t, "touch ungated.txt", true)

	id := a.SendChat("gate-off", "make a file", nil)
	waitForFinish(t, a, id)

	if len(a.PendingToolApprovals()) != 0 {
		t.Error("the gate is off and it still asked")
	}
	if _, err := os.Stat(filepath.Join(home, "workspace", "ungated.txt")); err != nil {
		t.Fatalf("the command did not run with the gate off: %v", err)
	}
	entries := a.ToolApprovalInfo(10)["entries"].([]backend.AuditEntry)
	if len(entries) == 0 {
		t.Fatal("with the gate off nothing was recorded; the log is the only trace left")
	}
	if entries[len(entries)-1].DecidedBy != backend.DecidedByGateOff {
		t.Errorf("decided_by = %q, want %q", entries[len(entries)-1].DecidedBy, backend.DecidedByGateOff)
	}
}
