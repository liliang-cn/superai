package app

import (
	"context"
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

// DomReady is the window's first full page. The window opens at the size
// main.go asks for, 1280×860, which is taller than many a laptop's screen
// (1366×768, or a VM's 1280×800): its title bar then starts above the top
// edge, where it can be neither seen nor dragged. So on a smaller screen it
// shrinks to fit and moves to the middle.
func (a *App) DomReady(ctx context.Context) {
	screens, err := runtime.ScreenGetAll(ctx)
	if err != nil {
		return
	}
	for _, s := range screens {
		if !s.IsCurrent {
			continue
		}
		w, h := runtime.WindowGetSize(ctx)
		if fw, fh, smaller := fitToScreen(w, h, s.Size.Width, s.Size.Height); smaller {
			runtime.WindowSetSize(ctx, fw, fh)
			runtime.WindowCenter(ctx)
		}
		return
	}
}

// fitToScreen is the window's size on a screen of sw×sh: unchanged when it
// fits, else at most nine tenths of the screen each way, leaving the menu
// bar, the Dock or the taskbar their room.
func fitToScreen(w, h, sw, sh int) (int, int, bool) {
	if sw <= 0 || sh <= 0 {
		return w, h, false
	}
	fw, fh := min(w, sw*9/10), min(h, sh*9/10)
	return fw, fh, fw != w || fh != h
}
