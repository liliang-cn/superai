package app

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A join trades the core's code for a token of the machine's own and learns
// where the link is from the core; what it keeps is what the service links by.
func TestAJoinTradesTheCodeAndKeepsWhereToLink(t *testing.T) {
	h, c, _ := pairingServer(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv(agentAdvertiseEnv, "192.168.1.5:31744,192.168.1.6:31744")

	tok, link, err := claimAgentToken(srv.URL, startCode(t, h, c), "agent box")
	if err != nil || tok == "" || link != "192.168.1.5:31744,192.168.1.6:31744" {
		t.Fatalf("claim: %q %q %v", tok, link, err)
	}
	if _, _, err := claimAgentToken(srv.URL, "000000", "agent box"); err == nil {
		t.Fatal("a wrong code was taken")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUPERLEO_HOME", "")
	if _, ok := joinedOptions(); ok {
		t.Fatal("options from a join never made")
	}
	dir := agentHome()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "agent.json"), []byte(`{"core":"`+link+`","name":"box"}`), 0o600)
	_ = os.WriteFile(filepath.Join(dir, agentTokenFile), []byte(tok+"\n"), 0o600)
	o, ok := joinedOptions()
	if !ok || o.Core != link || o.Token != tok || o.Name != "box" || os.Getenv("SUPERLEO_HOME") != dir {
		t.Fatalf("joined options: %+v %v", o, ok)
	}
}
