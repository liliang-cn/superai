package app

import (
	"strings"
	"testing"

	"github.com/liliang-cn/superai/internal/backend"
)

func TestTheQueenIsAddressedByHerNamesAndAliases(t *testing.T) {
	a := &App{settings: &backend.Settings{QueenName: "小蜂", QueenAliases: []string{"bee"}}}
	for _, msg := range []string{"@queen 看看日程", "@小蜂 帮我", "@小蜂，帮我", "@Bee: hi", "  @bee"} {
		if !a.addressedToQueen(msg) {
			t.Fatalf("%q not to the queen", msg)
		}
	}
	for _, msg := range []string{"小蜂，帮我", "@pi hi", "@bees hi", "mail me@小蜂", "@小蜂/x"} {
		if a.addressedToQueen(msg) {
			t.Fatalf("%q taken as the queen's", msg)
		}
	}
	// Unnamed, "@queen" is still hers.
	if !(&App{settings: &backend.Settings{}}).addressedToQueen("@queen hi") {
		t.Fatal("@queen with no name set")
	}
	// On a worker the queen is someone else.
	w := &App{settings: &backend.Settings{QueenAliases: []string{"bee"}, Hive: backend.HiveSettings{Role: backend.HiveRoleWorker}}}
	if w.addressedToQueen("@bee hi") {
		t.Fatal("a worker took an address to the queen")
	}
}

func TestAnAddressToTheQueenNeverGoesToAnAgentOfTheSameName(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	log := &eventLog{}
	a := NewApp()
	a.emitFn = log.record
	a.settings = &backend.Settings{
		QueenName: "小蜂", QueenAliases: []string{"bee"},
		RemoteAgents: backend.RemoteAgents{Enabled: true, Agents: map[string]backend.RemoteAgent{"bee": {URL: "http://127.0.0.1:1"}}},
	}
	if !a.addressable("bee") {
		t.Fatal("the agent is not reachable, so the test proves nothing")
	}
	a.SendChat("s1", "@bee 帮我看看", nil)
	// No service: the turn stays here and says so, rather than going out.
	events := log.snapshot()
	if len(events) == 0 || events[0].name != "chat:error" || !strings.Contains(events[0].text(), "backend not ready") {
		t.Fatalf("%+v", events)
	}
}

func TestClientsReadTheQueensName(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	a := &App{settings: &backend.Settings{QueenName: "小蜂", QueenAliases: []string{"bee"}}}
	if a.HiveStatus()["queen_name"] != "小蜂" || a.GetStatus()["queenName"] != "小蜂" {
		t.Fatal("status does not carry the name")
	}
	id := a.QueenIdentity()
	if id["name"] != "小蜂" || strings.Join(id["aliases"].([]string), ",") != "bee" {
		t.Fatalf("%v", id)
	}
	if (&App{}).QueenIdentity()["name"] != "queen" {
		t.Fatal("no settings")
	}
	if _, err := a.SetQueenIdentity("little bee", nil); err == nil {
		t.Fatal("a name with a space was accepted")
	}
}
