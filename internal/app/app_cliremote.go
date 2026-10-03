package app

// Claude Code on another machine, driven from this one.
//
// The other machine runs SuperAI too, with External agents on, and is listed
// here under Remote agents by URL and token. Its CLIs are then addressable as
// "claude.<name>": the run starts there, through its StartCLIRun, and is
// mirrored here as a run of this app's own — every event copied over its event
// stream as it happens, a follow-up resuming its session there, Stop stopping
// it there. A permission prompt Claude raises there becomes an approval card
// here, so the person at this desk (or on this phone) answers it.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/superai/internal/backend"
)

type cliRemote struct {
	name, base, token string
	// link, when set, is an agent connected over the agent link: calls and
	// events travel on its stream instead of HTTP. See agentlink_core.go.
	link *agentConn
}

var cliRemoteHTTP = &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Second}}

// splitRemoteCLI reads "claude.mac2" as the agent and the machine. The first
// dot splits: no agent CLI has one in its name, a machine may ("mac2.home").
func splitRemoteCLI(name string) (agent, host string, ok bool) {
	i := strings.IndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return "", "", false
	}
	return name[:i], name[i+1:], true
}

// cliRemotes is every other SuperAI configured by URL, and every agent
// connected over the agent link. A connected agent wins over a URL of the
// same name: it is the one that is certainly there.
func (a *App) cliRemotes() map[string]cliRemote {
	out := map[string]cliRemote{}
	a.mu.Lock()
	if a.settings != nil && a.settings.RemoteAgents.Enabled {
		for name, r := range a.settings.RemoteAgents.Agents {
			if u := strings.TrimRight(strings.TrimSpace(r.URL), "/"); u != "" {
				out[name] = cliRemote{name: name, base: u, token: r.Token}
			}
		}
	}
	a.mu.Unlock()
	for _, c := range a.agents().list() {
		out[c.name()] = cliRemote{name: c.name(), link: c}
	}
	return out
}

func (r cliRemote) rpc(ctx context.Context, method string, args []any, out any) error {
	if r.link != nil {
		return r.link.call(ctx, method, args, out)
	}
	body, _ := json.Marshal(args)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/api/rpc/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := cliRemoteHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", r.name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", r.name, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// The CLIs each remote has, asked for at most once a minute: the @ menu is
// rebuilt on every keystroke after an @.
type remoteCLICache struct {
	mu    sync.Mutex
	until time.Time
	names map[string][]string // remote → installed CLI names
}

func (a *App) remoteCLIs() map[string][]string {
	c := &a.remoteCLI
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.names != nil && time.Now().Before(c.until) {
		return c.names
	}
	out := map[string][]string{}
	remotes := a.cliRemotes()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range remotes {
		if r.link != nil {
			mu.Lock()
			out[r.name] = append([]string{}, r.link.hello.GetClis()...)
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(r cliRemote) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			var st []backend.ExternalAgentStatus
			if r.rpc(ctx, "ExternalAgentsStatus", []any{}, &st) != nil {
				return
			}
			for _, s := range st {
				if s.Installed {
					mu.Lock()
					out[r.name] = append(out[r.name], s.Name)
					mu.Unlock()
				}
			}
		}(r)
	}
	wg.Wait()
	// A remote that did not answer is asked again soon, not in a minute:
	// it may only have been starting up.
	keep := time.Minute
	if len(out) < len(remotes) {
		keep = 5 * time.Second
	}
	c.names, c.until = out, time.Now().Add(keep)
	return out
}

func (a *App) isRemoteCLI(name string) bool {
	agent, host, ok := splitRemoteCLI(name)
	if !ok {
		return false
	}
	for _, n := range a.remoteCLIs()[host] {
		if n == agent {
			return true
		}
	}
	return false
}

// remoteCLINames is what the @ menu adds for other machines' CLIs.
func (a *App) remoteCLINames() []map[string]string {
	all := a.remoteCLIs()
	hosts := make([]string, 0, len(all))
	for h := range all {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	var out []map[string]string
	for _, h := range hosts {
		for _, n := range all[h] {
			out = append(out, map[string]string{"name": n + "." + h, "about": n + " on " + h})
		}
	}
	return out
}

func (a *App) startRemoteCLIRun(o cliStart, host string) (CLIRun, error) {
	r, ok := a.cliRemotes()[host]
	if !ok {
		return CLIRun{}, fmt.Errorf("no SuperAI called %s under Remote agents", host)
	}
	// The stream is opened before the run is started: a run that ends fast
	// would otherwise finish before anyone here is listening.
	ctx, stop := context.WithCancel(context.Background())
	resp, err := r.events(ctx)
	if err != nil {
		stop()
		return CLIRun{}, err
	}

	var there CLIRun
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if o.Prev != nil && o.Prev.RemoteRun != "" {
		err = r.rpc(callCtx, "FollowUpCLIRun", []any{o.Prev.RemoteRun, o.Prompt}, &there)
	} else {
		err = r.rpc(callCtx, "StartCLIRun", []any{o.Agent, o.Prompt, o.Cwd, o.Model, o.Ask}, &there)
	}
	cancel()
	if err != nil {
		resp.Body.Close()
		stop()
		return CLIRun{}, err
	}

	id := uuid.NewString()
	thread := o.Thread
	if thread == "" {
		thread = id
	}
	run := &CLIRun{ID: id, Thread: thread, Agent: o.Agent + "." + host, Prompt: o.Prompt, Cwd: there.Cwd,
		Model: there.Model, Session: there.Session, Ask: there.Ask, Chat: o.Chat, State: "running",
		Started: time.Now(), Remote: host, RemoteRun: there.ID}
	s := a.cliStore()
	s.mu.Lock()
	s.runs[id] = run
	s.cancels[id] = func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_ = r.rpc(cctx, "CancelCLIRun", []any{there.ID}, nil)
	}
	if o.Watch != nil {
		s.watch[id] = o.Watch
	}
	if o.Done != nil {
		s.done[id] = o.Done
	}
	s.mu.Unlock()
	a.emit("cli:run", runPayload(run))

	go func() {
		defer stop()
		defer resp.Body.Close()
		a.mirrorRemoteRun(ctx, r, run, there, resp.Body)
	}()
	return summaryOf(run), nil
}

// events opens the remote's event stream: SSE over HTTP, or the agent link's
// events rendered the same way.
func (r cliRemote) events(ctx context.Context) (*http.Response, error) {
	if r.link != nil {
		return &http.Response{StatusCode: http.StatusOK, Body: r.link.eventStream(ctx)}, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/api/events", nil)
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := cliRemoteHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", r.name, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s refused its event stream: %s", r.name, resp.Status)
	}
	return resp, nil
}

// mirrorRemoteRun copies one remote run's events here until it ends.
func (a *App) mirrorRemoteRun(ctx context.Context, r cliRemote, run *CLIRun, there CLIRun, body io.Reader) {
	asking := map[string]context.CancelFunc{} // remote approval id → the card here
	var askMu sync.Mutex
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev struct {
			Name    string          `json:"name"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &ev) != nil {
			continue
		}
		switch ev.Name {
		case "cli:event":
			var p struct {
				Run   string      `json:"run"`
				Event CLIRunEvent `json:"event"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Run == there.ID {
				// The approval notes are this side's to write: the card is here.
				if p.Event.Kind == "note" && (p.Event.Text == "waiting for approval" || p.Event.Text == "approved" || strings.HasPrefix(p.Event.Text, "denied")) {
					continue
				}
				a.addCLIEvent(run, p.Event)
			}
		case "cli:run":
			var p CLIRun
			if json.Unmarshal(ev.Payload, &p) != nil || p.ID != there.ID {
				continue
			}
			if p.State == "running" {
				if p.Session != "" {
					a.setCLISession(run, p.Session)
				}
				continue
			}
			a.finishCLIRun(run, func(run *CLIRun) {
				run.State, run.Summary, run.Error = p.State, p.Summary, p.Error
				run.Session = p.Session
				run.In, run.Out, run.Cache, run.CostUSD = p.In, p.Out, p.Cache, p.CostUSD
			})
			return
		case "tool:approval":
			var p struct {
				ID      string         `json:"id"`
				Tool    string         `json:"tool"`
				Command string         `json:"command"`
				Args    map[string]any `json:"args"`
				Session string         `json:"session"`
			}
			if json.Unmarshal(ev.Payload, &p) != nil || p.Session != there.Thread {
				continue
			}
			actx, acancel := context.WithTimeout(ctx, cliApproveWait)
			askMu.Lock()
			asking[p.ID] = acancel
			askMu.Unlock()
			go a.relayRemoteApproval(actx, acancel, r, run, p.ID, p.Tool, p.Command, p.Args)
		case "tool:approval:closed":
			var p struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil {
				// Answered there, or timed out there: the card here goes.
				askMu.Lock()
				if c := asking[p.ID]; c != nil {
					c()
					delete(asking, p.ID)
				}
				askMu.Unlock()
			}
		}
	}
	a.finishCLIRun(run, func(run *CLIRun) {
		run.State, run.Error = "failed", "lost the connection to "+r.name
	})
}

// relayRemoteApproval asks here and answers there.
func (a *App) relayRemoteApproval(ctx context.Context, cancel context.CancelFunc, r cliRemote, run *CLIRun, remoteID, tool, command string, args map[string]any) {
	defer cancel()
	short := strings.TrimPrefix(tool, strings.SplitN(run.Agent, ".", 2)[0]+" · ")
	a.addCLIEvent(run, CLIRunEvent{Kind: "note", Tool: short, Text: "waiting for approval"})
	now := time.Now()
	dec, _ := a.askToolApproval(ctx, backend.ApprovalRequest{
		ID: uuid.NewString(), Tool: run.Agent + " · " + short, Command: command, Args: args,
		SessionID: run.Thread, AgentID: run.Agent, AskedAt: now, ExpiresAt: now.Add(cliApproveWait),
	})
	if ctx.Err() != nil && !dec.Allowed {
		return // answered on the other machine, or gone
	}
	cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer ccancel()
	_ = r.rpc(cctx, "ResolveToolApproval", []any{remoteID, dec.Allowed}, nil)
	if dec.Allowed {
		a.addCLIEvent(run, CLIRunEvent{Kind: "note", Tool: short, Text: "approved"})
	} else {
		a.addCLIEvent(run, CLIRunEvent{Kind: "note", Tool: short, Text: "denied: the user said no", Failed: true})
	}
}
