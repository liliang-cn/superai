package app

// SuperAI's activity hooks for the coding CLIs other than Claude Code that
// have a way in (cli_activity.go):
//
//   - opencode: a plugin file, <config>/plugins/superai-activity.js, which
//     opencode loads from its config directory by itself.
//   - pi: an extension file, <agent dir>/extensions/superai-activity.js,
//     which pi loads by itself.
//   - codex: entries in $CODEX_HOME/hooks.json, Claude Code's hook shape,
//     running superai-cchook --cli codex. Codex runs a new hook only once the
//     person has trusted it (/hooks in codex); until then codex is seen by
//     its processes.
//
// Each install only adds or replaces SuperAI's own file or entries: other
// plugins, extensions, hooks and config are left as they were, and a file
// SuperAI changes is kept beside it first as <name>.superai.bak. Run again,
// it changes nothing. A CLI whose config directory is not there and that is
// not on the PATH is left alone.

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liliang-cn/superai/internal/backend"
)

//go:embed cliplugins/opencode.js cliplugins/pi.js cliplugins/pi_queen.js
var cliPluginFS embed.FS

// cliPluginFile is the file name SuperAI's plugin goes by in a CLI's
// plugin directory.
const cliPluginFile = "superai-activity.js"

// cliSocketMark is the placeholder an installed plugin has the node's own
// hook socket in place of.
const cliSocketMark = "__SUPERAI_SOCKET__"

// cliPluginSource is a plugin with this node's socket written in.
func cliPluginSource(name, socket string) ([]byte, error) {
	b, err := cliPluginFS.ReadFile("cliplugins/" + name)
	if err != nil {
		return nil, err
	}
	if socket == "" {
		return b, nil
	}
	q, _ := json.Marshal(socket)
	return bytes.Replace(b, []byte(`"`+cliSocketMark+`"`), q, 1), nil
}

// writeKept writes content to path when it differs from what is there,
// keeping the old file beside it first. It says whether it wrote.
func writeKept(path string, content []byte) (bool, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o644)
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(old, content) {
			return false, nil
		}
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(path+".superai.bak", old, mode); err != nil {
			return false, fmt.Errorf("keeping the old %s: %w", filepath.Base(path), err)
		}
	case errors.Is(err, os.ErrNotExist):
		old = nil
	default:
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".superai.tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// cliHere says whether a CLI is set up on this machine: its config
// directory is there, or its binary is on the PATH.
func cliHere(dir, bin string) bool {
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return true
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// opencodeConfigDir is opencode's global config directory.
func opencodeConfigDir() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(expandTilde(x), "opencode")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".config", "opencode")
}

// piAgentDir is pi's agent directory.
func piAgentDir() string {
	if d := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); d != "" {
		return expandTilde(d)
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".pi", "agent")
}

// codexHome is codex's home directory.
func codexHome() string {
	if d := strings.TrimSpace(os.Getenv("CODEX_HOME")); d != "" {
		return expandTilde(d)
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".codex")
}

// installOpencodePlugin puts SuperAI's plugin in opencode's config dir.
func installOpencodePlugin(dir, socket string) (bool, error) {
	src, err := cliPluginSource("opencode.js", socket)
	if err != nil {
		return false, err
	}
	return writeKept(filepath.Join(dir, "plugins", cliPluginFile), src)
}

// installPiExtension puts SuperAI's extension in pi's agent dir.
func installPiExtension(dir, socket string) (bool, error) {
	src, err := cliPluginSource("pi.js", socket)
	if err != nil {
		return false, err
	}
	return writeKept(filepath.Join(dir, "extensions", cliPluginFile), src)
}

// codexHookEvents are the codex hooks SuperAI sets: every one that says
// where a session is in its turn.
var codexHookEvents = []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "Interrupt"}

// codexHookRE finds SuperAI's codex hook for an event in a command.
var codexHookRE = regexp.MustCompile(`(?:superai-cchook|superai['"]?\s+hook)['"]?\s+--cli\s+codex\s+([A-Za-z]+)`)

func isOurCodexHook(command, event string) bool {
	for _, m := range codexHookRE.FindAllStringSubmatch(command, -1) {
		if m[1] == event {
			return true
		}
	}
	return false
}

// codexHookCommand is the command for one event: superai-cchook when there
// is one beside bin, else superai itself.
func codexHookCommand(bin, slim, event string) string {
	if slim != "" {
		return hookQuote(slim) + " --cli codex " + event
	}
	return hookQuote(bin) + " hook --cli codex " + event
}

// mergeCodexHooks adds SuperAI's codex hooks missing from a hooks.json's
// content, each as a new group at the end of its event's list: codex knows
// a hook's trust by its place, so the groups already there keep theirs.
// Everything else in the file keeps its value and order.
func mergeCodexHooks(content []byte, bin, slim string) ([]byte, []string, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		content = []byte("{}")
	}
	top, err := readObject(content)
	if err != nil {
		return nil, nil, fmt.Errorf("hooks.json is not a JSON object: %w", err)
	}
	hi := -1
	var events []jsonField
	for i, f := range top {
		if f.Key == "hooks" {
			hi = i
			if string(bytes.TrimSpace(f.Val)) != "null" {
				if events, err = readObject(f.Val); err != nil {
					return nil, nil, fmt.Errorf("its hooks are not a JSON object: %w", err)
				}
			}
		}
	}
	var added []string
	for _, ev := range codexHookEvents {
		ei := -1
		var groups []json.RawMessage
		for i, f := range events {
			if f.Key == ev {
				ei = i
				if string(bytes.TrimSpace(f.Val)) != "null" {
					if err := json.Unmarshal(f.Val, &groups); err != nil {
						return nil, nil, fmt.Errorf("its %s hooks are not a list: %w", ev, err)
					}
				}
			}
		}
		if _, _, ok := findOurHook(groups, ev, isOurCodexHook); ok {
			continue
		}
		groups = append(groups, marshalPlain(ccHookGroup{Hooks: []ccHookCmd{{Type: "command", Command: codexHookCommand(bin, slim, ev), Timeout: 2}}}))
		parts := make([][]byte, len(groups))
		for i, g := range groups {
			parts[i] = g
		}
		val := append([]byte{'['}, bytes.Join(parts, []byte{','})...)
		val = append(val, ']')
		if ei < 0 {
			events = append(events, jsonField{Key: ev, Val: val})
		} else {
			events[ei].Val = val
		}
		added = append(added, ev)
	}
	if len(added) == 0 {
		return content, nil, nil
	}
	hooks := writeObject(events)
	if hi < 0 {
		top = append(top, jsonField{Key: "hooks", Val: hooks})
	} else {
		top[hi].Val = hooks
	}
	var out bytes.Buffer
	if err := json.Indent(&out, writeObject(top), "", "  "); err != nil {
		return nil, nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), added, nil
}

// installCodexHooks merges SuperAI's hooks into codex's hooks.json.
func installCodexHooks(home, bin, slim string) ([]string, error) {
	path := filepath.Join(home, "hooks.json")
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	next, added, err := mergeCodexHooks(old, bin, slim)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(added) == 0 {
		return nil, nil
	}
	if _, err := writeKept(path, next); err != nil {
		return nil, err
	}
	return added, nil
}

// cliHookTargets are where the CLI hooks go; the tests point them at a
// temp dir.
type cliHookTargets struct {
	Opencode, Pi, Codex string // config dirs; "" skips one
	Bin, Slim           string // superai and superai-cchook
	Socket              string // this node's hook socket
}

// defaultCLIHookTargets are this machine's CLIs that are set up here.
func defaultCLIHookTargets(bin string) cliHookTargets {
	t := cliHookTargets{Bin: bin, Slim: slimHookBeside(bin), Socket: filepath.Join(backend.DataDir(), ccHookSocketName)}
	if d := opencodeConfigDir(); d != "" && cliHere(d, "opencode") {
		t.Opencode = d
	}
	if d := piAgentDir(); d != "" && cliHere(d, "pi") {
		t.Pi = d
	}
	if d := codexHome(); d != "" && cliHere(d, "codex") && bin != "" {
		t.Codex = d
	}
	return t
}

// installCLIHooks installs every CLI hook t names and says what changed.
func installCLIHooks(t cliHookTargets) ([]string, []error) {
	var done []string
	var errs []error
	if t.Opencode != "" {
		if ok, err := installOpencodePlugin(t.Opencode, t.Socket); err != nil {
			errs = append(errs, fmt.Errorf("opencode: %w", err))
		} else if ok {
			done = append(done, "opencode plugin in "+filepath.Join(t.Opencode, "plugins"))
		}
	}
	if t.Pi != "" {
		if ok, err := installPiExtension(t.Pi, t.Socket); err != nil {
			errs = append(errs, fmt.Errorf("pi: %w", err))
		} else if ok {
			done = append(done, "pi extension in "+filepath.Join(t.Pi, "extensions"))
		}
	}
	if t.Codex != "" {
		if added, err := installCodexHooks(t.Codex, t.Bin, t.Slim); err != nil {
			errs = append(errs, fmt.Errorf("codex: %w", err))
		} else if len(added) > 0 {
			done = append(done, "codex hooks "+strings.Join(added, ", ")+" in "+filepath.Join(t.Codex, "hooks.json")+" (trust them once with /hooks in codex)")
		}
	}
	return done, errs
}

// autoInstallCLIHooks installs or upgrades the CLI hooks when a node
// starts. Off with SUPERAI_HOOK_AUTOINSTALL=0.
func autoInstallCLIHooks() {
	if os.Getenv("SUPERAI_HOOK_AUTOINSTALL") == "0" {
		return
	}
	bin := hookBinary()
	if bin == "" {
		return // a test binary or `go run`: not something to leave in a config
	}
	done, errs := installCLIHooks(defaultCLIHookTargets(bin))
	for _, err := range errs {
		log.Printf("cli activity: %v", err)
	}
	if len(done) > 0 {
		log.Printf("cli activity: set %s (new sessions pick them up)", strings.Join(done, "; "))
	}
}
