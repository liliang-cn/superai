package backend

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/pool"
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
	applySettingsProvider(&out, home)
	st, _ := loadSettingsFrom(filepath.Join(home, "settings.json"))
	var overrides map[string]string
	if st != nil {
		overrides = st.ExternalAgents.Binaries
	}
	out.Checks = append(out.Checks, externalAgentChecks(ctx, overrides)...)
	out.recount()
	return out
}

// recount rebuilds the tallies from the checks. Anything that appends to or
// rewrites Checks has to call it, and having one place to call is why the
// counting is not inlined at each of them.
func (r *DoctorReport) recount() {
	r.OK, r.Warn, r.Fail = 0, 0, 0
	for _, c := range r.Checks {
		switch c.Status {
		case "ok":
			r.OK++
		case "warn":
			r.Warn++
		default:
			r.Fail++
		}
	}
	r.Healthy = r.Fail == 0
}

// applySettingsProvider corrects the one check the framework cannot get
// right for this app. agent.Doctor looks for LLM providers in agentgo.db,
// but SuperAI keeps its brain in settings.json and hands it to the service
// through WithLLM — so on a healthy install the framework reports
// "no LLM provider configured", which is exactly wrong. When settings.json
// names a provider, that check is replaced with what the app will actually
// use, the key reported only as present or absent, and the counts redone.
func applySettingsProvider(out *DoctorReport, home string) {
	st, err := loadSettingsFrom(filepath.Join(home, "settings.json"))
	if err != nil || st == nil || strings.TrimSpace(st.LLMBaseURL) == "" || strings.TrimSpace(st.LLMModel) == "" {
		return
	}
	key := "key present"
	status := "ok"
	fix := ""
	if strings.TrimSpace(st.LLMKey) == "" {
		key = "no key"
		status = "warn"
		fix = "set llm_key in settings.json unless the gateway needs none"
	}
	detail := fmt.Sprintf("settings.json: %s @ %s (%s)", st.LLMModel, st.LLMBaseURL, key)
	replaced := false
	for i := range out.Checks {
		if out.Checks[i].Name == "llm.providers" {
			out.Checks[i] = DoctorCheck{Name: "llm.providers", Status: status, Detail: detail, Fix: fix}
			replaced = true
		}
	}
	if !replaced {
		out.Checks = append(out.Checks, DoctorCheck{Name: "llm.providers", Status: status, Detail: detail, Fix: fix})
	}
	// agent-go's own per-provider checks never run here — the provider lives
	// in settings.json, not agentgo.db — so ask the one question of theirs
	// that matters for a long task: can this model be priced at all? Loading
	// the settings above already registered any rates they carry.
	pricing := DoctorCheck{Name: "llm.pricing", Status: "ok", Detail: st.LLMModel + " is priced"}
	if _, known := pool.LookupModelPricing(st.LLMModel); !known {
		pricing = DoctorCheck{
			Name: "llm.pricing", Status: "warn",
			Detail: "no rates for " + st.LLMModel + "; spend reads 0 and the cost ceiling never fires",
			Fix:    "set llm_price_input_per_1k, llm_price_cached_per_1k and llm_price_output_per_1k in settings.json",
		}
	}
	out.Checks = append(out.Checks, pricing)
}

// Agent CLIs installed on this machine.
//
// SuperAI can hand a task to Claude Code, Codex, Gemini or cursor-agent, and
// the question someone actually has is "will it work", which is not the same
// question as "is it installed". Measured here on 2026-09-02: all four were
// installed and only `claude` ran. codex had a revoked refresh token, gemini
// an account tier that is not eligible, cursor-agent no API key. Worse,
// `claude` with a dead token prints "Failed to authenticate" as its answer and
// exits 0 — so neither the binary's presence nor its exit code is a verdict.
//
// The only thing that would be a verdict is running the CLI on a real prompt,
// and that is a several-second, token-spending network round trip per agent.
// This report is what the health card runs on every page load. So the check
// stops one step short on purpose: present, where, what version it admits to,
// and an explicit note that nothing here was signed in to. An honest "not
// checked" beats a green tick that means "the file exists".
//
// Spawning at all is a departure from the rule above about the MCP probe, and
// the difference is bounded-ness: an MCP stdio server is a process you have to
// keep alive to learn anything, while `--version` prints and exits. It still
// gets a deadline and all four still run concurrently, so the whole group
// costs one externalAgentProbeTimeout at worst rather than four.
const externalAgentProbeTimeout = 2 * time.Second

// externalAgentCLIs are the agent CLIs SuperAI knows how to delegate to, in
// the order the settings page lists them.
var externalAgentCLIs = []string{"claude", "codex", "gemini", "cursor-agent"}

// ExternalAgentStatus is one agent CLI as seen from outside: on this machine
// or not, and if so where and which version. It says nothing about whether
// that CLI can currently reach a model — see the comment above.
type ExternalAgentStatus struct {
	// Name is the CLI's command name, e.g. "cursor-agent".
	Name string `json:"name"`
	// Installed is true when the binary was found and is executable.
	Installed bool `json:"installed"`
	// Path is where it was found, empty when it was not.
	Path string `json:"path,omitempty"`
	// Version is what `--version` printed, empty when it would not say. A CLI
	// that refuses to report a version is still perfectly usable, so this
	// being empty is not a fault.
	Version string `json:"version,omitempty"`
	// Overridden is true when Path came from settings rather than PATH.
	Overridden bool `json:"overridden"`
}

// ExternalAgentStatuses probes every agent CLI SuperAI can delegate to.
// overrides is Settings.ExternalAgents.Binaries — a path from there is used
// instead of a PATH lookup, which is the escape hatch for a GUI launch whose
// inherited PATH is nothing like the login shell's.
func ExternalAgentStatuses(ctx context.Context, overrides map[string]string) []ExternalAgentStatus {
	out := make([]ExternalAgentStatus, len(externalAgentCLIs))
	var wg sync.WaitGroup
	for i, name := range externalAgentCLIs {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			out[i] = probeExternalAgent(ctx, name, strings.TrimSpace(overrides[name]))
		}(i, name)
	}
	wg.Wait()
	return out
}

func probeExternalAgent(ctx context.Context, name, override string) ExternalAgentStatus {
	st := ExternalAgentStatus{Name: name}
	lookup := name
	if override != "" {
		lookup, st.Overridden = expandHome(override), true
	}
	path, err := exec.LookPath(lookup)
	if err != nil {
		return st
	}
	st.Installed, st.Path = true, path
	st.Version = externalAgentVersion(ctx, path)
	return st
}

// externalAgentVersion asks a CLI what version it is, and takes silence for an
// answer. Anything it prints on stderr is discarded rather than reported: a
// CLI that greets an unauthenticated user with a paragraph would otherwise put
// that paragraph in the version column.
func externalAgentVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, externalAgentProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

// externalAgentChecks turns the probe into doctor findings: one summary line,
// plus a line per installed CLI. Not one line per *known* CLI — four "not
// installed" rows on a machine that has none of them is noise about a feature
// nobody asked for. Nothing here can fail: a machine with no agent CLIs is a
// normal machine, and this app works perfectly well on it.
func externalAgentChecks(ctx context.Context, overrides map[string]string) []DoctorCheck {
	found := []string{}
	checks := []DoctorCheck{}
	for _, st := range ExternalAgentStatuses(ctx, overrides) {
		if !st.Installed {
			continue
		}
		found = append(found, st.Name)
		detail := st.Path
		if st.Version != "" {
			detail = st.Version + " at " + st.Path
		}
		if st.Overridden {
			detail += " (path from settings)"
		}
		checks = append(checks, DoctorCheck{
			Name:   "external.agents." + st.Name,
			Status: "ok",
			Detail: detail,
		})
	}
	sort.Strings(found)
	summary := DoctorCheck{
		Name:   "external.agents",
		Status: "ok",
		Detail: strings.Join(found, ", ") + " installed; sign-in not checked here",
	}
	if len(found) == 0 {
		summary = DoctorCheck{
			Name:   "external.agents",
			Status: "warn",
			Detail: "none of claude, codex, gemini, cursor-agent is on PATH",
			Fix:    "install one, or set its full path under Settings → External agents — a windowed launch inherits a much shorter PATH than your shell",
		}
	}
	return append([]DoctorCheck{summary}, checks...)
}
