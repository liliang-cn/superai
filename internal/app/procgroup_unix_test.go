//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A machine command that runs out of time takes its whole process group with
// it, including a child that ignores SIGTERM.
func TestHostRunTimeoutKillsTheGroup(t *testing.T) {
	old := shellKillGrace
	shellKillGrace = 300 * time.Millisecond
	defer func() { shellKillGrace = old }()

	pidfile := filepath.Join(t.TempDir(), "pid")
	script := `trap '' TERM; sh -c 'trap "" TERM; sleep 60' & echo $! > ` + pidfile + `; wait`
	start := time.Now()
	_, _, err := hostRun(context.Background(), 400*time.Millisecond, "sh", "-c", script)
	if err == nil {
		t.Fatal("a command past its deadline reported no error")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("took %s", took)
	}
	b, _ := os.ReadFile(pidfile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid == 0 {
		t.Fatal("the child never started")
	}
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("pid %d outlived its command", pid)
}
