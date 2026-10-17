package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAndroidReleaseIsServedToPairedDevices(t *testing.T) {
	h, c, _ := pairingServer(t)
	dir := t.TempDir()
	mux := http.NewServeMux()
	androidRoutes(mux, func() string { return dir })
	gated := requireAuth(c, mux)

	code := startCode(t, h, c)
	rec := call(h, http.MethodPost, pairClaimPath, "", `{"code":"`+code+`","device":"Pixel"}`)
	var claim struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &claim)
	if claim.Token == "" {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}

	// No release yet: 404, which the app reads as nothing to install.
	if rec := call(gated, http.MethodGet, "/api/android/latest.json", claim.Token, ""); rec.Code != http.StatusNotFound {
		t.Errorf("no release: %d", rec.Code)
	}

	manifest := `{"versionCode":2,"versionName":"0.2.0","url":"superai-0.2.0.apk","sha256":"ab","notes":"闹钟"}`
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	apk := strings.Repeat("PK", 1000)
	if err := os.WriteFile(filepath.Join(dir, "superai-0.2.0.apk"), []byte(apk), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.apk"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Not without a credential.
	if rec := call(gated, http.MethodGet, "/api/android/latest.json", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}

	rec = call(gated, http.MethodGet, "/api/android/latest.json", claim.Token, "")
	if rec.Code != http.StatusOK || rec.Body.String() != manifest {
		t.Fatalf("latest.json: %d %q", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("latest.json type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("latest.json cache %q", cc)
	}

	rec = call(gated, http.MethodGet, "/api/android/superai-0.2.0.apk", claim.Token, "")
	if rec.Code != http.StatusOK || rec.Body.Len() != len(apk) {
		t.Fatalf("apk: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.android.package-archive" {
		t.Errorf("apk type %q", ct)
	}

	// A cut-off download resumes.
	r := httptest.NewRequest(http.MethodGet, "/api/android/superai-0.2.0.apk", nil)
	r.Header.Set("Authorization", "Bearer "+claim.Token)
	r.Header.Set("Range", "bytes=1000-")
	ranged := httptest.NewRecorder()
	gated.ServeHTTP(ranged, r)
	if ranged.Code != http.StatusPartialContent || ranged.Body.Len() != len(apk)-1000 {
		t.Errorf("range: %d, %d bytes", ranged.Code, ranged.Body.Len())
	}

	// Nothing but the release files, and nothing outside the directory (the
	// mux answers a path with .. in it with a redirect to its clean form).
	for _, p := range []string{"/api/android/../secret.apk", "/api/android/%2e%2e%2fsecret.apk", "/api/android/notes.txt", "/api/android/", "/api/android/sub/x.apk"} {
		if rec := call(gated, http.MethodGet, p, claim.Token, ""); rec.Code != http.StatusNotFound && rec.Code != http.StatusTemporaryRedirect {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	if rec := call(gated, http.MethodPost, "/api/android/latest.json", claim.Token, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}
