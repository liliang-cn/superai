package app

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// psMac is `ps -A -o pid=,ppid=,pcpu=,etime=,time=,args=` on the Mac, as it
// looked with claude, codex, pi and opencode running (home directory and
// session ids changed).
const psMac = `
84915 84106   0.3 01-01:29:46  16:24.42 claude --resume 00000000-0000-0000-0000-000000000001
 7509  6984  18.1 01-00:34:18  43:47.32 claude --permission-mode bypassPermissions --chrome -c
11332 11098   0.0       04:02   0:00.05 node /Users/me/.local/bin/codex --dangerously-bypass-approvals-and-sandbox
11337 11332   0.0       04:02   0:01.21 /Users/me/.local/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex --dangerously-bypass-approvals-and-sandbox
11343 11337   0.0       04:02   0:03.60 /Users/me/.codex/packages/app-server-daemon/releases/0.160.0-aarch64-apple-darwin/bin/codex app-server --listen unix:// --managed-daemon
11396 11337   0.0       04:02   0:00.30 /Users/me/.codex/packages/app-server-daemon/releases/0.160.0-aarch64-apple-darwin/bin/codex app-server daemon pid-update-loop
11739 11343   0.0       03:53   0:00.62 node ./plugin/launch.mjs
17291 14295   0.0       02:03   0:01.32 pi
69169 68884   0.3    02:10:06   3:53.82 opencode
80584     1   0.1 01-01:51:54  13:22.14 /Applications/Claude.app/Contents/MacOS/Claude
52044 80584   0.0    02:27:56   0:03.00 /Applications/Claude.app/Contents/Frameworks/Claude Helper (Renderer).app/Contents/MacOS/Claude Helper (Renderer) --type=renderer
55375 55359   0.0    02:25:04   0:00.08 /opt/homebrew/opt/node/bin/node /opt/homebrew/bin/agent-browser mcp
12134 11742   0.0       03:48   0:00.37 node /tmp/.npm/_npx/9833c18b2d85bc59/node_modules/.bin/playwright-mcp
garbage line
`

func TestPSLinesParse(t *testing.T) {
	rows := parsePS([]byte(psMac))
	if len(rows) != 13 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[1]
	if r.PID != 7509 || r.PPID != 6984 || r.CPU != 18.1 || r.CPUTime != 43*time.Minute+47320*time.Millisecond ||
		r.Elapsed != 24*time.Hour+34*time.Minute+18*time.Second || r.Args != "claude --permission-mode bypassPermissions --chrome -c" {
		t.Fatalf("%+v", r)
	}
	// Linux's procps: hh:mm:ss CPU time, days on both.
	lin := parsePS([]byte("  812     1  0.5 2-01:02:03 1-00:01:02 node /usr/lib/node_modules/openclaw/dist/index.js gateway\n"))
	if len(lin) != 1 || lin[0].CPUTime != 24*time.Hour+62*time.Second || lin[0].Elapsed != 49*time.Hour+2*time.Minute+3*time.Second {
		t.Fatalf("linux %+v", lin)
	}
}

func TestCLIProcessesAreKnownByWhatTheyRun(t *testing.T) {
	for _, c := range []struct {
		args, cli string
	}{
		{"claude --resume x", "claude"},
		{"/Users/me/.local/bin/claude", "claude"},
		{"node /Users/me/.local/bin/codex --yolo", "codex"},
		{"/Users/me/.local/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex --x", "codex"},
		{"pi ", "pi"},                          // pi sets its own title
		{"node /Users/me/.local/bin/pi", "pi"}, // before it does
		{"node /Users/me/.local/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js", "pi"},
		{"opencode", "opencode"},
		{"bun /Users/me/.opencode/bin/opencode", "opencode"},
		{"/Users/me/.opencode/bin/opencode run hi", "opencode"},
		{"/Users/me/.hermes/hermes-agent/venv/bin/python /Users/me/.hermes/hermes-agent/hermes chat", "hermes"},
		{"/opt/homebrew/Cellar/python@3.14/3.14.7/Frameworks/Python.framework/Versions/3.14/Resources/Python.app/Contents/MacOS/Python /Users/me/.hermes/hermes-agent/hermes", "hermes"},
		{"/Users/me/.local/share/uv/tools/kimi-cli/bin/python /Users/me/.local/bin/kimi", "kimi"},
		{"/Users/me/.local/bin/cursor-agent --use-system-ca /Users/me/.local/share/cursor-agent/versions/2026/index.js", "cursor-agent"},
		// gemini is gone (agy took its place): its old installs are not a CLI.
		{"node /Users/me/.local/bin/gemini", ""},
		{"/Users/me/.local/bin/goose session", "goose"},
		{"/Users/me/.local/bin/agy", "agy"},
		{"/opt/homebrew/bin/copilot", "copilot"},
		{"node /usr/lib/node_modules/openclaw/dist/index.js gateway", "openclaw"},
		{"openclaw-gateway", "openclaw"},

		// Not a session, or not a CLI at all.
		{"/Users/me/.codex/packages/app-server-daemon/releases/0.160.0/bin/codex app-server --listen unix://", ""},
		{"opencode serve --port 4096", ""},
		{"/Applications/Claude.app/Contents/MacOS/Claude", ""},
		{"/opt/homebrew/opt/node/bin/node /opt/homebrew/bin/agent-browser mcp", ""},
		{"node ./plugin/launch.mjs", ""},
		{"/opt/vendor/tools/pi --serve", ""}, // some other program called pi
		{"python3 /home/me/scripts/pi.py", ""},
		{"node", ""},
		{"", ""},
	} {
		got, ok := matchCLI(c.args)
		if got != c.cli || ok != (c.cli != "") {
			t.Errorf("%q: %q %v, want %q", c.args, got, ok, c.cli)
		}
	}
}

func TestTheMacsProcessesBecomeSessions(t *testing.T) {
	p := newProcPoller(99999)
	asked := [][]int{}
	p.cwdOf = func(pids []int) map[int]string {
		asked = append(asked, append([]int{}, pids...))
		return map[int]string{7509: "/Users/me/code/superai", 11332: "/Users/me/code/noted", 17291: "/Users/me/code/pets/", 69169: "/Users/me"}
	}
	now := time.Date(2026, 10, 17, 20, 0, 0, 0, time.Local)
	out := p.look(parsePS([]byte(psMac)), now)
	count := map[string]int{}
	for cli, ss := range out {
		count[cli] = len(ss)
	}
	if count["claude"] != 2 || count["codex"] != 1 || count["pi"] != 1 || count["opencode"] != 1 || len(count) != 4 {
		t.Fatalf("sessions %v", count)
	}
	// codex is its node wrapper, once: the native binary and its app server
	// under it are the same session.
	if s := out["codex"][0]; s.ID != "codex-11332" || s.Project != "noted" || s.State != actIdle {
		t.Fatalf("codex %+v", s)
	}
	if s := out["pi"][0]; s.Project != "pets" || !s.Started.Equal(now.Add(-2*time.Minute-3*time.Second)) {
		t.Fatalf("pi %+v", s)
	}
	// ps said 18% for the busy claude: working from the first look.
	var busy CCActivitySession
	for _, s := range out["claude"] {
		if s.ID == "claude-7509" {
			busy = s
		}
	}
	if busy.State != actWorking || busy.Project != "superai" {
		t.Fatalf("busy claude %+v", busy)
	}
	if len(asked) != 1 || len(asked[0]) != 5 {
		t.Fatalf("cwd asked %v", asked)
	}
	// The second look asks for no directory again.
	p.look(parsePS([]byte(psMac)), now.Add(3*time.Second))
	if len(asked) != 1 {
		t.Fatalf("cwd asked again %v", asked)
	}
}

// procLines is a ps listing of one opencode with a child, s seconds after it
// started, at the given
// CPU times.
func procLines(s int, ocCPU, childCPU string) []byte {
	et := fmt.Sprintf("%02d:%02d", s/60, s%60)
	return []byte("  500     1   0.0 " + et + "   " + ocCPU + " opencode\n  501   500   0.0 00:01   " + childCPU + " /bin/zsh -c go test ./...\n")
}

func TestWorkingIsCPUSinceTheLastLook(t *testing.T) {
	p := newProcPoller(77777)
	p.cwdOf = func([]int) map[int]string { return nil }
	t0 := time.Now()
	state := func(out map[string][]CCActivitySession) string {
		if len(out["opencode"]) != 1 {
			t.Fatalf("%+v", out)
		}
		return out["opencode"][0].State
	}
	if s := state(p.look(parsePS(procLines(60, "0:10.00", "0:00.00")), t0)); s != actIdle {
		t.Fatalf("first look %s", s)
	}
	// 0.05s in 3s: under 5%.
	if s := state(p.look(parsePS(procLines(63, "0:10.05", "0:00.00")), t0.Add(3*time.Second))); s != actIdle {
		t.Fatalf("quiet %s", s)
	}
	// Its child (a build it ran) used 1.5s in 3s: the CLI is working.
	out := p.look(parsePS(procLines(66, "0:10.05", "0:01.50")), t0.Add(6*time.Second))
	if s := state(out); s != actWorking {
		t.Fatalf("busy child %s", s)
	}
	since := out["opencode"][0].Since
	// A quiet look inside the hold keeps it working, since the same time.
	out = p.look(parsePS(procLines(69, "0:10.06", "0:01.50")), t0.Add(9*time.Second))
	if s := state(out); s != actWorking || !out["opencode"][0].Since.Equal(since) {
		t.Fatalf("hold %s %v", s, out["opencode"][0].Since)
	}
	// Past the hold: idle.
	if s := state(p.look(parsePS(procLines(76, "0:10.06", "0:01.50")), t0.Add(16*time.Second))); s != actIdle {
		t.Fatalf("after hold %s", s)
	}
}

func TestOwnRunsAndReusedPIDsAreNotSessions(t *testing.T) {
	p := newProcPoller(4000)
	p.cwdOf = func([]int) map[int]string { return nil }
	// This node (4000) started a coding run through a shell.
	ps := "4000     1  1.0 05:00 0:30.00 /Users/me/.superai-node/bin/superai agent\n" +
		"4001  4000  0.0 01:00 0:00.01 /bin/sh -c opencode run\n" +
		"4002  4001 50.0 01:00 0:20.00 opencode run do-it\n" +
		"5000     1  0.0 01:00 0:00.10 gemini\n"
	out := p.look(parsePS([]byte(ps)), time.Now())
	if len(out["opencode"]) != 0 || len(out["gemini"]) != 1 {
		t.Fatalf("%+v", out)
	}
	// The pid comes back as another program: a new session from then.
	t0 := out["gemini"][0].Started
	out = p.look(parsePS([]byte("5000     1  0.0 00:05 0:00.10 goose\n")), time.Now())
	if len(out["goose"]) != 1 || len(out["gemini"]) != 0 || out["goose"][0].Started.Equal(t0) {
		t.Fatalf("reused %+v", out)
	}
	if len(p.seen) != 1 {
		t.Fatalf("kept %d", len(p.seen))
	}
}

func TestLsofCwdParses(t *testing.T) {
	got := parseLsofCwd([]byte("p11332\nfcwd\nn/Users/me/code/noted\np17291\nfcwd\nn/Users/me/code/with space\n"))
	if got[11332] != "/Users/me/code/noted" || got[17291] != "/Users/me/code/with space" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

func TestEveryOfferedCLIIsLookedFor(t *testing.T) {
	want := []string{"agy", "claude", "codex", "copilot", "cursor-agent", "goose", "hermes", "kimi", "opencode", "pi"}
	var miss []string
	for _, c := range want {
		if !knownProcCLI(c) {
			miss = append(miss, c)
		}
	}
	sort.Strings(miss)
	if len(miss) > 0 {
		t.Fatalf("not looked for: %s", strings.Join(miss, ", "))
	}
}
