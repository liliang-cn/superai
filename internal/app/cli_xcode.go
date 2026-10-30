package app

// Xcode for a sandboxed codex run, run by SuperLeo outside the sandbox.
//
// Codex's workspace-write sandbox on macOS is a Seatbelt profile of its own
// whose mach-lookup list it does not let a config add to: xcodebuild test in
// it cannot reach CoreSimulator, and Swift macro plugins, run as processes
// of their own by the compiler, fail too. The same command outside passes.
// So a codex run in an Xcode project is handed an MCP server on the approval
// channel's loopback port (app_cliapprove.go) whose tools run xcodebuild and
// simctl here, in the project, and hand back what they printed.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// xcodeServer is the name codex knows the tools under.
const xcodeServer = "superleo_xcode"

// xcodeWait is how long one xcodebuild may take: a full UI test pass is long.
const xcodeWait = 45 * time.Minute

// xcodeMu keeps one xcodebuild at a time on this Mac: two test runs fight
// over the same simulators.
var xcodeMu sync.Mutex

// xcodeRoot is the directory of the Xcode project dir is in — one holding a
// .xcodeproj, a .xcworkspace or a Package.swift naming iOS — looked for from
// dir up to its repository's root; "" when there is none, or off a Mac.
func xcodeRoot(dir string) string {
	if runtime.GOOS != "darwin" || dir == "" {
		return ""
	}
	for d := dir; ; {
		if isXcodeDir(d) {
			return d
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return ""
		}
		up := filepath.Dir(d)
		if up == d {
			return ""
		}
		d = up
	}
}

func isXcodeDir(d string) bool {
	entries, err := os.ReadDir(d)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".xcodeproj") || strings.HasSuffix(n, ".xcworkspace") {
			return true
		}
	}
	b, err := os.ReadFile(filepath.Join(d, "Package.swift"))
	return err == nil && strings.Contains(string(b), ".iOS")
}

// codexXcodeArgs are the -c overrides that hand codex the Xcode server at
// url: its tools need no asking (the sandbox is what they stand in for), and
// a test run is given longer than codex's minute.
func codexXcodeArgs(url string) []string {
	c := map[string]any{
		"url":                         url,
		"default_tools_approval_mode": "approve",
		"tool_timeout_sec":            int(xcodeWait / time.Second),
	}
	return []string{"-c", "mcp_servers." + xcodeServer + "=" + tomlValue(c)}
}

type xcodeBuildIn struct {
	Action        string   `json:"action,omitempty" jsonschema:"test (the default), build, build-for-testing, test-without-building or clean"`
	Project       string   `json:"project,omitempty" jsonschema:"the .xcodeproj, relative to the project directory"`
	Workspace     string   `json:"workspace,omitempty" jsonschema:"the .xcworkspace, relative to the project directory; instead of project"`
	Scheme        string   `json:"scheme,omitempty" jsonschema:"the scheme to build"`
	Destination   string   `json:"destination,omitempty" jsonschema:"an xcodebuild -destination, e.g. platform=iOS Simulator,name=iPhone 17"`
	Configuration string   `json:"configuration,omitempty" jsonschema:"Debug or Release"`
	OnlyTesting   []string `json:"only_testing,omitempty" jsonschema:"tests to run alone, e.g. AppTests/AlarmTests"`
	SkipTesting   []string `json:"skip_testing,omitempty" jsonschema:"tests to leave out"`
}

var xcodeActions = map[string]bool{"test": true, "build": true, "build-for-testing": true, "test-without-building": true, "clean": true}

// xcodebuildArgs checks a call against the project at root and turns it into
// xcodebuild's arguments. Paths stay inside root; every value goes after its
// own flag, so none is read as another flag.
func xcodebuildArgs(root string, in xcodeBuildIn) ([]string, error) {
	action := strings.TrimSpace(in.Action)
	if action == "" {
		action = "test"
	}
	if !xcodeActions[action] {
		return nil, fmt.Errorf("action %q: use test, build, build-for-testing, test-without-building or clean", action)
	}
	if in.Project != "" && in.Workspace != "" {
		return nil, errors.New("name a project or a workspace, not both")
	}
	var args []string
	for flag, p := range map[string]string{"-project": in.Project, "-workspace": in.Workspace} {
		if p == "" {
			continue
		}
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, p)
		}
		abs = filepath.Clean(abs)
		if rel, err := filepath.Rel(root, abs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s is outside the project directory %s", p, root)
		}
		args = append(args, flag, abs)
	}
	for _, f := range [][2]string{{"-scheme", in.Scheme}, {"-destination", in.Destination}, {"-configuration", in.Configuration}} {
		if v := strings.TrimSpace(f[1]); v != "" {
			args = append(args, f[0], v)
		}
	}
	for _, t := range in.OnlyTesting {
		if t = strings.TrimSpace(t); t != "" {
			args = append(args, "-only-testing:"+t)
		}
	}
	for _, t := range in.SkipTesting {
		if t = strings.TrimSpace(t); t != "" {
			args = append(args, "-skip-testing:"+t)
		}
	}
	return append(args, action), nil
}

// xcodeKeep are the lines of xcodebuild's output a model needs: what failed
// and the verdict. A test that passed is only counted.
var (
	xcodeKeep   = regexp.MustCompile(`error:|Test [Cc]ase .* failed|Executed \d+ test|\*\* .* (SUCCEEDED|FAILED) \*\*|Testing failed|failed with|XCTAssert|Issue recorded|✘|Test run with`)
	xcodePassed = regexp.MustCompile(`Test [Cc]ase .* passed|✔ Test`)
)

// xcodeDigest is what goes back to codex: the exit, the full log's path, the
// lines that matter, and the end of the log.
func xcodeDigest(cmd string, exit int, log string, out []byte) string {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	var keep []string
	passed := 0
	for _, l := range lines {
		switch {
		case xcodePassed.MatchString(l):
			passed++
		case xcodeKeep.MatchString(l):
			keep = append(keep, l)
		}
	}
	const maxKeep, tail = 300, 40
	if len(keep) > maxKeep {
		keep = append([]string{fmt.Sprintf("… %d lines before", len(keep)-maxKeep)}, keep[len(keep)-maxKeep:]...)
	}
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s\nexit %d\ntests passed: %d\nfull log: %s\n", cmd, exit, passed, log)
	if len(keep) > 0 {
		fmt.Fprintf(&b, "\n--- results and errors ---\n%s\n", strings.Join(keep, "\n"))
	}
	fmt.Fprintf(&b, "\n--- last lines ---\n%s\n", strings.Join(lines, "\n"))
	return b.String()
}

// runXcode runs name args in dir, the log kept in a file codex can read.
func runXcode(ctx context.Context, runID, dir, name string, args ...string) (string, error) {
	logDir := filepath.Join(os.TempDir(), "superleo-xcode")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", err
	}
	log := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", runID, time.Now().Format("150405")))
	ctx, cancel := context.WithTimeout(ctx, xcodeWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(log, out, 0o644)
	exit := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		exit = ee.ExitCode()
	case err != nil:
		return "", err
	}
	if ctx.Err() == context.DeadlineExceeded {
		out = fmt.Appendf(out, "\nstopped after %s\n", xcodeWait)
	}
	return xcodeDigest(name+" "+strings.Join(args, " "), exit, log, out), nil
}

// cliXcodeHandler is the Xcode server for one run, in the project its
// directory is in.
func (a *App) cliXcodeHandler(runID string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: xcodeServer, Version: "1"}, nil)
	root := func() (string, error) {
		st := a.cliStore()
		st.mu.Lock()
		run := st.runs[runID]
		st.mu.Unlock()
		if run == nil || run.State != "running" {
			return "", errors.New("this run is no longer active")
		}
		r := xcodeRoot(run.Cwd)
		if r == "" {
			return "", fmt.Errorf("%s is not in an Xcode project", run.Cwd)
		}
		return r, nil
	}
	text := func(s string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
	}
	fail := func(err error) *mcp.CallToolResult {
		r := text(err.Error())
		r.IsError = true
		return r
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "xcodebuild",
		Description: "Runs xcodebuild (test by default) in this Xcode project outside your sandbox, where the iOS Simulator " +
			"(CoreSimulator) and Swift macro plugins work, and returns the results, the errors and the log's last lines. " +
			"Use it instead of running xcodebuild or swift test in your shell, where they fail.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in xcodeBuildIn) (*mcp.CallToolResult, any, error) {
		dir, err := root()
		if err != nil {
			return fail(err), nil, nil
		}
		args, err := xcodebuildArgs(dir, in)
		if err != nil {
			return fail(err), nil, nil
		}
		xcodeMu.Lock()
		defer xcodeMu.Unlock()
		a.xcodeNote(runID, "xcodebuild "+strings.Join(args, " "))
		out, err := runXcode(ctx, runID, dir, "xcodebuild", args...)
		if err != nil {
			return fail(err), nil, nil
		}
		return text(out), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "simulators",
		Description: "Lists the iOS simulators available on this Mac (xcrun simctl list devices available), to pick an xcodebuild destination.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if _, err := root(); err != nil {
			return fail(err), nil, nil
		}
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "devices", "available").CombinedOutput()
		if err != nil {
			return fail(fmt.Errorf("simctl: %v\n%s", err, out)), nil, nil
		}
		return text(string(out)), nil, nil
	})
	return s
}

// xcodeNote shows the run's watchers what SuperLeo runs for it.
func (a *App) xcodeNote(runID, what string) {
	st := a.cliStore()
	st.mu.Lock()
	run := st.runs[runID]
	st.mu.Unlock()
	if run != nil {
		a.addCLIEvent(run, CLIRunEvent{Kind: "note", Tool: xcodeServer, Detail: what, Text: "running outside the sandbox"})
	}
}
