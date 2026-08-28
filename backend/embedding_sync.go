package backend

import (
	"fmt"
	"log"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/config"
	"github.com/liliang-cn/agent-go/v3/pkg/store"
)

// settingsEmbeddingProviderName is the row in agentgo.db that SuperAI owns.
// A fixed name means repeated syncs update one row instead of accumulating a
// new provider every launch.
const settingsEmbeddingProviderName = "superai-settings"

// syncEmbeddingProvider pushes the embed_* settings into agentgo.db.
//
// agent-go builds its embedding pool solely from the embedding_providers table;
// it never looks at SuperAI's settings.json. The two stores drifted apart the
// moment anyone configured embeddings through Settings: the value was written,
// the UI showed it, and the pool started with nothing — announced only by one
// line on stderr saying no embedding provider was configured. Vector memory and
// RAG were silently off while the settings page insisted they were on.
//
// So Settings stays the place a person edits, and this copies that decision
// into the table the pool actually reads, before the config is loaded from it.
func syncEmbeddingProvider(cfg *config.Config, s *Settings) error {
	if cfg == nil || s == nil {
		return nil
	}

	db, err := store.NewAgentGoDB(cfg.AgentDBPath())
	if err != nil {
		return fmt.Errorf("open agentgo db: %w", err)
	}
	defer func() { _ = db.Close() }()

	model := strings.TrimSpace(s.EmbedModel)
	baseURL := strings.TrimSpace(s.EmbedBaseURL)

	// No usable embedding settings: retire our row rather than leave a stale one
	// claiming a provider that the user has since cleared.
	if !s.UseEmbeddings() || model == "" || baseURL == "" {
		if existing, gerr := db.GetEmbeddingProvider(settingsEmbeddingProviderName); gerr == nil && existing != nil {
			if derr := db.DeleteEmbeddingProvider(settingsEmbeddingProviderName); derr != nil {
				return fmt.Errorf("remove embedding provider: %w", derr)
			}
			log.Printf("superai: embeddings not configured in settings; removed %q from the embedding pool",
				settingsEmbeddingProviderName)
		}
		return nil
	}

	if err := db.SaveEmbeddingProvider(&store.EmbeddingProvider{
		Name:           settingsEmbeddingProviderName,
		BaseURL:        baseURL,
		Key:            strings.TrimSpace(s.EmbedKey),
		ModelName:      model,
		MaxConcurrency: 5,
		Capability:     3,
		Enabled:        true,
	}); err != nil {
		return fmt.Errorf("save embedding provider: %w", err)
	}

	// The pool is only enabled when rag.embedding_model is also set. agent-go
	// backfills it from the first provider, but only when the key is missing
	// entirely — an existing key naming a model nobody serves any more would
	// otherwise keep embeddings off with the provider sitting right there.
	if current, gerr := db.GetConfig("rag.embedding_model"); gerr != nil || strings.TrimSpace(current) != model {
		if serr := db.SaveConfig("rag.embedding_model", model); serr != nil {
			return fmt.Errorf("save rag.embedding_model: %w", serr)
		}
	}

	log.Printf("superai: embedding provider %q -> %s (%s)", settingsEmbeddingProviderName, baseURL, model)
	return nil
}
