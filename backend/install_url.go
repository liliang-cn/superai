package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
)

// Install from a URL.
//
// Registries only list what someone bothered to publish there, while an MCP
// server or a skill is usually documented on its own README: a command, some
// args, a couple of environment variables, written for a human. These two tools
// close that gap — hand over any URL and the install recipe gets worked out.
//
// The work is a loop, not one shot. One page is often not enough: a README
// links to a docs page, the npm page has the real package name, the example
// config lives in a separate file. And an install can fail — a wrong package
// name, a command that is not on PATH — in a way the error message explains.
// So each round the model picks an action (read another page, try installing,
// ask the user for a credential, give up), gets the outcome, and continues.
// Installing is verified rather than assumed: a server that started with zero
// tools is a failure worth reporting and retrying.

const (
	// fetchLimit caps how much of one page is read. READMEs are small; the
	// limit stops a huge page from blowing up the prompt.
	fetchLimit = 400 * 1024
	// pageBudget caps how much of a page is shown to the model per round.
	pageBudget = 24 * 1024
	// fetchTimeout bounds a single page fetch.
	fetchTimeout = 20 * time.Second
	// maxInstallRounds bounds the loop: enough to read a few linked pages and
	// retry a failed attempt or two, not enough to wander.
	maxInstallRounds = 6
	// maxPages bounds how many pages one install may read.
	maxPages = 5
)

// registerURLInstallTools adds the two "give me a URL" installers.
func (s *Service) registerURLInstallTools() {
	svc := s.svc
	if svc == nil {
		return
	}
	destMeta := agent.ToolMetadata{Destructive: true, InterruptBehavior: agent.InterruptBehaviorBlock}

	svc.AddToolWithMetadata(
		"install_mcp_from_url",
		"Given a URL (README, docs, npm page, GitHub repo), work out how this MCP server is installed and install it. It explores over several rounds by itself: read the page, follow linked pages when needed, try installing, retry a different way when the error says why, then verify the tool count. Use it when the user says \"install this MCP: <url>\". If it needs environment variables such as an API key that the user has not given, it returns needs_env — ask the user for them and call again with env.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url":  map[string]interface{}{"type": "string", "description": "URL of the MCP server's docs or repository"},
				"name": map[string]interface{}{"type": "string", "description": "Optional; the name to install the server under"},
				"env":  map[string]interface{}{"type": "object", "description": "Optional; environment variables (e.g. {\"GITHUB_TOKEN\":\"ghp_xxx\"})"},
			},
			"required": []string{"url"},
		},
		func(ctx context.Context, a map[string]interface{}) (interface{}, error) {
			return s.installMCPFromURL(ctx, argStr(a, "url"), argStr(a, "name"), argStrMap(a, "env"))
		},
		destMeta,
	)

	svc.AddToolWithMetadata(
		"install_skill_from_url",
		"Given a URL (git repository, raw SKILL.md, docs page), work out what it is and install it as a skill. It explores over several rounds by itself: decide whether it is a repository or a description page, clone it or write a SKILL.md from the page, then verify it loaded. Installed into ~/.superai-desktop/skills/<name> and hot-reloaded.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url":  map[string]interface{}{"type": "string", "description": "The skill's repository URL, a raw SKILL.md link, or a description page"},
				"name": map[string]interface{}{"type": "string", "description": "Optional; the name (directory name) to install the skill under"},
			},
			"required": []string{"url"},
		},
		func(ctx context.Context, a map[string]interface{}) (interface{}, error) {
			return s.installSkillFromURL(ctx, argStr(a, "url"), argStr(a, "name"))
		},
		destMeta,
	)
}

// ---------------------------------------------------------------------------
// The loop
// ---------------------------------------------------------------------------

// installLoop accumulates what the model has learned across rounds: the pages
// it read and how each attempt went. Both installers share it.
type installLoop struct {
	pages    []readPage
	attempts []string
	visited  map[string]bool
}

type readPage struct {
	url  string
	text string
}

func newInstallLoop() *installLoop {
	return &installLoop{visited: map[string]bool{}}
}

// fetch reads a page once, recording it for the prompt. Re-reading a page the
// loop already has is refused so a confused model cannot spin on one URL.
func (l *installLoop) fetch(ctx context.Context, rawURL string) error {
	key := strings.TrimSpace(rawURL)
	if key == "" {
		return fmt.Errorf("no URL to read was given")
	}
	if l.visited[key] {
		return fmt.Errorf("this URL has already been read: %s", key)
	}
	if len(l.pages) >= maxPages {
		return fmt.Errorf("%d pages have been read already; no more will be read", maxPages)
	}
	text, err := fetchReadable(ctx, key)
	l.visited[key] = true
	if err != nil {
		return err
	}
	l.pages = append(l.pages, readPage{url: key, text: truncate(text, pageBudget)})
	return nil
}

// note records the outcome of an attempt so the next round can learn from it.
func (l *installLoop) note(format string, args ...interface{}) {
	l.attempts = append(l.attempts, fmt.Sprintf(format, args...))
}

// context renders what is known so far.
func (l *installLoop) context() string {
	var b strings.Builder
	for _, p := range l.pages {
		fmt.Fprintf(&b, "\n=== Page: %s ===\n%s\n", p.url, p.text)
	}
	if len(l.attempts) > 0 {
		b.WriteString("\n=== Earlier attempts (do not repeat the same approach) ===\n")
		for i, a := range l.attempts {
			fmt.Fprintf(&b, "%d. %s\n", i+1, a)
		}
	}
	return b.String()
}

// trace is what the tool reports back about how it got there — without it a
// multi-round install is a black box to both the model and the user.
func (l *installLoop) trace() []string {
	out := make([]string, 0, len(l.pages)+len(l.attempts))
	for _, p := range l.pages {
		out = append(out, "read "+p.url)
	}
	out = append(out, l.attempts...)
	return out
}

// ---------------------------------------------------------------------------
// MCP
// ---------------------------------------------------------------------------

// mcpAction is one decision from the model.
type mcpAction struct {
	Action  string   `json:"action"` // fetch | install | need_env | give_up
	URL     string   `json:"url"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	EnvKeys []string `json:"env_keys"`
	Reason  string   `json:"reason"`
}

const mcpLoopPrompt = `You are installing an MCP server. Target URL: %s

Each round, pick exactly one action:
- fetch: what you have is not enough to determine how it starts — give the next URL to read (url). For example the README points at a docs page, or the npm package page is needed to confirm the package name.
- install: you can determine the stdio launch — give name / command / args / env_keys and install it.
- need_env: the launch is determined but required environment variables (env_keys) are missing and must be asked of the user.
- give_up: the page has nothing to do with an MCP server, or it only supports http/sse and cannot start over stdio; say so in reason.

Rules:
- command is the executable name alone (usually npx / uvx / node / python / docker); the parameters go in args.
- Remember "-y" with npx.
- List only the env_keys the page explicitly requires; do not invent any.
- Keep path placeholders (such as /path/to/dir) verbatim in args.
- If the last install failed, read the error before deciding: a wrong package name means fixing args, a missing command means an equivalent one (npx<->uvx), too little information means fetching another page.
- Never repeat an approach that has already failed exactly as it was.

What is known so far:%s`

// installMCPFromURL works out how to install an MCP server and does it,
// verifying the result and retrying on failure.
func (s *Service) installMCPFromURL(ctx context.Context, rawURL, name string, env map[string]string) (interface{}, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errResult("url is required"), nil
	}
	loop := newInstallLoop()
	if err := loop.fetch(ctx, rawURL); err != nil {
		return errResult(err.Error()), nil
	}

	for round := 1; round <= maxInstallRounds; round++ {
		var act mcpAction
		prompt := fmt.Sprintf(mcpLoopPrompt, rawURL, loop.context())
		if err := s.extractJSON(ctx, prompt, mcpActionSchema(), &act); err != nil {
			return errResult("could not read how to install it: " + err.Error()), nil
		}

		switch strings.ToLower(strings.TrimSpace(act.Action)) {
		case "fetch":
			if err := loop.fetch(ctx, act.URL); err != nil {
				loop.note("tried to read %s but failed: %v", act.URL, err)
			}
			continue

		case "need_env":
			missing := missingEnv(act.EnvKeys, env)
			if len(missing) == 0 {
				// Everything it asked for is already in hand; press on.
				loop.note("claimed environment variables were missing, but %v were all supplied; installing anyway", act.EnvKeys)
				continue
			}
			return okData(map[string]interface{}{
				"installed": false, "needs_env": missing,
				"server": firstNonEmpty(name, act.Name), "command": act.Command, "args": act.Args,
				"note":  "This MCP server needs the environment variables above; ask the user for them and call again with env.",
				"trace": loop.trace(),
			}), nil

		case "install":
			server := sanitizeName(firstNonEmpty(name, act.Name))
			if server == "" || strings.TrimSpace(act.Command) == "" {
				loop.note("the configuration given was incomplete (name=%q command=%q)", act.Name, act.Command)
				continue
			}
			if missing := missingEnv(act.EnvKeys, env); len(missing) > 0 {
				return okData(map[string]interface{}{
					"installed": false, "needs_env": missing,
					"server": server, "command": act.Command, "args": act.Args,
					"note":  "This MCP server needs the environment variables above; ask the user for them and call again with env.",
					"trace": loop.trace(),
				}), nil
			}

			err := s.InstallMCPServer(ctx, server, act.Command, act.Args, env)
			tools, running := s.mcpServerState(server)
			switch {
			case err != nil:
				loop.note("installing %s (%s %s) failed: %v", server, act.Command, strings.Join(act.Args, " "), err)
			case !running:
				loop.note("installed %s but the process did not start", server)
			case tools == 0:
				loop.note("%s started but exposed no tools; the configuration is probably wrong", server)
			default:
				return okData(map[string]interface{}{
					"installed": true, "server": server, "command": act.Command, "args": act.Args,
					"tools": tools, "rounds": round, "trace": loop.trace(),
					"note": "Started and written to the config; it survives a restart.",
				}), nil
			}
			// A failed attempt leaves nothing half-installed behind.
			_ = RemoveMCPServer(server)
			continue

		default: // give_up and anything unrecognised
			reason := firstNonEmpty(act.Reason, "could not determine from the page how to install it")
			return okData(map[string]interface{}{
				"installed": false, "reason": reason, "trace": loop.trace(),
			}), nil
		}
	}

	return okData(map[string]interface{}{
		"installed": false,
		"reason":    fmt.Sprintf("still not installed after %d rounds", maxInstallRounds),
		"trace":     loop.trace(),
	}), nil
}

// mcpServerState reports a server's tool count and whether it is running.
func (s *Service) mcpServerState(name string) (tools int, running bool) {
	if s.svc == nil || s.svc.MCP == nil {
		return 0, false
	}
	for _, srv := range s.svc.MCP.ListServers() {
		if srv.Name == name {
			return srv.ToolCount, srv.Running
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// Skills
// ---------------------------------------------------------------------------

// skillAction is one decision from the model.
type skillAction struct {
	Action  string `json:"action"` // fetch | clone | write | give_up
	URL     string `json:"url"`
	GitURL  string `json:"git_url"`
	Name    string `json:"name"`
	SkillMD string `json:"skill_md"`
	Reason  string `json:"reason"`
}

const skillLoopPrompt = `You are installing a skill (an AI skill package in SKILL.md format). Target URL: %s

Each round, pick exactly one action:
- fetch: what you have is not enough — give the next URL to read (url).
- clone: the page carries a repository URL that can be git cloned — fill in git_url and name.
- write: the page describes the skill but there is no repository — write the complete SKILL.md from the page into skill_md, and give a name.
- give_up: the page has nothing to do with a skill; say so in reason.

Rules:
- SKILL.md must open with YAML frontmatter carrying name and description:
  ---
  name: my-skill
  description: Use when ...
  ---
  and then the body.
- Name it in lowercase with hyphens.
- Write only from the page's content; do not invent capabilities.
- If the last clone failed or the repository had no SKILL.md, change approach: read another page, or write it yourself instead.

What is known so far:%s`

// installSkillFromURL installs a skill, exploring until it has something that
// actually loads.
func (s *Service) installSkillFromURL(ctx context.Context, rawURL, name string) (interface{}, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errResult("url is required"), nil
	}
	loop := newInstallLoop()

	// A repo root needs no model: clone it and see.
	if isGitRepoURL(rawURL) {
		res, err := s.installSkillFromGit(ctx, rawURL, name)
		if err != nil || installSucceeded(res) {
			return res, err
		}
		loop.note("cloning %s directly did not work (%s); still looking for clues on the page", rawURL, resultProblem(res))
	}
	if err := loop.fetch(ctx, rawURL); err != nil {
		return errResult(err.Error()), nil
	}
	if page := loop.pages[len(loop.pages)-1].text; looksLikeSkillMD(page) {
		return s.writeSkill(ctx, firstNonEmpty(sanitizeName(name), skillNameFromMD(page), skillNameFromURL(rawURL)), page, loop)
	}

	for round := 1; round <= maxInstallRounds; round++ {
		var act skillAction
		prompt := fmt.Sprintf(skillLoopPrompt, rawURL, loop.context())
		if err := s.extractJSON(ctx, prompt, skillActionSchema(), &act); err != nil {
			return errResult("could not read how to install it: " + err.Error()), nil
		}

		switch strings.ToLower(strings.TrimSpace(act.Action)) {
		case "fetch":
			if err := loop.fetch(ctx, act.URL); err != nil {
				loop.note("tried to read %s but failed: %v", act.URL, err)
			}
			continue

		case "clone":
			if strings.TrimSpace(act.GitURL) == "" {
				loop.note("chose clone but gave no repository URL")
				continue
			}
			res, err := s.installSkillFromGit(ctx, act.GitURL, firstNonEmpty(name, act.Name))
			if err != nil {
				return res, err
			}
			if installSucceeded(res) {
				return res, nil
			}
			loop.note("cloning %s did not install it: %s", act.GitURL, resultProblem(res))
			continue

		case "write":
			if strings.TrimSpace(act.SkillMD) == "" {
				loop.note("chose write but gave no SKILL.md content")
				continue
			}
			return s.writeSkill(ctx,
				firstNonEmpty(sanitizeName(name), sanitizeName(act.Name), skillNameFromURL(rawURL)),
				act.SkillMD, loop)

		default:
			return okData(map[string]interface{}{
				"installed": false,
				"reason":    firstNonEmpty(act.Reason, "could not tell how to install it"),
				"trace":     loop.trace(),
			}), nil
		}
	}

	return okData(map[string]interface{}{
		"installed": false,
		"reason":    fmt.Sprintf("still not installed after %d rounds", maxInstallRounds),
		"trace":     loop.trace(),
	}), nil
}

// installSkillFromGit clones a repo into the skills directory, keeping it only
// if it really contains a SKILL.md.
func (s *Service) installSkillFromGit(ctx context.Context, gitURL, name string) (interface{}, error) {
	skill := sanitizeName(firstNonEmpty(name, skillNameFromURL(gitURL)))
	if skill == "" {
		return errResult("could not determine the skill name; give one as name"), nil
	}
	dst := filepath.Join(DataDir(), "skills", skill)
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return errResult(err.Error()), nil
	}
	out, err := exec.CommandContext(ctx, "git", "clone", "--depth", "1", gitURL, dst).CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dst)
		return errResult("git clone failed: " + truncate(strings.TrimSpace(string(out)), 500)), nil
	}
	// Repos that bundle several skills keep each one a level down.
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		if found := findSkillDir(dst); found != "" {
			if cerr := copySkillDir(found, dst+".tmp"); cerr == nil {
				_ = os.RemoveAll(dst)
				_ = os.Rename(dst+".tmp", dst)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		_ = os.RemoveAll(dst)
		return errResult("the repository has no SKILL.md, so it is not a skill"), nil
	}
	return s.finishSkillInstall(ctx, skill, dst, "git clone", nil)
}

// writeSkill installs a skill from SKILL.md content.
func (s *Service) writeSkill(ctx context.Context, name, skillMD string, loop *installLoop) (interface{}, error) {
	name = sanitizeName(name)
	if name == "" {
		return errResult("could not determine the skill name; give one as name"), nil
	}
	dst := filepath.Join(DataDir(), "skills", name)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return errResult(err.Error()), nil
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		return errResult(err.Error()), nil
	}
	return s.finishSkillInstall(ctx, name, dst, "SKILL.md", loop)
}

// finishSkillInstall hot-reloads and verifies the skill actually loaded.
func (s *Service) finishSkillInstall(ctx context.Context, name, path, how string, loop *installLoop) (interface{}, error) {
	if s.svc.Skills != nil {
		_ = s.svc.Skills.LoadAll(ctx)
	}
	installed := s.svc.HasSkill(name)
	data := map[string]interface{}{
		"installed": installed, "skill": name, "path": path, "method": how,
	}
	if loop != nil {
		data["trace"] = loop.trace()
	}
	if !installed {
		// Written but not loaded means the SKILL.md is malformed; do not leave a
		// broken directory behind claiming success.
		data["note"] = "The file was written but did not load; the SKILL.md is probably malformed (it needs name/description frontmatter)"
	}
	return okData(data), nil
}

// ---------------------------------------------------------------------------
// Model plumbing
// ---------------------------------------------------------------------------

// extractJSON asks the model for one structured decision and decodes it.
func (s *Service) extractJSON(ctx context.Context, prompt string, schema map[string]interface{}, out interface{}) error {
	if s.brain == nil {
		return fmt.Errorf("no LLM configured")
	}
	res, err := s.brain.GenerateStructured(ctx, prompt, schema, &domain.GenerationOptions{Temperature: 0})
	if err != nil {
		return err
	}
	if res == nil || strings.TrimSpace(res.Raw) == "" {
		return fmt.Errorf("model returned nothing")
	}
	body := strings.TrimSpace(res.Raw)
	// Some models wrap JSON in a fenced block despite the schema.
	if i := strings.Index(body, "{"); i > 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	return json.Unmarshal([]byte(body), out)
}

func mcpActionSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":   map[string]interface{}{"type": "string", "enum": []string{"fetch", "install", "need_env", "give_up"}},
			"url":      map[string]interface{}{"type": "string", "description": "next page to read, for action=fetch"},
			"name":     map[string]interface{}{"type": "string", "description": "short server name, e.g. filesystem"},
			"command":  map[string]interface{}{"type": "string", "description": "executable only, e.g. npx"},
			"args":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"env_keys": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"reason":   map[string]interface{}{"type": "string"},
		},
		"required": []string{"action"},
	}
}

func skillActionSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":   map[string]interface{}{"type": "string", "enum": []string{"fetch", "clone", "write", "give_up"}},
			"url":      map[string]interface{}{"type": "string", "description": "next page to read, for action=fetch"},
			"git_url":  map[string]interface{}{"type": "string"},
			"name":     map[string]interface{}{"type": "string", "description": "lower-case dashed skill name"},
			"skill_md": map[string]interface{}{"type": "string", "description": "full SKILL.md including YAML frontmatter"},
			"reason":   map[string]interface{}{"type": "string"},
		},
		"required": []string{"action"},
	}
}

// ---------------------------------------------------------------------------
// Fetching and small helpers
// ---------------------------------------------------------------------------

// fetchReadable downloads a URL and returns it as text the model can read:
// HTML becomes markdown, everything else passes through.
func fetchReadable(ctx context.Context, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}
	// A GitHub repo page is mostly chrome; the raw README is the content.
	if raw := githubReadmeURL(rawURL); raw != "" {
		if body, ctype, err := httpGet(ctx, raw); err == nil {
			return asText(body, ctype), nil
		}
	}
	body, ctype, err := httpGet(ctx, rawURL)
	if err != nil {
		return "", err
	}
	text := asText(body, ctype)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("the page has no readable content: %s", rawURL)
	}
	return text, nil
}

func httpGet(ctx context.Context, u string) ([]byte, string, error) {
	rctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "SuperAI-Desktop/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("fetching %s returned %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchLimit))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// asText converts HTML to markdown, leaving other content types alone.
func asText(body []byte, contentType string) string {
	if strings.Contains(strings.ToLower(contentType), "html") {
		if md, err := htmltomarkdown.ConvertString(string(body)); err == nil {
			return md
		}
	}
	return string(body)
}

// githubReadmeURL maps a GitHub repo URL to its raw README, or "" when the URL
// is not a plain repo root.
func githubReadmeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return ""
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" {
		return ""
	}
	// HEAD resolves whatever the default branch is called.
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/HEAD/README.md", owner, repo)
}

// missingEnv lists the requested keys that were not supplied.
func missingEnv(keys []string, env map[string]string) []string {
	missing := []string{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := env[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

// installSucceeded reads a tool result envelope. okData reports ok=true even
// when the payload says installed=false (written but failed to load), so the
// loop has to look at the payload, not the envelope, before declaring success.
func installSucceeded(res interface{}) bool {
	m, _ := res.(map[string]interface{})
	if ok, _ := m["ok"].(bool); !ok {
		return false
	}
	data, _ := m["data"].(map[string]interface{})
	installed, _ := data["installed"].(bool)
	return installed
}

// resultProblem pulls whatever explanation a failed result carries.
func resultProblem(res interface{}) string {
	m, _ := res.(map[string]interface{})
	if e, ok := m["error"].(string); ok && e != "" {
		return e
	}
	data, _ := m["data"].(map[string]interface{})
	for _, key := range []string{"note", "reason"} {
		if v, ok := data[key].(string); ok && v != "" {
			return v
		}
	}
	return "not installed"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// isGitRepoURL reports whether a URL can be handed straight to git clone.
func isGitRepoURL(u string) bool {
	if strings.HasPrefix(u, "git@") || strings.HasSuffix(u, ".git") {
		return true
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Host)
	if host != "github.com" && host != "gitlab.com" && host != "bitbucket.org" {
		return false
	}
	// Exactly owner/repo is a clonable root; anything deeper is a page.
	return len(strings.Split(strings.Trim(parsed.Path, "/"), "/")) == 2
}

var skillFrontmatter = regexp.MustCompile(`(?s)\A\s*---\s*\n.*?\bname\s*:.*?\n---`)

// looksLikeSkillMD reports whether text is already a SKILL.md.
func looksLikeSkillMD(text string) bool {
	return skillFrontmatter.MatchString(text)
}

var skillNameField = regexp.MustCompile(`(?m)^\s*name\s*:\s*(.+)$`)

// skillNameFromMD reads the name out of SKILL.md frontmatter.
func skillNameFromMD(text string) string {
	if m := skillNameField.FindStringSubmatch(text); len(m) == 2 {
		return sanitizeName(strings.Trim(strings.TrimSpace(m[1]), `"'`))
	}
	return ""
}

// skillNameFromURL falls back to the last meaningful path segment.
func skillNameFromURL(rawURL string) string {
	rawURL = strings.TrimSuffix(strings.TrimSuffix(rawURL, "/"), ".git")
	parts := strings.Split(rawURL, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		seg := strings.TrimSpace(parts[i])
		if seg == "" || strings.EqualFold(seg, "SKILL.md") || strings.Contains(seg, ":") {
			continue
		}
		return sanitizeName(strings.TrimSuffix(seg, ".md"))
	}
	return ""
}

// findSkillDir looks one level down for a directory holding a SKILL.md, which
// is how repos that bundle several skills are laid out.
func findSkillDir(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		candidate := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(candidate, "SKILL.md")); err == nil {
			return candidate
		}
	}
	return ""
}
