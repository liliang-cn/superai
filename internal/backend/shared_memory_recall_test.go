package backend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/config"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/store"
)

// slowBrain answers a text search the way the shared brain answered the
// queen's standing wakes: correctly, after longer than agent-go's 2s default.
type slowBrain struct {
	*store.FileMemoryStore
	delay time.Duration
	fact  string
}

func (b *slowBrain) EmbedsServerSide() bool { return true }

func (b *slowBrain) SearchByText(ctx context.Context, _ string, _ int) ([]*domain.MemoryWithScore, error) {
	select {
	case <-time.After(b.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []*domain.MemoryWithScore{{
		Memory: &domain.Memory{ID: "pickup", Type: domain.MemoryTypeFact, Content: b.fact, Importance: 1, CreatedAt: time.Now()},
		Score:  1,
	}}, nil
}

// promptLLM keeps what the turn sent, to see whether recalled memory was in it.
type promptLLM struct {
	scriptedLLM
	mu   sync.Mutex
	sent strings.Builder
}

func (l *promptLLM) keep(msgs []domain.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range msgs {
		l.sent.WriteString(m.Content)
		l.sent.WriteByte('\n')
	}
}

func (l *promptLLM) GenerateWithTools(ctx context.Context, msgs []domain.Message, tools []domain.ToolDefinition, o *domain.GenerationOptions) (*domain.GenerationResult, error) {
	l.keep(msgs)
	return l.scriptedLLM.GenerateWithTools(ctx, msgs, tools, o)
}

func (l *promptLLM) StreamWithTools(ctx context.Context, msgs []domain.Message, tools []domain.ToolDefinition, o *domain.GenerationOptions, cb domain.ToolCallCallback) error {
	l.keep(msgs)
	return l.scriptedLLM.StreamWithTools(ctx, msgs, tools, o, cb)
}

func (l *promptLLM) prompt() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sent.String()
}

func TestSharedBrainRecallOutlastsTheDefaultDeadline(t *testing.T) {
	if sharedRecallTimeout <= agent.DefaultMemoryRecallTimeout {
		t.Fatalf("shared recall budget %v is no longer than agent-go's default %v", sharedRecallTimeout, agent.DefaultMemoryRecallTimeout)
	}
	home := t.TempDir()
	cfg := &config.Config{Home: home, RAG: config.RAGConfig{Enabled: false}}
	applySharedMemoryConfig(cfg, &Settings{SharedMemoryEndpoint: "127.0.0.1:1", SharedMemoryNamespace: "hive"})
	cfg.ApplyHomeLayout()
	if got, want := cfg.Memory.RecallTimeoutMs, int(sharedRecallTimeout/time.Millisecond); got != want {
		t.Fatalf("RecallTimeoutMs = %d, want %d", got, want)
	}

	files, err := store.NewFileMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const fact = "周五下午四点半去接三宝"
	brain := &slowBrain{FileMemoryStore: files, delay: agent.DefaultMemoryRecallTimeout + 500*time.Millisecond, fact: fact}
	llm := &promptLLM{}
	svc, err := agent.New("SuperLeo-test").
		WithConfig(cfg).
		WithLLM(llm).
		WithMemory(agent.WithMemoryStoreType(store.CortexRemoteStoreType), agent.WithMemoryStore(brain)).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if _, err := svc.Run(context.Background(), "这周五什么时候接三宝？"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompt(), fact) {
		t.Fatalf("a recall that took %v was dropped; the turn went out without %q", brain.delay, fact)
	}
}
