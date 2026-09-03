package main

import (
	"context"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai-desktop/backend"
)

// Saved dashboards, and keeping them current.
//
// The store is in backend/dashboards.go. What lives here is the half that needs
// the running app: re-asking the question through the agent, and arming the
// cron that does it unattended.
//
// A refresh is deliberately the same thing as asking again in chat — one call
// to Service.Stream, the same tools, the same approval gate. There is no
// special "dashboard mode" the model has to be told about, so nothing about a
// saved dashboard can drift away from what the conversation would have done.

// dashboardRefreshTimeout caps one manual refresh. Longer than a chat turn
// because a dashboard usually means several lookups, shorter than the
// scheduler's fifteen minutes because somebody is watching a spinner.
const dashboardRefreshTimeout = 8 * time.Minute

// scheduleOK is what the scheduling methods in app_schedule.go answer with when
// nothing went wrong. They return a reason string, not an error, and the
// success value is a word rather than the empty string.
const scheduleOK = "ok"

// Dashboards lists what has been saved.
func (a *App) Dashboards() []backend.Dashboard {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return []backend.Dashboard{}
	}
	return svc.Dashboards()
}

// SaveDashboard keeps a reply and the ask behind it, and returns the new row.
func (a *App) SaveDashboard(name, source, prompt string) map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return failWith("backend not ready")
	}
	d, err := svc.SaveDashboard(name, source, prompt)
	if err != nil {
		return failWith(err.Error())
	}
	return okData(d)
}

// RenameDashboard changes a dashboard's label.
func (a *App) RenameDashboard(id, name string) map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return failWith("backend not ready")
	}
	if err := svc.RenameDashboard(id, name); err != nil {
		return failWith(err.Error())
	}
	return okData(nil)
}

// DeleteDashboard forgets a dashboard, and disarms its schedule with it.
//
// Order matters: the schedule goes first. A dashboard removed while its cron
// still fires would keep waking the machine to refresh something that no longer
// exists, and the run would land in a session nobody reads.
func (a *App) DeleteDashboard(id string) map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return failWith("backend not ready")
	}
	a.dropDashboardSchedule(id)
	if err := svc.DeleteDashboard(id); err != nil {
		return failWith(err.Error())
	}
	return okData(nil)
}

// RefreshDashboard re-asks the saved question and replaces the contents.
//
// Returns as soon as the run has started. The answer arrives as a
// "dashboard:updated" event, because a refresh takes as long as the question
// does and blocking a bound method for eight minutes would freeze the window.
func (a *App) RefreshDashboard(id string) map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return failWith("backend not ready")
	}
	d, found := svc.Dashboard(id)
	if !found {
		return failWith("no such dashboard")
	}
	if strings.TrimSpace(d.Prompt) == "" {
		return failWith("this dashboard has no saved question, so there is nothing to re-ask")
	}
	if d.Refreshing {
		return failWith("already refreshing")
	}

	svc.MarkDashboardRefreshing(id, true)
	a.emit("dashboard:refreshing", map[string]any{"id": id})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dashboardRefreshTimeout)
		defer cancel()
		// Its own session, the same one the scheduled refresh uses, so a
		// dashboard's history is its own and a manual refresh sees what the
		// last automatic one did.
		answer, err := svc.Stream(ctx, backend.DashboardSession(id), d.Prompt, nil, func(*agent.Event) {})
		a.finishDashboardRefresh(id, answer, err)
	}()
	return okData(nil)
}

// finishDashboardRefresh stores an outcome and tells the UI, whichever kind of
// run produced it.
func (a *App) finishDashboardRefresh(id, answer string, runErr error) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return
	}
	svc.MarkDashboardRefreshing(id, false)
	// The mood tag is for an avatar, not for a saved panel: left in, it would
	// be stored as the last line of the dashboard and drawn as text under the
	// numbers every time it was opened.
	reply, _ := backend.SplitEmotion(answer)
	updated, err := svc.ApplyDashboardRefresh(id, reply, runErr)
	if err != nil {
		a.emit("dashboard:updated", map[string]any{"id": id, "error": err.Error()})
		return
	}
	a.emit("dashboard:updated", map[string]any{
		"id":        updated.ID,
		"source":    updated.Source,
		"error":     updated.LastError,
		"refreshed": updated.RefreshedAt.Format(time.RFC3339),
	})
}

// SetDashboardCron arms, re-arms or disarms a dashboard's own refresh.
//
// An empty cron removes the schedule. Anything else replaces it: the scheduler
// has no update, so the old entry is dropped and a new one created, which also
// means an invalid expression cannot leave two behind.
func (a *App) SetDashboardCron(id, cronExpr string) map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return failWith("backend not ready")
	}
	d, found := svc.Dashboard(id)
	if !found {
		return failWith("no such dashboard")
	}
	cronExpr = strings.TrimSpace(cronExpr)
	if cronExpr != "" && strings.TrimSpace(d.Prompt) == "" {
		return failWith("this dashboard has no saved question, so a schedule would have nothing to run")
	}

	a.dropDashboardSchedule(id)
	if cronExpr != "" {
		// SchedulePrompt answers with a string that is "ok" on success and the
		// reason otherwise — not the empty string. Reading it as "empty means
		// fine" arms the cron and then reports a failure, which leaves a
		// schedule firing for a dashboard that does not know it has one.
		if msg := a.SchedulePrompt(d.Prompt, cronExpr, "Refresh dashboard: "+d.Name, backend.DashboardSession(id)); msg != scheduleOK {
			return failWith(msg)
		}
	}
	if err := svc.SetDashboardCron(id, cronExpr); err != nil {
		return failWith(err.Error())
	}
	return okData(nil)
}

// dropDashboardSchedule removes every scheduled prompt that refreshes this
// dashboard.
//
// Identified by session, not by prompt text: the session is what a finished run
// carries back, so it is the only handle that cannot be confused by two
// dashboards asking the same question.
func (a *App) dropDashboardSchedule(id string) {
	want := backend.DashboardSession(id)
	for _, p := range a.ScheduledPrompts() {
		if p.Session == want {
			a.DeleteScheduledPrompt(p.ID)
		}
	}
}

// onDashboardScheduledRun routes a finished scheduled run back to the dashboard
// that asked for it, and reports whether it was one.
//
// A dashboard refresh is not news: it has no reader waiting, and the notice
// machinery would raise a toast, a native banner and a webhook every morning
// for a panel quietly bringing itself up to date. So this claims the run and
// the ordinary reporting path is skipped.
func (a *App) onDashboardScheduledRun(run agent.PromptRun) bool {
	id, isDashboard := backend.DashboardIDFromSession(run.SessionID)
	if !isDashboard {
		return false
	}
	if run.Cancelled {
		a.mu.Lock()
		svc := a.svc
		a.mu.Unlock()
		if svc != nil {
			svc.MarkDashboardRefreshing(id, false)
		}
		return true
	}
	a.finishDashboardRefresh(id, run.Answer, run.Err)
	return true
}

// okData and failWith are the {ok, data} / {ok, error} envelope every bound
// method in this file answers with — the same shape AddSchedule and friends
// use, which the frontend already unwraps in one place.
//
// Not named ok/fail: `ok` is the second return of half the type assertions in
// this package, and a package-level function that every one of them shadows is
// a trap laid for the next person to add a line.
func okData(v any) map[string]any { return map[string]any{"ok": true, "data": v} }

func failWith(msg string) map[string]any { return map[string]any{"ok": false, "error": msg} }
