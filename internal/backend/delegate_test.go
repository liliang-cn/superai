package backend

import (
	"strings"
	"testing"
	"time"
)

// Handing a task to another agent CLI is the most consequential call this app
// can make on someone's behalf: it spends a second subscription, runs for
// minutes, and writes files with that CLI's own approval prompt bypassed. Two
// things have to hold for that to be safe — the person approving it can see
// what is about to run, and the model only reaches for it when it is the right
// tool. These test both.

func TestTheApprovalCardShowsWhatWouldBeDelegated(t *testing.T) {
	got := approvalCommand("cli_agent_run", map[string]any{
		"agent":  "claude",
		"prompt": "refactor the auth package and run the tests",
		"cwd":    "/Users/someone/work/api",
	})
	for _, want := range []string{"claude", "-p", "refactor the auth package", "/Users/someone/work/api"} {
		if !strings.Contains(got, want) {
			t.Errorf("the card would not show %q; it says %q", want, got)
		}
	}
}

func TestADelegatedPromptIsOneLineOnTheCard(t *testing.T) {
	// A prompt is often a paragraph. The card is a few hundred pixels wide and
	// a wrapped one pushes the buttons off it, so the line is cut and marked.
	got := approvalCommand("cli_agent_run", map[string]any{
		"agent":  "codex",
		"prompt": "first line\nsecond line\nthird line",
	})
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("the card's command line wrapped: %q", got)
	}
	if !strings.Contains(got, "first line") || strings.Contains(got, "second line") {
		t.Fatalf("expected the first line and a marker, got %q", got)
	}

	long := strings.Repeat("x", 400)
	if n := len([]rune(approvalCommand("cli_agent_run", map[string]any{"agent": "claude", "prompt": long}))); n > 220 {
		t.Fatalf("a 400-character prompt rendered %d runes onto the card", n)
	}
}

func TestOtherToolsKeepTheirOwnCommandLine(t *testing.T) {
	// approvalCommand routes; it must not swallow what commandOf already did.
	if got := approvalCommand("bash", map[string]any{"command": "ls -la"}); got != "ls -la" {
		t.Fatalf("shell tools lost their command line: %q", got)
	}
	if got := approvalCommand("add_schedule", map[string]any{"title": "standup"}); got != "" {
		t.Fatalf("a tool with no command invented one: %q", got)
	}
}

func TestDelegationIsOnlyTaughtWhenItIsTurnedOn(t *testing.T) {
	// The persona is what decides whether the model reaches for another agent.
	// Teaching it about a tool that is not registered is how a reply comes
	// back promising work that never ran.
	off := buildPersona(time.Now(), false, false)
	if strings.Contains(off, "cli_agent_run") {
		t.Error("the persona offers delegation with the setting off")
	}
	on := buildPersona(time.Now(), false, true)
	if !strings.Contains(on, "cli_agent_run") || !strings.Contains(on, "cli_agent_list") {
		t.Error("the persona does not mention the tools with the setting on")
	}
	// The half that keeps it from becoming the answer to everything.
	for _, want := range []string{"Wrong for anything you can do yourself", "failed to sign in"} {
		if !strings.Contains(on, want) {
			t.Errorf("the persona is missing its guard rail: %q", want)
		}
	}
}
