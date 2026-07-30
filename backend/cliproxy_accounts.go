package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CLIProxyAccount is one signed-in provider credential. Tokens are deliberately
// never included — only what the UI needs to identify and manage the account.
type CLIProxyAccount struct {
	File     string `json:"file"`
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Project  string `json:"project"`
	Expires  string `json:"expires"`
	Disabled bool   `json:"disabled"`
}

// Accounts lists the credentials currently in the proxy's auth dir.
func (p *CLIProxy) Accounts() ([]CLIProxyAccount, error) {
	if p == nil {
		return nil, fmt.Errorf("cliproxy not running")
	}
	entries, err := os.ReadDir(p.AuthDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []CLIProxyAccount{}, nil
		}
		return nil, err
	}

	out := make([]CLIProxyAccount, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		acc := CLIProxyAccount{File: name}

		raw, rerr := os.ReadFile(filepath.Join(p.AuthDir(), name))
		if rerr == nil {
			var fields struct {
				Type      string `json:"type"`
				Email     string `json:"email"`
				ProjectID string `json:"project_id"`
				Expired   string `json:"expired"`
				Disabled  bool   `json:"disabled"`
			}
			if json.Unmarshal(raw, &fields) == nil {
				acc.Provider = fields.Type
				acc.Account = fields.Email
				acc.Project = fields.ProjectID
				acc.Expires = fields.Expired
				acc.Disabled = fields.Disabled
			}
		}
		if acc.Provider == "" {
			// Fall back to the filename convention, e.g. "codex-me@x.com.json".
			if i := strings.Index(name, "-"); i > 0 {
				acc.Provider = name[:i]
			}
		}
		if acc.Account == "" {
			acc.Account = strings.TrimSuffix(name, ".json")
		}
		out = append(out, acc)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Account < out[j].Account
	})
	return out, nil
}

// SetAccountDisabled flips a credential's disabled flag in place, keeping every
// other field untouched. The proxy's watcher reloads the file, so the account's
// models leave or rejoin the catalog without a restart.
func (p *CLIProxy) SetAccountDisabled(file string, disabled bool) error {
	path, err := p.authFilePath(file)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", file, err)
	}
	doc["disabled"] = disabled

	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0o600)
}

// RemoveAccount deletes a credential — signing that provider account out.
func (p *CLIProxy) RemoveAccount(file string) error {
	path, err := p.authFilePath(file)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// authFilePath resolves a UI-supplied file name inside the auth dir, refusing
// anything that tries to escape it.
func (p *CLIProxy) authFilePath(file string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("cliproxy not running")
	}
	file = strings.TrimSpace(file)
	if file == "" || file != filepath.Base(file) || !strings.HasSuffix(file, ".json") {
		return "", fmt.Errorf("invalid credential file %q", file)
	}
	return filepath.Join(p.AuthDir(), file), nil
}
