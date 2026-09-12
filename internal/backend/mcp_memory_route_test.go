package backend

import (
	"encoding/json"
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

// TestResolveMCPConfigDropsTheDuplicateRoute is the whole point: once the
// memory backend owns an endpoint, an MCP server pointed at that same endpoint
// is the same store under a second name, and must not be mounted.
func TestResolveMCPConfigDropsTheDuplicateRoute(t *testing.T) {
	src := writeConfig(t, twoRouteConfig)
	filtered := filepath.Join(filepath.Dir(src), "mcpServers.effective.json")

	path, dropped, err := resolveMCPConfigPath(src, filtered, "192.168.123.252:47821")
	if err != nil {
		t.Fatalf("resolveMCPConfigPath() error = %v", err)
	}
	if path != filtered {
		t.Errorf("path = %q, want the filtered copy %q", path, filtered)
	}
	if len(dropped) != 1 || dropped[0] != "cortexdb" {
		t.Fatalf("dropped = %v, want [cortexdb]", dropped)
	}

	raw, err := os.ReadFile(filtered)
	if err != nil {
		t.Fatalf("read filtered config: %v", err)
	}
	var parsed mcpServersFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("filtered config is not valid JSON: %v", err)
	}
	if _, still := parsed.MCPServers["cortexdb"]; still {
		t.Error("the duplicate route survived into the filtered config")
	}
	if _, kept := parsed.MCPServers["playwright"]; !kept {
		t.Error("an unrelated MCP server was dropped; only the duplicate route should go")
	}

	// The user's own file is never rewritten.
	original, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != twoRouteConfig {
		t.Error("resolveMCPConfigPath() modified the user's mcpServers.json")
	}
}

// TestResolveMCPConfigLocalBackendKeepsEverything pins the default: on local
// memory nothing is owned, so nothing is filtered and no file is written.
func TestResolveMCPConfigLocalBackendKeepsEverything(t *testing.T) {
	src := writeConfig(t, twoRouteConfig)
	filtered := filepath.Join(filepath.Dir(src), "mcpServers.effective.json")

	path, dropped, err := resolveMCPConfigPath(src, filtered, "")
	if err != nil {
		t.Fatalf("resolveMCPConfigPath() error = %v", err)
	}
	if path != src {
		t.Errorf("path = %q, want the original %q", path, src)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing", dropped)
	}
	if _, err := os.Stat(filtered); !os.IsNotExist(err) {
		t.Error("a filtered config was written even though nothing was filtered")
	}
}

// TestResolveMCPConfigMatchesByEndpointNotByName is the red line: the duplicate
// is found because it routes to the same address, not because it is called
// something memory-ish. A server renamed to anything at all is still caught,
// and a server that merely sounds like a memory server is left alone.
func TestResolveMCPConfigMatchesByEndpointNotByName(t *testing.T) {
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
	filtered := filepath.Join(filepath.Dir(src), "mcpServers.effective.json")

	_, dropped, err := resolveMCPConfigPath(src, filtered, "10.0.0.9:47821")
	if err != nil {
		t.Fatalf("resolveMCPConfigPath() error = %v", err)
	}
	if len(dropped) != 1 || dropped[0] != "some-unrelated-name" {
		t.Fatalf("dropped = %v, want [some-unrelated-name] — matching is by endpoint, not by name", dropped)
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
