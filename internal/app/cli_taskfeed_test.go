package app

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// A cli:event of a coding run, as the mirror of a run on another machine
// replays it here, lands on the board entry of the order that run is for: its tool, its count,
// its words.
func TestCLIEventsFromAnotherMachineUpdateTheTaskOfThatRun(t *testing.T) {
	a := &App{}
	var mu sync.Mutex
	var seen []map[string]any
	a.emitFn = func(name string, p map[string]any) {
		if name == "hive:task" {
			mu.Lock()
			seen = append(seen, p)
			mu.Unlock()
		}
	}
	id := a.tasks().Start("claude.mac", backend.TaskOut, "read files")
	run := &CLIRun{ID: "local1", Agent: "claude.mac", Remote: "mac", State: "running"}
	s := a.cliStore()
	s.mu.Lock()
	s.runs[run.ID] = run
	s.watch[run.ID] = a.cliTaskFeed(id)
	s.mu.Unlock()

	// What the mirror does with each cli:event of the remote run.
	for _, e := range []CLIRunEvent{
		{Kind: "tool", Tool: "Read", Detail: "/tmp/a.txt"},
		{Kind: "result", Tool: "Read", Text: "hello"},
		{Kind: "tool", Tool: "Bash", Detail: "ls"},
		{Kind: "delta", Text: "summing up"},
	} {
		a.addCLIEvent(run, e)
	}
	// A run that is not this task's changes nothing here.
	other := &CLIRun{ID: "local2", State: "running"}
	a.addCLIEvent(other, CLIRunEvent{Kind: "tool", Tool: "Bash", Detail: "other run"})

	tk, ok := a.tasks().Get(id)
	if !ok {
		t.Fatal("no task")
	}
	if tk.Tools != 2 || tk.Phase != backend.PhaseWriting {
		t.Fatalf("tools %d phase %q, want 2 and writing", tk.Tools, tk.Phase)
	}
	for _, want := range []string{"▸ Read /tmp/a.txt", "◂ hello", "▸ Bash ls"} {
		if !strings.Contains(tk.Live, want) {
			t.Fatalf("live %q lacks %q", tk.Live, want)
		}
	}
	if strings.Contains(tk.Live, "other run") {
		t.Fatalf("another run's event reached this task: %q", tk.Live)
	}
	time.Sleep(300 * time.Millisecond) // the throttled publish carrying the words
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 {
		t.Fatalf("only %d hive:task events", len(seen))
	}
	if l, _ := seen[len(seen)-1]["live"].(string); !strings.Contains(l, "summing up") {
		t.Fatalf("last published live %q", l)
	}
}
