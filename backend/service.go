package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v2/pkg/agent"
	"github.com/liliang-cn/agent-go/v2/pkg/browser"
	"github.com/liliang-cn/agent-go/v2/pkg/config"
	"github.com/liliang-cn/agent-go/v2/pkg/cortexbridge"
	"github.com/liliang-cn/agent-go/v2/pkg/cortexbridge/connectorbridge"
	"github.com/liliang-cn/agent-go/v2/pkg/domain"
	"github.com/liliang-cn/agent-go/v2/pkg/pool"
	"github.com/liliang-cn/agent-go/v2/pkg/providers"
	"github.com/liliang-cn/agent-go/v2/pkg/sandbox"
	"github.com/liliang-cn/cortexdb/v2/pkg/connector"
	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// Service wraps a maximally-configured AgentGo agent.Service plus the desktop
// app's supporting pieces (sandbox, browser, cortexdb handle, life-assistant
// store, settings).
type Service struct {
	svc      *agent.Service
	sb       sandbox.Sandbox
	br       browser.Browser
	cortex   *cortexdb.DB
	settings *Settings
	store    *lifeStore

	MemoryMode string
}

// NewService builds the full SuperAI agent service from the provided settings.
func NewService(s *Settings) (*Service, error) {
	if strings.TrimSpace(s.LLMKey) == "" {
		return nil, fmt.Errorf("LLM key is empty: set it in Settings (or LLM_KEY / DASHSCOPE_API_KEY)")
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
	if embedder != nil {
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

	// --- Browser (optional; degrade gracefully if chromedp/Chrome is missing). ---
	var br browser.Browser
	if b, berr := browser.NewChromedp(browser.WithHeadless(s.Headless)); berr != nil {
		log.Printf("superai: browser disabled (%v)", berr)
	} else {
		br = b
	}

	// --- Build the agent service. ---
	b := agent.New("SuperAI").
		WithPrompt(buildPersona(time.Now())).
		WithConfig(cfg).
		WithLLM(brain).
		WithSandbox(sb).
		WithVision(true).
		WithDeliverables(true).
		WithAutonomy(agent.AutonomyProfile{MaxRounds: s.MaxRounds, Scratchpad: true}).
		WithSkills()
	if s.DisablePTC {
		b = b.WithPTC(false) // direct tool-calling for models that reject PTC's format (e.g. DashScope qwen3.x)
	}
	if br != nil {
		b = b.WithBrowser(br)
	}
	memMode := "file"
	if embedder != nil {
		b = b.WithEmbedder(embedder).WithGraphMemory()
		memMode = "graphflow"
	} else {
		b = b.WithMemory(agent.WithMemoryStoreType("file"))
	}

	svc, err := b.Build()
	if err != nil {
		_ = sb.Close()
		if br != nil {
			_ = br.Close()
		}
		return nil, fmt.Errorf("build SuperAI: %w", err)
	}

	out := &Service{
		svc: svc, sb: sb, br: br, settings: s, MemoryMode: memMode,
	}

	// --- Built-in framework tools. ---
	agent.RegisterDateTimeTool(svc)
	agent.RegisterFetchURLTool(svc)

	// --- Life-assistant store + tools (ported from examples/superai). ---
	out.store = newLifeStore(filepath.Join(cfg.DataDir(), "superai-store.json"))
	out.store.load()
	out.registerLifeTools()

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
	if s.svc != nil {
		_ = s.svc.Close()
	}
	if s.br != nil {
		_ = s.br.Close()
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
func (s *Service) Stream(ctx context.Context, sessionID, message string, emit func(ev *agent.Event)) (string, error) {
	ch, err := s.svc.RunStreamWithOptions(ctx, message,
		agent.WithSessionID(sessionID),
		agent.WithMaxTurns(s.settings.MaxRounds),
	)
	if err != nil {
		return "", err
	}
	var final string
	for ev := range ch {
		if emit != nil {
			emit(ev)
		}
		if ev.Type == agent.EventTypeComplete {
			final = ev.Content
		}
	}
	return final, nil
}

// Deliverables returns the agent's produced artifacts.
func (s *Service) Deliverables(ctx context.Context) ([]agent.Deliverable, error) {
	return s.svc.Deliverables(ctx)
}

// ReadWorkspaceFile reads a workspace-relative file via the sandbox.
func (s *Service) ReadWorkspaceFile(path string) (string, error) {
	data, err := s.svc.Sandbox().ReadFile(context.Background(), path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// InstalledSkills returns the skills discovered/installed for this service.
func (s *Service) InstalledSkills() []string { return s.svc.InstalledSkills() }

// HasBrowser reports whether a browser was successfully attached.
func (s *Service) HasBrowser() bool { return s.br != nil }

// ----------------------------------------------------------------------------
// Persona
// ----------------------------------------------------------------------------

func buildPersona(now time.Time) string {
	return fmt.Sprintf(`你是 SuperAI，一个有温度的随身 AI 生活/工作助手桌面应用。
当前系统时间：%s %s（%s），时区 %s。
凡涉及相对时间（今天/明天/后天/大后天/这周五/下周一/下下周一/下个月3号/今晚N点…），都【必须先调用 resolve_datetime 工具】换算成绝对时间，再用返回的 rfc3339 去建日程/设提醒。绝不要自己心算日期。

职责：
- 从用户的话里识别意图，主动调用工具记录：约定/会面→add_schedule；提到人→upsert_person；工作/踩坑→add_record(work,挂 project)；生活/心情→add_record(diary)；笔记→add_record(note)；打卡/习惯→add_record(habit)；要提醒→set_reminder。
- 只要用户在陈述发生的事或要求记录/提醒，必须先调用对应工具存下来再回复。
- 需要阅读/审阅某个具体网址的真实页面内容时，用 fetch_url 抓取该网页正文。
- 你有沙箱、浏览器、视觉、可交付物与技能可用，复杂任务可以自主多步完成。
- 回答用中文，简短、自然、有人情味。每条回复最后单独一行输出情绪标签，格式严格为：情绪: <中性|开心|思考|惊讶|关心|抱歉>。

严禁输出英文、日文或韩文，一律用中文回复。`,
		now.Format("2006-01-02"), now.Format("15:04:05"), weekdayCN(now), now.Format("-07:00"))
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

	svc.AddToolWithMetadata("set_reminder", "设置提醒，可周期重复（到点 SuperAI 会主动提醒）。",
		obj(map[string]any{
			"title": sp("提醒内容"), "remind_at": sp("一次性用 RFC3339；每日用 HH:MM"),
			"recurrence": sp("重复规则：daily 或 none"),
		}, "title", "remind_at"),
		func(ctx context.Context, a map[string]any) (any, error) {
			db.mu.Lock()
			rec := map[string]any{
				"id": short(uuid.NewString()), "title": str(a, "title"),
				"remind_at": str(a, "remind_at"), "recurrence": orDefault(str(a, "recurrence"), "none"),
			}
			db.Reminders = append(db.Reminders, rec)
			db.mu.Unlock()
			db.save()
			return okData(rec), nil
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
