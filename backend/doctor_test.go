package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SuperAI's brain lives in settings.json, not in agentgo.db, so a home whose
// settings name a provider is healthy even though the framework's own
// provider table is empty.
func TestDoctorTrustsTheSettingsProvider(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"llm_base_url":"https://gw.example/v1","llm_key":"sk-test","llm_model":"test-model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := doctorReportFor(context.Background(), home)
	var got *DoctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "llm.providers" {
			got = &report.Checks[i]
		}
	}
	if got == nil || got.Status != "ok" || !strings.Contains(got.Detail, "test-model") || strings.Contains(got.Detail, "sk-test") {
		t.Fatalf("llm.providers = %+v, want ok naming the model and never the key", got)
	}
	if report.Fail != 0 || !report.Healthy {
		t.Fatalf("a home with a settings provider must be healthy: fail=%d healthy=%v", report.Fail, report.Healthy)
	}
}

// A gateway alias no pricing table knows costs $0 on every task, and the
// spend ceiling built on that number never fires. The doctor says so, and
// stops saying so once settings.json carries rates.
func TestDoctorWarnsWhenTheBrainIsUnpriced(t *testing.T) {
	find := func(r DoctorReport) *DoctorCheck {
		for i := range r.Checks {
			if r.Checks[i].Name == "llm.pricing" {
				return &r.Checks[i]
			}
		}
		return nil
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"llm_base_url":"https://gw.example/v1","llm_key":"sk-test","llm_model":"gw-alias-unpriced"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := find(doctorReportFor(context.Background(), home))
	if got == nil || got.Status != "warn" || !strings.Contains(got.Detail, "gw-alias-unpriced") || !strings.Contains(got.Fix, "llm_price_input_per_1k") {
		t.Fatalf("llm.pricing = %+v, want a warning naming the model and the settings keys", got)
	}

	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"llm_base_url":"https://gw.example/v1","llm_key":"sk-test","llm_model":"gw-alias-priced","llm_price_input_per_1k":0.001,"llm_price_output_per_1k":0.002}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := find(doctorReportFor(context.Background(), home)); got == nil || got.Status != "ok" {
		t.Fatalf("llm.pricing with rates in settings = %+v, want ok", got)
	}
}

// stubCLI writes an executable that answers --version, so the probe can be
// tested without a real agent CLI on the machine — and without spending a
// token asking one anything.
func stubCLI(t *testing.T, dir, name, version string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\necho '" + version + "'\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The check reports what is on the machine and stops short of claiming it
// works: a `claude` with a revoked token prints "Failed to authenticate" and
// exits 0, so the doctor says which CLIs are here and says out loud that it
// did not check whether any of them is signed in.
func TestDoctorFindsTheAgentCLIsOnPath(t *testing.T) {
	bin := t.TempDir()
	stubCLI(t, bin, "claude", "9.9.9 (Claude Code)")
	t.Setenv("PATH", bin)

	checks := externalAgentChecks(context.Background(), nil)
	byName := map[string]DoctorCheck{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	summary, ok := byName["external.agents"]
	if !ok || summary.Status != "ok" || !strings.Contains(summary.Detail, "sign-in not checked") {
		t.Fatalf("external.agents = %+v, want an ok summary that admits what it did not check", summary)
	}
	found, ok := byName["external.agents.claude"]
	if !ok || found.Status != "ok" || !strings.Contains(found.Detail, "9.9.9") {
		t.Fatalf("external.agents.claude = %+v, want the version it printed", found)
	}
	// Only what is installed gets a row. Four "not installed" lines on a
	// machine that has none of them is noise about a feature nobody asked for.
	for _, name := range []string{"codex", "gemini", "cursor-agent"} {
		if _, ok := byName["external.agents."+name]; ok {
			t.Errorf("a CLI that is not installed got its own check: %s", name)
		}
	}
}

// A machine with no agent CLIs is a normal machine: warn, never fail, and say
// where to put a path — a windowed launch inherits a PATH nothing like the
// login shell's, which is the commonest reason a CLI that works in a terminal
// is invisible here.
func TestDoctorWarnsRatherThanFailsWhenNoAgentCLIIsInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	checks := externalAgentChecks(context.Background(), nil)
	if len(checks) != 1 {
		t.Fatalf("checks = %+v, want only the summary", checks)
	}
	if checks[0].Status != "warn" || !strings.Contains(checks[0].Fix, "External agents") {
		t.Fatalf("summary = %+v, want a warning pointing at the settings section", checks[0])
	}

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "settings.json"),
		[]byte(`{"llm_base_url":"https://gw.example/v1","llm_key":"sk-test","llm_model":"test-model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := doctorReportFor(context.Background(), home)
	if report.Fail != 0 || !report.Healthy {
		t.Fatalf("no agent CLIs made the install unhealthy: fail=%d healthy=%v", report.Fail, report.Healthy)
	}
	if report.Warn == 0 {
		t.Error("the missing CLIs were not counted as a warning")
	}
}

// The binary override is the escape hatch for a windowed launch whose PATH is
// missing the CLI, so it has to win over PATH — and a path that points at
// nothing has to read as not installed rather than as a working agent.
func TestDoctorHonoursTheBinaryOverride(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	elsewhere := t.TempDir()
	path := stubCLI(t, elsewhere, "codex-x", "codex-cli 1.2.3")

	got := ExternalAgentStatuses(context.Background(), map[string]string{
		"codex":  path,
		"gemini": filepath.Join(elsewhere, "does-not-exist"),
	})
	byName := map[string]ExternalAgentStatus{}
	for _, st := range got {
		byName[st.Name] = st
	}
	if st := byName["codex"]; !st.Installed || !st.Overridden || st.Version != "codex-cli 1.2.3" {
		t.Fatalf("codex = %+v, want the overridden path probed", st)
	}
	if st := byName["gemini"]; st.Installed {
		t.Fatalf("gemini = %+v, want an override pointing at nothing to read as not installed", st)
	}
	if len(got) != 4 {
		t.Fatalf("got %d statuses, want one per known CLI so the settings page can list them all", len(got))
	}
}
