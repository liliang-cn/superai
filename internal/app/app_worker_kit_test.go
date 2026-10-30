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
	answerCards(t, q, true)

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

// answerCards answers every approval card on a as it comes, allowing or not,
// and hands back the cards it answered.
func answerCards(t *testing.T, a *App, allow bool) func() []map[string]any {
	t.Helper()
	var mu sync.Mutex
	var seen []map[string]any
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			for _, p := range a.PendingToolApprovals() {
				mu.Lock()
				seen = append(seen, p)
				mu.Unlock()
				a.ResolveToolApproval(p["id"].(string), allow)
			}
		}
	}()
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), seen...)
	}
}

// A worker's card adds a server reached by URL — streamable HTTP or SSE, with
// a token — once the person allows it on an approval card: the worker gets the
// headers as they were typed, the card shows only the token's last four.
func TestAWorkersCardAddsAServerByURLOnceAllowed(t *testing.T) {
	var mu sync.Mutex
	var installs [][]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/api/rpc/")
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		switch method {
		case "InstallMCPURLServer":
			mu.Lock()
			installs = append(installs, args)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode("ok")
		case "HiveKit":
			_ = json.NewEncoder(w).Encode(backend.WorkerKit{Worker: "w1",
				MCP: []backend.KitItem{{Name: "linear", OK: true, Own: true, Tools: []string{"list_issues", "create_issue"}}}})
		default:
			http.Error(w, "no such method", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	q := kitQueen(t, backend.HiveHello{Name: "w1", URL: srv.URL})

	// Said no: nothing reaches the worker.
	stopNo := answerCards(t, q, false)
	if _, err := q.AddWorkerMCPURL("w1", "linear", "", "https://mcp.linear.app/mcp", map[string]string{"Authorization": "Bearer lin_api_abcdef1234"}); err == nil ||
		!strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("a denied card said %v", err)
	}
	if len(stopNo()) != 1 {
		t.Fatalf("cards asked: %v", stopNo())
	}
	mu.Lock()
	if len(installs) != 0 {
		t.Fatalf("installed without approval: %v", installs)
	}
	mu.Unlock()

	q2 := kitQueen(t, backend.HiveHello{Name: "w1", URL: srv.URL})
	cards := answerCards(t, q2, true)
	kit, err := q2.AddWorkerMCPURL("w1", "linear", "streamable-http", "https://mcp.linear.app/mcp", map[string]string{"Authorization": "Bearer lin_api_abcdef1234"})
	if err != nil {
		t.Fatal(err)
	}
	if len(kit.MCP) != 1 || kit.MCP[0].Name != "linear" || len(kit.MCP[0].Tools) != 2 {
		t.Fatalf("kit after adding: %+v", kit)
	}
	mu.Lock()
	got := installs
	mu.Unlock()
	if len(got) != 1 || got[0][0] != "linear" || got[0][1] != "http" || got[0][2] != "https://mcp.linear.app/mcp" {
		t.Fatalf("the worker was asked %v", got)
	}
	if h, _ := got[0][3].(map[string]any); h["Authorization"] != "Bearer lin_api_abcdef1234" {
		t.Fatalf("the worker got headers %v", got[0][3])
	}
	seen := cards()
	if len(seen) != 1 || seen[0]["tool"] != "add_mcp_server" {
		t.Fatalf("cards: %v", seen)
	}
	raw, _ := json.Marshal(seen[0])
	if strings.Contains(string(raw), "abcdef") || !strings.Contains(string(raw), "••••1234") || !strings.Contains(string(raw), "mcp.linear.app") {
		t.Fatalf("the card shows %s", raw)
	}

	if _, err := q2.AddWorkerMCPURL("w1", "x", "websocket", "https://a.example/mcp", nil); err == nil {
		t.Fatal("an unknown transport was taken")
	}
	if _, err := q2.AddWorkerMCPURL("nobody", "x", "sse", "https://a.example/sse", nil); err == nil {
		t.Fatal("a worker not in the hive was asked")
	}
}
