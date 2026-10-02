package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superai/internal/backend"
)

func TestAClaudeToolCallAndItsResultReadAsOneStep(t *testing.T) {
	calls := map[string]string{}
	call := normaliseCLIEvent(agentexec.Event{Type: agentexec.EventToolCall, Payload: map[string]any{
		"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "date +%s", "description": "now"},
	}}, calls)
	if len(call) != 1 || call[0].Kind != "tool" || call[0].Tool != "Bash" || call[0].Detail != "date +%s" || call[0].CallID != "toolu_1" {
		t.Fatalf("call %+v", call)
	}
	res := normaliseCLIEvent(agentexec.Event{Type: agentexec.EventToolResult, Payload: map[string]any{
		"type": "user", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "1790914784", "is_error": false},
		}},
	}}, calls)
	if len(res) != 1 || res[0].Kind != "result" || res[0].Tool != "Bash" || res[0].Text != "1790914784" || res[0].Failed {
		t.Fatalf("result %+v", res)
	}
}

func TestACodexCommandArrivesWithItsOutput(t *testing.T) {
	evs := normaliseCLIEvent(agentexec.Event{Type: agentexec.EventToolCall, Payload: map[string]any{
		"type": "command_execution", "id": "item_3", "command": "/bin/zsh -lc 'ls'",
		"aggregated_output": "hello.txt\n", "exit_code": float64(2), "status": "failed",
	}}, map[string]string{})
	if len(evs) != 2 || evs[0].Kind != "tool" || evs[0].Tool != "shell" || evs[1].Kind != "result" ||
		evs[1].Text != "hello.txt\n" || !evs[1].Failed || evs[1].Tool != "shell" {
		t.Fatalf("events %+v", evs)
	}
}

func TestOnlyWhatTheAgentSaidIsShownNotItsLifecycle(t *testing.T) {
	for _, p := range []map[string]any{
		{"role": "system", "raw": map[string]any{"subtype": "init"}},
		{"role": "result", "raw": map[string]any{}},
		{"role": "assistant", "text": "   "},
	} {
		if evs := normaliseCLIEvent(agentexec.Event{Type: agentexec.EventAgentMessage, Payload: p}, nil); len(evs) != 0 {
			t.Fatalf("%v gave %+v", p, evs)
		}
	}
	evs := normaliseCLIEvent(agentexec.Event{Type: agentexec.EventAgentMessage, Payload: map[string]any{"role": "assistant", "text": "done"}}, nil)
	if len(evs) != 1 || evs[0].Text != "done" {
		t.Fatalf("%+v", evs)
	}
}

func TestARunWorksInsideTheWorkspaceOrARootAndNowhereElse(t *testing.T) {
	ws, root, other := t.TempDir(), t.TempDir(), t.TempDir()
	sub := filepath.Join(root, "proj")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &App{settings: &backend.Settings{WorkspaceDir: ws}}
	real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }

	if got, err := a.cliCwd("", []string{root}); err != nil || got != real(ws) {
		t.Fatalf("default %q %v", got, err)
	}
	if got, err := a.cliCwd(sub, []string{root}); err != nil || got != real(sub) {
		t.Fatalf("root %q %v", got, err)
	}
	if _, err := a.cliCwd(other, []string{root}); err == nil {
		t.Fatal("a directory outside every root was allowed")
	}
	if _, err := a.cliCwd(filepath.Join(sub, ".."), nil); err == nil {
		t.Fatal("climbing out of the workspace was allowed")
	}
}

func TestTheNextMentionContinuesTheNewestFinishedSessionOfThatAgentInThatChat(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	a := &App{}
	s := a.cliStore()
	now := time.Now()
	s.runs = map[string]*CLIRun{
		"1": {ID: "1", Agent: "claude", Chat: "c1", Session: "old", State: "done", Started: now.Add(-3 * time.Minute)},
		"2": {ID: "2", Agent: "claude", Chat: "c1", Session: "new", State: "done", Started: now.Add(-1 * time.Minute)},
		"3": {ID: "3", Agent: "codex", Chat: "c1", Session: "cx", State: "done", Started: now},
		"4": {ID: "4", Agent: "claude", Chat: "c2", Session: "other", State: "done", Started: now},
		"5": {ID: "5", Agent: "claude", Chat: "c1", Session: "busy", State: "running", Started: now},
	}
	if r := a.lastChatRun("c1", "claude"); r == nil || r.Session != "new" {
		t.Fatalf("got %+v", r)
	}
	if r := a.lastChatRun("c3", "claude"); r != nil {
		t.Fatalf("a fresh chat continued %+v", r)
	}
}

func TestAPtyReasonIsReadableText(t *testing.T) {
	if got := cliLastLine("boot\n\x1b[?25hFor more information, try '--help'.\x1b[0m\n"); got != "For more information, try '--help'." {
		t.Fatalf("%q", got)
	}
}

func TestAnAddressCanNameAMachine(t *testing.T) {
	known := func(n string) bool { return n == "claude" || n == "claude.mac2" || n == "pi.cluster" }
	for msg, want := range map[string]string{
		"@claude.mac2 fix the test": "claude.mac2",
		"@claude.mac2，看一下":          "claude.mac2",
		"@claude do it":             "claude",
		"@pi.cluster hi":            "pi.cluster",
	} {
		name, _, ok := addressedTo(msg, known)
		if !ok || name != want {
			t.Fatalf("%q → %q %v", msg, name, ok)
		}
	}
	if _, _, ok := addressedTo("@claude.nowhere hi", known); ok {
		t.Fatal("an unknown machine was addressed")
	}
	if a, h, ok := splitRemoteCLI("claude.mac2.home"); !ok || a != "claude" || h != "mac2.home" {
		t.Fatalf("%q %q", a, h)
	}
	for _, bad := range []string{"claude", ".mac2", "claude."} {
		if _, _, ok := splitRemoteCLI(bad); ok {
			t.Fatalf("%q split", bad)
		}
	}
}

func TestALinkAddressIsReadTheWayItIsTyped(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.5:43117":         "http://192.168.1.5:43117",
		" https://ai.superleo.cn/ ": "https://ai.superleo.cn",
		"http://mac2.local:47263/":  "http://mac2.local:47263",
	} {
		if got, err := normaliseSuperAIURL(in); err != nil || got != want {
			t.Fatalf("%q → %q %v", in, got, err)
		}
	}
	if validLinkName("mac 2") || validLinkName("a@b") || !validLinkName("mac2.home") {
		t.Fatal("link name rules")
	}
}
