package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graph"
)

// GraphPlanStore keeps a task's plan in a graph of its own.
//
// It implements agent.PlanStore, so a long task that is interrupted comes back
// knowing which steps it finished and what each one produced. One graph per
// task, in its own database file, beside the brain and never in it: a run's
// steps are what happened once, not what is known, and a few thousand of them
// would bury the knowledge graph they sat in.
//
// The same file is what `serve_graph_3d` renders, so the plan can be watched
// filling in while the task runs.
//
// # Why nodes are keyed by position
//
// A plan is an ordered list that may repeat itself — "run the tests" can
// legitimately appear twice, once before a fix and once after. CortexDB's
// entity tools key nodes on their name, which would merge those two into one
// and lose a step along with its progress. So this writes nodes directly with
// an explicit `step:<n>` id: position is identity, duplicates stay distinct,
// and order survives without depending on how rows come back.
type GraphPlanStore struct {
	root string

	mu   sync.Mutex
	open map[string]*cortexdb.DB
}

// NewGraphPlanStore returns a store that keeps its graphs under root.
func NewGraphPlanStore(root string) *GraphPlanStore {
	return &GraphPlanStore{root: root, open: map[string]*cortexdb.DB{}}
}

// planNodeType is what the 3D view colours these by.
const planNodeType = "step"

// planStepVector is the placeholder embedding every step node carries.
//
// The graph store rejects a node without a vector, and a plan step has nothing
// to embed against: it is retrieved by position, never by similarity. A fixed
// one-element vector satisfies the store without pretending these are
// searchable.
var planStepVector = []float32{0}

// safeKeyPattern matches keys that can be a filename as they are.
var safeKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,48}$`)

// graphNameFor turns a caller's key into a filename.
//
// The key is a task or session id chosen by whoever started the run, so it can
// be anything — a path, a sentence, empty. Readable keys are kept readable
// because someone reading the directory should be able to tell which task a
// file belongs to; everything else is hashed. The hash is always appended for
// the unsafe ones so two keys that clean up to the same string ("a/b" and
// "a_b") do not become one plan.
func graphNameFor(key string) string {
	if safeKeyPattern.MatchString(key) {
		return strings.ToLower(key)
	}
	sum := sha256.Sum256([]byte(key))
	return "plan-" + hex.EncodeToString(sum[:8])
}

// dbFor opens a task's graph, creating it on first write.
//
// Held open rather than reopened per call: a plan is written through on every
// change to it, and reopening a SQLite file on that cadence would be the
// slowest part of ticking off a step.
func (s *GraphPlanStore) dbFor(name string) (*cortexdb.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if db, ok := s.open[name]; ok {
		return db, nil
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create plan directory: %w", err)
	}
	// No embedder: nothing here is retrieved by similarity, and a plan must be
	// writable when the network is not.
	db, err := cortexdb.Open(cortexdb.DefaultConfig(filepath.Join(s.root, name+".db")))
	if err != nil {
		return nil, fmt.Errorf("open plan graph %q: %w", name, err)
	}
	if err := db.Graph().InitGraphSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init plan graph %q: %w", name, err)
	}
	s.open[name] = db
	return db, nil
}

func stepID(i int) string { return "step:" + strconv.Itoa(i) }

// SavePlan writes the whole plan, replacing whatever was there.
func (s *GraphPlanStore) SavePlan(ctx context.Context, key string, items []agent.PlanItem) error {
	db, err := s.dbFor(graphNameFor(key))
	if err != nil {
		return err
	}
	g := db.Graph()

	for i, it := range items {
		node := &graph.GraphNode{
			ID:       stepID(i),
			Vector:   planStepVector,
			Content:  it.Text,
			NodeType: planNodeType,
			Properties: map[string]interface{}{
				"index": i,
				"done":  it.Done,
				"note":  it.Note,
			},
		}
		if err := g.UpsertNode(ctx, node); err != nil {
			return fmt.Errorf("write step %d: %w", i, err)
		}
		if i > 0 {
			// The edge is what makes this a plan rather than a bag of steps,
			// and it is what the 3D view draws the sequence from.
			edge := &graph.GraphEdge{
				ID:         "next:" + strconv.Itoa(i-1),
				FromNodeID: stepID(i - 1),
				ToNodeID:   stepID(i),
				EdgeType:   "next",
				Weight:     1,
			}
			if err := g.UpsertEdge(ctx, edge); err != nil {
				return fmt.Errorf("link step %d: %w", i, err)
			}
		}
	}

	// A plan can get shorter — a step dropped, a plan rewritten. Left behind,
	// the old tail would come back on the next load as steps nobody planned.
	// Deleting the node takes its edges with it (ON DELETE CASCADE).
	for i := len(items); ; i++ {
		node, gerr := g.GetNode(ctx, stepID(i))
		if gerr != nil || node == nil {
			break
		}
		if derr := g.DeleteNode(ctx, stepID(i)); derr != nil {
			return fmt.Errorf("drop old step %d: %w", i, derr)
		}
	}
	return nil
}

// LoadPlan reads a task's plan back. An unknown key is an empty plan, not an
// error: a task that has never been planned is a normal thing to ask about.
func (s *GraphPlanStore) LoadPlan(ctx context.Context, key string) ([]agent.PlanItem, error) {
	name := graphNameFor(key)
	if _, err := os.Stat(filepath.Join(s.root, name+".db")); err != nil {
		return nil, nil
	}
	db, err := s.dbFor(name)
	if err != nil {
		return nil, err
	}
	g := db.Graph()

	type indexed struct {
		idx  int
		item agent.PlanItem
	}
	found := make([]indexed, 0, 8)
	// Walked by position rather than listed and sorted: the ids are dense from
	// zero by construction, so the first gap is the end of the plan.
	for i := 0; ; i++ {
		node, gerr := g.GetNode(ctx, stepID(i))
		if gerr != nil || node == nil {
			break
		}
		item := agent.PlanItem{Text: node.Content}
		if v, ok := node.Properties["done"].(bool); ok {
			item.Done = v
		}
		if v, ok := node.Properties["note"].(string); ok {
			item.Note = v
		}
		found = append(found, indexed{idx: i, item: item})
	}
	sort.Slice(found, func(a, b int) bool { return found[a].idx < found[b].idx })

	out := make([]agent.PlanItem, 0, len(found))
	for _, f := range found {
		out = append(out, f.item)
	}
	return out, nil
}

// Close releases every graph this store has open.
func (s *GraphPlanStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for name, db := range s.open {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.open, name)
	}
	return firstErr
}
