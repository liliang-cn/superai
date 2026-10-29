package app

import (
	"context"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// TraceLines returns the tail of a task's or a conversation's JSONL trace,
// oldest line first. id is a long task's id or a chat's session id.
//
// Each line is one event as agent-go's TraceWriter wrote it — model turns with
// their token split, tool calls with their durations, lints, retries,
// compactions, checkpoints and segment boundaries. The frontend parses them;
// handing back raw lines keeps this from having to grow a field every time the
// framework adds one.
//
// It reads the directory rather than going through a.svc, so a trace is still
// readable after a settings save has rebuilt the service — and after the run
// that wrote it is long over.
//
// A model call's request comes back as its size only; TraceCall has it whole.
func (a *App) TraceLines(taskID string, limit int) []string {
	return backend.TraceLines(backend.TracesDir(), taskID, limit)
}

// TraceCall returns one model call in full, what it was sent included: the
// model_end line with that span id in the trace id names, as raw JSON.
func (a *App) TraceCall(id, spanID string) string {
	return backend.TraceCall(backend.TracesDir(), id, spanID)
}

// ToolStats is every tool's calls over the last days days, across every trace
// on disk: how often, how often it failed, and how long it took. 0 means a
// week.
//
// Like TraceLines it reads the directory, not a.svc — the week it covers
// spans restarts and settings saves, and none of the processes that wrote it
// need to be the one asking.
func (a *App) ToolStats(days int) backend.ToolStatsReport {
	if days <= 0 {
		days = 7
	}
	return backend.ToolStats(backend.TracesDir(), time.Now().AddDate(0, 0, -days))
}

// FlowTrace is one flow, by its trace id, as raw JSONL lines oldest first:
// every run, model call, tool call and CortexDB call this machine traced
// under it, and the lines of each peer the flow handed work to, asked for
// over the same wire the order went out on while they answer.
//
// It is what an answer's trace id opens: the entry point at the root, the
// queen's runs under it, a worker's turn under the call that ordered it.
func (a *App) FlowTrace(traceID string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return backend.FlowTrace(ctx, traceID, a.remoteRunner().PeerFlowLines)
}

// FlowLines is this machine's own lines of a flow, for the core or queen that
// started it to merge into its timeline.
func (a *App) FlowLines(traceID string) []string {
	return backend.FlowLines(traceID)
}
