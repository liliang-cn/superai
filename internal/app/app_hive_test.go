package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superai-desktop/internal/backend"
)

// An instance that has never heard of the hive must stay a whole SuperAI. The
// hive is something it can join, not something it now depends on.

func TestStandaloneIsNotAHiveAtAll(t *testing.T) {
	a := &App{settings: &backend.Settings{}}
	st := a.HiveStatus()
	if st["role"] != "" {
		t.Fatalf("a default install reports role %q", st["role"])
	}
	if ms := st["members"].([]backend.HiveMember); len(ms) != 0 {
		t.Fatalf("members on a standalone: %+v", ms)
	}
	if _, has := st["queen"]; has {
		t.Fatal("a standalone reports a queen it is not joined to")
	}
	if got := a.hiveAgents(); len(got) != 0 {
		t.Fatalf("a standalone has a roster: %+v", got)
	}
	// And it must not start announcing to anyone.
	a.startHive()
	if a.hiveStop != nil || a.hiveAnn != nil {
		t.Fatal("a standalone started a hive loop")
	}
}

func TestOnlyAQueenAnswersAJoin(t *testing.T) {
	post := func(a *App) int {
		w := httptest.NewRecorder()
		a.handleHiveJoin(w, httptest.NewRequest(http.MethodPost, "/api/hive/join", strings.NewReader(`{}`)))
		return w.Code
	}
	if code := post(&App{settings: &backend.Settings{}}); code != http.StatusNotFound {
		t.Fatalf("a standalone answered a join with %d", code)
	}
	worker := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleWorker}}}
	if code := post(worker); code != http.StatusNotFound {
		t.Fatalf("a worker answered a join with %d", code)
	}
}

func TestAQueenWithNoWorkersStillWorks(t *testing.T) {
	// The hive mode must not need anyone in it: an empty roster is a queen that
	// has not been joined yet, not a broken one.
	a := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}}
	a.hive = backend.NewHive("q", 0)
	st := a.HiveStatus()
	if st["role"] != "queen" || st["name"] != "q" {
		t.Fatalf("status %+v", st)
	}
	out, err := a.hiveCommand(nil, map[string]any{"prompt": "anyone?"})
	if err != nil || !strings.Contains(out.(string), "no live workers") {
		t.Fatalf("out %v err %v", out, err)
	}
}

func TestOnlyARosterMemberMayReportATask(t *testing.T) {
	a := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen}}}
	a.hive = backend.NewHive("q", 0)
	a.hive.Join(backend.HiveHello{Protocol: backend.HiveProtocol, Name: "w1", Role: backend.HiveRoleWorker, URL: "http://w1:1"})

	post := func(body string) int {
		w := httptest.NewRecorder()
		a.handleHiveTask(w, httptest.NewRequest(http.MethodPost, "/api/hive/task", strings.NewReader(body)))
		return w.Code
	}
	good := `{"id":"t1","worker":"w2","from":"w1","dir":"peer","state":"running","started_at":"2026-09-29T12:00:00Z"}`
	if c := post(good); c != http.StatusOK {
		t.Fatalf("a member's report got %d", c)
	}
	if got := a.tasks().Recent(); len(got) != 1 || got[0].From != "w1" {
		t.Fatalf("%+v", got)
	}
	if c := post(strings.Replace(good, `"from":"w1"`, `"from":"stranger"`, 1)); c != http.StatusForbidden {
		t.Fatalf("a stranger's report got %d", c)
	}
	// Reporting is for peer tasks only: it must not be a way to write the
	// queen's own orders.
	if c := post(strings.Replace(good, `"dir":"peer"`, `"dir":"out"`, 1)); c != http.StatusBadRequest {
		t.Fatalf("a non-peer task got %d", c)
	}
}

func TestRosterAndTaskEndpointsAreQueenOnly(t *testing.T) {
	a := &App{settings: &backend.Settings{}}
	for name, h := range map[string]http.HandlerFunc{"roster": a.handleHiveRoster, "task": a.handleHiveTask, "leave": a.handleHiveLeave, "pulse": a.handleHivePulse} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`)))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s answered %d on a standalone", name, w.Code)
		}
	}
}

// A kubectl that keeps its replica count in a file, so a test can watch a
// StatefulSet grow and shrink without a cluster.
func statefulKubectl(t *testing.T, start int) (bin, state string) {
	dir := t.TempDir()
	state = dir + "/replicas"
	bin = dir + "/kubectl"
	os.WriteFile(state, []byte(strconv.Itoa(start)), 0o644)
	script := `#!/bin/sh
case "$*" in
  *jsonpath*) cat ` + state + ` ;;
  *scale*) for a in "$@"; do case "$a" in --replicas=*) printf '%s' "${a#--replicas=}" > ` + state + ` ;; esac; done ;;
esac
`
	os.WriteFile(bin, []byte(script), 0o755)
	return
}

func queenWithSpawner(t *testing.T, kubectl string, max int) *App {
	a := &App{settings: &backend.Settings{Hive: backend.HiveSettings{
		Role: backend.HiveRoleQueen, Name: "q",
		Spawner: &backend.SpawnerSettings{Kind: "kubectl", StatefulSet: "w", Namespace: "ns", MaxWorkers: max, Kubectl: kubectl},
	}}}
	a.hive = backend.NewHive("q", 10*time.Second)
	return a
}

func join(a *App, name string) { joinAt(a, name, time.Now()) }

// joinAt is a worker announcing itself with a given process start time.
func joinAt(a *App, name string, started time.Time) {
	a.hive.Join(backend.HiveHello{
		Protocol: backend.HiveProtocol, Name: name, Role: backend.HiveRoleWorker,
		URL: "http://" + name + ":1", StartedAt: started,
	})
}

func TestSpawnWaitsForTheNewWorkersToJoin(t *testing.T) {
	bin, state := statefulKubectl(t, 2)
	a := queenWithSpawner(t, bin, 10)
	join(a, "w-0")
	join(a, "w-1")
	go func() { time.Sleep(500 * time.Millisecond); join(a, "w-2") }()

	r := a.HiveSpawn(1)
	if ok, _ := r["ok"].(bool); !ok {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(state); string(b) != "3" {
		t.Fatalf("replicas %s", b)
	}
	joined := r["joined"].([]string)
	if r["complete"] != true || len(joined) != 1 || joined[0] != "w-2" {
		t.Fatalf("%+v", r)
	}
}

func TestSpawnRefusesPastTheCapAndTouchesNothing(t *testing.T) {
	bin, state := statefulKubectl(t, 4)
	a := queenWithSpawner(t, bin, 5)
	r := a.HiveSpawn(3)
	if ok, _ := r["ok"].(bool); ok {
		t.Fatalf("went past the cap: %+v", r)
	}
	if b, _ := os.ReadFile(state); string(b) != "4" {
		t.Fatalf("a refused spawn changed the cluster: %s", b)
	}
}

func TestOnlyAQueenWithASpawnerMakesWorkers(t *testing.T) {
	if r := (&App{settings: &backend.Settings{}}).HiveSpawn(1); r["ok"] == true {
		t.Fatal("a standalone made workers")
	}
	noSpawner := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen}}}
	noSpawner.hive = backend.NewHive("q", 0)
	r := noSpawner.HiveSpawn(1)
	if r["ok"] == true || !strings.Contains(r["error"].(string), "no spawner") {
		t.Fatalf("%+v", r)
	}
}

func TestRetireTakesTheHighestOnesAndRefusesTheBusy(t *testing.T) {
	bin, state := statefulKubectl(t, 4)
	a := queenWithSpawner(t, bin, 10)
	id := a.tasks().Start("w-3", backend.TaskOut, "still working")

	r := a.HiveRetire(2, false)
	if r["ok"] == true || !strings.Contains(r["error"].(string), "w-3") {
		t.Fatalf("retired a worker in the middle of an order: %+v", r)
	}
	if b, _ := os.ReadFile(state); string(b) != "4" {
		t.Fatalf("replicas %s after a refusal", b)
	}

	a.tasks().Finish(id, backend.TaskDone, "", "")
	r = a.HiveRetire(2, false)
	if r["ok"] != true || strings.Join(r["retiring"].([]string), ",") != "w-2,w-3" {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(state); string(b) != "2" {
		t.Fatalf("replicas %s", b)
	}

	// Force is the caller saying the work may be lost.
	a.tasks().Start("w-1", backend.TaskOut, "busy")
	if r := a.HiveRetire(1, true); r["ok"] != true {
		t.Fatalf("%+v", r)
	}
}

func TestRetireCannotGoBelowZero(t *testing.T) {
	bin, state := statefulKubectl(t, 1)
	a := queenWithSpawner(t, bin, 10)
	if r := a.HiveRetire(5, false); r["ok"] != true {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(state); string(b) != "0" {
		t.Fatalf("replicas %s", b)
	}
	if r := a.HiveRetire(1, false); r["ok"] == true {
		t.Fatal("retired a worker that does not exist")
	}
}

func TestASpawnedOrdinalIsNewOnlyUnderANewStartTime(t *testing.T) {
	// Retire the top ordinal and spawn again at once: the old worker-1 is still
	// "live" on the roster, and its next heartbeat carries the old start time.
	// Only the replacement, with a new one, is the worker that was asked for.
	bin, _ := statefulKubectl(t, 1)
	a := queenWithSpawner(t, bin, 10)
	old := time.Now().Add(-time.Hour)
	joinAt(a, "w-0", old)
	joinAt(a, "w-1", old) // the pod that was just retired, still on the roster
	// (replicas is 1 in the cluster, so the next ordinal to make is w-1)
	go func() {
		time.Sleep(600 * time.Millisecond)
		joinAt(a, "w-1", old) // a stale heartbeat from the pod on its way out
		time.Sleep(1200 * time.Millisecond)
		joinAt(a, "w-1", time.Now()) // the replacement
	}()
	started := time.Now()
	r := a.HiveSpawn(1)
	if r["complete"] != true {
		t.Fatalf("%+v", r)
	}
	if time.Since(started) < 1500*time.Millisecond {
		t.Fatalf("returned in %v, before the replacement had joined", time.Since(started))
	}
	if j := r["joined"].([]string); len(j) != 1 || j[0] != "w-1" {
		t.Fatalf("%+v", r)
	}
}

func TestOnlyARosterMemberMayReportAPulse(t *testing.T) {
	a := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen}}}
	a.hive = backend.NewHive("q", 0)
	a.hive.Join(backend.HiveHello{Protocol: backend.HiveProtocol, Name: "w1", Role: backend.HiveRoleWorker, URL: "http://w1:1"})
	post := func(body string) int {
		w := httptest.NewRecorder()
		a.handleHivePulse(w, httptest.NewRequest(http.MethodPost, "/api/hive/pulse", strings.NewReader(body)))
		return w.Code
	}
	good := `{"task":"t","worker":"w2","from":"w1","dir":"peer","kind":"tool","tool":"x"}`
	if c := post(good); c != http.StatusOK {
		t.Fatalf("a member's pulse got %d", c)
	}
	if c := post(strings.Replace(good, `"from":"w1"`, `"from":"nobody"`, 1)); c != http.StatusForbidden {
		t.Fatalf("a stranger's pulse got %d", c)
	}
	if c := post(strings.Replace(good, `"dir":"peer"`, `"dir":"out"`, 1)); c != http.StatusBadRequest {
		t.Fatalf("a non-peer pulse got %d", c)
	}
}
