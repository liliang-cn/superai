package app

// The worker's half of "one hive, one set of abilities" (backend/hive_abilities.go):
// take the queen's skills and MCP servers on start and every minute after.

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// handleHiveAbilities is GET /api/hive/abilities: the queen's skills and MCP
// servers, for her workers.
func (a *App) handleHiveAbilities(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.hive
	a.mu.Unlock()
	if h == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]any{"error": "this instance is not a queen"})
		return
	}
	writeJSONStatus(w, http.StatusOK, backend.CollectAbilities())
}

// AbilitiesState is what the last sync did, for the Hive page.
type AbilitiesState struct {
	Hash    string            `json:"hash,omitempty"`
	Skills  []string          `json:"skills"`
	MCP     []string          `json:"mcp"`
	Skipped map[string]string `json:"skipped,omitempty"`
	At      time.Time         `json:"at"`
	Error   string            `json:"error,omitempty"`
}

func (a *App) syncAbilities(ctx context.Context, ann *backend.Announcer) {
	applied := ""
	tick := time.NewTimer(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		tick.Reset(time.Minute)
		ab, err := ann.Abilities(ctx)
		if err != nil {
			a.setAbilities(AbilitiesState{Hash: applied, At: time.Now(), Error: err.Error()})
			continue
		}
		if ab.Hash == applied {
			continue
		}
		// A new MCP list needs the service rebuilt, which would cut a turn
		// short; wait for a quiet moment rather than interrupt work.
		if a.turnsRunning() > 0 {
			continue
		}
		res, err := backend.ApplyAbilities(ab, nil)
		st := AbilitiesState{Hash: ab.Hash, Skills: res.Skills, MCP: res.MCP, Skipped: res.Skipped, At: time.Now()}
		if err != nil {
			st.Error = err.Error()
			a.setAbilities(st)
			continue
		}
		switch {
		case res.MCPChanged:
			a.rebuild()
		case res.SkillsChanged:
			a.mu.Lock()
			svc := a.svc
			a.mu.Unlock()
			if svc != nil {
				svc.ReloadSkills(ctx)
			}
		}
		applied = ab.Hash
		a.setAbilities(st)
		log.Printf("hive: abilities from the queen: skills %v, mcp %v, skipped %v", res.Skills, res.MCP, res.Skipped)
	}
}

func (a *App) setAbilities(st AbilitiesState) {
	a.mu.Lock()
	a.abilities = st
	a.mu.Unlock()
}

// turnsRunning is how many conversation turns are in flight.
func (a *App) turnsRunning() int {
	n := 0
	a.openTurns.Range(func(_, _ any) bool { n++; return true })
	return n
}
