package app

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/superai/internal/backend"
)

// A second ask while the queen is still answering the first.
//
// The queen can take it herself — turns in one conversation run side by side —
// but on a hive the workers with nothing to do are the better place: the queen
// stays on the first ask, and the second does not compete with it for her
// model. When every worker is busy the queen takes it after all.

// idleWorker names a live worker with no order of this queen's running, or ""
// when there is none (or this is not a queen).
func (a *App) idleWorker() string {
	a.mu.Lock()
	queen := a.hive != nil
	a.mu.Unlock()
	if !queen {
		return ""
	}
	busy := map[string]bool{}
	for _, t := range a.tasks().Recent() {
		if t.Dir == backend.TaskOut && t.State == backend.TaskRunning {
			busy[t.Worker] = true
		}
	}
	var free []string
	for name := range a.remoteRunner().Workers() {
		if !busy[name] {
			free = append(free, name)
		}
	}
	if len(free) == 0 {
		return ""
	}
	sort.Strings(free)
	return free[0]
}

// recentContext is the last exchange of a conversation, short, for a worker
// that is taking up a question in the middle of it.
func (a *App) recentContext(sessionID string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return ""
	}
	var lines []string
	for _, t := range svc.SessionTurns(sessionID) {
		if t.Kind != "" {
			continue
		}
		who := "Person"
		if t.Role == "assistant" {
			who = "Assistant"
		}
		text := strings.TrimSpace(t.Content)
		if r := []rune(text); len(r) > 600 {
			text = string(r[:600]) + "…"
		}
		lines = append(lines, who+": "+text)
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "\n")
}

// routeToIdleWorker hands an ask to a worker while the queen is busy in the
// same conversation, and files the exchange there as if the queen had
// answered — attributed, so the transcript says who did.
func (a *App) routeToIdleWorker(requestID, sessionID, worker, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), a.routedTimeout(worker))
	a.trackRun(requestID, cancel)
	askedAt := time.Now()

	go func() {
		defer cancel()
		defer a.untrackRun(requestID)
		defer func() { recordTurnTime(sessionID, message, askedAt, time.Since(askedAt)) }()

		a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update",
			"content": "asking " + worker + "…"})

		prompt := message
		if c := a.recentContext(sessionID); c != "" {
			prompt = "You are taking a question in the middle of a conversation the queen is in. " +
				"The latest of it:\n\n" + c + "\n\nThe new question, to answer on your own:\n" + message
		}
		res := a.askAgent(ctx, worker, prompt)
		if a.runCancelled(requestID) || ctx.Err() != nil {
			a.emit("chat:cancelled", map[string]any{"requestId": requestID, "final": res.Text})
			return
		}
		if res.Failed {
			a.emit("chat:error", map[string]any{"requestId": requestID,
				"error": worker + " did not answer: " + res.Reason})
			return
		}
		reply := fmt.Sprintf("**%s** · %.1fs\n\n%s", worker, float64(res.MS)/1000, res.Text)
		a.recordWorkerExchange(sessionID, message, reply)
		a.emit("chat:done", map[string]any{"requestId": requestID, "final": reply})
	}()
}

// recordWorkerExchange files the question as asked and the worker's answer.
// Best effort, like recordRoutedExchange: the answer is already on screen.
func (a *App) recordWorkerExchange(sessionID, question, reply string) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil || svc.Agent() == nil {
		return
	}
	if err := svc.Agent().AppendSessionMessages(sessionID,
		domain.Message{Role: "user", Content: question},
		domain.Message{Role: "assistant", Content: reply},
	); err != nil {
		log.Printf("superai: could not record a worker's answer in %s: %v", sessionID, err)
	}
}
