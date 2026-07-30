package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Capability discovery.
//
// The install tools can add an MCP server or a skill, but only if the caller
// already knows the package name and how to launch it. An agent that hits a
// task it cannot do has no way to find that out, so it either gives up or
// invents an npx incantation.
//
// These searches close that gap. Their results are shaped to be handed straight
// to add_mcp_server / install_skill: the command and arguments are already
// assembled, so the model never has to guess at runtime flags.

const mcpRegistryURL = "https://registry.modelcontextprotocol.io/v0/servers"

// MCPCandidate is an installable MCP server found in the registry.
type MCPCandidate struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
	// Command and Args are ready for add_mcp_server as-is.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// RequiredEnv names variables the server refuses to start without, so the
	// agent can ask the user for them instead of failing at launch.
	RequiredEnv []string `json:"required_env,omitempty"`
	// RemoteURL is set for servers that are hosted rather than run locally.
	RemoteURL string `json:"remote_url,omitempty"`
}

// registryArg is one argument the registry says a runtime or package takes.
type registryArg struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// argValues flattens the registry's argument objects to the strings a shell needs.
func argValues(args []registryArg) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if v := strings.TrimSpace(a.Value); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// registryResponse mirrors the fields of the MCP registry API we rely on.
type registryResponse struct {
	Servers []struct {
		Server struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Version     string `json:"version"`
			Packages    []struct {
				RegistryType         string        `json:"registryType"`
				Identifier           string        `json:"identifier"`
				Version              string        `json:"version"`
				RuntimeHint          string        `json:"runtimeHint"`
				RuntimeArguments     []registryArg `json:"runtimeArguments"`
				PackageArguments     []registryArg `json:"packageArguments"`
				EnvironmentVariables []struct {
					Name       string `json:"name"`
					IsRequired bool   `json:"isRequired"`
				} `json:"environmentVariables"`
			} `json:"packages"`
			Remotes []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"remotes"`
		} `json:"server"`
		Meta struct {
			Official struct {
				IsLatest bool `json:"isLatest"`
			} `json:"io.modelcontextprotocol.registry/official"`
		} `json:"_meta"`
	} `json:"servers"`
}

// SearchMCPServers queries the official MCP registry and returns candidates
// that are ready to install.
func SearchMCPServers(ctx context.Context, query string, limit int) ([]MCPCandidate, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	// Ask for more than requested: the registry returns one entry per published
	// version, so several rows can collapse into a single server.
	q := url.Values{}
	if s := strings.TrimSpace(query); s != "" {
		q.Set("search", s)
	}
	q.Set("limit", fmt.Sprint(limit*5))

	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, mcpRegistryURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp registry unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("mcp registry: status %d", resp.StatusCode)
	}

	var body registryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}

	seen := map[string]int{} // server name -> index in out
	out := make([]MCPCandidate, 0, limit)
	for _, entry := range body.Servers {
		srv := entry.Server
		if strings.TrimSpace(srv.Name) == "" {
			continue
		}
		candidate := MCPCandidate{
			Name:        srv.Name,
			Title:       srv.Title,
			Description: srv.Description,
			Version:     srv.Version,
		}
		if len(srv.Packages) > 0 {
			pkg := srv.Packages[0]
			candidate.Command, candidate.Args = launchCommand(
				pkg.RuntimeHint, pkg.RegistryType, pkg.Identifier, pkg.Version,
				argValues(pkg.RuntimeArguments), argValues(pkg.PackageArguments),
			)
			for _, env := range pkg.EnvironmentVariables {
				if env.IsRequired && env.Name != "" {
					candidate.RequiredEnv = append(candidate.RequiredEnv, env.Name)
				}
			}
		}
		for _, remote := range srv.Remotes {
			if remote.URL != "" {
				candidate.RemoteURL = remote.URL
				break
			}
		}
		// Nothing to run and nowhere to connect: not installable.
		if candidate.Command == "" && candidate.RemoteURL == "" {
			continue
		}
		// One row per server. A later entry marked latest replaces an earlier one.
		if idx, ok := seen[srv.Name]; ok {
			if entry.Meta.Official.IsLatest {
				out[idx] = candidate
			}
			continue
		}
		seen[srv.Name] = len(out)
		out = append(out, candidate)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// launchCommand turns a registry package into an executable command line.
//
// The registry describes how to run a server rather than giving a command: a
// runtime hint (npx, uvx, docker), the arguments that runtime needs, the
// package identifier, and finally the package's own arguments.
func launchCommand(runtimeHint, registryType, identifier, version string, runtimeArgs, pkgArgs []string) (string, []string) {
	if identifier == "" {
		return "", nil
	}
	command := strings.TrimSpace(runtimeHint)
	if command == "" {
		switch registryType {
		case "npm":
			command = "npx"
		case "pypi":
			command = "uvx"
		case "oci":
			command = "docker"
		default:
			return "", nil
		}
	}

	spec := identifier
	// Pinning the version keeps a server that worked today working tomorrow.
	if version != "" && (command == "npx" || command == "uvx") {
		spec = identifier + "@" + version
	}

	args := append([]string{}, runtimeArgs...)
	if command == "npx" && !containsString(args, "-y") {
		// npx prompts before installing a package it has never seen, which hangs
		// a stdio server that nobody is watching.
		args = append(args, "-y")
	}
	args = append(args, spec)
	args = append(args, pkgArgs...)
	return command, args
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// SkillCandidate is a skill available to install, found on this machine.
type SkillCandidate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Installed   bool   `json:"installed"`
}

// skillSearchDirs are where ready-made skills tend to live on a developer's
// machine. Claude Code keeps its own under ~/.claude, and those are the same
// SKILL.md format SuperAI loads.
func skillSearchDirs() []string {
	dirs := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".claude", "skills"),
			filepath.Join(home, ".claude", "plugins"),
		)
	}
	if extra := strings.TrimSpace(os.Getenv("SUPERAI_SKILL_PATH")); extra != "" {
		dirs = append(dirs, filepath.SplitList(extra)...)
	}
	return dirs
}

// SearchSkills finds installable skills whose name or description matches every
// word of the query. An empty query lists everything available.
func SearchSkills(query string, limit int) []SkillCandidate {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	installedDir := filepath.Join(DataDir(), "skills")
	terms := strings.Fields(strings.ToLower(query))

	found := map[string]SkillCandidate{}
	for _, dir := range skillSearchDirs() {
		for _, path := range findSkillFiles(dir) {
			name, description := readSkillMeta(path)
			if name == "" {
				name = filepath.Base(filepath.Dir(path))
			}
			if _, exists := found[name]; exists {
				continue
			}
			if !matchesAll(terms, name+" "+description) {
				continue
			}
			skillDir := filepath.Dir(path)
			_, err := os.Stat(filepath.Join(installedDir, name))
			found[name] = SkillCandidate{
				Name:        name,
				Description: description,
				Path:        skillDir,
				Installed:   err == nil,
			}
		}
	}

	out := make([]SkillCandidate, 0, len(found))
	for _, c := range found {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// findSkillFiles locates SKILL.md files a couple of levels down, which covers
// both <dir>/<skill>/SKILL.md and a plugin's <dir>/<plugin>/skills/<skill>/SKILL.md.
func findSkillFiles(root string) []string {
	var files []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if path := filepath.Join(dir, "SKILL.md"); fileExists(path) {
			files = append(files, path)
			continue
		}
		nested, err := os.ReadDir(filepath.Join(dir, "skills"))
		if err != nil {
			continue
		}
		for _, sub := range nested {
			if !sub.IsDir() {
				continue
			}
			if path := filepath.Join(dir, "skills", sub.Name(), "SKILL.md"); fileExists(path) {
				files = append(files, path)
			}
		}
	}
	return files
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// readSkillMeta pulls name and description out of a SKILL.md front matter block
// without pulling in a YAML parser: both are single-line scalars by convention.
func readSkillMeta(path string) (name, description string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---") {
		return "", ""
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		return "", ""
	}
	for _, line := range strings.Split(text[3:end+3], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

// matchesAll reports whether every term appears in the haystack.
func matchesAll(terms []string, haystack string) bool {
	if len(terms) == 0 {
		return true
	}
	haystack = strings.ToLower(haystack)
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}
