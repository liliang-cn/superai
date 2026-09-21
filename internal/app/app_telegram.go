package app

import (
	"log"

	"github.com/liliang-cn/superai-desktop/internal/backend"
)

// The Telegram bridge's lifecycle, alongside the scheduler's.
//
// Started from boot and after every settings save, because turning the bridge
// on is a settings change and having to restart the app to pick it up is the
// kind of friction that makes a feature go unused. Stopped and rebuilt rather
// than reconfigured: the poller holds a long-lived HTTP request and a lock, and
// swapping a token underneath it is more moving parts than starting again.

// startTelegram brings the bridge up if Telegram is configured. Any problem is
// recorded and reported through GetStatus rather than failing the boot: an
// unreachable bot is no reason for the app not to open.
func (a *App) startTelegram() {
	a.stopTelegram()

	a.mu.Lock()
	svc, settings := a.svc, a.settings
	a.mu.Unlock()

	bridge, err := backend.NewTelegramBridge(svc, settings)
	if err != nil {
		a.mu.Lock()
		a.telegramErr = err.Error()
		a.mu.Unlock()
		log.Printf("superai: telegram not started: %v", err)
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
		log.Printf("superai: telegram not started: %v", err)
		return
	}
	a.telegramErr = ""
	a.telegram = bridge
	a.mu.Unlock()

	if started {
		log.Printf("superai: telegram bridge polling")
	} else {
		// Not an error, and said plainly so it is not read as one: the other
		// process is answering, and this one would have split the updates with
		// it. See backend/filelock.go.
		log.Printf("superai: telegram bridge idle — another process holds the poll")
	}
}

// stopTelegram ends the poll. Safe when nothing was started.
func (a *App) stopTelegram() {
	a.mu.Lock()
	bridge := a.telegram
	a.telegram = nil
	a.mu.Unlock()

	// Outside the lock: Stop waits for a turn in flight, and that turn runs
	// against the service this lock also guards.
	bridge.Stop()
}
