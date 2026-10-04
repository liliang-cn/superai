package backend

// One hive, one set of abilities.
//
// What the person installs on the queen — skills, MCP servers — is what the
// hive can do, and a job the queen splits up lands on workers that would
// otherwise not have it. So a worker takes the queen's skills and MCP servers
// as its own: it asks for them when it starts and every minute after, writes
// what changed, and drops what the queen no longer has. Its own installs are
// left alone; only what came from the queen is marked as such.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// HiveAbilities is what the queen hands its workers.
type HiveAbilities struct {
	// Skills by name: each file of the skill's folder, by relative path,
	// base64-encoded.
	Skills map[string]map[string]string `json:"skills"`
	// MCP is the queen's own mcpServers entries (not what the cluster gives
	// every member anyway).
	MCP  map[string]any `json:"mcp"`
	Hash string         `json:"hash"`
}

// fromQueenMark marks a skill folder as the queen's, so a skill the worker
// installed itself is never removed by a sync.
const fromQueenMark = ".from-queen"

// HiveMCPFile is where a worker keeps the queen's MCP servers, beside its own.
func HiveMCPFile() string { return filepath.Join(DataDir(), "hive-mcpServers.json") }

const maxSkillBytes = 4 << 20

// CollectAbilities reads this instance's skills and its user MCP servers.
func CollectAbilities() HiveAbilities {
	out := HiveAbilities{Skills: map[string]map[string]string{}, MCP: map[string]any{}}
	root := filepath.Join(DataDir(), "skills")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		files := map[string]string{}
		total := 0
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() == fromQueenMark {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil || total+len(raw) > maxSkillBytes {
				return nil
			}
			total += len(raw)
			rel, _ := filepath.Rel(dir, p)
			files[filepath.ToSlash(rel)] = base64.StdEncoding.EncodeToString(raw)
			return nil
		})
		if _, ok := files["SKILL.md"]; ok {
			out.Skills[e.Name()] = files
		}
	}
	if raw, err := os.ReadFile(mcpConfigPath()); err == nil {
		var f mcpServersFile
		if json.Unmarshal(raw, &f) == nil {
			for name, entry := range f.MCPServers {
				out.MCP[name] = map[string]any(entry)
			}
		}
	}
	out.Hash = abilitiesHash(out)
	return out
}

func abilitiesHash(a HiveAbilities) string {
	names := make([]string, 0, len(a.Skills))
	for n := range a.Skills {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		files := a.Skills[n]
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fmt.Fprintf(h, "%s/%s=%s\n", n, p, files[p])
		}
	}
	mcp, _ := json.Marshal(a.MCP) // map keys marshal sorted
	h.Write(mcp)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// AppliedAbilities says what a sync changed on this worker.
type AppliedAbilities struct {
	Skills        []string `json:"skills"`
	SkillsChanged bool     `json:"skillsChanged"`
	MCP           []string `json:"mcp"`
	MCPChanged    bool     `json:"mcpChanged"`
	// Skipped are MCP servers this worker cannot start: the program they run
	// is not installed here.
	Skipped map[string]string `json:"skipped,omitempty"`
}

// ApplyAbilities makes this worker's copy of the queen's abilities match ab.
// lookPath finds a program (exec.LookPath outside tests).
func ApplyAbilities(ab HiveAbilities, lookPath func(string) (string, error)) (AppliedAbilities, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	out := AppliedAbilities{Skipped: map[string]string{}}
	root := filepath.Join(DataDir(), "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return out, err
	}
	// Skills: write each, and remove the queen's that she no longer has.
	for name, files := range ab.Skills {
		safe := sanitizeName(name)
		if safe == "" {
			continue
		}
		dir := filepath.Join(root, safe)
		if _, err := os.Stat(dir); err == nil {
			if _, mine := os.Stat(filepath.Join(dir, fromQueenMark)); mine != nil {
				continue // the worker's own skill of the same name wins
			}
		}
		changed, err := writeSkill(dir, files)
		if err != nil {
			return out, fmt.Errorf("skill %s: %w", name, err)
		}
		out.SkillsChanged = out.SkillsChanged || changed
		out.Skills = append(out.Skills, safe)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if _, keep := ab.Skills[e.Name()]; keep || !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), fromQueenMark)); err == nil {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
			out.SkillsChanged = true
		}
	}
	sort.Strings(out.Skills)

	// MCP: the servers this worker can run, in a file of their own.
	servers := map[string]mcpServerEntry{}
	for name, v := range ab.MCP {
		entry, _ := v.(map[string]any)
		if entry == nil {
			continue
		}
		if cmd := mcpCommandOf(entry); cmd != "" {
			if _, err := lookPath(cmd); err != nil {
				out.Skipped[name] = cmd + " is not installed here"
				continue
			}
		}
		servers[name] = entry
		out.MCP = append(out.MCP, name)
	}
	sort.Strings(out.MCP)
	raw, _ := json.MarshalIndent(mcpServersFile{MCPServers: servers}, "", "  ")
	prev, _ := os.ReadFile(HiveMCPFile())
	if !bytes.Equal(prev, raw) {
		if len(servers) == 0 && len(prev) == 0 {
			// nothing before, nothing now
		} else if err := os.WriteFile(HiveMCPFile(), raw, 0o600); err != nil {
			return out, err
		} else {
			out.MCPChanged = true
		}
	}
	return out, nil
}

// writeSkill makes dir hold exactly files, marked as the queen's. It reports
// whether anything changed.
func writeSkill(dir string, files map[string]string) (bool, error) {
	want := map[string][]byte{}
	for rel, b64 := range files {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return false, err
		}
		clean := filepath.Clean(filepath.FromSlash(rel))
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		want[clean] = raw
	}
	changed := false
	for rel, raw := range want {
		p := filepath.Join(dir, rel)
		if cur, err := os.ReadFile(p); err == nil && bytes.Equal(cur, raw) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return changed, err
		}
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			return changed, err
		}
		changed = true
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		if _, ok := want[rel]; !ok && rel != fromQueenMark {
			_ = os.Remove(p)
			changed = true
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(dir, fromQueenMark)); err != nil {
		_ = os.WriteFile(filepath.Join(dir, fromQueenMark), []byte("installed from the queen; removed when she removes it\n"), 0o644)
		changed = true
	}
	return changed, nil
}

// mcpCommandOf is the program a stdio MCP entry starts, or "" for a remote one.
func mcpCommandOf(entry map[string]any) string {
	switch c := entry["command"].(type) {
	case string:
		return strings.Fields(c + " ")[0]
	case []any:
		if len(c) > 0 {
			return fmt.Sprint(c[0])
		}
	}
	return ""
}

// Abilities asks the queen for hers.
func (a *Announcer) Abilities(ctx context.Context) (HiveAbilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.Settings.JoinURL, "/")+"/api/hive/abilities", nil)
	if err != nil {
		return HiveAbilities{}, err
	}
	tok := a.Settings.JoinToken
	if tok == "" {
		tok = a.Token
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	client := a.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return HiveAbilities{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return HiveAbilities{}, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var ab HiveAbilities
	err = json.Unmarshal(raw, &ab)
	return ab, err
}
