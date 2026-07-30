package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// WithEnv, not the plain variant: most servers need a credential, and one
	// started without its env is up but unable to do anything.
	if err := s.svc.MCP.AddDynamicServerWithEnv(ctx, name, command, args, env); err != nil {
		return fmt.Errorf("saved, but it did not start: %w", err)
	}
	return nil
}

// RemoveMCPServer drops a server from the persisted config. The running process
// stays until the next launch — MCP has no detach — which the caller should say.
func RemoveMCPServer(name string) error {
	name = sanitizeName(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	path := mcpConfigPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cfg struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if _, ok := cfg.Servers[name]; !ok {
		return nil
	}
	delete(cfg.Servers, name)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// InstallSkillFromPath copies a skill directory found on this machine into
// SuperAI's skills folder and loads it immediately.
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

// InstalledSkillDetails lists the skills SuperAI has, with where they came from.
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
