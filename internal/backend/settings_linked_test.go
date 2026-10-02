package backend

import (
	"os"
	"testing"
)

func TestALinkMadeInThePageSurvivesSettingsBeingRewritten(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	if err := SaveLinkedSuperAIs(map[string]RemoteAgent{
		"mac":     {URL: "http://192.168.123.123:43779", Token: "k"},
		"cluster": {URL: "http://ignored"},
	}); err != nil {
		t.Fatal(err)
	}
	// What the hive's entrypoint does on every start: a fresh settings.json.
	if err := os.WriteFile(settingsPath(), []byte(`{"remote_agents":{"agents":{"cluster":{"url":"http://operator"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.RemoteAgents.Enabled || s.RemoteAgents.Agents["mac"].Token != "k" || s.RemoteAgents.Agents["cluster"].URL != "http://operator" {
		t.Fatalf("%+v", s.RemoteAgents)
	}
}
