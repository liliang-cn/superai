package app

import (
	"context"
	"log"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// haRefresh is how old the HA services may get before the next listing
// fetches them again (in the background: that listing still answers at once).
const haRefresh = 30 * time.Second

type haCache struct {
	mu       sync.Mutex
	services []backend.HAService
	at       time.Time
	busy     bool
	failed   bool
}

// haSource is the haify controller to ask: the settings', or
// SUPERAI_HAIFY_URL with SUPERAI_HAIFY_TOKEN (or SUPERAI_HAIFY_TOKEN_FILE)
// for a deployment that keeps it out of the settings.
func (a *App) haSource() backend.HaifySource {
	a.mu.Lock()
	var src backend.HaifySource
	if a.settings != nil {
		src = a.settings.HA.Haify
	}
	a.mu.Unlock()
	if u := os.Getenv("SUPERAI_HAIFY_URL"); u != "" {
		src.URL = u
	}
	if t := os.Getenv("SUPERAI_HAIFY_TOKEN"); t != "" {
		src.Token = t
	}
	if f := os.Getenv("SUPERAI_HAIFY_TOKEN_FILE"); f != "" {
		src.TokenFile = f
	}
	return src
}

// haServices is the HA services as last read, starting a refresh when they
// are stale. Empty until the first read finishes, and when none is set up.
func (a *App) haServices() []backend.HAService {
	src := a.haSource()
	c := &a.haCache
	c.mu.Lock()
	defer c.mu.Unlock()
	if !src.Enabled() {
		c.services = nil
		return nil
	}
	if !c.busy && time.Since(c.at) > haRefresh {
		c.busy = true
		go a.refreshHA(src)
	}
	return append([]backend.HAService(nil), c.services...)
}

func (a *App) refreshHA(src backend.HaifySource) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := backend.HaifyServices(ctx, src, nil)
	c := &a.haCache
	c.mu.Lock()
	c.busy, c.at = false, time.Now()
	changed := false
	if err != nil {
		// Keep what was known: a controller that blinked has not moved
		// anything. Say so once, not every half minute.
		if !c.failed {
			log.Printf("ha: %v", err)
		}
		c.failed = true
	} else {
		c.failed = false
		changed = !reflect.DeepEqual(got, c.services)
		c.services = got
	}
	c.mu.Unlock()
	if changed {
		a.emit("hosts:changed", nil)
	}
}
