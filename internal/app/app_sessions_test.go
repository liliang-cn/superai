package app

import (
	"sync"
	"testing"

	"github.com/liliang-cn/superai/internal/backend"
)

// A conversation whose first answer is still being written is listed, once,
// ahead of the saved ones; one already saved is not listed twice.
func TestRunningConversationsAreListed(t *testing.T) {
	var open sync.Map
	open.Store("r1", backend.ChatSessionInfo{ID: "new", Title: "research SDS"})
	open.Store("r2", backend.ChatSessionInfo{ID: "old", Title: "follow-up"})
	got := withOpenTurns([]backend.ChatSessionInfo{{ID: "old", Title: "saved"}}, &open)
	if len(got) != 2 || got[0].ID != "new" || got[1].Title != "saved" {
		t.Fatalf("got %+v", got)
	}
}
