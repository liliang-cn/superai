package backend

import (
	"context"
	"os"
	"testing"
)

// Reads only. Runs against a real shared brain when SUPERAI_TEST_CORTEX names
// one (and $CORTEXDB_GRPC_TOKEN holds its token); skipped otherwise.
func TestReadThroughMemoryStoreFindsDefaultNamespace(t *testing.T) {
	endpoint := os.Getenv("SUPERAI_TEST_CORTEX")
	if endpoint == "" {
		t.Skip("SUPERAI_TEST_CORTEX not set")
	}
	ctx := context.Background()
	only, err := newReadThroughMemoryStore(endpoint, "", "hive", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer only.Close()
	both, err := newReadThroughMemoryStore(endpoint, "", "hive", []string{sharedNamespaceDefault})
	if err != nil {
		t.Fatal(err)
	}
	defer both.Close()

	has := func(s *readThroughMemoryStore) bool {
		hits, err := s.SearchByText(ctx, "接三宝的时间", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hits {
			if h.ID == "user_daily_schedule_system" {
				return true
			}
		}
		return false
	}
	if has(only) {
		t.Log("hive alone already finds it")
	}
	if !has(both) {
		t.Fatal("reading default did not surface the pickup rule")
	}
	if !both.isForeign("user_daily_schedule_system") {
		t.Fatal("a default-namespace hit must be marked read-only")
	}
	if err := both.Delete(ctx, "user_daily_schedule_system"); err != errReadOnlyMemory {
		t.Fatalf("delete of a foreign memory: got %v, want errReadOnlyMemory", err)
	}
}
