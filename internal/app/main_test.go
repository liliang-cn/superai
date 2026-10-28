package app

import (
	"os"
	"testing"
)

// The tests here stand fake CLIs up in temporary directories. Asking one
// whether it is signed in (workers.go) runs it in the background, and a run
// still going when its test ends writes into a directory being removed. No
// test here is about that answer but the cache's own, which brings its own.
func TestMain(m *testing.M) {
	cliSignIn.checks = nil
	// No test here downloads agent-browser or drives a browser; the ones
	// about the browser set what they need.
	os.Setenv("SUPERLEO_BROWSER_MODE", "off")
	os.Exit(m.Run())
}
