package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	mimepkg "mime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/config"
	"github.com/liliang-cn/agent-go/v3/pkg/cortexbridge"
	"github.com/liliang-cn/agent-go/v3/pkg/cortexbridge/connectorbridge"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/pool"
	"github.com/liliang-cn/agent-go/v3/pkg/providers"
	"github.com/liliang-cn/agent-go/v3/pkg/sandbox"
	"github.com/liliang-cn/agent-go/v3/pkg/store"
	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// UploadsSubdir is where attachments land inside the agent workspace, so
// read_document can resolve them through the sandbox as "uploads/<name>".
const UploadsSubdir = "uploads"

// Service wraps a maximally-configured AgentGo agent.Service plus the desktop
// app's supporting pieces (sandbox, cortexdb handle, life-assistant store,
// settings).
type Service struct {
	svc      *agent.Service
	sb       sandbox.Sandbox
	cortex   *cortexdb.DB
	settings *Settings
	store    *lifeStore
	// files remembers which conversation produced which workspace file, so a
	// chat shows its own deliverables rather than everything ever written.
	files *sessionFiles
	// brain is the same generator the agent runs on, kept so tools that need a
	// single structured completion (reading an install page, say) can ask the
	// model directly instead of going through a whole agent turn.
	brain domain.Generator
	// promptScheduler is injected by whoever owns the process lifetime (the app,
	// or the background daemon), because a cron loop outlives a chat turn. Tools
	// that create schedules — set_reminder — go through it. Guarded by schedMu
	// because injection races tool calls already in flight.
	schedMu         sync.Mutex
	promptScheduler *agent.PromptScheduler
	// ownedScheduler is a manage-only scheduler this service opened for itself
	// because nobody injected one (the single-shot CLI, most obviously). It
	// writes schedules to the shared store without firing any, and this service
	// closes it. Never the same object as promptScheduler.
	ownedScheduler *agent.PromptScheduler
	// dataDir is the agent's own data directory (<home>/data), where agent-go
	// keeps its sqlite database.
	dataDir string

	MemoryMode string

	// SuppressedMCPServers names the MCP servers that were left unmounted
	// because they route to the same store the memory backend already owns.
	SuppressedMCPServers []string
}

// NewService builds the full SuperAI agent service from the provided settings.
func NewService(s *Settings) (*Service, error) {
	if strings.TrimSpace(s.LLMKey) == "" || strings.TrimSpace(s.LLMBaseURL) == "" {
		return nil, fmt.Errorf("no account yet: add one under Settings → Accounts")
	}
	if strings.TrimSpace(s.LLMModel) == "" {
		return nil, fmt.Errorf("no model selected: pick one under Settings → Accounts")
	}

	// The builtin websearch MCP reads SEARXNG_BASE_URL at construction; the
	// setting is the explicit way to point this process at an instance.
	if u := strings.TrimSpace(s.SearXNGURL); u != "" {
		_ = os.Setenv("SEARXNG_BASE_URL", u)
	}

	// --- Brain (LLM): OpenAI-compatible pool. ---
	brain, err := pool.NewPool(pool.PoolConfig{
		Enabled:  true,
		Strategy: pool.StrategyRoundRobin,
		Providers: []pool.Provider{{
			Name: "brain", BaseURL: s.LLMBaseURL, Key: s.LLMKey,
			ModelName: s.LLMModel, MaxConcurrency: 5, Capability: 8,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("build brain: %w", err)
	}

	// --- Embedder (optional): graph memory when present, file memory otherwise. ---
	var embedder domain.Embedder
	if s.UseEmbeddings() {
		ep, eerr := providers.NewOpenAIEmbedderProvider(&domain.OpenAIProviderConfig{
			BaseURL: s.EmbedBaseURL, APIKey: s.EmbedKey, EmbeddingModel: s.EmbedModel,
		})
		if eerr != nil {
			log.Printf("superai: embedder init failed, falling back to file memory: %v", eerr)
		} else {
			embedder = ep
		}
	}

	// --- Config / home layout. ---
	cfg := &config.Config{Home: DataDir()}
	// The whole catalogue goes straight into the schema, superleo-style: no
	// discovery layer, no search tool. Burning rounds on tool search cost more
	// benchmark tasks than a large schema ever did.
	cfg.Tooling.DisableToolSearch = true
	// Memory backend: local by default, or the shared CortexDB brain when the
	// user picked it and an endpoint is known. The shared backend is a remote
	// store, so the local embedder plays no part in it — the server owns the
	// embedding model.
	useShared := s.UseSharedMemory()
	switch {
	case useShared:
		cfg.Memory.StoreType = store.CortexRemoteStoreType
		cfg.Memory.DSN = s.SharedMemoryEndpointResolved()
		cfg.Memory.Options = map[string]string{
			"namespace": s.SharedMemoryNamespace,
			"scope":     "global",
		}
		if tok := s.SharedMemoryTokenResolved(); tok != "" {
			cfg.Memory.Options["token"] = tok
		}
	case embedder != nil:
		cfg.Memory.StoreType = config.MemoryStoreTypeGraphFlow
	}
	cfg.ApplyHomeLayout()
	if err := os.MkdirAll(cfg.DataDir(), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}
	if err := os.MkdirAll(s.WorkspaceDir, 0o755); err != nil {
		log.Printf("superai: mkdir workspace: %v", err)
	}

	// --- Sandbox (local workspace). ---
	sb, err := sandbox.NewLocal(sandbox.WithWorkspace(s.WorkspaceDir))
	if err != nil {
		return nil, fmt.Errorf("build sandbox: %w", err)
	}

	// Browser control left the framework in agent-go v3 (pkg/browser is gone).
	// SuperAI keeps the settings fields so existing config files still parse;
	// browsing is now expected to come from an MCP server instead.
	if s.DisableBrowser || strings.TrimSpace(os.Getenv("SUPERAI_NO_BROWSER")) != "" {
		log.Printf("superai: browser disabled by configuration")
	}

	// --- Build the agent service. ---
	b := agent.New("SuperAI").
		WithPrompt(buildPersona(time.Now(), !s.DisableSelfInstall) + uiRulesSection()).
		WithConfig(cfg).
		WithLLM(brain).
		WithSandbox(sb).
		WithAutonomy(agent.AutonomyProfile{MaxRounds: s.MaxRounds, Scratchpad: true}).
		WithSkills().
		WithOptions(agent.Options{Deliverables: true})
	// agent-go v3 removed PTC entirely — tools are always called directly, so
	// the DisablePTC setting no longer selects anything. It stays in Settings
	// only so existing settings files keep parsing.
	memMode := "file"
	switch {
	case useShared:
		// The shared brain. Note there is no WithEmbedder here even when one is
		// configured: the remote server does its own embedding, and a local
		// vector would mean nothing to it.
		memOpts := []agent.MemoryOption{
			agent.WithMemoryStoreType(store.CortexRemoteStoreType),
			agent.WithMemoryDSN(cfg.Memory.DSN),
			agent.WithMemoryOptions(cfg.Memory.Options),
		}
		b = b.WithMemory(memOpts...)
		memMode = "shared:" + cfg.Memory.DSN
	case embedder != nil:
		b = b.WithEmbedder(embedder).WithGraphMemory()
		memMode = "graphflow"
	default:
		b = b.WithMemory(agent.WithMemoryStoreType("file"))
	}

	// MCP: any user-defined servers from ~/.superai-desktop/mcpServers.json.
	// Drop that file in to add MCP servers. Web search does NOT come from here —
	// it is the built-in web_search tool registered below, so a fresh install
	// with no MCP config can still look something up.
	mcpOpts := []agent.MCPOption{}
	var droppedMCP []string
	mcpCfgPath := filepath.Join(cfg.DataDir(), "mcpServers.json")
	if _, statErr := os.Stat(mcpCfgPath); statErr == nil {
		// One capability, one route: if the memory backend now owns an
		// endpoint, an MCP server pointed at that same endpoint is a second
		// name for the same store, and gets left out of the tool surface.
		owned := ""
		if useShared {
			owned = cfg.Memory.DSN
		}
		effectivePath, dropped, ferr := resolveMCPConfigPath(
			mcpCfgPath, filepath.Join(cfg.DataDir(), "mcpServers.effective.json"), owned)
		if ferr != nil {
			log.Printf("superai: mcp config filter: %v", ferr)
		}
		droppedMCP = dropped
		if len(dropped) > 0 {
			log.Printf("superai: memory backend owns %s; not mounting MCP server(s) %v that route to the same store",
				owned, dropped)
		}
		mcpOpts = append(mcpOpts, agent.WithMCPConfigPaths(effectivePath))
	}
	b = b.WithMCP(mcpOpts...)

	svc, err := b.Build()
	if err != nil {
		_ = sb.Close()
		return nil, fmt.Errorf("build SuperAI: %w", err)
	}

	out := &Service{
		svc: svc, sb: sb, settings: s, MemoryMode: memMode, dataDir: cfg.DataDir(),
		brain: brain, SuppressedMCPServers: droppedMCP,
	}

	// --- Built-in framework tools. ---
	agent.RegisterDateTimeTool(svc)
	agent.RegisterFetchURLTool(svc)
	// Looking things up is not an optional extra. Without it the agent has no
	// answer to any question about now — the weather, a price, what happened
	// today — and correctly reports itself blocked, which is a worse outcome
	// than a grounded guess. On a fresh install with no MCP servers configured
	// this was the only route to the open web, and it was not wired.
	searchBase, searchKey, searchModel := s.WebSearch()
	agent.RegisterWebSearchTool(svc, agent.WebSearchConfig{
		BaseURL: searchBase, APIKey: searchKey, Model: searchModel,
	})

	// --- Life-assistant store + tools (ported from examples/superai). ---
	out.files = newSessionFiles(filepath.Join(cfg.DataDir(), "session-files.json"))
	out.store = newLifeStore(filepath.Join(cfg.DataDir(), "superai-store.json"))
	out.store.load()
	out.registerLifeTools()
	// Self-extension: chat-driven install of skills and MCP servers, plus
	// "here's a URL, work out how to install it". Withheld for one-shot runs —
	// see Settings.DisableSelfInstall.
	if !s.DisableSelfInstall {
		out.registerInstallTools()
		out.registerURLInstallTools()
	}

	// --- CortexDB data-import + graphrag query + connector tools (best-effort). ---
	if db, derr := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(cfg.DataDir(), "cortex.db"))); derr != nil {
		log.Printf("superai: cortexdb disabled (%v)", derr)
	} else {
		out.cortex = db
		if _, e := cortexbridge.RegisterImportFlow(svc, cortexbridge.NewImporter(db, brain)); e != nil {
			log.Printf("superai: import flow tools skipped: %v", e)
		}
		if _, e := cortexbridge.Register(svc, db); e != nil {
			log.Printf("superai: graphrag tools skipped: %v", e)
		}
		if _, e := connectorbridge.Register(svc, db, connector.ToolboxOptions{}); e != nil {
			log.Printf("superai: connector tools skipped: %v", e)
		}
	}

	return out, nil
}

// Close releases the agent service, browser, sandbox, and cortexdb handle.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.store != nil {
		s.store.save()
	}
	// Only the scheduler this service opened for itself; an injected one belongs
	// to whoever injected it.
	s.schedMu.Lock()
	owned := s.ownedScheduler
	s.ownedScheduler = nil
	s.schedMu.Unlock()
	if owned != nil {
		_ = owned.Stop()
	}
	if s.svc != nil {
		_ = s.svc.Close()
	}
	if s.sb != nil {
		_ = s.sb.Close()
	}
	if s.cortex != nil {
		_ = s.cortex.Close()
	}
	return nil
}

// Stream runs one turn, forwarding every agent event to emit, and returns the
// final completion text.
func (s *Service) Stream(ctx context.Context, sessionID, message string, imagePaths []string, emit func(ev *agent.Event)) (string, error) {
	opts := []agent.RunOption{
		agent.WithSessionID(sessionID),
		agent.WithMaxTurns(s.settings.MaxRounds),
	}
	// Route dropped images straight to the vision model as multimodal input
	// (workspace-relative paths -> absolute, so the provider can read them).
	if len(imagePaths) > 0 {
		root := ""
		if s.sb != nil {
			root = s.sb.Workspace()
		}
		abs := make([]string, 0, len(imagePaths))
		for _, p := range imagePaths {
			if filepath.IsAbs(p) || root == "" {
				abs = append(abs, p)
			} else {
				abs = append(abs, filepath.Join(root, p))
			}
		}
		opts = append(opts, agent.WithInputImages(abs...))
	}
	// Whatever the turn writes into the workspace belongs to this conversation.
	root := ""
	if s.sb != nil {
		root = s.sb.Workspace()
	}
	before := snapshotWorkspace(root)
	defer func() {
		if s.files != nil {
			// An attachment imported during this turn also shows up as new, and it
			// is the one thing here the agent did not produce.
			changed := changedFiles(before, snapshotWorkspace(root))
			kept := changed[:0]
			for _, p := range changed {
				if !s.files.isImported(p) {
					kept = append(kept, p)
				}
			}
			s.files.record(sessionID, kept)
		}
	}()

	ch, err := s.svc.RunStreamWithOptions(ctx, message, opts...)
	if err != nil {
		return "", err
	}
	var final string
	var lastErr string
	sawTerminal := false
	for ev := range ch {
		if emit != nil {
			emit(ev)
		}
		switch ev.Type {
		// A blocked run is an outcome, not an error: its explanation arrives as
		// EventTypeBlocked and is the answer the user should see.
		case agent.EventTypeComplete, agent.EventTypeBlocked:
			final = ev.Content
			sawTerminal = true
		case agent.EventTypeError:
			lastErr = ev.Content
		}
	}
	// A run that ended on an error event and never reached a terminal state
	// used to come back as ("", nil) — the caller saw "no answer" with the
	// reason silently dropped (a gateway 502 looked identical to a model that
	// said nothing). The error event is the explanation; return it as one.
	if !sawTerminal && lastErr != "" {
		return "", fmt.Errorf("%s", lastErr)
	}
	return final, nil
}

// Deliverables returns the agent's produced artifacts.
func (s *Service) Deliverables(ctx context.Context, sessionID string) ([]agent.Deliverable, error) {
	all, err := s.svc.Deliverables(ctx)
	if err != nil {
		return nil, err
	}
	// An empty session id means "everything" (used by tests and any caller that
	// has no conversation in hand).
	var owned map[string]bool
	if strings.TrimSpace(sessionID) != "" && s.files != nil {
		owned = map[string]bool{}
		for _, p := range s.files.forSession(sessionID) {
			owned[p] = true
		}
	}
	imported := func(string) bool { return false }
	if s.files != nil {
		imported = s.files.isImported
	}
	return keepDeliverables(all, owned, imported), nil
}

// keepDeliverables decides which artifacts belong to a conversation.
//
// What the user handed in is not something the agent produced, so it does not
// belong in the list. Judged per file rather than by directory: the agent writes
// its output beside its input, so excluding all of uploads/ also hid the
// converted copy of an attachment — the one file the user then goes looking for.
//
// owned is nil for the "everything" caller, which has no conversation to compare
// against; there, a path in uploads/ with no import record is still assumed to be
// an attachment, since it predates the record being kept.
func keepDeliverables(
	all []agent.Deliverable,
	owned map[string]bool,
	isImported func(string) bool,
) []agent.Deliverable {
	out := make([]agent.Deliverable, 0, len(all))
	for _, d := range all {
		rel := filepath.ToSlash(d.Path)
		if isImported(rel) {
			continue
		}
		if owned != nil {
			if !owned[rel] {
				continue
			}
		} else if strings.HasPrefix(rel, UploadsSubdir+"/") {
			continue
		}
		out = append(out, d)
	}
	return out
}

// ReadWorkspaceFile reads a workspace-relative file via the sandbox.
func (s *Service) ReadWorkspaceFile(path string) (string, error) {
	data, err := s.svc.Sandbox().ReadFile(context.Background(), path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadWorkspaceFileDataURL returns a workspace file as a data: URL, so the
// webview can render it natively — PDFs in an <embed>, images in an <img> —
// instead of the UI dumping raw bytes as text.
func (s *Service) ReadWorkspaceFileDataURL(path string) (string, error) {
	data, err := s.svc.Sandbox().ReadFile(context.Background(), path)
	if err != nil {
		return "", err
	}
	mime := mimeTypeFor(path)
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// mimeTypeFor guesses a content type from the extension, falling back to a
// generic binary type the webview will simply offer to download.
func mimeTypeFor(path string) string {
	if t := mimepkg.TypeByExtension(strings.ToLower(filepath.Ext(path))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// OpenWorkspaceFileExternal hands a workspace file to the OS default
// application — the escape hatch for formats the webview cannot render
// (Word, Excel, PowerPoint, archives).
func (s *Service) OpenWorkspaceFileExternal(path string) error {
	root := ""
	if s.sb != nil {
		root = s.sb.Workspace()
	}
	full := path
	if !filepath.IsAbs(full) && root != "" {
		full = filepath.Join(root, path)
	}
	if _, err := os.Stat(full); err != nil {
		return err
	}
	return exec.Command("open", full).Start()
}

// UsePromptScheduler hands the service the scheduler its tools should create
// schedules in — the one whose process owns the cron loop.
func (s *Service) UsePromptScheduler(sch *agent.PromptScheduler) {
	if s == nil {
		return
	}
	s.schedMu.Lock()
	s.promptScheduler = sch
	s.schedMu.Unlock()
}

// reminderScheduler returns something that can write a schedule.
//
// A reminder is data. Firing it is a cron loop's job, and the two are separate
// concerns that happen to share a database: agent-go keeps schedules in the
// same store as the sessions, so any process pointed at this home can create
// one and whichever process owns execution picks it up.
//
// This used to refuse outright when nobody had injected a running scheduler,
// which made "set a reminder" fail in the single-shot CLI — no window, no
// daemon, no cron loop, and therefore, absurdly, no way to write a row. A
// benchmark task was lost to it. Now the fallback opens the same store in
// manage-only mode: the reminder is stored, nothing is fired here, and the app
// or daemon rings it when the time comes.
func (s *Service) reminderScheduler() (*agent.PromptScheduler, error) {
	if s == nil {
		return nil, fmt.Errorf("no service")
	}
	s.schedMu.Lock()
	defer s.schedMu.Unlock()
	if s.promptScheduler != nil {
		return s.promptScheduler, nil
	}
	if s.ownedScheduler != nil {
		return s.ownedScheduler, nil
	}
	if s.svc == nil {
		return nil, fmt.Errorf("定时功能未启动")
	}
	sch, err := s.svc.NewPromptScheduler(agent.WithPromptSessionID("scheduled"))
	if err != nil {
		return nil, err
	}
	// Manage-only: this process must not fire schedules. If it did, a CLI run
	// and the desktop app would both ring the same reminder.
	if err := sch.StartManageOnly(); err != nil {
		return nil, err
	}
	s.ownedScheduler = sch
	return sch, nil
}

// NoteImported records files the host copied into the workspace on the user's
// behalf, so the deliverables list can tell them from what the agent produced
// even when they share a directory.
func (s *Service) NoteImported(paths []string) {
	if s == nil || s.files == nil {
		return
	}
	s.files.noteImported(paths)
}

// Agent exposes the underlying agent service, for capabilities this package
// does not wrap — the prompt scheduler being the first, since its lifetime
// belongs to the host rather than to a chat turn.
func (s *Service) Agent() *agent.Service {
	if s == nil {
		return nil
	}
	return s.svc
}

// InstalledSkills returns the skills discovered/installed for this service.
func (s *Service) InstalledSkills() []string { return s.svc.InstalledSkills() }

// HasBrowser reports whether browsing is available. Always false since
// agent-go v3 dropped pkg/browser; wire an MCP browser server instead.
func (s *Service) HasBrowser() bool { return false }

// ----------------------------------------------------------------------------
// Persona
// ----------------------------------------------------------------------------

// selfInstallSection is the "go and acquire what you lack" instruction. It is
// only included when those tools are actually registered — a prompt that tells
// the model to call search_mcp_servers when the schema has no such tool is an
// invitation to waste a round discovering that.
const selfInstallSection = `
- 【能力不够时自己去装】任务需要你现在没有的能力时，不要直接说做不到：
  · 缺工具（连数据库、访问某个服务、操作某类文件…）→ search_mcp_servers 搜，把结果里的 command/args 原样交给 add_mcp_server 装上再继续。
  · 缺专门知识或固定流程（某语言的最佳实践、某种文档的写法…）→ search_skills 搜，用 install_skill 的 source_path 装上再继续。
  · 搜到的 server 若有 required_env（API key 之类），先向用户要，别拿空值去装。
  · 装完直接接着做原来的任务，不用等用户再说一次。同一个能力只装一次，装之前先看已有的工具够不够。`

func buildPersona(now time.Time, selfInstall bool) string {
	installHint := ""
	if selfInstall {
		installHint = selfInstallSection
	}
	return fmt.Sprintf(`你是 SuperAI，一个有温度的随身 AI 生活/工作助手桌面应用。
当前系统时间：%s %s（%s），时区 %s。
凡涉及相对时间（今天/明天/后天/大后天/这周五/下周一/下下周一/下个月3号/今晚N点…），都【必须先调用 resolve_datetime 工具】换算成绝对时间，再用返回的 rfc3339 去建日程/设提醒。绝不要自己心算日期。

职责：
- 从用户的话里识别意图，主动调用工具记录：约定/会面→add_schedule；提到人→upsert_person；工作/踩坑→add_record(work,挂 project)；生活/心情→add_record(diary)；笔记→add_record(note)；打卡/习惯→add_record(habit)；要提醒→set_reminder。
- 只要用户在陈述发生的事或要求记录/提醒，必须先调用对应工具存下来再回复。
- 需要查最新/实时/记忆里没有的信息（天气、行情、新闻、事实核对…）时，用 web_search 搜；需要阅读/审阅某个具体网址的真实页面内容时，用 fetch_url 抓取该网页正文。
- 你有沙箱、浏览器、视觉、可交付物与技能可用，复杂任务可以自主多步完成。%s
- 回答用中文，简短、自然、有人情味。每条回复最后单独一行输出情绪标签，格式严格为：情绪: <中性|开心|思考|惊讶|关心|抱歉>。

严禁输出英文、日文或韩文，一律用中文回复。`,
		now.Format("2006-01-02"), now.Format("15:04:05"), weekdayCN(now), now.Format("-07:00"), installHint)
}

// uiRulesSection appends the transcript's rendering rules to the persona, so
// the model knows which rich blocks the UI can actually draw. Empty until the
// frontend has announced them once (see uirules.go).
func uiRulesSection() string {
	rules := LoadUIRules()
	if rules == "" {
		return ""
	}
	return "\n\n" + rules
}

func weekdayCN(t time.Time) string {
	return "周" + []string{"日", "一", "二", "三", "四", "五", "六"}[int(t.Weekday())]
}

// ----------------------------------------------------------------------------
// Life-assistant store (ported from examples/superai)
// ----------------------------------------------------------------------------

type lifeStore struct {
	mu        sync.Mutex
	path      string
	Schedules []map[string]any          `json:"schedules"`
	Records   []map[string]any          `json:"records"`
	Persons   map[string]map[string]any `json:"persons"`
	Reminders []map[string]any          `json:"reminders"`
}

func newLifeStore(path string) *lifeStore {
	return &lifeStore{path: path, Persons: map[string]map[string]any{}}
}

func (db *lifeStore) load() {
	raw, err := os.ReadFile(db.path)
	if err != nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	_ = json.Unmarshal(raw, db)
	if db.Persons == nil {
		db.Persons = map[string]map[string]any{}
	}
}

func (db *lifeStore) save() {
	db.mu.Lock()
	raw, err := json.MarshalIndent(db, "", "  ")
	db.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(db.path, raw, 0o644)
}

func okData(data any) map[string]any { return map[string]any{"ok": true, "data": data} }

// ----------------------------------------------------------------------------
// Life-assistant tools
// ----------------------------------------------------------------------------

func (s *Service) registerLifeTools() {
	svc, db := s.svc, s.store

	str := func(a map[string]any, k string) string {
		if v, ok := a[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	strSlice := func(a map[string]any, k string) []string {
		out := []string{}
		if raw, ok := a[k].([]any); ok {
			for _, v := range raw {
				if sv, ok := v.(string); ok && strings.TrimSpace(sv) != "" {
					out = append(out, strings.TrimSpace(sv))
				}
			}
		}
		return out
	}
	write := agent.ToolMetadata{InterruptBehavior: agent.InterruptBehaviorBlock}
	read := agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true, InterruptBehavior: agent.InterruptBehaviorCancel}

	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}
	sp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	arr := func(desc string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	}

	svc.AddToolWithMetadata("add_schedule", "新建一条日程/约会。时间请用 RFC3339 绝对时间（先用 resolve_datetime 换算）。",
		obj(map[string]any{
			"title": sp("日程标题"), "start_at": sp("开始时间 RFC3339"),
			"location": sp("地点"), "participants": arr("参与人姓名"),
		}, "title", "start_at"),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			rec := map[string]any{
				"id": short(uuid.NewString()), "title": str(a, "title"), "start_at": str(a, "start_at"),
				"location": str(a, "location"), "participants": strSlice(a, "participants"),
			}
			db.Schedules = append(db.Schedules, rec)
			db.mu.Unlock()
			db.save()
			return okData(rec), nil
		}, write)

	svc.AddToolWithMetadata("list_schedules", "列出全部日程。", obj(map[string]any{}),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			defer db.mu.Unlock()
			return okData(db.Schedules), nil
		}, read)

	svc.AddToolWithMetadata("add_record", "记录一条内容：日记/工作/笔记/习惯。",
		obj(map[string]any{
			"type": sp("类型：diary|work|note|habit"), "title": sp("简短标题"),
			"body": sp("正文内容"), "tags": arr("标签"), "project": sp("所属项目（工作记录用）"),
		}, "type", "body"),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			rec := map[string]any{
				"id": short(uuid.NewString()), "type": str(a, "type"), "title": str(a, "title"),
				"body": str(a, "body"), "tags": strSlice(a, "tags"), "project": str(a, "project"),
				"occurred_at": time.Now().Format(time.RFC3339),
			}
			db.Records = append(db.Records, rec)
			db.mu.Unlock()
			db.save()
			return okData(rec), nil
		}, write)

	svc.AddToolWithMetadata("search_records", "按关键词检索记录，可选按 type 过滤。",
		obj(map[string]any{"query": sp("关键词"), "type": sp("可选：diary|work|note|habit")}, "query"),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			defer db.mu.Unlock()
			q, typ := strings.ToLower(str(a, "query")), str(a, "type")
			hits := []map[string]any{}
			for _, r := range db.Records {
				if typ != "" && r["type"] != typ {
					continue
				}
				blob := strings.ToLower(fmt.Sprintf("%v %v %v %v", r["title"], r["body"], r["tags"], r["project"]))
				if q == "" || strings.Contains(blob, q) {
					hits = append(hits, r)
				}
			}
			return okData(hits), nil
		}, read)

	svc.AddToolWithMetadata("upsert_person", "新建或更新一个人物档案（关系、偏好、最近动态）。",
		obj(map[string]any{
			"name": sp("姓名"), "relation": sp("关系，如同事/朋友/室友"), "note": sp("偏好或最近动态"),
		}, "name"),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			name := str(a, "name")
			p := db.Persons[name]
			if p == nil {
				p = map[string]any{"name": name}
			}
			if v := str(a, "relation"); v != "" {
				p["relation"] = v
			}
			if v := str(a, "note"); v != "" {
				p["note"] = v
			}
			db.Persons[name] = p
			db.mu.Unlock()
			db.save()
			return okData(p), nil
		}, write)

	// A reminder is a scheduled prompt. Until the scheduler existed this tool
	// only appended a row to a JSON file while telling the model "SuperAI will
	// remind you when it is due" — a promise nothing kept.
	svc.AddToolWithMetadata("set_reminder",
		"设置提醒，到点会真的触发（后台常驻时即使 app 关着也会）。时间用 HH:MM（每天）或 RFC3339。",
		obj(map[string]any{
			"title":      sp("提醒内容；也可以是要先做的事，如「看看昨天的部署有没有问题」"),
			"remind_at":  sp("每天用 HH:MM（如 08:00）；具体某天用 RFC3339"),
			"recurrence": sp("daily 或 none；HH:MM 默认 daily"),
		}, "title", "remind_at"),
		func(ctx context.Context, a map[string]any) (any, error) {
			title := strings.TrimSpace(str(a, "title"))
			if title == "" {
				return errResult("title is required"), nil
			}
			plan, err := ReminderToCron(str(a, "remind_at"), str(a, "recurrence"))
			if err != nil {
				return errResult(err.Error()), nil
			}

			sch, err := s.reminderScheduler()
			if err != nil {
				return errResult("提醒功能不可用：" + err.Error()), nil
			}
			task, err := sch.Schedule(ReminderPrompt(title), plan.Cron, "提醒："+title, "reminders")
			if err != nil {
				return errResult(err.Error()), nil
			}

			data := map[string]any{
				"id": task.ID, "title": title, "schedule": plan.Cron, "when": plan.Note,
			}
			if task.NextRun != nil {
				data["next_run"] = task.NextRun.Local().Format("2006-01-02 15:04")
			}
			// Keep a row too, or list_reminders answers "none" for a reminder
			// the user just watched get created.
			db.mu.Lock()
			db.Reminders = append(db.Reminders, map[string]any{
				"id": task.ID, "title": title, "schedule": plan.Cron, "when": plan.Note,
				"created_at": time.Now().Format(time.RFC3339),
			})
			db.mu.Unlock()
			db.save()
			if plan.OneShot {
				// Said plainly rather than hidden: cron cannot express "once", so
				// the user should know this will come back next year.
				data["note"] = "cron 无法表达「只一次」，这条会每年同一天重复；不需要时删掉它。"
			}
			return okData(data), nil
		}, write)

	svc.AddToolWithMetadata("list_reminders", "列出全部提醒。", obj(map[string]any{}),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			defer db.mu.Unlock()
			return okData(db.Reminders), nil
		}, read)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// SplitEmotion peels the trailing "情绪: X" tag off a reply, returning the
// cleaned reply plus the emotion (used for avatar emotion events).
func SplitEmotion(text string) (reply, emotion string) {
	text = strings.TrimRight(text, " \t\r\n")
	for _, marker := range []string{"情绪:", "情绪："} {
		if i := strings.LastIndex(text, marker); i >= 0 {
			emotion = strings.TrimSpace(text[i+len(marker):])
			if nl := strings.IndexAny(emotion, "\r\n"); nl >= 0 {
				emotion = strings.TrimSpace(emotion[:nl])
			}
			reply = strings.TrimRight(text[:i], " \t\r\n")
			reply = strings.TrimSuffix(reply, "\\n")
			reply = strings.TrimRight(reply, " \t\r\n")
			return reply, emotion
		}
	}
	return text, ""
}
