package app

import (
	"testing"

	"github.com/liliang-cn/superai/internal/backend"
)

// A queen that restarts is still on the term she held, and a member already
// on it is not taken for a later queen's.
func TestARestartedQueenKeepsHerTerm(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	saveTerm(3)
	a := &App{}
	cfg := &backend.Settings{Hive: backend.HiveSettings{Role: backend.HiveRoleQueen, Name: "q"}}
	a.registerHiveTools(&backend.Service{}, cfg)
	if a.hive == nil || a.hive.Term() != 3 {
		t.Fatalf("hive %+v", a.hive)
	}
	if _, err := a.hive.Join(backend.HiveHello{Protocol: backend.HiveProtocol, Name: "w", Role: backend.HiveRoleWorker, URL: "http://w", Term: 3}); err != nil {
		t.Fatalf("a member on her own term was refused: %v", err)
	}
}
