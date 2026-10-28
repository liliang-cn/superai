package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each CLI's own log reads as the same events: what the person typed, what
// the agent said, its tool calls and what came back; injected context is not
// something anybody typed.
func TestSessionLogsReadTheSameAcrossCLIs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude/projects/-w/c1.jsonl", strings.Join([]string{
		`{"type":"user","cwd":"/w","timestamp":"2026-10-28T08:00:00Z","message":{"role":"user","content":"fix the test"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"x"},{"type":"text","text":"Looking."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok","is_error":false}]}}`,
	}, "\n"))
	write(".codex/sessions/2026/10/28/rollout-2026-10-28T07-00-00-x1.jsonl", strings.Join([]string{
		`{"type":"session_meta","payload":{"cwd":"/w"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>…"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"add a flag"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"ls\",\"-la\"]}","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"a b"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}`,
	}, "\n"))
	write(".pi/agent/sessions/--w--/2026-10-28T00-00-00Z_p1.jsonl", strings.Join([]string{
		`{"type":"session","cwd":"/w","id":"p1"}`,
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"k1","name":"read","arguments":{"path":"go.mod"}}]}}`,
		`{"type":"message","message":{"role":"toolResult","toolCallId":"k1","content":[{"type":"text","text":"module x"}]}}`,
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"It is x."}]}}`,
	}, "\n"))
	kinds := func(l SessionLog) string {
		var k []string
		for _, e := range l.Events {
			k = append(k, e.Kind)
		}
		return strings.Join(k, ",")
	}
	for _, c := range []struct{ cli, id, want, tool string }{
		{"claude", "c1", "you,text,tool,result", "go test ./..."},
		{"codex", "x1", "you,tool,result,text", "ls -la"},
		{"pi", "p1", "you,tool,result,text", "go.mod"},
	} {
		l, err := readSessionLog(c.cli, c.id)
		if err != nil {
			t.Fatalf("%s: %v", c.cli, err)
		}
		if got := kinds(l); got != c.want || l.Cwd != "/w" {
			t.Fatalf("%s: %s cwd %q", c.cli, got, l.Cwd)
		}
		for _, e := range l.Events {
			if e.Kind == "tool" && e.Detail != c.tool {
				t.Fatalf("%s: tool detail %q", c.cli, e.Detail)
			}
		}
	}
	if _, err := readSessionLog("claude", "../etc"); err == nil {
		t.Fatal("read a path for an id")
	}
}

// An answer for codex or pi goes into the terminal its hooks reported, tmux
// first; a terminal another session has since taken is refused.
func TestARepliesGoIntoTheTerminalTheSessionIsIn(t *testing.T) {
	var typed []string
	r := &cliReach{at: map[string]cliReachSpot{},
		paneAlive: func(p string) bool { return p == "%3" },
		typeTmux:  func(p, s string) error { typed = append(typed, "tmux "+p+" "+s); return nil },
		typeITerm: func(_ context.Context, tty, s string) error { typed = append(typed, "term "+tty+" "+s); return nil },
		liveTTYs:  func() map[string]bool { return map[string]bool{"/dev/ttys004": true} },
	}
	r.observe(ccHookEvent{CLI: "codex", Session: "a", Pane: "%3", TTY: "/dev/ttys009"})
	r.observe(ccHookEvent{CLI: "pi", Session: "b", TTY: "/dev/ttys004"})
	r.observe(ccHookEvent{CLI: "pi", Session: "c", TTY: "/dev/ttys005"})
	r.observe(ccHookEvent{CLI: "codex", Session: "d", TTY: "/dev/ttys004"}) // took b's tab
	if d, err := r.deliver(context.Background(), "codex", "a", "go on"); err != nil || d.Via != "tmux" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := r.deliver(context.Background(), "pi", "b", "hi"); err == nil {
		t.Fatal("typed into a tab another session has")
	}
	if _, err := r.deliver(context.Background(), "pi", "zz", "hi"); err == nil {
		t.Fatal("typed into a session nobody reported")
	}
	if d, err := r.deliver(context.Background(), "codex", "d", "yes"); err != nil || d.Via != "terminal" {
		t.Fatalf("%+v %v", d, err)
	}
	if strings.Join(typed, "|") != "tmux %3 go on|term /dev/ttys004 yes" {
		t.Fatalf("%v", typed)
	}
}
