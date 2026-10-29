package backend

import (
	"fmt"
	"testing"
)

func chainString(c []QueenEngineEntry) string {
	s := ""
	for _, e := range c {
		on := "-"
		if e.Enabled {
			on = "+"
		}
		s += on + e.Engine + " "
	}
	return s
}

// Settings from before the list read as the engine, then SuperLeo when the
// fallback was on.
func TestTheQueenEngineListIsReadFromTheSingleEngine(t *testing.T) {
	off := false
	for _, c := range []struct {
		h    HiveSettings
		want string
	}{
		{HiveSettings{}, "+superleo "},
		{HiveSettings{QueenEngine: "claude"}, "+claude +superleo "},
		{HiveSettings{QueenEngine: "claude.mac", QueenFallback: &off}, "+claude.mac -superleo "},
	} {
		if got := chainString(c.h.QueenChain()); got != c.want {
			t.Errorf("%+v: %q, want %q", c.h, got, c.want)
		}
	}
}

// The single engine and the fallback follow the list, for clients that know
// only them.
func TestTheSingleEngineFollowsTheList(t *testing.T) {
	for _, c := range []struct {
		list     []QueenEngineEntry
		engine   string
		fallback bool
		chain    string
	}{
		{[]QueenEngineEntry{{"claude", true}, {"claude.dell", true}, {"superleo", true}}, "claude", true, "+claude +claude.dell +superleo "},
		{[]QueenEngineEntry{{"claude", false}, {"codex", true}, {"superleo", false}}, "codex", false, "-claude +codex -superleo "},
		{[]QueenEngineEntry{{"superleo", true}, {"claude", true}}, "", true, "+superleo +claude "},
		// Blanks and repeats go; SuperLeo is always there to switch on.
		{[]QueenEngineEntry{{" claude ", true}, {"", true}, {"claude", false}}, "claude", false, "+claude -superleo "},
	} {
		var h HiveSettings
		h.SetQueenChain(c.list)
		if h.QueenEngine != c.engine || h.QueenFallsBack() != c.fallback || chainString(h.QueenChain()) != c.chain {
			t.Errorf("%v: engine %q fallback %v chain %q", c.list, h.QueenEngine, h.QueenFallsBack(), chainString(h.QueenChain()))
		}
	}
}

// An older client choosing one engine means what it always meant: that
// engine, then SuperLeo or nothing.
func TestChoosingOneEngineMeansWhatItAlwaysDid(t *testing.T) {
	var h HiveSettings
	h.SetQueenChain([]QueenEngineEntry{{"claude.dell", true}, {"claude", true}, {"superleo", true}})
	h.PromoteQueenEngine("claude", false)
	if got := chainString(h.QueenChain()); got != "+claude -claude.dell -superleo " || h.QueenEngine != "claude" || h.QueenFallsBack() {
		t.Fatalf("%q %q %v", got, h.QueenEngine, h.QueenFallsBack())
	}
	h.PromoteQueenEngine("", true)
	if got := chainString(h.QueenChain()); got != "+superleo -claude -claude.dell " || h.QueenEngine != "" {
		t.Fatalf("%q %q", got, h.QueenEngine)
	}
	if on := fmt.Sprint(h.QueenEnginesOn()); on != "[superleo]" {
		t.Fatal(on)
	}
}

// The list survives settings.json being rewritten (queenengine.json), and
// only SuperLeo switched on leaves no file.
func TestTheQueenEngineListIsKeptInItsOwnFile(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	s := &Settings{}
	s.Hive.SetQueenChain([]QueenEngineEntry{{"claude.dell", true}, {"claude", false}, {"superleo", true}})
	if err := s.saveQueenEngine(); err != nil {
		t.Fatal(err)
	}
	var again Settings
	again.mergeQueenEngine()
	if got := chainString(again.Hive.QueenChain()); got != "+claude.dell -claude +superleo " || again.Hive.QueenEngine != "claude.dell" {
		t.Fatalf("%q %q", got, again.Hive.QueenEngine)
	}
	s.Hive.SetQueenChain([]QueenEngineEntry{{"superleo", true}, {"claude", false}})
	if err := s.saveQueenEngine(); err != nil {
		t.Fatal(err)
	}
	var none Settings
	none.mergeQueenEngine()
	if none.Hive.QueenEngines != nil || none.Hive.QueenEngine != "" {
		t.Fatalf("%+v", none.Hive)
	}
}
