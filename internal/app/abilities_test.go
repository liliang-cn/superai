package app

import (
	"slices"
	"strings"
	"testing"

	agentlinkpb "github.com/liliang-cn/superleo/internal/agentlink/pb"
	"github.com/liliang-cn/superleo/internal/backend"
)

// A worker says what its machine can do; the queen sees it in the roster, in
// who a task needing it can go to, and in what hive_delegate tells her.
func TestAWorkersAbilitiesRouteATaskToItsMachine(t *testing.T) {
	core := NewApp()
	core.settings = &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "queen"}}
	addr := startCore(t, core)
	thinks := true
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "mac", Host: "mbp", Os: "darwin", Kind: "mac", Thinks: &thinks,
		Abilities: []*agentlinkpb.Ability{{Name: "desktop", About: "apps"}, {Name: "files"}}})
	linkHello(t, core, addr, &agentlinkpb.Hello{Name: "pod1", Host: "pod1", Os: "linux", Kind: "pod", Thinks: &thinks,
		Abilities: []*agentlinkpb.Ability{{Name: "files"}, {Name: "exec"}}})

	if got := core.delegateCandidates([]string{"desktop"}); !slices.Equal(got, []string{"mac"}) {
		t.Fatalf("who can do desktop: %v", got)
	}
	if got := core.delegateCandidates([]string{"exec"}); !slices.Equal(got, []string{"pod1"}) {
		t.Fatalf("who can do exec: %v", got)
	}
	if got := core.delegateCandidates([]string{"browser"}); len(got) != 0 {
		t.Fatalf("nobody has a browser: %v", got)
	}
	roster := core.abilityRoster()
	if !strings.Contains(roster, "mac (machine mac): desktop, files") || !strings.Contains(roster, "pod1 (machine pod1): exec, files") {
		t.Fatalf("roster = %q", roster)
	}
	var seen []string
	for _, w := range core.HiveWorkers() {
		if w.Name == "mac" {
			seen = abilityLine(w.Abilities)
		}
	}
	if !slices.Equal(seen, []string{"desktop", "files"}) {
		t.Fatalf("the worker list carries %v", seen)
	}
	for _, la := range core.LinkedAgentsAll() {
		if la.Name == "mac" && len(la.Abilities) != 2 {
			t.Fatalf("linked agent abilities: %v", la.Abilities)
		}
	}

	// A queen with nothing of her own lacks every ability, so a piece that
	// needs one can only go to a worker.
	if got := core.unmetAbilities([]string{"browser", "noted", "desktop"}); !slices.Equal(got, []string{"browser", "desktop"}) {
		t.Fatalf("unmet = %v", got)
	}
}
