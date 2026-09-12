package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// registerInstallTools exposes chat-callable tools that let the user install
// skills and MCP servers just by asking SuperAI in conversation:
//
//	"装一个 filesystem 的 MCP server"          -> add_mcp_server
//	"给我装上 github.com/foo/bar-skill"        -> install_skill
func (s *Service) registerInstallTools() {
	svc := s.svc
	if svc == nil {
		return
	}
	destMeta := agent.ToolMetadata{Destructive: true, InterruptBehavior: agent.InterruptBehaviorBlock}

	// --- search_mcp_servers ---
	// Searching is read-only, so it carries no destructive metadata: the agent
	// may look for a capability on its own and only needs approval to install.
	svc.AddTool(
		"search_mcp_servers",
		"Search the official registry for installable MCP servers. Call it first whenever the task needs a capability you do not have, then hand the command/args from the result to add_mcp_server verbatim to install it. The required_env in a result lists the environment variables that server needs; ask the user for any you are missing before installing.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Capability keyword, e.g. filesystem / postgres / slack"},
				"limit": map[string]interface{}{"type": "integer", "description": "How many results at most; defaults to 10"},
			},
			"required": []string{"query"},
		},
		func(ctx context.Context, a map[string]interface{}) (interface{}, error) {
			candidates, err := SearchMCPServers(ctx, argStr(a, "query"), argInt(a, "limit"))
			if err != nil {
				return errResult(err.Error()), nil
			}
			return okData(map[string]interface{}{"servers": candidates, "count": len(candidates)}), nil
		},
	)

	// --- search_skills ---
	svc.AddTool(
		"search_skills",
		"Search this machine for installable skills (SKILL.md format, including ~/.claude/skills). Search first whenever the task needs specialist knowledge or a set procedure, then install with install_skill's source_path. installed=true means it is already installed.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Keyword, e.g. go / investing / frontend; leave blank to list everything"},
				"limit": map[string]interface{}{"type": "integer", "description": "How many results at most; defaults to 20"},
			},
		},
		func(_ context.Context, a map[string]interface{}) (interface{}, error) {
			candidates := SearchSkills(argStr(a, "query"), argInt(a, "limit"))
			return okData(map[string]interface{}{"skills": candidates, "count": len(candidates)}), nil
		},
	)

	// --- add_mcp_server ---
	svc.AddToolWithMetadata(
		"add_mcp_server",
		"Install/add an MCP server (call it when the user asks in conversation for some MCP tool). It starts immediately over stdio and is written to ~/.superai-desktop/mcpServers.json, so it survives a restart.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":    map[string]interface{}{"type": "string", "description": "Server name, e.g. filesystem"},
				"command": map[string]interface{}{"type": "string", "description": "Command to launch it, e.g. npx"},
				"args":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Command arguments, e.g. ['-y','@modelcontextprotocol/server-filesystem','/path']"},
				"env":     map[string]interface{}{"type": "object", "description": "Environment variables (optional)"},
			},
			"required": []string{"name", "command"},
		},
		func(ctx context.Context, a map[string]interface{}) (interface{}, error) {
			name := sanitizeName(argStr(a, "name"))
			command := strings.TrimSpace(argStr(a, "command"))
			if name == "" || command == "" {
				return errResult("name and command are required"), nil
			}
			args := argStrSlice(a, "args")
			env := argStrMap(a, "env")
			started, note := true, "Started and written to the config."
			if err := s.InstallMCPServer(ctx, name, command, args, env); err != nil {
				started, note = false, err.Error()
				if !strings.Contains(err.Error(), "did not start") {
					return errResult(err.Error()), nil
				}
			}
			tools := 0
			if svc.MCP != nil {
				for _, srv := range svc.MCP.ListServers() {
					if srv.Name == name {
						tools = srv.ToolCount
					}
				}
			}
			return okData(map[string]interface{}{"server": name, "started": started, "tools": tools, "note": note}), nil
		},
		destMeta,
	)

	// --- install_skill ---
	svc.AddToolWithMetadata(
		"install_skill",
		"Install a skill from one of three sources: a local path (source_path, paired with search_skills), a git repository (git_url), or SKILL.md content given directly (skill_md). It is installed into ~/.superai-desktop/skills/<name> and hot-reloaded; once installed it shows up in the Skills panel.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "description": "Skill name (the directory name)"},
				"git_url":     map[string]interface{}{"type": "string", "description": "Git repository URL (optional; either this or skill_md)"},
				"skill_md":    map[string]interface{}{"type": "string", "description": "SKILL.md content given directly (optional)"},
				"source_path": map[string]interface{}{"type": "string", "description": "A skill directory on this machine (optional); use the path returned by search_skills"},
			},
			"required": []string{"name"},
		},
		func(ctx context.Context, a map[string]interface{}) (interface{}, error) {
			name := sanitizeName(argStr(a, "name"))
			if name == "" {
				return errResult("name is required"), nil
			}
			dst := filepath.Join(DataDir(), "skills", name)
			gitURL := strings.TrimSpace(argStr(a, "git_url"))
			skillMD := argStr(a, "skill_md")
			sourcePath := strings.TrimSpace(argStr(a, "source_path"))
			switch {
			case sourcePath != "":
				if err := copySkillDir(sourcePath, dst); err != nil {
					return errResult("copy skill: " + err.Error()), nil
				}
			case gitURL != "":
				_ = os.RemoveAll(dst)
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					return errResult(err.Error()), nil
				}
				out, err := exec.CommandContext(ctx, "git", "clone", "--depth", "1", gitURL, dst).CombinedOutput()
				if err != nil {
					return errResult("git clone failed: " + strings.TrimSpace(string(out))), nil
				}
			case strings.TrimSpace(skillMD) != "":
				if err := os.MkdirAll(dst, 0o755); err != nil {
					return errResult(err.Error()), nil
				}
				if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
					return errResult(err.Error()), nil
				}
			default:
				return errResult("provide source_path, git_url or skill_md"), nil
			}
			if svc.Skills != nil {
				_ = svc.Skills.LoadAll(ctx)
			}
			return okData(map[string]interface{}{"skill": name, "path": dst, "installed": svc.HasSkill(name)}), nil
		},
		destMeta,
	)
}

// ---- helpers ----

func errResult(msg string) map[string]interface{} {
	return map[string]interface{}{"ok": false, "error": msg}
}

func argStr(a map[string]interface{}, k string) string {
	if v, ok := a[k].(string); ok {
		return v
	}
	return ""
}

// argInt reads a numeric argument. JSON tool arguments arrive as float64, and
// some models send the number as a string.
func argInt(a map[string]interface{}, k string) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// copySkillDir copies a skill directory into the SuperAI skills folder,
// replacing whatever was there. Only regular files are copied: a skill found on
// this machine may be a git checkout, and its .git is of no use to the copy.
func copySkillDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		return fmt.Errorf("%s has no SKILL.md", src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// argBool reads a boolean argument that arrived as JSON, where a model may
// have sent the word rather than the value.
func argBool(a map[string]interface{}, k string) bool {
	switch v := a[k].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

func argStrSlice(a map[string]interface{}, k string) []string {
	switch v := a[k].(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, fmt.Sprintf("%v", e))
		}
		return out
	}
	return nil
}

func argStrMap(a map[string]interface{}, k string) map[string]string {
	m, ok := a[k].(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for kk, vv := range m {
		out[kk] = fmt.Sprintf("%v", vv)
	}
	return out
}

var nameSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "-")
	return nameSanitizer.ReplaceAllString(s, "")
}

// upsertMCPServer merges one server into the Claude-style mcpServers.json
// ({"mcpServers": {name: {command, args, env, type}}}).
func upsertMCPServer(path, name, command string, args []string, env map[string]string) error {
	type entry struct {
		Type    string            `json:"type,omitempty"`
		Command string            `json:"command,omitempty"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
	}
	file := struct {
		MCPServers map[string]entry `json:"mcpServers"`
	}{MCPServers: map[string]entry{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &file)
		if file.MCPServers == nil {
			file.MCPServers = map[string]entry{}
		}
	}
	file.MCPServers[name] = entry{Type: "stdio", Command: command, Args: args, Env: env}
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
