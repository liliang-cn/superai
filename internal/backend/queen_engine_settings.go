package backend

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	Engine    string `json:"engine,omitempty"`
	Fallback  *bool  `json:"fallback,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
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
	if u := strings.TrimSpace(q.PublicURL); u != "" {
		s.Hive.PublicURL = u
	}
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
	if q.Engine == "" && q.PublicURL == "" && q.Fallback == nil {
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
