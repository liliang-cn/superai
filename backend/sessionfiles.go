package backend

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Session ↔ deliverable ownership.
//
// The agent writes into one shared workspace and agent-go's deliverables scan
// has no notion of a conversation, so every chat would otherwise show every
// file ever produced. Instead, each turn is bracketed by a workspace snapshot:
// whatever appeared or changed while it ran belongs to that session.
//
// A snapshot is cheap (a walk recording size+mtime) and needs no cooperation
// from the model, which is the point — a rule like "write into <session>/" only
// holds while the model remembers to follow it.

// fileStamp identifies a file version without reading it.
type fileStamp struct {
	size  int64
	mtime int64
}

// sessionFiles maps a session id to the workspace-relative paths it produced,
// and remembers which paths arrived as attachments rather than being produced.
//
// Imported exists because "what the user handed in" was previously answered by
// location — anything under uploads/ — and the agent writes its output next to
// its input, so converting uploads/cv.pdf produced uploads/cv.docx and that file
// was excluded from the conversation's deliverables. Attachments are copied in by
// ImportFiles, so the exact set is known and does not have to be guessed from a
// directory name.
type sessionFiles struct {
	mu       sync.Mutex
	path     string
	Files    map[string][]string `json:"files"`
	Imported map[string]bool     `json:"imported,omitempty"`
}

func newSessionFiles(path string) *sessionFiles {
	sf := &sessionFiles{path: path, Files: map[string][]string{}, Imported: map[string]bool{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return sf
	}
	_ = json.Unmarshal(raw, sf)
	if sf.Files == nil {
		sf.Files = map[string][]string{}
	}
	if sf.Imported == nil {
		sf.Imported = map[string]bool{}
	}
	return sf
}

// noteImported records paths the app copied into the workspace for the user.
func (sf *sessionFiles) noteImported(paths []string) {
	if len(paths) == 0 {
		return
	}
	sf.mu.Lock()
	for _, p := range paths {
		if p = strings.TrimSpace(filepath.ToSlash(p)); p != "" {
			sf.Imported[p] = true
		}
	}
	sf.mu.Unlock()
	sf.save()
}

// isImported reports whether a workspace-relative path came from the user.
func (sf *sessionFiles) isImported(path string) bool {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.Imported[filepath.ToSlash(path)]
}

func (sf *sessionFiles) save() {
	sf.mu.Lock()
	raw, err := json.MarshalIndent(sf, "", "  ")
	sf.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(sf.path, raw, 0o644)
}

// record attaches paths to a session, keeping the list sorted and unique.
func (sf *sessionFiles) record(sessionID string, paths []string) {
	if strings.TrimSpace(sessionID) == "" || len(paths) == 0 {
		return
	}
	sf.mu.Lock()
	seen := map[string]bool{}
	for _, p := range sf.Files[sessionID] {
		seen[p] = true
	}
	for _, p := range paths {
		seen[p] = true
	}
	merged := make([]string, 0, len(seen))
	for p := range seen {
		merged = append(merged, p)
	}
	sort.Strings(merged)
	sf.Files[sessionID] = merged
	sf.mu.Unlock()

	sf.save()
}

// forSession returns the paths a session produced.
func (sf *sessionFiles) forSession(sessionID string) []string {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	out := make([]string, len(sf.Files[sessionID]))
	copy(out, sf.Files[sessionID])
	return out
}

// forget drops a session's associations (used when the conversation is deleted).
func (sf *sessionFiles) forget(sessionID string) {
	sf.mu.Lock()
	delete(sf.Files, sessionID)
	sf.mu.Unlock()
	sf.save()
}

// snapshotWorkspace stamps every file under root, skipping dotfiles.
//
// It used to skip the uploads directory outright. That also hid anything the
// agent wrote there, and the agent writes beside its input — so a converted
// attachment never showed up as produced by the turn. Attachments are filtered
// out by identity instead (see sessionFiles.isImported), which the caller
// applies to the diff.
func snapshotWorkspace(root string) map[string]fileStamp {
	out := map[string]fileStamp{}
	if strings.TrimSpace(root) == "" {
		return out
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if name != "." && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}
		return nil
	})
	return out
}

// changedFiles lists paths that appeared or changed between two snapshots.
func changedFiles(before, after map[string]fileStamp) []string {
	changed := make([]string, 0, len(after))
	for path, now := range after {
		if was, ok := before[path]; !ok || was != now {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}
