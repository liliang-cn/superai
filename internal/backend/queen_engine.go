package backend

import (
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
)

// QueenEngineServers are this instance's own MCP servers that an agent CLI
// thinking for the queen is handed too, when they are configured here: the
// person's data (noted) and the shared brain (cortexdb). The rest stay
// SuperAI's.
var QueenEngineServers = []string{NotedServer, "cortexdb"}

// EngineMCPServer is one of this instance's MCP servers as an agent CLI is
// handed it, in the shape Claude's --mcp-config reads. The entry keeps its
// ${NAME} references, which Claude fills in itself, and env carries their
// values: a secret then travels in the CLI's environment, never on its
// command line where any process listing shows it.
//
// ok is false when it is not configured here, needs a variable that is not
// set, or — with remote, for a CLI on another machine — is a command (it
// would run there, not here) or a URL only this machine can reach.
func EngineMCPServer(name string, remote bool) (entry map[string]any, env map[string]string, ok bool) {
	raw, found := rawMCPServer(MCPConfigSources(), name)
	if !found {
		return nil, nil, false
	}
	entry = make(map[string]any, len(raw))
	for k, v := range raw {
		entry[k] = v
	}
	u, _ := entry["url"].(string)
	switch t, _ := entry["type"].(string); {
	case u != "":
		// agent-go also reads streamable-http; Claude knows it as http.
		if t == "" || strings.Contains(t, "streamable") {
			entry["type"] = "http"
		}
		if resolved, _ := expandMCPEnv(u, os.LookupEnv); remote && loopbackURL(resolved) {
			return nil, nil, false
		}
	case remote:
		return nil, nil, false
	}
	body, _ := json.Marshal(entry)
	env = map[string]string{}
	for _, m := range mcpEnvRef.FindAllStringSubmatch(string(body), -1) {
		v, set := os.LookupEnv(m[1])
		if !set || v == "" {
			return nil, nil, false
		}
		// The other machine's own HOME or PATH is the one its CLI needs.
		if remote && slices.Contains([]string{"HOME", "PATH", "USER", "SHELL", "TMPDIR", "PWD"}, m[1]) {
			return nil, nil, false
		}
		env[m[1]] = v
	}
	return entry, env, true
}

// rawMCPServer is the first entry called name in sources, as written.
func rawMCPServer(sources []string, name string) (map[string]any, bool) {
	for _, src := range sources {
		b, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		var f struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		}
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		if e, ok := f.MCPServers[name]; ok {
			return e, true
		}
	}
	return nil, false
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	h := strings.ToLower(u.Hostname())
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.")
}

// MCPEnvRefs are the variables a piece of MCP config names as ${NAME}.
func MCPEnvRefs(s string) []string {
	var out []string
	for _, m := range mcpEnvRef.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
