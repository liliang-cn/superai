// Package backend hosts the AgentGo-powered SuperAI service, the avatar event
// driver, and the persisted user settings for the SuperAI desktop app.
package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Settings is the user-editable configuration, persisted as JSON at
// ~/.superai-desktop/settings.json.
type Settings struct {
	// Brain (LLM) — any OpenAI-compatible endpoint.
	LLMBaseURL string `json:"llm_base_url"`
	LLMKey     string `json:"llm_key"`
	LLMModel   string `json:"llm_model"`

	// Embeddings (optional). If EmbedKey is empty or "none", SuperAI falls back
	// to file memory so chat-only proxies still work.
	EmbedBaseURL string `json:"embed_base_url"`
	EmbedKey     string `json:"embed_key"`
	EmbedModel   string `json:"embed_model"`

	// Agent workspace (sandbox root).
	WorkspaceDir string `json:"workspace_dir"`

	// Autonomy / runtime.
	MaxRounds int  `json:"max_rounds"`
	Headless  bool `json:"headless"`

	// DisablePTC turns off Programmatic Tool Calling (model writes JS that calls
	// tools in a sandbox). PTC works great with capable models (gpt-5.x) but some
	// providers' "code" models (e.g. DashScope qwen3.x) reject its function-call
	// format — set true for them so the agent uses direct one-tool-per-round calling.
	DisablePTC bool `json:"disable_ptc"`

	// Avatar driver server port (127.0.0.1:AvatarPort).
	AvatarPort int `json:"avatar_port"`
}

const (
	dashScopeBase  = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	defaultMaxRds  = 40
	defaultAvatarP = 47615
)

// DataDir returns the SuperAI desktop data directory (~/.superai-desktop),
// honoring the SUPERAI_DESKTOP_HOME environment override.
func DataDir() string {
	if v := strings.TrimSpace(os.Getenv("SUPERAI_DESKTOP_HOME")); v != "" {
		return expandHome(v)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".superai-desktop"
	}
	return filepath.Join(home, ".superai-desktop")
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func settingsPath() string { return filepath.Join(DataDir(), "settings.json") }

// defaults returns a Settings populated with built-in defaults and environment
// fallbacks (LLM_BASE / LLM_KEY / LLM_MODEL, DASHSCOPE_API_KEY).
func defaults() *Settings {
	dash := strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY"))
	s := &Settings{
		LLMBaseURL:   envOr("LLM_BASE", dashScopeBase),
		LLMKey:       envOr("LLM_KEY", dash),
		LLMModel:     envOr("LLM_MODEL", "qwen-plus"),
		EmbedBaseURL: envOr("EMBED_BASE", dashScopeBase),
		EmbedKey:     envOr("EMBED_KEY", dash),
		EmbedModel:   envOr("EMBED_MODEL", "text-embedding-v4"),
		WorkspaceDir: filepath.Join(DataDir(), "workspace"),
		MaxRounds:    defaultMaxRds,
		Headless:     true,
		AvatarPort:   defaultAvatarP,
	}
	return s
}

// LoadSettings reads settings.json, falling back to defaults when the file is
// missing. Any zero-valued fields are backfilled with defaults so older files
// keep working after upgrades.
func LoadSettings() (*Settings, error) {
	def := defaults()
	raw, err := os.ReadFile(settingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return def, nil
		}
		return def, err
	}
	s := *def
	if err := json.Unmarshal(raw, &s); err != nil {
		return def, err
	}
	s.backfill(def)
	return &s, nil
}

func (s *Settings) backfill(def *Settings) {
	if strings.TrimSpace(s.LLMBaseURL) == "" {
		s.LLMBaseURL = def.LLMBaseURL
	}
	if strings.TrimSpace(s.LLMModel) == "" {
		s.LLMModel = def.LLMModel
	}
	if strings.TrimSpace(s.EmbedBaseURL) == "" {
		s.EmbedBaseURL = def.EmbedBaseURL
	}
	if strings.TrimSpace(s.EmbedModel) == "" {
		s.EmbedModel = def.EmbedModel
	}
	if strings.TrimSpace(s.WorkspaceDir) == "" {
		s.WorkspaceDir = def.WorkspaceDir
	}
	if s.MaxRounds <= 0 {
		s.MaxRounds = def.MaxRounds
	}
	if s.AvatarPort <= 0 {
		s.AvatarPort = def.AvatarPort
	}
}

// Save writes the settings to ~/.superai-desktop/settings.json (creating the
// data directory if needed).
func (s *Settings) Save() error {
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), raw, 0o644)
}

// UseEmbeddings reports whether an embedder should be built (graph memory) or
// SuperAI should fall back to file memory.
func (s *Settings) UseEmbeddings() bool {
	k := strings.TrimSpace(s.EmbedKey)
	return k != "" && k != "none"
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
