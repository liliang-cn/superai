package backend

// The dashboard's data side: what is running, what it has cost, what the
// agent is configured as, and what task memory holds.
//
// Token counting hangs off the runtime's observer rather than any one entry
// point. Chat turns, scheduled prompts and long-run segments all end in the
// same loop, and OnModelEnd fires for every model turn the loop makes — so
// one small observer sees them all, where instrumenting Stream and StreamLong
// would have missed whatever entry point is added next.

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	_ "modernc.org/sqlite"
)

// usageDaysKept bounds the per-day history. Two weeks is what the dashboard
// draws; the rest is pruned on write so the file cannot grow forever.
const usageDaysKept = 14

// usageTracker accumulates model-turn token counts, persisted as one small
// JSON file so totals survive restarts.
type usageTracker struct {
	mu   sync.Mutex
	path string

	Total  int64            `json:"total_tokens"`
	Cached int64            `json:"cached_tokens"`
	Turns  int64            `json:"model_turns"`
	Days   map[string]int64 `json:"days"` // yyyy-mm-dd -> tokens
}

func newUsageTracker(path string) *usageTracker {
	u := &usageTracker{path: path, Days: map[string]int64{}}
	if raw, err := os.ReadFile(path); err == nil {
		// A corrupt file starts the count over; usage is a gauge, not a ledger.
		_ = json.Unmarshal(raw, u)
		if u.Days == nil {
			u.Days = map[string]int64{}
		}
	}
	return u
}

func (u *usageTracker) add(tokens, cached int) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	// The turn is counted even when the provider reported no usage — zero
	// tokens across many turns is how the dashboard can say "this gateway
	// does not report usage" instead of showing an idle-looking zero.
	u.Turns++
	if tokens > 0 {
		u.Total += int64(tokens)
		u.Cached += int64(cached)
		u.Days[time.Now().Format("2006-01-02")] += int64(tokens)
	}
	if len(u.Days) > usageDaysKept {
		keys := make([]string, 0, len(u.Days))
		for k := range u.Days {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys[:len(keys)-usageDaysKept] {
			delete(u.Days, k)
		}
	}
	// Written on every model turn, which is at most one small file per few
	// seconds of the busiest run — cheaper than being wrong after a crash.
	if raw, err := json.Marshal(u); err == nil {
		_ = os.WriteFile(u.path, raw, 0o644)
	}
}

func (u *usageTracker) snapshot() map[string]any {
	if u == nil {
		return map[string]any{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	days := make([]map[string]any, 0, len(u.Days))
	keys := make([]string, 0, len(u.Days))
	for k := range u.Days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		days = append(days, map[string]any{"day": k, "tokens": u.Days[k]})
	}
	return map[string]any{
		"totalTokens":  u.Total,
		"cachedTokens": u.Cached,
		"modelTurns":   u.Turns,
		"today":        u.Days[time.Now().Format("2006-01-02")],
		"days":         days,
	}
}

// usageObserver feeds the tracker from every model turn the runtime makes.
type usageObserver struct {
	agent.BaseObserver
	u *usageTracker
}

func (o *usageObserver) OnModelEnd(_ context.Context, _ agent.ModelInfo, res *agent.ModelResult, _ error) {
	if res != nil {
		o.u.add(res.TokensUsed, res.CachedTokens)
	}
}

// DashboardData is everything the dashboard shows that only the service
// knows: runs in flight, accumulated usage, and task memory.
func (s *Service) DashboardData() map[string]any {
	if s == nil || s.svc == nil {
		return map[string]any{}
	}
	runs := s.svc.ActiveRuns()
	if runs == nil {
		runs = []agent.ActiveRun{}
	}
	return map[string]any{
		"activeRuns": runs,
		"usage":      s.usage.snapshot(),
		"tasks":      s.taskMemoryRows(8),
		"memoryMode": s.MemoryMode,
	}
}

// taskMemoryRows reads recent tasks straight from the task memory tables.
//
// Read directly rather than through agent.TaskStore because the store's
// interface is per-task on purpose (load one, resume one) and a listing is a
// dashboard concern, not a runtime one. The database belongs to this process;
// a read-only peek at two tables is the honest cheap version.
func (s *Service) taskMemoryRows(limit int) []map[string]any {
	out := []map[string]any{}
	db, err := sql.Open("sqlite", filepath.Join(s.dataDir, "agentgo.db"))
	if err != nil {
		return out
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT t.id, t.goal, t.status, t.resume_brief, t.updated_at,
		       (SELECT COUNT(*) FROM task_runs r WHERE r.task_id = t.id)
		FROM task_state t ORDER BY t.updated_at DESC LIMIT ?`, limit)
	if err != nil {
		// Most likely an older database without the tables yet; an empty
		// dashboard section says that better than an error would.
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, goal, status, brief string
			updated                 time.Time
			runCount                int
		)
		if rows.Scan(&id, &goal, &status, &brief, &updated, &runCount) != nil {
			continue
		}
		out = append(out, map[string]any{
			"id": id, "goal": goal, "status": status, "brief": brief,
			"updatedAt": updated, "runs": runCount,
		})
	}
	return out
}
