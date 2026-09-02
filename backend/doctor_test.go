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
