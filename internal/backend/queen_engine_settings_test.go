package backend

import "testing"

func TestOnlyQueenEngineDiffers(t *testing.T) {
	base := &Settings{LLMModel: "m", Hive: HiveSettings{Role: HiveRoleQueen, Name: "q"},
		ExternalAgents: ExternalAgents{Roots: []string{" /a ", ""}}}
	engine := *base
	engine.Hive.SetQueenChain([]QueenEngineEntry{{Engine: "hermes.mac", Enabled: true}, {Engine: QueenEngineSuperLeo, Enabled: true}})
	engine.Hive.PublicURL = "https://ai.example.test"
	engine.ExternalAgents.Roots = []string{"/a"} // the same once tidied, as Save leaves it
	if !OnlyQueenEngineDiffers(base, &engine) {
		t.Fatal("an engine change was taken for more")
	}
	model := engine
	model.LLMModel = "other"
	if OnlyQueenEngineDiffers(base, &model) {
		t.Fatal("a model change was taken for an engine change")
	}
	role := engine
	role.Hive.Role = HiveRoleWorker
	if OnlyQueenEngineDiffers(base, &role) || OnlyQueenEngineDiffers(nil, &engine) {
		t.Fatal("a role change, or no settings before, was taken for an engine change")
	}
	if base.ExternalAgents.Roots[0] != " /a " {
		t.Fatal("comparing tidied the settings themselves")
	}
}
