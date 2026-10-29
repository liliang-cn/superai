// SuperLeo about itself: what it is running on and how it is doing, and the
// preferences it may change, as one set of tools.
//
// The same table is served twice. On /mcp, as superleo_<name>, so any MCP
// client — Claude Code, another SuperLeo, a script — can ask a SuperLeo how it is
// and adjust it. And to its own agent, as <name>, so "what model are you on",
// "is anything wrong", "turn PII redaction on" are answered in a conversation
// by the code that answers them on /mcp. Two hand-written copies were how the
// schedule tools came to have different names and different checks.
//
// Reading is everything a status screen shows, minus what is a secret: keys
// and tokens are reported as set or not, a bee's hook path (its secret is in
// the URL) is left out, and an MCP server is named but not its command line.
// Writing is every setting, in the two tiers of backend/selfsettings.go (a
// boundary change waits for the person on an approval card), and pausing a
// bee.

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superleo/internal/backend"
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
	// RunBy, when set, is used instead of Run, with who is asking: "" for
	// SuperLeo's own agent, the MCP caller on /mcp.
	RunBy func(a *App, by string, args map[string]any) (any, error)
}

// run calls the tool, as by.
func (t selfTool) run(a *App, by string, args map[string]any) (any, error) {
	if t.RunBy != nil {
		return t.RunBy(a, by, args)
	}
	return t.Run(a, args)
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
	return append([]selfTool{
		{
			Name: "status", Title: "SuperLeo status", ReadOnly: true,
			Description: "How this SuperLeo is doing: whether it is ready (and the error if not), the model and provider it thinks with, " +
				"the embedding model, memory mode, skills, MCP servers and tools running, scheduler and Telegram state, its role in a hive " +
				"and how many members and linked agents it has, and anything that needs attention. Start here for any question about itself.",
			Run: func(a *App, _ map[string]any) (any, error) { return scrubStrings(toJSONAny(a.selfStatus())), nil },
		},
		{
			Name: "doctor", Title: "Health check", ReadOnly: true,
			Description: "Run SuperLeo's health checks (model reachable, memory, MCP servers, scheduler, disk…) and report each as ok, warn or fail with why.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.Doctor(), nil },
		},
		{
			Name: "attention", Title: "What needs attention", ReadOnly: true,
			Description: "What needs the person now (approvals, waiting bees, failed orders, members or agents not answering) and what is coming up soon — the Needs you list.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.Attention(), nil },
		},
		{
			Name: "linked_agents", Title: "Linked agents", ReadOnly: true,
			Description: "The agents connected to this SuperLeo over the agent link (host, OS, the coding CLIs and agents each offers, when last heard), and the ones that dropped recently without coming back." +
				" Each says what it is: role machine (machine true) is a machine machine_inspect and machine_act look after; worker is a hive worker's pod, reached through the hive tools; relay is there only for the named agents it serves." +
				" activity, when present, is what the interactive Claude Code sessions people run in that machine's terminals are doing (not SuperLeo's own runs): working and waiting counts, and per session its project (the directory's name), state (working, waiting — wait says permission or input — or idle), the tool it is on and since when. No prompts or tool arguments are ever in it." +
				" cliActivity is the same for every coding CLI running there (claude, codex, opencode, pi, gemini, ...), by CLI: source plugin is the CLI's own plugin or hooks (precise), source process is the machine's look at its processes (working while it uses CPU, else idle). A CLI not running is not in it.",
			Run: func(a *App, _ map[string]any) (any, error) { return a.selfLinkedAgents(), nil },
		},
		{
			Name: "hosts", Title: "Hosts", ReadOnly: true,
			Description: "Every host this SuperLeo knows of and what runs on each. A host is a pod, a container, a Mac, a VM or bare metal (kind), with parent the host it sits inside (a pod's k3s node) and state live, lost or configured (known from the settings only)." +
				" Its agents are SuperLeo itself (kind superleo, with role queen, worker, machine, relay or standalone, and the MCP servers it can call), the coding CLIs installed there (kind cli, named cli.machine, with what they are doing) and named agents (kind agent, like openclaw) with via, the SuperLeo that runs them." +
				" services are the HA services (provider haify): one copy runs on active and moves to a ready standby; reach it at vip. An agent such a service runs says service, and shows on standby hosts with state standby.",
			Run: func(a *App, _ map[string]any) (any, error) { return a.fullTopology(), nil },
		},
		{
			Name: "mcp_servers", Title: "MCP servers", ReadOnly: true,
			Description: "The MCP servers SuperLeo has configured: whether each is running and the tools it provides.",
			Run:         func(a *App, _ map[string]any) (any, error) { return a.selfMCPServers(), nil },
		},
		{
			Name: "skills", Title: "Skills", ReadOnly: true,
			Description: "The skills installed in SuperLeo, with what each is for.",
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
			Description: "The phones, desktops and agents paired with this SuperLeo, with when each was paired and last seen.",
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
			Description: "SuperLeo's own settings and the state the Settings page shows, all of it: model source (signed-in accounts or custom API) and accounts, " +
				"the queen's engines in order, proxy (mode, address, and the proxy in use now), browser, desktop control (apps left alone, Accessibility), " +
				"memory and embeddings, coding tools, remote agents, hive role, links and local hive, paired devices, notification channels, " +
				"tool approval, budgets, workspace, privacy. Secrets read only \"set\" or \"not set\". " +
				"No arguments: the whole picture (sections holds each section's settings and state). " +
				"section: one of " + strings.Join(selfSettingsSections(), ", ") + " (settings is every key in settings.json, keys is every key's tier and meaning, requests the approval requests). " +
				"key: one key, e.g. proxy.url. request: the outcome of a settings_set that needed approval.",
			Params: map[string]any{
				"section": map[string]any{"type": "string", "enum": selfSettingsSections(), "description": "one section instead of the whole picture"},
				"key":     map[string]any{"type": "string", "description": "one key, e.g. proxy.url or channels.<id>.enabled"},
				"request": map[string]any{"type": "string", "description": "a settings_set approval request's id"},
			},
			Run: func(a *App, args map[string]any) (any, error) { return a.selfSettingsGet(args) },
		},
		{
			Name: "settings_set", Title: "Change settings",
			Description: "Change SuperLeo's own settings: one key (key + value) or several at once (changes: {key: value}). " +
				"Writable keys are validated and applied at once, the way the Settings page saves them (under a running turn: as soon as it ends); " +
				"nothing not named changes. Keys that need approval (credentials, addresses data is sent to, and anything that widens what the agent may do) " +
				"are put to the person on an approval card, whatever the approval gate or YOLO says; the answer has needs_approval with an id and " +
				"nothing changes until they say yes; settings_get {\"request\": id} shows the outcome. Never ask the person to type a secret into a chat for this. " +
				"A value of null clears a key. Lists of objects are addressed by id, e.g. channels.<id>.enabled.\n\n" + backend.SelfKeyTable(),
			Params: map[string]any{
				"key":     map[string]any{"type": "string", "description": "setting name, e.g. proxy.url or max_rounds"},
				"value":   map[string]any{"description": "new value: a string, number, boolean, list or object as the setting requires"},
				"changes": map[string]any{"type": "object", "description": "several changes at once, {key: value}; all are checked before any is applied"},
			},
			RunBy: func(a *App, by string, args map[string]any) (any, error) { return a.selfSetSettings(by, args) },
		},
	}, machineTools()...)
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
				out, err := t.run(a, "", args)
				if err != nil {
					return map[string]any{"ok": false, "error": err.Error()}, nil
				}
				b, err := json.Marshal(out)
				return string(b), err
			}, meta)
	}
}

// addSelfMCPTools serves the table on the MCP server. caller is who the
// approval card says is asking, for a tool that has to ask.
func addSelfMCPTools(s *mcp.Server, app *App, caller string) {
	for _, t := range selfTools() {
		tool := &mcp.Tool{Name: "superleo_" + t.Name, Title: t.Title, Description: t.Description, InputSchema: t.schema()}
		if t.ReadOnly {
			tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
		}
		s.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if raw := req.Params.Arguments; len(raw) > 0 {
				if err := json.Unmarshal(raw, &args); err != nil {
					return selfErr("arguments are not a JSON object: " + err.Error()), nil
				}
			}
			if refused := app.mcpGate(ctx, caller, tool.Name, args); refused != nil {
				return refused, nil
			}
			out, err := t.run(app, caller, args)
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
		st["provider"] = backend.RedactValue("llm_base_url", cfg.LLMBaseURL)
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
	roleOf := a.agentRoles()
	for name, seen := range a.agents().lostAgents(time.Now()) {
		role := roleOf(name, a.agents().declaredRole(name))
		lost = append(lost, map[string]any{"name": name, "lastSeen": seen, "role": role, "machine": role == agentRoleMachine})
	}
	return map[string]any{"connected": a.LinkedAgentsAll(), "notAnswering": lost}
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
