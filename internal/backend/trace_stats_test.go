package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The scan's bounds: past the file count it stops at the newest files, and
// past a file's byte share it reads only that file's tail, dropping the line
// the cut lands in. Either way the report says it is partial.
func TestToolStatsBounds(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	line := func(tool string, ms int) string {
		return fmt.Sprintf(`{"ts":%q,"event":"tool_end","tool":%q,"duration_ms":%d}`, now.Format(time.RFC3339Nano), tool, ms)
	}
	for i, name := range []string{"old", "mid", "new"} {
		p := filepath.Join(dir, name+".jsonl")
		body := strings.Join([]string{line(name, 10), line(name, 20), line(name, 30)}, "\n") + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		mod := now.Add(time.Duration(i-3) * time.Minute)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	lineLen := int64(len(line("new", 10)) + 1)
	since := now.Add(-time.Hour)

	cases := []struct {
		name      string
		maxFiles  int
		maxBytes  int64
		fileBytes int64
		calls     map[string]int
		truncated bool
	}{
		{"unbounded", 10, 1 << 20, 1 << 20, map[string]int{"old": 3, "mid": 3, "new": 3}, false},
		{"two newest files", 2, 1 << 20, 1 << 20, map[string]int{"mid": 3, "new": 3}, true},
		// A share of two lines and a half: the half is dropped, two are read.
		{"a file's tail", 10, 1 << 20, 2*lineLen + lineLen/2, map[string]int{"old": 2, "mid": 2, "new": 2}, true},
		// The whole budget is one file's worth: the newest takes it all.
		{"total budget", 10, 3 * lineLen, 1 << 20, map[string]int{"new": 3}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := toolStats(dir, since, tc.maxFiles, tc.maxBytes, tc.fileBytes)
			got := map[string]int{}
			for _, s := range rep.Tools {
				got[s.Tool] = s.Calls
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.calls) {
				t.Errorf("calls %v, want %v", got, tc.calls)
			}
			if rep.Truncated != tc.truncated {
				t.Errorf("truncated %v, want %v", rep.Truncated, tc.truncated)
			}
		})
	}
}
