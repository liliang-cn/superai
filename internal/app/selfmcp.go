// SuperAI about itself: what it is running on and how it is doing, and the
// preferences it may change, as one set of tools.
//
// The same table is served twice. On /mcp, as superai_<name>, so any MCP
// client — Claude Code, another SuperAI, a script — can ask a SuperAI how it is
// and adjust it. And to its own agent, as <name>, so "what model are you on",
// "is anything wrong", "turn PII redaction on" are answered in a conversation
// by the code that answers them on /mcp. Two hand-written copies were how the
// schedule tools came to have different names and different checks.
//
// Reading is everything a status screen shows, minus what is a secret: keys
// and tokens are reported as set or not, a bee's hook path (its secret is in
// the URL) is left out, and an MCP server is named but not its command line.
// Writing is the preference whitelist in backend/selftools.go and pausing a
// bee; the safety gates, credentials and addresses stay out of reach for the
// reasons given there.

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// selfTool is one tool of the table. Name is the agent's; /mcp prefixes it.
type selfTool struct {
	Name, Title, Description string
	// Params is the JSON schema's properties; Required names the ones that
	// must be given. Nil Params is a tool that takes nothing.
	Params   map[string]any
	Required []string
	ReadOnly bool
	Run      func(a *App, args map[string]any) (any, error)
}

func (t selfTool) schema() map[string]any {
	props := t.Params
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props}
	if len(t.Required) > 0 {
		s["required"] = t.Required
	}
	return s
}

// selfTools is the table. A function rather than a variable: settings_set
// reaches SaveSettings, which rebuilds the agent, which registers this table.
func selfTools() []selfTool {
	return []selfTool{
		{
			Name: "status", Title: "SuperAI status", ReadOnly: true,
			Description: "How this SuperAI is doing: whether it is ready (and the error if not), the model and provider it thinks with, " +
				"the embedding model, memory mode, skills, MCP servers and tools running, scheduler and Telegram state, its role in a hive " +
				"and how many members and linked agents it has, and anything that needs attention. Start here for any question about itself.",
			Run: func(a *App, _ map[string]any) (any, error) { return a.selfStatus(), nil },
		},
		{
			Name: "doctor", Title: "Health check", ReadOnly: true,
			Description: "Run SuperAI's health checks (model reachable, memory, MCP servers, scheduler, disk…) and report each as ok, warn or fail with why.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.Doctor(), nil },
		},
		{
			Name: "attention", Title: "What needs attention", ReadOnly: true,
			Description: "What needs the person now (approvals, waiting bees, failed orders, members or agents not answering) and what is coming up soon — the Needs you list.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.Attention(), nil },
		},
		{
			Name: "linked_agents", Title: "Linked agents", ReadOnly: true,
			Description: "The machines whose agent is connected to this SuperAI (host, OS, the coding CLIs and agents each offers, when last heard), and the ones that dropped recently without coming back.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.selfLinkedAgents(), nil },
		},
		{
			Name: "mcp_servers", Title: "MCP servers", ReadOnly: true,
			Description: "The MCP servers SuperAI has configured: whether each is running and the tools it provides.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.selfMCPServers(), nil },
		},
		{
			Name: "skills", Title: "Skills", ReadOnly: true,
			Description: "The skills installed in SuperAI, with what each is for.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.Skills(), nil },
		},
		{
			Name: "standing_agents", Title: "Bees", ReadOnly: true,
			Description: "The bees — standing agents that wake on a schedule or an event: what each watches, whether it is paused, when it next wakes, and whether it is waiting for the person.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.selfStandingAgents(), nil },
		},
		{
			Name: "standing_agent_pause", Title: "Pause or resume a bee",
			Description: "Pause (paused true) or resume (paused false) a bee by the id standing_agents gave. A paused bee keeps its brief and history and does not wake.",
			Params: map[string]any{
				"id":     map[string]any{"type": "string", "description": "the bee's id, from standing_agents"},
				"paused": map[string]any{"type": "boolean", "description": "true pauses it, false resumes it"},
			},
			Required: []string{"id", "paused"},
			Run: func(a *App, args map[string]any) (any, error) {
				id := strings.TrimSpace(str(args["id"]))
				if id == "" {
					return nil, fmt.Errorf("id is empty: call standing_agents for the ids")
				}
				paused, _ := args["paused"].(bool)
				var err error
				if paused {
					err = a.PauseStandingAgent(id)
				} else {
					err = a.ResumeStandingAgent(id)
				}
				if err != nil {
					return nil, err
				}
				return map[string]any{"id": id, "paused": paused}, nil
			},
		},
		{
			Name: "devices", Title: "Paired devices", ReadOnly: true,
			Description: "The phones, desktops and agents paired with this SuperAI, with when each was paired and last seen.",
			Run: func(a *App, _ map[string]any) (any, error) {
				return a.PairedDevices()
			},
		},
		{
			Name: "coding_runs", Title: "Coding runs", ReadOnly: true,
			Description: "The most recent coding-agent runs (Claude Code, Codex and the like) with their state, newest first.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.selfCodingRuns(20), nil },
		},
		{
			Name: "settings_get", Title: "Read settings", ReadOnly: true,
			Description: "SuperAI's own configuration. Secrets are reported as whether they are set, never as their value. Also lists what settings_set may change, and any change still waiting to be applied.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.selfSettings(), nil },
		},
		{
			Name: "settings_set", Title: "Change a setting",
			Description: "Change one of SuperAI's own settings. Only preferences can be changed (settings_get lists them): the approval gate, the credentials and the addresses of the model and the memory are not writable from here. " +
				"The change is saved at once and applied as soon as no conversation is mid-turn, since applying it restarts the agent.",
			Params: map[string]any{
				"key":   map[string]any{"type": "string", "description": "setting name, e.g. llm_model or max_rounds"},
				"value": map[string]any{"description": "new value: a string, number or boolean as the setting requires"},
			},
			Required: []string{"key", "value"},
			Run: func(a *App, args map[string]any) (any, error) {
				return a.selfSetSetting(strings.TrimSpace(str(args["key"])), args["value"])
			},
		},
	}
}

// registerSelfTools gives the agent the table. Runs inside the build, which
// holds a.mu: nothing here may take it. settings_get and settings_set replace
// the backend's own, which save without applying.
func (a *App) registerSelfTools(svc *backend.Service) {
	if svc == nil || svc.Agent() == nil {
		return
	}
	inner := svc.Agent()
	for _, t := range selfTools() {
		meta := agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}
		if !t.ReadOnly {
			meta = agent.ToolMetadata{Destructive: true, InterruptBehavior: agent.InterruptBehaviorBlock}
		}
		inner.AddToolWithMetadata(t.Name, t.Description, t.schema(),
			func(_ context.Context, args map[string]any) (any, error) {
				out, err := t.Run(a, args)
				if err != nil {
					return map[string]any{"ok": false, "error": err.Error()}, nil
				}
				b, err := json.Marshal(out)
				return string(b), err
			}, meta)
	}
}

// addSelfMCPTools serves the table on the MCP server.
func addSelfMCPTools(s *mcp.Server, app *App) {
	for _, t := range selfTools() {
		tool := &mcp.Tool{Name: "superai_" + t.Name, Title: t.Title, Description: t.Description, InputSchema: t.schema()}
		if t.ReadOnly {
			tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
		}
		s.AddTool(tool, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if raw := req.Params.Arguments; len(raw) > 0 {
				if err := json.Unmarshal(raw, &args); err != nil {
					return selfErr("arguments are not a JSON object: " + err.Error()), nil
				}
			}
			out, err := t.Run(app, args)
			if err != nil {
				return selfErr(err.Error()), nil
			}
			b, err := json.Marshal(out)
			if err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
		})
	}
}

func selfErr(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func (a *App) selfStatus() map[string]any {
	st := a.GetStatus()
	a.mu.Lock()
	cfg := a.settings
	a.mu.Unlock()
	if cfg != nil {
		st["model"] = cfg.LLMModel
		st["provider"] = cfg.LLMBaseURL
		st["embedModel"] = cfg.EmbedModel
		st["maxRounds"] = cfg.MaxRounds
	}
	hive := a.HiveStatus()
	st["hive"] = map[string]any{"role": hive["role"], "name": hive["name"], "members": len(a.HiveMembers())}
	st["linkedAgents"] = len(a.LinkedAgents())
	needs := 0
	for _, it := range a.Attention() {
		if it.Level == "needs" {
			needs++
		}
	}
	st["needsAttention"] = needs
	return st
}

func (a *App) selfLinkedAgents() map[string]any {
	lost := []map[string]any{}
	for name, seen := range a.agents().lostAgents(time.Now()) {
		lost = append(lost, map[string]any{"name": name, "lastSeen": seen})
	}
	return map[string]any{"connected": a.LinkedAgents(), "notAnswering": lost}
}

// selfMCPServers names each server and its tools. The command line is left
// out: it is where a server's token tends to be passed.
func (a *App) selfMCPServers() []map[string]any {
	out := []map[string]any{}
	for _, s := range a.MCP() {
		tools := make([]string, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, t.Name)
		}
		out = append(out, map[string]any{"name": s.Name, "description": s.Description, "running": s.Running, "toolCount": s.ToolCount, "tools": tools})
	}
	return out
}

// selfStandingAgents is the bees without their hook paths, which carry each
// bee's secret.
func (a *App) selfStandingAgents() []AgentView {
	bees := a.StandingAgents()
	for i := range bees {
		bees[i].HookPath = ""
	}
	return bees
}

func (a *App) selfCodingRuns(n int) []CLIRun {
	runs := a.CLIRuns()
	if len(runs) > n {
		runs = runs[:n]
	}
	return runs
}

func (a *App) selfSettings() map[string]any {
	cfg := a.GetSettings()
	out := backend.SettingsSnapshot(&cfg)
	if p := a.pendingSettings(); len(p) > 0 {
		out["pending"] = p
	}
	if os.Getenv("SUPERAI_SETTINGS_JSON") != "" {
		out["note"] = "This instance takes its settings from its deployment: a change here lasts until it restarts."
	}
	return out
}

// Settings changes wait for a quiet moment: applying one rebuilds the agent,
// and a rebuild closes the service a turn is running on — including the turn
// that asked for the change, which would never get to say it was done.
var selfSettingsMu sync.Mutex

func (a *App) pendingSettings() map[string]any {
	a.selfPendingMu.Lock()
	defer a.selfPendingMu.Unlock()
	out := map[string]any{}
	for k, v := range a.selfPending {
		out[k] = v
	}
	return out
}

func (a *App) selfSetSetting(key string, value any) (any, error) {
	// Checked against the current settings now, so a bad key or value is the
	// answer to this call rather than a silent failure later.
	cfg := a.GetSettings()
	applied, err := backend.SetWritableSetting(&cfg, key, value)
	if err != nil {
		return nil, err
	}
	a.selfPendingMu.Lock()
	if a.selfPending == nil {
		a.selfPending = map[string]any{}
	}
	a.selfPending[key] = applied
	a.selfPendingMu.Unlock()
	go a.applyWhenIdle(key, applied)

	out := map[string]any{"key": key, "value": applied,
		"note": "Accepted; saved and applied as soon as no conversation is mid-turn, usually right after this reply."}
	if os.Getenv("SUPERAI_SETTINGS_JSON") != "" {
		out["note"] = out["note"].(string) + " This instance takes its settings from its deployment, so the change lasts until it restarts."
	}
	return out, nil
}

// applyWhenIdle applies one change once no turn is running, on top of
// whatever the settings are by then, and gives up after ten minutes of
// nobody being idle.
func (a *App) applyWhenIdle(key string, value any) {
	defer func() {
		a.selfPendingMu.Lock()
		if v, ok := a.selfPending[key]; ok && fmt.Sprint(v) == fmt.Sprint(value) {
			delete(a.selfPending, key)
		}
		a.selfPendingMu.Unlock()
	}()
	deadline := time.Now().Add(10 * time.Minute)
	for a.turnsRunning() > 0 {
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	selfSettingsMu.Lock()
	defer selfSettingsMu.Unlock()
	cfg := a.GetSettings()
	if _, err := backend.SetWritableSetting(&cfg, key, value); err != nil {
		return
	}
	_ = a.SaveSettings(cfg)
}
