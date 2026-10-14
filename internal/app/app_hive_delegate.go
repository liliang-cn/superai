package app

// The queen's side of delegation (backend/hive_delegate.go): which tools her
// run keeps, the hive_delegate tool, who gets each piece, the fallback when
// nobody can take it, and how what a worker does shows up in her run.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
)

// chatRequestKey carries the request id of the turn a tool runs in, so a tool
// can add events to that turn's stream. SendChat puts it there; a turn
// started any other way (a schedule, a dashboard refresh) has none and its
// tools simply have nobody to tell.
type chatRequestKey struct{}

func withChatRequest(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, chatRequestKey{}, id)
}

func chatRequestFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(chatRequestKey{}).(string)
	return id
}

// delegationMode is the mode in force here: off on anything but a queen.
func (a *App) delegationMode() string {
	a.mu.Lock()
	s, queen := a.settings, a.hive != nil
	a.mu.Unlock()
	if s == nil || !queen {
		return backend.DelegationOff
	}
	return s.Hive.DelegationMode()
}

// liveWorkers is the hive's own workers that are live. Agents reached some
// other way (a laptop's node) are not the hive's to hand its work to.
func (a *App) liveWorkers() []backend.HiveMember {
	var out []backend.HiveMember
	for _, m := range a.HiveMembers() {
		if m.Role == backend.HiveRoleWorker && m.State == "live" {
			out = append(out, m)
		}
	}
	return out
}

// delegateCandidates are the live workers that have every server a piece
// needs, the least busy first. Busy is the orders of this queen's still
// running on them; a worker that said what it has comes before one too old
// to say, at the same load.
func (a *App) delegateCandidates(needs []string) []string {
	running := map[string]int{}
	for _, t := range a.tasks().Recent() {
		if t.Dir == backend.TaskOut && t.State == backend.TaskRunning {
			running[t.Worker]++
		}
	}
	type cand struct {
		name  string
		load  int
		known bool
	}
	var cs []cand
	for _, m := range a.liveWorkers() {
		if backend.HasAll(m.MCP, needs) {
			cs = append(cs, cand{m.Name, running[m.Name], m.MCP != nil})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].load != cs[j].load {
			return cs[i].load < cs[j].load
		}
		if cs[i].known != cs[j].known {
			return cs[i].known
		}
		return cs[i].name < cs[j].name
	})
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.name
	}
	return out
}

// queenRunFilter is what a chat run on this instance must not have, by
// delegation mode. Installed on every service; it says nothing off a queen.
func (a *App) queenRunFilter(ctx context.Context, _ string, registry []string) []string {
	mode := a.delegationMode()
	if mode == backend.DelegationOff {
		return nil
	}
	if backend.IsQueenSelf(ctx) {
		// The queen doing a piece herself does it, rather than handing it
		// round again.
		return []string{backend.DelegateToolName, "hive_command", "hive_map"}
	}
	if len(a.liveWorkers()) == 0 {
		// Nobody to give it to: she keeps her tools, and the tool that would
		// only fall back to her is not offered.
		return []string{backend.DelegateToolName}
	}
	if mode == backend.DelegationAuto {
		return nil
	}
	return backend.WorkToolDenylist(registry)
}

// noteNoWorkers says, on a turn about to run in max mode with no live worker,
// that the queen is doing it herself — in the log and on the turn's stream.
func (a *App) noteNoWorkers(requestID string) {
	if a.delegationMode() != backend.DelegationMax || len(a.liveWorkers()) > 0 {
		return
	}
	log.Printf("hive: no live worker; the queen keeps her tools for request %s", requestID)
	a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update",
		"content": "no worker is live; the queen does this herself"})
}

// registerDelegateTool gives a queen hive_delegate. Called from
// registerHiveTools, under a.mu.
func (a *App) registerDelegateTool(inner *agent.Service, mode string) {
	if mode == backend.DelegationOff {
		return
	}
	inner.AddToolWithMetadata(backend.DelegateToolName,
		"Have workers do the work that needs tools, and get their reports back: noted (calendar, to-dos, notes, goals,"+
			" time, holdings), web search and pages, files and shell, CortexDB research, reports, checks."+
			"\n\nEach task stands alone — the worker sees nothing of this conversation — so put in it everything needed:"+
			" what to do, absolute dates and times, ids, what to report. Several independent tasks in one call run at the"+
			" same time on different workers, idle ones first; split a request that has independent parts. Set needs to"+
			" the MCP servers a task's tools come from (\"noted\") so it goes to a worker that has them."+
			"\n\nA worker that fails or times out is replaced by another; when no worker can do a task the queen does it"+
			" herself. The report says who did each task and which tools they called; tell the person in a few words.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tasks": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"task": map[string]any{"type": "string", "description": "One self-contained instruction."},
							"needs": map[string]any{
								"type": "array", "items": map[string]any{"type": "string"},
								"description": "MCP servers the task's tools come from, e.g. [\"noted\"].",
							},
							"worker": map[string]any{"type": "string", "description": "Only to pin the task to one worker; usually omit."},
						},
						"required": []string{"task"},
					},
					"description": "One entry per independent piece of work.",
				},
			},
			"required": []string{"tasks"},
		},
		a.hiveDelegate,
		// The whole report, every piece: a cut would drop the middle pieces
		// and have the queen ask for paid work again.
		agent.ToolMetadata{OutputLimit: -1})
}

// delegateTasks reads the tool's arguments, leniently: a bare string for a
// task, or a single "task" instead of a list, both mean what they say.
func delegateTasks(args map[string]any) []backend.DelegateTask {
	var out []backend.DelegateTask
	add := func(v any) {
		switch x := v.(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				out = append(out, backend.DelegateTask{Task: s})
			}
		case map[string]any:
			t := backend.DelegateTask{Task: strings.TrimSpace(str(x["task"])), Worker: strings.TrimSpace(str(x["worker"]))}
			if ns, ok := x["needs"].([]any); ok {
				for _, n := range ns {
					if s := strings.TrimSpace(str(n)); s != "" {
						t.Needs = append(t.Needs, s)
					}
				}
			}
			if t.Task != "" {
				out = append(out, t)
			}
		}
	}
	if ts, ok := args["tasks"].([]any); ok {
		for _, t := range ts {
			add(t)
		}
	}
	if t, ok := args["task"]; ok {
		add(t)
	}
	return out
}

// hiveDelegate is the hive_delegate tool.
func (a *App) hiveDelegate(ctx context.Context, args map[string]any) (any, error) {
	tasks := delegateTasks(args)
	if len(tasks) == 0 {
		return nil, fmt.Errorf("give tasks: one self-contained instruction per piece of work")
	}
	if len(tasks) > backend.MaxDelegateTasks {
		return nil, fmt.Errorf("%d tasks is more than one call takes (%d); for many uniform jobs use hive_map", len(tasks), backend.MaxDelegateTasks)
	}
	a.mu.Lock()
	svc, s := a.svc, a.settings
	a.mu.Unlock()
	timeout := backend.DefaultDelegateTimeout
	if s != nil {
		timeout = s.Hive.DelegateTimeout()
	}
	// A task that names a server the queen has needs it, said or not.
	servers := svc.MCPServersWithTools()
	for i := range tasks {
		if len(tasks[i].Needs) == 0 {
			tasks[i].Needs = backend.InferNeeds(tasks[i].Task, servers)
		}
	}

	requestID := chatRequestFrom(ctx)
	ctx = backend.WithWorkerTrace(ctx, func(ev backend.WorkerToolEvent) { a.emitWorkerTool(requestID, ev) })
	queen := a.hiveName()
	env := backend.DelegateEnv{
		Candidates: a.delegateCandidates,
		Run: func(ctx context.Context, worker, prompt string) (backend.RemoteResult, error) {
			return a.remoteRunner().Run(ctx, worker, prompt)
		},
		Self:     a.queenSelf,
		SelfName: queen,
		Timeout:  timeout,
		OnAssign: func(i int, worker string, fallback bool) {
			line := fmt.Sprintf("%s takes piece %d", worker, i+1)
			if fallback {
				line = fmt.Sprintf("no worker could take piece %d; the queen does it herself", i+1)
			}
			if requestID != "" {
				a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update", "content": line,
					"worker": worker, "delegated": true})
			}
		},
	}
	results := backend.RunDelegated(ctx, env, tasks)
	for _, r := range results {
		if r.Fallback {
			log.Printf("hive: piece %d done by the queen herself: %s", r.Index+1, r.Why)
		}
	}
	return backend.FormatDelegated(results), nil
}

// emitWorkerTool puts one tool event of delegated work on the queen's turn,
// attributed: the tool reads "orange2 · mcp_noted_list_events", and worker /
// workerTool carry the two halves for a client that draws them apart. The
// result is only how it came out; the worker's own stream has the rest.
func (a *App) emitWorkerTool(requestID string, ev backend.WorkerToolEvent) {
	if ev.Kind == "tool_result" && ev.OK && touchesAlarms(ev.Tool, nil) {
		// A worker changed an alarm in noted; the watcher here is the one
		// that pushes it to the phones.
		a.alarmsMayHaveChanged()
	}
	if requestID == "" {
		return
	}
	p := map[string]any{
		"requestId": requestID, "type": ev.Kind, "tool": ev.Label(),
		"worker": ev.Worker, "workerTool": ev.Tool, "delegated": true,
	}
	switch ev.Kind {
	case "tool_call":
		if b, err := json.Marshal(ev.Args); err == nil && len(b) <= 4096 {
			p["args"] = ev.Args
		}
	case "tool_result":
		r := map[string]any{"ok": ev.OK}
		if ev.Error != "" {
			r["error"] = ev.Error
		}
		p["result"] = r
	}
	a.emit("chat:event", p)
}

// queenSelf does a piece on the queen, when no worker could: a run of its own,
// with her work tools and without hive_delegate (queenRunFilter), on a
// context of its own so the parent run's deny list does not come along. It
// stops when the parent does.
func (a *App) queenSelf(ctx context.Context, prompt string) (backend.RemoteResult, error) {
	started := time.Now()
	res := backend.RemoteResult{Agent: a.hiveName(), Host: "the queen"}
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		res.Failed, res.Reason = true, "the backend is not ready"
		return res, nil
	}
	own, cancel := context.WithCancel(backend.WithQueenSelf(context.Background()))
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	own = backend.WithChatSession(own, backend.ChatSessionFrom(ctx))
	trace := backend.WorkerTraceFrom(ctx)
	session := backend.HiveSessionPrefix + uuid.NewString()
	turn, err := svc.StreamTurn(own, session, prompt, nil, func(ev *agent.Event) {
		if trace == nil {
			return
		}
		switch ev.Type {
		case agent.EventTypeToolCall:
			trace(backend.WorkerToolEvent{Kind: "tool_call", Tool: ev.ToolName, Args: ev.ToolArgs, OK: true})
		case agent.EventTypeToolResult:
			trace(backend.WorkerToolEvent{Kind: "tool_result", Tool: ev.ToolName, OK: !backend.ToolResultFailed(ev.ToolResult)})
		}
	})
	res.MS = time.Since(started).Milliseconds()
	res.Usage = turn.Usage
	text, _ := backend.SplitEmotion(turn.Text)
	res.Text = strings.TrimSpace(text)
	switch {
	case err != nil:
		res.Failed, res.Reason = true, err.Error()
	case ctx.Err() != nil:
		res.Failed, res.Reason = true, "stopped"
	case res.Text == "":
		res.Failed, res.Reason = true, "the queen's own run ended without saying anything"
	}
	return res, nil
}

// relayWorkerApproval asks the person about a tool call a worker is holding
// for approval while it works on one of this queen's orders, and returns the
// answer. The card says which worker asks, and it lands in the conversation
// the order came from. A queen whose gate is off has said she does not want
// to be asked, and that holds for her workers' calls too.
func (a *App) relayWorkerApproval(ctx context.Context, worker string, p map[string]any) bool {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return false
	}
	gate := svc.ToolGate()
	if gate == nil || !gate.Enabled() {
		return true
	}
	wait := gate.Wait()
	// Answer before the worker gives up on its own clock.
	if exp, err := time.Parse(time.RFC3339, str(p["expiresAt"])); err == nil {
		if d := time.Until(exp) - 2*time.Second; d > 0 && d < wait {
			wait = d
		}
	}
	actx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	args, _ := p["args"].(map[string]any)
	now := time.Now()
	dec, _ := a.askToolApproval(actx, backend.ApprovalRequest{
		ID: uuid.NewString(), Tool: str(p["tool"]), Command: str(p["command"]), Args: args,
		SessionID: backend.ChatSessionFrom(ctx), By: worker, AskedAt: now, ExpiresAt: now.Add(wait),
	})
	return dec.Allowed
}
