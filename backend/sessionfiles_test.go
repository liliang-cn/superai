package backend

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestSnapshotSkipsAttachmentsAndDotfiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "report.md", "hi")
	write(t, root, "nested/data.json", "{}")
	write(t, root, UploadsSubdir+"/resume.pdf", "%PDF")
	write(t, root, ".hidden", "x")
	write(t, root, ".cache/blob", "x")

	snap := snapshotWorkspace(root)
	if _, ok := snap["report.md"]; !ok {
		t.Error("a produced file must be in the snapshot")
	}
	if _, ok := snap["nested/data.json"]; !ok {
		t.Error("nested files must be in the snapshot")
	}
	if _, ok := snap[UploadsSubdir+"/resume.pdf"]; ok {
		t.Error("attachments must be skipped — the user supplied them")
	}
	if _, ok := snap[".hidden"]; ok {
		t.Error("dotfiles must be skipped")
	}
	if _, ok := snap[".cache/blob"]; ok {
		t.Error("dot directories must be skipped")
	}
}

func TestChangedFilesDetectsNewAndModified(t *testing.T) {
	root := t.TempDir()
	write(t, root, "kept.md", "same")
	write(t, root, "edited.md", "before")
	before := snapshotWorkspace(root)

	// A same-size rewrite still counts, because the mtime moves.
	time.Sleep(10 * time.Millisecond)
	write(t, root, "edited.md", "AFTER!")
	write(t, root, "fresh.md", "new")

	changed := changedFiles(before, snapshotWorkspace(root))
	got := map[string]bool{}
	for _, c := range changed {
		got[c] = true
	}
	if !got["fresh.md"] {
		t.Error("a new file must be reported")
	}
	if !got["edited.md"] {
		t.Error("a modified file must be reported")
	}
	if got["kept.md"] {
		t.Error("an untouched file must not be reported")
	}
}

func TestSessionFilesRecordAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-files.json")
	sf := newSessionFiles(path)

	sf.record("s1", []string{"b.md", "a.md"})
	sf.record("s1", []string{"a.md", "c.md"}) // overlapping, must dedupe
	sf.record("s2", []string{"other.md"})
	sf.record("", []string{"ignored.md"}) // no session: dropped
	sf.record("s3", nil)                  // nothing to record

	if got := sf.forSession("s1"); len(got) != 3 || got[0] != "a.md" || got[2] != "c.md" {
		t.Errorf("s1 files = %v, want sorted unique [a.md b.md c.md]", got)
	}
	if got := sf.forSession("s2"); len(got) != 1 {
		t.Errorf("s2 files = %v, want one", got)
	}
	if got := sf.forSession("nope"); len(got) != 0 {
		t.Errorf("unknown session should own nothing, got %v", got)
	}

	// The mapping must survive a restart.
	reloaded := newSessionFiles(path)
	if got := reloaded.forSession("s1"); len(got) != 3 {
		t.Errorf("after reload s1 files = %v, want 3", got)
	}

	reloaded.forget("s1")
	if got := reloaded.forSession("s1"); len(got) != 0 {
		t.Errorf("after forget s1 files = %v, want none", got)
	}
	if got := newSessionFiles(path).forSession("s2"); len(got) != 1 {
		t.Error("forgetting one session must not disturb another")
	}
}
