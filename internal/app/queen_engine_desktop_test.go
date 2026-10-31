package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// linkHelloUntil is linkHello with a way to drop the link.
func linkHelloUntil(t *testing.T, core *App, addr string, h *agentlinkpb.Hello) (drop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream := dialAsCtx(t, ctx, addr, "tok")
	if err := stream.Send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: h}}); err != nil {
		t.Fatal(err)
	}
	if f, err := stream.Recv(); err != nil || f.GetWelcome() == nil {
		t.Fatalf("welcome for %s: %v %v", h.GetName(), f, err)
	}
	waitFor(t, h.GetName()+" to register", func() bool { return core.agents().get(h.GetName()) != nil })
	return cancel
}

// dialAsCtx is dialAs on a context the test cancels to drop the link.
func dialAsCtx(t *testing.T, ctx context.Context, addr, token string) agentlinkpb.AgentLink_ConnectClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	stream, err := agentlinkpb.NewAgentLinkClient(conn).Connect(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token))
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func engineInfo(l QueenEngineListing, id string) (QueenEngineInfo, bool) {
	for _, e := range l.Engines {
		if e.ID == id {
			return e, true
		}
	}
	return QueenEngineInfo{}, false
}

// A desktop SuperLeo linked to core is one of the queen's engines while its
// link is up; one too old to say it can is not offered.
func TestADesktopSuperLeoIsAQueenEngineWhileItIsLinked(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin")
	core := NewApp()
	core.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}
	addr := startCore(t, core)

	drop := linkHelloUntil(t, core, addr, &agentlinkpb.Hello{Name: "mac", Os: "darwin", Kind: "mac", Version: "0.30.0",
		Clis: []string{"claude"}, QueenEngine: true})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "mac-old", Os: "darwin", Kind: "mac", Clis: []string{"claude"}})

	l := core.QueenEngineList()
	d, ok := engineInfo(l, "superleo.mac")
	if !ok || d.State != "online" || d.Label != "SuperLeo（Mac 桌面版）" || d.Where != "mac" || d.Enabled {
		t.Fatalf("the desktop SuperLeo is not offered as it should be: %+v (%v)", d, ok)
	}
	if _, ok := engineInfo(l, "superleo.mac-old"); ok {
		t.Fatal("a SuperLeo too old to think for the queen is offered")
	}
	if _, ok := engineInfo(l, "claude.mac-old"); !ok {
		t.Fatal("the old Mac's CLIs went missing")
	}
	legacy, _ := core.QueenEngines()["choices"].([]QueenEngineChoice)
	found := false
	for _, c := range legacy {
		found = found || c.Name == "superleo.mac"
	}
	if !found {
		t.Fatalf("the older phone apps' choices leave it out: %+v", legacy)
	}
	// It can be picked, and an old one cannot.
	next := *core.settings
	next.Hive.SetQueenChain([]backend.QueenEngineEntry{{Engine: "superleo.mac", Enabled: true}, {Engine: "superleo", Enabled: true}})
	if err := core.validQueenEngine(&next, ""); err != nil {
		t.Fatalf("picking it: %v", err)
	}
	old := *core.settings
	old.Hive.SetQueenChain([]backend.QueenEngineEntry{{Engine: "superleo.mac-old", Enabled: true}})
	if err := core.validQueenEngine(&old, ""); err == nil {
		t.Fatal("an old desktop could be picked")
	}

	core.settings.Hive.SetQueenChain([]backend.QueenEngineEntry{{Engine: "superleo.mac", Enabled: true},
		{Engine: "superleo.mac-old", Enabled: true}, {Engine: "superleo", Enabled: true}})
	drop()
	waitFor(t, "the desktop to go offline", func() bool {
		d, ok := engineInfo(core.QueenEngineList(), "superleo.mac")
		return ok && d.State == "offline"
	})
	l = core.QueenEngineList()
	if d, _ := engineInfo(l, "superleo.mac"); !d.Enabled || d.Reason != "not reachable now" || l.Engines[0].ID != "superleo.mac" {
		t.Fatalf("offline, it should keep its place: %+v", l.Engines)
	}
	if d, _ := engineInfo(l, "superleo.mac-old"); d.State != "offline" || !strings.Contains(d.Reason, "too old") {
		t.Fatalf("the old one in the list: %+v", d)
	}
}

// desktopTurns is what a stand-in desktop agent was asked.
type desktopTurns struct {
	mu   sync.Mutex
	got  [][2]string // session, message
	text string
}

func (d *desktopTurns) turn(ctx context.Context, session, message string, emit func(*agent.Event)) (backend.TurnResult, error) {
	d.mu.Lock()
	d.got = append(d.got, [2]string{session, message})
	d.mu.Unlock()
	emit(&agent.Event{Type: agent.EventTypePartial, Content: "桌面版"})
	emit(&agent.Event{Type: agent.EventTypeToolCall, ToolName: "memory_recall", ToolArgs: map[string]any{"q": "日程"}})
	emit(&agent.Event{Type: agent.EventTypeToolResult, ToolName: "memory_recall", ToolResult: map[string]any{"hits": 1}})
	emit(&agent.Event{Type: agent.EventTypePartial, Content: "的回答"})
	return backend.TurnResult{Text: "桌面版的回答"}, nil
}

func (d *desktopTurns) asked() [][2]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][2]string(nil), d.got...)
}

// With the desktop SuperLeo first, the queen's turn goes over the agent link
// to the desktop's own agent, streams back as it writes, and is kept; the
// next turn continues the desktop's conversation.
func TestTheQueensTurnGoesToTheDesktopSuperLeo(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.SetQueenChain([]backend.QueenEngineEntry{{Engine: "superleo.mac", Enabled: true}, {Engine: "superleo", Enabled: true}})
	addr := startCore(t, h.a)

	turns := &desktopTurns{}
	desk := NewApp()
	desk.desktop, desk.desktopTurnFn = true, turns.turn
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		desk.RunAgentLink(ctx, AgentLinkOptions{Core: addr, Token: "tok", Name: "mac", Version: "test"})
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "the desktop to link", func() bool { _, ok := h.a.desktopEngine("superleo.mac"); return ok })

	id := h.a.SendChat("c1", "今天有什么安排？", nil)
	name, end := h.ev.wait(t, id)
	if name != "chat:done" || end["final"] != "桌面版的回答" || end["engine"] != "superleo.mac" {
		t.Fatalf("%s %v", name, end)
	}
	var streamed string
	tools := 0
	for _, p := range h.ev.all("chat:event", id) {
		if p["engine"] != "superleo.mac" {
			t.Fatalf("an event without its engine: %v", p)
		}
		switch p["type"] {
		case "partial":
			streamed += p["content"].(string)
		case "tool_call":
			if p["tool"] != "memory_recall" {
				t.Fatalf("tool call %v", p)
			}
			tools++
		}
	}
	if streamed != "桌面版的回答" || tools != 1 {
		t.Fatalf("streamed %q with %d tool calls", streamed, tools)
	}
	if len(h.owned()) != 0 {
		t.Fatalf("core's own model answered too: %v", h.owned())
	}
	got := turns.asked()
	if len(got) != 1 || got[0][1] != "今天有什么安排？" || !strings.HasPrefix(got[0][0], desktopEngineSessionPrefix) {
		t.Fatalf("the desktop was asked %v", got)
	}
	h.mu.Lock()
	stored := append([][2]string(nil), h.stored...)
	h.mu.Unlock()
	if len(stored) != 1 || stored[0] != [2]string{"今天有什么安排？", "桌面版的回答"} {
		t.Fatalf("stored %v", stored)
	}

	// The next turn continues the same conversation on the desktop.
	if name, end := h.ev.wait(t, h.a.SendChat("c1", "那明天呢？", nil)); name != "chat:done" {
		t.Fatalf("%s %v", name, end)
	}
	got = turns.asked()
	if len(got) != 2 || got[1][0] != got[0][0] || got[1][1] != "那明天呢？" {
		t.Fatalf("the second turn was not the same conversation: %v", got)
	}
}

// Away, or too old, the desktop SuperLeo hands the turn to the next engine,
// as a CLI on a Mac that is away does; nothing fails.
func TestADesktopSuperLeoThatIsNotThereHandsTheTurnOn(t *testing.T) {
	for _, tc := range []struct {
		name string
		link *agentlinkpb.Hello
	}{
		{"away", nil},
		{"too old", &agentlinkpb.Hello{Name: "mac", Os: "darwin", Kind: "mac", Clis: []string{"claude"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newEngineHarness(t, fakeClaudeTurn, 0)
			h.a.settings.Hive.SetQueenChain([]backend.QueenEngineEntry{{Engine: "superleo.mac", Enabled: true}, {Engine: "superleo", Enabled: true}})
			addr := startCore(t, h.a)
			if tc.link != nil {
				linkHello(t, h.a, addr, tc.link)
			}
			id := h.a.SendChat("c1", "今天有什么安排？", nil)
			if name, end := h.ev.wait(t, id); name != "chat:done" || end["final"] != "own" {
				t.Fatalf("%s %v", name, end)
			}
			own := h.owned()
			if len(own) != 1 || own[0].standIn != "superleo.mac" {
				t.Fatalf("SuperLeo did not answer in its place: %v", own)
			}
		})
	}

	// And to a CLI next in the list.
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.SetQueenChain([]backend.QueenEngineEntry{{Engine: "superleo.mac", Enabled: true}, {Engine: "claude", Enabled: true}})
	id := h.a.SendChat("c1", "现在 hive 里有哪些成员？", nil)
	name, end := h.ev.wait(t, id)
	if name != "chat:done" || end["engine"] != "claude" || end["inPlaceOf"] != "superleo.mac" ||
		!strings.HasSuffix(end["final"].(string), "hive 里有两个成员。") {
		t.Fatalf("%s %v", name, end)
	}
}

// The desktop answers only in a conversation it keeps for the queen, and
// only with a model; any other agent name is a CLI's as before.
func TestTheDesktopThinksForTheQueenOnlyInItsOwnConversations(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	desk := NewApp()
	if _, err := desk.StartQueenCLIRun(QueenRunRequest{Agent: "superleo", Prompt: "hi"}); err == nil {
		t.Fatal("a SuperLeo that is not the desktop app thought for the queen")
	}
	desk.desktop, desk.desktopTurnFn = true, (&desktopTurns{}).turn
	if _, err := desk.StartQueenCLIRun(QueenRunRequest{Agent: "superleo", Prompt: "hi", Session: "0b6f2c1e-person"}); err == nil {
		t.Fatal("core continued one of the person's own conversations")
	}
	run, err := desk.StartQueenCLIRun(QueenRunRequest{Agent: "superleo", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run to end", func() bool { r, _ := desk.CLIRunDetail(run.ID); return r.State == "done" })
	r, _ := desk.CLIRunDetail(run.ID)
	if r.Summary != "桌面版的回答" || r.Tools != 1 || !r.Queen || r.Thread != r.Session {
		t.Fatalf("run %+v", r)
	}
}
