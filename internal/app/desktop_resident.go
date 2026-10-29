package app

// The desktop app staying where the hive can find it: opened at login, and
// kept running when its window is closed.
//
// Wails v2 has no menu-bar (status) item: that arrived in v3. What it has is
// HideWindowOnClose, which makes the red close button hide the app, as
// Cmd+H does, instead of quitting it; the Dock icon brings the window back
// and Cmd+Q still quits. That option is fixed when the window is made, so
// "keep running" is read as the app starts and a change applies at the next
// start (DesktopResidency says so).
//
// The login item is a LaunchAgent that opens the app at login with --hidden,
// which a keep-running app honours by starting with no window. It is a plist
// beside the others in ~/Library/LaunchAgents and is not loaded when written:
// launchd reads it at the next login, so enabling it never opens a second
// copy of the app.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/liliang-cn/superleo/internal/backend"
)

const loginItemLabel = "cn.superleo.superleo-login"

// HiddenArg is what the login item opens the app with.
const HiddenArg = "--hidden"

// KeepRunningWanted is whether the window's close should hide the app. Read
// before the window is made.
func KeepRunningWanted() bool {
	s, _ := backend.LoadSettings()
	return s != nil && s.KeepRunning && runtime.GOOS == "darwin"
}

// StartHiddenWanted is whether this start opens with no window: a login
// item's start of an app that is kept running.
func StartHiddenWanted(args []string) bool {
	if !KeepRunningWanted() {
		return false
	}
	for _, a := range args {
		if a == HiddenArg {
			return true
		}
	}
	return false
}

// holdResident is called once the app is up.
func (a *App) holdResident() { holdOffAppNap(a.GetSettings().KeepRunning) }

func loginItemPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", loginItemLabel+".plist")
}

func loginItemPlist(exe string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>LimitLoadToSessionType</key><string>Aqua</string>
  <key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`, loginItemLabel, xmlEscape(exe), HiddenArg)
}

// writeLoginItem adds or removes the login item.
func writeLoginItem(on bool) error {
	p := loginItemPath()
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(loginItemPlist(exe)), 0o644)
}

// syncLoginItem brings the login item in line with the setting. The desktop
// app only: a served SuperLeo has no login to start at.
func (a *App) syncLoginItem(on bool) {
	if a.ctx == nil || runtime.GOOS != "darwin" {
		return
	}
	_ = writeLoginItem(on)
}

// DesktopResident is how the app stays running, for Settings.
type DesktopResident struct {
	StartAtLogin bool `json:"startAtLogin"`
	KeepRunning  bool `json:"keepRunning"`
	// LoginItem is the login item on disk now.
	LoginItem bool `json:"loginItem"`
	// RestartToApply is a keep-running change the open window has not
	// picked up: it is read as the app starts.
	RestartToApply bool `json:"restartToApply"`
	// MenuBar is a menu-bar icon; Wails v2 cannot make one.
	MenuBar bool `json:"menuBar"`
	// Supported is whether this system has any of it.
	Supported bool `json:"supported"`
}

// DesktopResidency reports it.
func (a *App) DesktopResidency() DesktopResident {
	s := a.GetSettings()
	_, err := os.Stat(loginItemPath())
	return DesktopResident{
		StartAtLogin: s.StartAtLogin, KeepRunning: s.KeepRunning, LoginItem: err == nil,
		RestartToApply: s.KeepRunning != a.launchedKeepRunning.Load(),
		Supported:      runtime.GOOS == "darwin",
	}
}

func (a *App) setResident(set func(*backend.Settings)) error {
	if runtime.GOOS != "darwin" {
		return errors.New("only the Mac app can start at login or stay in the background")
	}
	a.mu.Lock()
	if a.settings == nil {
		a.mu.Unlock()
		return errors.New("the settings are not loaded")
	}
	next := *a.settings
	set(&next)
	if err := next.Save(); err != nil {
		a.mu.Unlock()
		return err
	}
	a.settings = &next
	a.mu.Unlock()
	a.emit("settings:changed", nil)
	return nil
}

// SetStartAtLogin turns the login item on or off.
func (a *App) SetStartAtLogin(on bool) (DesktopResident, error) {
	if err := a.setResident(func(s *backend.Settings) { s.StartAtLogin = on }); err != nil {
		return a.DesktopResidency(), err
	}
	if runtime.GOOS == "darwin" {
		if err := writeLoginItem(on); err != nil {
			return a.DesktopResidency(), err
		}
	}
	return a.DesktopResidency(), nil
}

// SetKeepRunning makes closing the window hide the app instead of quitting
// it, from the next start.
func (a *App) SetKeepRunning(on bool) (DesktopResident, error) {
	if err := a.setResident(func(s *backend.Settings) { s.KeepRunning = on }); err != nil {
		return a.DesktopResidency(), err
	}
	holdOffAppNap(on)
	return a.DesktopResidency(), nil
}
