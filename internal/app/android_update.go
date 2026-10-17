package app

// The Android app is sideloaded, so the core is where it updates from. The
// release is two files dropped into <data dir>/android: latest.json, and the
// APK it names.
//
//	{"versionCode":2,"versionName":"0.2.0","url":"superai-0.2.0.apk",
//	 "sha256":"<hex>","notes":"…"}
//
// `superai android publish` (android_publish.go) writes both. The app asks
// for latest.json at most once a day, downloads the APK when
// versionCode is newer than its own, checks sha256 and hands it to the
// system installer. A relative url is read against /api/android/. Both
// routes sit under /api, so a paired device's token is what opens them, like
// every other call it makes.

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liliang-cn/superai/internal/backend"
)

const (
	androidPrefix   = "/api/android/"
	androidDir      = "android"
	androidManifest = "latest.json"
)

// androidFile is a name the route serves: latest.json, or an APK with a
// plain name. Nothing with a slash, so nothing outside the directory.
var androidFile = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.apk$`)

func androidReleaseDir() string { return filepath.Join(backend.DataDir(), androidDir) }

// androidRoutes serves the release files from dir(); 404 when there is no
// release, which the app reads as "no update".
func androidRoutes(mux *http.ServeMux, dir func() string) {
	mux.HandleFunc(androidPrefix, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, androidPrefix)
		switch {
		case name == androidManifest:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-cache")
		case androidFile.MatchString(name):
			w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		default:
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(dir(), name)
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		// ServeFile answers Range requests, so a download cut off on a
		// train resumes instead of starting over.
		http.ServeFile(w, r, path)
	})
}
