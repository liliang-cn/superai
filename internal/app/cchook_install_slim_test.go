package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The prompt and tool events go to superai-cchook when it sits beside
// superai; an install from before it is moved over in place.
func TestTheBusyEventsMoveToTheSmallHook(t *testing.T) {
	const slim = "/Users/me/.superai-node/bin/superai-cchook"
	old, _, err := mergeCCHooks([]byte(otherToolsSettings), testBin, "", ccHookSpecs)
	if err != nil {
		t.Fatal(err)
	}
	// A person added a key of their own to SuperAI's group by hand.
	old = []byte(strings.Replace(string(old), `"command": "`+testBin+` hook PostToolUse",`,
		`"command": "`+testBin+` hook PostToolUse",`+"\n            \"note\": \"mine\",", 1))

	next, changed, err := mergeCCHooks(old, testBin, slim, ccHookSpecs)
	if err != nil {
		t.Fatal(err)
	}
	want := "UserPromptSubmit (now superai-cchook),PreToolUse (now superai-cchook),PostToolUse (now superai-cchook)"
	if strings.Join(changed, ",") != want {
		t.Fatalf("changed %v", changed)
	}
	s := readSettings(t, next)
	for _, ev := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		ours := 0
		for _, c := range commands(t, s, ev) {
			if isOurHook(c, ev) {
				t.Errorf("%s still runs %q", ev, c)
			}
			if c == slim+" "+ev {
				ours++
			}
		}
		if ours != 1 {
			t.Errorf("%s: %v", ev, commands(t, s, ev))
		}
	}
	if pre := commands(t, s, "PreToolUse"); len(pre) != 3 || pre[0] != "/Users/me/.config/iterm2/cc-status 2>&1" || pre[1] != "/opt/homebrew/bin/anywhered hook PreToolUse" {
		t.Fatalf("other tools' PreToolUse hooks moved: %v", pre)
	}
	if !strings.Contains(string(next), `"note": "mine"`) {
		t.Fatal("the rest of the migrated group was lost")
	}
	// Only the command strings changed: the same lines, three of them different.
	ol, nl := strings.Split(string(old), "\n"), strings.Split(string(next), "\n")
	if len(ol) != len(nl) {
		t.Fatalf("migration changed the layout: %d lines, then %d", len(ol), len(nl))
	}
	diff := 0
	for i := range ol {
		if ol[i] != nl[i] {
			diff++
		}
	}
	if diff != 3 {
		t.Fatalf("%d lines changed, want 3", diff)
	}
	for _, ev := range []string{"Stop", "SessionEnd", "PermissionRequest", "Notification"} {
		if got := commands(t, s, ev); len(got) != 1 || !isOurHook(got[0], ev) {
			t.Errorf("%s should stay with superai hook: %v", ev, got)
		}
	}
	// Again, with or without the small binary at hand: nothing to do.
	for _, sl := range []string{slim, "", "/elsewhere/superai-cchook"} {
		if again, changed, err := mergeCCHooks(next, testBin, sl, ccHookSpecs); err != nil || len(changed) != 0 || string(again) != string(next) {
			t.Fatalf("with %q: changed %v (%v)", sl, changed, err)
		}
	}
	// From nothing, the busy events go straight to it.
	fresh, _, _ := mergeCCHooks(nil, testBin, slim, ccHookSpecs)
	if got := commands(t, readSettings(t, fresh), "PreToolUse"); len(got) != 1 || got[0] != slim+" PreToolUse" {
		t.Fatalf("fresh PreToolUse: %v", got)
	}
}

func TestTheSmallHookIsUsedOnlyWhenItIsThere(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "superai")
	_ = os.WriteFile(bin, []byte("x"), 0o755)
	if got := slimHookBeside(bin); got != "" {
		t.Fatalf("found %q before it was there", got)
	}
	slim := filepath.Join(dir, ccSlimHookName)
	_ = os.WriteFile(slim, []byte("x"), 0o644)
	if got := slimHookBeside(bin); got != "" {
		t.Fatalf("a file that cannot run was taken: %q", got)
	}
	_ = os.Chmod(slim, 0o755)
	if got := slimHookBeside(bin); got != slim {
		t.Fatalf("slimHookBeside = %q", got)
	}
	path := filepath.Join(dir, "settings.json")
	if _, err := installCCHooks(path, bin); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if got := commands(t, readSettings(t, b), "PostToolUse"); len(got) != 1 || got[0] != slim+" PostToolUse" {
		t.Fatalf("PostToolUse: %v", got)
	}
	for cmd, want := range map[string]bool{
		slim + " PreToolUse":               true,
		"'/a b/superai-cchook' PreToolUse": true,
		"/x/superai hook PreToolUse":       false,
		"/x/superai-cchook PostToolUse":    false,
		"/x/not-superai-cchook PreToolUse": false,
	} {
		if got := isOurSlimHook(cmd, "PreToolUse"); got != want {
			t.Errorf("isOurSlimHook(%q) = %v", cmd, got)
		}
	}
}
