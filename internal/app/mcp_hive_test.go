package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearer puts a token on every request an MCP client makes.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// gatedServer is serve mode's gate in front of the real /mcp, the RPC surface
// and the notification route, in a fresh home.
func gatedServer(t *testing.T, a *App) (string, *credentials) {
	t.Helper()
	noAPNs(t)
	c := testCreds(t, "hunter2")
	var err error
	if c.devices, err = openDevices(devicesPath()); err != nil {
		t.Fatal(err)
	}
	if c.integrations, err = openIntegrations(integrationsPath()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	authRoutes(mux, c)
	mux.HandleFunc("/api/rpc/", func(w http.ResponseWriter, r *http.Request) { rpcCall(a, w, r) })
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("events")) })
	mux.Handle(mcpPath, newMCPHandler(a, "test"))
	srv := httptest.NewServer(requireAuth(c, mux))
	t.Cleanup(srv.Close)
	return srv.URL, c
}

func mcpAs(t *testing.T, base, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	ss, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: base + mcpPath, HTTPClient: &http.Client{Transport: bearer(token)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	return ss
}

func toolNames(t *testing.T, ss *mcp.ClientSession) []string {
	t.Helper()
	res, err := ss.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tl := range res.Tools {
		out = append(out, tl.Name)
	}
	slices.Sort(out)
	return out
}

func TestAQueenTokenReachesOnlyTheClusterTools(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := NewApp()
	base, c := gatedServer(t, a)
	minted, err := a.CreateQueenToken("claude-queen")
	if err != nil {
		t.Fatal(err)
	}
	tok := minted.Token
	if !strings.HasPrefix(tok, queenTokenPrefix) || minted.Scope != "queen" {
		t.Fatalf("minted %+v", minted)
	}

	// Nowhere but /mcp.
	for _, p := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/rpc/SendChat", `["s","hi",null]`},
		{http.MethodPost, "/api/rpc/CreateQueenToken", `["another"]`},
		{http.MethodPost, "/api/rpc/ResolveToolApproval", `["x",true]`},
		{http.MethodPost, postNotificationPath, `[{"title":"hi"}]`},
		{http.MethodGet, "/api/events", ``},
	} {
		req, _ := http.NewRequest(p.method, base+p.path, strings.NewReader(p.body))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("queen token on %s: %d", p.path, resp.StatusCode)
		}
	}
	if credentialsCheck(c)(tok) {
		t.Error("a queen token connects an agent")
	}

	// On /mcp, exactly the cluster-command tools: every one of them exists,
	// nothing else is listed.
	queen := mcpAs(t, base, tok)
	want := slices.Clone(queenMCPTools)
	slices.Sort(want)
	if got := toolNames(t, queen); !slices.Equal(got, want) {
		t.Fatalf("queen tools\n got %v\nwant %v", got, want)
	}
	full := mcpAs(t, base, c.Token)
	fullNames := toolNames(t, full)
	for _, n := range append([]string{"superai_ask", "superai_settings_set"}, queenMCPTools...) {
		if !slices.Contains(fullNames, n) {
			t.Errorf("the full token lacks %s", n)
		}
	}

	for _, cs := range []struct {
		tool    string
		args    map[string]any
		allowed bool
	}{
		{"superai_hive_tasks", map[string]any{}, true},
		{"superai_approvals_pending", map[string]any{}, true},
		{"superai_schedule_list", map[string]any{}, true},
		{"superai_status", map[string]any{}, true},
		{"superai_ask", map[string]any{"message": "change your settings"}, false},
		{"superai_settings_set", map[string]any{"key": "max_rounds", "value": 3}, false},
		{"superai_settings_get", map[string]any{}, false},
		{"superai_devices", map[string]any{}, false},
		{"superai_standing_agent_pause", map[string]any{"id": "x", "paused": true}, false},
	} {
		res, err := queen.CallTool(context.Background(), &mcp.CallToolParams{Name: cs.tool, Arguments: cs.args})
		switch {
		case cs.allowed && (err != nil || res.IsError):
			t.Errorf("%s refused to the queen: %v %+v", cs.tool, err, res)
		case !cs.allowed && err == nil:
			t.Errorf("%s answered a queen token: %+v", cs.tool, res)
		}
	}

	// A session opened with the full token is not the queen's to use.
	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"superai_ask","arguments":{"message":"hi"}}}`
	req, _ := http.NewRequest(http.MethodPost, base+mcpPath, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Mcp-Session-Id", full.ID())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a queen token used the full token's session")
	}

	list, err := a.ListIntegrationTokens()
	if err != nil || len(list) != 1 || list[0].Scope != "queen" {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := a.RevokeIntegrationToken(minted.ID); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodPost, base+mcpPath, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked queen token on /mcp: %d", resp.StatusCode)
	}
}

// answerNext waits for one approval card and answers it, reporting what it
// saw. Nothing on /mcp answers; this is the person's tap.
func answerNext(t *testing.T, a *App, allow bool) <-chan approvalLine {
	t.Helper()
	seen := make(chan approvalLine, 1)
	go func() {
		defer close(seen)
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if p := a.pendingApprovals(); len(p) > 0 {
				seen <- p[0]
				a.ResolveToolApproval(p[0].ID, allow)
				return
			}
		}
	}()
	return seen
}

func mcpText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestDestructiveMCPCallsWaitForThePerson(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_HOME", home)
	a := NewApp()
	gate := backend.NewToolGate(true, filepath.Join(home, "audit.jsonl"))
	gate.SetApprover(a.askToolApproval)
	gate.SetWait(5 * time.Second)
	a.gateForMCP = gate
	ss := selfSession(t, a)

	stopped := 0
	runningTask := func() string {
		id := a.tasks().Start("w1", backend.TaskOut, "a long order")
		a.tasks().OnStop(id, func() { stopped++; a.tasks().Finish(id, backend.TaskCancelled, "", "") })
		return id
	}

	for _, c := range []struct {
		name, tool, gateName string
		args                 func() map[string]any
		asks                 bool
		// ran says the call went ahead (it got past the gate), by its answer.
		ran func(text string) bool
	}{
		{"exec", "superai_machine_act", "machine_act",
			func() map[string]any {
				return map[string]any{"machine": "nosuch", "action": "exec", "command": "uptime"}
			},
			true, func(s string) bool { return strings.Contains(s, `no machine called "nosuch"`) }},
		{"restart", "superai_machine_act", "machine_act",
			func() map[string]any {
				return map[string]any{"machine": "nosuch", "action": "restart", "service": "nginx"}
			},
			true, func(s string) bool { return strings.Contains(s, `no machine called "nosuch"`) }},
		{"forced retire", "superai_hive_retire", "hive_retire",
			func() map[string]any { return map[string]any{"count": 1, "force": true} },
			true, func(s string) bool { return !strings.Contains(s, "was not done") }},
		{"schedule delete", "superai_schedule_delete", "schedule_remove",
			func() map[string]any { return map[string]any{"id": "nope"} },
			true, func(s string) bool { return !strings.Contains(s, "was not done") }},
		{"task cancel", "superai_hive_task_cancel", "hive_task_cancel",
			func() map[string]any { return map[string]any{"id": runningTask()} },
			true, func(s string) bool { return strings.Contains(s, "stopping") }},
		{"plain retire", "superai_hive_retire", "",
			func() map[string]any { return map[string]any{"count": 1} },
			false, func(s string) bool { return !strings.Contains(s, "was not done") }},
	} {
		for _, allow := range []bool{true, false} {
			if !c.asks && !allow {
				continue
			}
			var seen <-chan approvalLine
			if c.asks {
				seen = answerNext(t, a, allow)
			}
			before := stopped
			res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: c.tool, Arguments: c.args()})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			text := mcpText(res)
			if !c.asks {
				if p := a.pendingApprovals(); len(p) != 0 || !c.ran(text) {
					t.Errorf("%s was asked about or refused: %s %+v", c.name, text, p)
				}
				continue
			}
			card, ok := <-seen
			if !ok {
				t.Fatalf("%s: nobody was asked", c.name)
			}
			if card.Tool != c.gateName || card.By != mcpCallerFull {
				t.Errorf("%s: card %+v", c.name, card)
			}
			if got := c.ran(text); got != allow {
				t.Errorf("%s allow=%v: ran=%v (%s)", c.name, allow, got, text)
			}
			if !allow && !res.IsError {
				t.Errorf("%s: a denial is not an error: %s", c.name, text)
			}
			if c.tool == "superai_hive_task_cancel" && (stopped-before == 1) != allow {
				t.Errorf("task cancel allow=%v stopped %d", allow, stopped-before)
			}
		}
	}

	// The card names the machine and the command.
	seen := answerNext(t, a, false)
	_, _ = ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superai_machine_act",
		Arguments: map[string]any{"machine": "orange1", "action": "exec", "command": "rm -rf /tmp/x"}})
	if card := <-seen; card.Command != "rm -rf /tmp/x   on orange1" {
		t.Errorf("card command %q", card.Command)
	}

	// An id that names nothing is refused without troubling the person.
	res, _ := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superai_hive_task_cancel", Arguments: map[string]any{"id": "nope"}})
	if !res.IsError || len(a.pendingApprovals()) != 0 {
		t.Errorf("unknown id: %s", mcpText(res))
	}

	// With the gate off the person has said not to ask.
	a.gateForMCP = backend.NewToolGate(false, filepath.Join(home, "audit2.jsonl"))
	res, _ = ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superai_machine_act",
		Arguments: map[string]any{"machine": "nosuch", "action": "exec", "command": "uptime"}})
	if !strings.Contains(mcpText(res), `no machine called "nosuch"`) {
		t.Errorf("gate off: %s", mcpText(res))
	}
}

func TestPendingApprovalsAreListedNotAnswered(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := NewApp()
	ss := selfSession(t, a)

	empty, _ := callSelf(t, ss, "superai_approvals_pending", nil)
	if !strings.Contains(empty, `"pending":[]`) {
		t.Fatalf("nothing pending: %s", empty)
	}

	now := time.Now()
	done := make(chan backend.ApprovalDecision, 1)
	go func() {
		dec, _ := a.askToolApproval(context.Background(), backend.ApprovalRequest{
			ID: "ap-1", Tool: "fs_delete", Args: map[string]any{"path": "/srv/x", "token": "sk-very-secret"},
			SessionID: "conv-1", By: "orange2", AskedAt: now, ExpiresAt: now.Add(time.Minute),
		})
		done <- dec
	}()
	for deadline := time.Now().Add(5 * time.Second); len(a.pendingApprovals()) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("never pending")
		}
	}

	res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superai_approvals_pending", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatal(err, res)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct{ Pending []approvalLine }
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Pending) != 1 {
		t.Fatalf("pending %s", raw)
	}
	p := out.Pending[0]
	if p.ID != "ap-1" || p.Tool != "fs_delete" || p.By != "orange2" || p.Conversation != "conv-1" ||
		p.ExpiresInSeconds <= 0 || p.ExpiresInSeconds > 60 || !strings.Contains(p.Summary, "path=/srv/x") {
		t.Errorf("line %+v", p)
	}
	if strings.Contains(string(raw), "sk-very-secret") {
		t.Error("a secret argument was listed")
	}
	// Listing answers nothing, and no tool on /mcp can.
	if len(a.PendingToolApprovals()) != 1 {
		t.Error("listing resolved the approval")
	}
	for _, n := range toolNames(t, ss) {
		if strings.Contains(n, "resolve") || strings.Contains(n, "approve") {
			t.Errorf("%s could answer an approval", n)
		}
	}
	a.ResolveToolApproval("ap-1", false)
	if dec := <-done; dec.Allowed {
		t.Fatal("denied came back allowed")
	}
}

func TestHiveTasksListsAndDescribes(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := NewApp()
	ss := selfSession(t, a)
	board := a.tasks()
	old := board.Start("w1", backend.TaskOut, "an old order")
	board.Finish(old, backend.TaskDone, strings.Repeat("long answer ", 100), "")
	running := board.Start("w2", backend.TaskOut, "a running order")
	board.OnStop(running, func() {})
	peer := board.Start("w3", backend.TaskPeer, "between workers")

	type listOut struct {
		Tasks       []taskLine       `json:"tasks"`
		Delegations []delegationLine `json:"delegations"`
	}
	list := func(args map[string]any) listOut {
		t.Helper()
		res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superai_hive_tasks", Arguments: args})
		if err != nil || res.IsError {
			t.Fatal(err, mcpText(res))
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var out listOut
		_ = json.Unmarshal(raw, &out)
		return out
	}
	for _, c := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"all, newest first", map[string]any{}, []string{peer, running, old}},
		{"limit", map[string]any{"limit": 1}, []string{peer}},
		{"running only", map[string]any{"running_only": true}, []string{peer, running}},
	} {
		var got []string
		for _, tl := range list(c.args).Tasks {
			got = append(got, tl.ID)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for _, tl := range list(map[string]any{}).Tasks {
		if tl.Stoppable != (tl.ID == running) {
			t.Errorf("%s stoppable=%v", tl.Worker, tl.Stoppable)
		}
		if tl.ID == old && (len([]rune(tl.Result)) > 450 || tl.EndedAt.IsZero()) {
			t.Errorf("old line %+v", tl)
		}
	}

	for _, c := range []struct {
		id, want string
		isErr    bool
	}{
		{old, "long answer long answer", false},
		{running, `"stoppable":true`, false},
		{"nope", "no delegation or task", true},
	} {
		text, isErr := callSelf(t, ss, "superai_hive_tasks", map[string]any{"id": c.id})
		if isErr != c.isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s: err=%v %s", c.id, isErr, text)
		}
	}
}

// The queen's delegation over /mcp: the same route as her own hive_delegate,
// asynchronous, followed with superai_hive_tasks and stopped with
// superai_hive_task_cancel.
func TestDelegateOverMCP(t *testing.T) {
	h := newDelegateHive(t, "max", delegateThen(nextWeek))
	w1, w2 := newStubWorker(t, "w1"), newStubWorker(t, "w2")
	h.join(t, w1, []string{"noted"})
	ss := selfSession(t, h.a)

	delegate := func(args map[string]any) delegationView {
		t.Helper()
		res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superai_hive_delegate", Arguments: args})
		if err != nil || res.IsError {
			t.Fatal(err, mcpText(res))
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var v delegationView
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	follow := func(id string, until func(delegationView) bool) delegationView {
		t.Helper()
		var v delegationView
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			res, err := ss.CallTool(context.Background(), &mcp.CallToolParams{Name: "superai_hive_tasks", Arguments: map[string]any{"id": id}})
			if err != nil || res.IsError {
				t.Fatal(err, mcpText(res))
			}
			raw, _ := json.Marshal(res.StructuredContent)
			var out struct{ Delegation delegationView }
			_ = json.Unmarshal(raw, &out)
			if v = out.Delegation; until(v) {
				return v
			}
		}
		t.Fatalf("delegation %s stayed %+v", id, v)
		return v
	}
	piece := map[string]any{"task": "List the events from 2026-10-19 to 2026-10-25 in noted.", "needs": []string{"noted"}}

	// Quick: the answer comes back in the call.
	v := delegate(map[string]any{"tasks": []any{piece}, "wait_seconds": 20})
	if v.State != backend.TaskDone || len(v.Pieces) != 1 || v.Next != "" {
		t.Fatalf("delegation %+v", v)
	}
	p := v.Pieces[0]
	if p.Worker != "w1" || p.State != backend.TaskDone || !strings.Contains(p.Text, "REPORT-FROM-w1") || p.TaskID == "" ||
		!slices.Contains(p.Tools, "mcp_noted_list_events") {
		t.Fatalf("piece %+v", p)
	}
	var raw bool
	for _, s := range v.Steps {
		if s.Kind == "tool_call" && s.Tool == "mcp_noted_list_events" && s.Args["from"] == "2026-10-19" && s.Worker == "w1" {
			raw = true
		}
	}
	if !raw {
		t.Fatalf("steps %+v", v.Steps)
	}
	if h.ownCalls.Load() != 0 {
		t.Fatal("the queen did the work herself")
	}
	if got := h.a.hiveTaskList(5, false)["delegations"].([]delegationLine); len(got) != 1 || got[0].ID != v.ID || got[0].Done != 1 {
		t.Fatalf("listed %+v", got)
	}

	// Slow: the id comes back at once, and the delegation can be stopped.
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	w1.hold = hold
	v = delegate(map[string]any{"tasks": []any{piece}, "wait_seconds": 0})
	if v.State != backend.TaskRunning || !strings.HasPrefix(v.ID, delegationPrefix) || !strings.Contains(v.Next, v.ID) {
		t.Fatalf("not asynchronous: %+v", v)
	}
	follow(v.ID, func(v delegationView) bool { return v.Pieces[0].Worker == "w1" })
	seen := answerNext(t, h.a, true)
	if text, isErr := callSelf(t, ss, "superai_hive_task_cancel", map[string]any{"id": v.ID}); isErr {
		t.Fatalf("cancel: %s", text)
	}
	if card := <-seen; card.Tool != "hive_task_cancel" {
		t.Fatalf("card %+v", card)
	}
	v = follow(v.ID, func(v delegationView) bool { return v.State != backend.TaskRunning })
	if v.State != backend.TaskCancelled || h.ownCalls.Load() != 0 {
		t.Fatalf("after cancel %+v (queen calls %d)", v, h.ownCalls.Load())
	}

	// One order of a delegation, stopped by its task id: that piece is not
	// handed to the other worker or to the queen.
	h.join(t, w2, []string{"noted"})
	w2.hold = hold
	n1, n2 := len(w1.received()), len(w2.received())
	v = delegate(map[string]any{"tasks": []any{piece}, "wait_seconds": 0})
	var order string
	for deadline := time.Now().Add(10 * time.Second); order == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, tk := range h.a.tasks().Recent() {
			if tk.State == backend.TaskRunning && tk.Dir == backend.TaskOut && h.a.tasks().Stoppable(tk.ID) {
				order = tk.ID
			}
		}
	}
	if order == "" {
		t.Fatal("no stoppable order")
	}
	seen = answerNext(t, h.a, true)
	if text, isErr := callSelf(t, ss, "superai_hive_task_cancel", map[string]any{"id": order}); isErr {
		t.Fatalf("cancel order: %s", text)
	}
	<-seen
	v = follow(v.ID, func(v delegationView) bool { return v.State != backend.TaskRunning })
	if p := v.Pieces[0]; !strings.Contains(p.Reason, "stopped on request") || p.Fallback || len(p.Tried) != 1 {
		t.Fatalf("stopped piece %+v", p)
	}
	if got := len(w1.received()) - n1 + len(w2.received()) - n2; got != 1 || h.ownCalls.Load() != 0 {
		t.Fatalf("handed on: %d orders, queen calls %d", got, h.ownCalls.Load())
	}

	// Not on a queen: said so.
	t.Setenv("SUPERAI_HOME", t.TempDir())
	plain := selfSession(t, NewApp())
	if text, isErr := callSelf(t, plain, "superai_hive_delegate", map[string]any{"tasks": []any{piece}}); !isErr || !strings.Contains(text, "not a queen") {
		t.Errorf("standalone: %s", text)
	}
}
