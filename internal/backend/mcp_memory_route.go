package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// One capability, one route.
//
// When the shared CortexDB becomes SuperAI's memory backend, the built-in
// memory_* tools already reach it. An MCP server in mcpServers.json pointed at
// that same server offers memory a second time — and, observed in practice,
// the model then calls both and reports "not found". But the same server also
// offers what the built-in tools do not: the knowledge graph, ontologies,
// SPARQL, graph search. So the server stays mounted, and only its own memory
// tools are withheld from each run (memoryRouteDenylist).
//
// The server is identified structurally, by endpoint: one whose command line
// or environment carries the very address the memory backend owns is the same
// store by definition. It is not identified by name, and there is no list of
// "memory-ish" server names anywhere here — such a list would only ever cover
// the servers somebody thought to enumerate.

// mcpServerEntry mirrors the Claude-style mcpServers.json entry shape closely
// enough to read every string in it.
type mcpServerEntry map[string]any

type mcpServersFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// serversSharingMemory names the servers in srcPath that route to
// ownedEndpoint. With no endpoint owned there are none.
func serversSharingMemory(srcPath, ownedEndpoint string) ([]string, error) {
	ownedEndpoint = strings.TrimSpace(ownedEndpoint)
	if ownedEndpoint == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, err
	}
	var parsed mcpServersFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse %s: %w", srcPath, err)
	}
	var shared []string
	for name, entry := range parsed.MCPServers {
		if mcpEntryRoutesTo(entry, ownedEndpoint) {
			shared = append(shared, name)
		}
	}
	sort.Strings(shared)
	return shared, nil
}

// memoryRouteDenylist is what a run must not be offered: the memory tools of
// the servers that share the memory backend's store. tools is every tool name
// the agent has; agent-go names an MCP tool mcp_<server>_<tool>.
func memoryRouteDenylist(shared, tools []string) []string {
	var deny []string
	for _, server := range shared {
		for _, prefix := range []string{"mcp_" + server + "_memory_", "mcp_" + server + "_knowledge_memory_"} {
			for _, t := range tools {
				if strings.HasPrefix(t, prefix) {
					deny = append(deny, t)
				}
			}
		}
	}
	return deny
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

// dedupe returns the given paths with duplicates removed, order preserved.
// The two MCP config locations collapse into one whenever DataDir() and the
// agent's data directory happen to agree.
func dedupe(paths ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
