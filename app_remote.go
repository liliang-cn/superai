package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superai-desktop/backend"
)

// Asking another agent, by name.
//
// Two ways in, and they are deliberately different, because "@pi" at the front
// of a message and "@pi" in the middle of one mean different things to the
// person typing:
//
//   - At the front, it is an address. The message goes to pi and pi's answer
//     comes back. SuperAI's own model never runs, which is the point: a
//     forwarded question should not cost a turn of thinking to forward.
//   - Anywhere else, it is a mention. SuperAI runs the turn and decides, and
//     the tools below are how it acts on that — so "let @pi and @hermes each
//     look and compare" is one turn that makes two calls, rather than a
//     routing rule that cannot express it.
//
// The routing half lives in SendChat; this file holds the parse and the tools.
//
// Both kinds of agent share the one @ namespace, because the difference
// between them is a deployment detail. @pi is on a cluster and @claude is a
// binary in /usr/local/bin, and a person typing an @ is choosing who to ask,
// not choosing a transport. Remote names are checked first; a local CLI with
// the same name as a configured remote one would be shadowed, which is the
// right way round — the settings file is the more deliberate of the two.

// remoteRunner builds the runner on first use, from the settings in force.
func (a *App) remoteRunner() *backend.RemoteRunner {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil {
		return backend.NewRemoteRunner(backend.RemoteAgents{})
	}
	a.remoteOnce.Do(func() { a.remote = backend.NewRemoteRunner(s.RemoteAgents) })
	return a.remote
}

// resetRemote drops the cached runner so the next call rebuilds it from the
// settings that were just saved — otherwise a changed host list would go on
// being ignored until the app restarted. Callers hold a.mu.
func (a *App) resetRemote() {
	a.remoteOnce = sync.Once{}
	a.remote = nil
}

// Addressed reports the agent a message is addressed to, and what is left of
// the message once the address is taken off.
//
// Only at the very start, and only when a name is followed by whitespace or
// nothing: an email address, a Go struct tag and a decorator all contain an @
// in the middle of text, and treating those as an address would silently send
// somebody's paragraph to another machine. A bare "@pi" with nothing after it
// is an address with an empty question, which the caller reports rather than
// forwards.
func addressedTo(msg string, known func(string) bool) (agentName, rest string, ok bool) {
	trimmed := strings.TrimLeft(msg, " \t")
	if !strings.HasPrefix(trimmed, "@") {
		return "", msg, false
	}
	body := trimmed[1:]
	end := strings.IndexFunc(body, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.')
	})
	name := body
	if end >= 0 {
		name = body[:end]
	}
	if name == "" || !known(name) {
		return "", msg, false
	}
	if end < 0 {
		return name, "", true
	}
	// The character that ended the name has to be a separator. "@pi," is an
	// address; "@pi/foo" is a path someone is talking about.
	//
	// Decoded, not indexed: body[end] is a byte, and the separator a Chinese
	// keyboard produces is three of them — so the byte compared here was 0xEF
	// and "@pi，看一下" fell through to the model.
	sep, _ := utf8.DecodeRuneInString(body[end:])
	if !unicode.IsSpace(sep) && sep != ':' && sep != ',' && sep != '，' && sep != '：' {
		return "", msg, false
	}
	rest = strings.TrimLeft(body[end:], " \t\r\n:,，：")
	return name, rest, true
}

// localAgentNames is the CLI agents installed on this machine, when the
// external-agents setting is on. Discovery probes each binary, so the answer
// is what can actually be started rather than what a list once claimed.
func (a *App) localAgentNames() []agentexec.Installed {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil || !s.ExternalAgents.Enabled {
		return nil
	}
	return agentexec.Discover(s.ExternalAgents.Binaries)
}

// addressable reports whether a name can be reached at all, either way.
func (a *App) addressable(name string) bool {
	if a.remoteRunner().Config().Has(name) {
		return true
	}
	for _, c := range a.localAgentNames() {
		if c.Name == name {
			return true
		}
	}
	return false
}

// RemoteAgentNames is what the composer's @ menu offers: the agents on other
// machines and the CLIs on this one, in one list. Empty when both features are
// off, so the menu simply never appears.
func (a *App) RemoteAgentNames() []map[string]string {
	out := []map[string]string{}
	cfg := a.remoteRunner().Config()
	if cfg.Enabled {
		for _, name := range cfg.Names() {
			out = append(out, map[string]string{"name": name, "about": cfg.Agents[name].About})
		}
	}
	for _, c := range a.localAgentNames() {
		if cfg.Has(c.Name) {
			continue
		}
		about := c.Name + " — a coding agent installed on this machine"
		if c.Version != "" {
			about += " (" + c.Version + ")"
		}
		out = append(out, map[string]string{"name": c.Name, "about": about})
	}
	return out
}

// AskRemoteAgent is the routing path: the frontend hands over a message that
// began with an address, and gets that agent's answer back.
//
// A method of its own rather than a branch inside SendChat's reply, because
// the two produce different things. SendChat streams a turn; this returns one
// answer and never touches the conversation's history — a routed question is
// addressed to somebody else, and folding its answer into SuperAI's own
// session would make the next turn read as if SuperAI had said it.
func (a *App) AskRemoteAgent(name, prompt string) backend.RemoteResult {
	return a.askAgent(context.Background(), name, prompt)
}

// askAgent sends a question to whichever kind of agent the name belongs to.
func (a *App) askAgent(ctx context.Context, name, prompt string) backend.RemoteResult {
	name = strings.TrimSpace(name)
	if a.remoteRunner().Config().Has(name) {
		res, err := a.remoteRunner().Run(ctx, name, prompt)
		if err != nil {
			return backend.RemoteResult{Agent: name, Failed: true, Reason: err.Error()}
		}
		return res
	}
	return a.askLocalCLI(ctx, name, prompt)
}

// askLocalCLI forwards to the cli_agent_run tool, which is where the argv
// dialects, the trust-directory bypasses and the usage accounting for the
// locally installed CLIs already live. Re-implementing any of that here would
// mean two places to fix when a CLI changes its flags.
func (a *App) askLocalCLI(ctx context.Context, name, prompt string) backend.RemoteResult {
	started := time.Now()
	fail := func(reason string) backend.RemoteResult {
		return backend.RemoteResult{
			Agent: name, Host: "this machine", Failed: true,
			Reason: reason, MS: time.Since(started).Milliseconds(),
		}
	}

	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return fail("the backend is not ready")
	}
	reg := svc.Agent().GetToolRegistry()
	if reg == nil || !reg.Has("cli_agent_run") {
		return fail("agent CLIs are switched off in Settings (External agents)")
	}

	out, err := reg.Call(ctx, "cli_agent_run", map[string]any{"agent": name, "prompt": prompt})
	if err != nil {
		return fail(err.Error())
	}

	// The tool answers with a struct, not a string. Round-tripping it through
	// JSON reads the fields without importing the type, and — more to the
	// point — without fmt.Sprint, which renders it as Go map syntax and would
	// put "map[agent:claude duration_ms:10058 …]" in front of a person.
	var got struct {
		Summary  string  `json:"summary"`
		Failed   bool    `json:"failed"`
		ExitCode int     `json:"exit_code"`
		Error    string  `json:"error"`
		CostUSD  float64 `json:"cost_usd"`
	}
	raw, merr := json.Marshal(out)
	if merr != nil || json.Unmarshal(raw, &got) != nil {
		return fail("could not read what the CLI returned")
	}

	res := backend.RemoteResult{
		Agent: name, Host: "this machine",
		Text: strings.TrimSpace(got.Summary),
		MS:   time.Since(started).Milliseconds(),
	}
	// Failed is the only verdict. claude with a revoked token prints
	// "Failed to authenticate" as its answer and exits 0, so a caller that
	// trusted the exit code would relay an auth failure as a reply — which is
	// the one failure mode of this whole feature that produces a confident,
	// wrong-looking-like-right answer.
	if got.Failed {
		res.Failed = true
		// Condensed for the same reason the SSH path condenses: a CLI that
		// fails on an API error prints the whole body, and the transcript is
		// not the place for a page of JSON.
		res.Reason = backend.CondenseAgentFailure(got.Error)
		if res.Reason == "" {
			res.Reason = fmt.Sprintf("the CLI reported a failure (exit %d)", got.ExitCode)
		}
		return res
	}
	if res.Text == "" {
		res.Failed, res.Reason = true, "the CLI exited cleanly without saying anything"
	}
	return res
}

// registerRemoteTools gives the model the mention half.
//
// Takes the service for the same reason registerPetTools does: it is called
// from the build, which already holds a.mu.
func (a *App) registerRemoteTools(svc *backend.Service, cfg backend.RemoteAgents) {
	if svc == nil || !cfg.Enabled || len(cfg.Agents) == 0 {
		return
	}
	inner := svc.Agent()
	if inner == nil {
		return
	}

	var catalogue strings.Builder
	for _, name := range cfg.Names() {
		fmt.Fprintf(&catalogue, "\n  · %s — %s", name, cfg.Agents[name].About)
	}

	inner.AddToolWithMetadata("remote_agent_list",
		"The agents on other machines this app can ask, and what each is for.",
		map[string]any{"type": "object", "properties": map[string]any{}},
		func(ctx context.Context, _ map[string]any) (any, error) {
			out := []map[string]string{}
			for _, name := range cfg.Names() {
				out = append(out, map[string]string{"name": name, "about": cfg.Agents[name].About})
			}
			b, err := json.Marshal(out)
			return string(b), err
		},
		agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true})

	// Destructive, and not because it edits anything here. It runs a command
	// on another machine under a real account, and that is the same class of
	// decision as running one on this one — so it meets the approval gate the
	// way cli_agent_run does.
	inner.AddToolWithMetadata("remote_agent_run",
		"Ask an agent on another machine a question and get its answer back."+
			"\n\nRight when the other agent knows something this one does not — it is on the"+
			" machine in question, it has its own memory, its own tools, its own view of that"+
			" host. Right when the person named it."+
			"\n\nWrong as a way to avoid thinking: it is slower than answering, it spends a"+
			" second model's budget, and it comes back with prose you still have to read and"+
			" judge. Wrong for anything about this machine or this conversation, which it"+
			" cannot see."+
			"\n\nThe answer is one shot: the agent has no memory of your last call and cannot"+
			" ask you anything, so put everything it needs in the question."+
			"\n\nAvailable:"+catalogue.String(),
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"enum":        cfg.Names(),
					"description": "Which agent to ask.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The whole question, standing on its own.",
				},
			},
			"required": []string{"agent", "prompt"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			res, err := a.remoteRunner().Run(ctx, str(args["agent"]), str(args["prompt"]))
			if err != nil {
				return nil, err
			}
			// The verdict comes first and in words, because the failure that
			// matters here is the one that reads like an answer: a CLI sitting
			// on a login prompt prints a paragraph and exits, and a result that
			// buried "failed" in a field would be relayed to the person as
			// though the other agent had said it.
			if res.Failed {
				return fmt.Sprintf("%s did not answer (%s). What came back, if anything:\n%s",
					res.Agent, res.Reason, res.Text), nil
			}
			return fmt.Sprintf("%s answered, from %s, in %.1fs:\n\n%s",
				res.Agent, res.Host, float64(res.MS)/1000, res.Text), nil
		},
		agent.ToolMetadata{Destructive: true})
}

// routeToRemote forwards an addressed message and reports the answer on the
// chat channel, so the transcript draws it the way it draws any other reply.
//
// It emits the same three events a turn does — event / done / error — rather
// than a shape of its own, because everything downstream (the transcript, the
// history, the toast on failure, the request-id plumbing that lets Stop work)
// already understands those. A fourth event kind would mean teaching all of it
// about a case that differs only in who answered.
//
// The exchange is then written into the conversation. It first was not, on the
// reasoning that another agent's words should not be filed under SuperAI's
// name — but the reply carries its own attribution, and leaving it out meant
// the answer existed on screen and nowhere else: the next turn could not refer
// to what @hermes had just said, and reopening the conversation showed a gap
// where the question had been. Being addressed to someone else is not the same
// as not having happened.
func (a *App) routeToRemote(requestID, sessionID, name, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), a.routedTimeout(name))
	a.trackRun(requestID, cancel)

	go func() {
		defer cancel()
		defer a.untrackRun(requestID)

		if strings.TrimSpace(prompt) == "" {
			a.emit("chat:error", map[string]any{
				"requestId": requestID,
				"error":     "@" + name + " on its own does not ask anything. Put the question after the name.",
			})
			return
		}

		// Said before the wait, not after: an SSH round trip to a cluster node
		// plus another agent's whole turn is tens of seconds of nothing, and a
		// composer that has already cleared with no sign of life reads as a
		// message that was dropped.
		a.emit("chat:event", map[string]any{
			"requestId": requestID,
			"type":      "state_update",
			"content":   "asking " + name + "…",
		})

		res := a.askAgent(ctx, name, prompt)
		if a.runCancelled(requestID) || ctx.Err() != nil {
			a.emit("chat:cancelled", map[string]any{"requestId": requestID, "final": res.Text})
			return
		}
		if res.Failed {
			a.emit("chat:error", map[string]any{
				"requestId": requestID,
				"error":     name + " did not answer: " + res.Reason,
			})
			return
		}
		// Attributed in the reply itself. The answer is not this app's and a
		// transcript that presented it unmarked would be putting another
		// agent's words in SuperAI's mouth.
		reply := fmt.Sprintf("**@%s** · %s · %.1fs\n\n%s", res.Agent, res.Host, float64(res.MS)/1000, res.Text)
		a.recordRoutedExchange(sessionID, name, prompt, reply)
		a.emit("chat:done", map[string]any{"requestId": requestID, "final": reply})
	}()
}

// recordRoutedExchange files an addressed question and its answer in the
// conversation they happened in.
//
// The question goes in as the person typed it, @ and all: a later turn reading
// the history has to be able to tell that this was addressed elsewhere, and
// the name is the only thing that says so. The answer keeps the attribution
// line it was shown with, for the same reason.
//
// Best effort. A conversation that could not be written to is worth a log line
// and nothing more — the answer is already on screen, and failing the turn
// over its bookkeeping would take away the thing that did work.
func (a *App) recordRoutedExchange(sessionID, name, prompt, reply string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return
	}
	inner := svc.Agent()
	if inner == nil {
		return
	}
	if err := inner.AppendSessionMessages(sessionID,
		domain.Message{Role: "user", Content: "@" + name + " " + prompt},
		domain.Message{Role: "assistant", Content: reply},
	); err != nil {
		log.Printf("superai: could not record the @%s exchange in %s: %v", name, sessionID, err)
	}
}

// routedTimeout is how long an addressed message may take, which depends on
// who it was addressed to: a question to a cluster agent is bounded by the
// remote setting, a task handed to a local CLI by the far more generous
// external-agents one. Taking the smaller of the two would cut off exactly the
// runs that are worth delegating.
func (a *App) routedTimeout(name string) time.Duration {
	if a.remoteRunner().Config().Has(name) {
		return a.remoteRunner().Config().Timeout()
	}
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil {
		return backend.DefaultExternalAgentTimeout
	}
	return s.ExternalAgents.Timeout()
}

// RemoteAgentStatus is what one agent looked like when it was last asked.
type RemoteAgentStatus struct {
	Name  string `json:"name"`
	About string `json:"about"`
	// Local separates a CLI on this machine from one on the network. They are
	// the same to the person asking and very different to debug.
	Local bool     `json:"local"`
	Hosts []string `json:"hosts,omitempty"`
	// Reachable is the answer to a real question, not to a lookup.
	Reachable bool   `json:"reachable"`
	Where     string `json:"where,omitempty"`
	Detail    string `json:"detail,omitempty"`
	MS        int64  `json:"ms"`
}

// CheckRemoteAgents asks every configured agent one trivial question and
// reports who answered.
//
// A real question and not a probe of the binary, because the lesson this
// feature keeps re-teaching is that installed is not usable: a CLI with a
// revoked token, a node that no longer holds the volume and a model whose tier
// was withdrawn all look perfectly healthy until something is actually asked.
// It costs a token per agent, which is why it is a button rather than
// something the settings page does on open.
func (a *App) CheckRemoteAgents() []RemoteAgentStatus {
	listed := a.RemoteAgentNames()
	out := make([]RemoteAgentStatus, len(listed))
	cfg := a.remoteRunner().Config()

	var wg sync.WaitGroup
	for i, entry := range listed {
		name, about := entry["name"], entry["about"]
		st := RemoteAgentStatus{Name: name, About: about, Local: !cfg.Has(name)}
		if !st.Local {
			st.Hosts = cfg.Agents[name].Hosts
		}
		out[i] = st

		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			// Bounded well under the run timeout: this is a health check and a
			// person is watching it, so an agent that needs two minutes to say
			// hello is failing the check by definition.
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			res := a.askAgent(ctx, name, "Reply with exactly: ok")
			out[i].MS = res.MS
			out[i].Where = res.Host
			out[i].Reachable = !res.Failed
			if res.Failed {
				out[i].Detail = res.Reason
			} else {
				out[i].Detail = strings.TrimSpace(res.Text)
			}
		}(i, name)
	}
	wg.Wait()
	return out
}
