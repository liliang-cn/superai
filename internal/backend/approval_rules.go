package backend

// Remembered answers: "以后同类请求不用问我".
//
// YOLO is all or nothing, and per-request approval is one tap per call. The
// thing in between is a rule the user makes from a card they are answering:
// "Claude on mac may write under ~/Things/dev/apps/superai", "the queen may run
// `kubectl -n superai rollout restart …`". A rule is checked before a card is
// raised; an allow rule answers yes, a deny rule answers no, both audited under
// their own name so "who said yes to that?" still has an answer.
//
// Rules are only ever made narrow. The card offers candidates derived from the
// call being asked about, narrowest first, and the rule saved is exactly the
// one picked: the same tool kind, on the same machine, from the same asker,
// plus the target constraint the scope names. A rule never says more than the
// label the user read.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DecidedByApprovalRule marks an audit entry answered by a remembered rule.
const DecidedByApprovalRule = "approval-rule"

// Remember scopes: how far a remembered answer reaches.
const (
	// RememberSameTool: this kind of call, from this asker on this machine,
	// whatever it targets.
	RememberSameTool = "same_tool"
	// RememberSameToolAndTarget: this kind of call on this target — files
	// under the same project directory, the exact same shell command, the
	// exact same MCP tool.
	RememberSameToolAndTarget = "same_tool_and_target"
	// RememberSameCommandPrefix: shell commands starting with the same fixed
	// words, up to the first argument that looks variable.
	RememberSameCommandPrefix = "same_command_prefix"
)

// Rule decisions.
const (
	RuleAllow = "allow"
	RuleDeny  = "deny"
)

// LocalMachine is the machine constraint of a rule made from a call on this
// SuperAI itself. Empty would mean "any machine", which is broader than what
// the user was looking at.
const LocalMachine = "@local"

// Tool categories a rule's Tool may name instead of an exact tool.
const (
	KindShell     = "shell"
	KindWriteFile = "write_file"
)

// ApprovalRule is one remembered answer.
type ApprovalRule struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// Tool is an exact tool name ("cli_agent_run"), a category ("shell",
	// "write_file"), or an MCP pattern ("mcp:noted:update_task",
	// "mcp:noted:*").
	Tool     string    `json:"tool"`
	Scope    RuleScope `json:"scope,omitzero"`
	Decision string    `json:"decision"`
	Note     string    `json:"note,omitempty"`
	// CreatedFromApprovalID is the card the rule was made from, if any.
	CreatedFromApprovalID string `json:"created_from_approval_id,omitempty"`
	// Label is the rule in words, as the user saw it before saving.
	Label string `json:"label"`
}

// RuleScope narrows a rule. Every non-empty field must hold for it to match.
type RuleScope struct {
	// PathPrefix: the call's target file is this directory or under it.
	PathPrefix string `json:"path_prefix,omitempty"`
	// Path: the call's target file is exactly this.
	Path string `json:"path,omitempty"`
	// CommandPrefix: the shell command is a single simple command (no ;, &&,
	// |, redirections, substitutions) whose words start with these words.
	CommandPrefix string `json:"command_prefix,omitempty"`
	// Command: the shell command is exactly this.
	Command string `json:"command,omitempty"`
	// Machine: where the call runs — a remote node or hive worker by name,
	// LocalMachine for this SuperAI. Empty is any machine.
	Machine string `json:"machine,omitempty"`
	// Agent: who asks — claude, codex, a standing agent, "queen". Empty is
	// anyone.
	Agent string `json:"agent,omitempty"`
}

// RuleSuggestion is one candidate rule offered on an approval card.
type RuleSuggestion struct {
	Scope string `json:"scope"`
	// Label is what the card shows, e.g. "以后 claude 在 mac 上写入
	// ~/Things/dev/apps/superai 下的文件都不用问".
	Label string `json:"label"`
	// Rule is what would be saved if this one is picked with 同意.
	Rule ApprovalRule `json:"rule"`
}

// RememberOption is the "remember this" part of an answer.
type RememberOption struct {
	Scope string `json:"scope"`
	Note  string `json:"note,omitempty"`
}

// approvalSubject is an approval request reduced to what rules talk about.
type approvalSubject struct {
	Tool    string // as asked, e.g. "claude.mac · Bash"
	Bare    string // without the asker prefix, e.g. "Bash"
	Kind    string // "shell", "write_file", "mcp:noted:update_task", or the lowercased bare name
	Command string // shell command, for shell kinds
	Path    string // target file, for write kinds
	Machine string // LocalMachine, or a node / worker name
	Agent   string // claude, codex, a standing agent, "queen", or "" when not known
	Workdir string // the run's working directory, when known
}

func subjectOf(req ApprovalRequest) approvalSubject {
	s := approvalSubject{Tool: strings.TrimSpace(req.Tool), Workdir: strings.TrimSpace(req.Workdir)}
	s.Bare = s.Tool
	prefix := ""
	if p, bare, ok := strings.Cut(s.Tool, " · "); ok {
		prefix, s.Bare = strings.TrimSpace(p), strings.TrimSpace(bare)
	}
	s.Machine = strings.TrimSpace(req.Machine)
	switch {
	case prefix != "":
		// A coding-agent run: "claude" here, "claude.mac" when relayed from a
		// remote node.
		name, host, _ := strings.Cut(prefix, ".")
		s.Agent = name
		if s.Machine == "" {
			s.Machine = host
		}
	case req.Gated:
		// SuperAI's own tool call: a standing agent by its name, else the queen.
		s.Agent = strings.TrimSpace(req.By)
		if s.Agent == "" {
			s.Agent = "queen"
		}
	case req.By != "" && s.Machine == "":
		// A hive worker's held call, relayed by the queen: By is the worker.
		s.Machine = strings.TrimSpace(req.By)
	default:
		s.Agent = strings.TrimSpace(req.By)
	}
	if s.Machine == "" {
		s.Machine = LocalMachine
	}

	s.Kind = kindOf(s.Bare)
	switch s.Kind {
	case KindShell:
		s.Command = strings.TrimSpace(req.Command)
		if s.Command == "" {
			s.Command = strings.TrimSpace(commandOf("bash", req.Args))
		}
	case KindWriteFile:
		s.Path = targetPath(req.Args, req.Workdir)
	}
	return s
}

// kindOf puts a tool into the category rules name it by.
func kindOf(bare string) string {
	if server, tool, ok := mcpParts(bare); ok {
		return "mcp:" + server + ":" + tool
	}
	l := strings.ToLower(strings.TrimSpace(bare))
	for _, frag := range shellFragments {
		if strings.Contains(l, frag) {
			return KindShell
		}
	}
	switch l {
	case "write", "edit", "multiedit", "notebookedit", "fs_write", "fs_edit", "write_file", "edit_file",
		"create_file", "apply_patch", "apply_diff", "str_replace_editor", "str_replace_based_edit_tool":
		return KindWriteFile
	}
	return l
}

// mcpParts splits an MCP tool name: Claude Code's mcp__server__tool, or
// agent-go's mcp_server_tool (where the first underscore after the server is
// taken as the split; matching uses the raw name, so a server with an
// underscore in its name is still matched correctly by an exact rule).
func mcpParts(name string) (server, tool string, ok bool) {
	n := strings.TrimSpace(name)
	if rest, found := strings.CutPrefix(n, "mcp__"); found {
		server, tool, ok = strings.Cut(rest, "__")
		return strings.ToLower(server), strings.ToLower(tool), ok && server != "" && tool != ""
	}
	if rest, found := strings.CutPrefix(n, "mcp_"); found {
		server, tool, ok = strings.Cut(rest, "_")
		return strings.ToLower(server), strings.ToLower(tool), ok && server != "" && tool != ""
	}
	return "", "", false
}

// targetPath is the file a write call targets, absolute when it can be.
func targetPath(args map[string]any, workdir string) string {
	for _, k := range []string{"file_path", "path", "notebook_path", "filename", "file", "target_file"} {
		v, _ := args[k].(string)
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		if !path.IsAbs(v) && strings.TrimSpace(workdir) != "" {
			v = path.Join(workdir, v)
		}
		return path.Clean(v)
	}
	return ""
}

// ── matching ────────────────────────────────────────────────────────────────

// RuleMatches reports whether rule r covers the request.
func RuleMatches(r ApprovalRule, req ApprovalRequest) bool {
	return ruleMatches(r, subjectOf(req))
}

func ruleMatches(r ApprovalRule, s approvalSubject) bool {
	if !toolMatches(r.Tool, s) {
		return false
	}
	sc := r.Scope
	if sc.Machine != "" && !strings.EqualFold(sc.Machine, s.Machine) {
		return false
	}
	if sc.Agent != "" && !strings.EqualFold(sc.Agent, s.Agent) {
		return false
	}
	if sc.Path != "" && (s.Path == "" || path.Clean(sc.Path) != s.Path) {
		return false
	}
	if sc.PathPrefix != "" && !pathWithin(s.Path, sc.PathPrefix) {
		return false
	}
	if sc.Command != "" && strings.TrimSpace(sc.Command) != s.Command {
		return false
	}
	if sc.CommandPrefix != "" && !commandHasPrefix(s.Command, sc.CommandPrefix) {
		return false
	}
	return true
}

func toolMatches(pattern string, s approvalSubject) bool {
	p := strings.ToLower(strings.TrimSpace(pattern))
	if p == "" {
		return false
	}
	if p == s.Kind || strings.EqualFold(p, s.Bare) || strings.EqualFold(p, s.Tool) {
		return true
	}
	if !strings.HasPrefix(p, "mcp:") {
		return false
	}
	parts := strings.SplitN(p, ":", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return false
	}
	server, tool := parts[1], parts[2]
	raw := strings.ToLower(s.Bare)
	if tool == "*" {
		return strings.HasPrefix(s.Kind, "mcp:"+server+":") ||
			strings.HasPrefix(raw, "mcp__"+server+"__") || strings.HasPrefix(raw, "mcp_"+server+"_")
	}
	return raw == "mcp__"+server+"__"+tool || raw == "mcp_"+server+"_"+tool
}

// pathWithin: p is dir, or inside it. Both are cleaned first, so "../" in a
// target cannot walk out of the prefix.
func pathWithin(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	p, dir = path.Clean(p), path.Clean(dir)
	if p == dir {
		return true
	}
	if dir == "/" {
		return strings.HasPrefix(p, "/")
	}
	return strings.HasPrefix(p, dir+"/")
}

// shellControl is anything that lets one command line run more than one
// command, or run something other than the words it starts with. A prefix
// rule never matches a command containing any of it: "kubectl get pods; rm
// -rf ~" starts with "kubectl get" too.
var shellControl = []string{";", "&", "|", "`", "$", ">", "<", "\n", "\r", "(", ")"}

func hasShellControl(cmd string) bool {
	for _, c := range shellControl {
		if strings.Contains(cmd, c) {
			return true
		}
	}
	return false
}

func commandHasPrefix(cmd, prefix string) bool {
	if cmd == "" || hasShellControl(cmd) {
		return false
	}
	words, _, ok := shellWords(cmd)
	want, _, ok2 := shellWords(prefix)
	if !ok || !ok2 || len(want) == 0 || len(words) < len(want) {
		return false
	}
	for i := range want {
		if words[i] != want[i] {
			return false
		}
	}
	return true
}

// shellWords splits a command line the way a POSIX shell would split a simple
// command: whitespace-separated, single and double quotes grouping, backslash
// escaping. quoted[i] says whether word i had any quoting in it. ok is false
// for an unbalanced quote.
func shellWords(s string) (words []string, quoted []bool, ok bool) {
	var cur strings.Builder
	inWord, q, wasQuoted := false, rune(0), false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			quoted = append(quoted, wasQuoted)
		}
		cur.Reset()
		inWord, wasQuoted = false, false
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			} else if c == '\\' && q == '"' && i+1 < len(rs) {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			q, inWord, wasQuoted = c, true, true
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord, wasQuoted = true, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if q != 0 {
		return nil, nil, false
	}
	flush()
	return words, quoted, true
}

// codeRunners are programs whose arguments are themselves a program. A prefix
// rule on any of them ("bash -c …", "sudo …", "python3 …") is a rule for
// everything, so only the exact command is ever offered for them.
var codeRunners = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true,
	"eval": true, "exec": true, "source": true, ".": true, "sudo": true, "su": true, "doas": true,
	"env": true, "xargs": true, "nohup": true, "time": true, "timeout": true, "nice": true, "watch": true,
	"python": true, "python3": true, "node": true, "deno": true, "bun": true, "perl": true, "ruby": true,
	"php": true, "osascript": true, "pwsh": true, "powershell": true, "npx": true, "uvx": true,
	"find": true, "awk": true, "sed": true,
}

var programRe = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)

// variableWord says an argument is the part of a command that changes from
// one call to the next: a path, a file, a URL, a resource with an id in it, a
// quoted message. The prefix stops before the first one.
func variableWord(w string, wasQuoted bool) bool {
	if wasQuoted || w == "" || len([]rune(w)) > 40 {
		return true
	}
	if strings.ContainsAny(w, "/.~=:@$*?[]{}%,\\'\"") {
		return true
	}
	return strings.ContainsAny(w, "0123456789")
}

// CommandPrefix is the fixed part of a shell command: its words up to the
// first argument that looks variable. ok is false when no safe prefix exists:
// the command chains or redirects, runs an interpreter, or the prefix would be
// just the program name or the whole command.
func CommandPrefix(cmd string) (string, bool) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || hasShellControl(cmd) {
		return "", false
	}
	words, quoted, ok := shellWords(cmd)
	if !ok || len(words) < 2 || quoted[0] {
		return "", false
	}
	prog := words[0]
	if !programRe.MatchString(prog) || codeRunners[path.Base(prog)] {
		return "", false
	}
	n := 1
	for n < len(words) && !variableWord(words[n], quoted[n]) {
		n++
	}
	if n < 2 || n == len(words) {
		return "", false
	}
	return strings.Join(words[:n], " "), true
}

// ── suggestions ─────────────────────────────────────────────────────────────

// SuggestApprovalRules is up to three candidate rules for a card, narrowest
// first. None for a call a session rule insists on asking about: no
// remembered answer would ever be consulted for it.
func SuggestApprovalRules(req ApprovalRequest) []RuleSuggestion {
	if req.MustAsk {
		return nil
	}
	s := subjectOf(req)
	if s.Kind == "" {
		return nil
	}
	var scopes []string
	switch {
	case s.Kind == KindShell:
		scopes = []string{RememberSameCommandPrefix, RememberSameToolAndTarget, RememberSameTool}
	case s.Kind == KindWriteFile:
		scopes = []string{RememberSameToolAndTarget, RememberSameTool}
	case strings.HasPrefix(s.Kind, "mcp:"):
		scopes = []string{RememberSameToolAndTarget, RememberSameTool}
	default:
		scopes = []string{RememberSameTool}
	}
	var out []RuleSuggestion
	seen := map[string]bool{}
	for _, sc := range scopes {
		r, err := approvalRuleFor(s, sc, true)
		if err != nil {
			continue
		}
		key := ruleKey(r)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, RuleSuggestion{Scope: sc, Label: r.Label, Rule: r})
		if len(out) == 3 {
			break
		}
	}
	return out
}

// RuleFromApproval is the rule a scope makes from this request — the same one
// SuggestApprovalRules showed under that scope, with the decision given.
func RuleFromApproval(req ApprovalRequest, scope string, allow bool) (ApprovalRule, error) {
	if req.MustAsk {
		return ApprovalRule{}, errors.New("this agent's own rules require asking about this call; a remembered answer would never apply")
	}
	r, err := approvalRuleFor(subjectOf(req), scope, allow)
	if err != nil {
		return r, err
	}
	r.CreatedFromApprovalID = req.ID
	return r, nil
}

func approvalRuleFor(s approvalSubject, scope string, allow bool) (ApprovalRule, error) {
	r := ApprovalRule{Decision: RuleDeny, Scope: RuleScope{Machine: s.Machine, Agent: s.Agent}}
	if allow {
		r.Decision = RuleAllow
	}
	switch s.Kind {
	case KindShell:
		r.Tool = KindShell
		switch scope {
		case RememberSameTool:
		case RememberSameToolAndTarget:
			if s.Command == "" {
				return r, errors.New("this call has no command to remember")
			}
			r.Scope.Command = s.Command
		case RememberSameCommandPrefix:
			p, ok := CommandPrefix(s.Command)
			if !ok {
				return r, errors.New("this command has no fixed prefix that is safe to remember")
			}
			r.Scope.CommandPrefix = p
		default:
			return r, fmt.Errorf("unknown scope %q", scope)
		}
	case KindWriteFile:
		r.Tool = KindWriteFile
		switch scope {
		case RememberSameTool:
		case RememberSameToolAndTarget:
			if s.Path == "" {
				return r, errors.New("this call names no file to remember")
			}
			if dir := projectDir(s.Path, s.Workdir); dir != "" {
				r.Scope.PathPrefix = dir
			} else {
				r.Scope.Path = s.Path
			}
		case RememberSameCommandPrefix:
			return r, errors.New("only shell commands have a command prefix")
		default:
			return r, fmt.Errorf("unknown scope %q", scope)
		}
	default:
		r.Tool = s.Kind
		if strings.HasPrefix(s.Kind, "mcp:") {
			switch scope {
			case RememberSameToolAndTarget:
			case RememberSameTool:
				// The server's tools, all of them.
				parts := strings.SplitN(s.Kind, ":", 3)
				r.Tool = "mcp:" + parts[1] + ":*"
			case RememberSameCommandPrefix:
				return r, errors.New("only shell commands have a command prefix")
			default:
				return r, fmt.Errorf("unknown scope %q", scope)
			}
		} else {
			switch scope {
			case RememberSameTool, RememberSameToolAndTarget:
			case RememberSameCommandPrefix:
				return r, errors.New("only shell commands have a command prefix")
			default:
				return r, fmt.Errorf("unknown scope %q", scope)
			}
		}
	}
	r.Label = RuleLabel(r)
	return r, nil
}

// projectDir is the directory a "files here" rule covers: the run's working
// directory when the file is inside it, else the file's own directory. Empty
// when that would be too broad to be a project (/, a home directory, /tmp).
func projectDir(file, workdir string) string {
	if workdir = strings.TrimSpace(workdir); workdir != "" && path.IsAbs(workdir) {
		workdir = path.Clean(workdir)
		if pathWithin(file, workdir) && file != workdir && !tooBroad(workdir) {
			return workdir
		}
	}
	dir := path.Dir(file)
	if !path.IsAbs(dir) || tooBroad(dir) {
		return ""
	}
	return dir
}

var homeRootRe = regexp.MustCompile(`^/(Users|home)/[^/]+$`)

func tooBroad(dir string) bool {
	switch dir {
	case "", ".", "/", "/root", "/tmp", "/private/tmp", "/Users", "/home", "/etc", "/usr", "/var":
		return true
	}
	return homeRootRe.MatchString(dir)
}

func ruleKey(r ApprovalRule) string {
	b, _ := json.Marshal(struct {
		T string
		S RuleScope
	}{strings.ToLower(r.Tool), r.Scope})
	return string(b)
}

// ── words ───────────────────────────────────────────────────────────────────

var homePathRe = regexp.MustCompile(`^(/(Users|home)/[^/]+|/root)(/|$)`)

// tildePath shows /Users/x/a and /home/x/a as ~/a, for reading only.
func tildePath(p string) string {
	if m := homePathRe.FindStringSubmatchIndex(p); m != nil {
		rest := p[m[3]:]
		if rest == "" || rest == "/" {
			return "~"
		}
		return "~" + rest
	}
	return p
}

// RuleLabel is a rule in one Chinese sentence.
func RuleLabel(r ApprovalRule) string {
	var b strings.Builder
	b.WriteString("以后")
	sc := r.Scope
	if sc.Agent != "" {
		b.WriteString(" " + sc.Agent + " ")
	}
	if sc.Machine != "" && sc.Machine != LocalMachine {
		b.WriteString("在 " + sc.Machine + " 上")
	}
	act := ruleAction(r)
	b.WriteString(act)
	// "重启 superai-worker 都不用问": a Latin word does not run into the Chinese.
	if rs := []rune(act); len(rs) > 0 && rs[len(rs)-1] < 0x80 {
		b.WriteString(" ")
	}
	if r.Decision == RuleDeny {
		b.WriteString("都直接拒绝")
	} else {
		b.WriteString("都不用问")
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func ruleAction(r ApprovalRule) string {
	sc := r.Scope
	tool := strings.ToLower(r.Tool)
	switch {
	case tool == KindShell:
		switch {
		case sc.Command != "":
			if h := humanCommand(sc.Command); h != "" {
				return h
			}
			return "运行 “" + clipRunes(sc.Command, 80) + "”"
		case sc.CommandPrefix != "":
			return "运行以 “" + sc.CommandPrefix + "” 开头的命令"
		default:
			return "运行任何命令"
		}
	case tool == KindWriteFile:
		switch {
		case sc.Path != "":
			return "写入 " + tildePath(sc.Path)
		case sc.PathPrefix != "":
			return "写入 " + tildePath(sc.PathPrefix) + " 下的文件"
		default:
			return "写任何文件"
		}
	case strings.HasPrefix(tool, "mcp:"):
		parts := strings.SplitN(r.Tool, ":", 3)
		if len(parts) == 3 {
			if parts[2] == "*" {
				return "使用 " + parts[1] + " 的任何工具"
			}
			return "使用 " + parts[1] + " 的 " + parts[2]
		}
	}
	return "使用 " + r.Tool
}

// humanCommand names a few commands by what they do: "重启 superai-worker".
func humanCommand(cmd string) string {
	if hasShellControl(cmd) {
		return ""
	}
	w, _, ok := shellWords(cmd)
	if !ok || len(w) < 2 {
		return ""
	}
	last := w[len(w)-1]
	name := last[strings.LastIndex(last, "/")+1:]
	switch path.Base(w[0]) {
	case "kubectl":
		for i := 0; i+1 < len(w); i++ {
			if w[i] == "rollout" && w[i+1] == "restart" && i+2 == len(w)-1 {
				return "重启 " + name
			}
		}
	case "systemctl", "docker":
		if len(w) == 3 && w[1] == "restart" {
			return "重启 " + name
		}
	}
	return ""
}

// ── the store ───────────────────────────────────────────────────────────────

// ApprovalRulesPath is where rules are kept.
func ApprovalRulesPath() string {
	return filepath.Join(DataDir(), "approval-rules.json")
}

// ApprovalRuleStore is the rules, persisted as one JSON file.
type ApprovalRuleStore struct {
	mu    sync.Mutex
	path  string
	rules []ApprovalRule
	mtime time.Time
	size  int64
	now   func() time.Time
}

type ruleFile struct {
	Version int            `json:"version"`
	Rules   []ApprovalRule `json:"rules"`
}

// NewApprovalRuleStore opens (lazily) the rules at path.
func NewApprovalRuleStore(p string) *ApprovalRuleStore {
	return &ApprovalRuleStore{path: p, now: time.Now}
}

var sharedRules sync.Map // path → *ApprovalRuleStore

// SharedApprovalRules is the store for the current data directory.
func SharedApprovalRules() *ApprovalRuleStore {
	p := ApprovalRulesPath()
	if s, ok := sharedRules.Load(p); ok {
		return s.(*ApprovalRuleStore)
	}
	s, _ := sharedRules.LoadOrStore(p, NewApprovalRuleStore(p))
	return s.(*ApprovalRuleStore)
}

// Path is the file behind the store.
func (s *ApprovalRuleStore) Path() string { return s.path }

// refreshLocked re-reads the file when it changed on disk (or was never read).
func (s *ApprovalRuleStore) refreshLocked() {
	st, err := os.Stat(s.path)
	if err != nil {
		s.rules, s.mtime, s.size = nil, time.Time{}, 0
		return
	}
	if st.ModTime().Equal(s.mtime) && st.Size() == s.size {
		return
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f ruleFile
	if json.Unmarshal(raw, &f) != nil {
		// A file that does not parse is not a reason to start answering
		// differently; keep what was loaded last.
		return
	}
	s.rules, s.mtime, s.size = f.Rules, st.ModTime(), st.Size()
}

func (s *ApprovalRuleStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	rules := s.rules
	if rules == nil {
		rules = []ApprovalRule{}
	}
	raw, err := json.MarshalIndent(ruleFile{Version: 1, Rules: rules}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if st, err := os.Stat(s.path); err == nil {
		s.mtime, s.size = st.ModTime(), st.Size()
	}
	return nil
}

// List is every rule, oldest first.
func (s *ApprovalRuleStore) List() []ApprovalRule {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	out := make([]ApprovalRule, len(s.rules))
	copy(out, s.rules)
	for i := range out {
		if out[i].Label == "" {
			out[i].Label = RuleLabel(out[i])
		}
	}
	return out
}

// Add saves a rule. One already there with the same tool and scope is
// replaced (the newer decision wins), so remembering twice does not pile up.
func (s *ApprovalRuleStore) Add(r ApprovalRule) (ApprovalRule, error) {
	r.Tool = strings.TrimSpace(r.Tool)
	if r.Tool == "" {
		return r, errors.New("a rule needs a tool")
	}
	if r.Decision != RuleAllow && r.Decision != RuleDeny {
		return r, fmt.Errorf("decision must be %q or %q", RuleAllow, RuleDeny)
	}
	if r.Scope.CommandPrefix != "" {
		if w, _, ok := shellWords(r.Scope.CommandPrefix); !ok || len(w) == 0 {
			return r, errors.New("command prefix does not parse")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = s.now().UTC()
	}
	r.Label = RuleLabel(r)
	key := ruleKey(r)
	kept := s.rules[:0:0]
	for _, old := range s.rules {
		if ruleKey(old) != key {
			kept = append(kept, old)
		}
	}
	prev := s.rules
	s.rules = append(kept, r)
	if err := s.saveLocked(); err != nil {
		s.rules = prev
		return r, err
	}
	return r, nil
}

// Delete removes a rule by id; false when there was none.
func (s *ApprovalRuleStore) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	kept := s.rules[:0:0]
	for _, r := range s.rules {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(s.rules) {
		return false, nil
	}
	prev := s.rules
	s.rules = kept
	if err := s.saveLocked(); err != nil {
		s.rules = prev
		return false, err
	}
	return true, nil
}

// Match is the rule that answers this request, if any. A matching deny wins
// over a matching allow: "never" is not overridden by a broader "fine".
func (s *ApprovalRuleStore) Match(req ApprovalRequest) (ApprovalRule, bool) {
	if s == nil || req.MustAsk {
		return ApprovalRule{}, false
	}
	s.mu.Lock()
	s.refreshLocked()
	rules := make([]ApprovalRule, len(s.rules))
	copy(rules, s.rules)
	s.mu.Unlock()

	sub := subjectOf(req)
	var allow *ApprovalRule
	for i := range rules {
		r := rules[i]
		if !ruleMatches(r, sub) {
			continue
		}
		if r.Label == "" {
			r.Label = RuleLabel(r)
		}
		if r.Decision == RuleDeny {
			return r, true
		}
		if r.Decision == RuleAllow && allow == nil {
			allow = &r
		}
	}
	if allow != nil {
		return *allow, true
	}
	return ApprovalRule{}, false
}

// RuleDecision is the answer a matched rule gives, worded for the audit log
// and 最近处理.
func RuleDecision(r ApprovalRule) ApprovalDecision {
	if r.Decision == RuleDeny {
		return ApprovalDecision{By: DecidedByApprovalRule, Reason: "按规则自动拒绝：" + r.Label}
	}
	return ApprovalDecision{Allowed: true, By: DecidedByApprovalRule, Reason: "按规则自动同意：" + r.Label}
}
