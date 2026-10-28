package app

// `superleo hook install`: put SuperLeo's Claude Code hooks into the person's
// Claude Code settings, beside whatever else is there.
//
// The settings file is shared: other tools keep their own hooks in it (on
// the Mac: anywhered, Otty, an iTerm2 status script), and the person keeps
// everything else. So the install only ever adds: one entry per event that
// has no `superleo hook <Event>` yet, at the end of that event's list, with
// every other byte of the file's values left as it was and the keys in their
// order. Run again it finds its entries and changes nothing. The file before
// a change is kept beside it as settings.json.superleo.bak.
//
// A running node also upgrades the hooks on start (autoUpgradeCCHooks), but
// only in a settings file that already has a SuperLeo hook from the same
// binary: the person opted in once, and a newer build adding the events it
// has learned to read is that same choice. A file without one is left alone.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ccHookSpec is one hook SuperLeo wants in Claude Code's settings.
type ccHookSpec struct {
	Event   string
	Matcher string // "" for none
	Args    string // after `hook <Event>`
	Timeout int    // seconds
	// Slim is an event that fires on every prompt or tool call: it runs
	// superleo-cchook, the small binary beside superleo, when there is one —
	// starting all of SuperLeo for it cost each tool call 50 ms or more.
	Slim bool
}

// ccHookSpecs are SuperLeo's hooks. The activity-only ones get a short
// timeout: they return in milliseconds, and Claude Code waits on them.
var ccHookSpecs = []ccHookSpec{
	{Event: "SessionStart", Timeout: 5},
	{Event: "SessionEnd", Timeout: 5},
	{Event: "Stop", Timeout: 5},
	{Event: "StopFailure", Timeout: 5},
	{Event: "Notification", Matcher: "permission_prompt|idle_prompt", Timeout: 5},
	{Event: "PermissionRequest", Args: "--wait 100", Timeout: 120},
	{Event: "UserPromptSubmit", Timeout: 2, Slim: true},
	{Event: "PreToolUse", Matcher: "*", Timeout: 2, Slim: true},
	{Event: "PostToolUse", Matcher: "*", Timeout: 2, Slim: true},
}

// ccSlimHookName is the small hook binary's name, beside superleo
// (cmd/superleo-cchook).
const ccSlimHookName = "superleo-cchook"

// slimHookRE finds `superleo-cchook <Event>` in a hook command — or
// `superleo-cchook`, the name it was installed under before the rename, so an
// old entry is replaced rather than left beside the new one.
var slimHookRE = regexp.MustCompile(`(?:^|[/\s'"])super(?:leo|ai)-cchook['"]?\s+([A-Za-z]+)`)

// isOurSlimHook says whether a command is superleo-cchook for event.
func isOurSlimHook(command, event string) bool {
	for _, m := range slimHookRE.FindAllStringSubmatch(command, -1) {
		if m[1] == event {
			return true
		}
	}
	return false
}

// slimHookBeside is superleo-cchook next to bin, or "" when it is not there.
func slimHookBeside(bin string) string {
	if bin == "" {
		return ""
	}
	p := filepath.Join(filepath.Dir(bin), ccSlimHookName)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
		return p
	}
	return ""
}

// ourHookRE finds `superleo hook <Event>` in a hook command, whatever the
// path to the binary and however it is quoted; `superleo hook` too, as it
// was installed before the rename.
var ourHookRE = regexp.MustCompile(`(?:^|[/\s'"])super(?:leo|ai)['"]?\s+hook\s+([A-Za-z]+)`)

// isOurHook says whether a command is SuperLeo's hook for event.
func isOurHook(command, event string) bool {
	for _, m := range ourHookRE.FindAllStringSubmatch(command, -1) {
		if m[1] == event {
			return true
		}
	}
	return false
}

// hookQuote quotes a path for the command line Claude Code runs through a
// shell, when it needs it.
func hookQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[](){}<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ccHookCommand is the command line for one spec: superleo-cchook for a
// slim event when slim names it, else `superleo hook`.
func ccHookCommand(bin, slim string, sp ccHookSpec) string {
	if sp.Slim && slim != "" {
		return hookQuote(slim) + " " + sp.Event
	}
	cmd := hookQuote(bin) + " hook " + sp.Event
	if sp.Args != "" {
		cmd += " " + sp.Args
	}
	return cmd
}

// claudeSettingsPath is Claude Code's user settings: $CLAUDE_CONFIG_DIR, as
// Claude Code itself reads it, else ~/.claude.
func claudeSettingsPath() string {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return filepath.Join(expandTilde(d), "settings.json")
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".claude", "settings.json")
}

func expandTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// jsonField is one key of a JSON object, its value untouched.
type jsonField struct {
	Key string
	Val json.RawMessage
}

// readObject reads a JSON object's fields in order.
func readObject(b []byte) ([]jsonField, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var out []jsonField
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, jsonField{Key: k, Val: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

// writeObject is the fields back as one JSON object, compact.
func writeObject(fields []jsonField) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(marshalPlain(f.Key))
		b.WriteByte(':')
		b.Write(f.Val)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// marshalPlain is json.Marshal without turning & < > into \u escapes:
// commands like `2>&1` stay readable in a file people edit by hand.
func marshalPlain(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// ccHookGroup is a hook group as SuperLeo writes one, its keys in the order
// Claude Code's own docs show them.
type ccHookGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []ccHookCmd `json:"hooks"`
}

type ccHookCmd struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// groupsHaveOurHook says whether an event's groups already run SuperLeo's
// hook for it.
func groupsHaveOurHook(groups []json.RawMessage, event string) bool {
	_, _, found := findOurHook(groups, event, isOurHook)
	return found
}

// findOurHook finds the first group with a command is accepts for event, and
// that command.
func findOurHook(groups []json.RawMessage, event string, is func(cmd, event string) bool) (int, string, bool) {
	for i, g := range groups {
		var gr struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(g, &gr) != nil {
			continue
		}
		for _, h := range gr.Hooks {
			if is(h.Command, event) {
				return i, h.Command, true
			}
		}
	}
	return -1, "", false
}

// mergeCCHooks adds the specs missing from a settings file's content and
// returns the new content and what changed, by event; the content is
// unchanged when nothing did. With slim (superleo-cchook's path) a slim
// event's `superleo hook` entry, as an older install wrote it, is moved to
// slim in place: only that command string changes, nothing else in its
// group or the file.
func mergeCCHooks(content []byte, bin, slim string, specs []ccHookSpec) ([]byte, []string, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		content = []byte("{}")
	}
	top, err := readObject(content)
	if err != nil {
		return nil, nil, fmt.Errorf("the settings file is not a JSON object: %w", err)
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
	for _, sp := range specs {
		ei := -1
		var groups []json.RawMessage
		for i, f := range events {
			if f.Key == sp.Event {
				ei = i
				if string(bytes.TrimSpace(f.Val)) != "null" {
					if err := json.Unmarshal(f.Val, &groups); err != nil {
						return nil, nil, fmt.Errorf("its %s hooks are not a list: %w", sp.Event, err)
					}
				}
			}
		}
		change := sp.Event
		if _, _, ok := findOurHook(groups, sp.Event, isOurSlimHook); ok {
			continue // superleo-cchook already, whichever binary is here now
		}
		if gi, cmd, ok := findOurHook(groups, sp.Event, isOurHook); ok {
			if !sp.Slim || slim == "" {
				continue
			}
			moved := bytes.Replace(groups[gi], marshalPlain(cmd), marshalPlain(ccHookCommand(bin, slim, sp)), 1)
			if bytes.Equal(moved, groups[gi]) {
				continue // written some other way than ours; left as it is
			}
			groups[gi] = moved
			change = sp.Event + " (now " + ccSlimHookName + ")"
		} else {
			groups = append(groups, marshalPlain(ccHookGroup{Matcher: sp.Matcher, Hooks: []ccHookCmd{{Type: "command", Command: ccHookCommand(bin, slim, sp), Timeout: sp.Timeout}}}))
		}
		parts := make([][]byte, len(groups))
		for i, g := range groups {
			parts[i] = g
		}
		val := append([]byte{'['}, bytes.Join(parts, []byte{','})...)
		val = append(val, ']')
		if ei < 0 {
			events = append(events, jsonField{Key: sp.Event, Val: val})
		} else {
			events[ei].Val = val
		}
		added = append(added, change)
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

// installCCHooks merges SuperLeo's hooks into the settings file at path,
// creating it when there is none. It writes only when something was missing,
// keeping the file as it was beside it first.
func installCCHooks(path, bin string) ([]string, error) {
	return installCCHooksWith(path, bin, slimHookBeside(bin))
}

// installCCHooksWith is installCCHooks with superleo-cchook named (or "").
func installCCHooksWith(path, bin, slim string) ([]string, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // a settings file kept in a dotfiles repo stays a link
	}
	mode := os.FileMode(0o644)
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
	case errors.Is(err, os.ErrNotExist):
		old = nil
	default:
		return nil, err
	}
	next, added, err := mergeCCHooks(old, bin, slim, ccHookSpecs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(added) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if old != nil {
		if err := os.WriteFile(path+".superleo.bak", old, mode); err != nil {
			return nil, fmt.Errorf("keeping the old settings: %w", err)
		}
	}
	tmp := path + ".superleo.tmp"
	if err := os.WriteFile(tmp, next, mode); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return added, nil
}

// settingsHaveOurHook says whether a settings file runs any SuperLeo hook
// with bin.
func settingsHaveOurHook(content []byte, bin string) bool {
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(content, &s) != nil {
		return false
	}
	for event, groups := range s.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if isOurHook(h.Command, event) && sameFile(hookCommandBinary(h.Command), bin) {
					return true
				}
			}
		}
	}
	return false
}

// hookCommandBinary is the binary a SuperLeo hook command runs: what comes
// before ` hook <Event>`, unquoted.
func hookCommandBinary(command string) string {
	loc := regexp.MustCompile(`['"]?\s+hook\s+[A-Za-z]+`).FindStringIndex(command)
	if loc == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(command[:loc[0]]), `'"`)
}

// sameFile says whether two paths are one file, links followed: /tmp is
// /private/tmp on a Mac.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// hookBinary is this binary's real path, or "" for one that would make a
// poor hook: a test binary, or `go run`'s throwaway build.
func hookBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	base := filepath.Base(exe)
	if strings.HasSuffix(base, ".test") || strings.Contains(exe, "go-build") {
		return ""
	}
	return exe
}

// autoUpgradeCCHooks adds the hooks this build reads and the settings lack,
// in a settings file that already runs this binary's hooks. Off with
// SUPERLEO_HOOK_AUTOINSTALL=0.
func autoUpgradeCCHooks() {
	if os.Getenv("SUPERLEO_HOOK_AUTOINSTALL") == "0" {
		return
	}
	bin, path := hookBinary(), claudeSettingsPath()
	if bin == "" || path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil || !settingsHaveOurHook(b, bin) {
		return
	}
	added, err := installCCHooks(path, bin)
	switch {
	case err != nil:
		log.Printf("cc relay: upgrading the Claude Code hooks in %s: %v", path, err)
	case len(added) > 0:
		log.Printf("cc relay: Claude Code hooks %s set in %s (new sessions pick them up)", strings.Join(added, ", "), path)
	}
}

// HookInstallMain is `superleo hook install [-settings path] [-bin path]
// [-cchook path]`.
func HookInstallMain(argv []string) {
	fl := flag.NewFlagSet("hook install", flag.ExitOnError)
	settings := fl.String("settings", claudeSettingsPath(), "Claude Code's settings file")
	bin := fl.String("bin", hookBinary(), "the superleo binary the hooks run")
	slim := fl.String("cchook", "", "superleo-cchook, for the prompt and tool events; default: the one beside -bin, if there is one")
	_ = fl.Parse(argv)
	if *slim == "" {
		*slim = slimHookBeside(*bin)
	}
	if strings.TrimSpace(*bin) == "" {
		fmt.Fprintln(os.Stderr, "superleo hook install: say which binary the hooks run with -bin")
		os.Exit(2)
	}
	if strings.TrimSpace(*settings) == "" {
		fmt.Fprintln(os.Stderr, "superleo hook install: no home directory; say where the settings are with -settings")
		os.Exit(2)
	}
	added, err := installCCHooksWith(*settings, *bin, *slim)
	if err != nil {
		fmt.Fprintln(os.Stderr, "superleo hook install:", err)
		os.Exit(1)
	}
	if len(added) == 0 {
		fmt.Printf("%s already has every SuperLeo hook\n", *settings)
	} else {
		fmt.Printf("set %s in %s; Claude Code sessions started from now on use them\n", strings.Join(added, ", "), *settings)
	}
	// The other CLIs that have a way in: opencode, pi, codex.
	t := defaultCLIHookTargets(*bin)
	t.Slim = *slim
	done, errs := installCLIHooks(t)
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, "superleo hook install:", err)
	}
	for _, d := range done {
		fmt.Printf("set %s\n", d)
	}
	if len(errs) > 0 {
		os.Exit(1)
	}
}
