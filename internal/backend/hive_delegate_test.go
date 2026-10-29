package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"
)

func TestTheQueenHandsOverHerWorkToolsAndKeepsTheRest(t *testing.T) {
	registry := []string{
		"mcp_noted_list_events", "mcp_noted_create_event", "mcp_websearch_basic", "web_search", "fetch_url",
		"bash", "fs_read", "shell_start", "knowledge_search", "knowledge_save", "memory_recall", "cli_agent_run",
		"delegate_to_subagent", "ingest_document",
		// kept
		"hive_delegate", "hive_members", "hive_command", "hive_task", "resolve_datetime", "notify_user",
		"ui_block_rules", "set_reminder", "schedule_prompt", "settings_get", "task_complete",
		"search_available_tools", "scratchpad_add", "remote_agent_run", "coding_agent_run", "mcp_servers",
		"memory_save", "add_record", "dashboard_save", "skill_cortexdb",
	}
	deny := WorkToolDenylist(registry)
	for _, want := range []string{"mcp_noted_list_events", "mcp_noted_create_event", "mcp_websearch_basic", "web_search",
		"fetch_url", "bash", "fs_read", "shell_start", "knowledge_search", "knowledge_save", "memory_recall",
		"cli_agent_run", "delegate_to_subagent", "ingest_document"} {
		if !slices.Contains(deny, want) {
			t.Errorf("%s is work, but the queen keeps it", want)
		}
	}
	for _, keep := range []string{"hive_delegate", "hive_members", "hive_command", "hive_task", "resolve_datetime",
		"notify_user", "ui_block_rules", "set_reminder", "schedule_prompt", "settings_get", "task_complete",
		"search_available_tools", "scratchpad_add", "remote_agent_run", "coding_agent_run", "mcp_servers",
		"memory_save", "add_record", "dashboard_save", "skill_cortexdb"} {
		if slices.Contains(deny, keep) {
			t.Errorf("%s is the queen's own, but it was taken from her", keep)
		}
	}
}

func TestDelegationModes(t *testing.T) {
	t.Setenv("SUPERLEO_HIVE_DELEGATION", "")
	cases := []struct {
		h    HiveSettings
		want string
	}{
		{HiveSettings{Role: HiveRoleQueen}, DelegationMax},
		{HiveSettings{Role: HiveRoleQueen, Delegation: "auto"}, DelegationAuto},
		{HiveSettings{Role: HiveRoleQueen, Delegation: " OFF "}, DelegationOff},
		{HiveSettings{Role: HiveRoleQueen, Delegation: "sometimes"}, DelegationMax},
		{HiveSettings{Role: HiveRoleWorker, Delegation: "max"}, DelegationOff},
		{HiveSettings{}, DelegationOff},
	}
	for _, c := range cases {
		if got := c.h.DelegationMode(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.h, got, c.want)
		}
	}
	t.Setenv("SUPERLEO_HIVE_DELEGATION", "off")
	if got := (HiveSettings{Role: HiveRoleQueen, Delegation: "max"}).DelegationMode(); got != DelegationOff {
		t.Errorf("the environment did not win: %q", got)
	}
}

func TestTheQueensPromptFollowsTheMode(t *testing.T) {
	t.Setenv("SUPERLEO_HIVE_DELEGATION", "")
	max := hiveSection(HiveSettings{Role: HiveRoleQueen, Name: "q"})
	if !strings.Contains(max, "hive_delegate") || strings.Contains(max, "Answer small questions yourself") {
		t.Errorf("max: %q", max)
	}
	off := hiveSection(HiveSettings{Role: HiveRoleQueen, Name: "q", Delegation: "off"})
	if strings.Contains(off, "hive_delegate") || !strings.Contains(off, "Answer small questions yourself") {
		t.Errorf("off: %q", off)
	}
}

// fakeEnv is a hive of named workers whose answers the test decides.
type fakeEnv struct {
	mu      sync.Mutex
	workers []string
	has     map[string][]string
	answer  func(ctx context.Context, worker, prompt string) (RemoteResult, error)
	asked   []string
	self    []string
}

func (f *fakeEnv) env() DelegateEnv {
	return DelegateEnv{
		Candidates: func(needs []string) []string {
			var out []string
			for _, w := range f.workers {
				if HasAll(f.has[w], needs) {
					out = append(out, w)
				}
			}
			return out
		},
		Run: func(ctx context.Context, worker, prompt string) (RemoteResult, error) {
			f.mu.Lock()
			f.asked = append(f.asked, worker)
			f.mu.Unlock()
			return f.answer(ctx, worker, prompt)
		},
		Self: func(ctx context.Context, prompt string) (RemoteResult, error) {
			f.mu.Lock()
			f.self = append(f.self, prompt)
			f.mu.Unlock()
			if tr := WorkerTraceFrom(ctx); tr != nil {
				tr(WorkerToolEvent{Kind: "tool_call", Tool: "mcp_noted_list_events", OK: true})
			}
			return RemoteResult{Text: "the queen's own answer"}, nil
		},
		SelfName: "q",
		Timeout:  2 * time.Second,
	}
}

func TestTwoPiecesRunAtOnceOnTwoWorkersAndComeBackTogether(t *testing.T) {
	both := make(chan struct{})
	var arrived sync.WaitGroup
	arrived.Add(2)
	go func() { arrived.Wait(); close(both) }()
	f := &fakeEnv{workers: []string{"w1", "w2"}}
	f.answer = func(ctx context.Context, worker, prompt string) (RemoteResult, error) {
		arrived.Done()
		// Each waits for the other: only a parallel run gets past this.
		select {
		case <-both:
		case <-time.After(time.Second):
			return RemoteResult{Failed: true, Reason: "ran alone"}, nil
		}
		if tr := WorkerTraceFrom(ctx); tr != nil {
			tr(WorkerToolEvent{Worker: worker, Kind: "tool_call", Tool: "tool_of_" + worker, OK: true})
		}
		return RemoteResult{Text: "report from " + worker}, nil
	}
	var traced []string
	var tmu sync.Mutex
	ctx := WithWorkerTrace(context.Background(), func(ev WorkerToolEvent) {
		tmu.Lock()
		traced = append(traced, ev.Label())
		tmu.Unlock()
	})
	res := RunDelegated(ctx, f.env(), []DelegateTask{{Task: "find A"}, {Task: "find B"}})
	if res[0].Worker == res[1].Worker {
		t.Fatalf("both pieces went to %s", res[0].Worker)
	}
	for _, r := range res {
		if r.Failed || r.Fallback || r.Text != "report from "+r.Worker || len(r.Tools) != 1 {
			t.Fatalf("piece %+v", r)
		}
	}
	out := FormatDelegated(res)
	if !strings.Contains(out, "2 of 2") || !strings.Contains(out, "report from w1") || !strings.Contains(out, "report from w2") {
		t.Fatalf("merged report: %s", out)
	}
	steps := DelegatedSteps(out)
	slices.Sort(steps)
	if strings.Join(steps, "|") != "w1 · tool_of_w1|w2 · tool_of_w2" {
		t.Fatalf("steps %q", steps)
	}
	if len(traced) != 2 {
		t.Fatalf("the queen's run saw %q", traced)
	}
}

func TestAFailedWorkerIsReplacedThenTheQueenStepsIn(t *testing.T) {
	f := &fakeEnv{workers: []string{"w1", "w2"}}
	f.answer = func(ctx context.Context, worker, prompt string) (RemoteResult, error) {
		if worker == "w1" {
			return RemoteResult{Failed: true, Reason: "lost the worker"}, nil
		}
		return RemoteResult{Text: "w2 did it"}, nil
	}
	res := RunDelegated(context.Background(), f.env(), []DelegateTask{{Task: "x"}})
	if res[0].Worker != "w2" || res[0].Fallback || res[0].Text != "w2 did it" {
		t.Fatalf("not replaced: %+v", res[0])
	}

	// Both fail: the queen does it, and says why.
	f.asked = nil
	f.answer = func(ctx context.Context, worker, prompt string) (RemoteResult, error) {
		return RemoteResult{}, errors.New("connection refused")
	}
	res = RunDelegated(context.Background(), f.env(), []DelegateTask{{Task: "x"}})
	r := res[0]
	if !r.Fallback || r.Worker != "q" || r.Text != "the queen's own answer" || !strings.Contains(r.Why, "w1: connection refused") {
		t.Fatalf("fallback: %+v", r)
	}
	if len(f.asked) != 2 {
		t.Fatalf("tried %v, want two workers", f.asked)
	}
	out := FormatDelegated(res)
	if !strings.Contains(out, "Done by the queen herself") || !strings.Contains(out, "q · mcp_noted_list_events") {
		t.Fatalf("report: %s", out)
	}
}

func TestATimedOutWorkerIsNotWaitedOnForever(t *testing.T) {
	f := &fakeEnv{workers: []string{"w1"}}
	f.answer = func(ctx context.Context, worker, prompt string) (RemoteResult, error) {
		<-ctx.Done()
		return RemoteResult{Failed: true, Reason: "stopped"}, nil
	}
	env := f.env()
	env.Timeout = 50 * time.Millisecond
	res := RunDelegated(context.Background(), env, []DelegateTask{{Task: "x"}})
	if !res[0].Fallback || !strings.Contains(res[0].Why, "no answer within") {
		t.Fatalf("%+v", res[0])
	}
}

func TestNoWorkerWithTheServerMeansTheQueenDoesIt(t *testing.T) {
	f := &fakeEnv{workers: []string{"w1"}, has: map[string][]string{"w1": {"cortexdb"}}}
	f.answer = func(context.Context, string, string) (RemoteResult, error) {
		t.Fatal("a worker without noted was given noted work")
		return RemoteResult{}, nil
	}
	res := RunDelegated(context.Background(), f.env(), []DelegateTask{{Task: "list events", Needs: []string{"noted"}}})
	if !res[0].Fallback || !strings.Contains(res[0].Why, "no live worker has noted") {
		t.Fatalf("%+v", res[0])
	}
}

func TestAStoppedRequestDoesNotFallBack(t *testing.T) {
	f := &fakeEnv{workers: []string{"w1"}}
	ctx, cancel := context.WithCancel(context.Background())
	f.answer = func(ctx context.Context, worker, prompt string) (RemoteResult, error) {
		cancel()
		return RemoteResult{Failed: true, Reason: "stopped"}, nil
	}
	res := RunDelegated(ctx, f.env(), []DelegateTask{{Task: "x"}})
	if res[0].Fallback || !res[0].Failed || len(f.self) != 0 {
		t.Fatalf("%+v self=%v", res[0], f.self)
	}
}

func TestNeedsAreReadOffTheTask(t *testing.T) {
	got := InferNeeds("用 noted 查下周的例会 (mcp_noted_list_events)", []string{"noted", "cortexdb", "websearch"})
	if strings.Join(got, ",") != "noted" {
		t.Fatalf("got %v", got)
	}
	if got := InferNeeds("annotated notes", []string{"noted"}); len(got) != 0 {
		t.Fatalf("a word containing the name matched: %v", got)
	}
	if !HasAll(nil, []string{"noted"}) || HasAll([]string{}, []string{"noted"}) || !HasAll([]string{"noted", "x"}, []string{"noted"}) {
		t.Fatal("HasAll")
	}
}

func TestAReopenedConversationShowsWhoDidWhat(t *testing.T) {
	report := FormatDelegated([]DelegateResult{{Worker: "orange2", Tools: []string{"mcp_noted_list_events", "mcp_noted_list_events", "mcp_noted_get_note"}, Text: "两个例会"}})
	msgs := []domain.Message{
		{Role: "user", Content: "下周有哪些例会？"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "c1", Type: "function", Function: domain.FunctionCall{Name: DelegateToolName, Arguments: map[string]any{}}}}},
		{Role: "tool", ToolCallID: "c1", Content: report},
		{Role: "assistant", Content: "下周有两个例会。"},
	}
	turns := visibleTurns(msgs)
	last := turns[len(turns)-1]
	want := []string{"Called hive_delegate", "orange2 · mcp_noted_list_events", "orange2 · mcp_noted_get_note"}
	if strings.Join(last.Steps, "|") != strings.Join(want, "|") {
		t.Fatalf("steps %q", last.Steps)
	}
	// Another tool's result is not read for steps, whatever it says.
	msgs[1].ToolCalls[0].Function.Name = "web_search"
	if got := visibleTurns(msgs)[1].Steps; len(got) != 1 {
		t.Fatalf("a non-delegate result became steps: %q", got)
	}
}

func TestAWorkerSaysWhatItCanReachAndTheQueenKeepsIt(t *testing.T) {
	h := NewHive("q", time.Minute)
	base := HiveHello{Protocol: HiveProtocol, Role: HiveRoleWorker, URL: "http://w:1"}
	old, none, some := base, base, base
	old.Name, none.Name, some.Name = "old", "none", "some"
	none.MCP, some.MCP = []string{}, []string{"noted"}
	for _, hh := range []HiveHello{old, none, some} {
		raw, _ := json.Marshal(hh)
		var wire HiveHello
		_ = json.Unmarshal(raw, &wire) // as it travels
		if _, err := h.Join(wire); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string][]string{}
	for _, m := range h.Members() {
		got[m.Name] = m.MCP
	}
	if got["old"] != nil || got["none"] == nil || len(got["none"]) != 0 || strings.Join(got["some"], ",") != "noted" {
		t.Fatalf("members' servers: %#v", got)
	}
}

func TestTheQueensServerIsNotCopiedOverTheWorkersOwn(t *testing.T) {
	worker := t.TempDir()
	t.Setenv("SUPERLEO_HOME", worker)
	t.Setenv("NOTED_TOKEN", "x")
	// The cluster gave this worker noted already.
	os.MkdirAll(filepath.Join(worker, "data"), 0o755)
	os.WriteFile(filepath.Join(worker, "data", "mcpServers.json"), []byte(`{"mcpServers":{"noted":{"type":"http","url":"https://noted.example/mcp","headers":{"Authorization":"Bearer ${NOTED_TOKEN}"}}}}`), 0o600)
	ab := HiveAbilities{MCP: map[string]any{
		"noted": map[string]any{"type": "http", "url": "https://noted.example/mcp"},
		"other": map[string]any{"type": "http", "url": "https://other.example/mcp"},
	}}
	res, err := ApplyAbilities(ab, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.MCP, ",") != "other" || res.Skipped["noted"] == "" {
		t.Fatalf("applied %+v", res)
	}
	raw, _ := os.ReadFile(HiveMCPFile())
	if strings.Contains(string(raw), "noted") {
		t.Fatalf("a second noted entry was written: %s", raw)
	}
}

func TestAWorkersToolsAndApprovalsReachTheQueen(t *testing.T) {
	f := newFakeWorker(t, "tok")
	var resolved []any
	var rmu sync.Mutex
	gotResolve := make(chan struct{})
	f.srv.Config.Handler.(*http.ServeMux).HandleFunc("/api/rpc/ResolveToolApproval", func(w http.ResponseWriter, r *http.Request) {
		var args []any
		_ = json.NewDecoder(r.Body).Decode(&args)
		rmu.Lock()
		resolved = args
		rmu.Unlock()
		close(gotResolve)
		_ = json.NewEncoder(w).Encode("ok")
	})
	f.onSend = func(session, _ string, r *fakeWorker) string {
		go func() {
			time.Sleep(30 * time.Millisecond)
			r.emit("tool:approval", map[string]any{"id": "other", "session": "hive:someone-else", "tool": "bash"})
			r.emit("tool:approval", map[string]any{"id": "ap-1", "session": session, "tool": "mcp_noted_delete_event"})
			r.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_call", "tool": "mcp_noted_list_events", "args": map[string]any{"from": "2026-10-19"}})
			r.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_result", "tool": "mcp_noted_list_events", "result": map[string]any{"events": []any{}}})
			r.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_call", "tool": "mcp_noted_delete_event"})
			r.emit("chat:event", map[string]any{"requestId": "req-1", "type": "tool_result", "tool": "mcp_noted_delete_event", "result": map[string]any{"error": "not found"}})
			<-gotResolve
			r.emit("chat:done", map[string]any{"requestId": "req-1", "final": "done"})
		}()
		return "req-1"
	}
	var events []WorkerToolEvent
	var asked []string
	var emu sync.Mutex
	res := askWorker(context.Background(), workerTarget{name: "w1", url: f.srv.URL, token: "tok",
		tools: func(ev WorkerToolEvent) { emu.Lock(); events = append(events, ev); emu.Unlock() },
		approve: func(_ context.Context, p map[string]any) bool {
			emu.Lock()
			asked = append(asked, fmt.Sprint(p["id"]))
			emu.Unlock()
			return true
		}}, "clean up next week", nil)
	if res.Failed || !strings.HasPrefix(res.Text, "done\n") || !strings.Contains(res.Text, `mcp_noted_list_events → {"events":[]}`) {
		t.Fatalf("the report must carry what the tools returned: %+v", res)
	}
	emu.Lock()
	defer emu.Unlock()
	if strings.Join(asked, ",") != "ap-1" {
		t.Fatalf("asked about %v", asked)
	}
	rmu.Lock()
	if len(resolved) != 2 || resolved[0] != "ap-1" || resolved[1] != true {
		t.Fatalf("resolved %v", resolved)
	}
	rmu.Unlock()
	if len(events) != 4 || events[0].Label() != "w1 · mcp_noted_list_events" || events[0].Args["from"] != "2026-10-19" ||
		!events[1].OK || events[3].OK || events[3].Error != "not found" {
		t.Fatalf("events %+v", events)
	}
}

// A piece whose order was stopped on request is not given to another worker or
// to the queen: stopping it is the answer.
func TestAStoppedPieceIsNotHandedOn(t *testing.T) {
	f := &fakeEnv{workers: []string{"w1", "w2"}}
	f.answer = func(ctx context.Context, worker, prompt string) (RemoteResult, error) {
		return RemoteResult{Failed: true, Cancelled: true, Reason: ErrTaskStopped.Error(), TaskID: "t-" + worker}, nil
	}
	res := RunDelegated(context.Background(), f.env(), []DelegateTask{{Task: "x"}})
	r := res[0]
	if !r.Failed || r.Fallback || r.Worker != "w1" || r.TaskID != "t-w1" || r.Reason != ErrTaskStopped.Error() {
		t.Fatalf("%+v", r)
	}
	if len(f.asked) != 1 || len(f.self) != 0 {
		t.Fatalf("handed on: asked %v, self %v", f.asked, f.self)
	}
}

// What a worker writes reaches the queen while it writes it, gathered into a
// few pieces rather than one per streamed token, and the last piece arrives
// before the report does.
func TestAWorkersWordsReachTheQueenAsItWrites(t *testing.T) {
	f := newFakeWorker(t, "tok")
	f.onSend = func(_, _ string, r *fakeWorker) string {
		go func() {
			time.Sleep(30 * time.Millisecond)
			r.emit("chat:event", map[string]any{"requestId": "someone-else", "type": "partial", "content": "not mine"})
			for _, w := range []string{"内存", "一共", "32 GB", "，"} {
				r.emit("chat:event", map[string]any{"requestId": "req-1", "type": "partial", "content": w})
			}
			time.Sleep(workerWordsEvery + 50*time.Millisecond)
			r.emit("chat:event", map[string]any{"requestId": "req-1", "type": "partial", "content": "swap 快满了"})
			r.emit("chat:done", map[string]any{"requestId": "req-1", "final": "内存一共 32 GB，swap 快满了"})
		}()
		return "req-1"
	}
	var words []string
	var mu sync.Mutex
	res := askWorker(context.Background(), workerTarget{name: "mac", url: f.srv.URL, token: "tok",
		tools: func(ev WorkerToolEvent) {
			if ev.Kind == "text" {
				mu.Lock()
				words = append(words, ev.Text)
				mu.Unlock()
			}
		}}, "memory?", nil)
	if res.Failed {
		t.Fatalf("%+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(words, ""); got != "内存一共32 GB，swap 快满了" {
		t.Fatalf("words %q", words)
	}
	if len(words) < 2 || len(words) > 4 {
		t.Fatalf("words came in %d pieces: %q", len(words), words)
	}
}
