package app

// "以后同类请求不用问我": remembered answers, between YOLO (everything) and a
// card per call (nothing). The rules themselves and how they match live in
// backend/approval_rules.go; this is where they meet the cards.

import (
	"github.com/liliang-cn/superleo/internal/backend"
)

// approvalRules is the store for this app's data directory.
func (a *App) approvalRules() *backend.ApprovalRuleStore {
	return backend.SharedApprovalRules()
}

// preanswerApproval answers a request without a card when it can: YOLO first,
// as it always has, then a remembered rule. A call a session rule insists on
// asking about is never pre-answered.
func (a *App) preanswerApproval(req backend.ApprovalRequest) (backend.ApprovalDecision, bool) {
	if req.MustAsk {
		return backend.ApprovalDecision{}, false
	}
	if gate := a.toolGate(); gate != nil {
		// The gate's own calls never get here with YOLO on; a coding agent's
		// prompt or a hive worker's held call does, and "全部自动同意" means
		// those too — the same as switching it on answers the ones waiting.
		if on, _ := gate.YoloStatus(); on {
			return backend.ApprovalDecision{
				Allowed: true,
				By:      backend.DecidedByYolo,
				Reason:  "YOLO mode is on — approved without asking",
			}, true
		}
	}
	if r, ok := a.approvalRules().Match(req); ok {
		return backend.RuleDecision(r), true
	}
	return backend.ApprovalDecision{}, false
}

func (a *App) toolGate() *backend.ToolGate {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.ToolGate()
}

// recordApproval writes the audit line for a decision the gate did not make,
// to the gate's own log. Without a service there is no gate and no log.
func (a *App) recordApproval(req backend.ApprovalRequest, dec backend.ApprovalDecision) {
	if gate := a.toolGate(); gate != nil {
		gate.Record(req, dec)
	}
}

// ListApprovalRules is every remembered rule, oldest first.
func (a *App) ListApprovalRules() []backend.ApprovalRule {
	rules := a.approvalRules().List()
	if rules == nil {
		rules = []backend.ApprovalRule{}
	}
	return rules
}

// DeleteApprovalRule forgets one rule. Calls it would have answered are asked
// about again.
func (a *App) DeleteApprovalRule(id string) map[string]any {
	ok, err := a.approvalRules().Delete(id)
	if err != nil {
		return map[string]any{"deleted": false, "error": err.Error()}
	}
	if ok {
		a.emit("tool:approval:rules", map[string]any{"deleted": id})
	}
	return map[string]any{"deleted": ok}
}

// ResolveToolApprovalRemember answers a waiting call like ResolveToolApproval
// and remembers the answer for calls like it. remember.scope is one of the
// card's rule_suggestions (same_tool, same_tool_and_target,
// same_command_prefix); the rule saved is exactly the one that suggestion
// showed, with this answer as its decision. An empty scope is a plain answer.
//
// Answers {"result": <what ResolveToolApproval says>, "rule": <the rule>} or,
// when the rule could not be made, {"result": …, "error": …} — the answer
// itself is delivered either way.
func (a *App) ResolveToolApprovalRemember(id string, allow bool, remember backend.RememberOption) map[string]any {
	if remember.Scope == "" {
		return map[string]any{"result": a.ResolveToolApproval(id, allow)}
	}
	a.approvalMu.Lock()
	p := a.approvals[id]
	a.approvalMu.Unlock()
	if p == nil {
		return map[string]any{"result": a.ResolveToolApproval(id, allow)}
	}

	rule, err := backend.RuleFromApproval(p.req, remember.Scope, allow)
	if err == nil {
		rule.Note = remember.Note
		rule, err = a.approvalRules().Add(rule)
	}
	out := map[string]any{"result": a.ResolveToolApproval(id, allow)}
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	out["rule"] = rule
	a.emit("tool:approval:rules", map[string]any{"added": rule.ID})

	// Anything else already waiting that the new rule covers is answered by
	// it now, rather than left for a tap the rule just made pointless.
	a.approvalMu.Lock()
	var covered []*pendingApproval
	for oid, op := range a.approvals {
		if oid != id && !op.req.MustAsk && backend.RuleMatches(rule, op.req) {
			covered = append(covered, op)
		}
	}
	a.approvalMu.Unlock()
	dec := backend.RuleDecision(rule)
	for _, op := range covered {
		select {
		case op.answer <- dec:
		default:
		}
	}
	return out
}
