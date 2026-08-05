package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/store"
)

// TestDeleteSession drives the real agent database, since the whole point of
// this path is that agent.Service has no delete and we reach past it.
func TestDeleteSession(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "agentgo.db")

	db, err := store.NewAgentGoDB(path)
	if err != nil {
		t.Fatalf("NewAgentGoDB: %v", err)
	}
	save := func(id, text string) {
		sess := &store.ChatSession{ID: id, Title: text, Messages: []store.ChatMessage{{Role: "user", Content: text}}}
		if err := db.SaveSession(sess); err != nil {
			t.Fatalf("SaveSession %s: %v", id, err)
		}
	}
	save("keep-me", "first conversation")
	save("delete-me", "second conversation")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc := &Service{dataDir: dataDir}

	if err := svc.DeleteSession("delete-me"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	reopened, err := store.NewAgentGoDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.GetSession("delete-me"); err == nil {
		t.Error("the deleted session should be gone")
	}
	if kept, err := reopened.GetSession("keep-me"); err != nil || kept == nil {
		t.Errorf("the other session must survive: %v", err)
	}
}

func TestDeleteSessionRejectsBadInput(t *testing.T) {
	svc := &Service{dataDir: t.TempDir()}

	if err := svc.DeleteSession(""); err == nil {
		t.Error("an empty id is not a session")
	}
	if err := svc.DeleteSession("   "); err == nil {
		t.Error("a blank id is not a session")
	}
	// A missing database must report rather than create an empty one, which
	// would silently hide that the agent never wrote there.
	if err := svc.DeleteSession("anything"); err == nil {
		t.Error("expected an error when the database does not exist")
	} else if !strings.Contains(err.Error(), "agent database") {
		t.Errorf("error should name the problem, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.dataDir, "agentgo.db")); !os.IsNotExist(err) {
		t.Error("a failed delete must not create the database")
	}

	var nilService *Service
	if err := nilService.DeleteSession("x"); err == nil {
		t.Error("a nil service should not panic or claim success")
	}
}
