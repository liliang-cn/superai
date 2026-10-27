package app

import (
	"testing"
)

// A member keeps its queen's roster and key, and says where it would take
// agents in; with no roster, or no model, it draws nobody.
func TestAMemberKeepsTheRosterAndDrawsTheNextQueenFromIt(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	t.Setenv("SUPERAI_AGENT_DOOR_PORT", "43785")
	a := &App{}
	if _, _, ok := a.lanElect("w1", map[string]bool{}); ok {
		t.Fatal("drew a queen with no roster")
	}
	r := LanRoster{Queen: "mac", Term: 4, Key: "k3y", Members: []LanMember{{"w1", "10.0.0.1:43782"}, {"w2", "10.0.0.2:43782"}}}
	got, err := a.HiveRoster(r)
	if err != nil || got["door_port"] != 43785 {
		t.Fatalf("%v %v", got, err)
	}
	if hiveKey() != "k3y" || !hiveKeyMatches("k3y") || hiveKeyMatches("nope") {
		t.Fatal("the key was not kept")
	}
	if again := (&App{}).lanRoster(); again.Term != 4 || len(again.Members) != 2 {
		t.Fatalf("not kept across a restart: %+v", again)
	}
	// Without a model of its own a member cannot be queen, so draws nobody.
	if _, _, ok := a.lanElect("w1", map[string]bool{}); ok {
		t.Fatal("a member without a model took part")
	}
}
