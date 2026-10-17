package app

// Core's side of the agent link (proto/agentlink/v1). Agents dial in and hold
// a stream; core keeps them in a registry and, through each stream, calls the
// agent's methods and hears its events. Everything core already does with
// another SuperAI over HTTP — coding runs on its CLIs, the agents it can reach
// — it can then do with an agent it could never have dialled itself.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// agentConn is one connected agent.
type agentConn struct {
	hello     *agentlinkpb.Hello
	connected time.Time
	lastSeen  atomic.Int64 // unix ms

	out     chan *agentlinkpb.CoreFrame
	done    chan struct{}
	mu      sync.Mutex
	pending map[string]chan *agentlinkpb.Reply
	subs    map[int]chan *agentlinkpb.Event
	nextSub int

	// activity is what the machine's interactive Claude Code sessions are
	// doing, as it last said (cc:activity); nil when it has none.
	activity atomic.Pointer[CCActivity]
	// clis is what the machine's other coding CLIs are doing, by CLI, as it
	// last said (cli:activity, cli_activity.go); cliMu orders the writes.
	cliMu sync.Mutex
	clis  atomic.Pointer[cliMap]
}

func (c *agentConn) name() string { return c.hello.GetName() }

// call runs a method on the agent and decodes its result into out (which may
// be nil). It gives up with ctx, or when the agent goes away.
func (c *agentConn) call(ctx context.Context, method string, args []any, out any) error {
	if args == nil {
		args = []any{}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	id := uuid.NewString()
	ch := make(chan *agentlinkpb.Reply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	frame := &agentlinkpb.CoreFrame{Kind: &agentlinkpb.CoreFrame_Call{Call: &agentlinkpb.Call{Id: id, Method: method, ArgsJson: string(raw)}}}
	select {
	case c.out <- frame:
	case <-c.done:
		return fmt.Errorf("%s went away", c.name())
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case r := <-ch:
		if r.GetError() != "" {
			return errors.New(r.GetError())
		}
		if out != nil && r.GetResultJson() != "" {
			return json.Unmarshal([]byte(r.GetResultJson()), out)
		}
		return nil
	case <-c.done:
		return fmt.Errorf("%s went away during %s", c.name(), method)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// subscribe hears the agent's events until cancel is called or it goes away
// (the channel is closed then).
func (c *agentConn) subscribe() (<-chan *agentlinkpb.Event, func()) {
	ch := make(chan *agentlinkpb.Event, 256)
	c.mu.Lock()
	id := c.nextSub
	c.nextSub++
	c.subs[id] = ch
	c.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			c.mu.Lock()
			if _, ok := c.subs[id]; ok {
				delete(c.subs, id)
				close(ch)
			}
			c.mu.Unlock()
		})
	}
}

// eventStream renders the agent's events as the SSE lines /api/events
// writes, so the code that mirrors a run from another SuperAI over HTTP reads
// a linked agent's run without knowing the difference.
func (c *agentConn) eventStream(ctx context.Context) io.ReadCloser {
	events, cancel := c.subscribe()
	pr, pw := io.Pipe()
	go func() {
		defer cancel()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					pw.CloseWithError(fmt.Errorf("%s went away", c.name()))
					return
				}
				line, _ := json.Marshal(map[string]any{"name": ev.GetName(), "payload": json.RawMessage(orNull(ev.GetPayloadJson()))})
				if _, err := fmt.Fprintf(pw, "data: %s\n\n", line); err != nil {
					return
				}
			case <-ctx.Done():
				pw.CloseWithError(ctx.Err())
				return
			}
		}
	}()
	return pr
}

func orNull(s string) string {
	if strings.TrimSpace(s) == "" {
		return "null"
	}
	return s
}

// How core knows an agent is still there. It pings every agentPingEvery and
// the agent answers; any frame counts as hearing from it. Three pings without
// a word and the agent is dropped, even when the connection looks open: a
// machine that lost power or network leaves a half-open TCP connection, and
// the pings go into its send buffer without an error for many minutes.
const (
	agentPingEvery = 20 * time.Second
	agentSilence   = 3*agentPingEvery + 5*time.Second
)

// agentForget is how long a dropped agent stays in Needs you before it is
// taken as gone on purpose, like a hive worker (hiveForget in backend).
const agentForget = 15 * time.Minute

// agentHub is every agent connected to this core, by name, and the ones that
// dropped recently with when they were last heard.
type agentHub struct {
	mu     sync.Mutex
	agents map[string]*agentConn
	lost   map[string]time.Time
	// lostRole is the role a lost agent declared in its last Hello, so a hive
	// worker that dropped is not also reported as a machine gone missing.
	lostRole map[string]string

	pingEvery, silence time.Duration // agentPingEvery and agentSilence, shorter in tests
}

func (a *App) agents() *agentHub {
	a.agentHubOnce.Do(func() {
		a.agentHubV = &agentHub{agents: map[string]*agentConn{}, lost: map[string]time.Time{}, lostRole: map[string]string{},
			pingEvery: agentPingEvery, silence: agentSilence}
	})
	return a.agentHubV
}

// lostAgents is the agents that dropped without coming back, by when they
// were last heard, forgetting the ones gone longer than agentForget.
func (h *agentHub) lostAgents(now time.Time) map[string]time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]time.Time{}
	for name, seen := range h.lost {
		if now.Sub(seen) > agentForget {
			delete(h.lost, name)
			delete(h.lostRole, name)
			continue
		}
		out[name] = seen
	}
	return out
}

// declaredRole is the role a lost agent declared before it dropped.
func (h *agentHub) declaredRole(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lostRole[name]
}

func (h *agentHub) get(name string) *agentConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agents[name]
}

func (h *agentHub) list() []*agentConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*agentConn, 0, len(h.agents))
	for _, c := range h.agents {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name() < out[j].name() })
	return out
}

// serving is the agent that runs the named agent (openclaw, hermes, ...), if
// one connected says it can.
func (h *agentHub) serving(name string) *agentConn {
	c, _ := h.servingAs(name)
	return c
}

// servingAs finds the connection that serves name, and the name that agent
// knows it by.
//
// The phone's world names an agent with its machine, "openclaw.sds-b", and
// asked by that it reached nobody: agents announce themselves bare, so the
// lookup missed and the ask fell through to this machine's own CLIs, which
// said "switched off". A qualified name now reaches the bare one, on that
// machine when it is there, and on whichever machine serves it when it is
// not — openclaw follows its HA group from node to node, and the name the
// phone kept from before a failover must still get an answer.
func (h *agentHub) servingAs(name string) (*agentConn, string) {
	has := func(c *agentConn, n string) bool {
		for _, a := range c.hello.GetAgents() {
			if a.GetName() == n {
				return true
			}
		}
		return false
	}
	conns := h.list()
	for _, c := range conns {
		if has(c, name) {
			return c, name
		}
	}
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return nil, name
	}
	bare, machine := name[:i], name[i+1:]
	var other *agentConn
	for _, c := range conns {
		if !has(c, bare) {
			continue
		}
		if c.name() == machine || c.hello.GetHost() == machine {
			return c, bare
		}
		if other == nil {
			other = c
		}
	}
	if other != nil {
		return other, bare
	}
	return nil, name
}

// What a linked agent is to this core. Every agent on the link can be asked
// to run things; only a machine is shown and looked after as one.
const (
	// agentRoleMachine is a computer someone owns: the Mac, the orange boxes.
	agentRoleMachine = "machine"
	// agentRoleWorker is a hive worker's pod, which also dials the link. It is
	// on the roster and drawn as a worker; as a machine it would be twice.
	agentRoleWorker = "worker"
	// agentRoleRelay is an agent kept for the named agents it serves
	// (openclaw, hermes, ...): agent_link.relay_only in the settings.
	agentRoleRelay = "relay"
)

// agentRoles is a classifier for the agents connected right now, built once
// from the hive roster and the settings so a list does not take a.mu per row.
//
// A worker is known by what it says (Hello.role, sent by a build that knows
// it is one) or, for a worker from before that, by being on the roster under
// the name it links with: a worker pod links as $(POD_NAME), the name it
// joins the hive by.
func (a *App) agentRoles() func(name, declared string) string {
	a.mu.Lock()
	h := a.hive
	var link backend.AgentLinkSettings
	if a.settings != nil {
		link = a.settings.AgentLink
	}
	a.mu.Unlock()
	roster := map[string]bool{}
	if h != nil {
		for _, m := range h.Members() {
			roster[m.Name] = true
		}
	}
	return func(name, declared string) string {
		switch {
		case declared == agentRoleWorker || roster[name]:
			return agentRoleWorker
		case link.IsRelay(name):
			return agentRoleRelay
		}
		return agentRoleMachine
	}
}

// agentRole is what the connected agent c is to this core.
func (a *App) agentRole(c *agentConn) string {
	return a.agentRoles()(c.name(), c.hello.GetRole())
}

// LinkedAgent is a connected agent as the screens show it.
type LinkedAgent struct {
	Name      string              `json:"name"`
	Host      string              `json:"host"`
	OS        string              `json:"os"`
	Arch      string              `json:"arch"`
	Version   string              `json:"version"`
	CLIs      []string            `json:"clis"`
	Agents    []map[string]string `json:"agents"`
	Connected time.Time           `json:"connected"`
	LastSeen  time.Time           `json:"lastSeen"`
	// Machine is whether it is shown and looked after as a machine; Role
	// says what it is: machine, worker (a hive worker's pod) or relay (kept
	// for its named agents, agent_link.relay_only).
	Machine bool   `json:"machine"`
	Role    string `json:"role"`
	// Activity is what the machine's interactive Claude Code sessions are
	// doing — the ones people run in its terminals, not SuperAI's own runs;
	// absent when it has none.
	Activity *CCActivity `json:"activity,omitempty"`
	// CLIActivity is what every coding CLI running on it is doing, by CLI
	// (claude too, the same as Activity): from the CLI's own plugin or hooks
	// where it has them, else from its processes. A CLI not running is not
	// in it; absent when none is.
	CLIActivity cliMap `json:"cliActivity,omitempty"`
}

// LinkedAgents lists the machines connected to this core over the agent link:
// what the machine lists and the 3D view draw. Hive workers and relays are
// left out here, so a client that takes every row for a machine — the iOS
// app does — shows the right ones; LinkedAgentsAll has them too.
func (a *App) LinkedAgents() []LinkedAgent {
	out := []LinkedAgent{}
	for _, la := range a.LinkedAgentsAll() {
		if la.Machine {
			out = append(out, la)
		}
	}
	return out
}

// LinkedAgentsAll lists every agent connected over the agent link, each with
// what it is (Machine, Role).
func (a *App) LinkedAgentsAll() []LinkedAgent {
	out := []LinkedAgent{}
	role := a.agentRoles()
	for _, c := range a.agents().list() {
		la := LinkedAgent{Name: c.name(), Host: c.hello.GetHost(), OS: c.hello.GetOs(), Arch: c.hello.GetArch(),
			Version: c.hello.GetVersion(), CLIs: append([]string{}, c.hello.GetClis()...),
			Connected: c.connected, LastSeen: time.UnixMilli(c.lastSeen.Load())}
		for _, n := range c.hello.GetAgents() {
			la.Agents = append(la.Agents, map[string]string{"name": n.GetName(), "about": n.GetAbout()})
		}
		la.Activity = c.activity.Load()
		if m := c.cliActivity(); len(m) > 0 {
			la.CLIActivity = m
		}
		la.Role = role(la.Name, c.hello.GetRole())
		la.Machine = la.Role == agentRoleMachine
		out = append(out, la)
	}
	return out
}

// agentLinkServer is the gRPC service.
type agentLinkServer struct {
	agentlinkpb.UnimplementedAgentLinkServer
	app *App
}

// tokenCheck says whether a bearer token may connect an agent.
type tokenCheck func(token string) bool

// credentialsCheck accepts what the HTTP gate accepts as a bearer: the
// server's own token, or a paired device's.
func credentialsCheck(c *credentials) tokenCheck {
	want := []byte(c.Token)
	return func(tok string) bool {
		return subtle.ConstantTimeCompare([]byte(tok), want) == 1 || (c.devices != nil && c.devices.authenticate(tok))
	}
}

func bearerFrom(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get("authorization") {
		if t, ok := strings.CutPrefix(v, "Bearer "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// newAgentLinkServer builds the gRPC server core listens with.
func newAgentLinkServer(app *App, ok tokenCheck) *grpc.Server {
	auth := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !ok(bearerFrom(ss.Context())) {
			return status.Error(codes.Unauthenticated, "this core does not know that token")
		}
		return handler(srv, ss)
	}
	s := grpc.NewServer(
		grpc.StreamInterceptor(auth),
		grpc.MaxRecvMsgSize(32<<20),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
		// Core probes the transport too, so a dead peer is closed by gRPC
		// itself and not only by the silence check below.
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
	)
	agentlinkpb.RegisterAgentLinkServer(s, &agentLinkServer{app: app})
	return s
}

// ServeAgentLink listens for agents on addr until ctx ends.
func ServeAgentLink(ctx context.Context, app *App, addr string, ok tokenCheck) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s := newAgentLinkServer(app, ok)
	go func() {
		<-ctx.Done()
		s.GracefulStop()
	}()
	log.Printf("agent link on %s", addr)
	return s.Serve(lis)
}

func (s *agentLinkServer) Connect(stream agentlinkpb.AgentLink_ConnectServer) error {
	first, err := recvWithin(stream, 15*time.Second)
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil || strings.TrimSpace(hello.GetName()) == "" {
		return status.Error(codes.InvalidArgument, "the first frame must be a Hello with a name")
	}
	c := &agentConn{hello: hello, connected: time.Now(), out: make(chan *agentlinkpb.CoreFrame, 64),
		done: make(chan struct{}), pending: map[string]chan *agentlinkpb.Reply{}, subs: map[int]chan *agentlinkpb.Event{}}
	c.lastSeen.Store(time.Now().UnixMilli())

	hub := s.app.agents()
	hub.mu.Lock()
	if old := hub.agents[hello.GetName()]; old != nil {
		// The same agent again — a reconnect after a network blip, or a
		// restart. The new stream is the one that works.
		old.closeOnce()
	}
	hub.agents[hello.GetName()] = c
	delete(hub.lost, hello.GetName())
	delete(hub.lostRole, hello.GetName())
	hub.mu.Unlock()
	s.app.agentsChanged()
	log.Printf("agent %s connected from %s (%s/%s), clis %v, role %s", hello.GetName(), hello.GetHost(), hello.GetOs(), hello.GetArch(), hello.GetClis(), s.app.agentRole(c))
	go hintCCAwayTo(c, s.app.Presence().Away)

	// An agent that closes its side said goodbye: it is stopping, not lost.
	bye := false
	defer func() {
		hub.mu.Lock()
		if hub.agents[hello.GetName()] == c {
			delete(hub.agents, hello.GetName())
			if !bye {
				hub.lost[hello.GetName()] = time.UnixMilli(c.lastSeen.Load())
				c.mu.Lock()
				hub.lostRole[hello.GetName()] = c.hello.GetRole()
				c.mu.Unlock()
			}
		}
		hub.mu.Unlock()
		c.closeOnce()
		s.app.agentsChanged()
		log.Printf("agent %s disconnected", hello.GetName())
	}()

	if err := stream.Send(&agentlinkpb.CoreFrame{Kind: &agentlinkpb.CoreFrame_Welcome{Welcome: &agentlinkpb.Welcome{Name: hello.GetName()}}}); err != nil {
		return err
	}

	// One goroutine writes (gRPC streams take one sender at a time), another
	// reads, and a ticker keeps the line warm and the last-seen time honest.
	// This one waits for either to fail: a Recv on a dead connection never
	// returns, and returning from here is what ends it.
	sendErr := make(chan error, 1)
	go func() {
		ping := time.NewTicker(hub.pingEvery)
		defer ping.Stop()
		for {
			select {
			case f := <-c.out:
				if err := stream.Send(f); err != nil {
					sendErr <- err
					return
				}
			case <-ping.C:
				if quiet := time.Since(time.UnixMilli(c.lastSeen.Load())); quiet > hub.silence {
					log.Printf("agent %s silent for %s; dropping it", c.name(), quiet.Round(time.Second))
					sendErr <- status.Errorf(codes.DeadlineExceeded, "no word from the agent for %s", quiet.Round(time.Second))
					return
				}
				f := &agentlinkpb.CoreFrame{Kind: &agentlinkpb.CoreFrame_Ping{Ping: &agentlinkpb.Ping{AtUnixMs: time.Now().UnixMilli()}}}
				if err := stream.Send(f); err != nil {
					sendErr <- err
					return
				}
			case <-c.done:
				return
			case <-stream.Context().Done():
				return
			}
		}
	}()

	recvErr := make(chan error, 1)
	go func() { recvErr <- s.receive(stream, c) }()
	select {
	case err := <-recvErr:
		if errors.Is(err, io.EOF) {
			bye = true
			return nil
		}
		return err
	case err := <-sendErr:
		return err
	case <-c.done: // replaced by a newer stream from the same agent
		return nil
	}
}

// receive handles what the agent sends until the stream fails.
func (s *agentLinkServer) receive(stream agentlinkpb.AgentLink_ConnectServer, c *agentConn) error {
	for {
		f, err := stream.Recv()
		if err != nil {
			return err
		}
		c.lastSeen.Store(time.Now().UnixMilli())
		switch k := f.GetKind().(type) {
		case *agentlinkpb.AgentFrame_Hello:
			c.mu.Lock()
			c.hello = k.Hello
			c.mu.Unlock()
			s.app.agentsChanged()
		case *agentlinkpb.AgentFrame_Reply:
			c.mu.Lock()
			ch := c.pending[k.Reply.GetId()]
			c.mu.Unlock()
			if ch != nil {
				ch <- k.Reply
			}
		case *agentlinkpb.AgentFrame_Event:
			if k.Event.GetName() == "agent:pulse" {
				s.app.notePulse(c.name(), k.Event.GetPayloadJson())
				break
			}
			if k.Event.GetName() == ccSessionEvent {
				s.app.noteCCSession(c.name(), k.Event.GetPayloadJson())
				break
			}
			if k.Event.GetName() == ccActivityEvent {
				s.app.noteCCActivity(c, k.Event.GetPayloadJson())
				break
			}
			if k.Event.GetName() == cliActivityEvent {
				s.app.noteCLIActivity(c, k.Event.GetPayloadJson())
				break
			}
			c.mu.Lock()
			for _, ch := range c.subs {
				select {
				case ch <- k.Event:
				default: // a subscriber that cannot keep up misses an event, not the stream
				}
			}
			c.mu.Unlock()
		case *agentlinkpb.AgentFrame_Pong:
		}
	}
}

func (c *agentConn) closeOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return
	default:
	}
	close(c.done)
	for id, ch := range c.subs {
		close(ch)
		delete(c.subs, id)
	}
}

func recvWithin(stream agentlinkpb.AgentLink_ConnectServer, d time.Duration) (*agentlinkpb.AgentFrame, error) {
	type got struct {
		f   *agentlinkpb.AgentFrame
		err error
	}
	ch := make(chan got, 1)
	go func() {
		f, err := stream.Recv()
		ch <- got{f, err}
	}()
	select {
	case g := <-ch:
		return g.f, g.err
	case <-time.After(d):
		return nil, status.Error(codes.DeadlineExceeded, "no Hello")
	}
}

// agentsChanged tells the screens the set of agents moved, and drops the
// cached CLI list so the @ menu and the run picker see it at once.
func (a *App) agentsChanged() {
	a.remoteCLI.mu.Lock()
	a.remoteCLI.names = nil
	a.remoteCLI.mu.Unlock()
	a.emit("agents:changed", map[string]any{"count": len(a.agents().list())})
}
