package backend

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// A service built in a test must not download agent-browser or drive a
	// browser; the browser's own tests turn it back on where they need it.
	os.Setenv("SUPERLEO_BROWSER_MODE", "off")
	os.Setenv("SUPERLEO_DESKTOP", "off")
	// Never touch the person's real ~/.superleo (or anything else under their
	// home): every test runs against a throwaway home.
	home, err := os.MkdirTemp("", "superleo-test-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("SUPERLEO_HOME", home)
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
