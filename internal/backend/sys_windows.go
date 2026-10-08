//go:build windows

package backend

import (
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on f without waiting for it.
func lockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
}

func unlockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}

// killGroupOnCancel has cancelling cmd kill it. Windows has no process group
// to send a signal to, so the agent's own children are not reached.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Kill() }
}

// processCPUTime is the CPU this process has used, user and system; 0 when it
// cannot be read.
func processCPUTime() time.Duration {
	var c, e, k, u windows.Filetime
	if windows.GetProcessTimes(windows.CurrentProcess(), &c, &e, &k, &u) != nil {
		return 0
	}
	// Filetime counts 100ns ticks.
	ticks := func(t windows.Filetime) int64 { return int64(t.HighDateTime)<<32 | int64(t.LowDateTime) }
	return time.Duration((ticks(k) + ticks(u)) * 100)
}
