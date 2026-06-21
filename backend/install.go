package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liliang-cn/agent-go/v2/pkg/agent"
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

	// --- add_mcp_server ---
	svc.AddToolWithMetadata(
		"add_mcp_server",
		"安装/添加一个 MCP server(用户在对话里说想加某个 MCP 工具时调用)。会立即启动(stdio)并写入 ~/.superai-desktop/mcpServers.json,重启后仍在。",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":    map[string]interface{}{"type": "string", "description": "server 名称,如 filesystem"},
				"command": map[string]interface{}{"type": "string", "description": "启动命令,如 npx"},
				"args":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "命令参数,如 ['-y','@modelcontextprotocol/server-filesystem','/path']"},
				"env":     map[string]interface{}{"type": "object", "description": "环境变量(可选)"},
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
			if err := upsertMCPServer(filepath.Join(DataDir(), "mcpServers.json"), name, command, args, env); err != nil {
				return errResult("persist config: " + err.Error()), nil
			}
			started, note := false, "已写入配置,重启 SuperAI 后生效。"
			if svc.MCP != nil {
				if err := svc.MCP.AddDynamicServer(ctx, name, command, args); err == nil {
					started, note = true, "已启动并写入配置。"
				} else {
					note = "已写入配置,但本次启动失败(" + err.Error() + "),重启后重试。"
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
		"安装一个 skill(用户在对话里说想装某 skill 时调用)。支持 git 仓库 URL(git_url),或直接给 SKILL.md 内容(skill_md)创建。装到 ~/.superai-desktop/skills/<name> 并热重载;装好后在 Skills 面板可见。",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":     map[string]interface{}{"type": "string", "description": "skill 名称(目录名)"},
				"git_url":  map[string]interface{}{"type": "string", "description": "git 仓库地址(可选,与 skill_md 二选一)"},
				"skill_md": map[string]interface{}{"type": "string", "description": "直接提供的 SKILL.md 内容(可选)"},
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
			switch {
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
				return errResult("provide git_url or skill_md"), nil
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
