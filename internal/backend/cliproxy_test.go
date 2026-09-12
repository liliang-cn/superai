package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIProxyLifecycle boots the embedded CLIProxyAPI against a throwaway data
// dir and checks it serves the OpenAI-compatible surface the agent talks to.
// With no credentials in the auth dir the model list is empty — that is the
// expected "enabled but not logged in yet" state.
func TestCLIProxyLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	p, err := StartCLIProxy(43519)
	if err != nil {
		t.Fatalf("StartCLIProxy: %v", err)
	}
	defer p.Close()

	if got, want := p.BaseURL(), "http://127.0.0.1:43519/v1"; got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}
	if !strings.HasPrefix(p.Key(), "superai-") {
		t.Errorf("Key = %q, want a generated superai- key", p.Key())
	}

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 0 {
		t.Logf("models served: %v", models)
	}

	cfg, err := os.ReadFile(filepath.Join(home, "cliproxy", "config.yaml"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(cfg), `host: "127.0.0.1"`) {
		t.Errorf("generated config must pin loopback, got:\n%s", cfg)
	}
	if _, err := os.Stat(p.AuthDir()); err != nil {
		t.Errorf("auth dir missing: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// A second Start on the same port must succeed once the first is down.
	p2, err := StartCLIProxy(43519)
	if err != nil {
		t.Fatalf("restart after Close: %v", err)
	}
	_ = p2.Close()
}
