package app

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superleo/internal/backend"
)

func needMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Xcode projects are a Mac's")
	}
}

// A directory is in an Xcode project when it or a parent up to the
// repository's root holds a .xcodeproj, a .xcworkspace or an iOS package.
func TestXcodeRootIsFoundUpToTheRepository(t *testing.T) {
	needMac(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	ios := mkRepo(t, filepath.Join(base, "ios"))
	os.MkdirAll(filepath.Join(ios, "App.xcodeproj"), 0o755)
	sub := filepath.Join(ios, "App", "Sources")
	os.MkdirAll(sub, 0o755)
	if got := xcodeRoot(sub); got != ios {
		t.Fatalf("xcodeproj: got %q, want %q", got, ios)
	}

	pkg := mkRepo(t, filepath.Join(base, "pkg"))
	os.WriteFile(filepath.Join(pkg, "Package.swift"), []byte(`platforms: [.iOS(.v17)]`), 0o644)
	if got := xcodeRoot(pkg); got != pkg {
		t.Fatalf("iOS package: got %q", got)
	}

	server := mkRepo(t, filepath.Join(base, "server"))
	os.WriteFile(filepath.Join(server, "Package.swift"), []byte(`platforms: [.macOS(.v14)]`), 0o644)
	if got := xcodeRoot(server); got != "" {
		t.Fatalf("a macOS-only package is no iOS project: %q", got)
	}

	// The search stops at the repository: a project above it is not this one's.
	os.MkdirAll(filepath.Join(base, "Outer.xcodeproj"), 0o755)
	if got := xcodeRoot(server); got != "" {
		t.Fatalf("looked past the repository: %q", got)
	}
}

// A call becomes xcodebuild's arguments with paths kept in the project.
func TestXcodebuildArgsStayInTheProject(t *testing.T) {
	root := "/work/ios"
	got, err := xcodebuildArgs(root, xcodeBuildIn{Project: "App.xcodeproj", Scheme: "App",
		Destination: "platform=iOS Simulator,name=iPhone 17", OnlyTesting: []string{"AppTests/AlarmTests"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-project", "/work/ios/App.xcodeproj", "-scheme", "App", "-destination", "platform=iOS Simulator,name=iPhone 17",
		"-only-testing:AppTests/AlarmTests", "test"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for _, in := range []xcodeBuildIn{
		{Project: "../other/X.xcodeproj"},
		{Workspace: "/etc/X.xcworkspace"},
		{Action: "archive"},
		{Action: "-exportArchive"},
		{Project: "A.xcodeproj", Workspace: "A.xcworkspace"},
	} {
		if _, err := xcodebuildArgs(root, in); err == nil {
			t.Errorf("%+v was let through", in)
		}
	}
}

// The digest keeps the verdict and the failures, not the compile noise.
func TestXcodeDigestKeepsWhatMatters(t *testing.T) {
	out := strings.Repeat("CompileSwift normal arm64 /x.swift\n", 500) +
		"Test Case '-[AppTests.AlarmTests testFires]' failed (0.01 seconds).\n" +
		"/x/AlarmTests.swift:12: error: XCTAssertEqual failed\n" +
		"** TEST FAILED **\n"
	d := xcodeDigest("xcodebuild test", 65, "/tmp/l.log", []byte(out))
	for _, want := range []string{"exit 65", "/tmp/l.log", "testFires]' failed", "XCTAssertEqual failed", "** TEST FAILED **"} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q", want)
		}
	}
	if strings.Count(d, "CompileSwift") > 40 {
		t.Errorf("digest keeps the compile lines: %d", strings.Count(d, "CompileSwift"))
	}
}

// A sandboxed codex run in an Xcode project is handed the Xcode server on
// the approval channel; one elsewhere is not.
func TestASandboxedCodexRunInAnXcodeProjectGetsXcode(t *testing.T) {
	needMac(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	bin := fakeCLI(t, "codex", `for a in "$@"; do printf '%s\n' "$a"; done > `+argsFile)
	noDiscovery(t, agentexec.Installed{Name: "codex", Binary: bin})
	root, _ := filepath.EvalSymlinks(t.TempDir())
	a := codingApp(t, backend.ExternalAgents{Roots: []string{root}})

	server := func(cwd string) string {
		done := make(chan CLIRun, 1)
		if _, err := a.startCLIRun(cliStart{Agent: "codex", Prompt: "run the tests", Cwd: cwd, Ask: true, Done: func(r CLIRun) { done <- r }}); err != nil {
			t.Fatal(err)
		}
		<-done
		raw, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(raw), "\n") {
			if v, ok := strings.CutPrefix(l, "mcp_servers."+xcodeServer+"="); ok {
				return v
			}
		}
		return ""
	}

	ios := mkRepo(t, filepath.Join(root, "ios"))
	os.MkdirAll(filepath.Join(ios, "App.xcodeproj"), 0o755)
	v := server(ios)
	var cfg struct {
		S struct {
			URL     string `toml:"url"`
			Mode    string `toml:"default_tools_approval_mode"`
			Timeout int    `toml:"tool_timeout_sec"`
		} `toml:"s"`
	}
	if _, err := toml.Decode("s = "+v, &cfg); err != nil {
		t.Fatalf("%q: %v", v, err)
	}
	if !strings.HasPrefix(cfg.S.URL, "http://127.0.0.1:") || !strings.HasSuffix(cfg.S.URL, "/xcode") || cfg.S.Mode != "approve" || cfg.S.Timeout < 600 {
		t.Fatalf("server: %+v", cfg.S)
	}

	if v := server(mkRepo(t, filepath.Join(root, "web"))); v != "" {
		t.Fatalf("a run outside an Xcode project got xcode: %s", v)
	}
}
