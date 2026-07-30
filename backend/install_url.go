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
	"github.com/liliang-cn/agent-go/v2/pkg/agent"
	"github.com/liliang-cn/agent-go/v2/pkg/domain"
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
		"给一个网址(README/文档/npm 页面/GitHub 仓库),自动读懂安装方式并装上这个 MCP server。会自己多轮探索:读页面→必要时追读相关页面→试装→装不上就根据报错换个方式重试→装好后验证工具数。用户说\"装一下这个 MCP: <url>\"时用它。如果需要 API key 等环境变量而用户没给,会返回 needs_env——此时向用户索要,拿到后带 env 再调一次。",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url":  map[string]interface{}{"type": "string", "description": "MCP server 的文档/仓库网址"},
				"name": map[string]interface{}{"type": "string", "description": "可选,指定安装后的 server 名称"},
				"env":  map[string]interface{}{"type": "object", "description": "可选,环境变量(如 {\"GITHUB_TOKEN\":\"ghp_xxx\"})"},
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
		"给一个网址(git 仓库/raw SKILL.md/文档页),自动读懂并安装成一个 skill。会自己多轮探索:判断是仓库还是说明页→clone 或据页面写出 SKILL.md→验证装好。装到 ~/.superai-desktop/skills/<name> 并热重载。",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url":  map[string]interface{}{"type": "string", "description": "skill 的仓库地址、raw SKILL.md 链接或说明页网址"},
				"name": map[string]interface{}{"type": "string", "description": "可选,指定安装后的 skill 名称(目录名)"},
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
		return fmt.Errorf("没有给出要读的网址")
	}
	if l.visited[key] {
		return fmt.Errorf("这个网址已经读过了: %s", key)
	}
	if len(l.pages) >= maxPages {
		return fmt.Errorf("已经读了 %d 个页面,不再继续读", maxPages)
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
		fmt.Fprintf(&b, "\n=== 页面: %s ===\n%s\n", p.url, p.text)
	}
	if len(l.attempts) > 0 {
		b.WriteString("\n=== 之前的尝试(不要重复同样的做法) ===\n")
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
		out = append(out, "读取 "+p.url)
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

const mcpLoopPrompt = `你在安装一个 MCP server,目标网址: %s

每一轮你只选一个动作:
- fetch: 已有信息不足以确定启动方式时,给出下一个要读的网址(url)。比如 README 指向了文档页、或需要看 npm 包页面确认包名。
- install: 已经能确定 stdio 启动方式时,给出 name / command / args / env_keys 并安装。
- need_env: 启动方式已确定,但缺少必需的环境变量(env_keys),需要向用户索要。
- give_up: 页面与 MCP server 无关,或只支持 http/sse 而无法 stdio 启动;在 reason 里说明。

规则:
- command 只写可执行文件名(npx / uvx / node / python / docker 居多),参数放 args。
- npx 记得带 "-y"。
- env_keys 只列页面明确要求的,不要编造。
- 路径类占位符(如 /path/to/dir)原样保留在 args 里。
- 如果上一轮安装失败,读报错再决定:包名写错了就改 args,命令不存在就换等价方式(npx↔uvx),信息不够就 fetch 别的页面。
- 不要重复已经失败过的完全相同的做法。

已知信息:%s`

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
			return errResult("读取安装方式失败: " + err.Error()), nil
		}

		switch strings.ToLower(strings.TrimSpace(act.Action)) {
		case "fetch":
			if err := loop.fetch(ctx, act.URL); err != nil {
				loop.note("想读 %s 但失败: %v", act.URL, err)
			}
			continue

		case "need_env":
			missing := missingEnv(act.EnvKeys, env)
			if len(missing) == 0 {
				// Everything it asked for is already in hand; press on.
				loop.note("说缺环境变量,但 %v 都已提供,继续安装", act.EnvKeys)
				continue
			}
			return okData(map[string]interface{}{
				"installed": false, "needs_env": missing,
				"server": firstNonEmpty(name, act.Name), "command": act.Command, "args": act.Args,
				"note":  "这个 MCP server 需要上面的环境变量,请向用户索要后带 env 再调一次。",
				"trace": loop.trace(),
			}), nil

		case "install":
			server := sanitizeName(firstNonEmpty(name, act.Name))
			if server == "" || strings.TrimSpace(act.Command) == "" {
				loop.note("给出的配置不完整(name=%q command=%q)", act.Name, act.Command)
				continue
			}
			if missing := missingEnv(act.EnvKeys, env); len(missing) > 0 {
				return okData(map[string]interface{}{
					"installed": false, "needs_env": missing,
					"server": server, "command": act.Command, "args": act.Args,
					"note":  "这个 MCP server 需要上面的环境变量,请向用户索要后带 env 再调一次。",
					"trace": loop.trace(),
				}), nil
			}

			err := s.InstallMCPServer(ctx, server, act.Command, act.Args, env)
			tools, running := s.mcpServerState(server)
			switch {
			case err != nil:
				loop.note("试装 %s(%s %s)失败: %v", server, act.Command, strings.Join(act.Args, " "), err)
			case !running:
				loop.note("装了 %s 但进程没起来", server)
			case tools == 0:
				loop.note("%s 起来了但没有暴露任何工具,配置可能不对", server)
			default:
				return okData(map[string]interface{}{
					"installed": true, "server": server, "command": act.Command, "args": act.Args,
					"tools": tools, "rounds": round, "trace": loop.trace(),
					"note": "已启动并写入配置,重启后仍在。",
				}), nil
			}
			// A failed attempt leaves nothing half-installed behind.
			_ = RemoveMCPServer(server)
			continue

		default: // give_up and anything unrecognised
			reason := firstNonEmpty(act.Reason, "无法从页面确定安装方式")
			return okData(map[string]interface{}{
				"installed": false, "reason": reason, "trace": loop.trace(),
			}), nil
		}
	}

	return okData(map[string]interface{}{
		"installed": false,
		"reason":    fmt.Sprintf("试了 %d 轮仍没装上", maxInstallRounds),
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

const skillLoopPrompt = `你在安装一个 skill(SKILL.md 格式的 AI 技能包),目标网址: %s

每一轮你只选一个动作:
- fetch: 信息不足时,给出下一个要读的网址(url)。
- clone: 页面里有可以 git clone 的仓库地址时,填 git_url 和 name。
- write: 页面描述了这个 skill 但没有仓库时,你根据页面内容写出完整的 SKILL.md 放进 skill_md,并给 name。
- give_up: 页面和 skill 无关;在 reason 里说明。

规则:
- SKILL.md 必须以 YAML frontmatter 开头,含 name 和 description:
  ---
  name: my-skill
  description: Use when ...
  ---
  然后是正文。
- name 用小写短横线命名。
- 只根据页面内容写,不要编造功能。
- 如果上一轮 clone 失败或仓库里没有 SKILL.md,换个做法:读别的页面,或改用 write 自己写。

已知信息:%s`

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
		loop.note("直接 clone %s 没成功(%s),继续从页面找线索", rawURL, resultProblem(res))
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
			return errResult("读取安装方式失败: " + err.Error()), nil
		}

		switch strings.ToLower(strings.TrimSpace(act.Action)) {
		case "fetch":
			if err := loop.fetch(ctx, act.URL); err != nil {
				loop.note("想读 %s 但失败: %v", act.URL, err)
			}
			continue

		case "clone":
			if strings.TrimSpace(act.GitURL) == "" {
				loop.note("选了 clone 但没给仓库地址")
				continue
			}
			res, err := s.installSkillFromGit(ctx, act.GitURL, firstNonEmpty(name, act.Name))
			if err != nil {
				return res, err
			}
			if installSucceeded(res) {
				return res, nil
			}
			loop.note("clone %s 没装成: %s", act.GitURL, resultProblem(res))
			continue

		case "write":
			if strings.TrimSpace(act.SkillMD) == "" {
				loop.note("选了 write 但没给 SKILL.md 内容")
				continue
			}
			return s.writeSkill(ctx,
				firstNonEmpty(sanitizeName(name), sanitizeName(act.Name), skillNameFromURL(rawURL)),
				act.SkillMD, loop)

		default:
			return okData(map[string]interface{}{
				"installed": false,
				"reason":    firstNonEmpty(act.Reason, "无法判断安装方式"),
				"trace":     loop.trace(),
			}), nil
		}
	}

	return okData(map[string]interface{}{
		"installed": false,
		"reason":    fmt.Sprintf("试了 %d 轮仍没装上", maxInstallRounds),
		"trace":     loop.trace(),
	}), nil
}

// installSkillFromGit clones a repo into the skills directory, keeping it only
// if it really contains a SKILL.md.
func (s *Service) installSkillFromGit(ctx context.Context, gitURL, name string) (interface{}, error) {
	skill := sanitizeName(firstNonEmpty(name, skillNameFromURL(gitURL)))
	if skill == "" {
		return errResult("无法确定 skill 名称,请指定 name"), nil
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
		return errResult("仓库里没有 SKILL.md,不是一个 skill"), nil
	}
	return s.finishSkillInstall(ctx, skill, dst, "git clone", nil)
}

// writeSkill installs a skill from SKILL.md content.
func (s *Service) writeSkill(ctx context.Context, name, skillMD string, loop *installLoop) (interface{}, error) {
	name = sanitizeName(name)
	if name == "" {
		return errResult("无法确定 skill 名称,请指定 name"), nil
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
		data["note"] = "文件写下了但没能加载,SKILL.md 格式可能不对(需要 name/description frontmatter)"
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
		return "", fmt.Errorf("页面没有可读内容: %s", rawURL)
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
		return nil, "", fmt.Errorf("抓取失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("抓取 %s 返回 %d", u, resp.StatusCode)
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
	return "未装上"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(截断)"
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
