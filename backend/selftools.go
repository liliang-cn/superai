package backend

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// SuperAI, operated from inside the conversation.
//
// The app already lets the agent acquire capabilities — install.go gives it
// add_mcp_server and install_skill. These are the other half: the things it can
// do to *itself*. "把这个存成 dashboard，每天早上刷新" should not need the person to
// go and find the button.
//
// Two groups, with very different risk:
//
//   - Dashboards. Saving a reply someone can reopen is as consequential as
//     writing a note, and every one of them is visible and deletable in a panel.
//
//   - Settings. An agent that can rewrite its own configuration can also
//     switch off the thing that asks you before it runs a shell command. So the
//     writable set is a whitelist of preferences, and the fields that decide
//     what SuperAI is allowed to do are not in it. See settingsWritable.

// settingsWritable is every setting the agent may change about itself.
//
// The list is what it does not contain that matters:
//
//   - disable_tool_approval and disable_self_install are the safety gates. A
//     model that can turn off its own approval prompt has no approval prompt,
//     and it would only take one convincing page of injected text on the open
//     web to get it asked for.
//   - llm_key, embed_key, search_key, shared_memory_token, webhook_secret are
//     credentials. Nothing that can be talked into reading a web page should be
//     able to move a secret from a config file into a tool call.
//   - llm_base_url and shared_memory_endpoint decide who SuperAI talks to. The
//     brain and the memory are not somewhere a conversation gets to redirect.
//   - external_agents is disable_tool_approval wearing another hat. Turning it
//     on hands work to another agent CLI that writes files with its own
//     approval prompt bypassed, spends someone's subscription, and — through
//     roots and unattended — decides where and whether anyone is asked. A
//     model that can switch that on has granted itself everything the gate
//     above was withholding, so it is not writable here and its nested keys
//     are not reachable from this tool at all.
//
// What is left is preference: how hard it tries, where it works, whether the
// avatar bridge is on, and where a notice is sent.
var settingsWritable = map[string]string{
	"llm_model":     "The chat model, as the provider names it.",
	"embed_model":   "The embedding model.",
	"max_rounds":    "Tool-call rounds per task (1-200).",
	"headless":      "Run the browser without a visible window.",
	"avatar_port":   "Local SSE port for the avatar bridge; 0 turns it off.",
	"webhook_url":   "Where a notice is POSTed. Blank turns the webhook off.",
	"workspace_dir": "Where the agent reads and writes deliverable files.",
	"pii_redaction": "Strip personal data before sending to the model.",
	"disable_ptc":   "Turn off programmatic tool calling.",
}

// registerSelfTools gives the agent the app's own controls.
func (s *Service) registerSelfTools() {
	svc := s.svc
	if svc == nil {
		return
	}
	// Saving and deleting change what a person sees when they open the panel,
	// so they go through the approval gate like any other write.
	destMeta := agent.ToolMetadata{Destructive: true, InterruptBehavior: agent.InterruptBehaviorBlock}

	// --- dashboard_save ---
	svc.AddToolWithMetadata(
		"dashboard_save",
		"Keep a reply as a dashboard the user can reopen from the Dashboards panel. For \"save that as a dashboard\" — the usual case — pass only name and prompt: the reply already on screen is stored exactly as it was drawn. Do NOT retype the document; a rewrite is a second generation nobody has seen rendered, and it is how a working board becomes a broken one. `source` is only for a dashboard that has no reply behind it yet. `prompt` is the question that regenerates it — a dashboard saved without one can never refresh.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":   map[string]interface{}{"type": "string", "description": "Short label, e.g. 我的美股收益"},
				"prompt": map[string]interface{}{"type": "string", "description": "The question that regenerates it; omit only if it genuinely cannot be re-asked"},
				"source": map[string]interface{}{"type": "string", "description": "Leave this out to save the reply already on screen, which is almost always what is wanted. Only pass it for a dashboard with no reply behind it."},
			},
			"required": []string{"name"},
		},
		func(ctx context.Context, a map[string]interface{}) (interface{}, error) {
			source, from := strings.TrimSpace(argStr(a, "source")), "the text you supplied"
			if source == "" {
				// The reply that is already on screen, byte for byte.
				reply, ok := lastRenderableReply(s.SessionTurns(sessionIDFrom(ctx)))
				if !ok {
					return errResult("no reply in this conversation contains a block to save — draw the dashboard first, then save it"), nil
				}
				source, from = reply, "the reply already on screen"
			}
			d, err := s.SaveDashboard(argStr(a, "name"), source, argStr(a, "prompt"))
			if err != nil {
				return errResult(err.Error()), nil
			}
			note := "Stored " + from + "."
			if strings.TrimSpace(d.Prompt) == "" {
				note += " Saved without a question, so it cannot refresh itself."
			}
			return okData(map[string]interface{}{"id": d.ID, "name": d.Name, "note": note}), nil
		},
		destMeta,
	)

	// --- dashboard_list ---
	svc.AddToolWithMetadata(
		"dashboard_list",
		"List the saved dashboards: id, name, how old the contents are, and whether one refreshes itself.",
		map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
			rows := []map[string]interface{}{}
			for _, d := range s.Dashboards() {
				rows = append(rows, map[string]interface{}{
					"id": d.ID, "name": d.Name, "refreshed_at": d.RefreshedAt,
					"cron": d.Cron, "can_refresh": strings.TrimSpace(d.Prompt) != "",
					"last_error": d.LastError,
				})
			}
			return okData(map[string]interface{}{"dashboards": rows, "count": len(rows)}), nil
		},
		agent.ToolMetadata{ReadOnly: true},
	)

	// --- dashboard_delete ---
	svc.AddToolWithMetadata(
		"dashboard_delete",
		"Forget a saved dashboard. Its schedule, if it had one, is left for the app to clear.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"id": map[string]interface{}{"type": "string", "description": "id from dashboard_list"}},
			"required":   []string{"id"},
		},
		func(_ context.Context, a map[string]interface{}) (interface{}, error) {
			if err := s.DeleteDashboard(argStr(a, "id")); err != nil {
				return errResult(err.Error()), nil
			}
			return okData(map[string]interface{}{"deleted": argStr(a, "id")}), nil
		},
		destMeta,
	)

	// --- settings_get ---
	svc.AddToolWithMetadata(
		"settings_get",
		"Read SuperAI's own configuration. Secrets are reported as whether they are set, never as their value.",
		map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
			return okData(s.settingsSnapshot()), nil
		},
		agent.ToolMetadata{ReadOnly: true},
	)

	// --- settings_set ---
	svc.AddToolWithMetadata(
		"settings_set",
		"Change one of SuperAI's own settings and rebuild. Only preferences can be changed: the approval gate, the credentials, and the addresses of the model and the memory are not writable from a conversation. Call settings_get for the current values and the writable list.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key":   map[string]interface{}{"type": "string", "description": "Setting name, e.g. max_rounds"},
				"value": map[string]interface{}{"description": "New value; a string, number or boolean as the setting requires"},
			},
			"required": []string{"key", "value"},
		},
		func(_ context.Context, a map[string]interface{}) (interface{}, error) {
			key := strings.TrimSpace(argStr(a, "key"))
			if _, ok := settingsWritable[key]; !ok {
				return errResult("not a writable setting: " + key + " — call settings_get for the list"), nil
			}
			applied, err := s.applySetting(key, a["value"])
			if err != nil {
				return errResult(err.Error()), nil
			}
			return okData(map[string]interface{}{
				"key": key, "value": applied,
				"note": "Saved. The change takes effect when the app rebuilds the backend, which the UI does on save and a restart always does.",
			}), nil
		},
		destMeta,
	)
}

// settingsSnapshot is what the agent may see of its own configuration.
//
// Secrets are reported as a boolean. The point of the tool is to let it answer
// "what model am I on" and "is the webhook set", not to put a key where a reply
// could repeat it.
func (s *Service) settingsSnapshot() map[string]interface{} {
	cfg := s.settings
	if cfg == nil {
		return map[string]interface{}{"error": "settings unavailable"}
	}
	writable := map[string]string{}
	for k, v := range settingsWritable {
		writable[k] = v
	}
	return map[string]interface{}{
		"llm_base_url":           cfg.LLMBaseURL,
		"llm_model":              cfg.LLMModel,
		"llm_key_set":            strings.TrimSpace(cfg.LLMKey) != "",
		"embed_model":            cfg.EmbedModel,
		"embed_key_set":          strings.TrimSpace(cfg.EmbedKey) != "",
		"memory_backend":         cfg.MemoryBackend,
		"shared_memory_endpoint": cfg.SharedMemoryEndpoint,
		"max_rounds":             cfg.MaxRounds,
		"workspace_dir":          cfg.WorkspaceDir,
		"headless":               cfg.Headless,
		"avatar_port":            cfg.AvatarPort,
		"webhook_url":            cfg.WebhookURL,
		"webhook_secret_set":     strings.TrimSpace(cfg.WebhookSecret) != "",
		"pii_redaction":          cfg.PIIRedaction,
		"disable_ptc":            cfg.DisablePTC,
		"tool_approval_enabled":  !cfg.DisableToolApproval,
		// Readable so the agent can answer "can you hand this to Claude Code",
		// and no more than that: the roots, the binaries and the unattended
		// flag are policy about this machine, not something a reply needs.
		"external_agents_enabled": cfg.ExternalAgents.Enabled,
		"self_install_enabled":    !cfg.DisableSelfInstall,
		"writable_from_this_tool": writable,
	}
}

// applySetting writes one whitelisted field and persists.
//
// Values arrive from a model as whatever JSON it felt like emitting, so each
// field converts rather than asserts: "40" and 40 both mean forty rounds, and
// refusing the string would be a tool that fails for a reason nobody can see
// in the reply.
func (s *Service) applySetting(key string, raw interface{}) (interface{}, error) {
	cfg, err := LoadSettings()
	if err != nil {
		return nil, err
	}
	switch key {
	case "llm_model":
		cfg.LLMModel = toStr(raw)
	case "embed_model":
		cfg.EmbedModel = toStr(raw)
	case "workspace_dir":
		cfg.WorkspaceDir = toStr(raw)
	case "webhook_url":
		cfg.WebhookURL = toStr(raw)
	case "max_rounds":
		n := toInt(raw)
		if n < 1 || n > 200 {
			return nil, errBadRange("max_rounds", 1, 200, n)
		}
		cfg.MaxRounds = n
	case "avatar_port":
		n := toInt(raw)
		// Zero is off. Anything else has to be a port nothing privileged owns.
		if n != 0 && (n < 1024 || n > 65535) {
			return nil, errBadRange("avatar_port", 1024, 65535, n)
		}
		cfg.AvatarPort = n
	case "headless":
		cfg.Headless = toBool(raw)
	case "pii_redaction":
		cfg.PIIRedaction = toBool(raw)
	case "disable_ptc":
		cfg.DisablePTC = toBool(raw)
	default:
		return nil, errNotWritable(key)
	}
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	// The in-memory copy is what settings_get reads back, so it has to move too
	// or the agent is told its own change did not happen.
	s.settings = cfg
	return raw, nil
}

func errBadRange(key string, lo, hi, got int) error {
	return fmt.Errorf("%s must be between %d and %d (got %d)", key, lo, hi, got)
}

func errNotWritable(key string) error { return fmt.Errorf("not a writable setting: %s", key) }

func toStr(v interface{}) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

func toBool(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		ok, _ := strconv.ParseBool(strings.TrimSpace(b))
		return ok
	case float64:
		return b != 0
	}
	return false
}

// --- the conversation a tool call belongs to ---

type sessionIDKey struct{}

// withSessionID marks a context with the conversation whose turn is running.
//
// agent-go has this already — pkg/agent/context_session.go — but its accessor
// and its key type are unexported, so a host cannot read it. Stream owns the
// context it hands to the agent, and tools are called with a context derived
// from it, so putting the id back under our own key costs one line and needs no
// change upstream.
func withSessionID(ctx context.Context, id string) context.Context {
	if strings.TrimSpace(id) == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDKey{}, id)
}

func sessionIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sessionIDKey{}).(string)
	return id
}

// lastRenderableReply returns the most recent assistant message in a session
// that actually contains a block the transcript draws.
//
// This is what "save this one" means. Letting the model retype the document
// into the tool call instead — which is what dashboard_save did at first —
// makes saving a second generation: it wrote a `ui` document where the screen
// held a `bigscreen` one, nobody had rendered the new text, and the failure
// surfaced hours later when the panel was opened. What is on screen has been
// drawn once already; storing those bytes is the only version with evidence
// behind it.
func lastRenderableReply(turns []ChatTurn) (string, bool) {
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		if t.Role != "assistant" || t.Kind != "" {
			continue
		}
		if hasRenderableFence(t.Content) {
			return t.Content, true
		}
	}
	return "", false
}

// renderableFences are the block names the transcript's plugins draw. Kept
// beside the tool rather than imported from the frontend, which is where the
// authoritative list lives — a name added there and not here only costs this
// tool a fallback, never a wrong save.
var renderableFences = map[string]bool{
	"bigscreen": true, "ui": true, "chart": true,
	"list": true, "mermaid": true, "sources": true, "table": true,
}

func hasRenderableFence(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "```")
		if !ok {
			continue
		}
		if renderableFences[strings.TrimSpace(strings.ToLower(rest))] {
			return true
		}
	}
	return false
}
