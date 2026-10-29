package app

import (
	"context"
	"strings"

	"github.com/liliang-cn/superleo/internal/backend"
)

// DesktopStatus reports whether the agent may operate this machine's desktop
// apps, whether agent-desktop is ready, and the OS permission it runs under.
func (a *App) DesktopStatus() backend.DesktopStatus {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	st := svc.DesktopStatus()
	// The list is the settings', read from them rather than from the
	// service: a save rebuilds the service, and until the new one is up the
	// old one would answer with the list just replaced.
	cur := a.GetSettings()
	st.Blocked, st.BlockedIsDefault = backend.DesktopBlockedApps(&cur)
	return st
}

// SetDesktopControl turns desktop control on or off at once, the way
// SetBrowserMode does: the backend rebuilds around it.
func (a *App) SetDesktopControl(on bool) error {
	s := a.GetSettings()
	s.DesktopControl = on
	return a.SaveSettings(s)
}

// DesktopRequestAccess asks macOS for the Accessibility permission desktop
// control runs under, and opens System Settings where it is granted.
func (a *App) DesktopRequestAccess() error {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	return svc.DesktopRequestAccess(context.Background())
}

// SetDesktopBlockedApps replaces the apps desktop control leaves alone, or
// with useDefault puts the default list back.
func (a *App) SetDesktopBlockedApps(apps []string, useDefault bool) error {
	s := a.GetSettings()
	if useDefault {
		s.DesktopBlockedApps = nil
		return a.SaveSettings(s)
	}
	list := []string{}
	seen := map[string]bool{}
	for _, app := range apps {
		app = strings.TrimSpace(app)
		if app == "" || seen[strings.ToLower(app)] {
			continue
		}
		seen[strings.ToLower(app)] = true
		list = append(list, app)
	}
	s.DesktopBlockedApps = list
	return a.SaveSettings(s)
}
