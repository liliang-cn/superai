package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// The engine, the fallback and the public URL set over /api/rpc survive the
// hive's entrypoint writing settings.json from the Secret on the next start,
// and going back to SuperLeo's own agent leaves nothing that shadows the Secret.
func TestTheQueenEngineSurvivesTheEntrypointRewritingSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	t.Setenv("SUPERLEO_PUBLIC_URL", "")
	bin, _ := fakeClaude(t, "", 0)
	a := NewApp()
	s, err := backend.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.CLIProxyEnabled = false
	s.ExternalAgents = backend.ExternalAgents{Enabled: true, Binaries: map[string]string{"claude": bin}}
	s.Hive.PublicURL = "https://ai.example.test/"
	a.settings = s
	defer a.Shutdown(context.Background())
	if err := a.SaveSettings(*s); err != nil {
		t.Fatal(err)
	}
	if _, err := callMethod(a, "SetQueenEngine", []json.RawMessage{json.RawMessage(`"claude"`), json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}

	// What deploy/docker-entrypoint.sh does on every start.
	secret := `{"llm_model":"m","hive":{"role":"queen","name":"queen"}}`
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := backend.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if again.Hive.QueenEngine != "claude" || again.Hive.QueenFallsBack() || again.Hive.Public() != "https://ai.example.test" {
		t.Fatalf("after the rewrite: engine %q fallback %v public %q", again.Hive.QueenEngine, again.Hive.QueenFallsBack(), again.Hive.Public())
	}
	if again.Hive.Role != backend.HiveRoleQueen || again.LLMModel != "m" {
		t.Fatalf("the Secret's own settings lost: %+v", again.Hive)
	}
	b := &App{settings: again}
	if got := b.QueenEngines(); got["engine"] != "claude" || got["fallback"] != false || got["public_url"] != "https://ai.example.test/" {
		t.Fatalf("QueenEngines after restart: %v", got)
	}

	// A public URL the Secret gives is not hidden by a file without one.
	cleared := *again
	cleared.Hive.PublicURL = ""
	if err := cleared.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"hive":{"public_url":"https://op.example.test"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := backend.LoadSettings(); got.Hive.PublicURL != "https://op.example.test" || got.Hive.QueenEngine != "claude" {
		t.Fatalf("operator's public_url: %+v", got.Hive)
	}

	// Back to SuperLeo with the fallback on: the file goes, and the Secret's
	// word is the only one again.
	// The Secret leaves the proxy at its default (on); off here, or saving
	// starts the real CLIProxyAPI and its own data race fails -race.
	cleared.CLIProxyEnabled = false
	a.mu.Lock()
	a.settings = &cleared
	a.mu.Unlock()
	if _, err := a.SetQueenEngine("", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backend.DataDir(), "queenengine.json")); !os.IsNotExist(err) {
		t.Fatalf("queenengine.json kept: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := backend.LoadSettings(); got.Hive.QueenEngine != "" || !got.Hive.QueenFallsBack() {
		t.Fatalf("after going back: %+v", got.Hive)
	}
}
