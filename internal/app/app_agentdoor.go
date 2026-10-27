package app

// The desktop app's door for agents: another machine joins this Mac the way
// it joins a hive's core, with `superai agent join` and a pairing code.
//
// `superai serve` opens its agent link with -agent-port and answers the
// claim on its own HTTP address. The desktop app has neither on the network —
// its API lives in its window — so it opens two ports of its own: the agent
// link (gRPC) and, beside it, the claim alone, which is all a joining machine
// needs before it has a token. The claim says the link is on the same address
// the claim came in by, so no address has to be configured anywhere.
//
// Settings → Agents → Add an agent mints the code and shows the command,
// one per address this machine has (AgentJoinCode).

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// desktopClaimPort answers /api/pair/claim and nothing else.
	desktopClaimPort = 43781
	// desktopAgentPort is the desktop app's agent link.
	desktopAgentPort = 43782
)

// agentDoor is where this app takes agents in: the claim's HTTP port and the
// agent link's, and why it could not open them.
type agentDoor struct {
	mu    sync.Mutex
	claim int
	link  int
	err   string
}

func (d *agentDoor) set(claim, link int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if claim > 0 {
		d.claim = claim
	}
	if link > 0 {
		d.link = link
	}
	if err != nil {
		d.err = err.Error()
	}
}

func (d *agentDoor) get() (claim, link int, err string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.claim, d.link, d.err
}

// openAgentDoor opens the desktop app's agent link and claim, on every
// address, until ctx ends. Both ask for a credential: the link the core's
// token or a paired device's, the claim a live six-digit code, with the
// usual throttle on wrong ones.
func (a *App) openAgentDoor(ctx context.Context) {
	creds, err := loadOrCreateCredentials()
	if err != nil {
		a.agentDoor.set(0, 0, fmt.Errorf("no credentials: %w", err))
		return
	}
	linkAddr := fmt.Sprintf("0.0.0.0:%d", desktopAgentPort)
	lis, err := net.Listen("tcp", linkAddr)
	if err != nil {
		log.Printf("agent door: %v", err)
		a.agentDoor.set(0, 0, err)
		return
	}
	s := newAgentLinkServer(a, credentialsCheck(creds))
	go func() {
		<-ctx.Done()
		s.GracefulStop()
	}()
	go func() {
		log.Printf("agent door: agent link on %s", linkAddr)
		if err := s.Serve(lis); err != nil {
			log.Printf("agent door: %v", err)
		}
	}()

	mux := http.NewServeMux()
	pairRoutesLinking(mux, creds, func() int { return desktopAgentPort })
	srv := &http.Server{Addr: fmt.Sprintf("0.0.0.0:%d", desktopClaimPort), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	claim, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Printf("agent door: %v", err)
		a.agentDoor.set(0, desktopAgentPort, err)
		return
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() { _ = srv.Serve(claim) }()
	a.agentDoor.set(desktopClaimPort, desktopAgentPort, nil)
}

// AgentJoin is what Settings shows to add an agent: the code, until when it
// works, and the command to run on the other machine, once per address this
// one has.
type AgentJoin struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
	Commands  []string  `json:"commands"`
	Error     string    `json:"error,omitempty"`
}

// AgentJoinCode mints a pairing code for another machine to join this one as
// an agent, with the command that does it:
//
//	POST /api/rpc/AgentJoinCode  []
//	→ {"code":"123456","expiresAt":"…","commands":["superai agent join http://192.168.123.123:43781 123456", …]}
func (a *App) AgentJoinCode() (AgentJoin, error) {
	claim, link, doorErr := a.agentDoor.get()
	if claim == 0 || link == 0 {
		if doorErr == "" {
			doorErr = "this SuperAI is not taking agents in"
		}
		return AgentJoin{Error: doorErr}, nil
	}
	s, err := openDevices(devicesPath())
	if err != nil {
		return AgentJoin{}, err
	}
	code, exp := s.newCode()
	out := AgentJoin{Code: code, ExpiresAt: exp.UTC()}
	for _, ip := range reachableAddrs() {
		out.Commands = append(out.Commands, fmt.Sprintf("superai agent join http://%s %s", net.JoinHostPort(ip, fmt.Sprint(claim)), code))
	}
	if len(out.Commands) == 0 {
		out.Error = "this machine has no network address another one could reach"
	}
	return out, nil
}

// reachableAddrs are this machine's IPv4 addresses another machine could
// reach: the local network's first, then a Tailscale one.
func reachableAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var lan, tail []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			ip := ipn.IP.To4()
			switch {
			case cgnatNet.Contains(ip):
				tail = append(tail, ip.String())
			case ip.IsPrivate():
				// Not a VM bridge or a container network on this machine.
				if !strings.HasPrefix(ifc.Name, "bridge") && !strings.HasPrefix(ifc.Name, "docker") && !strings.HasPrefix(ifc.Name, "vmnet") && !strings.HasPrefix(ifc.Name, "utun") {
					lan = append(lan, ip.String())
				}
			}
		}
	}
	sort.Strings(lan)
	sort.Strings(tail)
	return append(lan, tail...)
}

var cgnatNet = func() *net.IPNet { _, n, _ := net.ParseCIDR("100.64.0.0/10"); return n }()
