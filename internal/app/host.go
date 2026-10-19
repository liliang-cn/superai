package app

// The machine this process runs on, as something core can look after: how it
// is (load, memory, disks, uptime), what services it runs, their logs, and —
// unless the machine's owner turned it off — restarting a service or running
// a command. Core reaches these over the agent link (agentlink_agent.go
// lists them as callable); machines.go is core's side, and the same methods
// answer for core's own machine.
//
// Nothing here goes through a shell except HostExec, whose whole point is
// one; a unit name or a log path is an argument, never part of a command line.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/sandbox"
)

// HostInfo is how a machine is doing right now.
type HostInfo struct {
	Name    string     `json:"name"`
	Host    string     `json:"host"`
	OS      string     `json:"os"`
	Arch    string     `json:"arch"`
	Kernel  string     `json:"kernel,omitempty"`
	Uptime  int64      `json:"uptimeSeconds"`
	CPUs    int        `json:"cpus"`
	Load    []float64  `json:"load"`
	Mem     HostMemory `json:"memory"`
	Disks   []HostDisk `json:"disks"`
	Version string     `json:"version,omitempty"`
	// Control says whether this machine accepts service actions and commands.
	Control bool `json:"control"`
	// Claude is what the machine's interactive Claude Code sessions are
	// doing, as core last heard (cchook_activity.go); core fills it in.
	Claude *CCActivity `json:"claude,omitempty"`
	// CLIActivity is every coding CLI running on it, by CLI, as core last
	// heard (cli_activity.go); core fills it in.
	CLIActivity map[string]CLIActivity `json:"cliActivity,omitempty"`
}

type HostMemory struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

type HostDisk struct {
	Mount string `json:"mount"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

// HostService is one service the machine's service manager knows.
type HostService struct {
	Name   string `json:"name"`
	State  string `json:"state"` // running, failed, stopped, ...
	About  string `json:"about,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Status int    `json:"lastExit,omitempty"`
}

// HostExecResult is what a command did.
type HostExecResult struct {
	Exit     int    `json:"exit"`
	Output   string `json:"output"`
	Cut      bool   `json:"truncated,omitempty"`
	Duration int64  `json:"ms"`
}

// hostControl says whether this machine takes orders that change it. On by
// default, since core can already run Claude Code here; the owner turns it
// off with SUPERAI_HOST_CONTROL=off in the service's environment.
func hostControl() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUPERAI_HOST_CONTROL"))) {
	case "0", "off", "false", "no":
		return false
	}
	return true
}

var errNoHostControl = errors.New("this machine does not take service actions or commands (SUPERAI_HOST_CONTROL is off)")

const hostOutputCap = 64 << 10

// run runs argv with a deadline and returns its combined output, cut to the
// cap from the front (the end of a log or an error is the part worth having).
func hostRun(ctx context.Context, d time.Duration, argv ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	out := buf.String()
	if len(out) > hostOutputCap {
		out = out[len(out)-hostOutputCap:]
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code, err = ee.ExitCode(), nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("%s did not finish within %s", argv[0], d)
	}
	return out, code, err
}

// HostStatus is this machine's load, memory, disks and uptime.
func (a *App) HostStatus() (HostInfo, error) {
	host, _ := os.Hostname()
	h := HostInfo{Name: a.hostName(), Host: host, OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPUs: runtime.NumCPU(), Version: buildVersion(), Control: hostControl(), Load: []float64{}, Disks: []HostDisk{}}
	ctx := context.Background()
	if out, _, err := hostRun(ctx, 5*time.Second, "uname", "-r"); err == nil {
		h.Kernel = strings.TrimSpace(out)
	}
	switch runtime.GOOS {
	case "linux":
		hostLinux(&h)
	case "darwin":
		hostDarwin(ctx, &h)
	}
	h.Disks = hostDisks(ctx)
	return h, nil
}

// hostName is the name core knows this machine by, when it is linked.
func (a *App) hostName() string {
	if n := strings.TrimSpace(os.Getenv("SUPERAI_AGENT_NAME")); n != "" {
		return n
	}
	h, _ := os.Hostname()
	return strings.Split(h, ".")[0]
}

func hostLinux(h *HostInfo) {
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		h.Load = parseLoad(strings.Fields(string(b)))
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			v, _ := strconv.ParseFloat(f[0], 64)
			h.Uptime = int64(v)
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		kb := map[string]uint64{}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) >= 2 {
				v, _ := strconv.ParseUint(f[1], 10, 64)
				kb[strings.TrimSuffix(f[0], ":")] = v
			}
		}
		h.Mem.Total = kb["MemTotal"] * 1024
		if avail, ok := kb["MemAvailable"]; ok && kb["MemTotal"] >= avail {
			h.Mem.Used = (kb["MemTotal"] - avail) * 1024
		}
	}
}

func hostDarwin(ctx context.Context, h *HostInfo) {
	if out, _, err := hostRun(ctx, 5*time.Second, "sysctl", "-n", "vm.loadavg"); err == nil {
		h.Load = parseLoad(strings.Fields(strings.Trim(strings.TrimSpace(out), "{}")))
	}
	if out, _, err := hostRun(ctx, 5*time.Second, "sysctl", "-n", "hw.memsize"); err == nil {
		h.Mem.Total, _ = strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	}
	// kern.boottime: "{ sec = 1760000000, usec = 0 } ..."
	if out, _, err := hostRun(ctx, 5*time.Second, "sysctl", "-n", "kern.boottime"); err == nil {
		if m := regexp.MustCompile(`sec = (\d+)`).FindStringSubmatch(out); m != nil {
			sec, _ := strconv.ParseInt(m[1], 10, 64)
			h.Uptime = time.Now().Unix() - sec
		}
	}
	// Used is what Activity Monitor calls it: app memory, wired and
	// compressed, which is total minus what could be handed out now.
	if out, _, err := hostRun(ctx, 5*time.Second, "vm_stat"); err == nil {
		page := uint64(4096)
		if m := regexp.MustCompile(`page size of (\d+) bytes`).FindStringSubmatch(out); m != nil {
			page, _ = strconv.ParseUint(m[1], 10, 64)
		}
		pages := map[string]uint64{}
		for _, line := range strings.Split(out, "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), "."), 10, 64)
			if err == nil {
				pages[strings.TrimSpace(k)] = n
			}
		}
		free := (pages["Pages free"] + pages["Pages inactive"] + pages["Pages speculative"] + pages["Pages purgeable"]) * page
		if h.Mem.Total > free {
			h.Mem.Used = h.Mem.Total - free
		}
	}
}

func parseLoad(f []string) []float64 {
	out := []float64{}
	for i := 0; i < len(f) && i < 3; i++ {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			break
		}
		out = append(out, v)
	}
	return out
}

// hostDisks is every mounted disk that is a real one: df's lines whose source
// is a device, without the read-only system snapshots macOS mounts.
func hostDisks(ctx context.Context) []HostDisk {
	out, _, err := hostRun(ctx, 10*time.Second, "df", "-kP")
	if err != nil {
		return []HostDisk{}
	}
	return parseDF(out, runtime.GOOS)
}

func parseDF(out, goos string) []HostDisk {
	disks := []HostDisk{}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 6 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		mount := strings.Join(f[5:], " ")
		if goos == "darwin" && mount != "/" && mount != "/System/Volumes/Data" &&
			(!strings.HasPrefix(mount, "/Volumes/") || mount == "/Volumes/Recovery") {
			continue
		}
		if strings.HasPrefix(mount, "/snap/") || strings.HasPrefix(mount, "/boot") {
			continue
		}
		total, _ := strconv.ParseUint(f[1], 10, 64)
		used, _ := strconv.ParseUint(f[2], 10, 64)
		if total == 0 || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		disks = append(disks, HostDisk{Mount: mount, Total: total * 1024, Used: used * 1024})
	}
	return disks
}

// unitName is what a service may be called: systemd's and launchd's names.
var unitName = regexp.MustCompile(`^[A-Za-z0-9@._:+-]{1,200}$`)

// HostServices lists the services this machine's service manager runs: the
// loaded systemd services on Linux, the launchd jobs outside Apple's own on a
// Mac.
func (a *App) HostServices() ([]HostService, error) {
	ctx := context.Background()
	switch runtime.GOOS {
	case "linux":
		out, _, err := hostRun(ctx, 15*time.Second, "systemctl", "list-units", "--type=service", "--all",
			"--no-legend", "--plain", "--no-pager")
		if err != nil {
			return nil, err
		}
		return parseSystemctl(out), nil
	case "darwin":
		out, _, err := hostRun(ctx, 15*time.Second, "launchctl", "list")
		if err != nil {
			return nil, err
		}
		return parseLaunchctl(out), nil
	}
	return nil, fmt.Errorf("services are not read on %s", runtime.GOOS)
}

func parseSystemctl(out string) []HostService {
	list := []HostService{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		// unit load active sub description...
		if len(f) < 4 || f[1] != "loaded" {
			continue
		}
		state := f[3]
		if f[2] == "failed" {
			state = "failed"
		} else if f[2] == "inactive" {
			// An inactive unit nobody asked about is noise: most are oneshots
			// that ran at boot. Keep only the ones that are something.
			continue
		}
		list = append(list, HostService{Name: strings.TrimSuffix(f[0], ".service"), State: state,
			About: strings.Join(f[4:], " ")})
	}
	sortServices(list)
	return list
}

func parseLaunchctl(out string) []HostService {
	list := []HostService{}
	for _, line := range strings.Split(out, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		label := f[2]
		if strings.HasPrefix(label, "com.apple.") || strings.HasPrefix(label, "application.") {
			continue
		}
		s := HostService{Name: label, State: "stopped"}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			s.PID, s.State = pid, "running"
		}
		if st, err := strconv.Atoi(f[1]); err == nil && st != 0 {
			s.Status = st
			if s.State != "running" {
				s.State = "failed"
			}
		}
		list = append(list, s)
	}
	sortServices(list)
	return list
}

// sortServices puts what needs looking at first: failed, then running.
func sortServices(list []HostService) {
	rank := func(s string) int {
		switch s {
		case "failed":
			return 0
		case "running":
			return 1
		}
		return 2
	}
	sort.SliceStable(list, func(i, j int) bool {
		if ri, rj := rank(list[i].State), rank(list[j].State); ri != rj {
			return ri < rj
		}
		return list[i].Name < list[j].Name
	})
}

// HostLogs is the last lines of a service's log, or of a log file given by
// its absolute path.
func (a *App) HostLogs(target string, lines int) (string, error) {
	target = strings.TrimSpace(target)
	if lines <= 0 {
		lines = 100
	}
	if lines > 2000 {
		lines = 2000
	}
	if strings.HasPrefix(target, "/") {
		return tailFile(target, lines)
	}
	if !unitName.MatchString(target) {
		return "", fmt.Errorf("%q is neither a service name nor an absolute path", target)
	}
	ctx := context.Background()
	switch runtime.GOOS {
	case "linux":
		out, _, err := hostRun(ctx, 20*time.Second, "journalctl", "-u", target, "-n", strconv.Itoa(lines),
			"--no-pager", "-o", "short-iso")
		return out, err
	case "darwin":
		// A launchd job logs where its plist says; read that file.
		for _, p := range launchdPlists(target) {
			for _, key := range []string{"StandardErrorPath", "StandardOutPath"} {
				out, code, err := hostRun(ctx, 5*time.Second, "plutil", "-extract", key, "raw", "-o", "-", p)
				if err == nil && code == 0 {
					if path := strings.TrimSpace(out); path != "" {
						return tailFile(path, lines)
					}
				}
			}
		}
		return "", fmt.Errorf("%s names no log file; give the log's path instead", target)
	}
	return "", fmt.Errorf("logs are not read on %s", runtime.GOOS)
}

func launchdPlists(label string) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, dir := range []string{home + "/Library/LaunchAgents", "/Library/LaunchAgents", "/Library/LaunchDaemons"} {
		p := dir + "/" + label + ".plist"
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// tailFile reads the last n lines of a file without reading all of a big one.
func tailFile(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	size := st.Size()
	from := size - hostOutputCap
	if from < 0 {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(f, hostOutputCap))
	if err != nil {
		return "", err
	}
	text := string(b)
	if from > 0 {
		// The first line is a piece of one; drop it.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	ls := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n"), nil
}

// HostService starts, stops or restarts a service.
func (a *App) HostService(name, action string) (string, error) {
	if !hostControl() {
		return "", errNoHostControl
	}
	name = strings.TrimSpace(name)
	if !unitName.MatchString(name) {
		return "", fmt.Errorf("%q is not a service name", name)
	}
	switch action {
	case "start", "stop", "restart":
	default:
		return "", fmt.Errorf("action is start, stop or restart, not %q", action)
	}
	ctx := context.Background()
	var argv []string
	switch runtime.GOOS {
	case "linux":
		argv = []string{"systemctl", action, name}
	case "darwin":
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), name)
		switch action {
		case "start":
			argv = []string{"launchctl", "kickstart", target}
		case "restart":
			argv = []string{"launchctl", "kickstart", "-k", target}
		case "stop":
			argv = []string{"launchctl", "kill", "SIGTERM", target}
		}
	default:
		return "", fmt.Errorf("services are not managed on %s", runtime.GOOS)
	}
	log.Printf("host: %s %s", action, name)
	out, code, err := hostRun(ctx, 60*time.Second, argv...)
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("%s %s failed (exit %d): %s", action, name, code, strings.TrimSpace(out))
	}
	done := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[action]
	return done + " " + name, nil
}

// HostExec runs a shell command on this machine and returns what it printed.
// The timeout is in seconds: a minute when zero, ten at most.
func (a *App) HostExec(command string, timeoutSeconds int) (HostExecResult, error) {
	if !hostControl() {
		return HostExecResult{}, errNoHostControl
	}
	if strings.TrimSpace(command) == "" {
		return HostExecResult{}, errors.New("no command")
	}
	d := time.Duration(timeoutSeconds) * time.Second
	if d <= 0 {
		d = time.Minute
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	// What was run is in the run that asked for it; the log keeps that
	// something was, without a command line that may carry a secret.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	name, argv := sandbox.HostShellCommand(command) // sh, or PowerShell on Windows
	cmd := exec.CommandContext(ctx, name, argv...)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	r := HostExecResult{Duration: time.Since(start).Milliseconds()}
	r.Output = buf.String()
	if len(r.Output) > hostOutputCap {
		r.Output, r.Cut = r.Output[len(r.Output)-hostOutputCap:], true
	}
	var ee *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		r.Exit = -1
		r.Output += fmt.Sprintf("\n(stopped after %s)", d)
	case errors.As(err, &ee):
		r.Exit = ee.ExitCode()
	case err != nil:
		return r, err
	}
	log.Printf("host: ran a command (%d bytes), exit %d in %dms", len(command), r.Exit, r.Duration)
	return r, nil
}
