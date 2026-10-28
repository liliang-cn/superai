package backend

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// A service built in a test must not download agent-browser or drive a
	// browser; the browser's own tests turn it back on where they need it.
	os.Setenv("SUPERLEO_BROWSER_MODE", "off")
	os.Exit(m.Run())
}
