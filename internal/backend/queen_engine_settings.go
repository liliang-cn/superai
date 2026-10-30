package backend

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// queenEngineChoice is queenengine.json: what the queen thinks with, set from
// a client (SetQueenEngine, the settings page, superleo_settings_set).
//
// A file of its own for the reason queen.json and agentlink.json are: in the
// hive's containers settings.json is rewritten from a Secret on every start,
// and a queen told to think with claude.mac would be back on SuperLeo's own
// agent after the next restart, without a word to anyone.
type queenEngineChoice struct {
	Engine    string             `json:"engine,omitempty"`
	Fallback  *bool              `json:"fallback,omitempty"`
	PublicURL string             `json:"public_url,omitempty"`
	Engines   []QueenEngineEntry `json:"engines,omitempty"`
}

func queenEnginePath() string { return filepath.Join(DataDir(), "queenengine.json") }

// mergeQueenEngine lays queenengine.json over what settings.json said. The
// file exists only once one of them was set to something other than its
// default, and that is the later word: the engine and the
// fallback as they are, the public URL when the file has one, so an
// operator's address in the Secret is not hidden by a blank nobody chose.
func (s *Settings) mergeQueenEngine() {
	raw, err := os.ReadFile(queenEnginePath())
	if err != nil {
		return
	}
	var q queenEngineChoice
	if json.Unmarshal(raw, &q) != nil {
		return
	}
	s.Hive.QueenEngine = strings.TrimSpace(q.Engine)
	s.Hive.QueenFallback = q.Fallback
	s.Hive.QueenEngines = q.Engines
	if u := strings.TrimSpace(q.PublicURL); u != "" {
		s.Hive.PublicURL = u
	}
}

// OnlyQueenEngineDiffers reports whether next is prev with nothing changed
// but who thinks for the queen: the engines, the fallback and the public URL
// their runs reach /mcp at. None of those goes into the service — a turn reads
// them from the settings as it starts — so a save that changes only them has
// nothing to rebuild, and the turns running on the service carry on.
func OnlyQueenEngineDiffers(prev, next *Settings) bool {
	if prev == nil || next == nil {
		return false
	}
	a, okA := withoutQueenEngine(prev)
	b, okB := withoutQueenEngine(next)
	return okA && okB && string(a) == string(b)
}

// withoutQueenEngine is s as it would be saved, with the engine fields blank.
// A copy through JSON, so tidying it does not touch slices s shares.
func withoutQueenEngine(s *Settings) ([]byte, bool) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	var c Settings
	if json.Unmarshal(raw, &c) != nil {
		return nil, false
	}
	c.ExternalAgents.normalize()
	c.RemoteAgents.normalize()
	c.normalizeQueen()
	c.AgentLink.normalize()
	if len(c.Channels) == 0 {
		c.Channels = nil // Save leaves none as an empty list
	}
	c.Hive.QueenEngine, c.Hive.QueenFallback, c.Hive.QueenEngines, c.Hive.PublicURL = "", nil, nil, ""
	out, err := json.Marshal(c)
	return out, err == nil
}

// saveQueenEngine writes queenengine.json when any of them is not its
// default and removes it when all are, so a default chosen here does not
// shadow what an operator later puts in the Secret.
func (s *Settings) saveQueenEngine() error {
	q := queenEngineChoice{
		Engine:    strings.TrimSpace(s.Hive.QueenEngine),
		PublicURL: strings.TrimSpace(s.Hive.PublicURL),
	}
	if !s.Hive.QueenFallsBack() {
		off := false
		q.Fallback = &off
	}
	// Only SuperLeo switched on is the default: the engines switched off
	// are offered again anyway, so their order is not worth a file.
	if on := s.Hive.QueenEnginesOn(); !slices.Equal(on, []string{QueenEngineSuperLeo}) {
		q.Engines = s.Hive.QueenChain()
	}
	if q.Engine == "" && q.PublicURL == "" && q.Fallback == nil && q.Engines == nil {
		if err := os.Remove(queenEnginePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(queenEnginePath(), raw, 0o644)
}

// QueenEngineSuperLeo names SuperLeo's own agent in Hive.QueenEngines.
const QueenEngineSuperLeo = "superleo"

// QueenEngineEntry is one engine in the queen's list: "superleo", a CLI on
// this machine ("claude") or on a linked one ("claude.mac"), and whether
// turns may go to it.
type QueenEngineEntry struct {
	Engine  string `json:"engine"`
	Enabled bool   `json:"enabled"`
}

func defaultQueenChain() []QueenEngineEntry {
	return []QueenEngineEntry{{Engine: QueenEngineSuperLeo, Enabled: true}}
}

// legacyQueenChain is the list a single engine and its fallback switch
// mean: the engine, then SuperLeo when the fallback is on.
func legacyQueenChain(engine string, fallback bool) []QueenEngineEntry {
	engine = strings.TrimSpace(engine)
	if engine == "" || engine == QueenEngineSuperLeo {
		return defaultQueenChain()
	}
	return []QueenEngineEntry{{Engine: engine, Enabled: true}, {Engine: QueenEngineSuperLeo, Enabled: fallback}}
}

// cleanQueenChain trims the names, drops blanks and repeats (the first
// one stays), and puts SuperLeo in when it is missing, switched off: it is
// always there to switch on.
func cleanQueenChain(list []QueenEngineEntry) []QueenEngineEntry {
	out := make([]QueenEngineEntry, 0, len(list)+1)
	seen := map[string]bool{}
	for _, e := range list {
		e.Engine = strings.TrimSpace(e.Engine)
		if e.Engine == "" || seen[e.Engine] {
			continue
		}
		seen[e.Engine] = true
		out = append(out, e)
	}
	if !seen[QueenEngineSuperLeo] {
		out = append(out, QueenEngineEntry{Engine: QueenEngineSuperLeo})
	}
	return out
}

// QueenChain is the queen's engines in the order they are tried, switched
// off ones included: QueenEngines, or what QueenEngine and QueenFallback
// said for settings from before the list.
func (h HiveSettings) QueenChain() []QueenEngineEntry {
	if len(h.QueenEngines) == 0 {
		return legacyQueenChain(h.QueenEngine, h.QueenFallsBack())
	}
	return cleanQueenChain(h.QueenEngines)
}

// QueenEnginesOn is the engines a turn may go to, in order.
func (h HiveSettings) QueenEnginesOn() []string {
	var out []string
	for _, e := range h.QueenChain() {
		if e.Enabled {
			out = append(out, e.Engine)
		}
	}
	return out
}

// SetQueenChain keeps a new list, and QueenEngine and QueenFallback in step
// with it: the first engine switched on ("" for SuperLeo), and whether
// SuperLeo is switched on after it.
func (h *HiveSettings) SetQueenChain(list []QueenEngineEntry) {
	list = cleanQueenChain(list)
	h.QueenEngines = list
	first, fallback := "", false
	for _, e := range list {
		if !e.Enabled {
			continue
		}
		if first == "" {
			first = e.Engine
			continue
		}
		if e.Engine == QueenEngineSuperLeo {
			fallback = true
		}
	}
	if first == "" || first == QueenEngineSuperLeo {
		h.QueenEngine, h.QueenFallback = "", nil
		return
	}
	h.QueenEngine, h.QueenFallback = first, &fallback
}

// PromoteQueenEngine is a client that knows only one engine and the
// fallback switch choosing them, and means what they always meant: the
// engine goes first, switched on (SuperLeo for ""), SuperLeo is switched on
// or off after it by the fallback, and every other engine is switched off,
// keeping its place.
func (h *HiveSettings) PromoteQueenEngine(engine string, fallback bool) {
	engine = strings.TrimSpace(engine)
	if engine == "" {
		engine = QueenEngineSuperLeo
	}
	list := []QueenEngineEntry{{Engine: engine, Enabled: true}}
	for _, e := range h.QueenChain() {
		if e.Engine == engine {
			continue
		}
		e.Enabled = e.Engine == QueenEngineSuperLeo && fallback
		list = append(list, e)
	}
	h.SetQueenChain(list)
}
