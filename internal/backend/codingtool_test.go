package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The task goes into one argument, whatever it contains: nothing in it can
// become another argument, an option, or a command.
func TestCodingToolArgvKeepsThePromptOneArgument(t *testing.T) {
	tool := CodingTool{ID: "g", Command: `gemini -m {model} -p {prompt} --yolo`}
	for _, prompt := range []string{
		"; rm -rf ~",
		`"quoted" and 'single' $(whoami) ` + "`id`",
		"line one\nline two && echo pwned | sh",
		"--yolo --model evil",
		"{model}",
	} {
		argv, err := tool.Argv(prompt, "")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"gemini", "-p", prompt, "--yolo"}
		if !reflect.DeepEqual(argv, want) {
			t.Fatalf("prompt %q: argv %q, want %q", prompt, argv, want)
		}
	}
}

func TestCodingToolArgvModel(t *testing.T) {
	cases := []struct {
		cmd, model, toolModel string
		want                  []string
	}{
		{"x -m {model} -p {prompt}", "", "", []string{"x", "-p", "P"}},
		{"x -m {model} -p {prompt}", "big", "", []string{"x", "-m", "big", "-p", "P"}},
		{"x -m {model} -p {prompt}", "", "saved", []string{"x", "-m", "saved", "-p", "P"}},
		{"x --model={model} {prompt}", "", "", []string{"x", "P"}},
		{"x --model={model} {prompt}", "m1", "", []string{"x", "--model=m1", "P"}},
		{"x run", "", "", []string{"x", "run", "P"}},                                      // {prompt} appended
		{`x "two words" --text={prompt}`, "", "", []string{"x", "two words", "--text=P"}}, // quotes keep a word
		{`x '' {prompt}`, "", "", []string{"x", "", "P"}},                                 // an empty quoted word stays
	}
	for _, c := range cases {
		got, err := CodingTool{Command: c.cmd, Model: c.toolModel}.Argv("P", c.model)
		if err != nil {
			t.Fatalf("%q: %v", c.cmd, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q model %q: %q, want %q", c.cmd, c.model, got, c.want)
		}
	}
}

func TestCodingToolTemplateRefuses(t *testing.T) {
	for _, cmd := range []string{"", "   ", "{prompt} x", "x {prompt} {prompt}", `x "open`} {
		if _, err := (CodingTool{Command: cmd}).Template(); err == nil {
			t.Errorf("%q: no error", cmd)
		}
	}
}

func TestCodingToolIDs(t *testing.T) {
	if s := CodingToolSlug("Gemini CLI"); s != "gemini-cli" {
		t.Errorf("slug %q", s)
	}
	if s := CodingToolSlug("编程"); s != "tool" {
		t.Errorf("slug of no ascii %q", s)
	}
	for _, bad := range []string{"", "a.b", "Kimi", "kimi", "claude", "a b", strings.Repeat("a", 40)} {
		if ValidCodingToolID(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"gemini", "my-tool_2"} {
		if err := ValidCodingToolID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

// A settings file from before custom tools reads as none; blank rows the page
// left behind are dropped, and so is a second tool with the same id.
func TestExternalAgentsCustomMigration(t *testing.T) {
	var old ExternalAgents
	if err := json.Unmarshal([]byte(`{"enabled":true,"binaries":{"codex":"/x/codex"}}`), &old); err != nil {
		t.Fatal(err)
	}
	old.normalize()
	if old.Custom != nil || old.Binary("codex") != "/x/codex" {
		t.Fatalf("old file: %+v", old)
	}
	e := ExternalAgents{Custom: []CodingTool{
		{ID: "a", Command: " a {prompt} "},
		{ID: "", Command: "b"},
		{ID: "c", Command: "  "},
		{ID: "a", Command: "dup"},
	}}
	e.normalize()
	if len(e.Custom) != 1 || e.Custom[0].Command != "a {prompt}" || e.Custom[0].Name != "a" {
		t.Fatalf("normalized: %+v", e.Custom)
	}
	raw, _ := json.Marshal(ExternalAgents{})
	if strings.Contains(string(raw), "custom") {
		t.Fatalf("an empty list is written: %s", raw)
	}
}

func TestFindProgram(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakecli")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if p, ok := FindProgram("fakecli"); !ok || p != bin {
		t.Fatalf("on PATH: %q %v", p, ok)
	}
	if p, ok := FindProgram(bin); !ok || p != bin {
		t.Fatalf("by path: %q %v", p, ok)
	}
	if _, ok := FindProgram("surely-not-a-cli-here"); ok {
		t.Fatal("found a missing program")
	}
	if _, ok := FindProgram(dir); ok {
		t.Fatal("a directory is not a program")
	}
}

// claude with a revoked token prints the refusal and exits 0; so may any
// text tool. A short refusal is a failure, an answer is not.
func TestCodingToolRefusal(t *testing.T) {
	for _, out := range []string{
		"Failed to authenticate. API Error: 401",
		"Error: not logged in. Please run `kimi login`",
		`{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`,
	} {
		if _, ok := CodingToolRefusal(out); !ok {
			t.Errorf("not read as a refusal: %q", out)
		}
	}
	for _, out := range []string{
		"ok",
		"",
		"Done. I added the test in auth_test.go.",
		strings.Repeat("The login flow calls authenticate() before it renders. ", 20),
	} {
		if r, ok := CodingToolRefusal(out); ok {
			t.Errorf("answer read as a refusal (%s): %q", r, out)
		}
	}
}
