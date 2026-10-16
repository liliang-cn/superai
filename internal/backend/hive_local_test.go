package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProcs stands in for `superai serve`: each start is recorded, and a
// test can make a running one exit as if it had crashed.
type fakeProcs struct {
	mu      sync.Mutex
	starts  map[string]int
	running map[string]chan error
}

func newFakeProcs() *fakeProcs {
	return &fakeProcs{starts: map[string]int{}, running: map[string]chan error{}}
}

func (f *fakeProcs) start(name string, port int) (func(), <-chan error, error) {
	if port <= 3000 {
		return nil, nil, fmt.Errorf("port %d", port)
	}
	ch := make(chan error, 1)
	f.mu.Lock()
	f.starts[name]++
	f.running[name] = ch
	f.mu.Unlock()
	stop := func() {
		f.mu.Lock()
		if f.running[name] == ch {
			delete(f.running, name)
		}
		f.mu.Unlock()
		select {
		case ch <- nil:
		default:
		}
	}
	return stop, ch, nil
}

func (f *fakeProcs) crash(name string) {
	f.mu.Lock()
	ch := f.running[name]
	delete(f.running, name)
	f.mu.Unlock()
	ch <- errors.New("crashed")
}

func (f *fakeProcs) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.running {
		out = append(out, n)
	}
	return out
}

func (f *fakeProcs) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts[name]
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testSpawner(f *fakeProcs, min, max int) *LocalSpawner {
	l := newLocalSpawner(LocalHiveSpec{Min: min, Max: max, Dir: "unused", Logf: func(string, ...any) {}})
	l.startProc = f.start
	return l
}

func TestLocalSpawnerGrowsAndShrinksFromTheTop(t *testing.T) {
	f := newFakeProcs()
	l := testSpawner(f, 0, 3)
	defer l.Close()
	ctx := context.Background()

	if err := l.SetReplicas(ctx, 3); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "three running", func() bool { return len(f.names()) == 3 })
	if n, _ := l.Replicas(ctx); n != 3 {
		t.Fatalf("replicas = %d", n)
	}
	if err := l.SetReplicas(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.names(); len(got) != 1 || got[0] != "local-0" {
		t.Fatalf("after shrinking, running %v; want only local-0", got)
	}
	if err := l.SetReplicas(ctx, 4); err == nil {
		t.Fatal("4 is past the cap of 3 and was allowed")
	}
}

func TestLocalSpawnerRestartsACrashedWorker(t *testing.T) {
	f := newFakeProcs()
	l := testSpawner(f, 0, 2)
	defer l.Close()
	if err := l.SetReplicas(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "local-0 up", func() bool { return f.count("local-0") == 1 })
	f.crash("local-0")
	// The first pause is a second.
	waitUntil(t, "local-0 started again", func() bool { return f.count("local-0") == 2 })
}

func TestLocalSpawnerCloseStopsEveryWorker(t *testing.T) {
	f := newFakeProcs()
	l := testSpawner(f, 0, 3)
	_ = l.SetReplicas(context.Background(), 2)
	waitUntil(t, "two running", func() bool { return len(f.names()) == 2 })
	l.Close()
	if got := f.names(); len(got) != 0 {
		t.Fatalf("still running after Close: %v", got)
	}
	if err := l.SetReplicas(context.Background(), 1); err == nil {
		t.Fatal("a closed spawner started a worker")
	}
}

func TestLocalSpawnerLetsIdleWorkersGoDownToTheMinimum(t *testing.T) {
	f := newFakeProcs()
	now := time.Now()
	idle := map[string]time.Time{"local-0": now.Add(-time.Hour), "local-1": now.Add(-time.Hour), "local-2": now.Add(-time.Minute)}
	var retired []string
	l := newLocalSpawner(LocalHiveSpec{
		Min: 1, Max: 3, Idle: 10 * time.Minute, Dir: "unused", Logf: func(string, ...any) {},
		IdleSince: func(n string) time.Time { return idle[n] },
		Retired:   func(n []string) { retired = append(retired, n...) },
	})
	l.startProc = f.start
	defer l.Close()
	_ = l.SetReplicas(context.Background(), 3)

	// The top one has only been idle a minute: nothing goes, even though a
	// lower one has been idle for an hour.
	if l.retireOneIdle(now) {
		t.Fatal("retired while the highest worker was recently busy")
	}
	idle["local-2"] = now.Add(-time.Hour)
	if !l.retireOneIdle(now) || !l.retireOneIdle(now) {
		t.Fatal("idle workers above the minimum were kept")
	}
	if l.retireOneIdle(now) {
		t.Fatal("went below the minimum")
	}
	if strings.Join(retired, ",") != "local-2,local-1" {
		t.Fatalf("retired %v", retired)
	}
	// Busy (zero) is never idle.
	_ = l.SetReplicas(context.Background(), 2)
	idle["local-1"] = time.Time{}
	if l.retireOneIdle(now) {
		t.Fatal("retired a busy worker")
	}
}

func TestLocalWorkerEnvDropsTheQueensOwn(t *testing.T) {
	env := LocalWorkerEnv([]string{
		"PATH=/bin", "SUPERAI_HOME=/q", "SUPERAI_SETTINGS_JSON={}", "SUPERAI_CORE=x:1", "NOTED_TOKEN=keep",
	}, "/w", "http://127.0.0.1:4000")
	got := strings.Join(env, "\n")
	for _, want := range []string{"PATH=/bin", "NOTED_TOKEN=keep", "SUPERAI_HOME=/w", "SUPERAI_ADVERTISE_URL=http://127.0.0.1:4000", ExitWithStdinEnv + "=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, gone := range []string{"SUPERAI_HOME=/q", "SUPERAI_SETTINGS_JSON", "SUPERAI_CORE="} {
		if strings.Contains(got, gone) {
			t.Errorf("kept %s", gone)
		}
	}
}

func TestLocalWorkerSettingsKeepTheModelAndDropTheSingletons(t *testing.T) {
	q := Settings{LLMModel: "m", LLMKey: "k", WorkspaceDir: "/ws", TelegramBotToken: "bot", CLIProxyEnabled: true, AvatarPort: 4321}
	q.Hive = HiveSettings{Role: HiveRoleQueen, Spawner: &SpawnerSettings{Kind: SpawnerLocal}}
	w := LocalWorkerSettings(q, "local-0", "http://127.0.0.1:5000", "tok", "http://127.0.0.1:6000")
	if w.LLMModel != "m" || w.LLMKey != "k" || w.WorkspaceDir != "/ws" {
		t.Fatalf("model or workspace not carried over: %+v", w)
	}
	if w.TelegramBotToken != "" || w.CLIProxyEnabled || w.AvatarPort != 0 {
		t.Fatal("a worker took one of the queen's single listeners")
	}
	if w.Hive.Role != HiveRoleWorker || w.Hive.Spawner != nil || w.Hive.JoinURL != "http://127.0.0.1:5000" || w.Hive.JoinToken != "tok" || w.Hive.Name != "local-0" {
		t.Fatalf("hive settings: %+v", w.Hive)
	}
	if q.Hive.Role != HiveRoleQueen {
		t.Fatal("deriving changed the queen's settings")
	}
}
