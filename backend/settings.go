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

	// Web search. The built-in web_search tool asks a search-capable,
	// OpenAI-compatible chat endpoint for a grounded answer. Left empty, these
	// fall back to the brain's own endpoint — a gateway that serves a
	// search-grounded model answers the same request, and an agent that cannot
	// look anything up is stuck on any question about now.
	SearchBaseURL string `json:"search_base_url"`
	SearchKey     string `json:"search_key"`
	SearchModel   string `json:"search_model"`

	// Agent workspace (sandbox root).
	WorkspaceDir string `json:"workspace_dir"`

	// Autonomy / runtime.
	MaxRounds int  `json:"max_rounds"`
	Headless  bool `json:"headless"`

	// DisableBrowser is retained for settings-file compatibility. agent-go v3
	// removed pkg/browser, so SuperAI no longer attaches a browser at all;
	// wire an MCP browser server if browsing is needed.
	DisableBrowser bool `json:"disable_browser"`

	// DisablePTC turns off Programmatic Tool Calling (the model writes JS that
	// drives tools in a sandbox, so one round does several tool calls).
	//
	// Default OFF — PTC is on. Measured with gpt-5.5 on 2026-07-29
	// (backend/ptc_rounds_test.go counts provider calls through a proxy):
	//
	//	                 PTC on   direct
	//	provider calls        5        8
	//	prompt tokens    39,957   96,614
	//	completion tok      638      415
	//	wall clock         26.8s    33.4s
	//
	// Every round re-sends the whole history plus every tool definition, so
	// fewer rounds is mostly a prompt-token win: ~59% cheaper here, and faster
	// too. Set this true for a model that rejects PTC's format.
	DisablePTC bool `json:"disable_ptc"`

	// PIIRedaction is currently a no-op: agent-go v3 removed the built-in PII
	// guardrail along with pkg/browser. The field is kept so existing settings
	// files still parse; re-implement redaction here (or in a provider proxy)
	// before advertising it again.
	PIIRedaction bool `json:"pii_redaction"`

	// Avatar driver server port (127.0.0.1:AvatarPort).
	AvatarPort int `json:"avatar_port"`

	// CLIProxyEnabled runs an embedded CLIProxyAPI (127.0.0.1:CLIProxyPort) and
	// routes the brain through it, so the agent uses the Claude Code / Codex /
	// Gemini CLI subscriptions whose credentials live in <data>/cliproxy/auths
	// instead of LLMBaseURL + LLMKey. LLMModel still selects the model — it must
	// be one the proxy serves (see the model list in Settings).
	CLIProxyEnabled bool `json:"cliproxy_enabled"`

	// CLIProxyPort is the local port the embedded proxy binds (loopback only).
	CLIProxyPort int `json:"cliproxy_port"`
}

const (
	defaultMaxRds  = 40
	defaultAvatarP = 47615
	// DefaultCLIProxyPort is deliberately off the well-known range to avoid
	// colliding with dev servers.
	DefaultCLIProxyPort = 43517
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
// fallbacks (LLM_BASE / LLM_KEY / LLM_MODEL, EMBED_BASE / EMBED_KEY /
// EMBED_MODEL). Out of the box SuperAI runs on the embedded CLI proxy, so no
// cloud endpoint or API key is assumed.
func defaults() *Settings {
	s := &Settings{
		LLMBaseURL:      envOr("LLM_BASE", ""),
		LLMKey:          envOr("LLM_KEY", ""),
		LLMModel:        envOr("LLM_MODEL", ""),
		EmbedBaseURL:    envOr("EMBED_BASE", ""),
		EmbedKey:        envOr("EMBED_KEY", ""),
		EmbedModel:      envOr("EMBED_MODEL", ""),
		SearchBaseURL:   envOr("SEARCH_BASE", ""),
		SearchKey:       envOr("SEARCH_KEY", ""),
		SearchModel:     envOr("SEARCH_MODEL", ""),
		WorkspaceDir:    filepath.Join(DataDir(), "workspace"),
		MaxRounds:       defaultMaxRds,
		Headless:        true,
		AvatarPort:      defaultAvatarP,
		CLIProxyEnabled: true,
		CLIProxyPort:    DefaultCLIProxyPort,
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
	if s.CLIProxyPort <= 0 {
		s.CLIProxyPort = def.CLIProxyPort
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

// WebSearch returns the endpoint the built-in web_search tool should use,
// defaulting to the brain's own endpoint field by field. Returning the brain is
// deliberate: it is the one endpoint SuperAI is guaranteed to have credentials
// for, and a search-grounded model behind it answers a grounded request. A
// dedicated search endpoint is configured by filling the Search* fields (or
// SEARCH_BASE / SEARCH_KEY / SEARCH_MODEL).
func (s *Settings) WebSearch() (baseURL, key, model string) {
	if s == nil {
		return "", "", ""
	}
	return firstNonEmpty(s.SearchBaseURL, s.LLMBaseURL),
		firstNonEmpty(s.SearchKey, s.LLMKey),
		firstNonEmpty(s.SearchModel, s.LLMModel)
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
