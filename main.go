package main

import (
	"embed"
	"os"

	"github.com/liliang-cn/superai-desktop/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

// The frontend build. It has to be declared here rather than in internal/app,
// because //go:embed cannot reach outside its own package directory and
// frontend/ is a sibling of this file, not of that package. main() hands it
// over on the first line it runs.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app.Assets = assets

	// The same binary, minus the window: `superai-desktop serve` runs the app
	// behind a local HTTP server instead of a WKWebView (see
	// internal/app/server.go).
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		app.ServeMain(os.Args[2:])
		return
	}

	// Create an instance of the app structure
	a := app.NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "SuperAI",
		Width:  1280,
		Height: 860,
		// A floor, because there was none: the window could be dragged to any
		// size at all, including ones no layout has an answer for. The frontend
		// handles a phone-shaped viewport — the browser build is used from
		// one — so the floor is that shape rather than a desktop one, and the
		// layout collapses to the icon rail and a floating panel on the way
		// down. Below this the reactor's own labels start leaving the disc.
		MinWidth:  480,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: app.NoCache,
		},
		// The paper this app is printed on, so a cold frame never flashes behind
		// the page while the webview is still coming up.
		BackgroundColour: &options.RGBA{R: 244, G: 241, B: 234, A: 1},
		// The window is part of the design, not a frame the OS wraps around it.
		// A stock title bar spends 28px of every screen on the word "SuperAI",
		// which the sidebar already says, in a strip that cannot hold anything
		// else. Hidden-inset keeps the traffic lights where every Mac user
		// reaches for them and gives the app the rest of the bar; the sidebar
		// reserves a 38px lane for them and marks it draggable
		// (--wails-draggable: drag) so the window still moves by its top edge.
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			// Appearance is deliberately unset: SetWindowTheme flips the
			// window between light and dark from the theme picker, and a value
			// here would pin the chrome to one of them for the whole run.
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			About: &mac.AboutInfo{
				Title:   "SuperAI",
				Message: "Your own agent, on your own machine.",
			},
		},
		OnStartup:  a.Startup,
		OnShutdown: a.Shutdown,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true, // native OS file drop -> runtime.OnFileDrop (host paths)
		},
		Bind: []interface{}{
			a,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
