package app

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agentexec"
)

// installedCLIs is agentexec.Discover, remembered.
//
// Discover runs every CLI's --version one after another — 1.9s for the eleven
// on the Mac — and that was paid before the reply to every message addressed
// to an agent and every turn the queen engine took. What is installed changes
// about never between two messages, so an answer older than a minute is still
// handed out at once and refreshed behind it. Changed overrides (a settings
// save) are the one case that waits for a fresh look.
func installedCLIs(overrides map[string]string) []agentexec.Installed {
	return clis.get(overrides)
}

var clis = &cliDiscovery{discover: agentexec.Discover}

const cliDiscoveryFresh = time.Minute

type cliDiscovery struct {
	discover func(map[string]string) []agentexec.Installed

	mu         sync.Mutex
	key        string
	at         time.Time
	list       []agentexec.Installed
	known      bool
	refreshing bool
}

func (d *cliDiscovery) get(overrides map[string]string) []agentexec.Installed {
	key := overridesKey(overrides)
	d.mu.Lock()
	if d.known && d.key == key {
		list := d.list
		if time.Since(d.at) > cliDiscoveryFresh && !d.refreshing {
			d.refreshing = true
			go d.refresh(key, maps.Clone(overrides))
		}
		d.mu.Unlock()
		return list
	}
	d.mu.Unlock()
	list := d.discover(overrides)
	d.store(key, list)
	return list
}

func (d *cliDiscovery) refresh(key string, overrides map[string]string) {
	list := d.discover(overrides)
	d.mu.Lock()
	d.refreshing = false
	d.mu.Unlock()
	d.store(key, list)
}

func (d *cliDiscovery) store(key string, list []agentexec.Installed) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.key, d.list, d.at, d.known = key, list, time.Now(), true
}

func overridesKey(overrides map[string]string) string {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(overrides)) {
		b.WriteString(name + "=" + overrides[name] + "\n")
	}
	return b.String()
}
