package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/superai-desktop/backend"
)

// The RPC reads the traces directory this app writes into, and hands back the
// tail. It must not need a Service: a trace is worth reading after the run is
// over, and after a settings save has rebuilt the service that wrote it.
func TestTraceLinesReadsTheTailWithoutAService(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	dir := backend.TracesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var sb strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&sb, `{"ts":"t%d","event":"model_end","round":%d}`+"\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-42.jsonl"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	app := NewApp()
	if app.svc != nil {
		t.Fatal("this test is about a trace read with no service built")
	}

	lines := app.TraceLines("task-42", 2)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want the last 2", len(lines))
	}
	if !strings.Contains(lines[0], `"round":4`) || !strings.Contains(lines[1], `"round":5`) {
		t.Errorf("tail is %v, want rounds 4 then 5", lines)
	}

	// A limit of zero is the page's "just give me some", not "give me none".
	if all := app.TraceLines("task-42", 0); len(all) != 6 {
		t.Errorf("limit 0 returned %d lines, want the whole short trace", len(all))
	}

	// A task with no trace, and an id that is not one, are both an empty
	// answer rather than something the panel has to catch.
	if got := app.TraceLines("never-ran", 10); len(got) != 0 {
		t.Errorf("unknown task returned %d lines", len(got))
	}
	if got := app.TraceLines("../../etc/passwd", 10); len(got) != 0 {
		t.Errorf("a path-shaped id returned %d lines", len(got))
	}
}
