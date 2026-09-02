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
	// claims stops two schedule-writing tools from both answering one request.
	// See schedule_once.go.
	claims *scheduleClaims
	// ownedScheduler is a manage-only scheduler this service opened for itself
	// because nobody injected one (the single-shot CLI, most obviously). It
	// writes schedules to the shared store without firing any, and this service
	// closes it. Never the same object as promptScheduler.
	ownedScheduler *agent.PromptScheduler
	// dataDir is the agent's own data directory (<home>/data), where agent-go
	// keeps its sqlite database.
	dataDir string
	// gate authorizes tool calls. It is installed on the agent at build time
	// and stays reachable afterwards so whoever owns a UI can hand it an
	// approver — the service is built before the window knows about it, and a
	// gate with no approver denies rather than allows.
	gate *ToolGate
	// plans is the store the agent's scratchpad persists to, kept so the run
	// wall can read a task's plan back without going through the agent.
	plans *GraphPlanStore

	MemoryMode string

	// usage counts model-turn tokens across every entry point, for the
	// dashboard. Fed by an observer registered at build time; see dashboard.go.
	usage *usageTracker

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
	// Must run before the agent is built: that is when agent-go reads the
	// embedding pool out of agentgo.db, and settings.json is not one of its
	// sources. Never fatal — embeddings are an enhancement, and refusing to
	// start over one would be worse than starting without it.
	if err := syncEmbeddingProvider(cfg, s); err != nil {
		log.Printf("superai: embedding settings not applied to the pool: %v", err)
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

	// --- Tool approval gate. ---
	// Built before the agent because the handler is installed as part of the
	// build: agent-go only consults SetPermissionHandler's handler, and a
	// service that gets one later has already run whatever it ran in between.
	// Default ON — see Settings.DisableToolApproval for why the setting is
	// phrased as a disable.
	gate := NewToolGate(!s.DisableToolApproval, AuditLogPath())

	// --- Plan persistence. ---
	//
	// Deliberately not the brain: a run's steps are what happened once, and a
	// few thousand of them would bury the knowledge they sit next to.
	planStore := NewGraphPlanStore(filepath.Join(cfg.DataDir(), "plans"))

	// --- Build the agent service. ---
	b := agent.New("SuperAI").
		WithPrompt(buildPersona(time.Now(), !s.DisableSelfInstall) + uiRulesSection()).
		WithConfig(cfg).
		WithLLM(brain).
		WithSandbox(sb).
		WithAutonomy(agent.AutonomyProfile{MaxRounds: s.MaxRounds, Scratchpad: true}).
		WithSkills().
		// A plan that outlives the process. Without it a long task that is
		// interrupted comes back having forgotten what it was doing — which is
		// the moment the plan is the only thing worth keeping. Each task gets
		// its own graph under <data>/plans, which is also what serve_graph_3d
		// renders, so a run can be watched filling in its own plan.
		WithPlanStore(planStore).
		WithOptions(agent.Options{
			Deliverables: true,
			// Both, always. agent-go treats a nil policy next to a non-nil
			// handler as "gate everything", which would put a prompt in front
			// of every memory write; and a nil handler makes authorizeTool a
			// no-op, which is the bug this closes.
			PermissionHandler: gate.Handler(),
			PermissionPolicy:  gate.Policy(),
		})
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
		claims: newScheduleClaims(),
		brain:  brain, SuppressedMCPServers: droppedMCP,
		gate: gate, plans: planStore,
	}
	// Token accounting for the dashboard: one observer sees every model turn,
	// whichever entry point started the run.
	out.usage = newUsageTracker(filepath.Join(cfg.DataDir(), "usage.json"))
	svc.RegisterObserver(&usageObserver{u: out.usage})

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
	out.registerNotifyTool()
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

// ToolGate is the approval gate this service's agent runs behind. Whoever has
// a user in front of them installs an approver on it; nobody else has to, and
// the gate denies gated calls when nobody does.
func (s *Service) ToolGate() *ToolGate {
	if s == nil {
		return nil
	}
	return s.gate
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
		return nil, fmt.Errorf("the scheduler is not running")
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
- When you lack a capability, go and get it — a task that needs something you do not have yet is not a task you refuse:
  · Missing a tool (reach a database, call a service, handle a kind of file…) → search_mcp_servers, hand the command/args from the result to add_mcp_server verbatim, install, carry on.
  · Missing specialist knowledge or a set procedure (a language's best practice, how a kind of document is written…) → search_skills, install with install_skill's source_path, carry on.
  · If the server you found lists required_env (an API key and the like), ask the user for it first; never install with empty values.
  · Once installed, resume the original task without waiting to be asked again. Install a capability once, and check the tools you already have before installing anything.`

func buildPersona(now time.Time, selfInstall bool) string {
	installHint := ""
	if selfInstall {
		installHint = selfInstallSection
	}
	return fmt.Sprintf(`You are SuperAI, a warm everyday AI assistant for life and work, running as a desktop app.
Current system time: %s %s (%s), timezone %s.
For any relative time (today, tomorrow, the day after tomorrow, this Friday, next Monday, the Monday after next, the 3rd of next month, N o'clock tonight…) you MUST call resolve_datetime first to turn it into an absolute time, then use the rfc3339 it returns to create the schedule or reminder. Never work a date out in your head.

Duties:
- Read the intent behind what the user says and record it unprompted: a plan or meeting→add_schedule; a person mentioned→upsert_person; work or a problem hit→add_record(work, with project); life or mood→add_record(diary); a note→add_record(note); a check-in or habit→add_record(habit).
- Scheduling: call exactly ONE tool. Every day at HH:MM, or one specific moment → set_reminder. Any other cadence — a weekday, every Monday, every few hours, a day of the month → schedule_prompt, which takes a cron and can say what the other cannot. Calling both leaves two schedules for one request, and one of them is always wrong.
- Whenever the user states something that happened, or asks to be reminded or to have something recorded, store it with the matching tool before you reply.
- When you need something current, live, or not in memory (weather, markets, news, fact-checking…), search with web_search; when you need to read or review the real content of a specific URL, fetch that page's text with fetch_url.
- You have a sandbox, a browser, vision, deliverables and skills; take complex tasks through as many steps as they need on your own.%s
- Answer in Chinese: short, natural, human. End every reply with the emotion tag alone on the last line, in exactly this format: 情绪: <中性|开心|思考|惊讶|关心|抱歉>.

Never answer in English, Japanese or Korean — always Chinese.`,
		now.Format("2006-01-02"), now.Format("15:04:05"), now.Format("Monday"), now.Format("-07:00"), installHint)
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

	svc.AddToolWithMetadata("add_schedule", "Create a calendar entry or appointment. Give the time as an absolute RFC3339 timestamp (resolve it with resolve_datetime first).",
		obj(map[string]any{
			"title": sp("Title of the entry"), "start_at": sp("Start time, RFC3339"),
			"location": sp("Location"), "participants": arr("Names of the participants"),
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

	svc.AddToolWithMetadata("list_schedules", "List every calendar entry.", obj(map[string]any{}),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			defer db.mu.Unlock()
			return okData(db.Schedules), nil
		}, read)

	svc.AddToolWithMetadata("add_record", "Record one entry: diary, work, note or habit.",
		obj(map[string]any{
			"type": sp("Kind: diary|work|note|habit"), "title": sp("Short title"),
			"body": sp("Body text"), "tags": arr("Tags"), "project": sp("Project it belongs to (for work records)"),
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

	svc.AddToolWithMetadata("search_records", "Search records by keyword, optionally filtered by type.",
		obj(map[string]any{"query": sp("Keyword"), "type": sp("Optional: diary|work|note|habit")}, "query"),
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

	svc.AddToolWithMetadata("upsert_person", "Create or update a person's profile (relationship, preferences, what they are up to).",
		obj(map[string]any{
			"name": sp("Name"), "relation": sp("Relationship, e.g. colleague/friend/flatmate"), "note": sp("Preferences or recent news"),
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
		"Set a reminder that really fires when it comes due (even with the app closed, as long as the background service is resident). Give the time as HH:MM (every day) or RFC3339.",
		obj(map[string]any{
			"title":      sp("What to remind about; it may also be something to do first, e.g. \"check whether yesterday's deploy is healthy\""),
			"remind_at":  sp("HH:MM for every day (e.g. 08:00); RFC3339 for one specific day"),
			"recurrence": sp("daily or none; HH:MM defaults to daily"),
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
				return errResult("reminders are unavailable: " + err.Error()), nil
			}
			// One request, one schedule — see schedule_once.go. What is already
			// scheduled is asked first: the in-memory claim below cannot see a
			// repeat from an hour ago or anything at all from before a restart.
			// A listing that fails is not a reason to refuse a reminder, so the
			// claim still stands behind it.
			if existing, lerr := sch.List(); lerr == nil {
				if held := alreadyScheduled(existing, title); held != nil {
					return errResult(duplicateScheduleMessage(held)), nil
				}
			}
			if held := s.claims.claim(title, plan.Cron); held != nil {
				return errResult(duplicateScheduleMessage(held)), nil
			}
			task, err := sch.Schedule(ReminderPrompt(title), plan.Cron, reminderNote(title, plan.OneShot), "reminders")
			if err != nil {
				s.claims.release(title)
				return errResult(err.Error()), nil
			}
			s.claims.record(title, task.ID, plan.Cron)

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
				// cron cannot say "once", so this is stored as the same day every
				// year and removed as soon as it has fired — see reminder_once.go.
				// Said plainly because a reminder that deletes itself is a
				// surprise if you go looking for it afterwards.
				data["note"] = "This fires once and is deleted automatically after it has fired."
			}
			return okData(data), nil
		}, write)

	// set_reminder covers "every day at HH:MM" and one specific moment, which is
	// most of what a person asks for and not all of it. "工作日下午三点"、"每四小时"、
	// "每月一号" have no shape in that tool, and a model asked for them either
	// picks the nearest daily time or gives up. This one takes the cron directly.
	svc.AddToolWithMetadata("schedule_prompt",
		"Run a prompt on any cadence: weekdays, every few hours, a day of the month. Use set_reminder instead only when the timing is every day at HH:MM, or one specific moment. Never call both for one request.",
		obj(map[string]any{
			"prompt":       sp("The instruction to run when it fires, written as you would type it into chat"),
			"cron":         sp("Five-field cron. Daily at 08:00 `0 8 * * *`; weekdays at 09:00 `0 9 * * 1-5`; every four hours `0 */4 * * *`; the 1st at 09:00 `0 9 1 * *`"),
			"name":         sp("Optional label for the list; defaults to the prompt"),
			"conversation": sp("Optional conversation the runs append to; blank shares one called scheduled"),
			"once": map[string]any{
				"type":        "boolean",
				"description": "True when the user named one specific moment rather than a repeating cadence. cron cannot say \"once\", so a single moment has to be stored as that day every year; with this set the schedule is deleted as soon as it has fired, instead of coming back a year later.",
			},
		}, "prompt", "cron"),
		func(ctx context.Context, a map[string]any) (any, error) {
			prompt := strings.TrimSpace(str(a, "prompt"))
			if prompt == "" {
				return errResult("prompt is required"), nil
			}
			sch, err := s.reminderScheduler()
			if err != nil {
				return errResult("scheduling is unavailable: " + err.Error()), nil
			}
			if existing, lerr := sch.List(); lerr == nil {
				if held := alreadyScheduled(existing, prompt); held != nil {
					return errResult(duplicateScheduleMessage(held)), nil
				}
			}
			if held := s.claims.claim(prompt, strings.TrimSpace(str(a, "cron"))); held != nil {
				return errResult(duplicateScheduleMessage(held)), nil
			}
			once, _ := a["once"].(bool)
			label := strings.TrimSpace(str(a, "name"))
			if label == "" {
				label = prompt
			}
			task, err := sch.Schedule(prompt, strings.TrimSpace(str(a, "cron")),
				markOneShot(label, once), strings.TrimSpace(str(a, "conversation")))
			if err != nil {
				s.claims.release(prompt)
				return errResult(err.Error()), nil
			}
			s.claims.record(prompt, task.ID, task.Schedule)
			data := map[string]any{"id": task.ID, "prompt": prompt, "schedule": task.Schedule}
			if once {
				data["note"] = "This fires once and is deleted automatically after it has fired."
			}
			// The next run is the only part a person can check against what they
			// meant. A cron they cannot read is not a confirmation.
			if task.NextRun != nil {
				data["next_run"] = task.NextRun.Local().Format("2006-01-02 15:04 MST")
			}
			return okData(data), nil
		}, write)

	svc.AddToolWithMetadata("list_reminders", "List every reminder.", obj(map[string]any{}),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			rows := append([]map[string]any(nil), db.Reminders...)
			db.mu.Unlock()
			return okData(s.reconcileReminders(rows)), nil
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

// Plan returns the current plan for a task, or nil.
//
// RunSegments scopes an unnamed plan to its task id under the scratchpad's
// default key, which is how two long tasks against one store stay apart.
func (s *Service) Plan(ctx context.Context, taskID string) []agent.PlanItem {
	if s == nil || s.plans == nil || taskID == "" {
		return nil
	}
	items, err := s.plans.LoadPlan(ctx, "default:"+taskID)
	if err != nil {
		return nil
	}
	return items
}
