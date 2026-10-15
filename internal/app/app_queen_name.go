package app

import (
	"strings"

	"github.com/liliang-cn/superai/internal/backend"
)

// The queen's name and aliases (backend/queen_name.go), as clients read and
// set them. HiveStatus and GetStatus carry the name too, so a client that
// polls those has it without asking; these two are for the settings screens,
// the phone's among them, which never sees the whole settings object.

// QueenIdentity is the queen's display name and aliases.
func (a *App) QueenIdentity() map[string]any {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	return map[string]any{"name": s.QueenDisplayName(), "aliases": s.QueenAliasList(), "default": backend.DefaultQueenName}
}

// SetQueenIdentity names the queen. An empty name is the default; the aliases
// replace the old ones. Saved like any other settings change, so the persona
// is rebuilt with the new name.
func (a *App) SetQueenIdentity(name string, aliases []string) (map[string]any, error) {
	if err := backend.ValidateQueenName(name, aliases); err != nil {
		return nil, err
	}
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	} else if s, err := backend.LoadSettings(); err == nil {
		next = *s
	}
	a.mu.Unlock()
	next.QueenName, next.QueenAliases = strings.TrimSpace(name), aliases
	if err := a.SaveSettings(next); err != nil {
		return nil, err
	}
	return a.QueenIdentity(), nil
}

// addressedToQueen reports whether a message opens with "@" and one of the
// queen's names. Not on a worker: there "queen" is someone else.
func (a *App) addressedToQueen(msg string) bool {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil {
		s = &backend.Settings{}
	}
	if s.Hive.Role == backend.HiveRoleWorker {
		return false
	}
	_, _, ok := addressedTo(msg, s.IsQueenName)
	return ok
}
