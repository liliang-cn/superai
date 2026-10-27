package app

// The queen's engine: who thinks for her when a person talks to her.
//
// By default that is this app's own model. Settings → Hive → Queen engine can
// hand it to an agent CLI instead — Claude Code, Codex, pi or Hermes, here
// ("codex") or on a linked machine ("codex.mac") — and SuperAI keeps
// everything around the thinking:
// the conversation and its history, the stream to every client, approvals,
// notices, alarms and schedules. Clients see the same chat:event / chat:done
// they always did, with "engine" on them naming who answered.
//
// Only a person's own turns go to the engine. Schedules, dashboards, tiles,
// the hive's traffic, a voice call (SendCallChat) and superai_ask all stay
// with SuperAI's agent: they are unattended, or cannot wait for a CLI to
// start, or would have the engine answering itself.
//
// Each turn is a run of the CLI (app_cliruns.go) given the queen's prompt
// (backend.ExternalQueenPrompt, the same text her own agent reads), the
// cluster-command tools on this app's /mcp under a queen-scoped token
// (mcp_scope.go), noted and the shared brain when this app has them, and the
// approval channel for its own permission prompts, each the way its CLI takes
// them (queen_engine_cli.go). The conversation's next turn resumes the same
// CLI session, for a CLI that keeps one.
//
// An engine that cannot take a turn — switched off, not installed, its
// machine asleep, failed before it said or did anything — hands it to the
// next of its backups (Hive.QueenBackups), in order. When none can, SuperAI's
// agent stands in, its answer opening with a line saying so, unless the
// fallback is switched off; then the turn fails with the reason.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
)

const (
	// queenHiveServer is what the engine calls this app's /mcp: its tools are
	// mcp__hive__superai_hive_members and so on.
	queenHiveServer = "hive"
	// queenTokenEnv carries the queen token to the CLI. The MCP config names
	// it and the CLI fills it in, so the token is never on a command line.
	queenTokenEnv = "SUPERAI_QUEEN_TOKEN"
	// queenEngineTokenName is the queen token this app mints for its engine.
	queenEngineTokenName = "queen engine (automatic)"
)

// QueenKit is what makes a CLI run the queen's.
type QueenKit struct {
	// SystemPrompt is appended to the CLI's own.
	SystemPrompt string `json:"systemPrompt"`
	// Servers are MCP server entries by name, as --mcp-config reads them.
	Servers map[string]any `json:"servers"`
	// Allowed are tool rules the CLI may use without a permission prompt.
	// The hive server is one: the cluster-changing calls on it pass this
	// app's own approval gate, so asking first as well would ask twice.
	Allowed []string `json:"allowed,omitempty"`
	// Env is set for the CLI alone: the tokens its servers' entries name,
	// and what keeps the run lean.
	Env map[string]string `json:"env,omitempty"`
	// Args go on Claude's command line as they are: queenLeanArgs. Every
	// other CLI's flags are its adapter's, on the machine it runs on.
	Args []string `json:"args,omitempty"`
}

// queenLeanArgs keep the person's own Claude Code out of a queen run: no
// user, project or local settings (so none of their hooks, plugins, model or
// permission rules), no CLAUDE.md from them, no skills or slash commands. The
// login is not a setting and still works. With --strict-mcp-config and
// queenLeanEnv the run is the queen's prompt and the servers it is handed.
var queenLeanArgs = []string{"--setting-sources", "", "--disable-slash-commands"}

// queenLeanEnv turns off Claude's auto-memory for the run, which would read
// and write notes under ~/.claude/projects for the workspace.
var queenLeanEnv = map[string]string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY": "1"}

// QueenRunRequest is a queen run asked of another machine over the agent link
// or its API: StartCLIRun's arguments, the session to resume and the kit.
type QueenRunRequest struct {
	Agent   string   `json:"agent"`
	Prompt  string   `json:"prompt"`
	Cwd     string   `json:"cwd,omitempty"`
	Model   string   `json:"model,omitempty"`
	Ask     bool     `json:"ask"`
	Session string   `json:"session,omitempty"`
	Kit     QueenKit `json:"kit"`
}

// StartQueenCLIRun starts a run here thinking for another machine's queen.
// Its servers must be ones this machine reaches by URL: a command in the kit
// would run here on that machine's say-so.
func (a *App) StartQueenCLIRun(r QueenRunRequest) (CLIRun, error) {
	for name, raw := range r.Kit.Servers {
		e, _ := raw.(map[string]any)
		if url, _ := e["url"].(string); url == "" || e["command"] != nil {
			return CLIRun{}, fmt.Errorf("MCP server %s is not one reached by URL", name)
		}
		if name == "superai" {
			return CLIRun{}, errors.New("the superai server name is this machine's approval channel")
		}
	}
	// Only the lean flags and the variables the servers name, or the lean
	// one: anything else on the command line or in the environment is that
	// machine running more than a Claude for its queen.
	if !slices.Equal(r.Kit.Args, queenLeanArgs) && len(r.Kit.Args) > 0 {
		return CLIRun{}, fmt.Errorf("arguments %q are not the queen's", r.Kit.Args)
	}
	named := map[string]bool{}
	if body, err := json.Marshal(r.Kit.Servers); err == nil {
		for _, name := range backend.MCPEnvRefs(string(body)) {
			named[name] = true
		}
	}
	for k := range r.Kit.Env {
		if _, lean := queenLeanEnv[k]; !lean && !named[k] {
			return CLIRun{}, fmt.Errorf("%s is not a variable the queen's servers use", k)
		}
	}
	if !canThinkForQueen(r.Agent) {
		return CLIRun{}, fmt.Errorf("%s cannot think for the queen", r.Agent)
	}
	kit := r.Kit
	return a.startCLIRun(cliStart{Agent: r.Agent, Prompt: r.Prompt, Cwd: r.Cwd, Model: r.Model, Ask: r.Ask,
		Session: r.Session, Queen: &kit})
}

// queenEngineFor is the engine a turn goes to, or "" for this app's model.
func (a *App) queenEngineFor(sessionID string, imagePaths []string) string {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	return queenEngineRoute(s, sessionID, len(imagePaths) > 0)
}

// queenEngineRoute decides who thinks for a turn: the engine for a person's
// own words in a conversation of theirs; this app's model for everything
// else, and for pictures, which the CLI is not handed.
func queenEngineRoute(s *backend.Settings, sessionID string, pictures bool) string {
	if s == nil || s.Hive.Role == backend.HiveRoleWorker || pictures {
		return ""
	}
	engine := strings.TrimSpace(s.Hive.QueenEngine)
	if engine == "" || !personalSession(sessionID) {
		return ""
	}
	return engine
}

// ownSessionPrefixes mark conversations no person is typing in: the hive's
// orders and mail, superai_ask's fresh ones, home tiles, terminal Claude Code
// sessions shown in the app.
var ownSessionPrefixes = []string{backend.HiveSessionPrefix, HiveMailSessionPrefix, askNewConversationPrefix, "tile-", "cc:"}

func personalSession(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || id == "scheduled" {
		return false
	}
	if _, ok := backend.DashboardIDFromSession(id); ok {
		return false
	}
	for _, p := range ownSessionPrefixes {
		if strings.HasPrefix(id, p) {
			return false
		}
	}
	return true
}

// routeToEngine answers a turn with the queen engine.
func (a *App) routeToEngine(requestID, sessionID, engine, message string, fresh bool) {
	a.mu.Lock()
	var s backend.Settings
	if a.settings != nil {
		s = *a.settings
	}
	a.mu.Unlock()

	started := time.Now()
	a.openTurns.Store(requestID, backend.ChatSessionInfo{ID: sessionID, Title: firstLineOf(message, 80),
		UpdatedAt: started.UTC().Format(time.RFC3339)})
	// Stop works before the run has an id too: it is stopped as it starts.
	var mu sync.Mutex
	runID, stopped := "", false
	a.trackRun(requestID, func() {
		mu.Lock()
		id := runID
		stopped = true
		mu.Unlock()
		if id != "" {
			a.CancelCLIRun(id)
		}
	})
	a.runSession(requestID, sessionID)
	end := func() {
		a.untrackRun(requestID)
		a.openTurns.Delete(requestID)
	}
	tag := func(m map[string]any) map[string]any {
		m["requestId"], m["engine"] = requestID, engine
		return m
	}
	label := engineLabel(engine)
	// busy is fresh as it came: fresh is the stream's own flag further down.
	busy := fresh

	// standIn hands the turn to the next engine in the chain, then to this
	// app's model, or reports why not.
	standIn := func(reason string) {
		end()
		reason = cliClip(strings.TrimSpace(reason), 300)
		if next := nextQueenEngine(s.Hive.QueenChain(), engine); next != "" {
			a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update",
				"content": label + " could not take this turn (" + reason + "); trying " + engineLabel(next)})
			a.routeToEngine(requestID, sessionID, next, message, busy)
			return
		}
		if !s.Hive.QueenFallsBack() {
			a.emit("chat:error", tag(map[string]any{"error": label + " could not answer: " + reason}))
			return
		}
		a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update",
			"content": label + " could not take this turn (" + reason + "); SuperAI answers it"})
		a.ownTurn(requestID, sessionID, message, nil, engine)
	}

	kit, err := a.queenKitFor(engine, &s)
	if err != nil {
		standIn(err.Error())
		return
	}
	o := cliStart{Agent: engine, Prompt: message, Ask: !s.ExternalAgents.Unattended, Chat: sessionID, Queen: kit}
	// fresh: the conversation's own session is still answering, so this
	// turn starts one of its own and is handed the conversation instead.
	// A CLI that keeps no session (hermes) is new to every turn.
	if prev := a.lastChatRun(sessionID, engine, true); prev != nil && !fresh && queenCLIs[engineCLI(engine)].Resumes {
		o.Thread, o.Session, o.Cwd, o.Model, o.Prev = prev.Thread, prev.Session, prev.Cwd, prev.Model, prev
	}
	// What was said here that the engine's session did not hear goes in
	// front of the question: the whole conversation for a session new to
	// it, the turns since its last answer for one it continues.
	if earlier := handoffText(a.unseenTurns(sessionID, engine, o.Prev != nil)); earlier != "" {
		o.Prompt = earlier + message
	}

	// The answer as it is written. Claude and pi send a message's text twice,
	// as deltas while they write and whole once they have; a message whose
	// deltas came is not written again.
	var said strings.Builder
	last, fresh := "", true
	write := func(chunk string) {
		if said.Len() > 0 && fresh {
			chunk = "\n\n" + chunk
		}
		said.WriteString(chunk)
		a.emit("chat:event", tag(map[string]any{"type": "partial", "content": chunk}))
	}
	// A CLI that prints only its last word (hermes) prints its failure the
	// same way: what it says is held until it is known to be an answer.
	holds := !queenCLIs[engineCLI(engine)].Streams
	o.Watch = func(e CLIRunEvent) {
		switch e.Kind {
		case "delta":
			write(e.Text)
			fresh = false
		case "text":
			last = e.Text
			if fresh && !holds {
				write(e.Text)
			}
			fresh = true
		case "tool":
			a.emit("chat:event", tag(map[string]any{"type": "tool_call", "tool": shortTool(e.Tool),
				"args": map[string]any{"detail": e.Detail}}))
		case "result":
			a.emit("chat:event", tag(map[string]any{"type": "tool_result", "tool": shortTool(e.Tool),
				"result": map[string]any{"ok": !e.Failed, "output": e.Text}}))
		case "note":
			if e.Text == "waiting for approval" {
				a.emit("chat:event", tag(map[string]any{"type": "state_update",
					"content": fmt.Sprintf("%s wants to use %s — waiting for your approval", label, shortTool(e.Tool))}))
			}
		}
	}
	o.Done = func(r CLIRun) {
		switch r.State {
		case "cancelled":
			end()
			a.emit("chat:cancelled", tag(map[string]any{"final": said.String()}))
			return
		case "failed":
			mu.Lock()
			byHand := stopped
			mu.Unlock()
			// Nothing said and nothing done: the engine never got going (a
			// login that ran out, a machine gone mid-start), and the person
			// still has a question.
			if said.Len() == 0 && r.Tools == 0 && !byHand {
				standIn(r.Error)
				return
			}
			end()
			a.emit("chat:error", tag(map[string]any{"error": label + " did not finish: " + r.Error}))
			return
		}
		end()
		answer := strings.TrimSpace(r.Summary)
		if answer == "" {
			answer = strings.TrimSpace(last)
		}
		if answer == "" {
			answer = strings.TrimSpace(said.String())
		}
		if holds && said.Len() == 0 && answer != "" {
			write(answer)
		}
		if err := a.appendExchange(sessionID, message, answer); err != nil {
			log.Printf("superai: could not record the queen engine's answer in %s: %v", sessionID, err)
		}
		// Claude reports what the whole session has cost so far; this turn's
		// share is what it added. The others report the turn's own.
		cost := r.CostUSD
		if engineCLI(engine) == "claude" && o.Prev != nil && r.Session == o.Prev.Session && cost >= o.Prev.CostUSD {
			cost -= o.Prev.CostUSD
		}
		recordTurnTimeBy(sessionID, message, started, time.Since(started), turnBy{Engine: engine, CostUSD: cost})
		done := tag(map[string]any{"final": answer})
		if cost > 0 {
			done["costUsd"] = cost
		}
		a.emit("chat:done", done)
	}

	a.emit("chat:event", tag(map[string]any{"type": "state_update", "content": "starting " + label + "…"}))
	run, err := a.startCLIRun(o)
	if err != nil {
		standIn(err.Error())
		return
	}
	mu.Lock()
	runID = run.ID
	cancelNow := stopped
	mu.Unlock()
	if cancelNow {
		a.CancelCLIRun(run.ID)
	}
}

// nextQueenEngine is the engine after this one in the chain, or "" when it
// is the last (or not in the chain at all, so a turn never goes round twice).
func nextQueenEngine(chain []string, engine string) string {
	if i := slices.Index(chain, engine); i >= 0 && i+1 < len(chain) {
		return chain[i+1]
	}
	return ""
}

// queenKitFor puts together what the engine is handed for one turn.
func (a *App) queenKitFor(engine string, s *backend.Settings) (*QueenKit, error) {
	_, _, remote := splitRemoteCLI(engine)
	var base string
	if remote {
		// The CLI is on another machine, so this app's loopback is not where
		// it can reach /mcp.
		if base = s.Hive.Public(); base == "" {
			return nil, fmt.Errorf("%s is on another machine and this SuperAI has no public address for it to reach (Settings → Hive → public address)", engine)
		}
	} else {
		u, err := a.localQueenURL()
		if err != nil {
			return nil, err
		}
		base = u
	}
	tok, err := a.queenEngineToken()
	if err != nil {
		return nil, fmt.Errorf("could not mint the queen's token: %w", err)
	}
	kit := &QueenKit{
		SystemPrompt: backend.ExternalQueenPrompt(s),
		Servers: map[string]any{queenHiveServer: map[string]any{
			"type": "http", "url": base + mcpPath,
			"headers": map[string]any{"Authorization": "Bearer ${" + queenTokenEnv + "}"},
		}},
		Allowed: []string{"mcp__" + queenHiveServer},
		Env:     map[string]string{queenTokenEnv: tok},
	}
	// Claude's lean flags travel with the kit, as they always have, so a
	// machine on an older SuperAI still runs its Claude lean.
	if engineCLI(engine) == "claude" {
		kit.Args = append([]string(nil), queenLeanArgs...)
		for k, v := range queenLeanEnv {
			kit.Env[k] = v
		}
	}
	for _, name := range backend.QueenEngineServers {
		if entry, env, ok := backend.EngineMCPServer(name, remote); ok {
			kit.Servers[name] = entry
			for k, v := range env {
				kit.Env[k] = v
			}
		}
	}
	return kit, nil
}

// queenMayCall reports whether a queen run may call an MCP tool without the
// person being asked: whenever the queen's own call of it would not be. The
// tool arrives as Claude and pi name it, mcp__<server>__<tool>.
func (a *App) queenMayCall(tool string, args map[string]any) bool {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return false
	}
	_, name, ok := strings.Cut(rest, "__")
	if !ok || name == "" {
		return false
	}
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	gate := a.gateForMCP
	if gate == nil {
		gate = svc.ToolGate()
	}
	if gate == nil {
		return false
	}
	return !gate.Policy()(agent.PermissionRequest{ToolName: name, ToolArgs: args})
}

// shortTool is a CLI's tool name as the trace shows it: an MCP tool by its
// own name, without the mcp__<server>__ in front.
func shortTool(t string) string {
	if rest, ok := strings.CutPrefix(t, "mcp__"); ok {
		if _, name, ok := strings.Cut(rest, "__"); ok && name != "" {
			return name
		}
	}
	return t
}

// engineLabel is how a person reads an engine's name.
func engineLabel(engine string) string {
	agent, host, ok := splitRemoteCLI(engine)
	if !ok {
		agent = engine
	}
	name := agentLabel(agent)
	if ok {
		name += " · " + host
	}
	return name
}

// standInNote opens an answer SuperAI's model gave in an engine's place.
func standInNote(engine string) string {
	return "> " + engineLabel(engine) + " 没能接这一条，由 SuperAI 内置的 queen 回答。\n\n"
}

// queenTokenCache is the engine's queen token, in memory only.
type queenTokenCache struct {
	mu     sync.Mutex
	secret string
}

// queenEngineToken is the queen token the engine reaches /mcp with: minted on
// first use, the same one every turn after, and minted again if it was
// revoked. The secret is in memory alone; the store keeps its hash, as for
// every token. One minted by an earlier run of this app is revoked when this
// one mints its own, since nothing holds that one's secret any more.
func (a *App) queenEngineToken() (string, error) {
	c := &a.queenToken
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := openIntegrations(integrationsPath())
	if err != nil {
		return "", err
	}
	if c.secret != "" && s.holds(c.secret, scopeQueen) {
		return c.secret, nil
	}
	old, err := s.list()
	if err != nil {
		return "", err
	}
	for _, t := range old {
		if t.Name == queenEngineTokenName && t.Scope == scopeQueen {
			if _, err := s.revoke(t.ID); err != nil {
				return "", err
			}
		}
	}
	_, secret, err := s.createScoped(queenEngineTokenName, scopeQueen)
	if err != nil {
		return "", err
	}
	c.secret = secret
	return secret, nil
}

// QueenEngineChoice is one thing the queen can think with.
type QueenEngineChoice struct {
	// Name is what Hive.QueenEngine is set to; "" is SuperAI's own agent.
	Name  string `json:"name"`
	Label string `json:"label"`
	// Online is whether it could take a turn now.
	Online bool `json:"online"`
}

// QueenEngines is what the queen thinks with, the engines tried after it,
// whether SuperAI stands in when none can answer, and every engine there is
// to choose from:
//
//	POST /api/rpc/QueenEngines  []
//	→ {"engine":"claude","backups":["claude.dell"],"fallback":true,"public_url":"","choices":[{"name":"","label":"SuperAI","online":true},…]}
func (a *App) QueenEngines() map[string]any {
	a.mu.Lock()
	var s backend.Settings
	if a.settings != nil {
		s = *a.settings
	}
	a.mu.Unlock()
	choices := []QueenEngineChoice{{Name: "", Label: "SuperAI", Online: true}}
	here := map[string]bool{}
	for _, c := range a.localAgentNames() {
		here[c.Name] = true
	}
	for _, name := range queenEngineNames {
		if here[name] {
			choices = append(choices, QueenEngineChoice{Name: name, Label: engineLabel(name), Online: true})
		}
	}
	remote := a.remoteCLIs()
	hosts := make([]string, 0, len(remote))
	for h := range remote {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	for _, h := range hosts {
		for _, name := range queenEngineNames {
			if slices.Contains(remote[h], name) {
				choices = append(choices, QueenEngineChoice{Name: name + "." + h, Label: engineLabel(name + "." + h), Online: true})
			}
		}
	}
	chain := s.Hive.QueenChain()
	for _, name := range chain {
		if !slices.ContainsFunc(choices, func(c QueenEngineChoice) bool { return c.Name == name }) {
			choices = append(choices, QueenEngineChoice{Name: name, Label: engineLabel(name)})
		}
	}
	cur, backups := "", []string{}
	if len(chain) > 0 {
		cur, backups = chain[0], chain[1:]
	}
	return map[string]any{"engine": cur, "backups": backups, "fallback": s.Hive.QueenFallsBack(), "public_url": s.Hive.PublicURL, "choices": choices}
}

// SetQueenEngine picks what the queen thinks with ("" is SuperAI's own agent)
// and whether SuperAI answers a turn the engine cannot take. Saved like any
// other settings change.
func (a *App) SetQueenEngine(engine string, fallback bool) (map[string]any, error) {
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	} else if s, err := backend.LoadSettings(); err == nil {
		next = *s
	}
	a.mu.Unlock()
	next.Hive.QueenEngine = strings.TrimSpace(engine)
	next.Hive.QueenFallback = &fallback
	if err := a.SaveSettings(next); err != nil {
		return nil, err
	}
	return a.QueenEngines(), nil
}

// SetQueenBackups sets the engines tried, in order, after the queen engine
// when it cannot take a turn:
//
//	POST /api/rpc/SetQueenBackups  [["claude.dell","codex"]]
func (a *App) SetQueenBackups(backups []string) (map[string]any, error) {
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	} else if s, err := backend.LoadSettings(); err == nil {
		next = *s
	}
	a.mu.Unlock()
	next.Hive.QueenBackups = backups
	if err := a.SaveSettings(next); err != nil {
		return nil, err
	}
	return a.QueenEngines(), nil
}

// validQueenEngine checks an engine before it is saved: a CLI that can think
// for the queen (queenCLIs), here with External agents on, or on a machine
// linked now. The engine already saved passes as it is, since its machine may
// only be asleep.
func (a *App) validQueenEngine(next *backend.Settings, prev string) error {
	// A backup is only tried when a turn needs it, so its machine may well be
	// asleep when it is chosen: it need only be a CLI that can think for her.
	for _, b := range next.Hive.QueenBackups {
		name, _, remote := splitRemoteCLI(strings.TrimSpace(b))
		if !remote {
			name = strings.TrimSpace(b)
		}
		if name != "" && !canThinkForQueen(name) {
			return fmt.Errorf("%s cannot think for the queen: Claude Code, Codex, pi and Hermes can", b)
		}
	}
	engine := strings.TrimSpace(next.Hive.QueenEngine)
	if engine == "" || engine == strings.TrimSpace(prev) {
		return nil
	}
	agentName, host, remote := splitRemoteCLI(engine)
	if !remote {
		agentName = engine
	}
	if !canThinkForQueen(agentName) {
		return fmt.Errorf("%s cannot think for the queen: Claude Code, Codex, pi and Hermes can", engine)
	}
	label := agentLabel(agentName)
	if remote {
		if !a.isRemoteCLI(engine) {
			return fmt.Errorf("no linked machine called %s has %s right now", host, label)
		}
		return nil
	}
	if !next.ExternalAgents.Enabled {
		return fmt.Errorf("the queen engine is %s on this machine, so External agents has to be on", label)
	}
	for _, c := range installedCLIs(next.ExternalAgents.Binaries) {
		if c.Name == agentName {
			return nil
		}
	}
	return fmt.Errorf("%s is not installed on this machine", label)
}

// How much of the earlier conversation an engine is handed: the last few
// turns, each and all of them cut to a size that is context and not a
// transcript.
const (
	handoffTurns    = 6
	handoffTurnMax  = 1200
	handoffTotalMax = 6000
)

// sessionTurns is a conversation as ChatHistory shows it, with who answered.
func (a *App) sessionTurns(sessionID string) []backend.ChatTurn {
	if a.turnsFn != nil {
		return withTurnTimes(sessionID, a.turnsFn(sessionID))
	}
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	return withTurnTimes(sessionID, svc.SessionTurns(sessionID))
}

// unseenTurns are the turns of a conversation the engine's session has not
// heard: all of them for a new session, those after its last answer for one
// it resumes. A resumed session whose last answer is not found (the history
// was compacted) is handed nothing rather than a guess.
func (a *App) unseenTurns(sessionID, engine string, resumed bool) []backend.ChatTurn {
	turns := a.sessionTurns(sessionID)
	if len(turns) == 0 && !resumed {
		turns = a.engineRunTurns(sessionID, engine)
	}
	from := 0
	if resumed {
		from = -1
		for i := len(turns) - 1; i >= 0; i-- {
			if turns[i].Role == "assistant" && turns[i].Engine == engine {
				from = i + 1
				break
			}
		}
		if from < 0 {
			return nil
		}
	}
	var out []backend.ChatTurn
	for _, t := range turns[from:] {
		if t.Kind == "" && (t.Role == "user" || t.Role == "assistant") && strings.TrimSpace(t.Content) != "" {
			out = append(out, t)
		}
	}
	return out
}

// handoffEnd closes the earlier conversation in front of a question.
const handoffEnd = "[End of the earlier conversation. The person now says:]\n\n"

// engineRunTurns is a conversation as the engine's own finished runs in it
// tell it, for an app with no history of its own to keep it in (no model
// set up): each run's question, without what it was handed in front of it,
// and its answer. A CLI that keeps no session of its own then still hears
// what was said.
func (a *App) engineRunTurns(sessionID, engine string) []backend.ChatTurn {
	s := a.cliStore()
	s.mu.Lock()
	var runs []CLIRun
	for _, r := range s.runs {
		if r.Chat == sessionID && r.Agent == engine && r.Queen && r.State == "done" && strings.TrimSpace(r.Summary) != "" {
			runs = append(runs, summaryOf(r))
		}
	}
	s.mu.Unlock()
	slices.SortFunc(runs, func(x, y CLIRun) int { return x.Started.Compare(y.Started) })
	var out []backend.ChatTurn
	for _, r := range runs {
		q := r.Prompt
		if i := strings.LastIndex(q, handoffEnd); i >= 0 {
			q = q[i+len(handoffEnd):]
		}
		out = append(out, backend.ChatTurn{Role: "user", Content: q}, backend.ChatTurn{Role: "assistant", Content: r.Summary, Engine: engine})
	}
	return out
}

// handoffText is the earlier conversation as it goes in front of a question,
// oldest first and marked as what it is.
func handoffText(turns []backend.ChatTurn) string {
	if len(turns) > handoffTurns {
		turns = turns[len(turns)-handoffTurns:]
	}
	var lines []string
	size := 0
	for i := len(turns) - 1; i >= 0; i-- {
		who := "Person"
		if turns[i].Role == "assistant" {
			who = "Queen"
		}
		line := who + ": " + cliClip(strings.TrimSpace(turns[i].Content), handoffTurnMax)
		if size+len(line) > handoffTotalMax {
			break
		}
		size += len(line)
		lines = append([]string{line}, lines...)
	}
	if len(lines) == 0 {
		return ""
	}
	return "[Earlier in this conversation, oldest first. You did not see these turns; they are context, not requests.]\n" +
		strings.Join(lines, "\n\n") + "\n" + handoffEnd
}
