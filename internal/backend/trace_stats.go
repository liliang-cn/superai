package backend

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Per-tool figures across every trace on disk.
//
// The pulse meter counts calls per tool for this process, since it started.
// That answers "what is it doing" and not "which tool keeps failing" or "which
// one is slow on a bad day" — a question about a week, across restarts and
// across every conversation and task. The traces already hold the answer, one
// tool_end per call with its duration and error, so this reads them back
// rather than keeping a second ledger that could disagree with them.

// The scan's bounds. The traces directory has no retention of its own, and a
// conversation's file grows for as long as the conversation does, so an
// unbounded read is one long chat away from stalling the page that asked.
// Newest files first: when the budget runs out, what is left out is the
// oldest, which is also the least likely to still matter.
const (
	toolStatsMaxFiles   = 400
	toolStatsMaxBytes   = 64 << 20 // across every file read
	toolStatsFileBytes  = 16 << 20 // the tail of any one file
	toolStatsReadWorker = 4
)

// ToolStat is one tool's calls in the window.
type ToolStat struct {
	Tool        string  `json:"tool"`
	Calls       int     `json:"calls"`
	Failures    int     `json:"failures"`
	FailureRate float64 `json:"failureRate"`
	MeanMs      int64   `json:"meanMs"`
	P50Ms       int64   `json:"p50Ms"`
	P95Ms       int64   `json:"p95Ms"`
	MaxMs       int64   `json:"maxMs"`
	LastCalled  string  `json:"lastCalled"`
}

// ToolStatsReport is the table and what it was read from. Truncated says the
// bounds cut the scan short, so the page can say the figures are partial
// rather than let them pass as the whole week.
type ToolStatsReport struct {
	Since     string     `json:"since"`
	Files     int        `json:"files"`
	Bytes     int64      `json:"bytes"`
	Truncated bool       `json:"truncated"`
	Tools     []ToolStat `json:"tools"`
}

// toolTally is one tool's raw calls, before they are reduced to figures.
type toolTally struct {
	durs     []int64
	failures int
	last     time.Time
}

// ToolStats reads every trace in dir modified since since and reduces their
// tool_end lines to per-tool figures, most-called first.
//
// A line is counted when its own ts is inside the window too: a file touched
// today can hold a month of a conversation, and its old calls are not this
// week's. A line that does not parse is skipped — a trace cut off mid-write
// leaves one, and it must not cost the rest of the file.
func ToolStats(dir string, since time.Time) ToolStatsReport {
	return toolStats(dir, since, toolStatsMaxFiles, toolStatsMaxBytes, toolStatsFileBytes)
}

func toolStats(dir string, since time.Time, maxFiles int, maxBytes, fileBytes int64) ToolStatsReport {
	rep := ToolStatsReport{Since: since.Format(time.RFC3339), Tools: []ToolStat{}}

	type cand struct {
		path string
		size int64
		mod  time.Time
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return rep
	}
	var files []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		files = append(files, cand{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })

	// Settle the budget before reading anything, so the workers need not
	// share a counter: each file gets what is left of it, newest first.
	type job struct {
		path string
		n    int64
	}
	var jobs []job
	left := maxBytes
	for _, f := range files {
		if len(jobs) == maxFiles || left <= 0 {
			rep.Truncated = true
			break
		}
		n := min(f.size, fileBytes, left)
		if n < f.size {
			rep.Truncated = true
		}
		jobs = append(jobs, job{f.path, n})
		left -= n
	}

	parts := make([]map[string]*toolTally, len(jobs))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(toolStatsReadWorker, len(jobs)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				parts[i] = tallyTrace(jobs[i].path, jobs[i].n, since)
			}
		}()
	}
	for i := range jobs {
		next <- i
	}
	close(next)
	wg.Wait()

	all := map[string]*toolTally{}
	for i, p := range parts {
		rep.Files++
		rep.Bytes += jobs[i].n
		for tool, t := range p {
			a := all[tool]
			if a == nil {
				all[tool] = t
				continue
			}
			a.durs = append(a.durs, t.durs...)
			a.failures += t.failures
			if t.last.After(a.last) {
				a.last = t.last
			}
		}
	}

	for tool, t := range all {
		rep.Tools = append(rep.Tools, reduceTally(tool, t))
	}
	sort.Slice(rep.Tools, func(i, j int) bool {
		if rep.Tools[i].Calls != rep.Tools[j].Calls {
			return rep.Tools[i].Calls > rep.Tools[j].Calls
		}
		return rep.Tools[i].Tool < rep.Tools[j].Tool
	})
	return rep
}

// tallyTrace reads the last n bytes of one trace and counts its tool calls.
// Reading a tail can start mid-line; that first fragment is dropped rather
// than parsed, since it is the oldest line in the read anyway.
func tallyTrace(path string, n int64, since time.Time) map[string]*toolTally {
	out := map[string]*toolTally{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out
	}
	off := max(info.Size()-n, 0)
	buf := make([]byte, info.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return out
	}
	data := string(buf)
	if off > 0 {
		if i := strings.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			return out
		}
	}

	for _, line := range strings.Split(data, "\n") {
		// Most of a trace's bytes are model_end requests; a substring test
		// keeps them from being parsed only to be thrown away.
		if !strings.Contains(line, `"event":"tool_end"`) {
			continue
		}
		var ev struct {
			TS         string `json:"ts"`
			Event      string `json:"event"`
			Tool       string `json:"tool"`
			DurationMs int64  `json:"duration_ms"`
			Error      string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Event != "tool_end" || ev.Tool == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, ev.TS)
		if err != nil || ts.Before(since) {
			continue
		}
		t := out[ev.Tool]
		if t == nil {
			t = &toolTally{}
			out[ev.Tool] = t
		}
		t.durs = append(t.durs, ev.DurationMs)
		if ev.Error != "" {
			t.failures++
		}
		if ts.After(t.last) {
			t.last = ts
		}
	}
	return out
}

func reduceTally(tool string, t *toolTally) ToolStat {
	d := append([]int64(nil), t.durs...)
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	var sum int64
	for _, v := range d {
		sum += v
	}
	n := len(d)
	return ToolStat{
		Tool:        tool,
		Calls:       n,
		Failures:    t.failures,
		FailureRate: float64(t.failures) / float64(n),
		MeanMs:      int64(math.Round(float64(sum) / float64(n))),
		P50Ms:       nearestRank(d, 0.50),
		P95Ms:       nearestRank(d, 0.95),
		MaxMs:       d[n-1],
		LastCalled:  t.last.Format(time.RFC3339),
	}
}

// nearestRank is the p-th percentile of sorted by the nearest-rank method:
// always one of the observed durations, never an interpolation between two
// calls that did not take that long.
func nearestRank(sorted []int64, p float64) int64 {
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}
