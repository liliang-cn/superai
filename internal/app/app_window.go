package app

import (
	"os/exec"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The window behaves like any other Mac window, though its title bar is the
// app's own: a double-click on a strip it can be dragged by does what the
// person set in System Settings (zoom, minimize or nothing), and the menu bar
// has the Window menu and full screen.

// TitleBarDoubleClick is a double-click on one of the window's drag strips.
func (a *App) TitleBarDoubleClick() {
	if a.ctx == nil {
		return
	}
	out, _ := exec.Command("defaults", "read", "-g", "AppleActionOnDoubleClick").Output()
	switch strings.TrimSpace(string(out)) {
	case "Minimize":
		runtime.WindowMinimise(a.ctx)
	case "None":
	default: // "Maximize" (shown as Zoom), and the default when never set
		runtime.WindowToggleMaximise(a.ctx)
	}
}

func (a *App) toggleFullscreen() {
	if a.ctx == nil {
		return
	}
	if runtime.WindowIsFullscreen(a.ctx) {
		runtime.WindowUnfullscreen(a.ctx)
	} else {
		runtime.WindowFullscreen(a.ctx)
	}
}

// AppMenu is the menu bar: the app menu, Edit (so copy and paste work in the
// window), View with full screen and reload, and the system Window menu. A
// function, not a method, so it is not bound into the window.
func AppMenu(a *App) *menu.Menu {
	m := menu.NewMenu()
	m.Append(menu.AppMenu())
	m.Append(menu.EditMenu())
	view := m.AddSubmenu("View")
	view.AddText("Toggle Full Screen", keys.Combo("f", keys.CmdOrCtrlKey, keys.ControlKey), func(*menu.CallbackData) { a.toggleFullscreen() })
	view.AddText("Reload", keys.CmdOrCtrl("r"), func(*menu.CallbackData) {
		if a.ctx != nil {
			runtime.WindowReloadApp(a.ctx)
		}
	})
	m.Append(menu.WindowMenu())
	return m
}
