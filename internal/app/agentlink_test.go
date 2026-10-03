package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// startCore runs a core's agent link on a free port and returns its address.
func startCore(t *testing.T, core *App) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := newAgentLinkServer(core, func(tok string) bool { return tok == "tok" })
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

// dialAs opens a raw agent stream, for a test playing the agent by hand.
func dialAs(t *testing.T, addr, token string) agentlinkpb.AgentLink_ConnectClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithCancel(metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token))
	t.Cleanup(cancel)
	stream, err := agentlinkpb.NewAgentLinkClient(conn).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCoreRefusesAnAgentWithoutItsToken(t *testing.T) {
	addr := startCore(t, NewApp())
	stream := dialAs(t, addr, "wrong")
	_ = stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: &agentlinkpb.Hello{Name: "x"}}})
	_, err := stream.Recv()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("got %v, want Unauthenticated", err)
	}
}

// An agent behind the link: what it says it has becomes something core can
// address, and core's calls and the agent's events travel on the one stream.
func TestCoreRoutesThroughALinkedAgent(t *testing.T) {
	core := NewApp()
	addr := startCore(t, core)
	stream := dialAs(t, addr, "tok")

	hello := &agentlinkpb.Hello{Name: "box", Host: "box.lan", Os: "linux", Arch: "arm64", Clis: []string{"claude"},
		Agents: []*agentlinkpb.NamedAgent{{Name: "openclaw", About: "the gateway"}}}
	if err := stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: hello}}); err != nil {
		t.Fatal(err)
	}
	if f, err := stream.Recv(); err != nil || f.GetWelcome().GetName() != "box" {
		t.Fatalf("welcome: %v %v", f, err)
	}
	waitFor(t, "registration", func() bool { return len(core.LinkedAgents()) == 1 })

	// What it has, core now offers.
	if !core.addressable("openclaw") || !core.isRemoteCLI("claude.box") {
		t.Fatal("the agent's openclaw and claude are not addressable on core")
	}
	names := map[string]string{}
	for _, n := range core.RemoteAgentNames() {
		names[n["name"]] = n["about"]
	}
	if !strings.Contains(names["openclaw"], "via box") || names["claude.box"] == "" {
		t.Fatalf("@ menu: %v", names)
	}

	// The agent answers what core asks of it.
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
			out, _ := json.Marshal(map[string]any{"agent": args[0], "text": "pong to " + args[1]})
			_ = stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Reply{Reply: &agentlinkpb.Reply{Id: c.GetId(), ResultJson: string(out)}}})
		}
	}()
	res := core.askAgent(context.Background(), "openclaw", "hi")
	if res.Failed || res.Text != "pong to hi" || res.Host != "box" {
		t.Fatalf("ask through the link: %+v", res)
	}

	// Its events arrive as the SSE lines the run mirror reads.
	body := core.agents().get("box").eventStream(context.Background())
	defer body.Close()
	_ = stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Event{Event: &agentlinkpb.Event{Name: "cli:run", PayloadJson: `{"id":"r1","state":"done"}`}}})
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data:") {
				line <- sc.Text()
				return
			}
		}
	}()
	select {
	case l := <-line:
		if !strings.Contains(l, `"name":"cli:run"`) || !strings.Contains(l, `"id":"r1"`) {
			t.Fatalf("event line %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent's event never reached core")
	}

	// Gone when it hangs up.
	_ = stream.CloseSend()
	waitFor(t, "unregistration", func() bool { return len(core.LinkedAgents()) == 0 })
	if core.addressable("openclaw") {
		t.Fatal("openclaw still addressable after its agent left")
	}
}

// A real agent App linking to a core: core can run its methods, and what its
// event bus says reaches core.
func TestAnAgentLinksToCoreAndAnswers(t *testing.T) {
	core := NewApp()
	addr := startCore(t, core)

	agent := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		agent.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "mac2", Version: "test"})
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "the agent to connect", func() bool { return core.agents().get("mac2") != nil })
	c := core.agents().get("mac2")

	var runs []CLIRun
	if err := c.call(context.Background(), "CLIRuns", nil, &runs); err != nil {
		t.Fatalf("CLIRuns over the link: %v", err)
	}
	if err := c.call(context.Background(), "SaveSettings", []any{map[string]any{}}, nil); err == nil || !strings.Contains(err.Error(), "not something core can ask") {
		t.Fatalf("a method outside the allowlist ran: %v", err)
	}

	events, stop := c.subscribe()
	defer stop()
	agent.emit("cli:run", map[string]any{"id": "x1", "state": "running"})
	select {
	case ev := <-events:
		if ev.GetName() != "cli:run" || !strings.Contains(ev.GetPayloadJson(), "x1") {
			t.Fatalf("event %v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the agent's event never reached core")
	}
	la := core.LinkedAgents()
	if len(la) != 1 || la[0].Version != "test" {
		t.Fatalf("linked agents: %+v", la)
	}
}

// An agent on the node an HA service lives on offers it only while it is
// there, and runs it on its own machine.
func TestALocalAgentIsOfferedOnlyWhileItIsHere(t *testing.T) {
	ra := backend.RemoteAgent{Hosts: []string{backend.LocalHost}, Command: []string{"echo", "{prompt}"}}
	ra.Probe = "true"
	if !backend.ProbeLocal(context.Background(), ra) {
		t.Fatal("a probe that succeeds said the agent is not here")
	}
	ra.Probe = "false"
	if backend.ProbeLocal(context.Background(), ra) {
		t.Fatal("a probe that fails said the agent is here")
	}
	if !onlyLocal([]string{"local"}) || onlyLocal([]string{"local", "sds@10.0.0.1"}) || onlyLocal(nil) {
		t.Fatal("onlyLocal")
	}
}
