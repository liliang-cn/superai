package app

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/liliang-cn/agentexec"
)

// Only the first ask waits for the CLIs to be probed; a stale answer is handed
// out at once and refreshed behind it; new overrides are looked at afresh.
func TestInstalledCLIsAreProbedOnceAndRefreshedBehindTheAnswer(t *testing.T) {
	var probes atomic.Int32
	release := make(chan struct{}, 8)
	d := &cliDiscovery{discover: func(o map[string]string) []agentexec.Installed {
		if probes.Add(1) > 1 {
			<-release
		}
		return []agentexec.Installed{{Name: "claude", Binary: o["claude"]}}
	}}
	over := map[string]string{"claude": "/bin/claude"}

	d.get(over)
	d.get(over)
	if n := probes.Load(); n != 1 {
		t.Fatalf("probed %d times for two asks a moment apart", n)
	}

	d.mu.Lock()
	d.at = time.Now().Add(-2 * cliDiscoveryFresh)
	d.mu.Unlock()
	start := time.Now()
	if got := d.get(over); len(got) != 1 {
		t.Fatalf("stale answer: %v", got)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("a stale answer waited for the refresh")
	}
	release <- struct{}{}
	waitFor(t, "the refresh", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return !d.refreshing && time.Since(d.at) < cliDiscoveryFresh
	})

	release <- struct{}{}
	if got := d.get(map[string]string{"claude": "/opt/claude"}); got[0].Binary != "/opt/claude" {
		t.Fatalf("new overrides answered from the old look: %v", got)
	}
}
