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

// agentHub is every agent connected to this core, by name.
type agentHub struct {
	mu     sync.Mutex
	agents map[string]*agentConn
}

func (a *App) agents() *agentHub {
	a.agentHubOnce.Do(func() { a.agentHubV = &agentHub{agents: map[string]*agentConn{}} })
	return a.agentHubV
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
	for _, c := range h.list() {
		for _, n := range c.hello.GetAgents() {
			if n.GetName() == name {
				return c
			}
		}
	}
	return nil
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
}

// LinkedAgents lists the agents connected to this core over the agent link.
func (a *App) LinkedAgents() []LinkedAgent {
	out := []LinkedAgent{}
	for _, c := range a.agents().list() {
		la := LinkedAgent{Name: c.name(), Host: c.hello.GetHost(), OS: c.hello.GetOs(), Arch: c.hello.GetArch(),
			Version: c.hello.GetVersion(), CLIs: append([]string{}, c.hello.GetClis()...),
			Connected: c.connected, LastSeen: time.UnixMilli(c.lastSeen.Load())}
		for _, n := range c.hello.GetAgents() {
			la.Agents = append(la.Agents, map[string]string{"name": n.GetName(), "about": n.GetAbout()})
		}
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
	hub.mu.Unlock()
	s.app.agentsChanged()
	log.Printf("agent %s connected from %s (%s/%s), clis %v", hello.GetName(), hello.GetHost(), hello.GetOs(), hello.GetArch(), hello.GetClis())

	defer func() {
		hub.mu.Lock()
		if hub.agents[hello.GetName()] == c {
			delete(hub.agents, hello.GetName())
		}
		hub.mu.Unlock()
		c.closeOnce()
		s.app.agentsChanged()
		log.Printf("agent %s disconnected", hello.GetName())
	}()

	if err := stream.Send(&agentlinkpb.CoreFrame{Kind: &agentlinkpb.CoreFrame_Welcome{Welcome: &agentlinkpb.Welcome{Name: hello.GetName()}}}); err != nil {
		return err
	}

	// One goroutine writes (gRPC streams take one sender at a time), this one
	// reads, and a ticker keeps the line warm and the last-seen time honest.
	sendErr := make(chan error, 1)
	go func() {
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			select {
			case f := <-c.out:
				if err := stream.Send(f); err != nil {
					sendErr <- err
					return
				}
			case <-ping.C:
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

	for {
		f, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
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
		select {
		case err := <-sendErr:
			return err
		default:
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
