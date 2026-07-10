package main

import (
	"embed"
	"net/http"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// noCache stops WKWebView/WebView2 from persisting index.html across rebuilds.
// Each build emits new hashed asset filenames, but a cached index.html keeps
// pointing at the old hashes (which 404 in the new binary) — leaving the UI
// unstyled/broken until the WebKit cache is manually cleared. Revalidating
// HTML (and our wails bindings) every load avoids that entirely.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || p == "" || strings.HasSuffix(p, ".html") || strings.HasPrefix(p, "/wails") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "SuperAI",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: noCache,
		},
		BackgroundColour: &options.RGBA{R: 14, G: 17, B: 22, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true, // native OS file drop -> runtime.OnFileDrop (host paths)
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
