package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeAPK(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app-release.apk")
	if err := os.WriteFile(p, []byte("PK\x03\x04"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishAndroidWritesTheReleaseTheRouteServes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "android")
	src := fakeAPK(t, strings.Repeat("x", 5000))
	rel, err := publishAndroid(dir, src, 3, "0.3.0", " 闹钟准时响 ")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(src)
	sum := sha256.Sum256(raw)
	if rel.URL != "superleo-0.3.0.apk" || rel.SHA256 != hex.EncodeToString(sum[:]) || rel.Notes != "闹钟准时响" || rel.VersionCode != 3 {
		t.Fatalf("release: %+v", rel)
	}
	var onDisk AndroidRelease
	b, _ := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err := json.Unmarshal(b, &onDisk); err != nil || onDisk != rel {
		t.Fatalf("latest.json: %s", b)
	}

	mux := http.NewServeMux()
	androidRoutes(mux, func() string { return dir })
	for path, want := range map[string]string{"/api/android/latest.json": string(b), "/api/android/superleo-0.3.0.apk": string(raw)} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}

	// Older APKs are pruned to the newest three.
	for i, name := range []string{"0.4.0", "0.5.0", "0.6.0"} {
		time.Sleep(10 * time.Millisecond)
		if _, err := publishAndroid(dir, src, int64(4+i), name, ""); err != nil {
			t.Fatal(err)
		}
	}
	apks, _ := filepath.Glob(filepath.Join(dir, "*.apk"))
	if len(apks) != 3 {
		t.Errorf("kept %v", apks)
	}
	if _, err := os.Stat(filepath.Join(dir, "superleo-0.3.0.apk")); !os.IsNotExist(err) {
		t.Error("the oldest APK was not pruned")
	}

	for _, c := range []struct {
		src  string
		code int64
		name string
	}{
		{src, 0, "1.0"},  // no versionCode
		{src, 7, "../x"}, // a name that leaves the directory
		{filepath.Join(t.TempDir(), "nope.apk"), 7, ""}, // no such file
	} {
		if _, err := publishAndroid(dir, c.src, c.code, c.name, ""); err == nil {
			t.Errorf("%+v: published", c)
		}
	}
	notZip := filepath.Join(t.TempDir(), "x.apk")
	_ = os.WriteFile(notZip, []byte("<html>"), 0o644)
	if _, err := publishAndroid(dir, notZip, 8, "", ""); err == nil {
		t.Error("published something that is not a zip")
	}
}
