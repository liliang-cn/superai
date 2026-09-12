package app

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The built frontend, and the two things that serve it.
//
// //go:embed can only reach files inside its own package directory, and the
// frontend builds to <repo>/frontend/dist — which is above this package. So the
// embed declaration stays in the root main.go, beside the directory it names,
// and hands the filesystem down here once at startup. Everything in this
// package reads it through Assets rather than carrying its own copy.

// Assets is the embedded frontend build. Set by main() before anything reads
// it; nil in tests that never touch the web surfaces.
var Assets embed.FS

// avatarWeb is the built frontend, rooted so "avatar.html" and "assets/…"
// resolve — the avatar server serves its page out of the same build the app
// itself runs on, rather than carrying a second copy of a renderer.
//
// Returns nil if the embed is somehow not there, which leaves the page
// unavailable and everything an external renderer actually needs — the event
// stream and the sprites — working.
func avatarWeb() fs.FS {
	sub, err := fs.Sub(Assets, "frontend/dist")
	if err != nil {
		return nil
	}
	return sub
}

// NoCache stops WKWebView/WebView2 from persisting index.html across rebuilds.
// Each build emits new hashed asset filenames, but a cached index.html keeps
// pointing at the old hashes (which 404 in the new binary) — leaving the UI
// unstyled/broken until the WebKit cache is manually cleared. Revalidating
// HTML (and our wails bindings) every load avoids that entirely.
//
// Exported because the window is assembled in main() and this is the
// middleware it installs on the Wails asset server.
func NoCache(next http.Handler) http.Handler {
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
