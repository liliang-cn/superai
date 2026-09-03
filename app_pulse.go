package main

import (
	"context"

	"github.com/liliang-cn/superai-desktop/backend"
)

// The live meter, on the wire.
//
// Two ways in. A page that has just opened asks Pulse() once for the whole
// window; from then on the meter pushes frames — the second in progress, the
// totals, the events not yet sent — through the same channel every other
// event uses, so the desktop window and a browser tab both get them. A frame
// leaves the moment something has happened, at most five a second, and once
// every two seconds when nothing has.

// livePulse returns the app's one Pulse, starting its push loop on first use.
func (a *App) livePulse() *backend.Pulse {
	a.pulseOnce.Do(func() {
		a.pulse = backend.NewPulse()
		a.pulse.Start(context.Background(), a.emit)
	})
	return a.pulse
}

// Pulse reports the whole window: the per-second histogram of the last two
// minutes, the runs with something open, the tools that have fired and the
// tail of recent activity. Frames carry it from here.
func (a *App) Pulse() backend.PulseSnapshot {
	return a.livePulse().Snapshot()
}
