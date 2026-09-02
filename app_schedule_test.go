package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
)

// Scheduled prompts, from the app's side.
//
// The value of this feature is entirely in "it fires by itself and tells me", so
// the test drives the real App bindings and checks the report reaches the host:
// the schedule is stored with a resolved next run, running it drives an agent
// turn, and the finished run is emitted with the answer a notification would
// carry.

func scheduleTestApp(t *testing.T) (*App, func() []captured) {
	return scheduleTestAppWith(t, nil)
}

// scheduleTestAppWith is scheduleTestApp with a hook that runs before the fake
// provider answers a completion. A hook that waits on r.Context() turns the
// provider into a turn that never comes back on its own — which is the only
// thing worth cancelling.
func scheduleTestAppWith(t *testing.T, beforeReply func(*http.Request)) (*App, func() []captured) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)
	t.Setenv("SUPERAI_NO_BROWSER", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "/models") {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
			return
		}
		if beforeReply != nil {
			beforeReply(r)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]string{"role": "assistant", "content": "今日收益 +1.2%。\n情绪: 开心"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	app := NewApp()
	app.settings = &backend.Settings{
		LLMBaseURL:   srv.URL + "/v1",
		LLMKey:       "k",
		LLMModel:     "test-model",
		WorkspaceDir: home + "/workspace",
		MaxRounds:    3,
		Headless:     true,
		DisablePTC:   true,
	}
	app.rebuild()
	if app.svc == nil {
		t.Fatalf("backend did not build: %s", app.buildErr)
	}

	var mu sync.Mutex
	var events []captured
	app.emitFn = func(name string, payload map[string]any) {
		mu.Lock()
		events = append(events, captured{name: name, payload: payload})
		mu.Unlock()
	}

	app.startScheduler()
	if app.scheduler == nil {
		t.Fatalf("scheduler did not start: %s", app.schedulerErr)
	}
	t.Cleanup(func() {
		app.stopScheduler()
		_ = app.svc.Close()
	})

	return app, func() []captured {
		mu.Lock()
		defer mu.Unlock()
		return append([]captured{}, events...)
	}
}

// awaitScheduleRun waits for the "schedule:run" report of a finished run.
func awaitScheduleRun(t *testing.T, seen func() []captured) *captured {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range seen() {
			if e.name == "schedule:run" {
				e := e
				return &e
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("a finished run must be reported, or the user learns nothing happened")
	return nil
}

func TestSchedulePromptFiresAndReports(t *testing.T) {
	app, seen := scheduleTestApp(t)

	const goal = "统计我的股票收益并发消息给我"
	if msg := app.SchedulePrompt(goal, "0 8 * * *", "每天早上八点", "stocks"); msg != "ok" {
		t.Fatalf("SchedulePrompt: %s", msg)
	}

	list := app.ScheduledPrompts()
	if len(list) != 1 {
		t.Fatalf("listed %d schedules, want 1", len(list))
	}
	got := list[0]
	if got.Prompt != goal || got.Schedule != "0 8 * * *" || !got.Enabled {
		t.Fatalf("stored schedule is wrong: %+v", got)
	}
	if got.NextRun == nil {
		t.Fatal("a schedule with no next run time will never fire")
	}
	if got.NextRun.Before(time.Now()) {
		t.Errorf("next run %v is in the past", got.NextRun)
	}

	// Firing it now is how a user checks the schedule does what they meant.
	// The binding starts the run and returns; the report arrives when the turn
	// is over, which is exactly why the call no longer blocks on it.
	if msg := app.RunScheduledPromptNow(got.ID); msg != "ok" {
		t.Fatalf("RunScheduledPromptNow: %s", msg)
	}

	run := awaitScheduleRun(t, seen)
	if got := run.str("error"); got != "" {
		t.Errorf("run reported an error: %s", got)
	}
	if got := run.str("prompt"); got != goal {
		t.Errorf("reported prompt = %q, want the scheduled goal", got)
	}
	if got := run.str("session"); got != "stocks" {
		t.Errorf("reported session = %q, want the one the schedule named", got)
	}
	// The answer is what a notification body and the in-app card both show.
	if answer := run.str("answer"); !strings.Contains(answer, "收益") {
		t.Errorf("reported answer = %q, want the agent's reply", answer)
	}

	// The turn is in that conversation, so opening it shows what happened.
	if turns := app.ChatHistory("stocks"); len(turns) == 0 {
		t.Error("the run should be readable in its conversation afterwards")
	}
}

// Stopping a run in flight. The point of the whole thing: "Run now" starts a
// turn that may take a quarter of an hour, and the user who realises it was a
// mistake must be able to end it — and must not then be told they broke
// something.
func TestCancelScheduledRunStopsTheTurnAndIsNotAnError(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	app, seen := scheduleTestAppWith(t, func(r *http.Request) {
		once.Do(func() { close(started) })
		<-r.Context().Done()
	})

	if msg := app.SchedulePrompt("分析一大堆东西", "0 8 * * *", "长任务", "slow"); msg != "ok" {
		t.Fatalf("SchedulePrompt: %s", msg)
	}
	id := app.ScheduledPrompts()[0].ID

	if msg := app.RunScheduledPromptNow(id); msg != "ok" {
		t.Fatalf("RunScheduledPromptNow: %s", msg)
	}
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("the run never reached the provider")
	}

	// The listing is what the UI draws the Stop button from.
	if !app.ScheduledPrompts()[0].Running {
		t.Fatal("a run in flight must be reported as running, or the UI has no button to show")
	}

	if msg := app.CancelScheduledRun(id); msg != "ok" {
		t.Fatalf("CancelScheduledRun: %s", msg)
	}

	run := awaitScheduleRun(t, seen)
	if got := run.str("error"); got != "" {
		t.Errorf("a stopped run reported an error: %q — cancel is an outcome, not a failure", got)
	}
	if cancelled, _ := run.payload["cancelled"].(bool); !cancelled {
		t.Error("the run report does not say it was cancelled")
	}

	// The run is over and the schedule survives it, runnable again.
	deadline := time.Now().Add(10 * time.Second)
	for app.ScheduledPrompts()[0].Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	after := app.ScheduledPrompts()
	if len(after) != 1 {
		t.Fatalf("the schedule should still exist, got %d", len(after))
	}
	if after[0].Running {
		t.Error("the run is still reported as running after it was stopped")
	}
	if !after[0].Enabled {
		t.Error("cancelling a run must not disable the schedule")
	}

	// A stop that lands after the run is over is late, not wrong.
	if msg := app.CancelScheduledRun(id); msg == "ok" {
		t.Error("cancelling nothing should say so rather than claim success")
	} else if strings.Contains(strings.ToLower(msg), "error") {
		t.Errorf("a late stop reads as an error: %q", msg)
	}
}

func TestSchedulePromptLifecycleBindings(t *testing.T) {
	app, _ := scheduleTestApp(t)

	if msg := app.SchedulePrompt("morning report", "@daily", "", ""); msg != "ok" {
		t.Fatalf("schedule: %s", msg)
	}
	id := app.ScheduledPrompts()[0].ID

	if msg := app.SetScheduledPromptEnabled(id, false); msg != "ok" {
		t.Fatalf("disable: %s", msg)
	}
	if app.ScheduledPrompts()[0].Enabled {
		t.Error("a paused schedule should report Enabled=false")
	}
	if msg := app.SetScheduledPromptEnabled(id, true); msg != "ok" {
		t.Fatalf("enable: %s", msg)
	}

	if msg := app.DeleteScheduledPrompt(id); msg != "ok" {
		t.Fatalf("delete: %s", msg)
	}
	if len(app.ScheduledPrompts()) != 0 {
		t.Error("the schedule should be gone")
	}

	// Nonsense is refused at creation rather than failing silently at 8am.
	for _, bad := range [][2]string{
		{"", "@daily"},
		{"something", ""},
		{"something", "not a cron expression"},
	} {
		if msg := app.SchedulePrompt(bad[0], bad[1], "", ""); msg == "ok" {
			t.Errorf("SchedulePrompt(%q, %q) should have been refused", bad[0], bad[1])
		}
	}
}

// TestScheduleBindingsWithoutSchedulerDoNotPanic covers the window before
// startup and after shutdown, when the UI may still call in.
func TestScheduleBindingsWithoutSchedulerDoNotPanic(t *testing.T) {
	app := NewApp()

	if got := app.ScheduledPrompts(); len(got) != 0 {
		t.Errorf("want an empty list, got %v", got)
	}
	for _, msg := range []string{
		app.SchedulePrompt("x", "@daily", "", ""),
		app.SetScheduledPromptEnabled("id", true),
		app.DeleteScheduledPrompt("id"),
		app.RunScheduledPromptNow("id"),
		app.CancelScheduledRun("id"),
	} {
		if msg == "ok" {
			t.Error("a binding should report that the scheduler is not running, not claim success")
		}
	}

	// Notifying with no Wails context must be a no-op rather than a crash.
	app.notify("t", "s", "b", "session")
	// And so must registering the click handler: initNotifications guards on
	// the same context, but the guard is the thing under test.
	app.watchNotificationClicks()
}
