package backend

import (
	"os"
	"path/filepath"
	"strconv"
)

// Who owns the timers.
//
// A schedule has to fire at eight in the morning whether or not the window is
// open, so a background daemon keeps the cron loop running. But the app runs one
// too — and both point at the same database, so without arbitration a task would
// execute twice, once per process. For "message me my stock returns" that means
// two messages; for anything that writes, two writes.
//
// An advisory lock on a file in the data directory settles it: whoever holds it
// runs the timers, and the other process still reads and edits schedules (they
// live in the shared database) but does not fire them. The lock is released when
// the holder exits — including a crash, since the kernel drops it with the file
// descriptor, so a killed daemon does not leave the app permanently unable to
// take over.
//
// This is deliberately not a "daemon wins" rule. The app must work on a machine
// where the daemon was never installed, and the daemon must work when the app is
// closed. Whoever is up first takes the timers.

// scheduleLockName is the lock file inside the data directory.
const scheduleLockName = "scheduler.lock"

// ScheduleLock is a held claim on running the timers. It is a FileLock under
// the name below; the alias keeps the callers reading as what they are about.
type ScheduleLock = FileLock

// AcquireScheduleLock tries to claim the timers for this process. It returns
// (nil, nil) when another process already holds them — that is the ordinary
// case, not an error, and the caller should carry on without a cron loop.
func AcquireScheduleLock() (*ScheduleLock, error) {
	return AcquireFileLock(scheduleLockName)
}

// ScheduleLockHolder reports the pid recorded in the lock file, for telling the
// user which process owns the timers. It reads the file rather than the lock, so
// a stale pid is possible; it is diagnostic only.
func ScheduleLockHolder() int {
	raw, err := os.ReadFile(filepath.Join(DataDir(), scheduleLockName))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(string(trimSpaceBytes(raw)))
	if err != nil {
		return 0
	}
	return pid
}

func trimSpaceBytes(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpaceByte(b[start]) {
		start++
	}
	for end > start && isSpaceByte(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
