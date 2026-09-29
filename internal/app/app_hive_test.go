package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
