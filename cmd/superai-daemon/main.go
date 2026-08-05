// Command superai-daemon keeps SuperAI's scheduled prompts running while the
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
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai-desktop/backend"
)

func main() {
	var (
		once     = flag.Bool("once", false, "run every enabled schedule immediately and exit, for checking the setup")
		list     = flag.Bool("list", false, "print the schedules and exit")
		logPath  = flag.String("log", "", "append logs to this file instead of stderr")
		notifyOn = flag.Bool("notify", true, "post a desktop notification when a run finishes")
	)
	flag.Parse()

	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("open log %s: %v", *logPath, err)
		}
		defer f.Close()
		log.SetOutput(f)
	}
	log.SetPrefix("superai-daemon ")

	settings, err := backend.LoadSettings()
	if err != nil {
		log.Printf("settings: %v (continuing with defaults)", err)
	}

	// Nothing here needs a browser, and launching Chrome costs about a second of
	// every start.
	if os.Getenv("SUPERAI_NO_BROWSER") == "" {
		_ = os.Setenv("SUPERAI_NO_BROWSER", "1")
	}

	svc, err := backend.NewService(settings)
	if err != nil {
		log.Fatalf("build agent: %v", err)
	}
	defer svc.Close()

	sch, err := svc.Agent().NewPromptScheduler(
		agent.WithPromptSessionID("scheduled"),
		agent.WithPromptObserver(func(run agent.PromptRun) {
			logRun(run)
			if *notifyOn {
				notify(run)
			}
		}),
	)
	if err != nil {
		log.Fatalf("build scheduler: %v", err)
	}

	// The one-shot modes only manage or run on demand, so they work even while
	// another process owns the timers.
	if *list || *once {
		if err := sch.StartManageOnly(); err != nil {
			log.Fatalf("open scheduler: %v", err)
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
	log.Print("stopping")
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
			log.Printf("schedule lock: %v (retrying in %s)", err, retry)
		} else if lock != nil {
			runWithLock(ctx, svc, sch, lock)
			return
		} else if !waiting {
			// Logged once, not every retry: this is the normal state while the
			// user has the app open.
			log.Printf("pid %d owns the timers; waiting for it to release them", backend.ScheduleLockHolder())
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
		log.Printf("start scheduler: %v", err)
		return
	}
	defer func() { _ = sch.Stop() }()

	// Tools that create schedules (set_reminder) go through the service, so a
	// reminder set by an agent running here lands in the same store.
	svc.UsePromptScheduler(sch)
	defer svc.UsePromptScheduler(nil)

	schedules, _ := sch.List()
	log.Printf("owning timers for %d schedule(s)", len(schedules))
	for _, s := range schedules {
		log.Printf("  %s  %s  next=%s  %s", s.ID[:8], s.Schedule, formatTime(s.NextRun), s.Prompt)
	}

	<-ctx.Done()
}

func printSchedules(sch *agent.PromptScheduler) {
	schedules, err := sch.List()
	if err != nil {
		log.Fatalf("list: %v", err)
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
		log.Fatalf("list: %v", err)
	}
	ran := 0
	for _, s := range schedules {
		if !s.Enabled {
			continue
		}
		log.Printf("running %s: %s", s.ID[:8], s.Prompt)
		if _, err := sch.RunNow(s.ID); err != nil {
			log.Printf("  failed: %v", err)
		}
		ran++
	}
	if ran == 0 {
		log.Print("nothing enabled to run")
	}
}

func logRun(run agent.PromptRun) {
	if run.Err != nil {
		log.Printf("run failed after %s: %v — %s", run.Duration.Round(time.Millisecond), run.Err, run.Prompt)
		return
	}
	log.Printf("run ok in %s: %s", run.Duration.Round(time.Millisecond), firstLine(run.Answer))
}

// notify posts a desktop notification through AppleScript.
//
// The app uses the Wails notification API, which needs a Wails frontend; this
// process has none. osascript is available on every macOS install and needs no
// permission grant of its own, which suits a background job.
func notify(run agent.PromptRun) {
	title := "SuperAI"
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
		log.Printf("notification failed: %v", err)
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
