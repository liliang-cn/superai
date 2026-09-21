package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

// An advisory lock on a file in the data directory, for the jobs that exactly
// one process may run.
//
// Two of them exist so far and they arrived for the same reason: the app and
// the daemon point at the same database, and a background job that runs in both
// happens twice. For the scheduler that is two messages and two writes; for the
// Telegram poller it is worse than twice, because two long polls on one bot do
// not duplicate the updates, they *split* them — each message goes to whichever
// process happened to be waiting, so half the conversation answers and half
// vanishes with no error anywhere.
//
// The lock is released when the holder exits, including a crash: the kernel
// drops it with the file descriptor. So a killed daemon does not leave the app
// permanently unable to take over.

// FileLock is a held claim on doing something only one process should do.
type FileLock struct {
	mu   sync.Mutex
	file *os.File
}

// AcquireFileLock tries to claim name for this process. It returns (nil, nil)
// when another process already holds it — that is the ordinary case, not an
// error, and the caller decides what to do without it.
func AcquireFileLock(name string) (*FileLock, error) {
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	path := filepath.Join(DataDir(), name)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}

	// Non-blocking: the point is to find out whether someone else has it, not
	// to wait for them to quit.
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, nil
	}

	// The pid is for a human reading the file during diagnosis; the lock itself
	// is what enforces anything.
	_ = file.Truncate(0)
	_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)

	return &FileLock{file: file}, nil
}

// Release gives up the claim. Safe to call more than once, and on a nil lock —
// callers hold either a lock or nil and should not have to tell them apart.
func (l *FileLock) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
