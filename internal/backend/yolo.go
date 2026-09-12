package backend

import "time"

// YOLO mode: approve everything, until it is switched off.
//
// The gate exists because an agent running shell commands with the user's
// permissions should be asked about. But there is a real cost to asking, and it
// falls hardest exactly when the work is going well: a long build-test-fix run
// is twenty commands the user would have approved without reading, and a prompt
// per command turns "watch it work" into "click Allow twenty times".
//
// So this is the pressure valve, and it is a switch:
//
//   - On means on. No window, no clamp, no expiry. A mode that ends on its own
//     ends in the middle of the run it was turned on for, and the prompt comes
//     back at the one moment nobody wants to read it.
//   - It ends when somebody ends it — from the banner, or from Settings.
//   - Everything it waves through is still audited, marked as decided by YOLO
//     rather than by a person, because "what ran while I was not looking" is
//     precisely the question this mode creates.
//
// The banner in the window is what makes that safe to have: prompts stopping is
// the sort of absence nobody notices, so the state is stated continuously
// rather than left to be remembered.

// DecidedByYolo marks an audit entry that nobody actually looked at.
const DecidedByYolo = "yolo"

// StartYolo approves everything from now until StopYolo.
func (g *ToolGate) StartYolo() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.yolo = true
	g.yoloSince = g.now()
}

// StopYolo ends it, and the gate goes back to asking.
func (g *ToolGate) StopYolo() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.yolo = false
	g.yoloSince = time.Time{}
}

// YoloStatus reports whether it is on, and since when.
//
// The UI needs this to keep saying so. A mode you cannot see is on is a mode
// you forget is on, and this one turns every prompt into a yes — which is why
// the banner shows how long it has been that way.
func (g *ToolGate) YoloStatus() (bool, time.Time) {
	if g == nil {
		return false, time.Time{}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.yolo, g.yoloSince
}

// yoloDecision is the answer YOLO gives, or nil when it is not on.
func (g *ToolGate) yoloDecision() *ApprovalDecision {
	g.mu.RLock()
	on := g.yolo
	g.mu.RUnlock()
	if !on {
		return nil
	}
	return &ApprovalDecision{
		Allowed: true,
		By:      DecidedByYolo,
		Reason:  "YOLO mode is on — approved without asking",
	}
}
