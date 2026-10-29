package app

import (
	"log/slog"
	"sync/atomic"

	"github.com/liliang-cn/superleo/internal/backend"
)

// The Telegram bridge's lifecycle, alongside the scheduler's.
//
// Started from boot and after every settings save, because turning the bridge
// on is a settings change and having to restart the app to pick it up is the
// kind of friction that makes a feature go unused. Stopped and rebuilt rather
// than reconfigured: the poller holds a long-lived HTTP request and a lock, and
// swapping a token underneath it is more moving parts than starting again.

// telegramPolling says whether this process holds the poll, for the
// Channels list: a bridge that exists may be idle behind another process.
var telegramPolling atomic.Bool

// startTelegram brings the bridge up if Telegram is configured. Any problem is
// recorded and reported through GetStatus rather than failing the boot: an
// unreachable bot is no reason for the app not to open.
func (a *App) startTelegram() {
	a.stopTelegram()
	// A member of a hive: the bot belongs to the queen.
	if a.isHiveMember() {
		return
	}

	a.mu.Lock()
	svc, settings := a.svc, a.settings
	a.mu.Unlock()

	bridge, err := backend.NewTelegramBridge(svc, settings)
	if err != nil {
		a.mu.Lock()
		a.telegramErr = err.Error()
		a.mu.Unlock()
		slog.Warn("telegram not started", "error", err)
		return
	}
	if bridge == nil {
		a.mu.Lock()
		a.telegramErr = ""
		a.mu.Unlock()
		return
	}

	started, err := bridge.Start()
	a.mu.Lock()
	if err != nil {
		a.telegramErr = err.Error()
		a.mu.Unlock()
		slog.Warn("telegram not started", "error", err)
		return
	}
	a.telegramErr = ""
	a.telegram = bridge
	a.mu.Unlock()
	telegramPolling.Store(started)

	if started {
		slog.Info("telegram bridge polling")
	} else {
		// Not an error, and said plainly so it is not read as one: the other
		// process is answering, and this one would have split the updates with
		// it. See backend/filelock.go.
		slog.Info("telegram bridge idle: another process holds the poll")
	}
}

// stopTelegram ends the poll. Safe when nothing was started.
func (a *App) stopTelegram() {
	a.mu.Lock()
	bridge := a.telegram
	a.telegram = nil
	a.mu.Unlock()
	telegramPolling.Store(false)

	// Outside the lock: Stop waits for a turn in flight, and that turn runs
	// against the service this lock also guards.
	bridge.Stop()
}
