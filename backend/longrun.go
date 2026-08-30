package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Long-horizon runs.
//
// Stream runs one turn: the model works until it answers or its round budget
// is spent, and a task that needs more than that comes back half done. A goal
// measured in hours is a different shape — many runs against one task, each
// starting from the plan and the workspace rather than from a conversation
// that would otherwise grow until it could not be sent.
//
// agent-go drives that with RunSegments. What made it unusable here is that
// RunSegments returns only when the whole task is over, so a window has
// nothing to draw for however long that takes. RunSegmentsStream forwards the
// segments' events as they happen, which is the same shape Stream already
// consumes — so a long run reaches the UI through the callback that is
// already there.

// LongRunOptions are the knobs a caller is likely to want. Zero values take
// agent-go's defaults.
type LongRunOptions struct {
	// MaxSegments caps how many runs the task may spend.
	MaxSegments int
	// RoundsPerSegment is each run's own round budget. Measured across a
	// thirteen-milestone build, anything from thirty to sixty behaves the
	// same; the model matters far more than this number.
	RoundsPerSegment int
	// MaxDuration stops starting new segments once the task has run this
	// long. A segment already started is allowed to finish, so the task ends
	// at a hand-off point with its plan and workspace consistent.
	MaxDuration time.Duration
	// MaxCostUSD caps what the whole task may spend. Each segment is handed
	// the remainder, so the ceiling holds inside a segment too.
	MaxCostUSD float64
	// TaskID names the task. Pass the id of an interrupted task to pick it
	// back up: the plan and its checkpoints live under it, so a resumed run
	// starts from what was already finished instead of from nothing.
	TaskID string
}

// LongRunReport is what the task did.
type LongRunReport struct {
	TaskID string `json:"task_id"`
	// Done is true only when the task actually finished — not merely when the
	// supervisor stopped asking.
	Done     bool   `json:"done"`
	Stop     string `json:"stop"`
	Segments int    `json:"segments"`
	Text     string `json:"text"`
	CostUSD  float64 `json:"cost_usd"`
	Duration string  `json:"duration"`
}

// StreamLong drives one goal across many runs, forwarding every event to emit
// exactly as Stream does, and returns what the task ended up doing.
//
// Cancelling ctx stops it at the next segment boundary; a segment already in
// flight sees the cancellation directly.
func (s *Service) StreamLong(ctx context.Context, goal string, o LongRunOptions, emit func(ev *agent.Event)) (*LongRunReport, error) {
	if s == nil || s.svc == nil {
		return nil, fmt.Errorf("superai: agent service unavailable")
	}

	cfg := agent.LongRunConfig{
		MaxSegments:      o.MaxSegments,
		RoundsPerSegment: o.RoundsPerSegment,
		MaxDuration:      o.MaxDuration,
		MaxTotalCostUSD:  o.MaxCostUSD,
	}
	var opts []agent.RunOption
	if o.TaskID != "" {
		opts = append(opts, agent.WithTaskID(o.TaskID))
	}

	// Whatever the task writes into the workspace belongs to it, the same way
	// a single turn's output does.
	root := ""
	if s.sb != nil {
		root = s.sb.Workspace()
	}
	before := snapshotWorkspace(root)

	began := time.Now()
	stream := s.svc.RunSegmentsStream(ctx, goal, cfg, opts...)
	for ev := range stream.Events {
		if emit != nil {
			emit(ev)
		}
	}
	out := <-stream.Result

	if s.files != nil {
		changed := changedFiles(before, snapshotWorkspace(root))
		kept := changed[:0]
		for _, p := range changed {
			if !s.files.isImported(p) {
				kept = append(kept, p)
			}
		}
		s.files.record(o.TaskID, kept)
	}

	if out.Err != nil {
		return nil, out.Err
	}
	if out.Result == nil {
		return nil, fmt.Errorf("superai: long run produced no result")
	}
	return &LongRunReport{
		TaskID:   out.Result.TaskID,
		Done:     out.Result.Done(),
		Stop:     string(out.Result.Stop),
		Segments: len(out.Result.Segments),
		Text:     out.Result.Text,
		CostUSD:  out.Result.TotalCostUSD,
		Duration: time.Since(began).Round(time.Second).String(),
	}, nil
}
