package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

func kitQueen(t *testing.T, workers ...backend.HiveHello) *App {
	t.Helper()
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	q := &App{settings: &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}}
	q.hive = backend.NewHive("q", 10*time.Second)
	for _, w := range workers {
		w.Protocol, w.Role = backend.HiveProtocol, backend.HiveRoleWorker
		if w.URL == "" {
			w.URL = "http://127.0.0.1:1"
		}
		if _, err := q.hive.Join(w); err != nil {
			t.Fatal(err)
		}
	}
	return q
}

// A worker says which MCP servers and skills it has; one only it has is where
// the queen sends a task that needs it, and what hive_delegate tells her.
func TestAWorkersOwnKitRoutesTheTaskThatNeedsIt(t *testing.T) {
	q := kitQueen(t,
		backend.HiveHello{Name: "w1", MCP: []string{"websearch", "sqlite"}, Skills: []string{"inventory"},
			MCPTools: map[string][]string{"sqlite": {"query", "list_tables"}, "websearch": {"basic"}}},
		backend.HiveHello{Name: "w2", MCP: []string{"websearch"}, Skills: []string{}},
	)
	if got := q.delegateCandidates([]string{"sqlite"}); !slices.Equal(got, []string{"w1"}) {
		t.Fatalf("who has sqlite: %v", got)
	}
	if got := q.delegateCandidates([]string{"inventory"}); !slices.Equal(got, []string{"w1"}) {
		t.Fatalf("who has the inventory skill: %v", got)
	}
	if got := q.delegateCandidates([]string{"websearch"}); len(got) != 2 {
		t.Fatalf("both have websearch: %v", got)
	}
	if got := q.kitRoster(); got != "w1: mcp sqlite (tools list_tables, query), skills inventory" {
		t.Fatalf("kit roster %q", got)
	}
	names := q.workerKitNames()
	if !slices.Contains(names, "sqlite") || !slices.Contains(names, "inventory") {
		t.Fatalf("names the queen infers needs from: %v", names)
	}
	// "query the stock in sqlite" needs sqlite even when the queen left needs out.
	if got := backend.InferNeeds("query the stock table with sqlite", names); !slices.Equal(got, []string{"sqlite"}) {
		t.Fatalf("inferred %v", got)
	}
	var w1 *HostAgent
	for _, h := range q.Hosts() {
		for i := range h.Agents {
			if h.Agents[i].Name == "w1" {
				w1 = &h.Agents[i]
			}
		}
	}
	if w1 == nil || !slices.Equal(w1.Skills, []string{"inventory"}) {
		t.Fatalf("the host list lost w1's skills: %+v", w1)
	}
}

// A worker's card reads its kit from the worker and adds to it and takes from
// it there, on that worker alone.
func TestAWorkersCardAddsAndRemovesItsOwnKit(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var installed []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/api/rpc/")
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		mu.Lock()
		calls = append(calls, method)
		mu.Unlock()
		switch method {
		case "HiveKit":
			_ = json.NewEncoder(w).Encode(backend.WorkerKit{Worker: "w1",
				MCP:    []backend.KitItem{{Name: "sqlite", OK: true, Own: true, Tools: []string{"query"}}, {Name: "noted", OK: true}},
				Skills: []backend.KitItem{{Name: "inventory", Own: true}}})
		case "InstallMCPServer":
			installed = args
			_ = json.NewEncoder(w).Encode("ok")
		case "InstallSkillFiles":
			files, _ := args[1].(map[string]any)
			if _, ok := files["SKILL.md"]; !ok || args[0] != "inventory" {
				_ = json.NewEncoder(w).Encode("no SKILL.md")
				return
			}
			_ = json.NewEncoder(w).Encode("ok")
		case "RemoveMCPServer":
			_ = json.NewEncoder(w).Encode("noted is given by the hive's queen and cannot be removed here")
		case "RemoveSkill":
			_ = json.NewEncoder(w).Encode("ok")
		default:
			http.Error(w, "no such method", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	q := kitQueen(t, backend.HiveHello{Name: "w1", URL: srv.URL, MCP: []string{"noted"}})

	kit, err := q.AddWorkerMCP("w1", "sqlite", "/usr/local/bin/mcp-sqlite-server", []string{"/data/stock.db"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) < 3 || installed[0] != "sqlite" || installed[1] != "/usr/local/bin/mcp-sqlite-server" {
		t.Fatalf("the worker was asked to install %v", installed)
	}
	if len(kit.MCP) != 2 || !kit.MCP[0].Own || kit.MCP[1].Own || kit.Worker != "w1" {
		t.Fatalf("kit after adding: %+v", kit)
	}

	skill := filepath.Join(t.TempDir(), "inventory")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: inventory\ndescription: stock counts\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AddWorkerSkill("w1", "inventory", skill); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AddWorkerSkill("w1", "nothing", t.TempDir()); err == nil {
		t.Fatal("a folder without a SKILL.md was sent")
	}
	if _, err := q.RemoveWorkerSkill("w1", "inventory"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RemoveWorkerMCP("w1", "noted"); err == nil || !strings.Contains(err.Error(), "queen") {
		t.Fatalf("removing the queen's server said %v", err)
	}
	if _, err := q.WorkerKit("nobody"); err == nil {
		t.Fatal("a kit for a worker that is not in the hive")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"InstallMCPServer", "HiveKit", "InstallSkillFiles", "HiveKit", "RemoveSkill", "HiveKit", "RemoveMCPServer"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls on the worker %v, want %v", calls, want)
	}
}
