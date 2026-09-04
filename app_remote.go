package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
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

// RemoteAgentNames is what the composer's @ menu offers. Empty when the
// feature is off, so the menu simply never appears.
func (a *App) RemoteAgentNames() []map[string]string {
	cfg := a.remoteRunner().Config()
	if !cfg.Enabled {
		return []map[string]string{}
	}
	out := []map[string]string{}
	for _, name := range cfg.Names() {
		out = append(out, map[string]string{"name": name, "about": cfg.Agents[name].About})
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
	res, err := a.remoteRunner().Run(context.Background(), name, prompt)
	if err != nil {
		return backend.RemoteResult{Agent: name, Failed: true, Reason: err.Error()}
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
func (a *App) routeToRemote(requestID, name, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), a.remoteRunner().Config().Timeout())
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

		res, err := a.remoteRunner().Run(ctx, name, prompt)
		if err != nil {
			a.emit("chat:error", map[string]any{"requestId": requestID, "error": err.Error()})
			return
		}
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
		a.emit("chat:done", map[string]any{
			"requestId": requestID,
			// Attributed in the reply itself. The answer is not this app's and
			// a transcript that presented it unmarked would be putting another
			// agent's words in SuperAI's mouth.
			"final": fmt.Sprintf("**@%s** · %s · %.1fs\n\n%s", res.Agent, res.Host, float64(res.MS)/1000, res.Text),
		})
	}()
}
