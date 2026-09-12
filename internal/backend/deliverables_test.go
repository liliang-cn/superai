package backend

import (
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// These call keepDeliverables directly. An earlier version of this file kept its
// own copy of the rule "so it can be tested without standing up a whole agent
// service" — and the copy is what the tests then guarded, which is how a wrong
// rule stayed green.

func paths(ds []agent.Deliverable) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Path)
	}
	return out
}

func noImports(string) bool { return false }

// With no conversation in hand there is nothing to compare against, so anything
// sitting in uploads/ is assumed to be an attachment.
func TestKeepDeliverablesWithoutASession(t *testing.T) {
	all := []agent.Deliverable{
		{Path: "uploads/resume.pdf"},
		{Path: "uploads/nested/scan.png"},
		{Path: "summary-zh.md"},
		{Path: "reports/summary.md"},
		{Path: "uploads-report.md"}, // not in the uploads dir despite the prefix
	}

	kept := paths(keepDeliverables(all, nil, noImports))
	if len(kept) != 3 {
		t.Fatalf("kept %d, want 3: %v", len(kept), kept)
	}
	for _, p := range kept {
		if strings.HasPrefix(p, UploadsSubdir+"/") {
			t.Errorf("attachment leaked into deliverables: %s", p)
		}
	}
	if kept[2] != "uploads-report.md" {
		t.Errorf("a file merely starting with %q must be kept, got %v", UploadsSubdir, kept)
	}
}

// The regression: the agent converted uploads/resume.pdf and wrote the result
// beside it, and the whole-directory rule hid it. What the conversation produced
// is what it shows, wherever that landed; only the attachment itself is dropped.
func TestKeepDeliverablesShowsWhatTheAgentWroteIntoUploads(t *testing.T) {
	all := []agent.Deliverable{
		{Path: "uploads/resume.pdf"},  // handed in
		{Path: "uploads/resume.docx"}, // produced from it, same directory
		{Path: "notes.md"},            // produced elsewhere
		{Path: "someone-elses.md"},    // another conversation's
	}
	owned := map[string]bool{
		"uploads/resume.pdf":  true, // seen changing during the turn, still an upload
		"uploads/resume.docx": true,
		"notes.md":            true,
	}
	imported := func(p string) bool { return p == "uploads/resume.pdf" }

	kept := paths(keepDeliverables(all, owned, imported))
	if len(kept) != 2 {
		t.Fatalf("kept %v, want [uploads/resume.docx notes.md]", kept)
	}
	if kept[0] != "uploads/resume.docx" {
		t.Errorf("the converted file must be listed, got %v", kept)
	}
	if kept[1] != "notes.md" {
		t.Errorf("files produced elsewhere must still be listed, got %v", kept)
	}
}

// A conversation shows its own artifacts and no one else's.
func TestKeepDeliverablesIsScopedToTheSession(t *testing.T) {
	all := []agent.Deliverable{{Path: "mine.md"}, {Path: "theirs.md"}}
	kept := paths(keepDeliverables(all, map[string]bool{"mine.md": true}, noImports))
	if len(kept) != 1 || kept[0] != "mine.md" {
		t.Errorf("kept %v, want [mine.md]", kept)
	}
}
