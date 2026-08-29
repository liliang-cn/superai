package backend

import (
	"fmt"
	"time"
)

// YOLO mode: approve everything for a while, then stop.
//
// The gate exists because an agent running shell commands with the user's
// permissions should be asked about. But there is a real cost to asking, and it
// falls hardest exactly when the work is going well: a long build-test-fix run
// is twenty commands the user would have approved without reading, and a prompt
// per command turns "watch it work" into "click Allow twenty times". That is
// how people end up turning the gate off in Settings and forgetting.
//
// So this is the pressure valve, and its whole design is about ending:
//
//   - It takes a window, never a toggle. There is no "on" — only "on until".
//   - The window is clamped. An hour is long enough for the run this exists
//     for; a day is someone disabling the gate without admitting it, and they
//     should use the Settings switch and see what they are doing.
//   - It expires by comparing clocks, not by a timer. A goroutine that was
//     supposed to switch it off can be lost to a panic or a paused process;
//     asking "is it still before the deadline?" cannot be.
//   - Everything it waves through is still audited, marked as decided by YOLO
//     rather than by a person, because "what ran while I was not looking" is
//     precisely the question this mode creates.

// MaxYoloWindow caps how long everything can be approved for.
//
// One hour, because this is meant to cover a run the user is watching. Wanting
// longer is wanting the gate off, which is a different decision with its own
// switch, made deliberately in Settings rather than by typing a big number.
const MaxYoloWindow = time.Hour

// DefaultYoloWindow is what the UI asks for when the user does not say.
const DefaultYoloWindow = 15 * time.Minute

// DecidedByYolo marks an audit entry that nobody actually looked at.
const DecidedByYolo = "yolo"

// StartYolo approves everything until the window elapses, and reports the
// window actually granted — which may be shorter than the one asked for.
func (g *ToolGate) StartYolo(d time.Duration) time.Duration {
	if g == nil {
		return 0
	}
	if d <= 0 {
		d = DefaultYoloWindow
	}
	if d > MaxYoloWindow {
		d = MaxYoloWindow
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.yoloUntil = g.now().Add(d)
	return d
}

// StopYolo ends it now.
func (g *ToolGate) StopYolo() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.yoloUntil = time.Time{}
}

// YoloStatus reports whether it is on and when it ends.
//
// The UI needs this to keep saying so while it lasts. A mode you cannot see is
// on is a mode you forget is on, and this one turns every prompt into a yes.
func (g *ToolGate) YoloStatus() (bool, time.Time) {
	if g == nil {
		return false, time.Time{}
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.yoloActiveLocked(), g.yoloUntil
}

// yoloActiveLocked answers from the clock rather than from a flag someone was
// supposed to clear. Callers hold g.mu.
func (g *ToolGate) yoloActiveLocked() bool {
	return !g.yoloUntil.IsZero() && g.now().Before(g.yoloUntil)
}

// yoloDecision is the answer YOLO gives, or nil when it is not on.
func (g *ToolGate) yoloDecision() *ApprovalDecision {
	g.mu.RLock()
	on, until := g.yoloActiveLocked(), g.yoloUntil
	g.mu.RUnlock()
	if !on {
		return nil
	}
	return &ApprovalDecision{
		Allowed: true,
		By:      DecidedByYolo,
		Reason: fmt.Sprintf("YOLO mode is on until %s — approved without asking",
			until.Format("15:04:05")),
	}
}
