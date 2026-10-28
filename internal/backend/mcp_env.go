package backend

// Secrets in mcpServers.json come from the environment.
//
// A remote MCP server (noted, say) wants a bearer token in a header. Written
// into the file verbatim it would travel wherever the file does — and the
// queen's file is handed to every worker as it is (hive_abilities.go), so one
// token would end up on every volume in the hive. Instead an entry names the
// variable: "Authorization": "Bearer ${NOTED_TOKEN}". The file stays free of
// the secret, each process fills it in from its own environment when it
// starts the server, and a member that was not given the variable leaves the
// server out instead of calling it with "Bearer ".
//
// agent-go reads the files itself and knows nothing of this, so the agent is
// handed a resolved copy (resolveMCPConfig) kept under the data directory with
// the same permissions as auth.json. Only `${NAME}` is expanded; a bare `$` is
// left alone so an argument that happens to contain one is not mangled.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var mcpEnvRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// MCPConfigSources are the MCP config files this instance reads, in order:
// the user's file (what the UI and the agent install into, and what a queen
// hands her workers), the one the cluster writes under data/, and what a
// worker took from its queen.
func MCPConfigSources() []string {
	return dedupe(mcpConfigPath(), filepath.Join(DataDir(), "data", "mcpServers.json"), HiveMCPFile())
}

// expandMCPEnv fills every ${NAME} in s from lookup and lists the names it
// could not fill (unset or empty).
func expandMCPEnv(s string, lookup func(string) (string, bool)) (string, []string) {
	var missing []string
	out := mcpEnvRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := mcpEnvRef.FindStringSubmatch(ref)[1]
		if v, ok := lookup(name); ok && v != "" {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	return out, missing
}

// expandMCPEntry expands the string fields of one mcpServers entry — url,
// headers, env, command and args — and returns a copy, plus the variables it
// needed and did not find. Other fields are copied untouched.
func expandMCPEntry(entry map[string]any, lookup func(string) (string, bool)) (map[string]any, []string) {
	out := make(map[string]any, len(entry))
	missing := map[string]bool{}
	// A variable the entry sets in its own env is there for its args and
	// headers too: mcp-remote's documented form is
	// "--header", "Authorization:${AUTH}" with "env": {"AUTH": "Bearer …"},
	// and the token is in no shell. The process's own value still wins.
	if own, ok := entry["env"].(map[string]any); ok && len(own) > 0 {
		outer := lookup
		lookup = func(k string) (string, bool) {
			if v, ok := outer(k); ok {
				return v, true
			}
			if v, ok := own[k].(string); ok && !strings.Contains(v, "${") {
				return v, true
			}
			return "", false
		}
	}
	str := func(v string) string {
		s, miss := expandMCPEnv(v, lookup)
		for _, m := range miss {
			missing[m] = true
		}
		return s
	}
	for k, v := range entry {
		switch k {
		case "url", "command":
			if s, ok := v.(string); ok {
				out[k] = str(s)
				continue
			}
			if list, ok := v.([]any); ok && k == "command" {
				cp := make([]any, len(list))
				for i, e := range list {
					if s, ok := e.(string); ok {
						cp[i] = str(s)
					} else {
						cp[i] = e
					}
				}
				out[k] = cp
				continue
			}
		case "args":
			if list, ok := v.([]any); ok {
				cp := make([]any, len(list))
				for i, e := range list {
					if s, ok := e.(string); ok {
						cp[i] = str(s)
					} else {
						cp[i] = e
					}
				}
				out[k] = cp
				continue
			}
		case "headers", "env":
			if m, ok := v.(map[string]any); ok {
				cp := make(map[string]any, len(m))
				for hk, hv := range m {
					if s, ok := hv.(string); ok {
						cp[hk] = str(s)
					} else {
						cp[hk] = hv
					}
				}
				out[k] = cp
				continue
			}
		}
		out[k] = v
	}
	names := make([]string, 0, len(missing))
	for n := range missing {
		names = append(names, n)
	}
	sort.Strings(names)
	return out, names
}

// mcpMissingEnv names the variables an entry refers to that lookup lacks.
func mcpMissingEnv(entry map[string]any, lookup func(string) (string, bool)) []string {
	_, missing := expandMCPEntry(entry, lookup)
	return missing
}

// resolvedMCPDir holds the expanded copies handed to agent-go. Under the data
// directory, owner-only: it holds the same kind of secret auth.json does.
func resolvedMCPDir() string { return filepath.Join(DataDir(), "run", "mcp") }

// resolveMCPConfig returns the path agent-go should read for src. A file with
// no ${NAME} in it is returned as it is. Otherwise an expanded copy is written
// under resolvedMCPDir and its path returned; a server whose variables are not
// all set is left out of the copy and logged, never started half-configured.
func resolveMCPConfig(src string, lookup func(string) (string, bool)) (string, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if !mcpEnvRef.Match(raw) {
		return src, nil
	}
	var f struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", err
	}
	out := map[string]map[string]any{}
	for name, entry := range f.MCPServers {
		resolved, missing := expandMCPEntry(entry, lookup)
		if len(missing) > 0 {
			log.Printf("superai: mcp server %s (%s) left out: %s not set", name, src, strings.Join(missing, ", "))
			continue
		}
		out[name] = resolved
	}
	body, err := json.MarshalIndent(map[string]any{"mcpServers": out}, "", "  ")
	if err != nil {
		return "", err
	}
	dir := resolvedMCPDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(src))
	dst := filepath.Join(dir, hex.EncodeToString(sum[:8])+".json")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("write resolved mcp config: %w", err)
	}
	return dst, nil
}

// findMCPServer returns the entry named name from the first of sources that
// has one, expanded from the environment, and the file it came from. ok is
// false when no file names it.
func findMCPServer(sources []string, name string, lookup func(string) (string, bool)) (entry map[string]any, missing []string, from string, ok bool) {
	for _, src := range sources {
		raw, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		var f struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if e, found := f.MCPServers[name]; found {
			resolved, miss := expandMCPEntry(e, lookup)
			return resolved, miss, src, true
		}
	}
	return nil, nil, "", false
}
