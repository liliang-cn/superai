package app

import (
	"context"
	"strings"

	"github.com/liliang-cn/superleo/internal/backend"
)

// A coding run handed out as a hive order (coding_agent_run, hive_command,
// a delegated piece) has an entry on the task board, and that entry used to say
// "thinking, 0 tools" until the run was over: the CLI's own events fed the
// run, never the entry. cliTaskFeed is the run's watcher that puts them there.
//
// For a run on another machine the events reach it through the agent link's
// cli:event stream, which the core's mirror copies into the local run (see
// mirrorRemoteRun) and so through this watcher, matched by run id. The board
// does the throttling: Live publishes at most every few hundred ms, and a
// phase that has not changed publishes nothing.
func (a *App) cliTaskFeed(taskID string) func(CLIRunEvent) {
	if taskID == "" {
		return nil
	}
	board := a.tasks()
	streamed := false // the words of this message came as deltas already
	return func(e CLIRunEvent) {
		switch e.Kind {
		case "tool":
			board.Progress(taskID, backend.PhaseTool, e.Tool)
			line := "\n▸ " + e.Tool
			if d := feedClip(e.Detail, 120); d != "" {
				line += " " + d
			}
			board.Live(taskID, line+"\n")
		case "result":
			mark := "◂ "
			if e.Failed {
				mark = "◂ ✗ "
			}
			board.Live(taskID, mark+feedClip(e.Text, 100)+"\n")
			board.Progress(taskID, backend.PhaseThinking, "")
		case "text":
			board.Progress(taskID, backend.PhaseWriting, "")
			if !streamed {
				board.Live(taskID, e.Text)
			}
			streamed = false
		case "delta":
			streamed = true
			board.Progress(taskID, backend.PhaseWriting, "")
			board.Live(taskID, e.Text)
		}
	}
}

// feedClip is s on one line, cut to n characters.
func feedClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

type boardTaskKey struct{}

// withBoardTask marks ctx with the board entry the work under it is for.
func withBoardTask(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, boardTaskKey{}, id)
}

func boardTaskFrom(ctx context.Context) string {
	s, _ := ctx.Value(boardTaskKey{}).(string)
	return s
}
