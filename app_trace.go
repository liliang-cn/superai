package main

import (
	"github.com/liliang-cn/superai-desktop/backend"
)

// TraceLines returns the tail of a task's JSONL trace, oldest line first.
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
func (a *App) TraceLines(taskID string, limit int) []string {
	return backend.TraceLines(backend.TracesDir(), taskID, limit)
}
