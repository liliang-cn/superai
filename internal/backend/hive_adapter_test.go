package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scriptEngine is an engine whose behaviour a test writes inline.
type scriptEngine struct {
	run func(ctx context.Context, prompt string, emit func(AdapterEvent)) (string, error)
}

func (s scriptEngine) Describe() string { return "script" }
func (s scriptEngine) Run(ctx context.Context, p string, emit func(AdapterEvent)) (string, error) {
	return s.run(ctx, p, emit)
}

// A queen's runner pointed at an adapter: the same code path a real queen uses.
func commandAdapter(t *testing.T, a *Adapter, token string) (*RemoteRunner, *TaskBoard, *pulseLog) {
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	b, _ := boardWithLog()
	var pl pulseLog
	b.SetPulse(pl.add)
	run := NewRemoteRunner(RemoteAgents{Enabled: true, Agents: map[string]RemoteAgent{"ext": {URL: srv.URL, Token: token}}})
	run.SetBoard(b)
	return run, b, &pl
}

func TestAnAgentThatIsNotSuperAIAnswersAQueen(t *testing.T) {
	a := &Adapter{Token: "tok", Engine: scriptEngine{func(_ context.Context, p string, emit func(AdapterEvent)) (string, error) {
		time.Sleep(150 * time.Millisecond) // after the queen has the turn id
		emit(AdapterEvent{Type: "tool_call", Tool: "Bash"})
		emit(AdapterEvent{Type: "tool_result", Result: "Pro.local"})
		return "done: " + p, nil
	}}}
	run, board, pulses := commandAdapter(t, a, "tok")
	res, _ := run.Run(context.Background(), "ext", "hostname?")
	if res.Failed || res.Text != "done: hostname?" {
		t.Fatalf("%+v", res)
	}
	k := board.Recent()[0]
	if k.State != TaskDone || k.Tools != 1 {
		t.Fatalf("the queen's record of it: %+v", k)
	}
	var kinds []string
	for _, p := range pulses.all() {
		if p.Kind == "thinking" {
			continue // taking the order; not what this is about
		}
		kinds = append(kinds, p.Kind+":"+p.Tool)
	}
	if strings.Join(kinds, " ") != "tool:Bash result:" {
		t.Fatalf("the agent's events did not become beams: %v", kinds)
	}
}

func TestAnAdapterRefusesAnyoneWithoutItsToken(t *testing.T) {
	a := &Adapter{Token: "right", Engine: scriptEngine{func(context.Context, string, func(AdapterEvent)) (string, error) {
		t.Error("an unauthenticated order reached the agent")
		return "x", nil
	}}}
	run, _, _ := commandAdapter(t, a, "wrong")
	res, _ := run.Run(context.Background(), "ext", "go")
	if !res.Failed || !strings.Contains(res.Reason, "401") {
		t.Fatalf("%+v", res)
	}
	// And with no token configured, nothing gets in at all.
	open := &Adapter{Engine: scriptEngine{func(context.Context, string, func(AdapterEvent)) (string, error) { return "x", nil }}}
	run2, _, _ := commandAdapter(t, open, "")
	if res, _ := run2.Run(context.Background(), "ext", "go"); !res.Failed {
		t.Fatal("an adapter with no token accepted an order")
	}
}

func TestAnAgentsFailureIsAFailureNotAnAnswer(t *testing.T) {
	a := &Adapter{Token: "t", Engine: scriptEngine{func(context.Context, string, func(AdapterEvent)) (string, error) {
		return "", fmt.Errorf("model unavailable")
	}}}
	run, _, _ := commandAdapter(t, a, "t")
	res, _ := run.Run(context.Background(), "ext", "go")
	if !res.Failed || !strings.Contains(res.Reason, "model unavailable") {
		t.Fatalf("%+v", res)
	}
	silent := &Adapter{Token: "t", Engine: scriptEngine{func(context.Context, string, func(AdapterEvent)) (string, error) { return "  ", nil }}}
	run2, _, _ := commandAdapter(t, silent, "t")
	if res, _ := run2.Run(context.Background(), "ext", "go"); !res.Failed {
		t.Fatal("an empty answer was reported as success")
	}
}

func TestStoppingTheQueenStopsTheForeignAgent(t *testing.T) {
	var stopped atomic.Bool
	a := &Adapter{Token: "t", Engine: scriptEngine{func(ctx context.Context, _ string, _ func(AdapterEvent)) (string, error) {
		<-ctx.Done()
		stopped.Store(true)
		return "", ctx.Err()
	}}}
	run, _, _ := commandAdapter(t, a, "t")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(250 * time.Millisecond); cancel() }()
	res, _ := run.Run(ctx, "ext", "long job")
	if !res.Failed {
		t.Fatalf("%+v", res)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !stopped.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !stopped.Load() {
		t.Fatal("the agent kept running after the queen stopped")
	}
}

func TestOneOrderAtATimeByDefaultAndTheRestWait(t *testing.T) {
	var inFlight, peak atomic.Int32
	a := &Adapter{Token: "t", Engine: scriptEngine{func(_ context.Context, p string, _ func(AdapterEvent)) (string, error) {
		n := inFlight.Add(1)
		for {
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(120 * time.Millisecond)
		inFlight.Add(-1)
		return "ok " + p, nil
	}}}
	run, _, _ := commandAdapter(t, a, "t")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if res, _ := run.Run(context.Background(), "ext", fmt.Sprint("job", i)); res.Failed {
				t.Errorf("job %d: %+v", i, res)
			}
		}(i)
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatalf("%d orders ran at once on a one-at-a-time adapter", peak.Load())
	}
}

func TestExecEngineRunsACommandWithThePromptAsOneArgument(t *testing.T) {
	e := &ExecEngine{Argv: []string{"sh", "-c", `printf 'you said: %s' "$1"`, "sh", "{prompt}"}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	// Shell metacharacters in the prompt must arrive as text, not run.
	out, err := e.Run(context.Background(), `hi; echo INJECTED $(whoami)`, func(AdapterEvent) {})
	if err != nil || out != `you said: hi; echo INJECTED $(whoami)` {
		t.Fatalf("%q %v", out, err)
	}
}

func TestExecEngineValidation(t *testing.T) {
	for name, e := range map[string]*ExecEngine{
		"no command":          {},
		"no placeholder":      {Argv: []string{"echo", "x"}},
		"two placeholders":    {Argv: []string{"echo", "{prompt}", "{prompt}"}},
		"unknown output mode": {Argv: []string{"echo", "{prompt}"}, Mode: "xml"},
	} {
		if err := e.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestExecEngineFailureCarriesWhatTheCommandSaid(t *testing.T) {
	e := &ExecEngine{Argv: []string{"sh", "-c", "echo 'not logged in' >&2; exit 3", "{prompt}"}}
	_, err := e.Run(context.Background(), "x", func(AdapterEvent) {})
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("%v", err)
	}
}

func TestCancellingKillsTheAgentsChildrenToo(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-alive")
	// The command starts a child that would touch the marker after a while.
	script := fmt.Sprintf("(sleep 2; touch %s) & wait", marker)
	e := &ExecEngine{Argv: []string{"sh", "-c", script, "{prompt}"}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	e.Run(ctx, "x", func(AdapterEvent) {})
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the agent's child outlived the cancel")
	}
}

const claudeSample = `{"type":"system","subtype":"hook_started"}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"hostname"}}]}}
{"type":"rate_limit_event"}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x","content":"Pro.local"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"It is Pro.local"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"It is Pro.local"}`

func TestClaudeStreamLinesBecomeEventsAndAnAnswer(t *testing.T) {
	var got []string
	final := ""
	for _, line := range strings.Split(claudeSample, "\n") {
		evs, res, fin, _ := parseClaudeLine(line)
		for _, e := range evs {
			got = append(got, e.Type+":"+e.Tool)
		}
		if fin {
			final = res
		}
	}
	if strings.Join(got, " ") != "tool_call:Bash tool_result: partial:" || final != "It is Pro.local" {
		t.Fatalf("%v %q", got, final)
	}
	if _, _, fin, isErr := parseClaudeLine(`{"type":"result","is_error":true,"result":"boom"}`); !fin || !isErr {
		t.Fatal("an error result was not recognised")
	}
	if evs, _, fin, _ := parseClaudeLine("not json at all"); len(evs) != 0 || fin {
		t.Fatal("garbage produced events")
	}
}

func TestClaudeModeRunsTheCommandAndReadsItsStream(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "sample.jsonl")
	os.WriteFile(f, []byte(claudeSample+"\n"), 0o644)
	e := &ExecEngine{Argv: []string{"cat", f, "{prompt}"}, Mode: "claude"}
	// cat of the prompt (a nonexistent path) errors on stderr but the stream
	// on stdout is what counts; use sh to keep it clean.
	e.Argv = []string{"sh", "-c", `cat "$1"`, "sh", f, "{prompt}"}
	var kinds []string
	out, err := e.Run(context.Background(), "ignored", func(ev AdapterEvent) { kinds = append(kinds, ev.Type) })
	if err != nil || out != "It is Pro.local" {
		t.Fatalf("%q %v", out, err)
	}
	if strings.Join(kinds, " ") != "tool_call tool_result partial" {
		t.Fatalf("%v", kinds)
	}
}

func TestOpenAIEngineStreamsAModelsAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" || r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range []string{"Hel", "lo ", "there"} {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", c)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	e := &OpenAIEngine{BaseURL: srv.URL + "/v1", Key: "k", Model: "m"}
	var chunks int
	out, err := e.Run(context.Background(), "hi", func(AdapterEvent) { chunks++ })
	if err != nil || out != "Hello there" || chunks != 3 {
		t.Fatalf("%q %d %v", out, chunks, err)
	}
	bad := &OpenAIEngine{BaseURL: srv.URL + "/v1", Key: "wrong", Model: "m"}
	if _, err := bad.Run(context.Background(), "hi", func(AdapterEvent) {}); err == nil {
		t.Fatal("a refused request was reported as an answer")
	}
}

func TestAForeignWorkerJoinsWithALabelAndIsThenCommanded(t *testing.T) {
	a := &Adapter{Token: "w-tok", Engine: scriptEngine{func(_ context.Context, p string, _ func(AdapterEvent)) (string, error) {
		return "ext answers " + p, nil
	}}}
	worker := httptest.NewServer(a.Handler())
	defer worker.Close()

	q := NewHive("q", 40*time.Millisecond)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/hive/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer q-tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var x HiveHello
		jsonDecode(r, &x)
		wl, err := q.Join(x)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonEncode(w, wl)
	})
	queen := httptest.NewServer(mux)
	defer queen.Close()

	ann := &Announcer{
		Settings: HiveSettings{Role: HiveRoleWorker, Name: "claude-mac", JoinURL: queen.URL, JoinToken: "q-tok", AdvertiseURL: worker.URL},
		Token:    "w-tok", Engine: a.Engine.Describe(), About: "Claude Code on the Mac",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ann.Run(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for len(q.Members()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ms := q.Members()
	if len(ms) != 1 || ms[0].Engine != "script" {
		t.Fatalf("%+v", ms)
	}
	ag := q.Agents()["claude-mac"]
	if !strings.Contains(ag.About, "Claude Code on the Mac") || ag.Token != "w-tok" {
		t.Fatalf("%+v", ag)
	}

	run := NewRemoteRunner(RemoteAgents{})
	run.SetRoster(q.Agents)
	res, _ := run.Run(context.Background(), "claude-mac", "ping")
	if res.Failed || res.Text != "ext answers ping" {
		t.Fatalf("%+v", res)
	}
}

func jsonDecode(r *http.Request, v any)       { json.NewDecoder(r.Body).Decode(v) }
func jsonEncode(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }

func TestStdinCarriesAnOrderThatWouldBeCodeAsAnArgument(t *testing.T) {
	// The shape used over ssh: the remote command reads the order from stdin, so
	// a quote or a $(...) in it is text. Here sh stands in for the remote shell.
	e := &ExecEngine{Argv: []string{"sh", "-c", `printf 'got: %s' "$(cat)"`}, Stdin: true}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	evil := `it's "quoted"; $(echo INJECTED) ` + "`echo INJECTED`"
	out, err := e.Run(context.Background(), evil, func(AdapterEvent) {})
	if err != nil || out != "got: "+evil {
		t.Fatalf("%q %v", out, err)
	}
	if (&ExecEngine{Argv: []string{"cat", "{prompt}"}, Stdin: true}).Validate() == nil {
		t.Fatal("stdin with a {prompt} argument was accepted")
	}
}

func TestAWorkerInTheHiveBeatsAConfiguredAgentOfTheSameName(t *testing.T) {
	// openclaw is a default ssh agent in the settings, switched off; then an
	// openclaw joins the hive. Orders to that name must reach the one that
	// joined, not the disabled default.
	a := &Adapter{Token: "t", Engine: scriptEngine{func(context.Context, string, func(AdapterEvent)) (string, error) {
		return "from the hive", nil
	}}}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	run := NewRemoteRunner(RemoteAgents{Enabled: false, Agents: map[string]RemoteAgent{
		"openclaw": {Hosts: []string{"sds@x"}, Command: []string{"openclaw", "{prompt}"}},
	}})
	run.SetRoster(func() map[string]RemoteAgent {
		return map[string]RemoteAgent{"openclaw": {URL: srv.URL, Token: "t"}}
	})
	res, _ := run.Run(context.Background(), "openclaw", "hi")
	if res.Failed || res.Text != "from the hive" {
		t.Fatalf("%+v", res)
	}
	// With nobody of that name in the hive the configured one is what answers,
	// which here is refused because remote agents are off.
	run.SetRoster(func() map[string]RemoteAgent { return nil })
	if res, _ := run.Run(context.Background(), "openclaw", "hi"); !res.Failed {
		t.Fatalf("%+v", res)
	}
}
