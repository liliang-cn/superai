package backend

import (
	"context"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/config"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
)

// scriptedLLM answers immediately so a segmented run terminates without a
// provider.
type scriptedLLM struct{}

func (scriptedLLM) Generate(context.Context, string, *domain.GenerationOptions) (string, error) {
	return "done", nil
}
func (scriptedLLM) Stream(context.Context, string, *domain.GenerationOptions, func(string)) error {
	return nil
}
func (scriptedLLM) GenerateWithTools(context.Context, []domain.Message, []domain.ToolDefinition, *domain.GenerationOptions) (*domain.GenerationResult, error) {
	return &domain.GenerationResult{Content: "All done.", FinishReason: "stop"}, nil
}
func (scriptedLLM) StreamWithTools(_ context.Context, _ []domain.Message, _ []domain.ToolDefinition, _ *domain.GenerationOptions, cb domain.ToolCallCallback) error {
	return cb(&domain.GenerationResult{Content: "All done.", FinishReason: "stop"})
}
func (scriptedLLM) GenerateStructured(context.Context, string, interface{}, *domain.GenerationOptions) (*domain.StructuredResult, error) {
	return &domain.StructuredResult{Valid: true, Raw: "{}"}, nil
}
func (scriptedLLM) RecognizeIntent(context.Context, string) (*domain.IntentResult, error) {
	return nil, nil
}

func newTestLongRunService(t *testing.T) *Service {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Home:   home,
		RAG:    config.RAGConfig{Enabled: false},
		Memory: config.MemoryConfig{StoreType: "file"},
	}
	cfg.ApplyHomeLayout()

	agentSvc, err := agent.New("SuperAI-test").
		WithConfig(cfg).
		WithLLM(scriptedLLM{}).
		Build()
	if err != nil {
		t.Fatalf("build agent service: %v", err)
	}
	t.Cleanup(func() { _ = agentSvc.Close() })
	return &Service{svc: agentSvc}
}
