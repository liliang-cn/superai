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
	"github.com/liliang-cn/superleo/internal/backend"
)

// CLIRunEvent is one thing a CLI run did, normalised across CLIs.
type CLIRunEvent struct {
	Seq int       `json:"seq"`
	At  time.Time `json:"at"`
	// Kind is text (the agent talking), tool (a call), result (what came
	// back), note (the CLI or this app saying something) or error. A queen
	// run also has delta, a piece of text as it is written, which goes to the
	// turn watching it and is not kept.
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
	Chat string `json:"chat,omitempty"`
	// Queen marks a run thinking for the queen (app_queen_engine.go). Its
	// session is the conversation's queen session, apart from any @mention
	// of the same agent there.
	Queen bool `json:"queen,omitempty"`
	// Remote is the SuperLeo this run is happening on, by its name in Remote
	// agents; RemoteRun is the run's id there. Empty for a run on this machine.
	Remote    string `json:"remote,omitempty"`
	RemoteRun string `json:"remoteRun,omitempty"`
	// Origin is who handed the run out: a chat turn, a hive task, core over
	// the agent link, a page.
	Origin string `json:"origin,omitempty"`
	// PID is the CLI's process while it runs on this machine, outside
	// SuperLeo's own (cli_live.go).
	PID int `json:"pid,omitempty"`
	// Resumed counts the times the CLI's session was resumed because
	// SuperLeo came back to find it gone.
	Resumed int `json:"resumed,omitempty"`
	// Paused says why a run on another machine is waiting: the line to it
	// dropped and this side is waiting for it to come back.
	Paused  string        `json:"paused,omitempty"`
	State   string        `json:"state"`
	Started time.Time     `json:"started"`
	Ended   *time.Time    `json:"ended,omitempty"`
	Summary string        `json:"summary,omitempty"`
	Error   string        `json:"error,omitempty"`
	In      int           `json:"in"`
	Out     int           `json:"out"`
	Cache   int           `json:"cache"`
	CostUSD float64       `json:"costUsd"`
	Tools   int           `json:"tools"`
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
	// live is the journal of each run going on this machine (cli_live.go).
	live map[string]*cliDriver
}

// cliStart is everything a run is started with.
type cliStart struct {
	Agent, Prompt, Cwd, Model string
	Ask                       bool
	Thread, Session, Chat     string
	// Origin is who handed the run out, kept with it.
	Origin string
	// Prev is the run this one continues, if any.
	Prev *CLIRun
	// Queen, when set, makes the run one thinking for the queen.
	Queen *QueenKit
	Watch func(CLIRunEvent)
	Done  func(CLIRun)
}

func (a *App) cliStore() *cliRunStore {
	a.cliOnce.Do(func() {
		a.cli = &cliRunStore{runs: map[string]*CLIRun{}, cancels: map[string]context.CancelFunc{},
			watch: map[string]func(CLIRunEvent){}, done: map[string]func(CLIRun){}, live: map[string]*cliDriver{}}
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
		// A run that was going when the process died is not going now,
		// unless its journal says where to pick it up (recoverCLIRuns).
		if r.State == "running" && !cliLiveExists(r.ID) {
			r.State = "cancelled"
			r.Error = "SuperLeo stopped while this was running"
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
	r, err := a.startCLIRun(cliStart{Agent: agent, Prompt: prompt, Cwd: cwd, Model: model, Ask: ask, Origin: "a page"})
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
	if p.Session == "" && p.RemoteRun == "" {
		return CLIRun{}, fmt.Errorf("%s did not report a session to continue", p.Agent)
	}
	return a.startCLIRun(cliStart{Agent: p.Agent, Prompt: prompt, Cwd: p.Cwd, Model: p.Model, Ask: p.Ask,
		Thread: p.Thread, Session: p.Session, Chat: p.Chat, Origin: p.Origin, Prev: &p})
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
	// SuperLeo stopping ends the turns that wait on runs, and they stop
	// their runs as they go: the runs carry on instead, and are picked up
	// when it is back (cli_live.go).
	if a.cliDetached.Load() {
		return "SuperLeo is stopping; the run carries on and is picked up when it is back"
	}
	cancel()
	return "ok"
}

func (a *App) startCLIRun(o cliStart) (CLIRun, error) {
	agent, prompt, cwd, model, ask, thread, session := strings.TrimSpace(o.Agent), strings.TrimSpace(o.Prompt), o.Cwd, o.Model, o.Ask, o.Thread, o.Session
	// With YOLO on, or the gate switched off, nobody is asked: the agent runs
	// in its own no-prompt mode rather than forwarding prompts to a card.
	if ask && a.terminalYolo() != "" {
		ask = false
	}
	if prompt == "" {
		return CLIRun{}, errors.New("the prompt is empty")
	}
	if name, host, ok := splitRemoteCLI(agent); ok && a.isRemoteCLI(agent) {
		o.Agent, o.Prompt = name, prompt
		return a.startRemoteCLIRun(o, host)
	}
	provider, ext, err := a.cliProvider(agent)
	if err != nil {
		return CLIRun{}, err
	}
	id := uuid.NewString()
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
		Session: session, Ask: ask, Chat: o.Chat, Queen: o.Queen != nil, Origin: o.Origin, State: "running", Started: time.Now()}

	req := agentexec.Request{
		RunID: id, Prompt: prompt, WorkspacePath: dir, Model: model,
		ResumeSessionID: session, PermissionMode: agentexec.PermissionBypass,
		// This run reports through cli:run already; `superleo hook` sees this
		// and stays out of it (cchook.go).
		Env: map[string]string{"SUPERLEO_CLI_RUN": id},
	}
	// The CLI goes out the way this machine's own requests do (Settings →
	// Runtime → Proxy); its own servers here stay direct through NO_PROXY.
	for k, v := range backend.ProxyEnv() {
		req.Env[k] = v
	}
	if req.Env["HTTPS_PROXY"] != "" {
		req.Env["NODE_USE_ENV_PROXY"] = "1" // Node CLIs (pi, codex's npm shim) read the proxy only with this
	}
	// A run thinking for the queen is handed her kit the way its CLI takes
	// it (queen_engine_cli.go).
	approve := ""
	if q := o.Queen; q != nil {
		eng, ok := queenCLIs[agent]
		if !ok {
			return CLIRun{}, fmt.Errorf("%s cannot think for the queen", agent)
		}
		if ask && eng.Asks {
			if approve, err = a.cliApproveURL(id); err != nil {
				return CLIRun{}, fmt.Errorf("could not open the approval channel: %w", err)
			}
		}
		if err := eng.apply(&req, q, ask, approve); err != nil {
			return CLIRun{}, err
		}
	} else if agent == "claude" {
		if ask {
			if approve, err = a.cliApproveURL(id); err != nil {
				return CLIRun{}, fmt.Errorf("could not open the approval channel: %w", err)
			}
		}
		req.ExtraArgs = append(req.ExtraArgs, claudeMCPArgs(nil, approve)...)
	}
	if ask {
		switch agent {
		case "claude":
			req.PermissionMode = agentexec.PermissionDefault
			req.ExtraArgs = append(req.ExtraArgs, "--permission-prompt-tool", "mcp__superleo__approve")
			// Claude gives an MCP call a short while and then reports "The
			// operation timed out": a person reaching for their phone took
			// longer than that, and Claude gave up on the command. The call
			// is allowed as long as the approval card waits, and a little more.
			req.Env["MCP_TOOL_TIMEOUT"] = fmt.Sprint((cliApproveWait + time.Minute).Milliseconds())
		case "codex":
			// Codex has no prompt to forward in exec mode; ask means its own
			// sandbox: writes inside the workspace only. The network stays
			// open in it — an agent sent to look something up (a repository's
			// activity, a release page) otherwise fails on its first request,
			// and what the sandbox is for is what it may change, not read.
			req.PermissionMode = agentexec.PermissionDefault
			req.Sandbox = true
			// -c rather than --sandbox: `codex exec resume` takes no --sandbox.
			req.ExtraArgs = append(req.ExtraArgs, "--skip-git-repo-check", "-c", `sandbox_mode="workspace-write"`,
				"-c", "sandbox_workspace_write.network_access=true")
		}
	}
	sess := provider.NewSession()
	spec, err := sess.BuildCommand(context.Background(), req)
	if err != nil {
		return CLIRun{}, fmt.Errorf("could not build the %s command: %w", agent, err)
	}

	ctx, cancel := backend.WithLimit(context.Background(), ext.Timeout())
	live := &cliLive{Req: req, Approve: approve}
	if dl, ok := ctx.Deadline(); ok {
		live.Deadline = dl
	}
	d := newCLIDriver(a, run, live, provider, sess)
	s := a.cliStore()
	s.mu.Lock()
	s.runs[id] = run
	s.cancels[id] = cancel
	s.live[id] = d
	if o.Watch != nil {
		s.watch[id] = o.Watch
	}
	if o.Done != nil {
		s.done[id] = o.Done
	}
	s.mu.Unlock()
	// The CLI runs in a session of its own, writing to a file, so SuperLeo
	// can stop and start again around it (cli_live.go).
	wait, err := d.launch(spec)
	if err != nil {
		cancel()
		// The caller hears it as the error returned, not from its hook too.
		s.mu.Lock()
		delete(s.watch, id)
		delete(s.done, id)
		s.mu.Unlock()
		a.finishCLIRun(run, func(run *CLIRun) { run.State, run.Error = "failed", err.Error() })
		return CLIRun{}, fmt.Errorf("could not start %s: %w", agent, err)
	}
	a.emit("cli:run", runPayload(run))

	// Read before the run is under way: from then on it changes under the
	// store's lock, which this does not hold.
	s.mu.Lock()
	out := summaryOf(run)
	s.mu.Unlock()
	go d.drive(ctx, cancel, wait, 0)
	return out, nil
}

// cliProvider is the agentexec provider that runs agent here, with the
// settings it runs under.
func (a *App) cliProvider(agent string) (agentexec.Provider, backend.ExternalAgents, error) {
	a.mu.Lock()
	var ext backend.ExternalAgents
	if a.settings != nil {
		ext = a.settings.ExternalAgents
	}
	a.mu.Unlock()
	if !ext.Enabled {
		return nil, ext, errors.New("agent CLIs are switched off in Settings (External agents)")
	}
	for _, d := range ext.Disabled {
		if strings.EqualFold(strings.TrimSpace(d), agent) {
			return nil, ext, fmt.Errorf("%s is switched off here (external_agents.disabled)", agent)
		}
	}
	registry := agentexec.RegistryFrom(installedCLIs(ext.Binaries))
	registerCodingTools(registry, ext)
	provider, err := registry.Get(agent)
	if err != nil {
		return nil, ext, fmt.Errorf("%s is not installed here", agent)
	}
	return provider, ext, nil
}

// finishCLIRun records how a run ended, saves, tells the UI, and calls the
// hook of a chat turn waiting on it. set fills in the outcome, under the lock.
func (a *App) finishCLIRun(run *CLIRun, set func(*CLIRun)) {
	s := a.cliStore()
	s.mu.Lock()
	if run.State != "running" {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	run.Ended = &now
	run.Paused = ""
	set(run)
	delete(s.cancels, run.ID)
	delete(s.watch, run.ID)
	done := s.done[run.ID]
	delete(s.done, run.ID)
	live := s.live[run.ID]
	delete(s.live, run.ID)
	run.PID = 0
	s.saveLocked()
	payload := runPayload(run)
	final := summaryOf(run)
	s.mu.Unlock()
	// Saved with the rest now: the journal it was picked up by goes.
	if live != nil {
		live.forget()
	}
	a.emit("cli:run", payload)
	if done != nil {
		done(final)
	}
}

func (a *App) addCLIEvent(run *CLIRun, e CLIRunEvent) {
	s := a.cliStore()
	if e.Kind == "delta" {
		// Only the turn wants these. They are sent on from where the CLI
		// runs, for a core mirroring it, and kept nowhere.
		s.mu.Lock()
		watch := s.watch[run.ID]
		s.mu.Unlock()
		if watch != nil {
			watch(e)
		}
		if run.Remote == "" {
			a.emit("cli:event", map[string]any{"run": run.ID, "thread": run.Thread, "event": e})
		}
		return
	}
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
	live := s.live[run.ID]
	s.mu.Unlock()
	if live != nil {
		live.record(e)
	}
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
	live := s.live[run.ID]
	s.mu.Unlock()
	if changed {
		if live != nil {
			live.save()
		}
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
		// The workspace is the app's own: made when it is not there yet (a
		// fresh install, a folder someone cleared) rather than failing the run.
		if err := os.MkdirAll(allowed[0], 0o755); err != nil {
			return "", err
		}
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
			// pi: a piece of a message as it is written; the whole message
			// follows when it ends.
			if d, _ := p["delta"].(bool); d {
				if t, _ := p["text"].(string); t != "" {
					return []CLIRunEvent{{Kind: "delta", Text: t}}
				}
				return nil
			}
			if t, _ := p["text"].(string); strings.TrimSpace(t) != "" {
				return []CLIRunEvent{{Kind: "text", Text: cliClip(t, cliRunTextMax)}}
			}
		case "error":
			if t, _ := p["text"].(string); t != "" {
				return []CLIRunEvent{{Kind: "error", Text: cliClip(t, cliRunTextMax)}}
			}
		case "mcp_tool_call":
			// Codex reports an MCP call once, finished, with what came back.
			item, _ := p["raw"].(map[string]any)
			if item == nil {
				return nil
			}
			name, detail, id := toolCallParts(item)
			calls[id] = name
			text := resultText(item["result"])
			if text == "" {
				if e, ok := item["error"].(map[string]any); ok {
					text = pickStr(e, "message")
				}
			}
			failed := pickStr(item, "status") == "failed" || item["error"] != nil
			if r, ok := item["result"].(map[string]any); ok {
				if isErr, _ := r["is_error"].(bool); isErr {
					failed = true
				}
			}
			return []CLIRunEvent{{Kind: "tool", Tool: name, Detail: detail, CallID: id},
				{Kind: "result", Tool: name, CallID: id, Text: cliClip(text, cliRunTextMax), Failed: failed}}
		case "stream_event":
			// --include-partial-messages: a message's text as it is written.
			raw, _ := p["raw"].(map[string]any)
			ev, _ := raw["event"].(map[string]any)
			delta, _ := ev["delta"].(map[string]any)
			if pickStr(ev, "type") == "content_block_delta" && pickStr(delta, "type") == "text_delta" {
				if t, _ := delta["text"].(string); t != "" {
					return []CLIRunEvent{{Kind: "delta", Text: t}}
				}
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
		// Named as Claude and pi name an MCP tool, so the trace shows it bare.
		detail = ""
		if args, ok := p["arguments"].(map[string]any); ok && len(args) > 0 {
			raw, _ := json.Marshal(args)
			detail = cliClip(string(raw), 600)
		}
		return "mcp__" + pickStr(p, "server") + "__" + pickStr(p, "tool"), detail, id
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
	// pi: the end of one call, with its output.
	if _, ok := p["is_error"]; ok && p["message"] == nil {
		failed, _ := p["is_error"].(bool)
		id := pickStr(p, "id")
		tool := calls[id]
		if tool == "" {
			tool = pickStr(p, "name")
		}
		return []CLIRunEvent{{Kind: "result", Tool: tool, CallID: id, Text: cliClip(resultText(p["output"]), cliRunTextMax), Failed: failed}}
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
	case map[string]any:
		// An MCP result, or pi's: {"content":[{"type":"text","text":…}]}.
		return resultText(t["content"])
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
