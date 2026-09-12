package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// traceEvents returns the "event" field of every line, and fails on a line
// that is not a JSON object — a trace a program cannot parse is not a trace.
func traceEvents(t *testing.T, lines []string) []string {
	t.Helper()
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(l), &obj); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", i, err, l)
		}
		if obj["ts"] == nil {
			t.Errorf("line %d carries no timestamp: %s", i, l)
		}
		ev, _ := obj["event"].(string)
		out = append(out, ev)
	}
	return out
}

// The trace must fill from a real segmented run through the observer. If the
// store is not registered, or a callback is not forwarded, this goes red.
func TestTraceStoreRecordsASegmentedRun(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	dir := t.TempDir()
	svc.traces = NewTraceStore(dir)
	svc.Agent().RegisterObserver(svc.traces)

	const id = "trace-segmented"
	if _, err := svc.StreamLong(context.Background(), "Say hello.",
		LongRunOptions{MaxSegments: 2, RoundsPerSegment: 1, TaskID: id}, nil); err != nil {
		t.Fatalf("StreamLong: %v", err)
	}

	lines := TraceLines(dir, id, 500)
	if len(lines) == 0 {
		t.Fatal("no trace written for a task that just ran")
	}
	events := traceEvents(t, lines)
	for _, want := range []string{"model_start", "model_end", "segment_start", "segment_end"} {
		var found bool
		for _, ev := range events {
			if ev == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no %s line in the trace; events were %v", want, events)
		}
	}

	// Every line belongs to this task, which is what makes a file separable
	// when several runs share the service.
	for _, l := range lines {
		var obj map[string]any
		_ = json.Unmarshal([]byte(l), &obj)
		if tid, ok := obj["task_id"].(string); ok && tid != id {
			t.Errorf("line from another task in this file: %s", l)
		}
	}

	// The run is over, so the writer is closed and the file is complete.
	if _, err := os.Stat(filepath.Join(dir, id+".jsonl")); err != nil {
		t.Errorf("trace file missing: %v", err)
	}
}

// A chat turn is not a task worth a file. The observer sees every run the
// service makes; only a long run goes through Begin.
func TestTraceStoreIgnoresRunsItWasNotToldAbout(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	dir := t.TempDir()
	svc.traces = NewTraceStore(dir)
	svc.Agent().RegisterObserver(svc.traces)

	if _, err := svc.Stream(context.Background(), "chat-session", "Say hello.", nil, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			t.Fatalf("a chat turn wrote a trace file: %s", e.Name())
		}
	}
}

// A resumed task appends to the file it already had: a segmented run is one
// document, not one per segment.
func TestTraceStoreAppendsOnResume(t *testing.T) {
	svc := newTestLongRunService(t)
	defer svc.Close()

	dir := t.TempDir()
	svc.traces = NewTraceStore(dir)
	svc.Agent().RegisterObserver(svc.traces)

	const id = "trace-resumed"
	opts := LongRunOptions{MaxSegments: 1, RoundsPerSegment: 1, TaskID: id}
	if _, err := svc.StreamLong(context.Background(), "Say hello.", opts, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := len(TraceLines(dir, id, 1000))
	if first == 0 {
		t.Fatal("first run wrote nothing")
	}
	if _, err := svc.StreamLong(context.Background(), "Say hello again.", opts, nil); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if second := len(TraceLines(dir, id, 1000)); second <= first {
		t.Errorf("resume produced %d lines, first run had %d — the file was replaced, not appended",
			second, first)
	}
}

// Old traces are pruned when a new task starts, and a live one is never among
// them.
func TestTraceStorePrunesOldTraces(t *testing.T) {
	dir := t.TempDir()
	store := NewTraceStore(dir)

	// One that is open, and must survive whatever its age.
	store.Begin("live-task")
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(store.Path("live-task"), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	for i := 0; i < traceKeepFiles+8; i++ {
		p := filepath.Join(dir, fmt.Sprintf("stale-%03d.jsonl", i))
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		when := time.Now().Add(-time.Duration(i+1) * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	store.Begin("fresh-task")
	defer store.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) > traceKeepFiles {
		t.Errorf("%d traces on disk, cap is %d", len(entries), traceKeepFiles)
	}
	if _, err := os.Stat(store.Path("live-task")); err != nil {
		t.Errorf("an open trace was pruned: %v", err)
	}
	if _, err := os.Stat(store.Path("fresh-task")); err != nil {
		t.Errorf("the new trace is missing: %v", err)
	}
}

// Past its cap a trace stops growing and says so once, rather than filling the
// disk with a run that loops.
func TestTraceCapStopsWithAMarker(t *testing.T) {
	var sb strings.Builder
	c := &capWriter{w: &sb, max: 40}
	for i := 0; i < 20; i++ {
		if _, err := c.Write([]byte(`{"event":"model_end"}` + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	out := sb.String()
	if !strings.Contains(out, "trace_truncated") {
		t.Error("no truncation marker written")
	}
	if strings.Count(out, "trace_truncated") != 1 {
		t.Errorf("marker written %d times, want once", strings.Count(out, "trace_truncated"))
	}
	if len(out) > 200 {
		t.Errorf("cap of 40 bytes let %d bytes through", len(out))
	}
}

// A task id is a file name. One with a separator in it would be a write, and
// a read, wherever it pointed.
func TestTraceIDsCannotEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "escaped.jsonl")
	if err := os.WriteFile(outside, []byte(`{"event":"secret"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	store := NewTraceStore(filepath.Join(dir, "traces"))
	for _, bad := range []string{"../escaped", "a/b", "..", ""} {
		store.Begin(bad)
		if got := TraceLines(filepath.Join(dir, "traces"), bad, 10); len(got) != 0 {
			t.Errorf("id %q read %d line(s) it should not have", bad, len(got))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "traces")); err == nil {
		t.Error("a rejected id still created the traces directory")
	}
}

// The tail is the tail: limit lines, oldest first, and a missing trace is an
// empty answer rather than an error the page has to draw.
func TestTraceLinesTail(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sb, `{"event":"model_end","round":%d}`+"\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "tail.jsonl"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := TraceLines(dir, "tail", 3)
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3", len(got))
	}
	if !strings.Contains(got[0], `"round":7`) || !strings.Contains(got[2], `"round":9`) {
		t.Errorf("tail is %v, want rounds 7..9 in order", got)
	}
	if lines := TraceLines(dir, "no-such-task", 10); len(lines) != 0 {
		t.Errorf("a missing trace returned %d lines", len(lines))
	}
}
