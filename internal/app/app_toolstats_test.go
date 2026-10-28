package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// The per-tool table is read back from the traces on disk, over a window.
// Both the file's age and each line's own ts decide what is in it: a file
// touched today can hold a month of one conversation, and only this week's
// calls belong in this week's figures.
func TestToolStatsAggregatesTheWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	dir := backend.TracesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	now := time.Now()
	end := func(at time.Time, tool string, ms int, errText string) string {
		e := ""
		if errText != "" {
			e = fmt.Sprintf(`,"error":%q`, errText)
		}
		return fmt.Sprintf(`{"ts":%q,"event":"tool_end","call_id":"c-%d","tool":%q,"duration_ms":%d%s}`,
			at.Format(time.RFC3339Nano), ms, tool, ms, e)
	}
	write := func(name string, mod time.Time, lines ...string) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}

	write("chat-a.jsonl", now,
		`{"ts":"`+now.Format(time.RFC3339Nano)+`","event":"tool_start","call_id":"c-1","tool":"bash","args":{"command":"ls"}}`,
		end(now.Add(-1*time.Hour), "bash", 100, ""),
		end(now.Add(-2*time.Hour), "bash", 300, "exit status 1"),
		// A line cut off mid-write: skipped, and the rest of the file still counts.
		`{"ts":"`+now.Format(time.RFC3339Nano)+`","event":"tool_end","tool":"bash","duration_ms":9`,
		end(now.Add(-3*time.Hour), "fs_read", 20, ""),
		// An old call in a file touched today.
		end(now.AddDate(0, 0, -10), "bash", 200, ""),
		`{"ts":"`+now.Format(time.RFC3339Nano)+`","event":"model_end","span_id":"trace-1","duration_ms":4000,"request":{"messages":[]}}`,
	)
	write("task-b.jsonl", now.AddDate(0, 0, -1),
		end(now.AddDate(0, 0, -1), "fs_read", 40, ""),
		end(now.AddDate(0, 0, -1), "bash", 1000, ""),
	)
	write("task-old.jsonl", now.AddDate(0, 0, -20),
		end(now.AddDate(0, 0, -20), "bash", 50, ""),
		end(now.AddDate(0, 0, -20), "web_fetch", 500, "timeout"),
	)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(end(now, "bash", 1, "")), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	last := now.Add(-1 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		name  string
		days  int
		files int
		want  []backend.ToolStat
	}{
		{
			name:  "a week",
			days:  7,
			files: 2,
			want: []backend.ToolStat{
				{Tool: "bash", Calls: 3, Failures: 1, MeanMs: 467, P50Ms: 300, P95Ms: 1000, MaxMs: 1000, LastCalled: last},
				{Tool: "fs_read", Calls: 2, MeanMs: 30, P50Ms: 20, P95Ms: 40, MaxMs: 40},
			},
		},
		{
			name:  "zero is a week",
			days:  0,
			files: 2,
			want: []backend.ToolStat{
				{Tool: "bash", Calls: 3, Failures: 1, MeanMs: 467, P50Ms: 300, P95Ms: 1000, MaxMs: 1000, LastCalled: last},
				{Tool: "fs_read", Calls: 2, MeanMs: 30, P50Ms: 20, P95Ms: 40, MaxMs: 40},
			},
		},
		{
			name:  "a month",
			days:  30,
			files: 3,
			want: []backend.ToolStat{
				{Tool: "bash", Calls: 5, Failures: 1, MeanMs: 330, P50Ms: 200, P95Ms: 1000, MaxMs: 1000, LastCalled: last},
				{Tool: "fs_read", Calls: 2, MeanMs: 30, P50Ms: 20, P95Ms: 40, MaxMs: 40},
				{Tool: "web_fetch", Calls: 1, Failures: 1, MeanMs: 500, P50Ms: 500, P95Ms: 500, MaxMs: 500},
			},
		},
	}

	app := NewApp()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := app.ToolStats(tc.days)
			if rep.Files != tc.files {
				t.Errorf("read %d files, want %d", rep.Files, tc.files)
			}
			if rep.Truncated {
				t.Errorf("a few small files must not be cut short")
			}
			if len(rep.Tools) != len(tc.want) {
				t.Fatalf("got %d tools %+v, want %d", len(rep.Tools), rep.Tools, len(tc.want))
			}
			for i, w := range tc.want {
				g := rep.Tools[i]
				if g.Tool != w.Tool || g.Calls != w.Calls || g.Failures != w.Failures ||
					g.MeanMs != w.MeanMs || g.P50Ms != w.P50Ms || g.P95Ms != w.P95Ms || g.MaxMs != w.MaxMs {
					t.Errorf("row %d = %+v, want %+v", i, g, w)
				}
				if rate := float64(w.Failures) / float64(w.Calls); g.FailureRate != rate {
					t.Errorf("%s failure rate %v, want %v", w.Tool, g.FailureRate, rate)
				}
				if w.LastCalled != "" && g.LastCalled != w.LastCalled {
					t.Errorf("%s last called %s, want %s", w.Tool, g.LastCalled, w.LastCalled)
				}
			}
		})
	}
}

// No traces directory yet is an empty table, not an error the page catches.
func TestToolStatsWithNoTraces(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	rep := NewApp().ToolStats(7)
	if rep.Files != 0 || rep.Tools == nil || len(rep.Tools) != 0 {
		t.Errorf("got %+v, want an empty, non-nil table", rep)
	}
}
