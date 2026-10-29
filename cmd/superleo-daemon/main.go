// Command superleo-daemon keeps SuperLeo's scheduled prompts running while the
// desktop app is closed.
//
// "Every morning at eight, work out my stock returns and message me" is the
// point of the feature, and at eight in the morning the window is not open. The
// app's own cron loop stops with it, so something has to stay up. This is that
// something: the same agent, the same schedules out of the same database, no UI.
//
// It holds an advisory lock while it owns the timers (see
// backend.AcquireScheduleLock), so a task never runs twice when the app is open
// too. If the app has the lock, this waits for it rather than exiting — exiting
// would leave nothing to fire the eight-o'clock schedule after the app closes.
//
// Install it with `make install-daemon`, which writes a launchd job that starts
// it at login and restarts it if it dies.
//
// It attaches no approver to the tool gate, and that is the point rather than
// an omission: there is no window here and nobody awake at eight in the
// morning, so a shell command a scheduled prompt asks for is denied with a
// reason the model can report, instead of running unwatched. A schedule that
// genuinely needs to run commands unattended is a decision to make once, in
// Settings, by turning the gate off — not one made silently every night. The
// app's own cron loop, which does have a window, prompts as usual.
package main

import (
	"context"
	"flag"
	"fmt"
	_ "github.com/liliang-cn/superleo/internal/brand" // the old SuperLeo names keep working
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superleo/internal/backend"
)

func main() {
	var (
		once     = flag.Bool("once", false, "run every enabled schedule immediately and exit, for checking the setup")
		list     = flag.Bool("list", false, "print the schedules and exit")
		logPath  = flag.String("log", "", "append logs to this file instead of stderr")
		notifyOn = flag.Bool("notify", true, "post a desktop notification when a run finishes")
	)
	flag.Parse()

	backend.SetupLogging("daemon")
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			slog.Error("open log", "path", *logPath, "error", err)
			os.Exit(1)
		}
		defer f.Close()
		backend.SetupLogging("daemon", f)
	}

	settings, err := backend.LoadSettings()
	if err != nil {
		slog.Warn("settings unreadable, continuing with defaults", "error", err)
	}

	// Nothing here needs a browser, and launching Chrome costs about a second of
	// every start.
	if os.Getenv("SUPERLEO_NO_BROWSER") == "" {
		_ = os.Setenv("SUPERLEO_NO_BROWSER", "1")
	}

	svc, err := backend.NewService(settings)
	if err != nil {
		slog.Error("build agent", "error", err)
		os.Exit(1)
	}
	defer svc.Close()

	sch, err := svc.Agent().NewPromptScheduler(
		agent.WithPromptSessionID("scheduled"),
		agent.WithPromptObserver(func(run agent.PromptRun) {
			logRun(run)
			// The webhook first: it is the only one of the three that reaches a
			// person who is not at this machine, and it is the whole reason the
			// daemon exists on a headless box.
			svc.NotifyScheduledRun(context.Background(), run)
			if *notifyOn {
				notify(run)
			}
		}),
	)
	if err != nil {
		slog.Error("build scheduler", "error", err)
		os.Exit(1)
	}

	// The one-shot modes only manage or run on demand, so they work even while
	// another process owns the timers.
	if *list || *once {
		if err := sch.StartManageOnly(); err != nil {
			slog.Error("open scheduler", "error", err)
			os.Exit(1)
		}
		defer func() { _ = sch.Stop() }()
		if *list {
			printSchedules(sch)
		} else {
			runAllNow(sch)
		}
		return
	}
	_ = sch.Stop() // not started yet; keeps the deferred close paths uniform

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ownTimers(ctx, svc, sch)
	slog.Info("stopping")
}

// ownTimers runs the cron loop for as long as this process holds the lock,
// waiting for it when the app has it.
//
// Exiting on a busy lock would be simpler and wrong: launchd would not bring the
// job back (it exited cleanly), so closing the app would leave nothing to fire
// the eight-o'clock schedule. Waiting means the daemon takes over the moment the
// app releases it.
func ownTimers(ctx context.Context, svc *backend.Service, sch *agent.PromptScheduler) {
	const retry = 20 * time.Second
	waiting := false

	for {
		lock, err := backend.AcquireScheduleLock()
		if err != nil {
			slog.WarnContext(ctx, "schedule lock", "error", err, "retry_in", retry)
		} else if lock != nil {
			runWithLock(ctx, svc, sch, lock)
			return
		} else if !waiting {
			// Logged once, not every retry: this is the normal state while the
			// user has the app open.
			slog.InfoContext(ctx, "another process owns the timers; waiting for it to release them", "pid", backend.ScheduleLockHolder())
			waiting = true
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}

// runWithLock starts the cron loop and holds it until the process is asked to
// stop.
func runWithLock(ctx context.Context, svc *backend.Service, sch *agent.PromptScheduler, lock *backend.ScheduleLock) {
	defer lock.Release()

	if err := sch.Start(); err != nil {
		slog.ErrorContext(ctx, "start scheduler", "error", err)
		return
	}
	defer func() { _ = sch.Stop() }()

	// Tools that create schedules (set_reminder) go through the service, so a
	// reminder set by an agent running here lands in the same store.
	svc.UsePromptScheduler(sch)
	defer svc.UsePromptScheduler(nil)

	schedules, _ := sch.List()
	slog.InfoContext(ctx, "owning timers", "schedules", len(schedules))
	for _, s := range schedules {
		slog.InfoContext(ctx, "schedule", "id", s.ID[:8], "cron", s.Schedule, "next", formatTime(s.NextRun), "prompt", s.Prompt)
	}

	<-ctx.Done()
}

func printSchedules(sch *agent.PromptScheduler) {
	schedules, err := sch.List()
	if err != nil {
		slog.Error("list schedules", "error", err)
		os.Exit(1)
	}
	if len(schedules) == 0 {
		fmt.Println("no schedules")
		return
	}
	for _, s := range schedules {
		state := "enabled"
		if !s.Enabled {
			state = "paused"
		}
		fmt.Printf("%s  %-14s  %-8s  next=%-20s last=%-20s  %s\n",
			s.ID[:8], s.Schedule, state, formatTime(s.NextRun), formatTime(s.LastRun), s.Prompt)
	}
}

// runAllNow fires every enabled schedule once. Waiting until tomorrow morning to
// discover a prompt does not work is the worst way to test this feature.
func runAllNow(sch *agent.PromptScheduler) {
	schedules, err := sch.List()
	if err != nil {
		slog.Error("list schedules", "error", err)
		os.Exit(1)
	}
	ran := 0
	for _, s := range schedules {
		if !s.Enabled {
			continue
		}
		slog.Info("running schedule", "id", s.ID[:8], "prompt", s.Prompt)
		if _, err := sch.RunNow(s.ID); err != nil {
			slog.Warn("schedule run failed", "id", s.ID[:8], "error", err)
		}
		ran++
	}
	if ran == 0 {
		slog.Info("nothing enabled to run")
	}
}

func logRun(run agent.PromptRun) {
	if run.Err != nil {
		slog.Warn("run failed", "took", run.Duration.Round(time.Millisecond), "error", run.Err, "prompt", run.Prompt)
		return
	}
	slog.Info("run ok", "took", run.Duration.Round(time.Millisecond), "answer", firstLine(run.Answer))
}

// notify posts a desktop notification through AppleScript.
//
// The app uses the Wails notification API, which needs a Wails frontend; this
// process has none. osascript is available on every macOS install and needs no
// permission grant of its own, which suits a background job.
func notify(run agent.PromptRun) {
	title := "SuperLeo"
	body := firstLine(run.Answer)
	if run.Err != nil {
		body = "运行失败：" + run.Err.Error()
	}
	if body == "" {
		body = "定时任务已完成"
	}
	script := fmt.Sprintf(
		`display notification %s with title %s subtitle %s`,
		quoteAppleScript(truncate(body, 240)),
		quoteAppleScript(title),
		quoteAppleScript(truncate(firstLine(run.Prompt), 60)),
	)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		slog.Warn("notification failed", "error", err)
	}
}

// quoteAppleScript renders a Go string as an AppleScript literal. Unescaped
// quotes or backslashes would end the literal early and turn the rest of an
// agent's answer into script — which is a text the model wrote, so it must not
// be trusted as code.
func quoteAppleScript(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "…"
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}
