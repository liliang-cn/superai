package app

// Long tasks, started from the conversation.
//
// A job too big for one conversation — hours of work, many rounds — runs as a
// long task: the supervisor drives the agent segment by segment, each segment
// starting fresh from the plan, notes and workspace, so the context never
// fills up. There is no form for it; the person says what they want and the
// model decides it is that kind of job.

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
)

func (a *App) registerLongTaskTools(svc *backend.Service, cfg *backend.Settings) {
	if svc == nil {
		return
	}
	inner := svc.Agent()
	num := func(args map[string]any, k string) int {
		if v, ok := args[k].(float64); ok && v > 0 {
			return int(v)
		}
		return 0
	}
	// Approvals follow this install: where tool calls already run without
	// asking, so does the task; elsewhere its calls wait on the approval cards.
	unattended := cfg != nil && cfg.DisableToolApproval

	inner.AddToolWithMetadata("long_task_start",
		"Start a long task: a job too big for one conversation — building something with many steps, a migration, "+
			"a research project that takes hours. It runs in the background, segment by segment, keeping a plan and "+
			"its workspace between segments, and survives this conversation ending. Use it only when the person asks "+
			"for work of that size or agrees to it; for anything that fits in this conversation, just do it. "+
			"Returns the task's id; tell the person it has started and that you can report on it.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"goal":         map[string]any{"type": "string", "description": "The whole job, self-contained: what done looks like, where to work, constraints."},
				"max_minutes":  map[string]any{"type": "integer", "minimum": 1, "description": "Time limit. Default 240."},
				"max_segments": map[string]any{"type": "integer", "minimum": 1, "description": "At most this many segments. Default 8."},
				"max_tokens":   map[string]any{"type": "integer", "minimum": 1, "description": "Optional token budget for the whole task. Only when the person gives one; otherwise the settings decide, and by default there is none."},
			},
			"required": []string{"goal"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			goal := strings.TrimSpace(fmt.Sprint(args["goal"]))
			if goal == "" || goal == "<nil>" {
				return map[string]any{"ok": false, "error": "goal is required"}, nil
			}
			minutes, segments := num(args, "max_minutes"), num(args, "max_segments")
			if minutes == 0 {
				minutes = 240
			}
			if segments == 0 {
				segments = 8
			}
			tokens := num(args, "max_tokens")
			id := a.LongRunStart(goal, segments, 40, minutes, tokens, "", unattended)
			out := map[string]any{"ok": true, "id": id, "max_minutes": minutes, "max_segments": segments}
			if tokens > 0 {
				out["max_tokens"] = tokens
			}
			return out, nil
		},
		agent.ToolMetadata{Destructive: true})

	inner.AddToolWithMetadata("long_task_status",
		"How the long tasks are going. With an id: that task's progress, plan and result. Without: every task, newest first.",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string", "description": "A task id from long_task_start."}},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			id, _ := args["id"].(string)
			if strings.TrimSpace(id) == "" {
				return map[string]any{"ok": true, "tasks": a.LongRunList()}, nil
			}
			st := a.LongRunState(id)
			if st == nil {
				return map[string]any{"ok": false, "error": "no long task " + id}, nil
			}
			return map[string]any{"ok": true, "task": map[string]any{
				"id": st.TaskID, "goal": st.Goal, "running": st.Running, "done": st.Done, "stop": st.Stop,
				"segments": len(st.Segments), "max_segments": st.MaxSegments, "rounds": len(st.Rounds),
				"tool_calls": st.ToolCalls, "tool_errors": st.ToolErrors, "plan": st.Plan, "result": st.Final,
				"started_at": st.StartedAt, "ended_at": st.EndedAt,
			}}, nil
		},
		agent.ToolMetadata{})

	inner.AddToolWithMetadata("long_task_stop",
		"Stop a long task that is running. Its plan and workspace stay, so it can be picked up again.",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string"}},
			"required":   []string{"id"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			id, _ := args["id"].(string)
			return map[string]any{"ok": a.LongRunStop(id)}, nil
		},
		agent.ToolMetadata{Destructive: true})
}
