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
