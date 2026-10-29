package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Coding tools a person adds themselves.
//
// The CLIs SuperLeo knows by name (claude, codex, cursor-agent, and the rest
// agentexec has a dialect for) are found on PATH and driven with flags and
// JSON this app knows. Anything else is a CodingTool: a name and the command
// line that asks it one thing, with {prompt} where the task goes. It runs
// through the same run manager as the known ones — same roots, timeout,
// approval and Stop — and its output is taken as plain text.
//
// The command is a template, split into argv once and filled in per run. The
// task is substituted into one argument after the split, never into a string
// a shell reads, so nothing a prompt contains can become another argument or
// another command.

// CodingTool is one user-added coding tool.
type CodingTool struct {
	// ID is what it is called by: "@id", "id.machine". Lowercase letters,
	// digits, - and _; fixed once made.
	ID string `json:"id"`
	// Name is what it is shown as.
	Name string `json:"name"`
	// Command is the template as typed: words split on spaces, quotes keep
	// words together, {prompt} marks the argument the task goes into and
	// {model} the one the model goes into.
	Command string `json:"command"`
	// Model, when set, fills {model}. Unset drops the argument holding
	// {model}, and the flag in front of it.
	Model string `json:"model,omitempty"`
	// Preset is the catalogue entry it was made from, if any.
	Preset string `json:"preset,omitempty"`
}

// CodingToolPreset is a well-known coding CLI the add menu offers.
type CodingToolPreset struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Binary is the command it is found by.
	Binary string `json:"binary"`
	// Command is the template a command preset is added with. Empty for a
	// CLI agentexec drives itself, whose flags and output it already knows:
	// adding one of those means offering it, not writing a command.
	Command string `json:"command,omitempty"`
}

// CodingToolPresets are the coding CLIs the add menu knows. The headless
// flags were read off each CLI's own --help (gemini 0.x, kimi, goose,
// opencode, copilot) or its documentation (qwen, aider); agentexec carries
// the same ones for the CLIs it drives.
var CodingToolPresets = []CodingToolPreset{
	// Retired as a built-in (app/cli_discover.go): offered as a command.
	// -p is headless mode, --yolo approves every tool call.
	{ID: "gemini", Label: "Gemini CLI", Binary: "gemini", Command: "gemini -m {model} -p {prompt} --yolo"},
	{ID: "opencode", Label: "opencode", Binary: "opencode"}, // opencode run --format json … <prompt>
	{ID: "kimi", Label: "Kimi CLI", Binary: "kimi"},         // kimi --print --output-format stream-json --prompt <prompt>
	{ID: "goose", Label: "Goose", Binary: "goose"},          // goose run --output-format stream-json --text <prompt>
	{ID: "qwen", Label: "Qwen Code", Binary: "qwen"},        // qwen --output-format stream-json --yolo -p <prompt>
	{ID: "aider", Label: "Aider", Binary: "aider"},          // aider --message <prompt> --yes-always
	{ID: "copilot", Label: "GitHub Copilot CLI", Binary: "copilot"},
	{ID: "pi", Label: "pi", Binary: "pi"},
	{ID: "hermes", Label: "Hermes", Binary: "hermes"},
	{ID: "agy", Label: "Antigravity", Binary: "agy"},
}

// BuiltinCodingTools are the CLIs always listed, installed or not.
var BuiltinCodingTools = []string{"claude", "codex", "cursor-agent"}

// drivenCodingCLIs are the names agentexec has a dialect for. A user-added
// tool may not take one, or "@kimi" would mean two different things.
// gemini is missing on purpose: retired as a built-in, it is a command now.
var drivenCodingCLIs = map[string]bool{
	"claude": true, "codex": true, "cursor-agent": true, "qwen": true, "kimi": true,
	"opencode": true, "copilot": true, "goose": true, "pi": true, "agy": true,
	"hermes": true, "aider": true,
}

// IsDrivenCodingCLI reports whether a name is one agentexec drives itself.
func IsDrivenCodingCLI(name string) bool { return drivenCodingCLIs[name] }

// PresetByID finds a catalogue entry.
func PresetByID(id string) (CodingToolPreset, bool) {
	for _, p := range CodingToolPresets {
		if p.ID == id {
			return p, true
		}
	}
	return CodingToolPreset{}, false
}

var codingToolID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ValidCodingToolID says what is wrong with an id, or nil.
func ValidCodingToolID(id string) error {
	if !codingToolID.MatchString(id) {
		return errors.New("a tool's id is lowercase letters, digits, - and _ (up to 32)")
	}
	if drivenCodingCLIs[id] {
		return fmt.Errorf("%s is already a known coding tool", id)
	}
	return nil
}

// CodingToolSlug turns a name into an id: "Gemini CLI" → "gemini-cli".
func CodingToolSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	if s == "" {
		s = "tool"
	}
	return s
}

// SplitCommand splits a command line into words the way the settings page
// does (AddWorkerCard's splitCommand): on white space, with single or double
// quotes keeping a word together. Nothing is expanded — no variables, no
// globs, no ~ — because no shell ever reads it.
func SplitCommand(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	has := false
	for _, ch := range line {
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			} else {
				cur.WriteRune(ch)
			}
		case ch == '"' || ch == '\'':
			quote, has = ch, true
		case unicode.IsSpace(ch):
			if has || cur.Len() > 0 {
				out = append(out, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteRune(ch)
		}
	}
	if quote != 0 {
		return nil, errors.New("a quote in the command is not closed")
	}
	if has || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

// Template is the command split into argv, with {prompt} placed: appended as
// the last argument when the command does not say where it goes.
func (t CodingTool) Template() ([]string, error) {
	argv, err := SplitCommand(t.Command)
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, errors.New("the command is empty")
	}
	if strings.Contains(argv[0], "{prompt}") || strings.Contains(argv[0], "{model}") {
		return nil, errors.New("the command starts with the program to run, not {prompt}")
	}
	n := 0
	for _, a := range argv {
		if strings.Contains(a, "{prompt}") {
			n++
		}
	}
	switch {
	case n > 1:
		return nil, errors.New("{prompt} may appear once")
	case n == 0:
		argv = append(argv, "{prompt}")
	}
	return argv, nil
}

// Argv is the command for one run: the template with the model and the task
// filled in, each into the one argument that holds its placeholder.
func (t CodingTool) Argv(prompt, model string) ([]string, error) {
	tmpl, err := t.Template()
	if err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = strings.TrimSpace(t.Model)
	}
	out := make([]string, 0, len(tmpl))
	for i := 0; i < len(tmpl); i++ {
		a := tmpl[i]
		if strings.Contains(a, "{model}") {
			if model == "" {
				// "-m {model}" goes as a pair; "--model={model}" alone.
				if a == "{model}" && len(out) > 0 && strings.HasPrefix(out[len(out)-1], "-") {
					out = out[:len(out)-1]
				}
				continue
			}
			a = strings.ReplaceAll(a, "{model}", model)
		}
		if strings.Contains(a, "{prompt}") {
			a = strings.ReplaceAll(a, "{prompt}", prompt)
		}
		out = append(out, a)
	}
	return out, nil
}

// Program is the command's first word: what has to be installed.
func (t CodingTool) Program() string {
	argv, err := SplitCommand(t.Command)
	if err != nil || len(argv) == 0 {
		return ""
	}
	return argv[0]
}

// userBinDirs are where coding CLIs install themselves outside a GUI app's
// PATH: npm's and pip's user prefixes, Homebrew, bun, cargo, opencode's own.
func userBinDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home != "" {
		for _, d := range []string{".local/bin", ".npm-global/bin", ".bun/bin", ".cargo/bin", ".opencode/bin", "go/bin"} {
			dirs = append(dirs, filepath.Join(home, d))
		}
	}
	return dirs
}

// FindProgram resolves a program to an absolute path: a path as given, a
// bare name on PATH or in the user bin dirs. ok is false when there is
// nothing executable there.
func FindProgram(prog string) (string, bool) {
	prog = expandHome(strings.TrimSpace(prog))
	if prog == "" {
		return "", false
	}
	if p, err := exec.LookPath(prog); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, true
		}
		return p, true
	}
	if strings.ContainsRune(prog, filepath.Separator) {
		return "", false
	}
	for _, d := range userBinDirs() {
		p := filepath.Join(d, prog)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// normalizeCodingTools drops entries that cannot run and duplicates of an id,
// and trims what the page typed.
func normalizeCodingTools(in []CodingTool) []CodingTool {
	var out []CodingTool
	seen := map[string]bool{}
	for _, t := range in {
		t.ID = strings.TrimSpace(t.ID)
		t.Name = strings.TrimSpace(t.Name)
		t.Command = strings.TrimSpace(t.Command)
		t.Model = strings.TrimSpace(t.Model)
		if t.ID == "" || seen[t.ID] || t.Command == "" {
			continue
		}
		seen[t.ID] = true
		if t.Name == "" {
			t.Name = t.ID
		}
		out = append(out, t)
	}
	return out
}

// CustomTool finds a user-added tool by id.
func (e ExternalAgents) CustomTool(id string) (CodingTool, bool) {
	for _, t := range e.Custom {
		if t.ID == id {
			return t, true
		}
	}
	return CodingTool{}, false
}

// codingRefusalRunes bounds what can be read as a refusal: a CLI that could
// not reach its model says so in a line or two, and an answer long enough to
// discuss authentication is an answer.
const codingRefusalRunes = 600

// CodingToolRefusal reads what a plain-text tool printed before exiting 0
// and says whether it was a refusal rather than an answer: not signed in,
// out of quota, an ineligible account. claude does this with a revoked
// token — "Failed to authenticate", exit 0 — and a text tool has no verdict
// field to say otherwise, so the words are all there is. The classification
// is CondenseAgentFailure's; only a short output is judged.
func CodingToolRefusal(output string) (string, bool) {
	out := strings.TrimSpace(output)
	if out == "" || len([]rune(out)) > codingRefusalRunes {
		return "", false
	}
	low := strings.ToLower(out)
	signIn := strings.Contains(low, "failed to authenticate") || strings.Contains(low, "not logged in") ||
		strings.Contains(low, "not authenticated") || strings.Contains(low, "please log in") ||
		strings.Contains(low, "please login") || strings.Contains(low, "invalid api key") ||
		strings.Contains(low, "unauthorized") || strings.Contains(low, "401")
	reason := CondenseAgentFailure(out)
	switch {
	case strings.HasPrefix(reason, "rate-limited"), strings.HasPrefix(reason, "this account is not eligible"):
		return reason, true
	case signIn:
		if !strings.HasPrefix(reason, "not signed in") {
			reason = "not signed in — " + reason
		}
		return reason, true
	}
	return "", false
}
