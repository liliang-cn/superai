package app

// Agent CLI runs as things a person can watch, steer and stop.
//
// cli_agent_run hands a task to Claude Code or Codex from inside a model turn
// and blocks until it is done; nobody sees what the CLI does along the way, and
// its session is gone with the turn. These runs are the other way in: started
// from a page or the phone, in the background, every tool call and result
// pushed as it happens, a follow-up resumes the same CLI session, and each can
// be stopped on its own.
//
// Claude Code's permission prompts come here too. Instead of bypassing them, a
// run in "ask" mode points --permission-prompt-tool at a small MCP server this
// process serves on loopback; each prompt becomes one of the app's own approval
// cards, so a Bash call Claude wants to make is answered on the desktop or the
// phone like any other.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agentexec"
	agentexecpty "github.com/liliang-cn/agentexec/pty"
	"github.com/liliang-cn/superai/internal/backend"
)

// CLIRunEvent is one thing a CLI run did, normalised across CLIs.
type CLIRunEvent struct {
	Seq int       `json:"seq"`
	At  time.Time `json:"at"`
	// Kind is text (the agent talking), tool (a call), result (what came
	// back), note (the CLI or this app saying something) or error.
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Detail string `json:"detail,omitempty"`
	CallID string `json:"callId,omitempty"`
	Failed bool   `json:"failed,omitempty"`
}

// CLIRun is one invocation of an agent CLI. Follow-ups are runs of their own
// that share the first run's Thread and resume its Session.
type CLIRun struct {
	ID      string `json:"id"`
	Thread  string `json:"thread"`
	Agent   string `json:"agent"`
	Prompt  string `json:"prompt"`
	Cwd     string `json:"cwd"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
	// Ask sends the CLI's permission prompts to the user; off, it runs with
	// them bypassed.
	Ask bool `json:"ask"`
	// Chat is the conversation an @mention started this run from, if any;
	// the next @mention of the same agent there continues this session.
	Chat    string     `json:"chat,omitempty"`
	State   string     `json:"state"`
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`
	Summary string     `json:"summary,omitempty"`
	Error   string     `json:"error,omitempty"`
	In      int        `json:"in"`
	Out     int        `json:"out"`
	Cache   int        `json:"cache"`
	CostUSD float64    `json:"costUsd"`
	Tools   int        `json:"tools"`
	Events  []CLIRunEvent `json:"events,omitempty"`
}

const (
	cliRunsKept      = 200
	cliRunEventsKept = 1500
	cliRunTextMax    = 8 << 10
)

type cliRunStore struct {
	mu      sync.Mutex
	runs    map[string]*CLIRun
	cancels map[string]context.CancelFunc
	// watch and done are the hooks of a run started for a chat turn.
	watch map[string]func(CLIRunEvent)
	done  map[string]func(CLIRun)
}

// cliStart is everything a run is started with.
type cliStart struct {
	Agent, Prompt, Cwd, Model string
	Ask                       bool
	Thread, Session, Chat     string
	Watch                     func(CLIRunEvent)
	Done                      func(CLIRun)
}

func (a *App) cliStore() *cliRunStore {
	a.cliOnce.Do(func() {
		a.cli = &cliRunStore{runs: map[string]*CLIRun{}, cancels: map[string]context.CancelFunc{},
			watch: map[string]func(CLIRunEvent){}, done: map[string]func(CLIRun){}}
		a.cli.load()
	})
	return a.cli
}

func cliRunsPath() string { return filepath.Join(backend.DataDir(), "cli-runs.json") }

func (s *cliRunStore) load() {
	raw, err := os.ReadFile(cliRunsPath())
	if err != nil {
		return
	}
	var runs []*CLIRun
	if json.Unmarshal(raw, &runs) != nil {
		return
	}
	for _, r := range runs {
		// A run that was going when the process died is not going now.
		if r.State == "running" {
			r.State = "cancelled"
			r.Error = "SuperAI stopped while this was running"
		}
		s.runs[r.ID] = r
	}
}

// saveLocked writes the newest runs. Called with s.mu held, at the end of a
// run, not per event: a run's events are in memory while it goes.
func (s *cliRunStore) saveLocked() {
	runs := s.sortedLocked()
	if len(runs) > cliRunsKept {
		for _, r := range runs[cliRunsKept:] {
			delete(s.runs, r.ID)
		}
		runs = runs[:cliRunsKept]
	}
	raw, err := json.Marshal(runs)
	if err != nil {
		return
	}
	tmp := cliRunsPath() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, cliRunsPath())
	}
}

func (s *cliRunStore) sortedLocked() []*CLIRun {
	out := make([]*CLIRun, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

func summaryOf(r *CLIRun) CLIRun {
	c := *r
	c.Events = nil
	return c
}

// CLIRuns lists runs newest first, without their events.
func (a *App) CLIRuns() []CLIRun {
	s := a.cliStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []CLIRun{}
	for _, r := range s.sortedLocked() {
		out = append(out, summaryOf(r))
	}
	return out
}

// CLIRunDetail is one run with everything it did.
func (a *App) CLIRunDetail(id string) (CLIRun, error) {
	s := a.cliStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[id]
	if r == nil {
		return CLIRun{}, fmt.Errorf("no run %s", id)
	}
	c := *r
	c.Events = append([]CLIRunEvent(nil), r.Events...)
	return c, nil
}

// StartCLIRun starts an agent CLI on a task in the background.
func (a *App) StartCLIRun(agent, prompt, cwd, model string, ask bool) (CLIRun, error) {
	r, err := a.startCLIRun(cliStart{Agent: agent, Prompt: prompt, Cwd: cwd, Model: model, Ask: ask})
	if err != nil {
		return CLIRun{}, err
	}
	return r, nil
}

// FollowUpCLIRun continues a run's CLI session with another prompt.
func (a *App) FollowUpCLIRun(id, prompt string) (CLIRun, error) {
	s := a.cliStore()
	s.mu.Lock()
	prev := s.runs[id]
	if prev == nil {
		s.mu.Unlock()
		return CLIRun{}, fmt.Errorf("no run %s", id)
	}
	// The newest run in the thread holds the session to resume.
	last := prev
	for _, r := range s.runs {
		if r.Thread == prev.Thread && r.Started.After(last.Started) {
			last = r
		}
	}
	p := *last
	busy := last.State == "running"
	s.mu.Unlock()
	if busy {
		return CLIRun{}, errors.New("the last turn is still running; wait for it or stop it")
	}
	if p.Session == "" {
		return CLIRun{}, fmt.Errorf("%s did not report a session to continue", p.Agent)
	}
	return a.startCLIRun(cliStart{Agent: p.Agent, Prompt: prompt, Cwd: p.Cwd, Model: p.Model, Ask: p.Ask,
		Thread: p.Thread, Session: p.Session, Chat: p.Chat})
}

// CancelCLIRun stops a running CLI.
func (a *App) CancelCLIRun(id string) string {
	s := a.cliStore()
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel == nil {
		return "that run is not running"
	}
	cancel()
	return "ok"
}

func (a *App) startCLIRun(o cliStart) (CLIRun, error) {
	agent, prompt, cwd, model, ask, thread, session := strings.TrimSpace(o.Agent), strings.TrimSpace(o.Prompt), o.Cwd, o.Model, o.Ask, o.Thread, o.Session
	if prompt == "" {
		return CLIRun{}, errors.New("the prompt is empty")
	}
	a.mu.Lock()
	var ext backend.ExternalAgents
	if a.settings != nil {
		ext = a.settings.ExternalAgents
	}
	a.mu.Unlock()
	if !ext.Enabled {
		return CLIRun{}, errors.New("agent CLIs are switched off in Settings (External agents)")
	}
	installed := agentexec.Discover(ext.Binaries)
	registry := agentexec.RegistryFrom(installed)
	id := uuid.NewString()
	provider, err := registry.Get(agent)
	if err != nil {
		return CLIRun{}, fmt.Errorf("%s is not installed here", agent)
	}
	dir, err := a.cliCwd(cwd, ext.Roots)
	if err != nil {
		return CLIRun{}, err
	}
	if model == "" {
		model = ext.Models[agent]
	}

	if thread == "" {
		thread = id
	}
	run := &CLIRun{ID: id, Thread: thread, Agent: agent, Prompt: prompt, Cwd: dir, Model: model,
		Session: session, Ask: ask, Chat: o.Chat, State: "running", Started: time.Now()}

	req := agentexec.Request{
		RunID: id, Prompt: prompt, WorkspacePath: dir, Model: model,
		ResumeSessionID: session, PermissionMode: agentexec.PermissionBypass,
	}
	// Claude's MCP servers go on the command line as JSON, never as a file:
	// agentexec writes its config into the working directory, where the agent
	// lists it among the user's files and could read the approval URL.
	// Strict, so the user's own servers stay out of a delegated run.
	if agent == "claude" {
		servers := map[string]any{}
		if ask {
			url, err := a.cliApproveURL(id)
			if err != nil {
				return CLIRun{}, fmt.Errorf("could not open the approval channel: %w", err)
			}
			servers["superai"] = map[string]any{"type": "http", "url": url}
		}
		cfg, _ := json.Marshal(map[string]any{"mcpServers": servers})
		req.ExtraArgs = []string{"--mcp-config", string(cfg), "--strict-mcp-config"}
	}
	if ask {
		switch agent {
		case "claude":
			req.PermissionMode = agentexec.PermissionDefault
			req.ExtraArgs = append(req.ExtraArgs, "--permission-prompt-tool", "mcp__superai__approve")
		case "codex":
			// Codex has no prompt to forward in exec mode; ask means its own
			// sandbox: writes inside the workspace only, no network.
			req.PermissionMode = agentexec.PermissionDefault
			req.Sandbox = true
			// -c rather than --sandbox: `codex exec resume` takes no --sandbox.
			req.ExtraArgs = []string{"--skip-git-repo-check", "-c", `sandbox_mode="workspace-write"`}
		}
	}
	sess := provider.NewSession()
	spec, err := sess.BuildCommand(context.Background(), req)
	if err != nil {
		return CLIRun{}, fmt.Errorf("could not build the %s command: %w", agent, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), ext.Timeout())
	s := a.cliStore()
	s.mu.Lock()
	s.runs[id] = run
	s.cancels[id] = cancel
	if o.Watch != nil {
		s.watch[id] = o.Watch
	}
	if o.Done != nil {
		s.done[id] = o.Done
	}
	s.mu.Unlock()
	a.emit("cli:run", runPayload(run))

	go a.driveCLIRun(ctx, cancel, run, sess, spec)
	return summaryOf(run), nil
}

func (a *App) driveCLIRun(ctx context.Context, cancel context.CancelFunc, run *CLIRun, sess agentexec.Session, spec agentexec.CommandSpec) {
	defer cancel()
	calls := map[string]string{} // call id → tool name, to label results
	push := func(evs []agentexec.Event) {
		for _, e := range evs {
			for _, ce := range normaliseCLIEvent(e, calls) {
				a.addCLIEvent(run, ce)
			}
		}
	}
	res, runErr := agentexecpty.Run(ctx, agentexecpty.Command{Argv: spec.Argv, Env: spec.Env, WorkDir: spec.WorkDir},
		func(chunk []byte) {
			evs, _ := sess.ParseChunk(chunk)
			push(evs)
			if id := sess.SessionID(); id != "" {
				a.setCLISession(run, id)
			}
		})
	result, tail, finErr := sess.Finalize(context.Background(), res.Output, res.ExitCode)
	push(tail)

	s := a.cliStore()
	s.mu.Lock()
	now := time.Now()
	run.Ended = &now
	if id := sess.SessionID(); id != "" {
		run.Session = id
	}
	run.Summary = strings.TrimSpace(result.Summary)
	run.In, run.Out, run.Cache = int(result.Usage.InputTokens), int(result.Usage.OutputTokens), int(result.Usage.CacheTokens)
	run.CostUSD = result.Usage.EstimatedCostUSD
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		run.State, run.Error = "cancelled", "stopped"
	case errors.Is(runErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		run.State, run.Error = "failed", "did not finish in time and was stopped"
	case runErr != nil:
		run.State, run.Error = "failed", runErr.Error()
	case finErr != nil:
		run.State, run.Error = "failed", finErr.Error()
	case result.Failed || res.ExitCode != 0:
		run.State = "failed"
		run.Error = run.Summary
		if run.Error == "" {
			run.Error = cliLastLine(string(res.Output))
		}
	default:
		run.State = "done"
	}
	delete(s.cancels, run.ID)
	delete(s.watch, run.ID)
	done := s.done[run.ID]
	delete(s.done, run.ID)
	s.saveLocked()
	payload := runPayload(run)
	final := summaryOf(run)
	s.mu.Unlock()
	a.emit("cli:run", payload)
	if done != nil {
		done(final)
	}
}

func (a *App) addCLIEvent(run *CLIRun, e CLIRunEvent) {
	s := a.cliStore()
	s.mu.Lock()
	e.Seq = len(run.Events) + 1
	if n := len(run.Events); n > 0 {
		e.Seq = run.Events[n-1].Seq + 1
	}
	e.At = time.Now()
	run.Events = append(run.Events, e)
	if len(run.Events) > cliRunEventsKept {
		run.Events = run.Events[len(run.Events)-cliRunEventsKept:]
	}
	if e.Kind == "tool" {
		run.Tools++
	}
	watch := s.watch[run.ID]
	s.mu.Unlock()
	if watch != nil {
		watch(e)
	}
	a.emit("cli:event", map[string]any{"run": run.ID, "thread": run.Thread, "event": e})
}

func (a *App) setCLISession(run *CLIRun, id string) {
	s := a.cliStore()
	s.mu.Lock()
	changed := run.Session != id
	run.Session = id
	payload := runPayload(run)
	s.mu.Unlock()
	if changed {
		a.emit("cli:run", payload)
	}
}

func runPayload(r *CLIRun) map[string]any {
	raw, _ := json.Marshal(summaryOf(r))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

// cliCwd resolves where a run works: inside the workspace or one of the
// configured roots, nowhere else.
func (a *App) cliCwd(raw string, roots []string) (string, error) {
	ws := a.workspaceDir()
	allowed := []string{}
	for _, r := range append([]string{ws}, roots...) {
		if r = strings.TrimSpace(r); r == "" {
			continue
		}
		if abs, err := filepath.Abs(cliExpandHome(r)); err == nil {
			if real, err := filepath.EvalSymlinks(abs); err == nil {
				abs = real
			}
			allowed = append(allowed, abs)
		}
	}
	if len(allowed) == 0 {
		return "", errors.New("no workspace is set, so there is nowhere a CLI may run")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return allowed[0], nil
	}
	dir, err := filepath.Abs(cliExpandHome(raw))
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", raw)
	}
	for _, root := range allowed {
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("%s is outside the workspace and the allowed roots", raw)
}

func cliExpandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// ansi matches terminal control sequences: the pty hands them over with the
// output, and "[?25h" is not a reason anyone can act on.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07|\x1b[()][A-Z0-9]`)

func cliLastLine(s string) string {
	s = strings.TrimSpace(ansi.ReplaceAllString(s, ""))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return cliClip(s, 400)
}

func cliClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// normaliseCLIEvent turns a provider's event into what the run view shows.
// Claude hands over raw tool_use blocks and user messages carrying
// tool_result blocks; Codex hands over items. Anything else the CLI says
// about itself (init, hooks, rate limits) is dropped: it is not the work.
func normaliseCLIEvent(e agentexec.Event, calls map[string]string) []CLIRunEvent {
	p := e.Payload
	switch e.Type {
	case agentexec.EventAgentMessage:
		switch p["role"] {
		case "assistant":
			if t, _ := p["text"].(string); strings.TrimSpace(t) != "" {
				return []CLIRunEvent{{Kind: "text", Text: cliClip(t, cliRunTextMax)}}
			}
		case "error":
			if t, _ := p["text"].(string); t != "" {
				return []CLIRunEvent{{Kind: "error", Text: cliClip(t, cliRunTextMax)}}
			}
		}
	case agentexec.EventToolCall:
		name, detail, id := toolCallParts(p)
		if id != "" {
			calls[id] = name
		}
		out := []CLIRunEvent{{Kind: "tool", Tool: name, Detail: detail, CallID: id}}
		// Codex reports a command once, finished, with its output in the
		// same item: the call and its result arrive together.
		if pickStr(p, "type") == "command_execution" && pickStr(p, "status") != "in_progress" {
			out = append(out, toolResults(p, calls)...)
		}
		return out
	case agentexec.EventToolResult:
		return toolResults(p, calls)
	}
	return nil
}

func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// toolCallParts reads a tool call's name, the one line that says what it
// does, and its id.
func toolCallParts(p map[string]any) (name, detail, id string) {
	id = pickStr(p, "id", "call_id")
	// Codex items.
	switch pickStr(p, "type") {
	case "command_execution":
		return "shell", cliClip(pickStr(p, "command"), 600), id
	case "file_change":
		var paths []string
		if ch, ok := p["changes"].([]any); ok {
			for _, c := range ch {
				if cm, ok := c.(map[string]any); ok {
					paths = append(paths, pickStr(cm, "path"))
				}
			}
		}
		return "edit", strings.Join(paths, ", "), id
	case "mcp_tool_call":
		return pickStr(p, "server") + "." + pickStr(p, "tool"), "", id
	case "web_search":
		return "web_search", pickStr(p, "query"), id
	}
	name = pickStr(p, "name", "tool_name", "tool")
	if name == "" {
		name = "tool"
	}
	in, _ := p["input"].(map[string]any)
	if in != nil {
		detail = pickStr(in, "command", "file_path", "path", "pattern", "url", "query", "description", "prompt")
		if detail == "" {
			raw, _ := json.Marshal(in)
			detail = string(raw)
		}
	}
	return name, cliClip(detail, 600), id
}

func toolResults(p map[string]any, calls map[string]string) []CLIRunEvent {
	// Codex: the item itself, finished.
	if t := pickStr(p, "type"); t != "" && t != "user" {
		out := pickStr(p, "aggregated_output", "output", "result")
		failed := pickStr(p, "status") == "failed"
		if code, ok := p["exit_code"].(float64); ok && code != 0 {
			failed = true
		}
		id := pickStr(p, "id")
		return []CLIRunEvent{{Kind: "result", Tool: calls[id], CallID: id, Text: cliClip(out, cliRunTextMax), Failed: failed}}
	}
	// Claude: a user message whose content holds tool_result blocks.
	msg, _ := p["message"].(map[string]any)
	content, _ := msg["content"].([]any)
	var out []CLIRunEvent
	for _, c := range content {
		b, ok := c.(map[string]any)
		if !ok || b["type"] != "tool_result" {
			continue
		}
		id := pickStr(b, "tool_use_id")
		failed, _ := b["is_error"].(bool)
		out = append(out, CLIRunEvent{Kind: "result", Tool: calls[id], CallID: id, Text: cliClip(resultText(b["content"]), cliRunTextMax), Failed: failed})
	}
	return out
}

func resultText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				if s := pickStr(m, "text"); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
