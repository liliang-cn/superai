package app

// How each agent CLI is made to think for the queen.
//
// The queen's kit (QueenKit) says what a run is handed in one shape: her
// prompt, her MCP servers as Claude's --mcp-config reads them (${NAME} in
// place of every secret, the value in Env), the tool rules she may use
// unasked. Each CLI takes those its own way, and a queenCLI is that way,
// applied on the machine the CLI runs on — so a kit sent to another
// SuperLeo works for whichever CLI is there.
//
//   - claude: --mcp-config as JSON, --append-system-prompt, --allowedTools,
//     permission prompts through --permission-prompt-tool to the approval
//     channel. Streams as it writes; resumes with --resume.
//   - codex: `codex exec --json`, the servers as -c mcp_servers.<name>=…
//     with the token named by bearer_token_env_var, the prompt as
//     developer_instructions, without the person's config.toml or hooks.
//     Resumes with `codex exec resume`. Codex has no prompt to forward in
//     exec mode: attended, it runs in its workspace-write sandbox, and the
//     hive server's changes still wait on this app's own approval.
//   - pi: `pi -p --mode json` with an extension of SuperLeo's own
//     (cliplugins/pi_queen.js) that registers the servers and sends every
//     call that is not a read to the approval channel. Streams as it
//     writes; resumes with --session.
//   - hermes: `hermes -z` in a hermes home of SuperLeo's naming the servers,
//     with the person's own home laid over it for their model and keys.
//     One-shot mode prints only the answer, approves everything itself and
//     keeps no session to resume, so each turn is handed the conversation
//     again; attended, it gets only the queen's servers and hermes's toolset
//     without a shell.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superleo/internal/backend"
)

// queenCLI is one agent CLI as the queen's engine.
type queenCLI struct {
	// Resumes is whether the next turn continues the CLI's session. One
	// that cannot is handed the conversation again each turn instead.
	Resumes bool
	// Asks is whether the CLI can put what it is about to do to the person
	// through the approval channel.
	Asks bool
	// Streams is whether the CLI says what it says as it goes. One that
	// does not prints its last word only, a failure too.
	Streams bool
	// apply puts the kit into a run's request: servers, prompt, flags and
	// environment. ask is whether the run is attended; approve is the
	// approval channel's URL for an attended run of a CLI that Asks.
	apply func(req *agentexec.Request, kit *QueenKit, ask bool, approve string) error
}

// queenCLIs are the CLIs that can think for the queen.
var queenCLIs = map[string]queenCLI{
	"claude": {Resumes: true, Asks: true, Streams: true, apply: claudeQueen},
	"codex":  {Resumes: true, Streams: true, apply: codexQueen},
	"pi":     {Resumes: true, Asks: true, Streams: true, apply: piQueen},
	"hermes": {apply: hermesQueen},
}

// canThinkForQueen is whether an agent CLI, by its own name, can be the
// queen's engine.
func canThinkForQueen(agent string) bool {
	_, ok := queenCLIs[agent]
	return ok
}

// engineCLI is the CLI an engine name runs: "codex" and "codex.mac" both
// run codex.
func engineCLI(engine string) string {
	if agent, _, ok := splitRemoteCLI(engine); ok {
		return agent
	}
	return engine
}

// queenEngineNames are the CLIs that can think for the queen, in the order
// they are offered.
var queenEngineNames = []string{"claude", "codex", "pi", "hermes"}

// claudeMCPArgs are Claude's MCP flags: the servers as JSON on the command
// line, never as a file — agentexec writes its config into the working
// directory, where the agent lists it among the user's files and could read
// the approval URL. Strict, so the user's own servers stay out of the run.
func claudeMCPArgs(servers map[string]any, approve string) []string {
	all := map[string]any{}
	for name, entry := range servers {
		all[name] = entry
	}
	if approve != "" {
		all["superleo"] = map[string]any{"type": "http", "url": approve}
	}
	cfg, _ := json.Marshal(map[string]any{"mcpServers": all})
	return []string{"--mcp-config", string(cfg), "--strict-mcp-config"}
}

func claudeQueen(req *agentexec.Request, kit *QueenKit, _ bool, approve string) error {
	req.ExtraArgs = append(req.ExtraArgs, claudeMCPArgs(kit.Servers, approve)...)
	req.SystemPrompt = kit.SystemPrompt
	req.ExtraArgs = append(req.ExtraArgs, kit.Args...)
	// --allowedTools takes every word after it, so a flag follows it and
	// the prompt is not read as a tool.
	if len(kit.Allowed) > 0 {
		req.ExtraArgs = append(req.ExtraArgs, "--allowedTools", strings.Join(kit.Allowed, ","))
	}
	// The answer goes into the chat as it is written, not a message at a
	// time.
	req.ExtraArgs = append(req.ExtraArgs, "--include-partial-messages")
	queenEnv(req, kit)
	return nil
}

func queenEnv(req *agentexec.Request, kit *QueenKit) {
	if req.Env == nil {
		req.Env = map[string]string{}
	}
	for k, v := range kit.Env {
		req.Env[k] = v
	}
}

// allowedServers are the servers whose every tool a kit's rules let the
// CLI use unasked: "mcp__hive" is all of hive's.
func allowedServers(kit *QueenKit) map[string]bool {
	out := map[string]bool{}
	for _, rule := range kit.Allowed {
		if name, ok := strings.CutPrefix(rule, "mcp__"); ok && name != "" && !strings.Contains(name, "__") {
			out[name] = true
		}
	}
	return out
}

func sortedNames(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// codexQueen: the person's config.toml is left out (their servers, hooks,
// plugins and model with it) while the login in CODEX_HOME still works;
// the servers and the prompt are -c overrides.
func codexQueen(req *agentexec.Request, kit *QueenKit, _ bool, _ string) error {
	args := []string{"--ignore-user-config", "--disable", "hooks"}
	if req.Model != "" {
		// agentexec does not hand codex the model; the flag does.
		args = append(args, "-m", req.Model)
	}
	if p := strings.TrimSpace(kit.SystemPrompt); p != "" {
		args = append(args, "-c", "developer_instructions="+tomlString(p))
	}
	unasked := allowedServers(kit)
	for _, name := range sortedNames(kit.Servers) {
		entry, _ := kit.Servers[name].(map[string]any)
		c, err := codexServer(entry)
		if err != nil {
			return fmt.Errorf("MCP server %s: %w", name, err)
		}
		if unasked[name] {
			c["default_tools_approval_mode"] = "approve"
		}
		args = append(args, "-c", "mcp_servers."+tomlKey(name)+"="+tomlValue(c))
	}
	req.ExtraArgs = append(req.ExtraArgs, args...)
	queenEnv(req, kit)
	return nil
}

var mcpRefOnly = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// codexServer is a Claude-shaped server entry as codex's mcp_servers reads
// it. Codex fills in no ${NAME} itself, so a reference becomes the setting
// that names the variable: bearer_token_env_var, env_http_headers, env_vars.
func codexServer(e map[string]any) (map[string]any, error) {
	out := map[string]any{}
	refIn := func(v any) bool {
		raw, _ := json.Marshal(v)
		return len(backend.MCPEnvRefs(string(raw))) > 0
	}
	if u, _ := e["url"].(string); u != "" {
		if refIn(u) {
			return nil, errors.New("its URL names a variable")
		}
		out["url"] = u
		headers, _ := e["headers"].(map[string]any)
		plain, fromEnv := map[string]any{}, map[string]any{}
		for _, h := range sortedNames(headers) {
			v, _ := headers[h].(string)
			if strings.EqualFold(h, "Authorization") {
				if tok, ok := strings.CutPrefix(v, "Bearer "); ok {
					if m := mcpRefOnly.FindStringSubmatch(tok); m != nil {
						out["bearer_token_env_var"] = m[1]
						continue
					}
				}
			}
			switch m := mcpRefOnly.FindStringSubmatch(v); {
			case m != nil:
				fromEnv[h] = m[1]
			case refIn(v):
				return nil, fmt.Errorf("header %s is not a variable or a value", h)
			default:
				plain[h] = v
			}
		}
		if len(plain) > 0 {
			out["http_headers"] = plain
		}
		if len(fromEnv) > 0 {
			out["env_http_headers"] = fromEnv
		}
		return out, nil
	}
	cmd, _ := e["command"].(string)
	if cmd == "" {
		return nil, errors.New("it has neither a URL nor a command")
	}
	if refIn(cmd) || refIn(e["args"]) {
		return nil, errors.New("its command names a variable")
	}
	out["command"] = cmd
	if args, ok := e["args"].([]any); ok && len(args) > 0 {
		out["args"] = args
	}
	env, _ := e["env"].(map[string]any)
	plain := map[string]any{}
	var pass []any
	for _, k := range sortedNames(env) {
		v, _ := env[k].(string)
		switch m := mcpRefOnly.FindStringSubmatch(v); {
		case m != nil && m[1] == k:
			pass = append(pass, k)
		case refIn(v):
			return nil, fmt.Errorf("%s is set from another variable", k)
		default:
			plain[k] = v
		}
	}
	if len(plain) > 0 {
		out["env"] = plain
	}
	if len(pass) > 0 {
		out["env_vars"] = pass
	}
	return out, nil
}

// tomlString is s as a TOML basic string: JSON's escapes are TOML's.
func tomlString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

var tomlBareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if tomlBareKey.MatchString(k) {
		return k
	}
	return tomlString(k)
}

// tomlValue is v as an inline TOML value, which -c reads.
func tomlValue(v any) string {
	switch x := v.(type) {
	case string:
		return tomlString(x)
	case bool:
		return fmt.Sprint(x)
	case float64, int, int64:
		return fmt.Sprint(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = tomlValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = tomlString(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		parts := make([]string, 0, len(x))
		for _, k := range sortedNames(x) {
			parts = append(parts, tomlKey(k)+" = "+tomlValue(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return tomlString(fmt.Sprint(v))
}

// The pi extension's environment: what it registers and where it asks.
const (
	piServersEnv = "SUPERLEO_QUEEN_SERVERS"
	piAllowedEnv = "SUPERLEO_QUEEN_ALLOWED"
	piApproveEnv = "SUPERLEO_APPROVE_URL"
)

// piQueen: SuperLeo's extension alone, none of the person's (extensions,
// skills, prompt templates, AGENTS.md or project files); the servers and
// the approval channel go to it in the environment.
func piQueen(req *agentexec.Request, kit *QueenKit, _ bool, approve string) error {
	ext, err := piQueenExtension()
	if err != nil {
		return fmt.Errorf("could not write the queen's pi extension: %w", err)
	}
	servers := map[string]any{}
	unasked := allowedServers(kit)
	for name, raw := range kit.Servers {
		entry, _ := raw.(map[string]any)
		e := map[string]any{}
		for k, v := range entry {
			e[k] = v
		}
		// The hive's tools are declared to the model; a bigger server's are
		// found with tool_search when they are wanted.
		if unasked[name] {
			e["exposure"] = "direct"
		} else {
			e["exposure"] = "deferred"
		}
		servers[name] = e
	}
	body, _ := json.Marshal(servers)
	req.SystemPrompt = kit.SystemPrompt
	// --no-extensions drops pi's built-in ones too: MCP, and tool_search and
	// codemode that reach a deferred server's tools, are named again.
	req.ExtraArgs = append(req.ExtraArgs, "--no-extensions", "-e", "builtin:mcp", "-e", "builtin:tool-search",
		"-e", "builtin:codemode", "-e", ext, "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-approve")
	queenEnv(req, kit)
	req.Env[piServersEnv] = string(body)
	req.Env[piAllowedEnv] = strings.Join(kit.Allowed, ",")
	if approve != "" {
		req.Env[piApproveEnv] = approve
	}
	return nil
}

// piQueenExtension writes the extension where pi is pointed at it, in this
// app's data, and says where that is.
func piQueenExtension() (string, error) {
	src, err := cliPluginFS.ReadFile("cliplugins/pi_queen.js")
	if err != nil {
		return "", err
	}
	path := filepath.Join(backend.DataDir(), "queen-engine", "pi-queen.js")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, src) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, src, 0o600); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// hermesSafeToolset is hermes's own toolset without a shell or file writes:
// web, vision, images.
const hermesSafeToolset = "safe"

// hermesQueen: hermes reads its MCP servers only from the config.yaml in
// HERMES_HOME — one-shot mode starts no MCP at all unless that file names a
// server — so the run gets a home of SuperLeo's own whose config.yaml names
// the queen's, and the person's own hermes home is laid over it as the
// managed layer (HERMES_MANAGED_DIR): their model, provider and keys
// (config.yaml and .env) as they set them. Their sessions, memory, SOUL.md,
// skills and plugins stay out of the run, as Claude's do. A login kept in
// their auth.json (an OAuth provider) does not come along: it is written as
// it refreshes, and only their own hermes may do that. The prompt goes in
// front of the question: one-shot mode takes no other.
func hermesQueen(req *agentexec.Request, kit *QueenKit, ask bool, _ string) error {
	servers := map[string]any{}
	for name, raw := range kit.Servers {
		entry, _ := raw.(map[string]any)
		e := map[string]any{}
		for k, v := range entry {
			if k != "type" {
				e[k] = v
			}
		}
		servers[name] = e
	}
	home, err := hermesQueenHome(map[string]any{"mcp_servers": servers})
	if err != nil {
		return fmt.Errorf("could not write the queen's hermes config: %w", err)
	}
	req.SystemPrompt = kit.SystemPrompt
	queenEnv(req, kit)
	req.Env["HERMES_HOME"] = home
	if own := personHermesHome(); own != "" {
		req.Env["HERMES_MANAGED_DIR"] = own
	}
	// Attended: what hermes would do unasked is kept to the queen's servers,
	// whose changes wait on this app's approval, and tools without a shell.
	if ask {
		sets := append([]string{hermesSafeToolset}, sortedNames(servers)...)
		req.ExtraArgs = append(req.ExtraArgs, "--toolsets", strings.Join(sets, ","))
	}
	return nil
}

// personHermesHome is the person's own hermes home, when there is one.
func personHermesHome() string {
	dir := strings.TrimSpace(os.Getenv("HERMES_HOME"))
	if dir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(h, ".hermes")
	}
	dir = expandTilde(dir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}

// hermesQueenHome is a hermes home whose config.yaml is cfg, one per cfg,
// in this app's data, sharing the dependency environment every such home
// does (hermesShareInstalls). JSON is YAML.
func hermesQueenHome(cfg map[string]any) (string, error) {
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	dir := filepath.Join(hermesQueenRoot(), hex.EncodeToString(sum[:6]))
	path := filepath.Join(dir, "config.yaml")
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(old, body) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return "", err
		}
	}
	hermesShareInstalls(dir)
	return dir, nil
}

// hermesQueenRoot holds the hermes homes of SuperLeo's own.
func hermesQueenRoot() string { return filepath.Join(backend.DataDir(), "queen-engine", "hermes") }

// hermesInstallsDir is the dependency environment every hermes home of
// SuperLeo's shares. Hermes keeps one per home (<home>/installs), and a home
// new to it builds its own on its first launch: ten minutes of installing
// before the first answer, and again for every new set of servers. Each home's
// installs is a link to this one, which warmHermes fills at start.
func hermesInstallsDir() string { return filepath.Join(hermesQueenRoot(), "installs") }

// hermesShareInstalls points a home's installs at the shared one. A home from
// before, with an environment of its own, keeps it; where links cannot be made
// (Windows without the right), the home builds its own as it did.
func hermesShareInstalls(home string) {
	shared := hermesInstallsDir()
	link := filepath.Join(home, "installs")
	if _, err := os.Lstat(link); err == nil {
		return
	}
	if err := os.MkdirAll(shared, 0o700); err != nil {
		slog.Warn("hermes: could not make the shared dependency directory", "dir", shared, "error", err)
		return
	}
	if err := os.Symlink(shared, link); err != nil {
		slog.Warn("hermes: this home builds its own dependencies", "home", home, "error", err)
	}
}

// queenAgentsInstalled are the CLIs installed here that can think for the
// queen, by name.
func queenAgentsInstalled(installed []agentexec.Installed) []string {
	var out []string
	for _, name := range queenEngineNames {
		if slices.ContainsFunc(installed, func(c agentexec.Installed) bool { return c.Name == name }) {
			out = append(out, name)
		}
	}
	return out
}
