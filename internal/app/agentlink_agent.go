package app

// The agent's side of the agent link: dial core, say who this is and what it
// can run, then answer core's calls and pass on what this machine's event bus
// says — until the process ends, reconnecting whenever the line drops.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	agentlinkpb "github.com/liliang-cn/superai/internal/agentlink/pb"
	"github.com/liliang-cn/superai/internal/backend"
	"google.golang.org/grpc"
	grpccreds "google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// AgentLinkOptions is how an agent finds and proves itself to core.
type AgentLinkOptions struct {
	// Core is host:port of core's agent link.
	Core string
	// Token is a bearer core accepts: its own token or a paired device's.
	Token string
	// Name is what core calls this agent; defaults to the short host name.
	Name string
	// TLS dials with TLS (a public core behind a proxy); off for a LAN one.
	TLS bool
	// Version is this build's, for the screens.
	Version string
}

// agentCallable is what core may call on an agent. The agent link gives core
// the agent's hands, not its settings: running and following coding runs,
// answering their approvals, asking the agents this machine can reach.
var agentCallable = map[string]bool{
	"StartCLIRun": true, "FollowUpCLIRun": true, "CancelCLIRun": true,
	"CLIRuns": true, "CLIRunDetail": true,
	"ExternalAgentsStatus": true, "RemoteAgentNames": true, "AskRemoteAgent": true,
	"PendingToolApprovals": true, "ResolveToolApproval": true, "ToolApprovalInfo": true,
}

// agentQuiet are events too frequent to be worth the line.
var agentQuiet = map[string]bool{"pulse:frame": true}

// RunAgentLink keeps this App linked to core until ctx ends.
func (a *App) RunAgentLink(ctx context.Context, o AgentLinkOptions) {
	if o.Name == "" {
		h, _ := os.Hostname()
		o.Name = strings.Split(h, ".")[0]
	}
	// Core may be given as several addresses (any node of a cluster that
	// serves it): each failure moves on to the next.
	var cores []string
	for _, c := range strings.Split(o.Core, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cores = append(cores, c)
		}
	}
	if len(cores) == 0 {
		return
	}
	wait, next := time.Second, 0
	for ctx.Err() == nil {
		one := o
		one.Core = cores[next%len(cores)]
		started := time.Now()
		err := a.agentLinkOnce(ctx, one)
		if ctx.Err() != nil {
			return
		}
		next++
		if time.Since(started) > time.Minute {
			wait = time.Second // it was up a good while: this is a fresh drop
		}
		log.Printf("agent link to %s: %v; again in %s", one.Core, err, wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < 30*time.Second {
			wait *= 2
		}
	}
}

func (a *App) agentHello(o AgentLinkOptions) *agentlinkpb.Hello {
	host, _ := os.Hostname()
	h := &agentlinkpb.Hello{Name: o.Name, Host: host, Os: runtime.GOOS, Arch: runtime.GOARCH, Version: o.Version}
	for _, c := range a.localAgentNames() {
		h.Clis = append(h.Clis, c.Name)
	}
	cfg := a.remoteRunner().Config()
	if cfg.Enabled {
		for _, n := range cfg.Names() {
			ra := cfg.Agents[n]
			// Only what this machine runs itself; another SuperAI it reaches by
			// URL is that SuperAI's business, not something to pass on.
			if ra.URL != "" {
				continue
			}
			// An agent run here (host "local") is offered only while it is
			// here: an HA service that moved to another node is that node's
			// agent's to offer, and core should not be sent here for it.
			if onlyLocal(ra.Hosts) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				here := backend.ProbeLocal(ctx, ra)
				cancel()
				if !here {
					continue
				}
			}
			h.Agents = append(h.Agents, &agentlinkpb.NamedAgent{Name: n, About: ra.About})
		}
	}
	return h
}

func (a *App) agentLinkOnce(ctx context.Context, o AgentLinkOptions) error {
	creds := insecure.NewCredentials()
	if o.TLS {
		creds = grpccreds.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	conn, err := grpc.NewClient(o.Core,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(32<<20)),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	sctx, cancel := context.WithCancel(metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+o.Token))
	defer cancel()
	stream, err := agentlinkpb.NewAgentLinkClient(conn).Connect(sctx)
	if err != nil {
		return err
	}

	// One writer for the stream; everything that wants to say something
	// queues here.
	out := make(chan *agentlinkpb.AgentFrame, 256)
	send := func(f *agentlinkpb.AgentFrame) {
		select {
		case out <- f:
		case <-sctx.Done():
		}
	}
	sendErr := make(chan error, 1)
	go func() {
		for {
			select {
			case f := <-out:
				if err := stream.Send(f); err != nil {
					sendErr <- err
					cancel()
					return
				}
			case <-sctx.Done():
				return
			}
		}
	}()

	hello := a.agentHello(o)
	send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: hello}})
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first.GetWelcome() == nil {
		return errors.New("core did not welcome this agent")
	}
	log.Printf("agent link: connected to %s as %s, clis %v", o.Core, hello.GetName(), hello.GetClis())

	// What this machine's event bus says goes up the line.
	tap := func(name string, payload map[string]any) {
		if agentQuiet[name] {
			return
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return
		}
		select {
		case out <- &agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Event{Event: &agentlinkpb.Event{Name: name, PayloadJson: string(raw)}}}:
		default: // a full queue drops an event rather than stalling the app
		}
	}
	a.agentTap.Store(&tap)
	defer a.agentTap.Store(nil)
	go a.sendPulseSummaries(sctx, hello.GetName(), tap)

	// A changed set of CLIs or agents is said again, so core's menus follow.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		last := fmt.Sprint(hello.GetClis(), hello.GetAgents())
		for {
			select {
			case <-t.C:
				h := a.agentHello(o)
				if now := fmt.Sprint(h.GetClis(), h.GetAgents()); now != last {
					last = now
					send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Hello{Hello: h}})
				}
			case <-sctx.Done():
				return
			}
		}
	}()

	for {
		f, err := stream.Recv()
		if err != nil {
			select {
			case serr := <-sendErr:
				return serr
			default:
			}
			return err
		}
		switch k := f.GetKind().(type) {
		case *agentlinkpb.CoreFrame_Ping:
			send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Pong{Pong: &agentlinkpb.Pong{AtUnixMs: k.Ping.GetAtUnixMs()}}})
		case *agentlinkpb.CoreFrame_Call:
			go func(c *agentlinkpb.Call) {
				send(&agentlinkpb.AgentFrame{Kind: &agentlinkpb.AgentFrame_Reply{Reply: a.answerCall(c)}})
			}(k.Call)
		}
	}
}

func onlyLocal(hosts []string) bool {
	if len(hosts) == 0 {
		return false
	}
	for _, h := range hosts {
		if !backend.IsLocalHost(h) {
			return false
		}
	}
	return true
}

// answerCall runs one of core's calls through the same dispatcher /api/rpc
// uses, if it is one core may make.
func (a *App) answerCall(c *agentlinkpb.Call) *agentlinkpb.Reply {
	r := &agentlinkpb.Reply{Id: c.GetId()}
	if !agentCallable[c.GetMethod()] {
		r.Error = c.GetMethod() + " is not something core can ask an agent to do"
		return r
	}
	var raw []json.RawMessage
	if s := strings.TrimSpace(c.GetArgsJson()); s != "" {
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			r.Error = "arguments must be a JSON array: " + err.Error()
			return r
		}
	}
	result, err := callMethod(a, c.GetMethod(), raw)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	b, err := json.Marshal(result)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.ResultJson = string(b)
	return r
}
