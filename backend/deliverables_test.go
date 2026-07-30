package backend

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v2/pkg/agent"
)

// filterDeliverables mirrors the filter Deliverables applies, so the rule can be
// tested without standing up a whole agent service.
func filterDeliverables(all []agent.Deliverable) []agent.Deliverable {
	out := make([]agent.Deliverable, 0, len(all))
	for _, d := range all {
		if strings.HasPrefix(filepath.ToSlash(d.Path), UploadsSubdir+"/") {
			continue
		}
		out = append(out, d)
	}
	return out
}

func TestDeliverablesExcludeUserUploads(t *testing.T) {
	all := []agent.Deliverable{
		{Path: "uploads/resume.pdf"},
		{Path: "uploads/nested/scan.png"},
		{Path: "summary-zh.md"},
		{Path: "reports/summary.md"},
		{Path: "uploads-report.md"}, // not in the uploads dir despite the prefix
	}

	kept := filterDeliverables(all)
	if len(kept) != 3 {
		t.Fatalf("kept %d deliverables, want 3: %+v", len(kept), kept)
	}
	for _, d := range kept {
		if strings.HasPrefix(d.Path, UploadsSubdir+"/") {
			t.Errorf("attachment leaked into deliverables: %s", d.Path)
		}
	}
	if kept[2].Path != "uploads-report.md" {
		t.Errorf("a file merely starting with %q must be kept, got %+v", UploadsSubdir, kept)
	}
}
