// Package backend hosts the AgentGo-powered SuperAI service, the avatar event
// driver, and the persisted user settings for the SuperAI desktop app.
package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/pool"
)

// Settings is the user-editable configuration, persisted as JSON at
// ~/.superai-desktop/settings.json.
type Settings struct {
	// Brain (LLM) — any OpenAI-compatible endpoint.
	LLMBaseURL string `json:"llm_base_url"`
	LLMKey     string `json:"llm_key"`
	LLMModel   string `json:"llm_model"`
	// Rates for LLMModel, USD per 1k tokens. agent-go prices a run from its
	// own table, which knows the public model names and nothing about a
	// gateway alias like gemini-3.7-flash-high; unpriced, every cost reads 0
	// and the spend ceilings never trigger. Set these and the run wall, the
	// health card and MaxTotalCostUSD all start meaning something.
	LLMPriceInputPer1K  float64 `json:"llm_price_input_per_1k,omitempty"`
	LLMPriceCachedPer1K float64 `json:"llm_price_cached_per_1k,omitempty"`
	LLMPriceOutputPer1K float64 `json:"llm_price_output_per_1k,omitempty"`

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

	// SearXNGURL points the builtin websearch MCP at a self-hosted SearXNG
	// instance (its JSON API must be enabled). Configured, that instance is
	// tried before the scraping engines — a stable aggregator beats parsing
	// search sites' HTML. Empty, the scrapers behave as before.
	SearXNGURL string `json:"searxng_url"`

	// DisableSelfInstall withholds the self-extension toolset —
	// search_mcp_servers, add_mcp_server, search_skills, install_skill and the
	// URL installer. Those tools let the agent go and acquire a capability it
	// lacks, which is right for a desktop conversation and wrong for a
	// one-shot invocation: a run that answers one question should not be
	// installing software, and the tools cost a schema slot and a tempting
	// detour on every task that will never use them. cmd/superai sets it.
	DisableSelfInstall bool `json:"disable_self_install"`

	// DisableToolApproval turns off the tool approval gate: shell commands,
	// self-installs and deletions then run without asking (they are still
	// written to the audit log).
	//
	// Phrased as a disable so the safe state is the zero value. A settings.json
	// written before this field existed unmarshals to false, which means the
	// gate is on — an upgrade cannot silently leave a machine ungated, and
	// neither can a hand-edited file that forgot the key. The same reasoning
	// the DisableSelfInstall / DisableBrowser fields use, applied to the one
	// setting where getting the default backwards actually costs something.
	DisableToolApproval bool `json:"disable_tool_approval"`

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

	// MemoryBackend selects where durable memory lives:
	//
	//	"local"  (default) — this machine's own store under <data>/data.
	//	"shared"           — a remote CortexDB (the "shared brain") that other
	//	                     agents also read and write, over gRPC.
	//
	// This is a one-of-two choice on purpose. A capability should have exactly
	// one route: when the shared brain is the memory backend, any MCP server
	// pointed at the same endpoint is dropped from the tool surface, because
	// two names for one store is how a model ends up calling both and
	// reporting "not found".
	MemoryBackend string `json:"memory_backend"`

	// SharedMemoryEndpoint is host:port of the shared CortexDB gRPC server.
	// Used only when MemoryBackend is "shared"; falls back to $CORTEXDB_REMOTE.
	SharedMemoryEndpoint string `json:"shared_memory_endpoint"`

	// SharedMemoryToken is the shared brain's bearer token. Leave it empty to
	// read $CORTEXDB_GRPC_TOKEN instead, which keeps the secret out of
	// settings.json.
	SharedMemoryToken string `json:"shared_memory_token"`

	// SharedMemoryNamespace scopes reads and writes inside the shared brain.
	// Defaults to "default", which is the namespace the other clients use.
	SharedMemoryNamespace string `json:"shared_memory_namespace"`

	// WebhookURL receives a JSON POST for every notification the agent
	// produces: notify_user mid-task, and each scheduled run as it finishes.
	//
	// It exists because the other two surfaces both need someone already
	// looking — notify_user rides the stream to an open page, and the desktop
	// notification is macOS-only and absent in serve mode. On a headless box a
	// reminder that fired at 08:00 reached a log and nobody. One POST is what
	// Telegram, a WeCom bot, bark, ntfy and a home-made receiver all accept
	// behind a short adapter, which is why this is a URL and not a list of
	// per-service integrations.
	//
	// Empty disables it, which is the default.
	WebhookURL string `json:"webhook_url"`

	// WebhookSecret, when set, is sent as `Authorization: Bearer <secret>` so a
	// receiver on the open internet can tell the agent's calls from anyone
	// else's. A bearer token rather than a request signature: the receiver is
	// usually a few lines of script, and a token it can compare is the check it
	// will actually implement.
	WebhookSecret string `json:"webhook_secret"`

	// ExternalAgents lets SuperAI hand a task to an agent CLI installed on
	// this machine. Nested rather than flattened into a dozen top-level keys
	// so the whole feature is one object a person can read, delete or diff in
	// settings.json — and so nothing here can be reached by a settings key the
	// agent is allowed to write (see settingsWritable in selftools.go).
	ExternalAgents ExternalAgents `json:"external_agents"`

	// RemoteAgents lets SuperAI ask an agent on another machine — see
	// remoteagents.go. Separate from ExternalAgents because the two differ in
	// what they can hurt: one spends a subscription on this laptop, the other
	// runs commands on a cluster.
	RemoteAgents RemoteAgents `json:"remote_agents"`
}

// ExternalAgents configures handing work to agent CLIs installed on this
// machine. Off by default: it spends money through someone else's
// subscription and writes files with a bypassed approval prompt.
type ExternalAgents struct {
	Enabled bool `json:"enabled"`
	// Roots bounds the directories a delegated run may work in. Empty means
	// the workspace only.
	Roots []string `json:"roots,omitempty"`
	// Unattended lets a delegated run through this app's approval gate
	// without asking. Meant for scheduled work with nobody watching; every
	// call it lets through is still audited.
	Unattended bool `json:"unattended"`
	// Binaries overrides where an agent's CLI lives, by name.
	Binaries map[string]string `json:"binaries,omitempty"`
	// Models overrides which model an agent is asked for, by name.
	Models map[string]string `json:"models,omitempty"`
	// TimeoutSeconds bounds one delegated run. Zero takes the default.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// DefaultExternalAgentTimeout bounds one delegated run when the settings do
// not. Generous on purpose: the whole point of handing a task to Claude Code
// is that it is a task, and a five-minute ceiling would kill the runs worth
// delegating. It exists so a CLI sitting on a login prompt nobody can answer
// eventually gives the turn back instead of holding it forever.
const DefaultExternalAgentTimeout = 20 * time.Minute

// Timeout is how long one delegated run may take.
func (e ExternalAgents) Timeout() time.Duration {
	if e.TimeoutSeconds <= 0 {
		return DefaultExternalAgentTimeout
	}
	return time.Duration(e.TimeoutSeconds) * time.Second
}

// Binary returns the configured path to an agent's CLI, or the bare name for
// PATH lookup. A GUI app launched from Finder inherits a very short PATH — not
// the one the login shell builds — so a CLI that works in a terminal can be
// invisible here, and the override is how that gets fixed without asking
// anyone to relaunch the app from a shell.
func (e ExternalAgents) Binary(name string) string {
	if v := strings.TrimSpace(e.Binaries[name]); v != "" {
		return expandHome(v)
	}
	return name
}

// Model returns the configured model for an agent, or "" to let the CLI pick
// its own default.
func (e ExternalAgents) Model(name string) string {
	return strings.TrimSpace(e.Models[name])
}

// ExternalAgentRoots is the set of directories a delegated run may work in.
//
// Empty roots mean the workspace, not the whole disk: an unconfigured feature
// that would let another agent write anywhere is a footgun handed out by
// default. The workspace is always included even when roots are named, because
// a delegated run still reports back through the same deliverable directory
// every other tool writes to.
func (s *Settings) ExternalAgentRoots() []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		p = expandHome(p)
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(s.WorkspaceDir)
	for _, r := range s.ExternalAgents.Roots {
		add(r)
	}
	return out
}

// normalize drops what a hand-edited or UI-written settings file leaves
// behind: blank rows from an "add root" button nobody filled in, and a
// negative timeout, which would otherwise mean "already expired".
func (e *ExternalAgents) normalize() {
	roots := e.Roots[:0]
	for _, r := range e.Roots {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, r)
		}
	}
	e.Roots = roots
	if len(e.Roots) == 0 {
		e.Roots = nil
	}
	e.Binaries = trimmedPairs(e.Binaries)
	e.Models = trimmedPairs(e.Models)
	if e.TimeoutSeconds < 0 {
		e.TimeoutSeconds = 0
	}
}

// trimmedPairs drops entries whose value is blank. The settings UI writes a
// key the moment a field is focused, so an override someone typed and then
// cleared would otherwise persist as "" and shadow the real binary.
func trimmedPairs(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range m {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Memory backend choices for Settings.MemoryBackend.
const (
	MemoryBackendLocal  = "local"
	MemoryBackendShared = "shared"
)

// UseSharedMemory reports whether the shared brain is the configured backend
// and reachable (an endpoint is known).
func (s *Settings) UseSharedMemory() bool {
	return s.MemoryBackend == MemoryBackendShared && s.SharedMemoryEndpointResolved() != ""
}

// SharedMemoryEndpointResolved returns the configured endpoint, falling back to
// $CORTEXDB_REMOTE.
func (s *Settings) SharedMemoryEndpointResolved() string {
	if v := strings.TrimSpace(s.SharedMemoryEndpoint); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("CORTEXDB_REMOTE"))
}

// SharedMemoryTokenResolved returns the configured token, falling back to
// $CORTEXDB_GRPC_TOKEN. Never log the result.
func (s *Settings) SharedMemoryTokenResolved() string {
	if v := strings.TrimSpace(s.SharedMemoryToken); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("CORTEXDB_GRPC_TOKEN"))
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
		// Local memory is the default: a fresh install must not depend on a
		// server somewhere else on the network.
		MemoryBackend:         MemoryBackendLocal,
		SharedMemoryEndpoint:  envOr("CORTEXDB_REMOTE", ""),
		SharedMemoryNamespace: "default",
	}
	return s
}

// LoadSettings reads settings.json, falling back to defaults when the file is
// missing. Any zero-valued fields are backfilled with defaults so older files
// keep working after upgrades.
func LoadSettings() (*Settings, error) {
	return loadSettingsFrom(settingsPath())
}

// loadSettingsFrom is LoadSettings against a named file, for callers that
// look at a home other than the running app's.
func loadSettingsFrom(path string) (*Settings, error) {
	def := defaults()
	raw, err := os.ReadFile(path)
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
	s.registerPricing()
	// An older settings file has no memory_backend; it was on local memory, so
	// that is what it stays on.
	if s.MemoryBackend != MemoryBackendShared {
		s.MemoryBackend = MemoryBackendLocal
	}
	if strings.TrimSpace(s.SharedMemoryEndpoint) == "" {
		s.SharedMemoryEndpoint = def.SharedMemoryEndpoint
	}
	if strings.TrimSpace(s.SharedMemoryNamespace) == "" {
		s.SharedMemoryNamespace = def.SharedMemoryNamespace
	}
	// Nothing here is backfilled from defaults. External agents are off in the
	// zero value, which is what a settings file written before this existed
	// unmarshals to, and an upgrade must not switch on a feature that spends
	// money — the same reasoning DisableToolApproval uses, from the other
	// direction.
	s.ExternalAgents.normalize()
	// Remote agents get their catalogue backfilled but not their switch: a
	// file written before this existed knows nothing about pi or hermes, and
	// filling the list in costs nothing while Enabled stays false.
	s.RemoteAgents.backfill()
	s.RemoteAgents.normalize()
}

// Save writes the settings to ~/.superai-desktop/settings.json (creating the
// data directory if needed).
// registerPricing tells agent-go what the brain's model costs, when the
// settings say. Registered rates win over the bundled table, so a public
// name can be corrected too. Nothing set leaves the table alone.
func (s *Settings) registerPricing() {
	model := strings.TrimSpace(s.LLMModel)
	if model == "" || (s.LLMPriceInputPer1K <= 0 && s.LLMPriceOutputPer1K <= 0) {
		return
	}
	pool.RegisterModelPricing(model, pool.ModelPricing{
		InputPer1K:       s.LLMPriceInputPer1K,
		CachedInputPer1K: s.LLMPriceCachedPer1K,
		OutputPer1K:      s.LLMPriceOutputPer1K,
	})
}

func (s *Settings) Save() error {
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	// On the way in as well as on the way out. The settings page appends an
	// empty row the moment "Add a directory" is pressed, and a file full of
	// blank roots is a file nobody can read to answer "where may it write".
	s.ExternalAgents.normalize()
	s.RemoteAgents.normalize()
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
