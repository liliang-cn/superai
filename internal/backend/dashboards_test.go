package backend

import (
	"path/filepath"
	"strings"
	"testing"
)

func dashService(t *testing.T) *Service {
	t.Helper()
	return &Service{dashboards: newDashboardStore(filepath.Join(t.TempDir(), "dashboards.json"))}
}

// Improving a board the user already has must change that board, not leave the
// old version beside it. Three rows called 资产管理 with three slightly different
// prompts is not a history — it is a list with no way to tell which is current,
// which is what shipped before this.
func TestReplacingABoardKeepsItsIdentity(t *testing.T) {
	s := dashService(t)
	first, err := s.SaveDashboard("资产管理", "<first>", "查看资产管理看板")
	if err != nil {
		t.Fatal(err)
	}
	// A schedule is the thing most obviously pointing at the id.
	if err := s.SetDashboardCron(first.ID, "0 9 * * *"); err != nil {
		t.Fatal(err)
	}

	second, err := s.ReplaceDashboard(first.ID, "资产管理", "<second>", "升级资产管理看板")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s.Dashboards()); got != 1 {
		t.Fatalf("replacing produced %d boards", got)
	}
	if second.ID != first.ID {
		t.Errorf("the id changed: %s -> %s", first.ID, second.ID)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Error("the day it was made was overwritten")
	}
	if second.Cron != "0 9 * * *" {
		t.Errorf("the schedule was lost: %q", second.Cron)
	}
	if second.Source != "<second>" || second.Prompt != "升级资产管理看板" {
		t.Errorf("the contents did not change: %+v", second)
	}
	if !second.RefreshedAt.After(first.RefreshedAt) && second.RefreshedAt.Equal(first.RefreshedAt) {
		t.Error("the board reads as no fresher than before it was replaced")
	}
}

// A save that forgot to repeat the question must not turn a board that
// refreshes itself into one that never can again.
func TestReplacingWithoutAPromptKeepsTheOldOne(t *testing.T) {
	s := dashService(t)
	first, _ := s.SaveDashboard("board", "<a>", "the question")
	got, err := s.ReplaceDashboard(first.ID, "", "<b>", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompt != "the question" {
		t.Errorf("the question was erased: %q", got.Prompt)
	}
	if got.Name != "board" {
		t.Errorf("an empty name overwrote the label: %q", got.Name)
	}
}

// A board is what the user calls it, so the name is how one is found again.
func TestBoardsAreFoundByName(t *testing.T) {
	s := dashService(t)
	if _, err := s.SaveDashboard("资产管理", "<a>", "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDashboard("Other", "<b>", "q"); err != nil {
		t.Fatal(err)
	}
	if got := s.DashboardsNamed("  资产管理 "); len(got) != 1 {
		t.Errorf("a name with stray spaces found %d boards", len(got))
	}
	if got := s.DashboardsNamed("other"); len(got) != 1 {
		t.Errorf("the name lookup is case-sensitive: %d", len(got))
	}
	if got := s.DashboardsNamed("nothing"); len(got) != 0 {
		t.Errorf("an unknown name matched %d", len(got))
	}
	// Two of a name is possible — the store never stopped it — and the answer
	// has to be both, so a caller can refuse rather than guess.
	if _, err := s.SaveDashboard("资产管理", "<c>", "q"); err != nil {
		t.Fatal(err)
	}
	if got := s.DashboardsNamed("资产管理"); len(got) != 2 {
		t.Errorf("a duplicated name found %d boards, want both", len(got))
	}
}

// Replacing something that is not there must say so rather than quietly
// creating it, or a typo'd id becomes a fourth board.
func TestReplacingAMissingBoardFails(t *testing.T) {
	s := dashService(t)
	if _, err := s.ReplaceDashboard("nope", "x", "<a>", "q"); err == nil {
		t.Fatal("replacing a board that does not exist was accepted")
	}
	if got := len(s.Dashboards()); got != 0 {
		t.Errorf("it created %d boards anyway", got)
	}
	// And an empty document is still nothing to save, however it is written.
	first, _ := s.SaveDashboard("board", "<a>", "q")
	if _, err := s.ReplaceDashboard(first.ID, "board", "   ", "q"); err == nil {
		t.Error("an empty replacement was accepted")
	}
	if d, _ := s.Dashboard(first.ID); !strings.Contains(d.Source, "<a>") {
		t.Error("a refused replacement still wiped the contents")
	}
}

// Which board a save lands on. This is the decision that was missing: without
// it, dashboard_save could only append, and every "升级一下这个看板" left the
// previous version beside the new one.
func TestASaveLandsOnTheBoardItIsAbout(t *testing.T) {
	s := dashService(t)
	first, _ := s.SaveDashboard("资产管理", "<a>", "查看资产管理看板")

	// The same name is the same board.
	target, note, refuse := s.dashboardTarget("资产管理", "", false)
	if refuse != "" || target != first.ID {
		t.Fatalf("a repeat save did not find the board: target=%q refuse=%q", target, refuse)
	}
	if note == "" {
		t.Error("the model was not told it replaced something")
	}

	// An id beats a name, and a wrong id is refused rather than turned into a
	// new board — a typo must not become a fourth row.
	if got, _, _ := s.dashboardTarget("something else", first.ID, false); got != first.ID {
		t.Errorf("an explicit id was ignored: %q", got)
	}
	if _, _, refuse := s.dashboardTarget("资产管理", "not-an-id", false); refuse == "" {
		t.Error("an unknown id was accepted")
	}

	// A name nobody has used is a new board.
	if got, _, refuse := s.dashboardTarget("brand new", "", false); got != "" || refuse != "" {
		t.Errorf("an unused name did not create: target=%q refuse=%q", got, refuse)
	}

	// And so is one the user explicitly asked for a second copy of.
	if got, _, refuse := s.dashboardTarget("资产管理", "", true); got != "" || refuse != "" {
		t.Errorf("new:true still replaced: target=%q refuse=%q", got, refuse)
	}
}

// The mess that already exists must not be guessed at. Three boards called
// 资产管理 are on disk right now; picking one of them would overwrite something
// the user might still want.
func TestAnAmbiguousNameIsRefusedRatherThanGuessed(t *testing.T) {
	s := dashService(t)
	for i := 0; i < 3; i++ {
		if _, err := s.SaveDashboard("资产管理", "<x>", "q"); err != nil {
			t.Fatal(err)
		}
	}
	target, _, refuse := s.dashboardTarget("资产管理", "", false)
	if target != "" {
		t.Errorf("it picked %q out of three", target)
	}
	if !strings.Contains(refuse, "3") || !strings.Contains(refuse, "dashboard_list") {
		t.Errorf("the refusal does not say how to resolve it: %q", refuse)
	}
	// Naming one of them explicitly still works.
	id := s.Dashboards()[0].ID
	if got, _, refuse := s.dashboardTarget("资产管理", id, false); got != id || refuse != "" {
		t.Errorf("an id did not break the tie: %q / %q", got, refuse)
	}
}
