package app

// Alarms (闹钟).
//
// The definitions live in noted; the iOS app rings them on the phone with
// AlarmKit, so it has to hear about a change soon, also while it sits in the
// background with no event stream open. This file is the part that notices
// and tells it:
//
//   - NotedAlarms and NotedHolidays are the app's reads, noted's own results
//     handed through like the home tiles' (app_actions.go).
//   - The watcher asks noted for its alarms version once a minute, and at once
//     after anything here ran a noted alarm tool: the queen in a turn, or a
//     button through CallTool. When the version moves it emits noted:alarms
//     {version} on the event stream and sends every paired phone that
//     registered a push token (RegisterPushToken, pairing.go) a silent push.
//
// The version seen last is kept on disk, so a change made while this process
// was down is still announced when it comes back. The very first version this
// instance ever sees is a starting point, not a change.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/superai/internal/apns"
	"github.com/liliang-cn/superai/internal/backend"
)

const (
	// alarmsEvent is emitted when noted's alarms version moves.
	alarmsEvent = "noted:alarms"
	// alarmPollEvery is how often the watcher asks. list_alarms is one
	// indexed read on noted's side.
	alarmPollEvery  = 60 * time.Second
	alarmsStateFile = "noted-alarms.version"
)

// NotedAlarms is every alarm with its schedule and next rings, and the
// version to compare on the next sync (list_alarms):
//
//	POST /api/rpc/NotedAlarms  []
//	→ {"items":[{"kind":"alarm","id":"…","link":"noted://alarm/…",…}],"version":"…"}
//
// 503 when noted is not configured, 502 when it fails, as NotedToday.
func (a *App) NotedAlarms() (any, error) {
	return a.notedRead("list_alarms", map[string]any{})
}

// NotedHolidays is the statutory holiday calendar alarms follow for a year;
// 0 is this year (get_holidays):
//
//	POST /api/rpc/NotedHolidays  [2026]
//	→ {"year":2026,"region":"CN","days":[{"date":"2026-10-01","kind":"holiday","name":"国庆节"}],"availableYears":[…],"version":"…"}
func (a *App) NotedHolidays(year int) (any, error) {
	args := map[string]any{}
	if year > 0 {
		args["year"] = year
	}
	return a.notedRead("get_holidays", args)
}

// alarmPusher wakes one device. *apns.Client is one.
type alarmPusher interface {
	Wake(ctx context.Context, deviceToken, env string, data any) error
}

// alarmWatcher notices noted's alarms changing. Its parts are functions so a
// test can stand in for noted, the phones and Apple.
type alarmWatcher struct {
	every time.Duration
	// version asks noted for the current version; false when there is nobody
	// to ask (noted not configured, or not answering).
	version func(ctx context.Context) (string, bool)
	emit    func(name string, payload map[string]any)
	// pusher is nil when pushes are not configured.
	pusher  func() alarmPusher
	devices func() (*deviceStore, error)
	// vendors picks the sender for an Android phone's provider
	// (vendorSenderFor when nil). See push_vendor.go.
	vendors func(provider string) vendorSender
	// statePath keeps the last version across restarts.
	statePath string

	kick chan struct{}

	mu     sync.Mutex
	last   string
	loaded bool
}

func newAlarmWatcher() *alarmWatcher {
	return &alarmWatcher{every: alarmPollEvery, kick: make(chan struct{}, 1)}
}

// poke asks for a check soon. It never blocks: a check already asked for
// covers this one too.
func (w *alarmWatcher) poke() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// run checks at start, every w.every, and whenever poked, until ctx ends.
func (w *alarmWatcher) run(ctx context.Context) {
	t := time.NewTicker(w.every)
	defer t.Stop()
	w.check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.kick:
		}
		w.check(ctx)
	}
}

// check asks noted once and acts on what changed.
func (w *alarmWatcher) check(ctx context.Context) {
	v, ok := w.version(ctx)
	if !ok || v == "" {
		return
	}
	w.observe(ctx, v)
}

// observe takes a version as current. A different one than last time is
// announced and pushed; the first one ever is only remembered.
func (w *alarmWatcher) observe(ctx context.Context, v string) {
	w.mu.Lock()
	if !w.loaded {
		if b, err := os.ReadFile(w.statePath); err == nil {
			w.last = strings.TrimSpace(string(b))
		}
		w.loaded = true
	}
	prev := w.last
	if v == prev {
		w.mu.Unlock()
		return
	}
	w.last = v
	if w.statePath != "" {
		if err := os.MkdirAll(filepath.Dir(w.statePath), 0o755); err == nil {
			_ = os.WriteFile(w.statePath, []byte(v+"\n"), 0o600)
		}
	}
	w.mu.Unlock()
	if prev == "" {
		return
	}
	w.emit(alarmsEvent, map[string]any{"version": v})
	w.pushAll(ctx, v)
}

// pushAll sends each registered phone the silent push, and forgets the
// tokens Apple says are dead.
func (w *alarmWatcher) pushAll(ctx context.Context, version string) {
	if w.devices == nil {
		return
	}
	store, err := w.devices()
	if err != nil {
		log.Printf("alarms: no devices to push to: %v", err)
		return
	}
	data := map[string]any{"kind": "alarms", "version": version}
	pushToVendors(ctx, store, "alarms", w.vendors, vendorPush{Silent: true, Data: data})
	if w.pusher == nil {
		return
	}
	p := w.pusher()
	if p == nil {
		return
	}
	pushToDevices(ctx, store, "alarms", func(ctx context.Context, t pushTarget) error {
		return p.Wake(ctx, t.Token, t.Env, data)
	})
}

// versionOf reads the version out of list_alarms' result.
func versionOf(structured any) string {
	m, ok := structured.(map[string]any)
	if !ok {
		return ""
	}
	switch v := m["version"].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// alarmWatch is the one watcher, built on first use: CallTool and the agent
// may poke it before or without it running.
func (a *App) alarmWatch() *alarmWatcher {
	a.alarmsOnce.Do(func() {
		w := newAlarmWatcher()
		w.version = a.notedAlarmsVersion
		w.emit = a.emit
		w.pusher = a.alarmPusher
		w.devices = func() (*deviceStore, error) { return openDevices(devicesPath()) }
		w.statePath = filepath.Join(backend.DataDir(), alarmsStateFile)
		a.alarmsV = w
	})
	return a.alarmsV
}

// alarmsMayHaveChanged is called after a noted alarm tool ran.
func (a *App) alarmsMayHaveChanged() { a.alarmWatch().poke() }

// startAlarmWatch runs the watcher, except on a hive worker: the phones are
// paired to the queen, and the queen is the one watching.
func (a *App) startAlarmWatch() {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s != nil && s.Hive.Role == backend.HiveRoleWorker {
		return
	}
	w := a.alarmWatch()
	ctx, cancel := context.WithCancel(context.Background())
	a.alarmsMu.Lock()
	if a.alarmsStop != nil {
		a.alarmsStop()
	}
	a.alarmsStop = cancel
	a.alarmsMu.Unlock()
	go w.run(ctx)
	a.startFireWatch(ctx)
}

func (a *App) stopAlarmWatch() {
	a.alarmsMu.Lock()
	defer a.alarmsMu.Unlock()
	if a.alarmsStop != nil {
		a.alarmsStop()
		a.alarmsStop = nil
	}
}

// notedAlarmsVersion asks noted for list_alarms' version. An unconfigured
// noted is skipped without a call; a failing one is logged once per reason,
// not once a minute.
func (a *App) notedAlarmsVersion(ctx context.Context) (string, bool) {
	acts := a.mcpActions()
	if ok, _ := acts.Configured(backend.NotedServer); !ok {
		return "", false
	}
	res := acts.Call(ctx, backend.NotedServer, "list_alarms", map[string]any{}, backend.MCPCallOptions{
		Unlisted: true,
		Quiet:    true,
		Timeout:  15 * time.Second,
	})
	why := ""
	if !res.OK {
		why = res.Code + ": " + res.Error
	}
	a.alarmsMu.Lock()
	if why != a.alarmsErr {
		if why != "" {
			log.Printf("alarms: list_alarms: %s", why)
		}
		a.alarmsErr = why
	}
	a.alarmsMu.Unlock()
	if !res.OK {
		return "", false
	}
	v := versionOf(res.Structured)
	return v, v != ""
}

// alarmToolObserver pokes the watcher after the agent ran a noted alarm tool,
// directly or from programmatic tool calling's script.
type alarmToolObserver struct {
	agent.BaseObserver
	poke func()
}

func (o alarmToolObserver) OnToolEnd(_ context.Context, info agent.ToolInfo, _ any, err error) {
	if err == nil && touchesAlarms(info.Tool, info.Args) {
		o.poke()
	}
}

// touchesAlarms says whether a call was (or, for a script, may have been) to
// a noted tool that changes what the phones ring: mcp_noted_create_alarm and
// its siblings, and the event and to-do tools. A false positive costs one
// list_alarms.
func touchesAlarms(tool string, args map[string]any) bool {
	prefix := "mcp_" + backend.NotedServer + "_"
	if strings.HasPrefix(tool, prefix) {
		return touchesSchedule(tool)
	}
	if tool == "execute_javascript" {
		b, _ := json.Marshal(args)
		code := string(b)
		return strings.Contains(code, prefix) && touchesSchedule(code)
	}
	return false
}

// touchesSchedule says whether a noted tool name (or a script naming tools)
// may change the schedule: alarms, events and to-dos all ring or notify now,
// and the alarms version covers them all.
func touchesSchedule(s string) bool {
	return strings.Contains(s, "alarm") || strings.Contains(s, "event") || strings.Contains(s, "task")
}

// alarmPusher is the APNs client the settings describe, or nil with one log
// line saying why there is none. It is rebuilt only when the configuration
// changes.
func (a *App) alarmPusher() alarmPusher {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	cfg, keyPEM := s.PushSettings()
	// The key text is part of what decides a rebuild, not of anything logged.
	key := fmt.Sprintf("%+v|%d|%x", cfg, len(keyPEM), hashToken(keyPEM))

	a.pushMu.Lock()
	defer a.pushMu.Unlock()
	if a.pushBuilt && key == a.pushKey {
		return a.pushV
	}
	a.pushBuilt, a.pushKey, a.pushV = true, key, nil

	var missing []string
	if keyPEM == "" && cfg.KeyPath == "" {
		missing = append(missing, "APNS_KEY_PATH or APNS_KEY")
	}
	if cfg.KeyID == "" {
		missing = append(missing, "APNS_KEY_ID")
	}
	if len(missing) > 0 {
		log.Printf("apns: pushes disabled: %s not set", strings.Join(missing, ", "))
		return nil
	}
	c, err := apns.New(apns.Config{
		Key:     []byte(keyPEM),
		KeyPath: cfg.KeyPath,
		KeyID:   cfg.KeyID,
		TeamID:  cfg.TeamID,
		Topic:   cfg.Topic,
		Env:     cfg.Env,
	})
	if err != nil {
		log.Printf("apns: pushes disabled: %v", err)
		return nil
	}
	log.Printf("apns: pushes enabled for %s (default %s)", cfg.Topic, cfg.Env)
	a.pushV = c
	return c
}
