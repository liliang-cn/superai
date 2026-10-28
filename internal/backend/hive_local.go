package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// A hive on one machine.
//
// The cluster's queen makes workers by asking Kubernetes for more pods
// (hive_spawn.go). A queen on a laptop has no cluster, but it has the same
// binary and an operating system that starts processes. So here a worker is
// `superleo serve` run as a child: bound to loopback on a port of its own, with
// a data directory of its own under <data>/hive/<name>, told where the queen
// is, and from then on a worker like any other — it announces itself, takes
// orders and delegated tools over the same protocol, and the queen's roster is
// still the truth about who is there.
//
// What this file adds over a StatefulSet is the part Kubernetes did for free:
// keeping the processes alive, and making sure none outlives the queen. A
// worker that crashes is started again after a growing pause; a queen that
// exits or crashes takes its workers with it, because each one exits when the
// pipe on its stdin closes, which the operating system does for a dead parent
// whatever killed it.

// SpawnerLocal is the spawner kind that runs workers as processes on this
// machine.
const SpawnerLocal = "local"

// LocalWorkerPrefix is what every local worker's name starts with, followed
// by its ordinal: local-0, local-1, …
const LocalWorkerPrefix = "local"

// Defaults for a local hive whose settings leave them out.
const (
	DefaultLocalMinWorkers  = 2
	DefaultLocalIdleMinutes = 10
)

// DefaultLocalMaxWorkers is the cap on one machine: a worker is a whole agent
// process, so more of them than cores buys waiting, not work.
func DefaultLocalMaxWorkers() int {
	n := runtime.NumCPU()
	if n > 4 {
		n = 4
	}
	if n < 2 {
		n = 2
	}
	return n
}

// ExitWithStdinEnv tells `superleo serve` to shut down when its stdin closes:
// how a local worker learns that its queen is gone.
const ExitWithStdinEnv = "SUPERLEO_EXIT_WITH_STDIN"

// LocalHiveSpec is what the queen supplies to run workers.
type LocalHiveSpec struct {
	// Exe is the superleo binary; empty takes this process's own.
	Exe string
	// Dir holds one data directory per worker; empty takes <data>/hive.
	Dir      string
	Min, Max int
	Idle     time.Duration
	// Settings returns the settings.json a worker should start with, given
	// its name and the address it will be reached at.
	Settings func(name, advertise string) ([]byte, error)
	// IdleSince says since when a worker has had nothing to do; zero means it
	// is busy now. Nil turns idle retirement off.
	IdleSince func(name string) time.Time
	// Retired is told about workers the spawner let go of on its own, so the
	// roster can drop them before their goodbye arrives.
	Retired func(names []string)
	Logf    func(format string, args ...any)
}

// LocalSpawner keeps a number of worker processes running.
type LocalSpawner struct {
	spec LocalHiveSpec

	mu      sync.Mutex
	want    int
	procs   map[int]*localProc
	closed  bool
	stopIdl chan struct{}
	idleWG  sync.WaitGroup
	// startProc is a seam for tests: it starts one worker and returns how to
	// stop it and a channel that yields how it exited, once.
	startProc func(name string, port int) (stop func(), exited <-chan error, err error)
}

type localProc struct {
	name   string
	cancel chan struct{}
	done   chan struct{}
}

// NewLocalSpawner starts the minimum number of workers and returns the
// spawner that keeps them.
func NewLocalSpawner(spec LocalHiveSpec) *LocalSpawner {
	l := newLocalSpawner(spec)
	l.SetReplicas(context.Background(), l.spec.Min) //nolint:errcheck // Min is within 0..Max by construction
	l.idleWG.Add(1)
	go l.retireIdle()
	return l
}

func newLocalSpawner(spec LocalHiveSpec) *LocalSpawner {
	if spec.Max <= 0 {
		spec.Max = DefaultLocalMaxWorkers()
	}
	if spec.Min < 0 {
		spec.Min = 0
	}
	if spec.Min > spec.Max {
		spec.Min = spec.Max
	}
	if spec.Idle <= 0 {
		spec.Idle = DefaultLocalIdleMinutes * time.Minute
	}
	if spec.Dir == "" {
		spec.Dir = filepath.Join(DataDir(), "hive")
	}
	if spec.Logf == nil {
		spec.Logf = log.Printf
	}
	l := &LocalSpawner{spec: spec, procs: map[int]*localProc{}, stopIdl: make(chan struct{})}
	l.startProc = l.startServe
	return l
}

func (l *LocalSpawner) Max() int       { return l.spec.Max }
func (l *LocalSpawner) Min() int       { return l.spec.Min }
func (l *LocalSpawner) Prefix() string { return LocalWorkerPrefix }

// Replicas is how many workers this spawner is keeping, started or starting.
func (l *LocalSpawner) Replicas(context.Context) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.want, nil
}

// SetReplicas starts or stops workers until n are kept. Like a StatefulSet it
// grows at the top and shrinks from the top.
func (l *LocalSpawner) SetReplicas(_ context.Context, n int) error {
	if n < 0 || n > l.spec.Max {
		return fmt.Errorf("%d workers is outside 0..%d", n, l.spec.Max)
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return errors.New("the local hive is stopped")
	}
	var stopping []*localProc
	for i := 0; i < n; i++ {
		if l.procs[i] == nil {
			p := &localProc{name: l.name(i), cancel: make(chan struct{}), done: make(chan struct{})}
			l.procs[i] = p
			go l.keep(p)
		}
	}
	for i, p := range l.procs {
		if i >= n {
			stopping = append(stopping, p)
			delete(l.procs, i)
		}
	}
	l.want = n
	l.mu.Unlock()
	for _, p := range stopping {
		close(p.cancel)
	}
	for _, p := range stopping {
		<-p.done
	}
	return nil
}

// Close stops every worker and waits for them to exit.
func (l *LocalSpawner) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	procs := l.procs
	l.procs = map[int]*localProc{}
	l.want = 0
	l.mu.Unlock()
	close(l.stopIdl)
	for _, p := range procs {
		close(p.cancel)
	}
	for _, p := range procs {
		<-p.done
	}
	l.idleWG.Wait()
}

func (l *LocalSpawner) name(i int) string { return fmt.Sprintf("%s-%d", LocalWorkerPrefix, i) }

// keep runs one worker until it is no longer wanted, starting it again when it
// exits on its own. The pause before a restart doubles up to a minute and
// resets once a run has lasted a minute, so a worker that dies on start does
// not spin and one that died once an hour ago is back at once.
func (l *LocalSpawner) keep(p *localProc) {
	defer close(p.done)
	pause := time.Second
	for {
		port, err := freePort()
		var stop func()
		var exited <-chan error
		if err == nil {
			stop, exited, err = l.startProc(p.name, port)
		}
		if err != nil {
			l.spec.Logf("local hive: %s did not start: %v", p.name, err)
		} else {
			started := time.Now()
			select {
			case <-p.cancel:
				stop()
				<-exited
				return
			case err := <-exited:
				if time.Since(started) > time.Minute {
					pause = time.Second
				}
				l.spec.Logf("local hive: %s exited (%v); starting it again in %s", p.name, err, pause)
			}
		}
		select {
		case <-p.cancel:
			return
		case <-time.After(pause):
		}
		if pause *= 2; pause > time.Minute {
			pause = time.Minute
		}
	}
}

// retireIdle lets go of workers above the minimum that have had nothing to do
// for the idle time, one at a time and always the highest, so the names that
// stay are the low ones.
func (l *LocalSpawner) retireIdle() {
	defer l.idleWG.Done()
	if l.spec.IdleSince == nil {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-l.stopIdl:
			return
		case <-tick.C:
			l.retireOneIdle(time.Now())
		}
	}
}

func (l *LocalSpawner) retireOneIdle(now time.Time) bool {
	l.mu.Lock()
	n := l.want
	l.mu.Unlock()
	if n <= l.spec.Min {
		return false
	}
	top := l.name(n - 1)
	since := l.spec.IdleSince(top)
	if since.IsZero() || now.Sub(since) < l.spec.Idle {
		return false
	}
	if err := l.SetReplicas(context.Background(), n-1); err != nil {
		return false
	}
	l.spec.Logf("local hive: %s idle since %s; let go", top, since.Format(time.Kitchen))
	if l.spec.Retired != nil {
		l.spec.Retired([]string{top})
	}
	return true
}

// startServe runs `superleo serve` for one worker.
func (l *LocalSpawner) startServe(name string, port int) (func(), <-chan error, error) {
	exe := l.spec.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return nil, nil, fmt.Errorf("finding the superleo binary: %w", err)
		}
	}
	home := filepath.Join(l.spec.Dir, name)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, nil, err
	}
	advertise := fmt.Sprintf("http://127.0.0.1:%d", port)
	settings, err := l.spec.Settings(name, advertise)
	if err != nil {
		return nil, nil, fmt.Errorf("settings for %s: %w", name, err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), settings, 0o600); err != nil {
		return nil, nil, err
	}
	logf, err := os.OpenFile(filepath.Join(home, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(exe, "serve", "--bind", "127.0.0.1", "--port", fmt.Sprint(port))
	cmd.Env = LocalWorkerEnv(os.Environ(), home, advertise)
	cmd.Stdout, cmd.Stderr = logf, logf
	stdin, err := cmd.StdinPipe()
	if err != nil {
		logf.Close()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, nil, err
	}
	exited := make(chan error, 1)
	gone := make(chan struct{})
	go func() {
		err := cmd.Wait()
		logf.Close()
		close(gone)
		exited <- err
	}()
	stop := func() {
		// Closing stdin is the polite way: the worker says goodbye to the
		// queen and stops serving. Past a grace period it is killed.
		_ = stdin.Close()
		select {
		case <-gone:
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	return stop, exited, nil
}

// localWorkerDropEnv is what a worker must not inherit from its queen: where
// the queen keeps her data and settings, who she is in a cluster, and the
// listeners that would collide.
var localWorkerDropEnv = []string{
	"SUPERLEO_HOME", "SUPERLEO_SETTINGS_JSON", "SUPERLEO_ADVERTISE_URL", "SUPERLEO_HIVE_DELEGATION",
	"SUPERLEO_CORE", "SUPERLEO_CORE_TOKEN", "SUPERLEO_CORE_TOKEN_FILE", "SUPERLEO_AGENT_PORT", "SUPERLEO_AGENT_NAME",
	"SUPERLEO_PPROF", "SUPERLEO_HOOK_SOCKET", "SUPERLEO_NODE", ExitWithStdinEnv,
}

// LocalWorkerEnv is the environment a local worker runs with: the queen's,
// minus what belongs to her alone, plus its own home and address.
func LocalWorkerEnv(parent []string, home, advertise string) []string {
	out := make([]string, 0, len(parent)+4)
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, d := range localWorkerDropEnv {
			if k == d {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return append(out,
		"SUPERLEO_HOME="+home,
		"SUPERLEO_ADVERTISE_URL="+advertise,
		"SUPERLEO_NO_BROWSER=1",
		ExitWithStdinEnv+"=1",
	)
}

// LocalWorkerSettings derives a worker's settings from its queen's: the same
// model, keys, memory and workspace, and none of what only one instance on a
// machine may hold — the Telegram bot, the phone pushes, the embedded proxy's
// port, the avatar's port, the links to other SuperLeos.
func LocalWorkerSettings(queen Settings, name, joinURL, joinToken, advertise string) Settings {
	w := queen
	w.Hive = HiveSettings{
		Role: HiveRoleWorker, Name: name, JoinURL: joinURL, JoinToken: joinToken,
		AdvertiseURL: advertise, MaxConcurrentOrders: queen.Hive.MaxConcurrentOrders,
	}
	w.TelegramBotToken = ""
	w.TelegramAllowedChats = nil
	w.WebhookURL, w.WebhookSecret = "", ""
	w.APNs = APNsSettings{}
	w.CLIProxyEnabled = false
	w.AvatarPort = 0
	w.RemoteAgents = RemoteAgents{}
	w.Headless = true
	return w
}

// freePort asks the operating system for a loopback port nobody holds.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// WatchStdin calls stop when stdin closes. A local worker runs it, so the
// worker goes when its queen does.
func WatchStdin(stop func()) {
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		stop()
	}()
}
