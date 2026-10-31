package app

// The queen's engines as an ordered list (Hive.QueenEngines): a person's
// turn goes to the first one switched on, and when that one cannot answer —
// not installed, its machine away, signed out, failed before it said or did
// anything — to the next, until one answers or none is left. SuperLeo's own
// agent ("superleo") is one entry like the others. routeToEngine
// (app_queen_engine.go) walks the list; this file keeps it in settings and
// says which engines there are to put in it.

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/liliang-cn/superleo/internal/backend"
)

// queenStartRun starts one engine's run for a turn; a test puts its own in.
var queenStartRun = func(a *App, o cliStart) (CLIRun, error) { return a.startCLIRun(o) }

// QueenEngineInfo is one engine the queen can think with, as the settings
// show it.
type QueenEngineInfo struct {
	// ID is what goes in Hive.QueenEngines: "superleo", "claude",
	// "claude.mac".
	ID string `json:"id"`
	// Label is the CLI's name ("Claude Code"), or "SuperLeo".
	Label string `json:"label"`
	// Where is the machine it runs on.
	Where string `json:"where"`
	// Local is whether that is this machine.
	Local   bool   `json:"local"`
	Version string `json:"version,omitempty"`
	// State is "online" (SuperLeo with a model, a linked machine's CLI
	// reachable now), "installed" (a CLI here, ready) or "offline".
	State string `json:"state"`
	// Reason says why it is offline.
	Reason string `json:"reason,omitempty"`
	// Enabled is whether turns may go to it.
	Enabled bool `json:"enabled"`
}

// QueenEngineListing is the queen's engines in the order they are tried,
// then every other engine this system has, switched off.
type QueenEngineListing struct {
	Engines []QueenEngineInfo `json:"engines"`
	// Now is who took the queen's latest turn when it was not the first
	// engine (queenEngineNow).
	Now string `json:"now"`
	// PublicURL is where an engine on another machine reaches this one.
	PublicURL string `json:"public_url"`
}

// QueenEngineList is the queen's engines in order, with every engine this
// system actually has: SuperLeo, the CLIs that can think for her on this
// machine, and those on machines linked now. One saved in the list that is
// not there now is listed offline.
//
//	POST /api/rpc/QueenEngineList  []
//	→ {"engines":[{"id":"claude","label":"Claude Code","where":"Pro.local","local":true,
//	   "version":"2.1.0","state":"installed","enabled":true},…],"now":"","public_url":""}
func (a *App) QueenEngineList() QueenEngineListing {
	a.mu.Lock()
	var s backend.Settings
	if a.settings != nil {
		s = *a.settings
	}
	a.mu.Unlock()
	have := a.queenEnginesThere(&s)
	var out []QueenEngineInfo
	listed := map[string]bool{}
	for _, e := range s.Hive.QueenChain() {
		info, ok := have[e.Engine]
		switch {
		case ok:
		case isDesktopEngineID(e.Engine):
			info = a.desktopEngineAway(e.Engine)
		default:
			info = queenEngineAway(e.Engine)
		}
		info.Enabled = e.Enabled
		out = append(out, info)
		listed[e.Engine] = true
	}
	for _, id := range queenEngineOrder(have) {
		if !listed[id] {
			out = append(out, have[id])
		}
	}
	return QueenEngineListing{Engines: out, Now: a.queenEngineNow(), PublicURL: s.Hive.PublicURL}
}

// SetQueenEngineList saves the queen's engines in the order they are to be
// tried, each switched on or off, and where machines other than this one
// reach it (for an engine on one of them). Engines not given are left out
// of the list; SuperLeo is always in it.
//
//	POST /api/rpc/SetQueenEngineList  [[{"engine":"claude","enabled":true},{"engine":"superleo","enabled":true}], ""]
//	→ QueenEngineList's answer
func (a *App) SetQueenEngineList(engines []backend.QueenEngineEntry, publicURL string) (QueenEngineListing, error) {
	a.mu.Lock()
	var next backend.Settings
	if a.settings != nil {
		next = *a.settings
	} else if s, err := backend.LoadSettings(); err == nil {
		next = *s
	}
	a.mu.Unlock()
	next.Hive.SetQueenChain(engines)
	next.Hive.PublicURL = strings.TrimSpace(publicURL)
	if err := a.SaveSettings(next); err != nil {
		return QueenEngineListing{}, err
	}
	return a.QueenEngineList(), nil
}

// queenEnginesThere is every engine this system has now, by id.
func (a *App) queenEnginesThere(s *backend.Settings) map[string]QueenEngineInfo {
	here, _ := os.Hostname()
	out := map[string]QueenEngineInfo{}
	sl := QueenEngineInfo{ID: backend.QueenEngineSuperLeo, Label: "SuperLeo", Where: here, Local: true, State: "online"}
	if !a.hasModel() {
		sl.State, sl.Reason = "offline", "no model set up"
	}
	out[sl.ID] = sl
	ready := map[string]bool{}
	for _, c := range a.localAgentNames() {
		ready[c.Name] = true
	}
	off := map[string]bool{}
	for _, n := range s.ExternalAgents.Disabled {
		off[strings.ToLower(strings.TrimSpace(n))] = true
	}
	for _, c := range installedCLIs(s.ExternalAgents.Binaries) {
		if !canThinkForQueen(c.Name) {
			continue
		}
		info := QueenEngineInfo{ID: c.Name, Label: agentLabel(c.Name), Where: here, Local: true, Version: versionNumber(c.Version), State: "installed"}
		switch {
		case ready[c.Name]:
		case !s.ExternalAgents.Enabled:
			info.State, info.Reason = "offline", "external agents are off"
		case off[strings.ToLower(c.Name)]:
			info.State, info.Reason = "offline", "switched off"
		default:
			info.State, info.Reason = "offline", "signed out"
		}
		out[c.Name] = info
	}
	for host, names := range a.remoteCLIs() {
		for _, n := range names {
			if canThinkForQueen(n) {
				id := n + "." + host
				out[id] = QueenEngineInfo{ID: id, Label: agentLabel(n), Where: host, State: "online"}
			}
		}
	}
	// The desktop SuperLeos linked now that think for the queen with their
	// own agent (queen_engine_desktop.go).
	for _, d := range a.desktopEngines() {
		out[d.ID] = d
	}
	return out
}

var versionRe = regexp.MustCompile(`\d+(\.\d+)+`)

// versionNumber is the number in what a CLI's --version printed:
// "2.1.312 (Claude Code)" and "codex-cli 0.165.0" are 2.1.312 and 0.165.0.
func versionNumber(v string) string {
	if n := versionRe.FindString(v); n != "" {
		return n
	}
	return strings.TrimSpace(v)
}

// queenEngineAway is an engine in the list that is not there now.
func queenEngineAway(id string) QueenEngineInfo {
	if id == backend.QueenEngineSuperLeo {
		return QueenEngineInfo{ID: id, Label: "SuperLeo", Local: true, State: "offline"}
	}
	if cli, host, ok := splitRemoteCLI(id); ok {
		return QueenEngineInfo{ID: id, Label: agentLabel(cli), Where: host, State: "offline", Reason: "not reachable now"}
	}
	here, _ := os.Hostname()
	return QueenEngineInfo{ID: id, Label: agentLabel(id), Where: here, Local: true, State: "offline", Reason: "not installed"}
}

// queenEngineOrder is the engines there are, as they are offered: SuperLeo,
// this machine's CLIs, then each linked machine's, by name.
func queenEngineOrder(have map[string]QueenEngineInfo) []string {
	rank := func(id string) (int, string, int) {
		if id == backend.QueenEngineSuperLeo {
			return 0, "", 0
		}
		cli, host, remote := splitRemoteCLI(id)
		if !remote {
			cli = id
		}
		i := slices.Index(queenEngineNames, cli)
		if !remote {
			return 1, "", i
		}
		return 2, host, i
	}
	ids := make([]string, 0, len(have))
	for id := range have {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(x, y string) int {
		gx, hx, ix := rank(x)
		gy, hy, iy := rank(y)
		if gx != gy {
			return gx - gy
		}
		if c := strings.Compare(hx, hy); c != 0 {
			return c
		}
		return ix - iy
	})
	return ids
}

// queenEnginesAfter is the engines switched on after one, in order: where a
// turn goes when that one cannot take it.
func queenEnginesAfter(s *backend.Settings, engine string) []string {
	on := s.Hive.QueenEnginesOn()
	if i := slices.Index(on, engine); i >= 0 {
		return on[i+1:]
	}
	return on
}

// reconcileQueenChain makes a settings save's engine list and its single
// engine and fallback agree. A client that changed only the single engine
// or the fallback (an older phone app, the first-start guide) has it put
// first in the list; otherwise the list is the word and they follow it.
func reconcileQueenChain(next *backend.Settings, prev *backend.HiveSettings) {
	h := &next.Hive
	if len(h.QueenEngines) == 0 {
		h.SetQueenChain(h.QueenChain())
		return
	}
	legacyChanged := strings.TrimSpace(h.QueenEngine) != strings.TrimSpace(prev.QueenEngine) ||
		h.QueenFallsBack() != prev.QueenFallsBack()
	if legacyChanged && slices.Equal(h.QueenChain(), prev.QueenChain()) {
		h.PromoteQueenEngine(h.QueenEngine, h.QueenFallsBack())
		return
	}
	h.SetQueenChain(h.QueenEngines)
}

// validQueenEngine checks the queen's engines before they are saved, and
// puts the list and the single engine in step (reconcileQueenChain). An
// engine switched on must be a CLI that can think for the queen (queenCLIs),
// here with External agents on, or on a machine linked now; one already in
// the saved list (or prev, the engine saved) passes as it is, since its
// machine may only be asleep. One at least has to be switched on.
func (a *App) validQueenEngine(next *backend.Settings, prev string) error {
	var before backend.HiveSettings
	a.mu.Lock()
	if a.settings != nil {
		before = a.settings.Hive
	}
	a.mu.Unlock()
	reconcileQueenChain(next, &before)
	saved := map[string]bool{strings.TrimSpace(prev): true, backend.QueenEngineSuperLeo: true}
	for _, e := range before.QueenChain() {
		saved[e.Engine] = true
	}
	on := 0
	for _, e := range next.Hive.QueenChain() {
		if e.Enabled {
			on++
		}
		if saved[e.Engine] {
			continue
		}
		if !e.Enabled {
			if !canBeQueenEngine(e.Engine) {
				return fmt.Errorf("%s cannot think for the queen: Claude Code, Codex, pi and Hermes can", e.Engine)
			}
			continue
		}
		if err := a.validQueenEngineOne(next, e.Engine); err != nil {
			return err
		}
	}
	if on == 0 && next.Hive.Role != backend.HiveRoleWorker {
		return errors.New("at least one of the queen's engines has to be on")
	}
	return nil
}

// validQueenEngineOne checks one engine that is new to the list.
func (a *App) validQueenEngineOne(next *backend.Settings, engine string) error {
	if isDesktopEngineID(engine) {
		if _, ok := a.desktopEngine(engine); !ok {
			_, host, _ := splitRemoteCLI(engine)
			return fmt.Errorf("no desktop SuperLeo called %s that thinks for the queen is linked right now", host)
		}
		return nil
	}
	agentName, host, remote := splitRemoteCLI(engine)
	if !remote {
		agentName = engine
	}
	if !canThinkForQueen(agentName) {
		return fmt.Errorf("%s cannot think for the queen: Claude Code, Codex, pi and Hermes can", engine)
	}
	label := agentLabel(agentName)
	if remote {
		if !a.isRemoteCLI(engine) {
			return fmt.Errorf("no linked machine called %s has %s right now", host, label)
		}
		return nil
	}
	if !next.ExternalAgents.Enabled {
		return fmt.Errorf("the queen engine is %s on this machine, so External agents has to be on", label)
	}
	for _, c := range installedCLIs(next.ExternalAgents.Binaries) {
		if c.Name == agentName {
			return nil
		}
	}
	return fmt.Errorf("%s is not installed on this machine", label)
}

// inPlaceNote opens an answer an engine gave in the place of the one first
// in the list.
func inPlaceNote(failed, by string) string {
	return "> " + engineLabel(failed) + " 没能接这一条，由 " + engineLabel(by) + " 回答。\n\n"
}
