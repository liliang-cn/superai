package backend

// Per-session rules in front of the gate's own.
//
// A standing agent carries its own permissions: what it may do on its own,
// what it must ask about, what it may never do, and which connectors it may
// use at all. Those are about one agent, not about the tool, so they cannot
// live in the name rules; the host installs a SessionRule that knows which
// session belongs to which agent, and the gate asks it first.

import (
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Verdict is what a SessionRule says about one call.
type Verdict int

const (
	// VerdictNone: the rule has nothing to say; the gate decides as usual.
	VerdictNone Verdict = iota
	// VerdictAllow: allowed without asking.
	VerdictAllow
	// VerdictAsk: asked, even for a tool the gate would let through.
	VerdictAsk
	// VerdictDeny: refused without asking.
	VerdictDeny
)

// SessionRule judges a call before the gate's own rules. The reason is what
// the audit log and, on a denial, the model are told.
type SessionRule func(req agent.PermissionRequest) (Verdict, string)

// DecidedByRule marks a decision made by a SessionRule.
const DecidedByRule = "agent-rule"

// RuleAskWait is how long an ask raised by a rule waits. Rules belong to
// agents that run while nobody is looking: the card has to wait for a
// person to take their phone out, not for one already at the screen.
const RuleAskWait = 15 * time.Minute

// SetSessionRule installs (or with nil, removes) the rule.
func (g *ToolGate) SetSessionRule(r SessionRule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rule = r
}

func (g *ToolGate) sessionVerdict(req agent.PermissionRequest) (Verdict, string) {
	g.mu.RLock()
	r := g.rule
	g.mu.RUnlock()
	if r == nil {
		return VerdictNone, ""
	}
	return r(req)
}
