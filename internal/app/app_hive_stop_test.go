package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
)

// tools catches what a register function adds, to call it as the model would.
type tools map[string]func(context.Context, map[string]any) (any, error)

func (t tools) AddToolWithMetadata(name, _ string, _ map[string]any, fn func(context.Context, map[string]any) (any, error), _ agent.ToolMetadata) {
	t[name] = fn
}

// Both are the queen's: in her own run, kept when she hands her work tools to
// the workers, and on the queen's /mcp scope.
func TestTheQueenCanStopHerOrdersAndMessageOutsideAgents(t *testing.T) {
	h := newDelegateHive(t, "max", func(llmRequest) (string, map[string]any) { return "", map[string]any{"text": "ok"} })
	reg := h.a.svc.Agent().GetToolRegistry()
	for _, name := range []string{"hive_task_cancel", "remote_agent_send"} {
		if !reg.Has(name) {
			t.Errorf("the queen has no %s", name)
		}
		if !backend.QueenKeepsTool(name) {
			t.Errorf("%s would be taken from the queen in max mode", name)
		}
	}
	for _, name := range []string{"superleo_hive_task_cancel", "superleo_remote_agent_send"} {
		if !slices.Contains(queenMCPTools, name) {
			t.Errorf("a queen thinking through a CLI lacks %s", name)
		}
	}
}

// A coding run the queen handed out is stopped by its task id: the run is
// cancelled where it runs, and the order settles as cancelled.
func TestStoppingAnOrderStopsItsCodingRun(t *testing.T) {
	noDiscovery(t)
	sleeper := fakeCLI(t, "sleeper", `sleep 30; echo late`)
	a := codingApp(t, backend.ExternalAgents{Unattended: true, Custom: []backend.CodingTool{{ID: "sleeper", Name: "Sleeper", Command: sleeper + " {prompt}"}}})
	tl := tools{}
	a.registerQueenOrderTools(tl)

	type result struct {
		text   string
		failed bool
		id     string
	}
	out := make(chan result, 1)
	go func() {
		text, failed, _, id := a.delegateOutsideID(context.Background(), "sleeper", "build the thing")
		out <- result{text, failed, id}
	}()

	var id string
	deadline := time.Now().Add(10 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		for _, tk := range a.tasks().Recent() {
			if tk.Worker == "sleeper" && tk.State == backend.TaskRunning && a.tasks().Stoppable(tk.ID) {
				id = tk.ID
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("the order never showed on the board as stoppable")
	}
	if msg, err := tl["hive_task_cancel"](context.Background(), map[string]any{"id": id}); err != nil || !strings.Contains(msg.(string), id) {
		t.Fatalf("%v %v", msg, err)
	}
	select {
	case r := <-out:
		if !r.failed || r.id != id {
			t.Fatalf("%+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the coding run went on after its order was stopped")
	}
	if tk, _ := a.tasks().Get(id); tk.State != backend.TaskCancelled {
		t.Fatalf("the order is %s, want cancelled", tk.State)
	}
	runs := a.CLIRuns()
	if len(runs) != 1 || runs[0].State != "cancelled" {
		t.Fatalf("runs %+v", runs)
	}

	if _, err := tl["hive_task_cancel"](context.Background(), map[string]any{"id": id}); err == nil {
		t.Fatal("a finished order was stopped twice")
	}
	if _, err := tl["hive_task_cancel"](context.Background(), map[string]any{"id": "nope"}); err == nil {
		t.Fatal("an unknown id was accepted")
	}
}

// remote_agent_send reaches an agent outside the hive and says the task id;
// a name nobody answers to, or a hive worker, is refused.
func TestAMessageToAnOutsideAgentComesBackWithItsTask(t *testing.T) {
	noDiscovery(t)
	echo := fakeCLI(t, "echoer", `printf 'got: %s' "$1"`)
	a := codingApp(t, backend.ExternalAgents{Unattended: true, Custom: []backend.CodingTool{{ID: "echoer", Name: "Echoer", Command: echo + " {prompt}"}}})
	tl := tools{}
	a.registerQueenOrderTools(tl)

	msg, err := tl["remote_agent_send"](context.Background(), map[string]any{"agent": "echoer", "message": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if s := msg.(string); !strings.Contains(s, "got: hello") || !strings.Contains(s, "task ") {
		t.Fatalf("%q", s)
	}
	if _, err := tl["remote_agent_send"](context.Background(), map[string]any{"agent": "nobody", "message": "hi"}); err == nil {
		t.Fatal("a name nobody answers to was accepted")
	}
}

// The far side of a link ask stops when core says so.
func TestALinkAskStopsWhenCoreSaysSo(t *testing.T) {
	a := &App{}
	if got := a.CancelRemoteAsk("unknown"); got == "ok" {
		t.Fatal("an unknown ask was cancelled")
	}
	ctx, cancel := context.WithCancel(context.Background())
	remoteAsks.mu.Lock()
	if remoteAsks.cancels == nil {
		remoteAsks.cancels = map[string]context.CancelFunc{}
	}
	remoteAsks.cancels["ask-1"] = cancel
	remoteAsks.mu.Unlock()
	if got := a.CancelRemoteAsk("ask-1"); got != "ok" || ctx.Err() == nil {
		t.Fatalf("%q %v", got, ctx.Err())
	}
	for _, m := range []string{"AskRemoteAgentAs", "CancelRemoteAsk"} {
		if !agentCallable[m] {
			t.Errorf("core cannot call %s over the link", m)
		}
	}
}

// An ask over the agent link that is stopped on core is stopped on the
// agent too: core tells it which ask, by the id it went out under.
func TestStoppingALinkAskTellsTheAgent(t *testing.T) {
	core := NewApp()
	addr := startCore(t, core)
	stream := dialAs(t, addr, "tok")
	hello := &agentlinkpb.Hello{Name: "box", Agents: []*agentlinkpb.NamedAgent{{Name: "pi-mac", About: "pi"}}}
	if err := stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: hello}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "registration", func() bool { return len(core.LinkedAgents()) == 1 })

	asked, cancelled := make(chan string, 1), make(chan string, 1)
	go func() {
		for {
			f, err := stream.Recv()
			if err != nil {
				return
			}
			c := f.GetCall()
			if c == nil {
				continue
			}
			var args []string
			_ = json.Unmarshal([]byte(c.GetArgsJson()), &args)
			switch c.GetMethod() {
			case "AskRemoteAgentAs":
				asked <- args[0] // and never answers: it is still working
				continue
			case "CancelRemoteAsk":
				cancelled <- args[0]
			}
			_ = stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Reply{Reply: &agentlinkpb.Reply{Id: c.GetId(), ResultJson: `"ok"`}}})
		}
	}()

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan backend.RemoteResult, 1)
	go func() { done <- core.askAgent(ctx, "pi-mac", "long job") }()
	var id string
	select {
	case id = <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the ask never reached the agent")
	}
	stop()
	select {
	case got := <-cancelled:
		if got != id {
			t.Fatalf("cancelled %q, asked %q", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent was never told to stop")
	}
	if res := <-done; !res.Failed {
		t.Fatalf("%+v", res)
	}
}
