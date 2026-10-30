package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Install and removal, shared by the chat tools and the Skills/MCP panels.
//
// The agent installs a capability by calling a tool; a person installs the same
// capability by clicking a button. Both go through here so the two can never
// disagree about where a skill lands or what starting a server means.

// InstallMCPServer persists a server and starts it in this session.
func (s *Service) InstallMCPServer(ctx context.Context, name, command string, args []string, env map[string]string) error {
	name = sanitizeName(name)
	if name == "" || command == "" {
		return fmt.Errorf("name and command are required")
	}
	if err := upsertMCPServer(mcpConfigPath(), name, command, args, env); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	if s == nil || s.svc == nil || s.svc.MCP == nil {
		return nil
	}
	// A start failure is not an install failure: the config is written, so the
	// server comes back on the next launch.
	// With its env: most servers need a credential, and one started without
	// it is up but unable to do anything. AddMCPServer also indexes its tools:
	// started without that, the server ran and the model could not find a
	// single tool of it until a restart.
	if err := s.svc.AddMCPServer(ctx, name, command, args, env); err != nil {
		return fmt.Errorf("saved, but it did not start: %w", err)
	}
	s.refreshEagerTools()
	return nil
}

// RemoveMCPServer drops a server from every config this SuperLeo keeps. One
// that only the hive's queen hands out (hive_abilities.go) cannot be removed
// here, and says so. The running process is UninstallMCPServer's.
func RemoveMCPServer(name string) error {
	name = sanitizeName(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	found := false
	for _, path := range []string{mcpConfigPath(), filepath.Join(DataDir(), "data", "mcpServers.json")} {
		ok, err := removeMCPServerFrom(path, name)
		if err != nil {
			return err
		}
		found = found || ok
	}
	if !found {
		if ok, _ := hasMCPServer(HiveMCPFile(), name); ok {
			return fmt.Errorf("%s comes from the hive's queen; remove it there", name)
		}
	}
	return nil
}

func readMCPConfig(path string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	servers := map[string]json.RawMessage{}
	if s, ok := top["mcpServers"]; ok {
		if err := json.Unmarshal(s, &servers); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
		}
	}
	return top, servers, nil
}

func hasMCPServer(path, name string) (bool, error) {
	_, servers, err := readMCPConfig(path)
	if err != nil {
		return false, err
	}
	_, ok := servers[name]
	return ok, nil
}

// removeMCPServerFrom deletes one entry and keeps everything else in the file.
func removeMCPServerFrom(path, name string) (bool, error) {
	top, servers, err := readMCPConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if _, ok := servers[name]; !ok {
		return false, nil
	}
	delete(servers, name)
	b, err := json.Marshal(servers)
	if err != nil {
		return false, err
	}
	top["mcpServers"] = b
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return false, err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return true, os.WriteFile(path, out, mode)
}

// InstallSkillFromPath copies a skill directory found on this machine into
// SuperLeo's skills folder and loads it immediately.
func (s *Service) InstallSkillFromPath(ctx context.Context, name, sourcePath string) error {
	name = sanitizeName(name)
	if name == "" || sourcePath == "" {
		return fmt.Errorf("name and source path are required")
	}
	if err := copySkillDir(sourcePath, filepath.Join(DataDir(), "skills", name)); err != nil {
		return err
	}
	s.ReloadSkills(ctx)
	return nil
}

// RemoveSkill deletes an installed skill.
func RemoveSkill(name string) error {
	name = sanitizeName(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	dir := filepath.Join(DataDir(), "skills", name)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(dir)
}

// ReloadSkills re-reads the skills folder so a change shows up without a restart.
func (s *Service) ReloadSkills(ctx context.Context) {
	if s == nil || s.svc == nil || s.svc.Skills == nil {
		return
	}
	_ = s.svc.Skills.LoadAll(ctx)
}

// InstalledSkillDetails lists the skills SuperLeo has, with where they came from.
func InstalledSkillNames() []string {
	entries, err := os.ReadDir(filepath.Join(DataDir(), "skills"))
	if err != nil {
		return []string{}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

func mcpConfigPath() string { return filepath.Join(DataDir(), "mcpServers.json") }

// UninstallSkill removes a skill and unloads it: gone from the folder and from
// the running agent, without a restart.
func (s *Service) UninstallSkill(ctx context.Context, name string) error {
	if err := RemoveSkill(name); err != nil {
		return err
	}
	s.ReloadSkills(ctx)
	return nil
}

// UninstallMCPServer removes a server from the config, stops it, and takes
// its tools away from the running agent.
func (s *Service) UninstallMCPServer(name string) error {
	if err := RemoveMCPServer(name); err != nil {
		return err
	}
	if s == nil || s.svc == nil {
		return nil
	}
	// agent-go keeps a loaded server in its list until the next build; this
	// service leaves it out from now on (MCPServers).
	s.removedMCP.Store(sanitizeName(name), true)
	s.refreshEagerTools()
	if s.svc.MCP != nil {
		_ = s.svc.MCP.StopServer(sanitizeName(name))
	}
	reg := s.svc.GetToolRegistry()
	prefix := "mcp_" + sanitizeName(name) + "_"
	for _, t := range reg.Names() {
		if strings.HasPrefix(t, prefix) {
			reg.Unregister(t)
		}
	}
	return nil
}
