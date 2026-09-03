package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai-desktop/backend"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Scheduled prompts, and telling the user when one has run.
//
// agent-go owns the cron loop and runs the turn (agent.PromptScheduler); this
// file is the desktop half: expose it to the UI, and report a finished run two
// ways.
//
// A native notification, because the point of "every morning at eight" is that
// you are not watching the window — and an in-app event, because when you are,
// a system banner is the wrong place for something the app can show properly.
// The run's answer also lands in the session the schedule names, so opening that
// conversation shows the whole turn, tools and produced files included.

// notifyTitle keeps the banner short; the body carries the answer.
const scheduleNotifyTitle = "SuperAI"

// startScheduler builds and starts the prompt scheduler for the current service.
// A failure is not fatal: the app is still usable, it just will not fire
// timers, which GetStatus reports.
func (a *App) startScheduler() {
	a.mu.Lock()
	svc := a.svc
	if a.scheduler != nil {
		a.mu.Unlock()
		a.stopScheduler()
		a.mu.Lock()
	}
	a.mu.Unlock()

	if svc == nil {
		return
	}

	sch, err := svc.Agent().NewPromptScheduler(
		agent.WithPromptObserver(a.onScheduledRun),
		agent.WithPromptSessionID("scheduled"),
	)
	if err != nil {
		a.mu.Lock()
		a.schedulerErr = err.Error()
		a.mu.Unlock()
		return
	}

	// Only one process may fire schedules, or a task runs once per process — two
	// messages, two files written. The lock decides; whoever loses still manages
	// schedules, so creating a reminder works either way.
	lock, lockErr := backend.AcquireScheduleLock()
	if lockErr != nil {
		a.mu.Lock()
		a.schedulerErr = lockErr.Error()
		a.mu.Unlock()
		return
	}

	if lock != nil {
		err = sch.Start()
	} else {
		err = sch.StartManageOnly()
	}
	if err != nil {
		lock.Release()
		a.mu.Lock()
		a.schedulerErr = err.Error()
		a.mu.Unlock()
		return
	}

	// Tools that create schedules go through the service, so hand it the one
	// this process owns.
	svc.UsePromptScheduler(sch)

	a.mu.Lock()
	a.scheduler = sch
	a.scheduleLock = lock
	a.schedulerErr = ""
	a.mu.Unlock()
}

// stopScheduler halts the cron loop. In-flight runs finish on their own.
func (a *App) stopScheduler() {
	a.mu.Lock()
	sch := a.scheduler
	lock := a.scheduleLock
	svc := a.svc
	a.scheduler = nil
	a.scheduleLock = nil
	a.mu.Unlock()

	if svc != nil {
		svc.UsePromptScheduler(nil)
	}
	if sch != nil {
		_ = sch.Stop()
	}
	// Released after the loop has stopped, so a daemon taking over cannot start
	// firing while this process is still mid-run.
	lock.Release()
}

// onScheduledRun reports a finished run. It runs on the scheduler's goroutine,
// so it does no work beyond publishing.
func (a *App) onScheduledRun(run agent.PromptRun) {
	// A dashboard bringing itself up to date is not news — see
	// onDashboardScheduledRun. It stores the answer and claims the run, so
	// nothing below it fires.
	if a.onDashboardScheduledRun(run) {
		return
	}

	// One notice, drawn by every surface that is attached: the toast in an open
	// window, the native banner on this desktop, and the webhook for whoever is
	// nowhere near either. What the message says is decided once, in Go, rather
	// than three times in three places that drift.
	a.mu.Lock()
	notifySvc := a.svc
	a.mu.Unlock()
	if notifySvc != nil {
		notifySvc.NotifyScheduledRun(context.Background(), run)
	}

	a.emit("schedule:run", map[string]any{
		"prompt":     run.Prompt,
		"session":    run.SessionID,
		"answer":     run.Answer,
		"error":      errText(run.Err),
		"cancelled":  run.Cancelled,
		"startedAt":  run.StartedAt.Format("2006-01-02 15:04:05"),
		"durationMs": run.Duration.Milliseconds(),
	})

	// A reminder for one moment is done once it has fired. Deleted off the
	// scheduler's goroutine so the list is not modified while it is being
	// walked, and after the emit so the report of the run never waits on it.
	// A cancelled run has not fired, so it keeps its schedule.
	if !run.Cancelled {
		a.mu.Lock()
		svc := a.svc
		a.mu.Unlock()
		if svc != nil {
			prompt := run.Prompt
			go func() {
				if n := svc.DropFinishedOneShots(prompt); n > 0 {
					log.Printf("superai: removed %d one-shot reminder(s) after firing", n)
				}
			}()
		}
	}

}

// bannerSink draws a notice as a native system notification.
//
// Subscribed rather than called, because this is the surface that only exists
// in the desktop build: serve mode has no window to hang a banner off, and the
// publisher should not have to know which build it is in.
//
// It draws only what was marked Push, and for the same reason the webhook does.
// A banner is an interruption, and the message that earns one is the message
// worth reaching someone who is not looking — which is exactly what that flag
// means. "Settings saved" would otherwise put a system notification on screen
// for something the user is already watching happen.
func (a *App) bannerSink(_ context.Context, n backend.Notice) {
	if !n.Push {
		return
	}
	a.notify(scheduleNotifyTitle, firstLine(n.Source), n.Message, n.Session)
}

// notify sends a native notification, degrading to nothing when the platform or
// the user has not granted permission — a failed banner must never take a
// scheduled run down with it.
func (a *App) notify(title, subtitle, body, session string) {
	a.mu.Lock()
	ctx := a.ctx
	allowed := a.notifyOK
	a.mu.Unlock()

	if ctx == nil || !allowed {
		return
	}
	opts := runtime.NotificationOptions{
		ID:       "superai-" + uuid.NewString(),
		Title:    title,
		Subtitle: subtitle,
		Body:     truncateRunes(body, 300),
	}
	if session != "" {
		// Carried on the notification itself, not in a map this process keeps.
		// A banner outlives the run that raised it — it sits in Notification
		// Centre until it is dismissed, which may be tomorrow — and a lookup
		// table would have to be either unbounded or wrong by then.
		opts.Data = map[string]any{"session": session}
	}
	if err := runtime.SendNotification(ctx, opts); err != nil {
		// Logged, not surfaced: the run itself succeeded.
		fmt.Printf("superai: notification failed: %v\n", err)
	}
}

// watchNotificationClicks opens the conversation a banner came from.
//
// Without this a banner is a dead end: it tells you a run finished and leaves
// you to find it, which for a reminder that fired hours ago means scrolling a
// list of conversations to work out which one it meant.
func (a *App) watchNotificationClicks() {
	if a.ctx == nil {
		return
	}
	runtime.OnNotificationResponse(a.ctx, func(result runtime.NotificationResult) {
		if result.Error != nil {
			return
		}
		session, _ := result.Response.UserInfo["session"].(string)
		if session == "" {
			return
		}
		// The window is where the conversation is, and a click on a banner is a
		// request to look at something.
		runtime.WindowShow(a.ctx)
		a.emit("open:conversation", map[string]any{"session": session})
	})
}

// initNotifications asks for permission once at startup. macOS requires
// authorization before a banner will show, and asking at the moment a timer
// fires would put the prompt in front of a user who is not at the machine.
func (a *App) initNotifications() {
	if a.ctx == nil {
		return
	}
	if err := runtime.InitializeNotifications(a.ctx); err != nil {
		return
	}
	if !runtime.IsNotificationAvailable(a.ctx) {
		return
	}
	granted, err := runtime.RequestNotificationAuthorization(a.ctx)
	if err != nil || !granted {
		return
	}
	a.mu.Lock()
	a.notifyOK = true
	a.mu.Unlock()
	a.watchNotificationClicks()
}

// ---- bindings ----

// ScheduledPrompts lists the scheduled prompts.
func (a *App) ScheduledPrompts() []agent.ScheduledPrompt {
	a.mu.Lock()
	sch := a.scheduler
	a.mu.Unlock()
	if sch == nil {
		return []agent.ScheduledPrompt{}
	}
	list, err := sch.List()
	if err != nil {
		return []agent.ScheduledPrompt{}
	}
	return list
}

// SchedulePrompt registers a prompt on a cron expression ("0 8 * * *") or a
// shorthand ("@daily"). session names the conversation runs append to; empty
// puts them in a shared "scheduled" conversation.
func (a *App) SchedulePrompt(prompt, cronExpr, note, session string) string {
	a.mu.Lock()
	sch := a.scheduler
	a.mu.Unlock()
	if sch == nil {
		return "scheduler not running"
	}
	if _, err := sch.Schedule(prompt, cronExpr, note, session); err != nil {
		return err.Error()
	}
	return "ok"
}

// SetScheduledPromptEnabled pauses or resumes a schedule.
func (a *App) SetScheduledPromptEnabled(id string, enabled bool) string {
	a.mu.Lock()
	sch := a.scheduler
	a.mu.Unlock()
	if sch == nil {
		return "scheduler not running"
	}
	if err := sch.SetEnabled(id, enabled); err != nil {
		return err.Error()
	}
	return "ok"
}

// DeleteScheduledPrompt removes a schedule.
func (a *App) DeleteScheduledPrompt(id string) string {
	a.mu.Lock()
	sch := a.scheduler
	a.mu.Unlock()
	if sch == nil {
		return "scheduler not running"
	}
	if err := sch.Delete(id); err != nil {
		return err.Error()
	}
	return "ok"
}

// RunScheduledPromptNow starts a schedule immediately, which is how you find
// out whether it does what you meant without waiting for the hour.
//
// It returns as soon as the run has started, not when it has finished. A
// scheduled prompt is allowed fifteen minutes, and this used to block the Wails
// call for all of them: the window froze, so the run could not be watched and —
// worse — could not be stopped, because the button that would stop it was on
// the other side of the frozen bridge.
//
// The outcome arrives the way a timer-fired run's does: the "schedule:run"
// event, and the run's own conversation. ScheduledPrompts reports Running in
// the meantime.
func (a *App) RunScheduledPromptNow(id string) string {
	a.mu.Lock()
	sch := a.scheduler
	a.mu.Unlock()
	if sch == nil {
		return "scheduler not running"
	}
	if _, err := sch.RunNowAsync(id); err != nil {
		return err.Error()
	}
	return "ok"
}

// CancelScheduledRun stops the run of a schedule that is currently in flight.
//
// The schedule itself is untouched — its timer stands and it can be run again.
// That is the difference between this and SetScheduledPromptEnabled(id, false).
//
// A stop that lands after the run has finished is not an error, so it says so
// in words rather than failing: the caller needs to tell "stopped it" from
// "there was nothing left to stop".
func (a *App) CancelScheduledRun(id string) string {
	a.mu.Lock()
	sch := a.scheduler
	a.mu.Unlock()
	if sch == nil {
		return "scheduler not running"
	}
	stopped, err := sch.CancelRun(id)
	if err != nil {
		return err.Error()
	}
	if stopped == 0 {
		return "that run is not in flight — it has already finished"
	}
	return "ok"
}

// ---- helpers ----

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncateRunes(s, 60)
}

func truncateRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}
