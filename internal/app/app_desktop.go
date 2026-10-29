package app

import (
	"context"

	"github.com/liliang-cn/superleo/internal/backend"
)

// DesktopStatus reports whether the agent may operate this machine's desktop
// apps, whether agent-desktop is ready, and the OS permission it runs under.
func (a *App) DesktopStatus() backend.DesktopStatus {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	return svc.DesktopStatus()
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
