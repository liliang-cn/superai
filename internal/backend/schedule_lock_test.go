package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// The lock is what stops a schedule running twice — once in the app and once in
// the daemon, which for "message me my returns" means two messages. flock is
// counted per open file description, so a second acquisition fails even from the
// same process, which is what makes this testable without spawning one.
func TestScheduleLockIsExclusive(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())

	first, err := AcquireScheduleLock()
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if first == nil {
		t.Fatal("the first acquisition must succeed — nothing else holds it")
	}

	// The contract that matters: a busy lock is reported as "not yours", not as
	// an error, because the caller's correct response is to carry on without a
	// cron loop rather than to fail.
	second, err := AcquireScheduleLock()
	if err != nil {
		t.Errorf("a busy lock should not be an error, got %v", err)
	}
	if second != nil {
		t.Error("two holders at once — a schedule would fire twice")
		second.Release()
	}

	// After release the next process can take over, which is how the daemon
	// picks up the timers when the app quits.
	first.Release()
	third, err := AcquireScheduleLock()
	if err != nil || third == nil {
		t.Fatalf("the lock must be reclaimable after release (err=%v)", err)
	}
	third.Release()

	// Releasing twice is safe: shutdown paths can be reached more than once.
	third.Release()
	var nilLock *ScheduleLock
	nilLock.Release()
}

func TestScheduleLockRecordsHolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	lock, err := AcquireScheduleLock()
	if err != nil || lock == nil {
		t.Fatalf("acquire: %v", err)
	}
	defer lock.Release()

	// The pid is diagnostic only — it is what lets the daemon log "pid N owns the
	// timers" instead of leaving the user wondering why nothing fires.
	if got := ScheduleLockHolder(); got != os.Getpid() {
		t.Errorf("holder pid = %d, want %d", got, os.Getpid())
	}

	if _, err := os.Stat(filepath.Join(home, scheduleLockName)); err != nil {
		t.Errorf("lock file should exist: %v", err)
	}
}

func TestScheduleLockHolderWithoutLockFile(t *testing.T) {
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	if got := ScheduleLockHolder(); got != 0 {
		t.Errorf("with no lock file the holder should be unknown (0), got %d", got)
	}
}
