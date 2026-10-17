package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentLinkSettings is how core treats the agents that dial in over the agent
// link (proto/agentlink/v1). The zero value treats every one as a machine.
type AgentLinkSettings struct {
	// RelayOnly names agents (as linked_agents shows them) that are relays,
	// not machines: their named agents (openclaw, hermes, ...) and CLIs stay
	// routable, but they are left out of the machine lists, the 3D view's
	// machines and the machine_inspect / machine_act targets.
	RelayOnly []string `json:"relay_only,omitempty"`
}

// IsRelay reports whether the agent called name is one of RelayOnly.
func (s AgentLinkSettings) IsRelay(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, r := range s.RelayOnly {
		if strings.EqualFold(strings.TrimSpace(r), name) {
			return true
		}
	}
	return false
}

// normalize trims the names, drops blanks and repeats, and leaves nil for none.
func (s *AgentLinkSettings) normalize() {
	var out []string
	seen := map[string]bool{}
	for _, r := range s.RelayOnly {
		r = strings.TrimSpace(r)
		if r == "" || seen[strings.ToLower(r)] {
			continue
		}
		seen[strings.ToLower(r)] = true
		out = append(out, r)
	}
	s.RelayOnly = out
}

// agentLinkPath is agentlink.json: the agent link settings set from a client.
//
// A file of its own for the reason queen.json is one: in the hive's containers
// settings.json is rewritten from a Secret on every start, and a relay named
// through the settings RPC would be a machine again after the next restart.
func agentLinkPath() string { return filepath.Join(DataDir(), "agentlink.json") }

// mergeAgentLink lays agentlink.json over what settings.json said. The file
// exists only once the settings were saved with something in them, and that
// is the later word.
func (s *Settings) mergeAgentLink() {
	raw, err := os.ReadFile(agentLinkPath())
	if err != nil {
		return
	}
	var al AgentLinkSettings
	if json.Unmarshal(raw, &al) != nil {
		return
	}
	al.normalize()
	s.AgentLink = al
}

// saveAgentLink writes agentlink.json when there is something to keep and
// removes it when there is not, so an empty list chosen here does not shadow
// one an operator later puts in the Secret.
func (s *Settings) saveAgentLink() error {
	if len(s.AgentLink.RelayOnly) == 0 {
		if err := os.Remove(agentLinkPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := json.MarshalIndent(s.AgentLink, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(agentLinkPath(), raw, 0o644)
}

// toNames reads a list of names from what a model or a client sent: a JSON
// array, or one string with the names separated by commas or spaces.
func toNames(v interface{}) ([]string, error) {
	var out []string
	switch x := v.(type) {
	case nil:
	case []string:
		out = append(out, x...)
	case []interface{}:
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("a list of names, not %v", e)
			}
			out = append(out, s)
		}
	case string:
		if t := strings.TrimSpace(x); strings.HasPrefix(t, "[") {
			if err := json.Unmarshal([]byte(t), &out); err != nil {
				return nil, fmt.Errorf("a list of names: %v", err)
			}
			break
		}
		out = strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	default:
		return nil, fmt.Errorf("a list of names, not %v", v)
	}
	al := AgentLinkSettings{RelayOnly: out}
	al.normalize()
	return al.RelayOnly, nil
}
