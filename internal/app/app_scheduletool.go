package app

// The model's view of everything the hive will do on its own: the scheduled
// prompts and the bees' next wakes, in one list. Without it the queen could
// set a calendar entry but could not see, start or stop the recurring work the
// person had set up — the work it was most often asked about.

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/backend"
)

// UpcomingItem is one thing the hive will do by itself, whoever holds it.
type UpcomingItem struct {
	Kind     string     `json:"kind"` // "schedule" or "bee"
	ID       string     `json:"id"`
	What     string     `json:"what"`
	When     string     `json:"when,omitempty"` // the cron expression, or the bee's rhythm
	Next     *time.Time `json:"next,omitempty"`
	Enabled  bool       `json:"enabled"`
	Running  bool       `json:"running"`
	LastRun  *time.Time `json:"lastRun,omitempty"`
	BeeName  string     `json:"bee,omitempty"`
	Waiting  string     `json:"waitingFor,omitempty"`
	Schedule string     `json:"schedule,omitempty"`
}

// Upcoming lists the scheduled prompts and the bees together, soonest first.
// Bound for the canvas as well as the model, so both see the same list.
func (a *App) Upcoming() []UpcomingItem {
	out := []UpcomingItem{}
	for _, p := range a.ScheduledPrompts() {
		what := p.Note
		if what == "" {
			what = p.Prompt
		}
		out = append(out, UpcomingItem{Kind: "schedule", ID: p.ID, What: what, When: p.Schedule, Schedule: p.Schedule,
			Next: p.NextRun, Enabled: p.Enabled, Running: p.Running, LastRun: p.LastRun})
	}
	for _, b := range a.StandingAgents() {
		when := b.Cron
		out = append(out, UpcomingItem{Kind: "bee", ID: b.ID, What: b.Goal, When: when, Next: b.NextDue,
			Enabled: !b.Paused, Running: b.Running != nil, BeeName: b.Name, Waiting: b.WaitingFor})
	}
	sort.SliceStable(out, func(i, j int) bool {
		switch {
		case out[i].Next == nil:
			return false
		case out[j].Next == nil:
			return true
		}
		return out[i].Next.Before(*out[j].Next)
	})
	return out
}

// registerScheduleTools runs inside the build, which holds a.mu: nothing here
// may take it. The tools themselves run later, at call time.
func (a *App) registerScheduleTools(svc *backend.Service) {
	if svc == nil {
		return
	}
	inner := svc.Agent()
	if inner == nil {
		return
	}

	inner.AddToolWithMetadata("schedule_list",
		"Everything that will happen without anyone asking: scheduled prompts (kind schedule) and the bees — standing agents — with when each next wakes (kind bee). Soonest first.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(ctx context.Context, _ map[string]any) (any, error) {
			b, err := json.Marshal(a.Upcoming())
			return string(b), err
		},
		agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true})

	inner.AddToolWithMetadata("schedule_add",
		"Run a prompt on a schedule from now on. cron is a cron expression (\"0 8 * * 1-5\") or a shorthand (\"@daily\", \"@every 2h\"). The prompt must stand on its own: it runs later with none of this conversation.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "What to do each time, standing on its own."},
				"cron":   map[string]any{"type": "string", "description": "When: a cron expression or a shorthand."},
				"note":   map[string]any{"type": "string", "description": "A short name for it, shown in lists."},
			},
			"required": []string{"prompt", "cron"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			return a.SchedulePrompt(str(args["prompt"]), str(args["cron"]), str(args["note"]), ""), nil
		},
		agent.ToolMetadata{})

	inner.AddToolWithMetadata("schedule_set_enabled",
		"Pause (enabled false) or resume (enabled true) a scheduled prompt or a bee, by the id schedule_list gave.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":      map[string]any{"type": "string"},
				"enabled": map[string]any{"type": "boolean"},
			},
			"required": []string{"id", "enabled"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			id := str(args["id"])
			on, _ := args["enabled"].(bool)
			for _, b := range a.StandingAgents() {
				if b.ID == id {
					var err error
					if on {
						err = a.ResumeStandingAgent(id)
					} else {
						err = a.PauseStandingAgent(id)
					}
					if err != nil {
						return err.Error(), nil
					}
					return "ok", nil
				}
			}
			return a.SetScheduledPromptEnabled(id, on), nil
		},
		agent.ToolMetadata{})

	inner.AddToolWithMetadata("schedule_remove",
		"Delete a scheduled prompt for good, by the id schedule_list gave. Bees are not removed here; pause them instead.",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string"}},
			"required":   []string{"id"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			return a.DeleteScheduledPrompt(str(args["id"])), nil
		},
		agent.ToolMetadata{Destructive: true})
}
