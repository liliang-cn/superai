package backend

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTheQueenIsCalledQueenUntilSomeoneNamesHer(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.QueenDisplayName() != "queen" || len(s.QueenAliasList()) != 0 || s.QueenNamed() {
		t.Fatalf("default: %q %v", s.QueenDisplayName(), s.QueenAliasList())
	}
	if queenNameSection(s) != "" {
		t.Fatal("an unnamed queen changed the prompt")
	}
	var zero *Settings
	if zero.QueenDisplayName() != "queen" || !zero.IsQueenName("Queen") {
		t.Fatal("nil settings")
	}
}

func TestAQueenNameIsSavedAndSurvivesSettingsBeingRewritten(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	s, _ := LoadSettings()
	s.QueenName = " 小蜂 "
	s.QueenAliases = []string{"bee", "", "Bee", "小蜂", "queen", "蜂蜂"}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.QueenDisplayName() != "小蜂" || strings.Join(got.QueenAliasList(), ",") != "bee,蜂蜂" {
		t.Fatalf("%q %v", got.QueenDisplayName(), got.QueenAliasList())
	}
	// What the hive's entrypoint does on every start: settings.json from the
	// Secret, which knows nothing about the name.
	if err := os.WriteFile(settingsPath(), []byte(`{"llm_model":"m"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadSettings()
	if got.QueenDisplayName() != "小蜂" || len(got.QueenAliasList()) != 2 {
		t.Fatalf("lost on rewrite: %q %v", got.QueenDisplayName(), got.QueenAliasList())
	}
	// Back to the default removes queen.json, so it shadows nothing.
	got.QueenName, got.QueenAliases = "", nil
	if err := got.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(queenPath()); !os.IsNotExist(err) {
		t.Fatalf("queen.json kept: %v", err)
	}
	if again, _ := LoadSettings(); again.QueenDisplayName() != "queen" {
		t.Fatalf("%q", again.QueenDisplayName())
	}
}

func TestEveryNameTheQueenAnswersToMatchesExactly(t *testing.T) {
	s := &Settings{QueenName: "小蜂", QueenAliases: []string{"Bee"}}
	for _, n := range []string{"queen", "QUEEN", "小蜂", "bee", "Bee"} {
		if !s.IsQueenName(n) {
			t.Fatalf("%q not hers", n)
		}
	}
	for _, n := range []string{"", "小", "bees", "superleo-queen", "worker-1"} {
		if s.IsQueenName(n) {
			t.Fatalf("%q taken as hers", n)
		}
	}
}

func TestAQueenNameMustBeAddressable(t *testing.T) {
	for _, ok := range [][]string{{"小蜂"}, {"", "bee"}, {"Bee.2", "b_e-e"}} {
		if err := ValidateQueenName(ok[0], ok[1:]); err != nil {
			t.Fatalf("%v: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{"little bee"}, {"bee", "a@b"}, {strings.Repeat("x", 33)}, {"q", "1", "2", "3", "4", "5", "6", "7", "8", "9"}} {
		if ValidateQueenName(bad[0], bad[1:]) == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestThePersonaCarriesTheQueensName(t *testing.T) {
	s := &Settings{QueenName: "小蜂", QueenAliases: []string{"bee"}, Hive: HiveSettings{Role: HiveRoleQueen, Name: "superleo-queen"}}
	p := buildPersona(time.Now(), false, false) + hiveSection(s.Hive) + queenNameSection(s)
	for _, want := range []string{"The person calls you 小蜂", "queen, bee also mean you", "Call yourself 小蜂"} {
		if !strings.Contains(p, want) {
			t.Fatalf("missing %q in\n%s", want, p)
		}
	}
	// Aliases alone are enough to say something.
	if !strings.Contains(queenNameSection(&Settings{QueenAliases: []string{"bee"}}), "calls you queen; bee also mean you") {
		t.Fatal(queenNameSection(&Settings{QueenAliases: []string{"bee"}}))
	}
	// A worker is not the queen, whatever its settings say.
	s.Hive.Role = HiveRoleWorker
	if queenNameSection(s) != "" {
		t.Fatal("a worker was told it is the queen")
	}
}
