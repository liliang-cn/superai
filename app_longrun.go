package main

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai-desktop/backend"
)

// The Runs page: a window onto a task that takes hours.
//
// A long run has a backend (StreamLong) and, until this, no surface. These
// are the four calls the page makes, and the wall they read from.

// runWall returns the app's one RunWall, creating it on first use. One per
// app rather than per service, so a settings save does not wipe the wall.
func (a *App) runWall() *backend.RunWall {
	a.wallOnce.Do(func() {
		a.wall = backend.NewRunWall(
			func(ctx context.Context, taskID string) []agent.PlanItem {
				a.mu.Lock()
				svc := a.svc
				a.mu.Unlock()
				if svc == nil {
					return nil
				}
				return svc.Plan(ctx, taskID)
			},
			func(taskID, kind string) {
				a.emit("longrun:tick", map[string]any{"taskId": taskID, "kind": kind})
			},
		)
	})
	return a.wall
}

// LongRunStart begins a segmented task and returns its id. Everything the
// run does reaches the page as longrun:tick events; LongRunState reads the
// wall back. maxSegments / roundsPerSegment / maxMinutes / maxCostUSD at zero
// take agent-go's defaults. taskID non-empty resumes that task. unattended
// lets its tool calls through the approval gate without asking (audited),
// which a task that runs for hours needs and a page nobody has open cannot
// give.
func (a *App) LongRunStart(goal string, maxSegments, roundsPerSegment, maxMinutes int, maxCostUSD float64, taskID string, unattended bool) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	a.mu.Lock()
	svc := a.svc
	settings := a.settings
	buildErr := a.buildErr
	a.mu.Unlock()

	if taskID == "" {
		taskID = uuid.NewString()
	}
	wall := a.runWall()
	model := ""
	if settings != nil {
		model = settings.LLMModel
	}
	wall.Begin(taskID, goal, model, maxSegments)

	if svc == nil {
		wall.Finish(taskID, false, "unavailable", "", errBackendNotReady(buildErr))
		return taskID
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.longMu.Lock()
	if a.longRuns == nil {
		a.longRuns = map[string]context.CancelFunc{}
	}
	a.longRuns[taskID] = cancel
	a.longMu.Unlock()

	go func() {
		defer func() {
			cancel()
			a.longMu.Lock()
			delete(a.longRuns, taskID)
			a.longMu.Unlock()
		}()
		opts := backend.LongRunOptions{
			MaxSegments:      maxSegments,
			RoundsPerSegment: roundsPerSegment,
			MaxCostUSD:       maxCostUSD,
			TaskID:           taskID,
			Unattended:       unattended,
		}
		if maxMinutes > 0 {
			opts.MaxDuration = time.Duration(maxMinutes) * time.Minute
		}
		report, err := svc.StreamLong(ctx, goal, opts, nil)
		if err != nil {
			stop := "error"
			if ctx.Err() != nil {
				stop = "cancelled"
			}
			wall.Finish(taskID, false, stop, "", err)
			return
		}
		wall.Finish(taskID, report.Done, report.Stop, report.Text, nil)
	}()
	return taskID
}

// LongRunStop cancels a task in flight. A task not in flight is not an error.
func (a *App) LongRunStop(taskID string) bool {
	a.longMu.Lock()
	cancel, ok := a.longRuns[taskID]
	a.longMu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// LongRunState returns everything the wall knows about a task, or nil.
func (a *App) LongRunState(taskID string) *backend.TaskState {
	return a.runWall().Snapshot(context.Background(), taskID)
}

// LongRunList returns every task the wall has seen, newest first.
func (a *App) LongRunList() []backend.TaskSummary {
	return a.runWall().List()
}

type backendNotReady string

func (e backendNotReady) Error() string { return "backend not ready: " + string(e) }

func errBackendNotReady(buildErr string) error { return backendNotReady(buildErr) }
