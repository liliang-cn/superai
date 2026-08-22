package backend

import (
	"path/filepath"
	"testing"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// The in-process cortexbridge toolbox must expose the full ontology surface
// (ObjectSets + Actions landed in cortexdb v2.67.0; superai was pinned to
// v2.63.2 which only had save/get/list/delete).
func TestInProcessToolboxHasFullOntologySurface(t *testing.T) {
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(t.TempDir(), "cortex.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	have := map[string]bool{}
	for _, def := range db.GraphRAGTools().Definitions() {
		have[def.Name] = true
	}
	for _, want := range []string{
		"ontology_save", "ontology_get", "ontology_list", "ontology_diff",
		"ontology_delete", "object_set_resolve", "ontology_action_list", "ontology_action_apply",
	} {
		if !have[want] {
			t.Errorf("missing tool %s", want)
		}
	}
}
