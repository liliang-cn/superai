package app

// The app's side of a hive on one machine (backend/hive_local.go): when this
// instance is a queen whose spawner is "local", it keeps worker processes
// running, tells them where it is, and lets go of the extra ones once they
// have been idle a while.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// spawnerFor is the spawner the settings describe: the running local one, or
// one built from settings for a cluster.
func (a *App) spawnerFor(s *backend.Settings) (backend.Spawner, error) {
	if s == nil {
		return nil, nil
	}
	if s.Hive.Spawner.IsLocal() {
		a.mu.Lock()
		l := a.localHive
		a.mu.Unlock()
		if l == nil {
			return nil, errors.New("the local hive is not running")
		}
		return l, nil
	}
	return backend.NewSpawner(s.Hive.Spawner)
}

// localQueenURL is where this instance's API answers on loopback, for its
// local workers to join: serve mode's own port, or in the desktop app the
// loopback server that "open in browser" also uses.
func (a *App) localQueenURL() (string, error) {
	a.mu.Lock()
	u := a.serveURL
	a.mu.Unlock()
	if u != "" {
		return u, nil
	}
	c, err := a.ensureCompanion()
	if err != nil {
		return "", err
	}
	return c.origin, nil
}

// syncLocalHive brings the local workers in line with the settings: started
// when this is a queen with a local spawner, stopped otherwise, and started
// afresh when what the workers would be given has changed — a new model, a
// new key, a different number of them.
func (a *App) syncLocalHive() {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil || s.Hive.Role != backend.HiveRoleQueen || !s.Hive.Spawner.IsLocal() {
		a.stopLocalHive()
		return
	}
	queenURL, err := a.localQueenURL()
	if err != nil {
		log.Printf("local hive: no address for the workers to join: %v", err)
		a.stopLocalHive()
		return
	}
	creds, err := loadOrCreateCredentials()
	if err != nil {
		log.Printf("local hive: %v", err)
		a.stopLocalHive()
		return
	}
	// The workers' model is the one the queen's turns actually use: when her
	// embedded proxy is running, that is the proxy on loopback, not the empty
	// address in her settings.
	a.mu.Lock()
	eff := a.effective()
	a.mu.Unlock()
	sp := s.Hive.Spawner
	min := backend.DefaultLocalMinWorkers
	if sp.MinWorkers != nil {
		min = *sp.MinWorkers
	}
	idle := time.Duration(sp.IdleMinutes) * time.Minute
	template := backend.LocalWorkerSettings(*eff, "", queenURL, creds.Token, "")
	key, _ := json.Marshal(map[string]any{"min": min, "max": sp.MaxWorkers, "idle": idle, "worker": template})

	a.mu.Lock()
	same := a.localHive != nil && bytes.Equal(a.localHiveKey, key)
	a.mu.Unlock()
	if same {
		return
	}
	a.stopLocalHive()

	l := backend.NewLocalSpawner(backend.LocalHiveSpec{
		Min: min, Max: sp.MaxWorkers, Idle: idle,
		Settings: func(name, advertise string) ([]byte, error) {
			a.mu.Lock()
			cur := a.effective()
			a.mu.Unlock()
			if cur == nil {
				return nil, errors.New("settings are not loaded")
			}
			w := backend.LocalWorkerSettings(*cur, name, queenURL, creds.Token, advertise)
			return json.MarshalIndent(w, "", "  ")
		},
		IdleSince: a.workerIdleSince,
		Retired: func(names []string) {
			a.mu.Lock()
			h := a.hive
			a.mu.Unlock()
			if h != nil {
				h.Retire(names)
			}
		},
	})
	a.mu.Lock()
	a.localHive, a.localHiveKey = l, key
	a.mu.Unlock()
	log.Printf("local hive: keeping %d worker(s), up to %d, joining %s", l.Min(), l.Max(), queenURL)
}

// stopLocalHive stops every local worker and waits for them to go.
func (a *App) stopLocalHive() {
	a.mu.Lock()
	l := a.localHive
	a.localHive, a.localHiveKey = nil, nil
	a.mu.Unlock()
	if l != nil {
		l.Close()
	}
}

// markWorker counts a piece of delegated work starting (+1) or ending (-1) on
// a worker, for the idle check.
func (a *App) markWorker(name string, d int) {
	a.workerUseMu.Lock()
	defer a.workerUseMu.Unlock()
	if a.workerInFlight == nil {
		a.workerInFlight, a.workerLastUse = map[string]int{}, map[string]time.Time{}
	}
	a.workerInFlight[name] += d
	a.workerLastUse[name] = time.Now()
}

// workerIdleSince is since when a worker has had nothing to do: zero while it
// holds delegated work or a running order, otherwise the later of the last
// time it was used and when it joined.
func (a *App) workerIdleSince(name string) time.Time {
	a.workerUseMu.Lock()
	busy := a.workerInFlight[name] > 0
	last := a.workerLastUse[name]
	a.workerUseMu.Unlock()
	if busy {
		return time.Time{}
	}
	for _, t := range a.tasks().Recent() {
		if t.Worker != name && t.From != name {
			continue
		}
		if t.State == backend.TaskRunning {
			return time.Time{}
		}
		for _, at := range []time.Time{t.StartedAt, t.EndedAt} {
			if at.After(last) {
				last = at
			}
		}
	}
	for _, m := range a.HiveMembers() {
		if m.Name == name && m.JoinedAt.After(last) {
			last = m.JoinedAt
		}
	}
	return last
}

// LocalHive is the settings page's view of the hive on this machine.
type LocalHive struct {
	// Available is false where a local hive does not apply: a worker, or a
	// queen whose workers are pods.
	Available  bool   `json:"available"`
	Enabled    bool   `json:"enabled"`
	MinWorkers int    `json:"min_workers"`
	MaxWorkers int    `json:"max_workers"`
	Running    int    `json:"running"`
	Live       int    `json:"live"`
	Note       string `json:"note,omitempty"`
}

// GetLocalHive says whether this machine runs a local hive and how it is doing.
func (a *App) GetLocalHive() LocalHive {
	a.mu.Lock()
	s, l := a.settings, a.localHive
	a.mu.Unlock()
	out := LocalHive{Available: true, MinWorkers: backend.DefaultLocalMinWorkers, MaxWorkers: backend.DefaultLocalMaxWorkers()}
	if s == nil {
		return out
	}
	sp := s.Hive.Spawner
	switch {
	case s.Hive.Role == backend.HiveRoleWorker:
		out.Available, out.Note = false, "this instance is a worker in a hive"
		return out
	case sp != nil && !sp.IsLocal():
		out.Available, out.Note = false, "this queen's workers run in a cluster"
		return out
	}
	if sp.IsLocal() {
		out.Enabled = s.Hive.Role == backend.HiveRoleQueen
		if sp.MinWorkers != nil {
			out.MinWorkers = *sp.MinWorkers
		}
		if sp.MaxWorkers > 0 {
			out.MaxWorkers = sp.MaxWorkers
		}
	}
	if l != nil {
		out.Running, _ = l.Replicas(context.Background())
		for _, m := range a.HiveMembers() {
			if m.Role == backend.HiveRoleWorker && m.State == "live" && strings.HasPrefix(m.Name, backend.LocalWorkerPrefix+"-") {
				out.Live++
			}
		}
	}
	return out
}

// SetLocalHive turns the hive on this machine on or off and saves it: on, this
// instance becomes a queen whose workers are local processes, keeping
// minWorkers of them running and making up to maxWorkers when there is more
// to do; off, it goes back to working alone. Zero for either takes the
// default. Returns the settings now in force.
func (a *App) SetLocalHive(enabled bool, minWorkers, maxWorkers int) (backend.Settings, error) {
	a.mu.Lock()
	if a.settings == nil {
		a.mu.Unlock()
		return backend.Settings{}, errors.New("settings are not loaded yet")
	}
	s := *a.settings
	a.mu.Unlock()
	if s.Hive.Role == backend.HiveRoleWorker {
		return s, errors.New("this instance is a worker in a hive; it cannot run workers of its own")
	}
	if s.Hive.Spawner != nil && !s.Hive.Spawner.IsLocal() {
		return s, errors.New("this queen's workers run in a cluster; a local hive would be a second one")
	}
	if enabled {
		if maxWorkers <= 0 {
			maxWorkers = backend.DefaultLocalMaxWorkers()
		}
		if minWorkers <= 0 {
			minWorkers = backend.DefaultLocalMinWorkers
		}
		if minWorkers > maxWorkers {
			return s, fmt.Errorf("%d workers to keep is more than the %d allowed", minWorkers, maxWorkers)
		}
		sp := &backend.SpawnerSettings{Kind: backend.SpawnerLocal, MaxWorkers: maxWorkers, MinWorkers: &minWorkers}
		if old := s.Hive.Spawner; old != nil {
			sp.IdleMinutes = old.IdleMinutes
		}
		s.Hive.Role, s.Hive.Spawner = backend.HiveRoleQueen, sp
		// On one machine the queen keeps her own tools and hands work out when
		// it splits; "max" would send every tool call through another process.
		if s.Hive.Delegation == "" {
			s.Hive.Delegation = backend.DelegationAuto
		}
	} else if s.Hive.Spawner.IsLocal() {
		s.Hive.Role, s.Hive.Spawner = "", nil
	}
	if err := a.SaveSettings(s); err != nil {
		return s, err
	}
	return s, nil
}
