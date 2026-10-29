package app

import (
	"context"

	"github.com/liliang-cn/superleo/internal/backend"
)

// Doctor inspects this install and reports what it found.
//
// It deliberately does not go through a.svc: the question it answers is "is
// this machine set up correctly", and the commonest reason to ask it is that
// the service failed to build. A check that needs the thing it is checking
// would be silent exactly when it is wanted.
func (a *App) Doctor() backend.DoctorReport {
	return backend.RunDoctor(context.Background())
}

// ExternalAgentsStatus reports which agent CLIs are on this machine.
//
// Separate from Doctor() because the settings page asks a narrower question
// and asks it while someone is looking at the switch: it needs the list of
// four, installed or not, to render the section — Doctor() only carries a line
// per CLI it found, since four "not installed" rows would be noise in a health
// report. Both go through the same probe, so they cannot disagree.
func (a *App) ExternalAgentsStatus() []backend.ExternalAgentStatus {
	a.mu.Lock()
	var ext backend.ExternalAgents
	if a.settings != nil {
		ext = a.settings.ExternalAgents
	}
	a.mu.Unlock()
	// The user-added tools too, so a SuperLeo that reaches this one by URL
	// can offer them as "tool.machine".
	return append(backend.ExternalAgentStatuses(context.Background(), ext.Binaries), customAgentStatuses(ext)...)
}
