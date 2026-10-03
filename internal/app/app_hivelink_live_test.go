package app

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLinkToARealQueen pairs with a running queen, asks her something, hears
// her events, and unpairs again — the desktop window's link end to end.
// Skipped unless SUPERAI_LIVE_QUEEN (her address) and SUPERAI_LIVE_CODE (a
// pairing code she is showing) are set.
func TestLinkToARealQueen(t *testing.T) {
	queen, code := os.Getenv("SUPERAI_LIVE_QUEEN"), os.Getenv("SUPERAI_LIVE_CODE")
	if queen == "" || code == "" {
		t.Skip("SUPERAI_LIVE_QUEEN / SUPERAI_LIVE_CODE not set")
	}
	t.Setenv("SUPERAI_HOME", t.TempDir())

	heard := make(chan string, 64)
	prev := windowEmit
	windowEmit = func(_ context.Context, name string, _ map[string]any) {
		select {
		case heard <- name:
		default:
		}
	}
	t.Cleanup(func() { windowEmit = prev })

	a := &App{ctx: context.Background()}
	if _, err := a.LinkHive(queen, code); err != nil {
		t.Fatalf("link: %v", err)
	}
	a.hiveLink.mu.Lock()
	device := a.hiveLink.link.DeviceID
	a.hiveLink.mu.Unlock()
	t.Cleanup(func() {
		// Leave the queen's device list as it was.
		if _, err := a.Remote("UnpairDevice", []any{device}); err != nil {
			t.Errorf("unpair: %v", err)
		}
		_ = a.UnlinkHive()
	})

	st, err := a.Remote("HiveStatus", nil)
	if err != nil {
		t.Fatalf("HiveStatus: %v", err)
	}
	m, _ := st.(map[string]any)
	members, _ := m["members"].([]any)
	t.Logf("role %v, %d members", m["role"], len(members))
	if m["role"] != "queen" || len(members) == 0 {
		t.Fatalf("not a queen with a hive: %v", m["role"])
	}

	deadline := time.After(30 * time.Second)
	for {
		select {
		case name := <-heard:
			if name == "hivelink:state" {
				continue
			}
			t.Logf("first relayed event: %s", name)
			return
		case <-deadline:
			t.Fatal("no event from the queen in 30s")
		}
	}
}
