package backend

import (
	"context"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// Checking the install without running the agent.
//
// Everything the desktop app needs — a home directory it can write, a
// migrated agentgo.db, at least one LLM provider, a memory store type
// something can actually build, an mcpServers.json that parses, a skills
// directory whose SKILL.md files load — fails in the same place if it is
// wrong: somewhere deep inside a chat turn, as a message about the symptom.
// agent.Doctor inspects all of it without calling a model and without
// connecting to anything, so it is safe to run on demand from a window.
//
// The MCP probe is deliberately left off. Probing a stdio server means
// spawning it, and a health check that launches processes is not a health
// check.

// DoctorCheck is one finding, restated with JSON tags so the Wails bindings
// carry lowercase keys like every other type crossing this boundary.
type DoctorCheck struct {
	// Name identifies the check, e.g. "home.layout" or "llm.provider.openai".
	Name string `json:"name"`
	// Status is "ok", "warn" or "fail".
	Status string `json:"status"`
	// Detail says what was found. It never contains an API key.
	Detail string `json:"detail"`
	// Fix is what to do about it, empty for a passing check.
	Fix string `json:"fix,omitempty"`
}

// DoctorReport is everything one inspection found.
type DoctorReport struct {
	// Home is the AgentGo home the report is about — this app's data
	// directory, which is what backend.NewService builds its config from.
	Home string `json:"home"`
	// Healthy is true when nothing failed. A warning is not unhealthy: an
	// install with no embedder and no skills is a working install.
	Healthy bool `json:"healthy"`
	OK      int  `json:"ok"`
	Warn    int  `json:"warn"`
	Fail    int  `json:"fail"`
	// Checks are the findings in the order they were made, because the first
	// failure usually explains the ones after it.
	Checks []DoctorCheck `json:"checks"`
	// Error is set only when the inspection itself could not run — a home
	// that will not resolve. A broken install is a report with failures in
	// it, not an error.
	Error string `json:"error,omitempty"`
	// At is when the report was taken, so a stale card can say so.
	At string `json:"at"`
}

// RunDoctor inspects the install this app runs on.
//
// The home is DataDir() rather than AGENTGO_HOME: NewService builds its
// config.Config with Home: DataDir(), so that — not the framework's own
// default — is the install a report about this app has to be about. Desktop
// and serve mode share it; both boot the same App, and DataDir() honours
// SUPERAI_DESKTOP_HOME in both.
func RunDoctor(ctx context.Context) DoctorReport {
	return doctorReportFor(ctx, DataDir())
}

// doctorReportFor is RunDoctor against a named home, which is what makes it
// testable without moving the caller's own install.
func doctorReportFor(ctx context.Context, home string) DoctorReport {
	out := DoctorReport{Home: home, At: nowStamp(), Checks: []DoctorCheck{}}
	report, err := agent.Doctor(ctx, agent.WithDoctorHome(home))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Home = report.Home
	out.Healthy = report.Healthy()
	out.OK, out.Warn, out.Fail = report.Counts()
	for _, c := range report.Checks {
		out.Checks = append(out.Checks, DoctorCheck{
			Name:   c.Name,
			Status: string(c.Status),
			Detail: c.Detail,
			Fix:    c.Fix,
		})
	}
	return out
}
