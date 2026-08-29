package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/cortexbridge"
	"github.com/liliang-cn/agent-go/v3/pkg/mcp"
	"github.com/liliang-cn/agent-go/v3/pkg/skills"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// SkillInfo is one installed skill shown in the Skills panel.
type SkillInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	WhenToUse   string `json:"when_to_use"`
	Collection  string `json:"collection"`
}

// SkillsDetailed returns the installed skills with descriptions for the Skills panel.
func (s *Service) SkillsDetailed() []SkillInfo {
	if s == nil || s.svc == nil || s.svc.Skills == nil {
		return nil
	}
	list, err := s.svc.Skills.ListSkills(context.Background(), skills.SkillFilter{})
	if err != nil {
		return nil
	}
	out := make([]SkillInfo, 0, len(list))
	for _, sk := range list {
		if sk == nil {
			continue
		}
		out = append(out, SkillInfo{
			ID: sk.ID, Name: sk.Name, Description: sk.Description,
			WhenToUse: sk.WhenToUse, Collection: sk.Collection,
		})
	}
	return out
}

// MCPServers returns the configured MCP servers (built-in + user) with status
// and tool counts for the MCP panel.
func (s *Service) MCPServers() []mcp.ServerStatus {
	if s == nil || s.svc == nil || s.svc.MCP == nil {
		return nil
	}
	return s.svc.MCP.ListServers()
}

// LifeData is the snapshot the Life panel renders (schedules / records / persons
// / reminders), read directly from the life-assistant store.
type LifeData struct {
	Schedules []map[string]any          `json:"schedules"`
	Records   []map[string]any          `json:"records"`
	Persons   map[string]map[string]any `json:"persons"`
	Reminders []map[string]any          `json:"reminders"`
}

// Life returns the current life-assistant data for the Life panel.
func (s *Service) Life() LifeData {
	if s == nil || s.store == nil {
		return LifeData{Persons: map[string]map[string]any{}}
	}
	s.store.mu.Lock()
	data := LifeData{
		Schedules: append([]map[string]any(nil), s.store.Schedules...),
		Records:   append([]map[string]any(nil), s.store.Records...),
		Persons:   s.store.Persons,
		Reminders: append([]map[string]any(nil), s.store.Reminders...),
	}
	s.store.mu.Unlock()
	// Outside the lock: reconciling asks the scheduler, and holding the store
	// while calling into it is how two locks become one deadlock. See
	// reminder_once.go for why the reminder rows need reconciling at all.
	data.Reminders = s.reconcileReminders(data.Reminders)
	return data
}

// MemoryRecall queries long-term memory and returns the formatted recall context
// (empty when memory isn't configured). Used by the Memory panel.
func (s *Service) MemoryRecall(ctx context.Context, query string) (string, error) {
	if s == nil || s.svc == nil || s.svc.Memory == nil {
		return "", nil
	}
	formatted, _, err := s.svc.Memory.RetrieveAndInject(ctx, query, "memory-panel")
	return formatted, err
}

// ImportableExtensions are the file types importflow has a parser for. Exported
// so the UI can say so up front rather than letting someone pick a file and
// find out on submit.
var ImportableExtensions = []string{".csv", ".tsv", ".txt", ".sql", ".dump"}

// importableExt reports whether this importer can read a file with this
// extension. Case-insensitive: a .CSV off a Windows machine is a CSV.
func importableExt(ext string) bool {
	ext = strings.ToLower(ext)
	for _, e := range ImportableExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

// ImportFile imports a data file into the CortexDB RAG store and knowledge
// graph through importflow, which infers the column-to-field mapping with the
// LLM. hint is an optional description of the dataset, which is what the mapper
// leans on when a column name is ambiguous on its own ("id", "name", "date").
// Returns a JSON report.
//
// The format is chosen by extension rather than by asking: the caller has a
// file, and which parser reads it is not a decision they should have to make.
// An unrecognised extension is refused by name — silently treating a .json as
// CSV would produce one column of garbage and an import that looks like it
// worked.
func (s *Service) ImportFile(ctx context.Context, path, hint string) (string, error) {
	if s == nil || s.cortex == nil {
		return "", fmt.Errorf("data import unavailable: CortexDB not configured")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(path))
	if !importableExt(ext) {
		return "", fmt.Errorf("%q is not a format this can read — use %s",
			ext, strings.Join(ImportableExtensions, ", "))
	}
	var src importflow.Source
	switch ext {
	case ".csv", ".tsv", ".txt":
		src, err = importflow.NewCSVSource(f, importflow.CSVOptions{})
		if err != nil {
			return "", fmt.Errorf("read csv: %w", err)
		}
	case ".sql", ".dump":
		// Dialect auto: the parser reads MySQL and PostgreSQL dumps and works
		// out which from the file, which is one fewer thing to get wrong than
		// asking someone to say.
		src, err = importflow.NewSQLDumpSource(f, importflow.DumpOptions{Dialect: importflow.DialectAuto})
		if err != nil {
			return "", fmt.Errorf("read sql dump: %w", err)
		}
	}

	im := cortexbridge.NewImporter(s.cortex, s.svc.LLM)
	report, err := im.AutoImport(ctx, src, importflow.Goal{BuildRAG: true, BuildKG: true, Hint: hint})
	if err != nil {
		return "", fmt.Errorf("import: %w", err)
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	return string(b), nil
}

// ImportCSV is the old name. Kept because it is a bound method the frontend
// bindings still carry, and forwarding costs a line.
func (s *Service) ImportCSV(ctx context.Context, path, hint string) (string, error) {
	return s.ImportFile(ctx, path, hint)
}
