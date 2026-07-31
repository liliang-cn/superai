package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestReadPrompt(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		bad  bool
	}{
		{name: "one argument", args: []string{"what is 2 + 2?"}, want: "what is 2 + 2?"},
		// Quoting a prompt is easy to forget, and failing on it would be a
		// pointless way to lose a benchmark run.
		{name: "unquoted words are joined", args: []string{"what", "is", "2+2?"}, want: "what is 2+2?"},
		{name: "surrounding space is not part of it", args: []string{"  hi  "}, want: "hi"},
		{name: "nothing at all", args: nil, bad: true},
		{name: "only whitespace", args: []string{"   "}, bad: true},
	}
	for _, tc := range cases {
		got, err := readPrompt(tc.args)
		if tc.bad {
			if err == nil {
				t.Errorf("%s: expected an error, got %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestReadPromptFromStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	real := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = real }()

	go func() {
		_, _ = w.WriteString("  summarise this  \n")
		_ = w.Close()
	}()

	got, err := readPrompt([]string{"-"})
	if err != nil {
		t.Fatalf("readPrompt: %v", err)
	}
	if got != "summarise this" {
		t.Errorf("got %q, want %q", got, "summarise this")
	}
}

// The JSON shape is the contract a harness reads. Renaming a field or dropping
// one silently breaks whatever is scoring the run, so the names are pinned here
// rather than only in the struct tags.
func TestResultJSONShape(t *testing.T) {
	raw, err := json.Marshal(result{
		OK:         true,
		Answer:     "4",
		Emotion:    "开心",
		Tools:      []string{"calculator"},
		DurationMS: 1200,
		SessionID:  "cli-1",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"ok", "answer", "emotion", "tools", "duration_ms", "session_id"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing field %q in %s", key, raw)
		}
	}
	// A run with no error says nothing about one, so a reader can test presence.
	if _, ok := got["error"]; ok {
		t.Errorf("a successful run should carry no error field: %s", raw)
	}
	// Tools is always a list, never null: a harness iterating it should not have
	// to nil-check.
	if _, ok := got["tools"].([]any); !ok {
		t.Errorf("tools should be a list, got %T", got["tools"])
	}
}

// Nothing the agent prints while starting up may reach stdout, because in plain
// mode stdout is the answer and a benchmark parses it verbatim.
func TestWithStdoutOnStderrKeepsStdoutClean(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	defer func() { os.Stdout, os.Stderr = realOut, realErr }()

	out, fnErr := withStdoutOnStderr(func() (string, error) {
		os.Stdout.WriteString("ℹ️  No embedding provider configured\n")
		return "service", nil
	})

	os.Stdout, os.Stderr = realOut, realErr
	_ = w.Close()
	captured := make([]byte, 512)
	n, _ := r.Read(captured)
	_ = r.Close()

	if fnErr != nil || out != "service" {
		t.Fatalf("the wrapped call must still return its value: %q %v", out, fnErr)
	}
	if !strings.Contains(string(captured[:n]), "No embedding provider") {
		t.Error("the notice should have been forwarded, not swallowed")
	}
}
