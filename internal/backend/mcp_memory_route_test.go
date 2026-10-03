package backend

import (
	"os"
	"path/filepath"
	"testing"
)

const twoRouteConfig = `{
  "mcpServers": {
    "cortexdb": {
      "command": "/Users/x/.cortexdb/bin/cortexdb-mcp-stdio",
      "env": {"CORTEXDB_REMOTE": "192.168.123.252:47821", "CORTEXDB_GRPC_TOKEN": "deadbeef"}
    },
    "playwright": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest"]
    }
  }
}`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcpServers.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The server that shares the memory backend's store is found, and only its
// memory tools are withheld; the rest of what it offers stays.
func TestTheSharedServerKeepsAllButItsMemoryTools(t *testing.T) {
	src := writeConfig(t, twoRouteConfig)
	shared, err := serversSharingMemory(src, "192.168.123.252:47821")
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 1 || shared[0] != "cortexdb" {
		t.Fatalf("shared = %v, want [cortexdb]", shared)
	}
	tools := []string{
		"memory_save", "mcp_cortexdb_memory_save", "mcp_cortexdb_memory_search",
		"mcp_cortexdb_knowledge_memory_recall", "mcp_cortexdb_knowledge_save",
		"mcp_cortexdb_expand_graph", "mcp_playwright_browser_navigate",
	}
	deny := memoryRouteDenylist(shared, tools)
	want := map[string]bool{"mcp_cortexdb_memory_save": true, "mcp_cortexdb_memory_search": true, "mcp_cortexdb_knowledge_memory_recall": true}
	if len(deny) != len(want) {
		t.Fatalf("deny = %v", deny)
	}
	for _, d := range deny {
		if !want[d] {
			t.Errorf("%s withheld; only the shared server's memory tools should be", d)
		}
	}
}

// On local memory nothing is owned, so nothing is withheld.
func TestALocalBackendSharesNothing(t *testing.T) {
	shared, err := serversSharingMemory(writeConfig(t, twoRouteConfig), "")
	if err != nil || len(shared) != 0 {
		t.Fatalf("shared = %v, %v", shared, err)
	}
}

// The red line: the server is found because it routes to the same address,
// not because it is called something memory-ish.
func TestTheSharedServerIsFoundByEndpointNotByName(t *testing.T) {
	src := writeConfig(t, `{
  "mcpServers": {
    "some-unrelated-name": {
      "command": "whatever",
      "env": {"BRAIN_ADDR": "10.0.0.9:47821"}
    },
    "cortexdb-memory-brain": {
      "type": "http",
      "url": "https://elsewhere.example/mcp"
    }
  }
}`)
	shared, err := serversSharingMemory(src, "10.0.0.9:47821")
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 1 || shared[0] != "some-unrelated-name" {
		t.Fatalf("shared = %v, want [some-unrelated-name]", shared)
	}
}

func TestSettingsSharedMemoryResolution(t *testing.T) {
	t.Setenv("CORTEXDB_REMOTE", "env.example:47821")
	t.Setenv("CORTEXDB_GRPC_TOKEN", "env-token")

	s := &Settings{MemoryBackend: MemoryBackendLocal}
	if s.UseSharedMemory() {
		t.Error("local backend reported as shared")
	}

	s.MemoryBackend = MemoryBackendShared
	if !s.UseSharedMemory() {
		t.Error("shared backend with an env endpoint reported as unusable")
	}
	if got := s.SharedMemoryEndpointResolved(); got != "env.example:47821" {
		t.Errorf("endpoint = %q, want the env fallback", got)
	}
	if got := s.SharedMemoryTokenResolved(); got != "env-token" {
		t.Errorf("token = %q, want the env fallback", got)
	}

	s.SharedMemoryEndpoint = "explicit.example:47821"
	s.SharedMemoryToken = "explicit-token"
	if got := s.SharedMemoryEndpointResolved(); got != "explicit.example:47821" {
		t.Errorf("endpoint = %q, want the explicit setting to win", got)
	}
	if got := s.SharedMemoryTokenResolved(); got != "explicit-token" {
		t.Errorf("token = %q, want the explicit setting to win", got)
	}

	// Shared with no endpoint anywhere must not silently half-enable.
	t.Setenv("CORTEXDB_REMOTE", "")
	s.SharedMemoryEndpoint = ""
	if s.UseSharedMemory() {
		t.Error("shared backend with no endpoint reported as usable")
	}
}

// TestSettingsBackfillDefaultsToLocal pins that an existing settings file with
// no memory_backend key keeps behaving exactly as before.
func TestSettingsBackfillDefaultsToLocal(t *testing.T) {
	def := defaults()
	s := Settings{}
	s.backfill(def)
	if s.MemoryBackend != MemoryBackendLocal {
		t.Errorf("MemoryBackend = %q, want %q", s.MemoryBackend, MemoryBackendLocal)
	}
	if s.SharedMemoryNamespace != "default" {
		t.Errorf("SharedMemoryNamespace = %q, want \"default\"", s.SharedMemoryNamespace)
	}
}
