package app

// "@claude …" and "@codex …" in a conversation, run as a CLI run the chat
// watches: the CLI's text streams into the reply, its tool calls and their
// results into the trace, a permission prompt becomes an approval card, and
// Stop stops it. The next @mention of the same agent in the same conversation
// resumes that CLI's session, so "@claude now add a test for it" means the
// thing it just wrote.

import (
	"fmt"
	"strings"
	"time"
)

// routeToCLI answers an addressed message with a local agent CLI.
func (a *App) routeToCLI(requestID, sessionID, name, prompt string) {
	if strings.TrimSpace(prompt) == "" {
		a.emit("chat:error", map[string]any{
			"requestId": requestID,
			"error":     "@" + name + " on its own does not ask anything. Put the question after the name.",
		})
		return
	}
	a.mu.Lock()
	unattended := a.settings != nil && a.settings.ExternalAgents.Unattended
	a.mu.Unlock()

	o := cliStart{Agent: name, Prompt: prompt, Ask: !unattended, Chat: sessionID}
	if prev := a.lastChatRun(sessionID, name); prev != nil {
		o.Thread, o.Session, o.Cwd, o.Model, o.Ask = prev.Thread, prev.Session, prev.Cwd, prev.Model, prev.Ask
	}

	var said []string
	o.Watch = func(e CLIRunEvent) {
		switch e.Kind {
		case "text":
			chunk := e.Text
			if len(said) > 0 {
				chunk = "\n\n" + chunk
			}
			said = append(said, e.Text)
			a.emit("chat:event", map[string]any{"requestId": requestID, "type": "partial", "content": chunk})
		case "tool":
			a.emit("chat:event", map[string]any{"requestId": requestID, "type": "tool_call",
				"tool": name + "·" + e.Tool, "args": map[string]any{"detail": e.Detail}})
		case "result":
			tool := e.Tool
			if tool == "" {
				tool = "tool"
			}
			a.emit("chat:event", map[string]any{"requestId": requestID, "type": "tool_result",
				"tool": name + "·" + tool, "result": map[string]any{"ok": !e.Failed, "output": e.Text}})
		case "note":
			if e.Text == "waiting for approval" {
				a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update",
					"content": fmt.Sprintf("%s wants to use %s — waiting for your approval", name, e.Tool)})
			}
		}
	}
	started := time.Now()
	o.Done = func(r CLIRun) {
		defer a.untrackRun(requestID)
		switch r.State {
		case "cancelled":
			a.emit("chat:cancelled", map[string]any{"requestId": requestID, "final": strings.Join(said, "\n\n")})
			return
		case "failed":
			a.emit("chat:error", map[string]any{"requestId": requestID, "error": name + " did not finish: " + r.Error})
			return
		}
		answer := strings.TrimSpace(r.Summary)
		if answer == "" && len(said) > 0 {
			answer = said[len(said)-1]
		}
		head := fmt.Sprintf("**@%s** · %.1fs · %d tool calls", name, time.Since(started).Seconds(), r.Tools)
		if r.CostUSD > 0 {
			head += fmt.Sprintf(" · $%.3f", r.CostUSD)
		}
		reply := head + "\n\n" + answer
		a.recordRoutedExchange(sessionID, name, prompt, reply)
		a.emit("chat:done", map[string]any{"requestId": requestID, "final": reply})
	}

	a.emit("chat:event", map[string]any{"requestId": requestID, "type": "state_update", "content": "starting " + name + "…"})
	run, err := a.startCLIRun(o)
	if err != nil {
		a.emit("chat:error", map[string]any{"requestId": requestID, "error": err.Error()})
		return
	}
	a.trackRun(requestID, func() { a.CancelCLIRun(run.ID) })
}

// lastChatRun is the newest finished run of an agent started from a
// conversation, the one whose session the next @mention continues.
func (a *App) lastChatRun(chat, agent string) *CLIRun {
	if chat == "" {
		return nil
	}
	s := a.cliStore()
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *CLIRun
	for _, r := range s.runs {
		if r.Chat == chat && r.Agent == agent && r.Session != "" && r.State != "running" {
			if best == nil || r.Started.After(best.Started) {
				c := *r
				c.Events = nil
				best = &c
			}
		}
	}
	return best
}

// isLocalCLI reports whether a name is an agent CLI on this machine that the
// run manager can drive, rather than an agent configured on another host.
func (a *App) isLocalCLI(name string) bool {
	if a.remoteRunner().Config().Has(name) {
		return false
	}
	for _, c := range a.localAgentNames() {
		if c.Name == name {
			return true
		}
	}
	return false
}
