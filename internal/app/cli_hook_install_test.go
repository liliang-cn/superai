package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIPluginsInNode runs the plugins' own tests (cliplugins/
// plugins_test.mjs) when node is here.
func TestCLIPluginsInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	out, err := exec.Command(node, "--test", "cliplugins/plugins_test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// codexHooksJSON is the Mac's codex hooks.json, as other tools wrote it
// (paths shortened), with its \/ escapes and spaced colons.
const codexHooksJSON = `{
  "hooks" : {
    "PermissionRequest" : [
      {
        "_otty" : true,
        "hooks" : [
          {
            "command" : "'\/Applications\/Otty.app\/otty-hook.sh' awaiting \"$PPID\"",
            "type" : "command"
          }
        ]
      }
    ],
    "PreToolUse" : [
      {
        "hooks" : [
          {
            "command" : "\/opt\/homebrew\/bin\/anywhered hook PreToolUse codex",
            "type" : "command"
          }
        ]
      }
    ],
    "Stop" : [
      {
        "hooks" : [
          {
            "command" : "\/opt\/homebrew\/bin\/anywhered hook Stop codex",
            "type" : "command"
          }
        ]
      },
      {
        "_otty" : true,
        "hooks" : [
          {
            "command" : "'\/Applications\/Otty.app\/otty-hook.sh' idle \"$PPID\"",
            "type" : "command"
          }
        ]
      }
    ]
  }
}
`

func TestCodexHooksJoinTheOthersAtTheEnd(t *testing.T) {
	const bin, slim = "/Users/me/.superleo-node/bin/superleo", "/Users/me/.superleo-node/bin/superleo-cchook"
	next, added, err := mergeCodexHooks([]byte(codexHooksJSON), bin, slim)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(added, ",") != strings.Join(codexHookEvents, ",") {
		t.Fatalf("added %v", added)
	}
	var file struct {
		Hooks map[string][]struct {
			Otty  bool `json:"_otty"`
			Hooks []struct{ Command, Type string }
		}
	}
	if err := json.Unmarshal(next, &file); err != nil {
		t.Fatal(err)
	}
	// Codex keys a hook's trust by its place: the ones there keep theirs.
	stop := file.Hooks["Stop"]
	if len(stop) != 3 || !strings.Contains(stop[0].Hooks[0].Command, "anywhered hook Stop") || !stop[1].Otty ||
		stop[2].Hooks[0].Command != slim+" --cli codex Stop" || stop[2].Hooks[0].Type != "command" {
		t.Fatalf("Stop %+v", stop)
	}
	if pr := file.Hooks["PermissionRequest"]; len(pr) != 2 || !pr[0].Otty || pr[0].Hooks[0].Command != `'/Applications/Otty.app/otty-hook.sh' awaiting "$PPID"` {
		t.Fatalf("PermissionRequest %+v", pr)
	}
	if in := file.Hooks["Interrupt"]; len(in) != 1 || in[0].Hooks[0].Command != slim+" --cli codex Interrupt" {
		t.Fatalf("Interrupt %+v", in)
	}
	// Again: nothing to do, whichever binary is at hand.
	for _, s := range []string{slim, ""} {
		again, more, err := mergeCodexHooks(next, bin, s)
		if err != nil || len(more) != 0 || !bytes.Equal(again, next) {
			t.Fatalf("again with %q: %v %v", s, more, err)
		}
	}
	// Without superleo-cchook, superleo itself.
	fresh, _, _ := mergeCodexHooks(nil, bin, "")
	if !strings.Contains(string(fresh), `"command": "`+bin+` hook --cli codex PreToolUse"`) {
		t.Fatalf("fresh %s", fresh)
	}
	if _, _, err := mergeCodexHooks([]byte(`{"hooks":{"Stop":{}}}`), bin, slim); err == nil {
		t.Fatal("a Stop that is not a list was taken")
	}
}

// An opencode with its own config, packages and plugins, a pi and a codex:
// SuperLeo adds its file or entries, leaves the rest, keeps what it changes,
// and a second run changes nothing.
func TestCLIHooksInstallBesideTheirConfig(t *testing.T) {
	root := t.TempDir()
	oc, pi, cx := filepath.Join(root, "opencode"), filepath.Join(root, "pi"), filepath.Join(root, "codex")
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	others := map[string]string{
		filepath.Join(oc, "opencode.jsonc"):              "{\n  // mine\n  \"model\": \"linode/gemini\",\n  \"plugin\": [\"opencode-wakatime\"]\n}\n",
		filepath.Join(oc, "package.json"):                `{"dependencies":{"@opencode-ai/plugin":"1.18.3"}}`,
		filepath.Join(oc, "plugins", "mine.ts"):          "export const Mine = async () => ({})\n",
		filepath.Join(oc, "node_modules", "x", "i.js"):   "module.exports = 1\n",
		filepath.Join(pi, "settings.json"):               `{"theme":"light"}`,
		filepath.Join(pi, "extensions", "hello.ts"):      "export default function () {}\n",
		filepath.Join(cx, "config.toml"):                 "model = \"gpt\"\n[features]\nhooks = true\n",
		filepath.Join(cx, "hooks.json"):                  codexHooksJSON,
		filepath.Join(oc, "plugins", "superleo-old.txt"): "not ours to touch",
	}
	for p, s := range others {
		write(p, s)
	}
	tg := cliHookTargets{Opencode: oc, Pi: pi, Codex: cx, Bin: "/n/bin/superleo", Slim: "/n/bin/superleo-cchook", Socket: "/n/cc-hook.sock"}
	done, errs := installCLIHooks(tg)
	if len(errs) != 0 || len(done) != 3 {
		t.Fatalf("done %v, errs %v", done, errs)
	}
	plugin, err := os.ReadFile(filepath.Join(oc, "plugins", cliPluginFile))
	if err != nil || !strings.Contains(string(plugin), `const INSTALLED = "/n/cc-hook.sock"`) || !strings.Contains(string(plugin), `export default { id: "superleo-activity"`) {
		t.Fatalf("opencode plugin: %v", err)
	}
	ext, err := os.ReadFile(filepath.Join(pi, "extensions", cliPluginFile))
	if err != nil || !strings.Contains(string(ext), `const INSTALLED = "/n/cc-hook.sock"`) || !strings.Contains(string(ext), "export default function superleoActivity") {
		t.Fatalf("pi extension: %v", err)
	}
	for p, s := range others {
		if p == filepath.Join(cx, "hooks.json") {
			continue
		}
		if b, _ := os.ReadFile(p); string(b) != s {
			t.Errorf("%s changed", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(cx, "hooks.json.superleo.bak")); string(b) != codexHooksJSON {
		t.Error("the old hooks.json was not kept")
	}
	// Run again: nothing changes, nothing is kept again.
	before := snapshotDir(t, root)
	if done, errs := installCLIHooks(tg); len(done) != 0 || len(errs) != 0 {
		t.Fatalf("again: %v %v", done, errs)
	}
	if after := snapshotDir(t, root); after != before {
		t.Fatal("a second run changed files")
	}
	// A newer build's plugin replaces the old one, and keeps it.
	write(filepath.Join(oc, "plugins", cliPluginFile), "// an older superleo-activity\n")
	if done, _ := installCLIHooks(tg); len(done) != 1 || !strings.HasPrefix(done[0], "opencode plugin") {
		t.Fatalf("upgrade: %v", done)
	}
	if b, _ := os.ReadFile(filepath.Join(oc, "plugins", cliPluginFile+".superleo.bak")); string(b) != "// an older superleo-activity\n" {
		t.Fatal("the older plugin was not kept")
	}
	// A CLI whose directory is not there is left alone.
	if cliHere(filepath.Join(root, "nope"), "no-such-cli-anywhere") {
		t.Fatal("found a CLI that is not here")
	}
}

// snapshotDir is every file under root and its content, as one string.
func snapshotDir(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			c, _ := os.ReadFile(p)
			b.WriteString(p + "\n" + string(c) + "\n")
		}
		return nil
	})
	return b.String()
}
