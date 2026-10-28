package app

// The node's look at the coding CLIs running on its machine (cli_activity.go
// says why). Every few seconds, while it is linked to a core, it lists the
// processes (one ps), picks out the known CLIs by what they run, finds each
// one's working directory (lsof on a Mac, /proc on Linux; once per process),
// and calls one working when it, with everything it started, used more than
// a little CPU since the last look. That is all a process can say: no
// prompts, no files, only that it is busy, where, and since when.
//
// A CLI started by this SuperLeo (a coding run) is left out: it is a child of
// this process, and it already reports as a run.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// cliPollEvery is how often the processes are looked at.
	cliPollEvery = 3 * time.Second
	// cliCPUWorking is the CPU, in percent of one core, a CLI with its
	// children must use between two looks to count as working. An idle TUI
	// redrawing its spinner or clock stays well under it.
	cliCPUWorking = 5.0
	// cliWorkHold is how long a CLI stays working after its last busy look:
	// the pause between a tool and the next model call is not idleness.
	cliWorkHold = 9 * time.Second
	// maxProcSessions bounds one CLI's process sessions.
	maxProcSessions = 16
)

// procCLIs are the CLIs looked for, by the names their programs go by.
var procCLIs = map[string]string{
	"agy": "agy", "claude": "claude", "codex": "codex", "copilot": "copilot",
	"cursor-agent": "cursor-agent", "goose": "goose",
	"hermes": "hermes", "kimi": "kimi", "opencode": "opencode", "pi": "pi",
	"openclaw": "openclaw", "openclaw-gateway": "openclaw",
}

// procCLIPaths name a CLI by a piece of its script's path, for one started
// as `node …/dist/cli.js` that did not set its title.
var procCLIPaths = []struct{ piece, cli string }{
	{"/pi-coding-agent/", "pi"},
	{"/@openai/codex/", "codex"},
	{"/@github/copilot/", "copilot"},
	{"/opencode-ai/", "opencode"},
	{"/@anthropic-ai/claude-code/", "claude"},
	{"/hermes-agent/", "hermes"},
	{"/kimi-cli/", "kimi"},
	{"/openclaw/", "openclaw"},
}

// procNotSessions are a CLI's subcommands that are servers, not a session
// someone is working in.
var procNotSessions = map[string]map[string]bool{
	"codex":    {"app-server": true, "mcp-server": true},
	"opencode": {"serve": true, "web": true},
	"claude":   {"mcp": true},
}

// knownProcCLI says whether the process look would see cli.
func knownProcCLI(cli string) bool {
	for _, c := range procCLIs {
		if c == cli {
			return true
		}
	}
	return false
}

// piPath says whether a program called pi is the pi coding agent: "pi" is a
// short name, so only when it set that title itself (it does), or lives
// where the agent installs: its package, or a bin directory's pi.
func piPath(p string) bool {
	return p == "pi" || strings.Contains(p, "/pi-coding-agent/") || strings.HasSuffix(p, "/bin/pi")
}

// isInterpreter says whether a program runs a script named after it.
func isInterpreter(base string) bool {
	switch strings.ToLower(base) {
	case "node", "nodejs", "bun", "deno", "python", "python3", "pypy3":
		return true
	}
	b := strings.ToLower(base)
	return strings.HasPrefix(b, "python3.") || strings.HasPrefix(b, "python2")
}

// scriptBase is a program's name without its directory and script suffix.
func scriptBase(p string) string {
	b := filepath.Base(p)
	for _, ext := range []string{".js", ".mjs", ".cjs", ".ts", ".py", ".exe"} {
		b = strings.TrimSuffix(b, ext)
	}
	return b
}

// matchCLI says which CLI a command line runs, if any. args is ps's: the
// program and its arguments, split on spaces.
func matchCLI(args string) (string, bool) {
	f := strings.Fields(args)
	if len(f) == 0 {
		return "", false
	}
	cli, rest := "", f[1:]
	argv0 := f[0]
	base := scriptBase(argv0)
	switch {
	case procCLIs[base] != "":
		cli = procCLIs[base]
		if cli == "pi" && !piPath(argv0) {
			return "", false
		}
	case isInterpreter(base):
		i := 0
		for i < len(rest) && strings.HasPrefix(rest[i], "-") {
			i++
		}
		if i == len(rest) {
			return "", false
		}
		script := rest[i]
		rest = rest[i+1:]
		cli = procCLIs[scriptBase(script)]
		for _, h := range procCLIPaths {
			if cli == "" && strings.Contains(script, h.piece) {
				cli = h.cli
			}
		}
		if cli == "pi" && !piPath(script) {
			return "", false
		}
	}
	if cli == "" {
		return "", false
	}
	if len(rest) > 0 && procNotSessions[cli][rest[0]] {
		return "", false
	}
	return cli, true
}

// procRow is one line of ps.
type procRow struct {
	PID, PPID int
	CPU       float64       // ps's own recent CPU, percent
	CPUTime   time.Duration // all the CPU it has used
	Elapsed   time.Duration // since it started
	Args      string
}

// psArgs is the one ps call: every process, its parent, CPU and command.
var psArgs = []string{"-A", "-o", "pid=,ppid=,pcpu=,etime=,time=,args="}

// parseClock reads ps's times: [dd-][hh:]mm:ss[.ss].
func parseClock(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var days int
	if i := strings.IndexByte(s, '-'); i > 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0, false
		}
		days, s = d, s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	var total float64
	mul := 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		v, err := strconv.ParseFloat(parts[i], 64)
		if err != nil || v < 0 {
			return 0, false
		}
		total += v * mul
		mul *= 60
	}
	return time.Duration((total + float64(days)*86400) * float64(time.Second)), true
}

// parsePS reads ps's lines; malformed ones are skipped.
func parsePS(out []byte) []procRow {
	var rows []procRow
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		cpu, e3 := strconv.ParseFloat(strings.ReplaceAll(f[2], ",", "."), 64)
		el, ok2 := parseClock(f[3])
		ct, ok1 := parseClock(strings.ReplaceAll(f[4], ",", "."))
		if e1 != nil || e2 != nil || e3 != nil || !ok1 || !ok2 {
			continue
		}
		rows = append(rows, procRow{PID: pid, PPID: ppid, CPU: cpu, CPUTime: ct, Elapsed: el, Args: strings.Join(f[5:], " ")})
	}
	return rows
}

// procSeen is what the poller keeps about one CLI process between looks.
type procSeen struct {
	cli        string
	started    time.Time
	cwd        string
	cpuTime    time.Duration // its tree's, at the last look
	at         time.Time
	lastBusy   time.Time
	state      string
	since      time.Time
	cwdChecked bool
}

// procPoller turns ps output into sessions, remembering each process.
type procPoller struct {
	self  int
	seen  map[int]*procSeen
	cwdOf func(pids []int) map[int]string
}

func newProcPoller(self int) *procPoller {
	return &procPoller{self: self, seen: map[int]*procSeen{}, cwdOf: procCwds}
}

// look takes one ps listing at now and returns every CLI's sessions.
func (p *procPoller) look(rows []procRow, now time.Time) map[string][]CCActivitySession {
	byPID := make(map[int]*procRow, len(rows))
	kids := map[int][]int{}
	for i := range rows {
		r := &rows[i]
		byPID[r.PID] = r
		kids[r.PPID] = append(kids[r.PPID], r.PID)
	}
	match := map[int]string{}
	for _, r := range rows {
		if cli, ok := matchCLI(r.Args); ok {
			match[r.PID] = cli
		}
	}
	// ancestors walks up from pid; f says stop.
	ancestors := func(pid int, f func(int) bool) {
		for i, cur := 0, byPID[pid]; cur != nil && i < 64; i++ {
			if cur.PPID == cur.PID || f(cur.PPID) {
				return
			}
			cur = byPID[cur.PPID]
		}
	}
	var roots []int
	for pid, cli := range match {
		skip := pid == p.self && p.self > 1
		ancestors(pid, func(up int) bool {
			if (up == p.self && p.self > 1) || match[up] == cli {
				skip = true // our own run, or a part of a CLI already counted
				return true
			}
			return false
		})
		if !skip {
			roots = append(roots, pid)
		}
	}
	sort.Ints(roots)
	// tree is a root's CPU time and ps CPU with everything under it.
	tree := func(pid int) (time.Duration, float64) {
		var ct time.Duration
		var cpu float64
		stack := []int{pid}
		for n := 0; len(stack) > 0 && n < 4096; n++ {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if r := byPID[cur]; r != nil {
				ct += r.CPUTime
				cpu += r.CPU
			}
			stack = append(stack, kids[cur]...)
		}
		return ct, cpu
	}
	live := map[int]bool{}
	var need []int
	for _, pid := range roots {
		r := byPID[pid]
		started := now.Add(-r.Elapsed).Truncate(time.Second)
		s := p.seen[pid]
		// A pid used again by another process is a new one.
		if s != nil && (s.cli != match[pid] || absDur(s.started.Sub(started)) > 5*time.Second) {
			s = nil
		}
		ct, cpu := tree(pid)
		if s == nil {
			s = &procSeen{cli: match[pid], started: started, state: actIdle, since: now}
			p.seen[pid] = s
			if cpu >= cliCPUWorking {
				s.lastBusy = now
			}
		} else if wall := now.Sub(s.at); wall > 0 {
			if pct := float64(ct-s.cpuTime) / float64(wall) * 100; pct >= cliCPUWorking {
				s.lastBusy = now
			}
		}
		s.cpuTime, s.at = ct, now
		state := actIdle
		if !s.lastBusy.IsZero() && now.Sub(s.lastBusy) < cliWorkHold {
			state = actWorking
		}
		if state != s.state {
			s.state, s.since = state, now
		}
		if !s.cwdChecked {
			need = append(need, pid)
		}
		live[pid] = true
	}
	if len(need) > 0 && p.cwdOf != nil {
		cwds := p.cwdOf(need)
		for _, pid := range need {
			p.seen[pid].cwd, p.seen[pid].cwdChecked = cwds[pid], true
		}
	}
	for pid := range p.seen {
		if !live[pid] {
			delete(p.seen, pid)
		}
	}
	out := map[string][]CCActivitySession{}
	for _, pid := range roots {
		s := p.seen[pid]
		if len(out[s.cli]) >= maxProcSessions {
			continue
		}
		last := s.lastBusy
		if last.IsZero() {
			last = s.started
		}
		out[s.cli] = append(out[s.cli], CCActivitySession{
			ID: fmt.Sprintf("%s-%d", s.cli, pid), Project: clipField(projectName(s.cwd)), State: s.state,
			Since: s.since, Started: s.started, Last: last,
		})
	}
	return out
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// procCwds finds processes' working directories: /proc on Linux, one lsof
// for all of them elsewhere.
func procCwds(pids []int) map[int]string {
	out := map[int]string{}
	if runtime.GOOS == "linux" {
		for _, pid := range pids {
			if p, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
				out[pid] = p
			}
		}
		return out
	}
	list := make([]string, len(pids))
	for i, p := range pids {
		list[i] = strconv.Itoa(p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, _ := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-Fn", "-p", strings.Join(list, ",")).Output()
	return parseLsofCwd(b)
}

// parseLsofCwd reads `lsof -Fn -d cwd`: p<pid> then n<path> lines.
func parseLsofCwd(b []byte) map[int]string {
	out := map[int]string{}
	pid := 0
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid > 0 {
				out[pid] = line[1:]
			}
		}
	}
	return out
}

// cliPollOn says whether to look at processes: not with SUPERLEO_CLI_POLL=0,
// and not in a test binary unless the test asks (it would see this machine's
// real CLIs).
var cliPollOn = func() bool {
	return os.Getenv("SUPERLEO_CLI_POLL") != "0" && !strings.HasSuffix(os.Args[0], ".test")
}

// runPS is the ps call.
func runPS(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "ps", psArgs...).Output()
}

// cliPollConfig is a test's own process look: how often, and its ps.
type cliPollConfig struct {
	every time.Duration
	ps    func(context.Context) ([]byte, error)
}

// pollCLIProcs looks at the processes every cliPollEvery while this SuperLeo
// is linked to a core, until ctx ends. Off with SUPERLEO_CLI_POLL=0.
func (a *App) pollCLIProcs(ctx context.Context, t *cliTracker) {
	every, ps := cliPollEvery, runPS
	if c := a.cliPoll; c != nil {
		every, ps = c.every, c.ps // a test's
	} else if !cliPollOn() {
		return
	}
	p := newProcPoller(os.Getpid())
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if a.agentTap.Load() == nil {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		out, err := ps(pctx)
		cancel()
		if err != nil {
			continue
		}
		rows := parsePS(out)
		alive := make(map[int]bool, len(rows))
		for _, r := range rows {
			alive[r.PID] = true
		}
		t.setProcsAlive(p.look(rows, time.Now()), alive)
		for _, c := range t.clis() {
			t.kick(c) // a snapshot that says nothing new is not sent
		}
	}
}
