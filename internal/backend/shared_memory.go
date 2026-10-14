package backend

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/store"
)

// sharedNamespaceDefault is the namespace every other client of the shared
// brain writes to: openclaw, Claude Code, Codex, the desktop app, noted's sync.
const sharedNamespaceDefault = "default"

// errReadOnlyMemory refuses a write aimed at a memory this store only reads.
var errReadOnlyMemory = errors.New("shared memory: that memory belongs to another namespace and is read-only here")

// readThroughMemoryStore writes to one namespace of the shared brain and reads
// from it plus others.
//
// The hive keeps its own namespace so its patrol notes don't flood the brain
// everyone else reads, but the person's own facts (where they live, when to
// pick up 三宝, their meetings) were written by other clients into "default".
// A hive that searched only its own namespace answered "nothing recorded" to
// questions the shared brain could answer, so searches go to both.
//
// Writes stay in the primary namespace. A memory found through a read-only
// namespace is remembered, and an update, delete or supersede aimed at it is
// refused, so the hive's consolidation can never rewrite another client's facts.
type readThroughMemoryStore struct {
	*store.CortexRemoteMemoryStore
	extra []*store.CortexRemoteMemoryStore

	foreign sync.Map // memory id → struct{}, ids seen only through extra
}

func newReadThroughMemoryStore(endpoint, token, namespace string, extraNamespaces []string) (*readThroughMemoryStore, error) {
	open := func(ns string) (*store.CortexRemoteMemoryStore, error) {
		return store.NewCortexRemoteMemoryStore(store.CortexRemoteConfig{
			Endpoint:  endpoint,
			Token:     token,
			Namespace: ns,
			Scope:     "global",
		})
	}
	primary, err := open(namespace)
	if err != nil {
		return nil, err
	}
	rt := &readThroughMemoryStore{CortexRemoteMemoryStore: primary}
	seen := map[string]bool{strings.TrimSpace(namespace): true}
	for _, ns := range extraNamespaces {
		ns = strings.TrimSpace(ns)
		if ns == "" || seen[ns] {
			continue
		}
		seen[ns] = true
		s, err := open(ns)
		if err != nil {
			_ = rt.Close()
			return nil, err
		}
		rt.extra = append(rt.extra, s)
	}
	return rt, nil
}

// SearchByText searches every namespace and merges by the server's score. The
// scores come from the same server and model, so they compare directly.
func (r *readThroughMemoryStore) SearchByText(ctx context.Context, query string, topK int) ([]*domain.MemoryWithScore, error) {
	if topK <= 0 {
		topK = 10
	}
	type result struct {
		hits []*domain.MemoryWithScore
		err  error
	}
	all := append([]*store.CortexRemoteMemoryStore{r.CortexRemoteMemoryStore}, r.extra...)
	results := make([]result, len(all))
	var wg sync.WaitGroup
	for i, s := range all {
		wg.Add(1)
		go func(i int, s *store.CortexRemoteMemoryStore) {
			defer wg.Done()
			hits, err := s.SearchByText(ctx, query, topK)
			results[i] = result{hits, err}
		}(i, s)
	}
	wg.Wait()

	// The primary namespace failing is an error; a read-only one failing only
	// loses its share of the results.
	if results[0].err != nil {
		return nil, results[0].err
	}
	merged := make([]*domain.MemoryWithScore, 0, topK*len(all))
	ids := map[string]bool{}
	for i, res := range results {
		for _, h := range res.hits {
			if h == nil || h.Memory == nil || ids[h.ID] {
				continue
			}
			ids[h.ID] = true
			if i > 0 {
				r.foreign.Store(h.ID, struct{}{})
			}
			merged = append(merged, h)
		}
	}
	sort.SliceStable(merged, func(a, b int) bool { return merged[a].Score > merged[b].Score })
	if len(merged) > topK {
		merged = merged[:topK]
	}
	return merged, nil
}

func (r *readThroughMemoryStore) isForeign(id string) bool {
	_, ok := r.foreign.Load(id)
	return ok
}

func (r *readThroughMemoryStore) Update(ctx context.Context, memory *domain.Memory) error {
	if memory != nil && r.isForeign(memory.ID) {
		return errReadOnlyMemory
	}
	return r.CortexRemoteMemoryStore.Update(ctx, memory)
}

func (r *readThroughMemoryStore) Delete(ctx context.Context, id string) error {
	if r.isForeign(id) {
		return errReadOnlyMemory
	}
	return r.CortexRemoteMemoryStore.Delete(ctx, id)
}

func (r *readThroughMemoryStore) MarkStale(ctx context.Context, id string, supersededByID string) error {
	if r.isForeign(id) {
		return errReadOnlyMemory
	}
	return r.CortexRemoteMemoryStore.MarkStale(ctx, id, supersededByID)
}

func (r *readThroughMemoryStore) Close() error {
	err := r.CortexRemoteMemoryStore.Close()
	for _, s := range r.extra {
		_ = s.Close()
	}
	return err
}

var (
	_ domain.MemoryStore           = (*readThroughMemoryStore)(nil)
	_ domain.MemoryStaleMarker     = (*readThroughMemoryStore)(nil)
	_ domain.MemoryGraphWriter     = (*readThroughMemoryStore)(nil)
	_ domain.MemoryServerEmbedding = (*readThroughMemoryStore)(nil)
)
