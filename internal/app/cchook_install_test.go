package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A settings file as the Mac has it: other tools' hooks on the same events,
// SuperLeo's older set, and settings that have nothing to do with hooks.
const otherToolsSettings = `{
  "model": "opus",
  "permissions": {
    "allow": ["Bash(git status)"]
  },
  "hooks": {
    "PreToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/me/.config/iterm2/cc-status 2>&1"
          }
        ]
      },
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "/opt/homebrew/bin/anywhered hook PreToolUse"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/me/.superleo-node/bin/superleo hook Stop",
            "timeout": 5
          }
        ]
      }
    ],
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/Users/me/.superleo-node/bin/superleo hook SessionStart",
            "timeout": 5
          }
        ]
      }
    ]
  },
  "statusLine": {
    "type": "command",
    "command": "x"
  }
}
`

const testBin = "/Users/me/.superleo-node/bin/superleo"

func readSettings(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return m
}

// commands lists every hook command for event.
func commands(t *testing.T, settings map[string]any, event string) []string {
	t.Helper()
	var out []string
	groups, _ := settings["hooks"].(map[string]any)[event].([]any)
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			out = append(out, h.(map[string]any)["command"].(string))
		}
	}
	return out
}

func TestInstallAddsOnlyWhatIsMissingAndKeepsEveryoneElses(t *testing.T) {
	next, added, err := mergeCCHooks([]byte(otherToolsSettings), testBin, "", ccHookSpecs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SessionEnd", "StopFailure", "Notification", "PermissionRequest", "UserPromptSubmit", "PreToolUse", "PostToolUse"}
	if strings.Join(added, ",") != strings.Join(want, ",") {
		t.Fatalf("added %v, want %v", added, want)
	}
	s := readSettings(t, next)
	if s["model"] != "opus" || s["statusLine"].(map[string]any)["command"] != "x" {
		t.Fatalf("other settings changed: %s", next)
	}
	pre := commands(t, s, "PreToolUse")
	if len(pre) != 3 || pre[0] != "/Users/me/.config/iterm2/cc-status 2>&1" || pre[1] != "/opt/homebrew/bin/anywhered hook PreToolUse" || pre[2] != testBin+" hook PreToolUse" {
		t.Fatalf("PreToolUse: %v", pre)
	}
	if got := commands(t, s, "Stop"); len(got) != 1 {
		t.Fatalf("Stop doubled: %v", got)
	}
	if got := commands(t, s, "PermissionRequest"); len(got) != 1 || got[0] != testBin+" hook PermissionRequest --wait 100" {
		t.Fatalf("PermissionRequest: %v", got)
	}
	// Written for people: no & for the &, keys in their order, the new
	// group as Claude Code's docs show one.
	text := string(next)
	if strings.Contains(text, "\\u0026") || strings.Contains(text, "\\u003e") || !strings.Contains(text, "2>&1") {
		t.Fatalf("escaped: %s", text)
	}
	if i, j, k := strings.Index(text, `"model"`), strings.Index(text, `"permissions"`), strings.Index(text, `"statusLine"`); !(i < j && j < strings.Index(text, `"hooks"`) && strings.Index(text, `"hooks"`) < k) {
		t.Fatalf("keys reordered: %s", text)
	}
	if !strings.Contains(text, "{\n        \"matcher\": \"*\",\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \""+testBin+" hook PostToolUse\",\n            \"timeout\": 2") {
		t.Fatalf("new group layout:\n%s", text)
	}
	if strings.Contains(text, `"matcher": ""`) {
		t.Fatalf("an empty matcher was written: %s", text)
	}

	// Again: nothing to do, the same bytes.
	again, added, err := mergeCCHooks(next, testBin, "", ccHookSpecs)
	if err != nil || len(added) != 0 || string(again) != string(next) {
		t.Fatalf("second install added %v (%v)", added, err)
	}
	// Another path to SuperLeo is still SuperLeo: not a second set.
	if _, added, _ := mergeCCHooks(next, "/usr/local/bin/superleo", "", ccHookSpecs); len(added) != 0 {
		t.Fatalf("a second binary added %v", added)
	}
}

func TestInstallFromNothing(t *testing.T) {
	for _, in := range []string{"", "{}", `{"hooks": null}`} {
		next, added, err := mergeCCHooks([]byte(in), "/opt/super ai/superleo", "", ccHookSpecs)
		if err != nil || len(added) != len(ccHookSpecs) {
			t.Fatalf("%q: added %v (%v)", in, added, err)
		}
		s := readSettings(t, next)
		if got := commands(t, s, "UserPromptSubmit"); len(got) != 1 || got[0] != "'/opt/super ai/superleo' hook UserPromptSubmit" {
			t.Fatalf("%q: %v", in, got)
		}
	}
	if _, _, err := mergeCCHooks([]byte(`["not", "settings"]`), testBin, "", ccHookSpecs); err == nil {
		t.Fatal("a list was taken for settings")
	}
	if _, _, err := mergeCCHooks([]byte(`{"hooks": {"Stop": {"oops": 1}}}`), testBin, "", ccHookSpecs); err == nil {
		t.Fatal("a broken hooks entry was written over")
	}
}

func TestInstallWritesTheFileThroughItsLinkAndKeepsTheOld(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "claude-settings.json")
	_ = os.MkdirAll(filepath.Dir(real), 0o755)
	if err := os.WriteFile(real, []byte(otherToolsSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	added, err := installCCHooks(link, testBin)
	if err != nil || len(added) == 0 {
		t.Fatalf("install: %v %v", added, err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a file")
	}
	st, _ := os.Stat(real)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if bak, err := os.ReadFile(real + ".superleo.bak"); err != nil || string(bak) != otherToolsSettings {
		t.Fatalf("no backup of the old file: %v", err)
	}
	b, _ := os.ReadFile(real)
	if got := commands(t, readSettings(t, b), "PreToolUse"); len(got) != 3 {
		t.Fatalf("PreToolUse: %v", got)
	}
	// Run again: untouched, not even rewritten.
	before, _ := os.Stat(real)
	if added, err := installCCHooks(link, testBin); err != nil || len(added) != 0 {
		t.Fatalf("second install: %v %v", added, err)
	}
	if after, _ := os.Stat(real); !after.ModTime().Equal(before.ModTime()) {
		t.Error("an install with nothing to add rewrote the file")
	}

	// A broken file is left as it is.
	broken := filepath.Join(dir, "broken.json")
	_ = os.WriteFile(broken, []byte(`{"hooks": `), 0o644)
	if _, err := installCCHooks(broken, testBin); err == nil {
		t.Fatal("a broken file was written over")
	}
	if b, _ := os.ReadFile(broken); string(b) != `{"hooks": ` {
		t.Fatalf("broken file now %q", b)
	}
}

func TestOnlyAnOptedInSettingsFileIsUpgraded(t *testing.T) {
	if !settingsHaveOurHook([]byte(otherToolsSettings), testBin) {
		t.Fatal("SuperLeo's own hooks were not recognised")
	}
	if settingsHaveOurHook([]byte(otherToolsSettings), "/elsewhere/superleo") {
		t.Fatal("another binary's hooks count as this one's")
	}
	for cmd, want := range map[string]string{
		"/x/superleo hook Stop":                             "/x/superleo",
		"'/a b/superleo' hook PermissionRequest --wait 100": "/a b/superleo",
		"superleo hook Stop":                                "superleo",
		"/opt/homebrew/bin/anywhered PreToolUse":            "",
	} {
		if got := hookCommandBinary(cmd); got != want {
			t.Errorf("hookCommandBinary(%q) = %q, want %q", cmd, got, want)
		}
	}
	// The same binary through a link is the same binary.
	dir0 := t.TempDir()
	bin0 := filepath.Join(dir0, "superleo")
	_ = os.WriteFile(bin0, []byte("x"), 0o755)
	_ = os.Symlink(dir0, filepath.Join(dir0, "via"))
	linked := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + filepath.Join(dir0, "via", "superleo") + ` hook Stop"}]}]}}`
	if !settingsHaveOurHook([]byte(linked), bin0) {
		t.Error("a hook through a linked directory was not this binary's")
	}
	onlyOthers := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"/opt/homebrew/bin/anywhered hook PreToolUse"}]}]}}`
	if settingsHaveOurHook([]byte(onlyOthers), "/opt/homebrew/bin/anywhered") {
		t.Fatal("anywhered's hook taken for SuperLeo's")
	}
	for cmd, want := range map[string]bool{
		"/x/superleo hook Stop":                 true,
		"'/a b/superleo' hook Stop":             true,
		"superleo hook Stop --flag":             true,
		"/opt/homebrew/bin/anywhered hook Stop": false,
		"/x/superleo-desktop hook Stop":         false,
		"/x/superleo hook StopFailure":          false,
	} {
		if got := isOurHook(cmd, "Stop"); got != want {
			t.Errorf("isOurHook(%q) = %v", cmd, got)
		}
	}

	// The node's start-up upgrade, against a settings file of its own.
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("SUPERLEO_HOOK_AUTOINSTALL", "")
	path := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(path, []byte(onlyOthers), 0o644)
	autoUpgradeCCHooks() // a test binary never installs itself
	if b, _ := os.ReadFile(path); string(b) != onlyOthers {
		t.Fatalf("a file without SuperLeo's hooks was changed: %s", b)
	}
}
