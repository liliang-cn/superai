package backend

import (
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/config"
	"github.com/liliang-cn/agent-go/v3/pkg/store"
)

func syncTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{Home: t.TempDir()}
	cfg.ApplyHomeLayout()
	return cfg
}

func openSyncedDB(t *testing.T, cfg *config.Config) *store.AgentGoDB {
	t.Helper()
	db, err := store.NewAgentGoDB(cfg.AgentDBPath())
	if err != nil {
		t.Fatalf("open agentgo db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// The settings page was writing to a store the embedding pool never reads.
func TestSyncEmbeddingProviderWritesSettingsIntoPool(t *testing.T) {
	cfg := syncTestConfig(t)
	s := &Settings{
		EmbedBaseURL: "https://t2m.example/v1",
		EmbedKey:     "t2m-key",
		EmbedModel:   "embeddinggemma:latest",
	}

	if err := syncEmbeddingProvider(cfg, s); err != nil {
		t.Fatalf("sync: %v", err)
	}

	db := openSyncedDB(t, cfg)
	provider, err := db.GetEmbeddingProvider(settingsEmbeddingProviderName)
	if err != nil {
		t.Fatalf("provider missing from the pool's own table: %v", err)
	}
	if provider.BaseURL != s.EmbedBaseURL || provider.ModelName != s.EmbedModel || provider.Key != s.EmbedKey {
		t.Fatalf("provider does not match settings: %+v", provider)
	}
	if !provider.Enabled {
		t.Fatal("provider written but left disabled, so the pool would still be empty")
	}

	// A provider alone does not switch embeddings on; the model key has to agree.
	model, err := db.GetConfig("rag.embedding_model")
	if err != nil {
		t.Fatalf("rag.embedding_model not set: %v", err)
	}
	if model != s.EmbedModel {
		t.Fatalf("rag.embedding_model = %q, want %q", model, s.EmbedModel)
	}
}

// Repeated launches must not pile up one provider row per start.
func TestSyncEmbeddingProviderIsIdempotent(t *testing.T) {
	cfg := syncTestConfig(t)
	s := &Settings{
		EmbedBaseURL: "https://t2m.example/v1",
		EmbedKey:     "t2m-key",
		EmbedModel:   "embeddinggemma:latest",
	}

	for i := 0; i < 3; i++ {
		if err := syncEmbeddingProvider(cfg, s); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}

	db := openSyncedDB(t, cfg)
	providers, err := db.ListEmbeddingProviders()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("got %d providers after three syncs, want 1", len(providers))
	}
}

// Changing the model in Settings has to move rag.embedding_model with it, or
// the pool keeps asking for a model the gateway no longer serves.
func TestSyncEmbeddingProviderFollowsAModelChange(t *testing.T) {
	cfg := syncTestConfig(t)
	s := &Settings{
		EmbedBaseURL: "https://t2m.example/v1",
		EmbedKey:     "t2m-key",
		EmbedModel:   "embeddinggemma:latest",
	}
	if err := syncEmbeddingProvider(cfg, s); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	s.EmbedModel = "qwen3-embedding:latest"
	if err := syncEmbeddingProvider(cfg, s); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	db := openSyncedDB(t, cfg)
	model, err := db.GetConfig("rag.embedding_model")
	if err != nil {
		t.Fatalf("get model: %v", err)
	}
	if model != "qwen3-embedding:latest" {
		t.Fatalf("rag.embedding_model = %q, want the new model", model)
	}
}

// Clearing embeddings in Settings must actually clear them, not leave the last
// working provider behind still answering.
func TestSyncEmbeddingProviderRetiresClearedSettings(t *testing.T) {
	cfg := syncTestConfig(t)
	s := &Settings{
		EmbedBaseURL: "https://t2m.example/v1",
		EmbedKey:     "t2m-key",
		EmbedModel:   "embeddinggemma:latest",
	}
	if err := syncEmbeddingProvider(cfg, s); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	s.EmbedKey = ""
	if err := syncEmbeddingProvider(cfg, s); err != nil {
		t.Fatalf("clearing sync: %v", err)
	}

	db := openSyncedDB(t, cfg)
	if _, err := db.GetEmbeddingProvider(settingsEmbeddingProviderName); err == nil {
		t.Fatal("provider survived having its key cleared in Settings")
	}
}

// A user who configured embeddings through agent-go directly should keep that
// provider; SuperAI only owns its own row.
func TestSyncEmbeddingProviderLeavesForeignProvidersAlone(t *testing.T) {
	cfg := syncTestConfig(t)
	db := openSyncedDB(t, cfg)
	if err := db.SaveEmbeddingProvider(&store.EmbeddingProvider{
		Name:      "hand-rolled",
		BaseURL:   "http://127.0.0.1:11434/v1",
		ModelName: "nomic-embed-text",
		Enabled:   true,
	}); err != nil {
		t.Fatalf("seed foreign provider: %v", err)
	}

	if err := syncEmbeddingProvider(cfg, &Settings{}); err != nil {
		t.Fatalf("sync with empty settings: %v", err)
	}

	if _, err := db.GetEmbeddingProvider("hand-rolled"); err != nil {
		t.Fatalf("a provider SuperAI does not own was removed: %v", err)
	}
}
