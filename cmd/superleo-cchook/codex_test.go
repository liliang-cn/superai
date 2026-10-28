package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// codex's hooks (hooks.json, Claude Code's shape) come in with --cli codex:
// every event of a turn, by name, and nothing of what it said or ran.
func TestCodexHooksReachTheNodeAsCodex(t *testing.T) {
	sock, got := node(t, true)
	t.Setenv("SUPERLEO_HOOK_SOCKET", sock)
	t.Setenv("SUPERLEO_CLI_RUN", "")
	t.Setenv("TMUX_PANE", "")
	in := []struct{ event, payload string }{
		{"SessionStart", `{"session_id":"cx","transcript_path":null,"cwd":"/Users/me/code/noted","hook_event_name":"SessionStart","model":"gpt","source":"startup"}`},
		{"UserPromptSubmit", `{"session_id":"cx","turn_id":"t1","cwd":"/Users/me/code/noted","hook_event_name":"UserPromptSubmit","prompt":"PROMPT-SECRET"}`},
		{"PreToolUse", `{"session_id":"cx","turn_id":"t1","cwd":"/Users/me/code/noted","hook_event_name":"PreToolUse","tool_name":"exec_command","tool_input":{"cmd":"echo ARG-SECRET"}}`},
		{"PermissionRequest", `{"session_id":"cx","turn_id":"t1","cwd":"/Users/me/code/noted","hook_event_name":"PermissionRequest","tool_name":"apply_patch","tool_input":{"patch":"ARG-SECRET"}}`},
		{"Interrupt", `{"session_id":"cx","turn_id":"t1","cwd":"/Users/me/code/noted","hook_event_name":"Interrupt"}`},
		{"PreCompact", `{"session_id":"cx","cwd":"/Users/me/code/noted"}`}, // not one that moves a session
	}
	for _, c := range in {
		runWith(t, []string{"--cli", "codex", c.event}, c.payload)
	}
	runWith(t, []string{"--cli", "claude", "Stop"}, `{"session_id":"cc"}`) // claude has its own hooks
	for _, want := range []event{
		{CLI: "codex", Event: "SessionStart", Session: "cx", Cwd: "noted"},
		{CLI: "codex", Event: "UserPromptSubmit", Session: "cx", Cwd: "noted"},
		{CLI: "codex", Event: "PreToolUse", Session: "cx", Cwd: "noted", Tool: "exec_command"},
		{CLI: "codex", Event: "PermissionRequest", Session: "cx", Cwd: "noted", Tool: "apply_patch"},
		{CLI: "codex", Event: "Stop", Session: "cx", Cwd: "noted"},
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
			t.Fatalf("the node never heard %+v", want)
		}
	}
	select {
	case b := <-got:
		t.Fatalf("the node heard more: %s", b)
	case <-time.After(200 * time.Millisecond):
	}
}
