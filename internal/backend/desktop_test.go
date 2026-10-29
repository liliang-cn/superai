package backend

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeDesktopRelease serves archive as this platform's release asset, pinned
// at digest, for the length of the test.
func fakeDesktopRelease(t *testing.T, archive []byte, digest string) *int {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	key := runtime.GOOS + "/" + runtime.GOARCH
	oldAssets, oldURL := agentDesktopAssets[key], agentDesktopURL
	agentDesktopAssets[key] = [2]string{"agent-desktop.tar.gz", digest}
	agentDesktopURL = func(string) string { return srv.URL }
	t.Cleanup(func() { agentDesktopAssets[key], agentDesktopURL = oldAssets, oldURL })
	return &hits
}

func binName() string {
	if runtime.GOOS == "windows" {
		return "agent-desktop.exe"
	}
	return "agent-desktop"
}

func TestAgentDesktopIsUnpackedOnceFromAVerifiedArchive(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	archive := tarGz(t, map[string]string{binName(): "#!/bin/sh\n", "agent-desktop-macos-helper": "helper"})
	sum := sha256.Sum256(archive)
	hits := fakeDesktopRelease(t, archive, hex.EncodeToString(sum[:]))

	bin, err := EnsureAgentDesktop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(bin) != binName() {
		t.Fatalf("bin %s", bin)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(bin), "agent-desktop-macos-helper")); err != nil {
		t.Fatal("the helper beside the binary was not unpacked")
	}
	if _, err := EnsureAgentDesktop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *hits != 1 {
		t.Fatalf("downloaded %d times; the unpacked copy should have been trusted", *hits)
	}
}

func TestAgentDesktopRefusesAnArchiveThatDoesNotMatch(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	fakeDesktopRelease(t, tarGz(t, map[string]string{binName(): "evil"}), strings.Repeat("0", 64))
	if _, err := EnsureAgentDesktop(context.Background()); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err %v, want a digest refusal", err)
	}
	if _, err := os.Stat(AgentDesktopDir()); !os.IsNotExist(err) {
		t.Fatal("something was unpacked from an archive that did not match")
	}
}

func TestAgentDesktopRefusesAnArchiveWithPaths(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	archive := tarGz(t, map[string]string{"../escape": "x"})
	sum := sha256.Sum256(archive)
	fakeDesktopRelease(t, archive, hex.EncodeToString(sum[:]))
	if _, err := EnsureAgentDesktop(context.Background()); err == nil {
		t.Fatal("an archive with a path in a name was unpacked")
	}
}

func TestDesktopControlIsOffUntilChosen(t *testing.T) {
	t.Setenv("SUPERLEO_DESKTOP", "")
	if (&Settings{}).desktopControl() {
		t.Fatal("desktop control on by default")
	}
	if !(&Settings{DesktopControl: true}).desktopControl() {
		t.Fatal("chosen and still off")
	}
	t.Setenv("SUPERLEO_DESKTOP", "off")
	if (&Settings{DesktopControl: true}).desktopControl() {
		t.Fatal("SUPERLEO_DESKTOP=off did not win over the file")
	}
}

func TestDesktopLeavesThePersonsPrivateAppsAlone(t *testing.T) {
	d := &desktopRuntime{blocked: (&Settings{}).desktopBlocked(), snaps: map[string]string{}}
	for _, app := range []string{"微信", "WeChat", "wechat", "Mail", "邮件", "Messages", "1Password 8", "Terminal", "Otty", "系统设置"} {
		if !d.blockedName(app) {
			t.Errorf("%s is not blocked", app)
		}
	}
	for _, app := range []string{"计算器", "Calculator", "Finder", "访达", "SuperLeo", "Notes", ""} {
		if d.blockedName(app) {
			t.Errorf("%s is blocked", app)
		}
	}
	// The person's own list replaces the default.
	own := &desktopRuntime{blocked: (&Settings{DesktopBlockedApps: []string{"Notes"}}).desktopBlocked()}
	if !own.blockedName("Notes") || own.blockedName("微信") {
		t.Fatal("the person's own list did not replace the default")
	}
}

func TestDesktopActsOnlyOnRefsItHandedOut(t *testing.T) {
	d := &desktopRuntime{snaps: map[string]string{}}
	if _, err := d.refApp("@s8f3k2p9:e5"); err == nil {
		t.Fatal("a ref from no snapshot taken here was accepted")
	}
	d.remember("计算器", []byte(`{"data":{"tree":{"ref_id":"@skdq:e1","children":[{"ref_id":"@skdq:e2"}]}}}`))
	app, err := d.refApp("@skdq:e2")
	if err != nil || app != "计算器" {
		t.Fatalf("app %q err %v", app, err)
	}
	for _, bad := range []string{"e2", "@skdq", "@skdq:e2 --headed", "@skdq:e2;rm"} {
		if _, err := d.refApp(bad); err == nil {
			t.Errorf("%q passed as a ref", bad)
		}
	}
}

func TestDesktopRefusesCombosThatQuit(t *testing.T) {
	for _, c := range []string{"cmd+q", "Cmd+Q", "command+q", "q+cmd", "cmd+w", "alt+f4", "ctrl+q", "option+cmd+escape", "cmd+shift+q"} {
		if !isQuitCombo(c) {
			t.Errorf("%s passed", c)
		}
	}
	for _, c := range []string{"return", "escape", "cmd+c", "cmd+f", "shift+tab", "q", "w"} {
		if isQuitCombo(c) {
			t.Errorf("%s refused", c)
		}
	}
}

// An emptied list is the person's "none" and must survive the file; only a
// null one is the default.
func TestAnEmptiedBlockListIsKept(t *testing.T) {
	for _, tc := range []struct {
		in      []string
		def     bool
		blocked int
	}{{nil, true, len(desktopBlockedDefault)}, {[]string{}, false, 0}, {[]string{"Notes"}, false, 1}} {
		raw, err := json.Marshal(&Settings{DesktopBlockedApps: tc.in})
		if err != nil {
			t.Fatal(err)
		}
		var back Settings
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if (back.DesktopBlockedApps == nil) != tc.def || len(back.desktopBlocked()) != tc.blocked {
			t.Errorf("%v came back as %v", tc.in, back.DesktopBlockedApps)
		}
	}
}
