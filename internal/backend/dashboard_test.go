package backend

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/config"
)

// The tracker must see every model turn, even when the provider reports no
// usage — a gateway that hides usage fields must read as "turns counted,
// tokens unknown", not as an idle agent.
func TestUsageObserverCountsModelTurns(t *testing.T) {
	cfg := &config.Config{
		Home:   t.TempDir(),
		RAG:    config.RAGConfig{Enabled: false},
		Memory: config.MemoryConfig{StoreType: "file"},
	}
	cfg.ApplyHomeLayout()

	svc, err := agent.New("usage-test").
		WithConfig(cfg).
		WithLLM(scriptedLLM{}).
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer svc.Close()

	u := newUsageTracker(filepath.Join(t.TempDir(), "usage.json"))
	svc.RegisterObserver(&usageObserver{u: u})

	if _, err := svc.Run(context.Background(), "Say pong."); err != nil {
		t.Fatalf("run: %v", err)
	}

	snap := u.snapshot()
	if turns := snap["modelTurns"].(int64); turns == 0 {
		t.Fatalf("observer never saw a model turn: %+v", snap)
	}
}
