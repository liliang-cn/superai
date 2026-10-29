package app

// The queen calling back what she sent out, and talking to the agents that
// are not hive workers.
//
// hive_task_cancel stops one of her orders by the id hive_command, hive_map
// and remote_agent_send print: a worker's order, a delegation, or work handed
// outside the hive — a coding run (codex.mac) or a named agent (pi-mac,
// openclaw) — which is stopped where it runs, not merely no longer waited on.
//
// remote_agent_send is a message to one of those named agents: the ones the
// settings reach, and the ones the linked machines say they run, which
// remote_agent_run (registered only when the settings name some) leaves out
// on a queen whose settings have none.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superleo/internal/backend"
)

type toolAdder interface {
	AddToolWithMetadata(name, description string, params map[string]any, fn func(context.Context, map[string]any) (any, error), meta agent.ToolMetadata)
}

// stoppableTask is ctx for the work behind board task id, ended by Stop on
// that task. The returned func releases it once the work is over.
func (a *App) stoppableTask(ctx context.Context, id string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	a.tasks().OnStop(id, cancel)
	return ctx, cancel
}

// finishOutside closes a board entry for work handed outside the hive, as
// cancelled when it was stopped.
func (a *App) finishOutside(ctx context.Context, id string, ok bool, answer, errMsg string) {
	if !ok && ctx.Err() != nil {
		a.tasks().Finish(id, backend.TaskCancelled, firstLineOf(answer, 400), "stopped")
		return
	}
	a.finishDelegated(id, ok, answer, errMsg)
}

func (a *App) registerQueenOrderTools(inner toolAdder) {
	inner.AddToolWithMetadata("hive_task_cancel",
		"Stop an order you sent out, by the task id hive_command, hive_map, remote_agent_send or hive_task show:"+
			" a worker's order, a delegation (every piece still out), or work handed outside the hive — a coding run"+
			" (codex.mac), which is stopped on its machine, or a named agent's run (pi-mac). Refused for an id that is"+
			" unknown, already finished, or not one this hive gave.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "The task or delegation id."},
			},
			"required": []string{"id"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			id := strings.TrimSpace(str(args["id"]))
			if id == "" {
				return "", fmt.Errorf("id is required: a task id as hive_command or hive_task print it")
			}
			id, err := a.resolveTaskID(id)
			if err != nil {
				return "", err
			}
			stop, what, err := a.stopTarget(id)
			if err != nil {
				return "", err
			}
			if err := stop(); err != nil {
				return "", err
			}
			return fmt.Sprintf("stopping %s %s; it settles as cancelled once whatever runs it has stopped (hive_task shows it)", what, id), nil
		},
		agent.ToolMetadata{Destructive: true})

	inner.AddToolWithMetadata("remote_agent_send",
		"Send a message to an agent that is not a hive worker and get its answer back: a named agent on a linked"+
			" machine (pi-mac, hermes-win, openclaw), one the settings reach, or a coding agent on a machine (codex.mac)."+
			" The agent has no memory of earlier messages from you and cannot ask you anything, so the message must stand"+
			" alone. remote_agent_list and coding_agent_list say who there is. The reply names the task id, which"+
			" hive_task_cancel stops.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent":   map[string]any{"type": "string", "description": "Who: pi-mac, openclaw, codex.mac …"},
				"message": map[string]any{"type": "string", "description": "The whole message, standing on its own."},
			},
			"required": []string{"agent", "message"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			name, msg := strings.TrimSpace(str(args["agent"])), strings.TrimSpace(str(args["message"]))
			if name == "" || msg == "" {
				return "", fmt.Errorf("agent and message are both required")
			}
			if _, onRoster := a.remoteRunner().Workers()[name]; onRoster {
				return "", fmt.Errorf("%s is a hive worker: send it orders with hive_command, or write with hive_send", name)
			}
			ctx = a.traceWorkers(ctx)
			text, failed, handled, id := a.delegateOutsideID(ctx, name, msg)
			switch {
			case !handled:
				return "", fmt.Errorf("no agent called %q here; remote_agent_list and coding_agent_list say who there is", name)
			case failed:
				return fmt.Sprintf("%s did not answer (task %s): %s", name, id, text), nil
			}
			return fmt.Sprintf("%s answered (task %s):\n\n%s", name, id, text), nil
		},
		agent.ToolMetadata{Destructive: true, OutputLimit: -1})
}

// The far side of a link ask that core may stop: AskRemoteAgentAs runs the
// ask under core's id, CancelRemoteAsk stops it.
var remoteAsks struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// AskRemoteAgentAs is AskRemoteAgent under an id core can stop it by.
func (a *App) AskRemoteAgentAs(id, name, prompt string) backend.RemoteResult {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remoteAsks.mu.Lock()
	if remoteAsks.cancels == nil {
		remoteAsks.cancels = map[string]context.CancelFunc{}
	}
	remoteAsks.cancels[id] = cancel
	remoteAsks.mu.Unlock()
	defer func() {
		remoteAsks.mu.Lock()
		delete(remoteAsks.cancels, id)
		remoteAsks.mu.Unlock()
	}()
	return a.askAgent(ctx, name, prompt)
}

// CancelRemoteAsk stops an ask AskRemoteAgentAs is running.
func (a *App) CancelRemoteAsk(id string) string {
	remoteAsks.mu.Lock()
	cancel := remoteAsks.cancels[id]
	remoteAsks.mu.Unlock()
	if cancel == nil {
		return "that ask is not running"
	}
	cancel()
	return "ok"
}

// cancelFarAsk tells the agent an ask it runs for us is no longer wanted.
func cancelFarAsk(ctx context.Context, c *agentConn, id string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = c.call(cctx, "CancelRemoteAsk", []any{id}, nil)
}
