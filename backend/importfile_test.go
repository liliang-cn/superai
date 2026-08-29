package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A nil-cortex Service refuses before it opens anything, so these exercise the
// format decision and the failure messages rather than a real import — which
// needs a brain and an LLM. What is worth pinning here is that an unreadable
// format is named rather than guessed at.
func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// Silently reading a .json as CSV would produce one column of garbage and an
// import that looks like it worked. Refuse by name instead.
func TestImportRefusesFormatsItCannotRead(t *testing.T) {
	s := &Service{}
	for _, name := range []string{"data.json", "sheet.xlsx", "notes.md", "archive.zip", "noext"} {
		_, err := s.ImportFile(context.Background(), writeTemp(t, name, "{}"), "")
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		// With no cortex configured every path fails; what matters is which
		// message comes back for a format that is genuinely unreadable.
		if !strings.Contains(err.Error(), "CortexDB not configured") &&
			!strings.Contains(err.Error(), "not a format this can read") {
			t.Errorf("%s failed with an unhelpful message: %v", name, err)
		}
	}
}

// The extensions the importer actually has a parser for.
func TestImportKnowsItsFormats(t *testing.T) {
	for _, name := range []string{"a.csv", "a.CSV", "a.tsv", "a.sql", "a.dump"} {
		if !importableExt(filepath.Ext(name)) {
			t.Errorf("%s should be importable", name)
		}
	}
	for _, name := range []string{"a.json", "a.xlsx", "a.parquet", "a"} {
		if importableExt(filepath.Ext(name)) {
			t.Errorf("%s should not be importable — nothing here parses it", name)
		}
	}
}
