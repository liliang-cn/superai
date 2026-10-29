package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superleo/internal/backend"
)

// fakeCLI writes an executable shell script into a fresh directory.
func fakeCLI(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// noDiscovery stands in for agentexec.Discover: the machine running the test
// has whatever CLIs it has, and they are not what is under test.
func noDiscovery(t *testing.T, found ...agentexec.Installed) {
	was := clis
	clis = &cliDiscovery{discover: func(map[string]string) []agentexec.Installed { return found }}
	t.Cleanup(func() { clis = was })
}

func codingApp(t *testing.T, ext backend.ExternalAgents) *App {
	t.Helper()
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	ext.Enabled = true
	return &App{settings: &backend.Settings{WorkspaceDir: t.TempDir(), ExternalAgents: ext}}
}

func runTo(t *testing.T, a *App, agent, prompt string) CLIRun {
	t.Helper()
	done := make(chan CLIRun, 1)
	if _, err := a.startCLIRun(cliStart{Agent: agent, Prompt: prompt, Done: func(r CLIRun) { done <- r }}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not end")
	}
	return CLIRun{}
}

// A user-added tool is run like any coding agent: by its id, in the
// workspace, the task one argument however it is written.
func TestACustomToolRunsWithThePromptAsOneArgument(t *testing.T) {
	noDiscovery(t)
	// Prints each argument on its own line between brackets, and where it ran.
	echo := fakeCLI(t, "echoer", `for a in "$@"; do printf '[%s]\n' "$a"; done; printf 'cwd=%s\n' "$PWD"`)
	a := codingApp(t, backend.ExternalAgents{Custom: []backend.CodingTool{
		{ID: "echoer", Name: "Echoer", Command: echo + " --say {prompt} -m {model}"},
	}})
	prompt := `; rm -rf ~ && echo "pwned" 'x'`
	r := runTo(t, a, "echoer", prompt)
	if r.State != "done" {
		t.Fatalf("state %s: %s", r.State, r.Error)
	}
	ws, _ := filepath.EvalSymlinks(a.settings.WorkspaceDir)
	want := "[--say]\n[" + prompt + "]\ncwd=" + ws
	if r.Summary != want {
		t.Fatalf("summary\n%s\nwant\n%s", r.Summary, want)
	}
	if !a.isDrivenCLI("echoer") {
		t.Fatal("the tool is not offered as a coding agent")
	}
}

// Exit 0 with a refusal is a failure, not an answer.
func TestACustomToolThatRefusesAndExitsZeroFails(t *testing.T) {
	noDiscovery(t)
	cli := fakeCLI(t, "refuser", `echo "Failed to authenticate. API Error: 401 {\"type\":\"authentication_error\"}"; exit 0`)
	a := codingApp(t, backend.ExternalAgents{Custom: []backend.CodingTool{{ID: "refuser", Command: cli + " -p {prompt}"}}})
	r := runTo(t, a, "refuser", "hello")
	if r.State != "failed" || !strings.Contains(r.Error, "Failed to authenticate") {
		t.Fatalf("state %s, error %q", r.State, r.Error)
	}
	try := a.TryCodingTool("", CodingToolEdit{Kind: "custom", ID: "refuser", Command: cli + " -p {prompt}"})
	if try.OK || !strings.HasPrefix(try.Reason, "not signed in") {
		t.Fatalf("try: %+v", try)
	}
}

func TestTryingACustomTool(t *testing.T) {
	noDiscovery(t)
	cli := fakeCLI(t, "okcli", `echo ok`)
	a := codingApp(t, backend.ExternalAgents{})
	try := a.TryCodingTool("", CodingToolEdit{Kind: "custom", ID: "okcli", Command: cli + " {prompt}"})
	if !try.OK || try.Text != "ok" {
		t.Fatalf("%+v", try)
	}
	if got := lastTry("okcli"); got == nil || !got.OK {
		t.Fatal("the answer is not remembered for the list")
	}
	silent := a.TryCodingTool("", CodingToolEdit{Kind: "custom", Command: fakeCLI(t, "mute", "exit 0")})
	if silent.OK {
		t.Fatal("silence passed as an answer")
	}
	missing := a.TryCodingTool("", CodingToolEdit{Kind: "custom", Command: "surely-not-installed-cli {prompt}"})
	if missing.OK || !strings.Contains(missing.Reason, "not installed") {
		t.Fatalf("%+v", missing)
	}
}

func TestSavingAndDeletingCodingTools(t *testing.T) {
	e := backend.ExternalAgents{Disabled: []string{"kimi"}}
	if err := applyCodingToolEdit(&e, CodingToolEdit{Kind: "custom", Name: "Gemini CLI", Command: "gemini -p {prompt} --yolo", Preset: "gemini"}); err != nil {
		t.Fatal(err)
	}
	if err := applyCodingToolEdit(&e, CodingToolEdit{Kind: "custom", Name: "Gemini CLI", Command: "gemini {prompt}"}); err != nil {
		t.Fatal(err)
	}
	if len(e.Custom) != 2 || e.Custom[0].ID != "gemini-cli" || e.Custom[1].ID != "gemini-cli-2" {
		t.Fatalf("%+v", e.Custom)
	}
	// An edit keeps the id, name and preset it is not given.
	if err := applyCodingToolEdit(&e, CodingToolEdit{Kind: "custom", ID: "gemini-cli", Command: "gemini -p {prompt}", Model: "pro"}); err != nil {
		t.Fatal(err)
	}
	if c := e.Custom[0]; c.Name != "Gemini CLI" || c.Preset != "gemini" || c.Model != "pro" || c.Command != "gemini -p {prompt}" {
		t.Fatalf("%+v", c)
	}
	for _, bad := range []CodingToolEdit{
		{Kind: "custom", Name: "x", Command: `x "open`},
		{Kind: "custom", ID: "kimi", Name: "Kimi", Command: "kimi {prompt}"},
		{Kind: "custom", Command: "x"},
		{Kind: "nope", ID: "x"},
	} {
		if applyCodingToolEdit(&e, bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// A found CLI: its path and model, and offered again.
	if err := applyCodingToolEdit(&e, CodingToolEdit{Kind: "found", ID: "kimi", Path: "~/bin/kimi", Model: "k2"}); err != nil {
		t.Fatal(err)
	}
	if e.Binaries["kimi"] != "~/bin/kimi" || e.Models["kimi"] != "k2" || len(e.Disabled) != 0 {
		t.Fatalf("%+v", e)
	}
	if err := deleteCodingTool(&e, "kimi"); err != nil || e.Binaries["kimi"] != "" || len(e.Disabled) != 1 {
		t.Fatalf("%v %+v", err, e)
	}
	if err := deleteCodingTool(&e, "gemini-cli-2"); err != nil || len(e.Custom) != 1 {
		t.Fatalf("%v %+v", err, e.Custom)
	}
	if deleteCodingTool(&e, "claude") == nil {
		t.Fatal("a built-in was deleted")
	}
}

// The list: the three built-ins always, other found CLIs unless switched off,
// the added tools, and in the menu only what is not listed — marked found
// or not.
func TestTheCodingToolList(t *testing.T) {
	bin := filepath.Dir(fakeCLI(t, "kimi", "echo kimi 1.0"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	noDiscovery(t,
		agentexec.Installed{Name: "codex", Binary: "/x/codex", Version: "0.9"},
		agentexec.Installed{Name: "opencode", Binary: "/x/opencode"},
		agentexec.Installed{Name: "goose", Binary: "/x/goose"},
	)
	a := codingApp(t, backend.ExternalAgents{Disabled: []string{"goose"}, Custom: []backend.CodingTool{
		{ID: "gem", Name: "Gemini CLI", Command: "gemini -p {prompt}", Preset: "gemini"},
	}})
	l := a.LocalCodingTools()
	kinds := map[string]string{}
	for _, r := range l.Tools {
		kinds[r.ID] = r.Kind
	}
	want := map[string]string{"claude": "builtin", "codex": "builtin", "cursor-agent": "builtin", "opencode": "found", "gem": "custom"}
	if len(kinds) != len(want) {
		t.Fatalf("rows %v", kinds)
	}
	for id, k := range want {
		if kinds[id] != k {
			t.Fatalf("rows %v", kinds)
		}
	}
	offers := map[string]bool{}
	for _, o := range l.Offers {
		offers[o.ID] = o.Found
	}
	if _, listed := offers["opencode"]; listed {
		t.Fatal("a listed CLI is offered")
	}
	if _, used := offers["gemini"]; used {
		t.Fatal("a preset already added is offered")
	}
	if found, ok := offers["kimi"]; !ok || !found {
		t.Fatalf("kimi on PATH: %v", offers)
	}
	if _, ok := offers["goose"]; !ok {
		t.Fatalf("goose, switched off, is not offered back: %v", offers)
	}
}

// The added tools are in the @ list and in what a linked SuperLeo is told.
func TestCustomToolsAreOffered(t *testing.T) {
	noDiscovery(t, agentexec.Installed{Name: "codex", Binary: "/x/codex"})
	ok := fakeCLI(t, "mine", "echo hi")
	ext := backend.ExternalAgents{Enabled: true, Custom: []backend.CodingTool{
		{ID: "mine", Command: ok + " {prompt}"},
		{ID: "gone", Command: "surely-not-installed-cli {prompt}"},
		{ID: "off", Command: ok},
	}, Disabled: []string{"off"}}
	got := withCustomCLIs(installedCLIs(nil), ext)
	if len(got) != 2 || got[1].Name != "mine" || got[1].Binary != ok {
		t.Fatalf("%+v", got)
	}
	st := customAgentStatuses(ext)
	if len(st) != 1 || st[0].Name != "mine" || !st[0].Installed {
		t.Fatalf("%+v", st)
	}
}
