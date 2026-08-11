package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// One capability, one route.
//
// When the shared CortexDB becomes SuperAI's memory backend, the built-in
// memory_* tools already reach it. If an MCP server in mcpServers.json is
// *also* pointed at that same server, the model sees two names for one store
// and — observed in practice — calls both and then reports "not found".
//
// The duplicate is identified structurally, by endpoint: a server whose command
// line or environment carries the very address the memory backend now owns is
// the same store by definition. It is not identified by name, and there is no
// list of "memory-ish" server names anywhere here — such a list would only ever
// cover the servers somebody thought to enumerate.

// mcpServerEntry mirrors the Claude-style mcpServers.json entry shape closely
// enough to re-serialise a filtered file without losing fields.
type mcpServerEntry map[string]any

type mcpServersFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// resolveMCPConfigPath returns the config path to hand agent-go, plus the names
// of any servers that were dropped because they route to ownedEndpoint.
//
// With no endpoint to own, or nothing matching it, srcPath is returned
// unchanged and nothing is written. Otherwise a filtered copy is written next
// to it and that path is returned; the user's own file is never modified.
func resolveMCPConfigPath(srcPath, filteredPath, ownedEndpoint string) (string, []string, error) {
	ownedEndpoint = strings.TrimSpace(ownedEndpoint)
	if ownedEndpoint == "" {
		return srcPath, nil, nil
	}
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return srcPath, nil, err
	}
	var parsed mcpServersFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return srcPath, nil, fmt.Errorf("parse %s: %w", srcPath, err)
	}

	kept := map[string]mcpServerEntry{}
	var dropped []string
	for name, entry := range parsed.MCPServers {
		if mcpEntryRoutesTo(entry, ownedEndpoint) {
			dropped = append(dropped, name)
			continue
		}
		kept[name] = entry
	}
	if len(dropped) == 0 {
		return srcPath, nil, nil
	}

	out, err := json.MarshalIndent(mcpServersFile{MCPServers: kept}, "", "  ")
	if err != nil {
		return srcPath, dropped, err
	}
	if err := os.MkdirAll(filepath.Dir(filteredPath), 0o755); err != nil {
		return srcPath, dropped, err
	}
	if err := os.WriteFile(filteredPath, out, 0o600); err != nil {
		return srcPath, dropped, err
	}
	return filteredPath, dropped, nil
}

// mcpEntryRoutesTo reports whether an MCP server entry is configured to talk to
// endpoint — i.e. the address appears in its url, command, args or environment.
func mcpEntryRoutesTo(entry mcpServerEntry, endpoint string) bool {
	for _, value := range flattenJSONStrings(entry) {
		if strings.Contains(value, endpoint) {
			return true
		}
	}
	return false
}

// flattenJSONStrings collects every string leaf in a decoded JSON value.
func flattenJSONStrings(v any) []string {
	switch typed := v.(type) {
	case string:
		return []string{typed}
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, flattenJSONStrings(item)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, item := range typed {
			out = append(out, flattenJSONStrings(item)...)
		}
		return out
	case mcpServerEntry:
		return flattenJSONStrings(map[string]any(typed))
	default:
		return nil
	}
}
