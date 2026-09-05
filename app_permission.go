package main

// The UI half of the tool approval gate.
//
// backend.ToolGate decides *that* a call needs a human and enforces the
// deadline; this file is the human. It is one approver serving both surfaces,
// because both surfaces are the same App: in the desktop build a.emit reaches
// the window through the Wails runtime, in `serve` mode it reaches the browser
// through the SSE hub, and the answer comes back through a bound method that
// the RPC bridge exposes automatically. Nothing here knows which one it is on,
// which is the property that keeps the two frontends one frontend.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/liliang-cn/superai-desktop/backend"
)

// pendingApproval is one tool call parked in front of the user, with the
// channel the agent goroutine is blocked on.
type pendingApproval struct {
	req backend.ApprovalRequest
	// Buffered, and sent to with a default branch: a user who double-clicks
	// Allow, or an Allow that races the deadline, must not block a bound
	// method on a channel nobody is reading any more.
	answer chan backend.ApprovalDecision
}

// askToolApproval is the backend.Approver installed on every service this app
// builds. It blocks until the user answers or the gate stops waiting.
func (a *App) askToolApproval(ctx context.Context, req backend.ApprovalRequest) (backend.ApprovalDecision, error) {
	p := &pendingApproval{req: req, answer: make(chan backend.ApprovalDecision, 1)}

	a.approvalMu.Lock()
	if a.approvals == nil {
		a.approvals = make(map[string]*pendingApproval)
	}
	a.approvals[req.ID] = p
	a.approvalMu.Unlock()

	defer func() {
		a.approvalMu.Lock()
		delete(a.approvals, req.ID)
		a.approvalMu.Unlock()
		// Tell the UI to retire the card whichever way this ended — answered,
		// timed out, or run cancelled. Without it a stopped run leaves a
		// prompt on screen for a tool call that no longer exists, and clicking
		// Allow on it does nothing, which is the worst possible thing for a
		// button whose whole job is to be trusted.
		a.emit("tool:approval:closed", map[string]any{"id": req.ID})
	}()

	a.emit("tool:approval", approvalPayload(req))

	select {
	case dec := <-p.answer:
		return dec, nil
	case <-ctx.Done():
		// The gate gave up (deadline) or the run ended. It has already decided
		// what that means and will write the reason; anything returned here is
		// discarded, so return the deny rather than an error, which would be
		// recorded as "could not ask" if it ever were read.
		return backend.ApprovalDecision{Allowed: false}, nil
	}
}

// approvalPayload is the event the frontend renders. Nothing is redacted: the
// user is being asked to vouch for this command, and a command they cannot
// read in full is one they cannot vouch for.
func approvalPayload(req backend.ApprovalRequest) map[string]any {
	return map[string]any{
		"id":        req.ID,
		"tool":      req.Tool,
		"command":   req.Command,
		"args":      req.Args,
		"session":   req.SessionID,
		"agent":     req.AgentID,
		"askedAt":   req.AskedAt.Format(time.RFC3339),
		"expiresAt": req.ExpiresAt.Format(time.RFC3339),
	}
}

// PendingToolApprovals lists the prompts still waiting for an answer.
//
// Bound because events are fire-and-forget on both surfaces: a browser tab
// that reloads, or one that connects to /api/events after the agent already
// asked, would otherwise never see the prompt and the call would sit there
// until it timed out. The frontend calls this on mount and gets back whatever
// it missed.
func (a *App) PendingToolApprovals() []map[string]any {
	a.approvalMu.Lock()
	defer a.approvalMu.Unlock()
	out := make([]map[string]any, 0, len(a.approvals))
	for _, p := range a.approvals {
		out = append(out, approvalPayload(p.req))
	}
	return out
}

// ResolveToolApproval delivers the user's answer to the waiting tool call.
//
// One bound method serves both surfaces: Wails exposes it to the window and
// server.go's reflection bridge exposes the same method at
// POST /api/rpc/ResolveToolApproval, so the browser frontend needs no separate
// route and the two cannot drift apart.
func (a *App) ResolveToolApproval(id string, allow bool) string {
	a.approvalMu.Lock()
	p := a.approvals[id]
	a.approvalMu.Unlock()
	if p == nil {
		// Already answered, already timed out, or the run was stopped. Not an
		// error worth throwing at the UI — the card is about to disappear
		// anyway — but the frontend shows the string so a user who clicked
		// just too late learns why nothing happened.
		return "that request is no longer waiting (it timed out or the run ended)"
	}
	reason := "denied by the user"
	if allow {
		reason = "approved by the user"
	}
	select {
	case p.answer <- backend.ApprovalDecision{Allowed: allow, By: backend.DecidedByUser, Reason: reason}:
		return "ok"
	default:
		return "already answered"
	}
}

// ToolApprovalInfo is the Settings page's view of the gate: whether it is on,
// how long it waits, where the log is, and the tail of the log itself.
func (a *App) ToolApprovalInfo(limit int) map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()

	info := map[string]any{
		"enabled":      false,
		"waitSeconds":  int(backend.DefaultApprovalWait / time.Second),
		"auditPath":    backend.AuditLogPath(),
		"entries":      []backend.AuditEntry{},
		"serviceReady": svc != nil,
	}
	if svc == nil {
		return info
	}
	gate := svc.ToolGate()
	if gate != nil {
		// Included so a page that loads mid-window sees the banner. Events
		// alone would leave a reloaded tab believing prompts are still coming.
		yoloOn, yoloSince := gate.YoloStatus()
		info["yolo"] = yoloOn
		if yoloOn {
			info["yoloSince"] = yoloSince.Format(time.RFC3339)
		}
	}
	if gate == nil {
		return info
	}
	info["enabled"] = gate.Enabled()
	info["waitSeconds"] = int(gate.Wait() / time.Second)
	if p := gate.AuditPath(); p != "" {
		info["auditPath"] = p
	}
	if entries := gate.Log(limit); len(entries) > 0 {
		info["entries"] = entries
	}
	return info
}

// StartYoloMode approves every gated call until it is switched off.
//
// Bound so the approval modal can offer it at the moment it is wanted: the
// prompt a user is about to click through for the twentieth time is exactly
// where "stop asking" belongs, and burying it in Settings is how people end up
// turning the gate off permanently instead — which is the same thing, minus the
// banner that keeps saying so.
func (a *App) StartYoloMode() map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil || svc.ToolGate() == nil {
		return map[string]any{"error": "the approval gate is not running"}
	}
	svc.ToolGate().StartYolo()
	on, since := svc.ToolGate().YoloStatus()

	// Announced rather than left to be noticed. Every surface that could be
	// showing an approval card needs to know they are about to stop appearing,
	// and a user who left the tab open needs to see the banner without asking.
	a.emit("tool:yolo", map[string]any{
		"active": on,
		"since":  since.Format(time.RFC3339),
	})

	// Answer whatever is already on screen, or those cards sit there waiting
	// for a click that YOLO has made meaningless.
	a.approvalMu.Lock()
	pending := make([]string, 0, len(a.approvals))
	for id := range a.approvals {
		pending = append(pending, id)
	}
	a.approvalMu.Unlock()
	for _, id := range pending {
		_ = a.ResolveToolApproval(id, true)
	}

	return map[string]any{"active": on, "since": since.Format(time.RFC3339)}
}

// StopYoloMode ends it now.
func (a *App) StopYoloMode() map[string]any {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil || svc.ToolGate() == nil {
		return map[string]any{"error": "the approval gate is not running"}
	}
	svc.ToolGate().StopYolo()
	a.emit("tool:yolo", map[string]any{"active": false})
	return map[string]any{"active": false}
}

// graphProxy forwards to whatever live view this process is running.
//
// The address is resolved per request, not captured: the view starts on demand
// and restarts on a new port whenever the memory backend changes.
func (a *App) graphProxy() http.Handler {
	return backend.NewGraphProxy(func() string {
		a.mu.Lock()
		graphs := &a.graphs
		a.mu.Unlock()
		if sv := graphs.Running(); sv != nil {
			return sv.URL()
		}
		return ""
	})
}

// avatarProxy forwards to the avatar bridge this process is running.
//
// The port is read per request rather than captured: it comes from settings and
// a save rebuilds the server on a new one.
func (a *App) avatarProxy() http.Handler {
	return backend.NewAvatarProxy(func() string {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.settings == nil || a.settings.AvatarPort <= 0 {
			return ""
		}
		return fmt.Sprintf("http://127.0.0.1:%d", a.settings.AvatarPort)
	})
}
