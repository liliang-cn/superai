package backend

// A worker's own kit: the MCP servers and skills it has, told apart from
// what every worker has anyway (the cluster's servers, the queen's skills and
// servers, hive_abilities.go). Its card in the app shows the kit and adds to
// it or takes from it; its hello names it, so the queen sends work that needs
// a server or a skill to a worker that has it.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KitItem is one MCP server or skill a worker has.
type KitItem struct {
	Name  string   `json:"name"`
	About string   `json:"about,omitempty"`
	OK    bool     `json:"ok"`
	Tools []string `json:"tools,omitempty"`
	// Own is the worker's own: added on it, and removable there. Not own is
	// the cluster's or the queen's, which every worker has.
	Own bool `json:"own"`
}

// WorkerKit is a worker's MCP servers and skills.
type WorkerKit struct {
	Worker string    `json:"worker"`
	MCP    []KitItem `json:"mcp"`
	Skills []KitItem `json:"skills"`
}

// OwnMCPServers names the servers in this instance's own mcpServers.json:
// what was added here, not what the cluster or the queen hands out.
func OwnMCPServers() map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(mcpConfigPath())
	if err != nil {
		return out
	}
	var f mcpServersFile
	if json.Unmarshal(raw, &f) == nil {
		for name := range f.MCPServers {
			out[name] = true
		}
	}
	return out
}

// SkillFolders is every skill this instance has, by its folder's name, with
// the first line of its description; Own is one not from the queen.
func SkillFolders() []KitItem {
	root := filepath.Join(DataDir(), "skills")
	entries, _ := os.ReadDir(root)
	var out []KitItem
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		md := filepath.Join(dir, "SKILL.md")
		if _, err := os.Stat(md); err != nil {
			continue
		}
		_, queen := os.Stat(filepath.Join(dir, fromQueenMark))
		out = append(out, KitItem{Name: e.Name(), About: skillDescription(md), OK: true, Own: queen != nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// skillDescription is the description: line of a SKILL.md's front matter.
func skillDescription(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan() && n < 40; n++ {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "description:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
