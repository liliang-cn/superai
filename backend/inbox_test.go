package backend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testInbox(t *testing.T) *Inbox {
	t.Helper()
	return NewInbox(t.TempDir())
}

func raise(b *Inbox, n Notice) {
	if n.At == "" {
		n.At = time.Now().Format(time.RFC3339Nano)
	}
	b.Sink()(context.Background(), n)
}

// The centre exists for the messages nobody was there to see, so what it keeps
// has to survive the process that raised them.
func TestNoticesAreKeptAndReadBackNewestFirst(t *testing.T) {
	dir := t.TempDir()
	b := NewInbox(dir)
	raise(b, Notice{Level: LevelInfo, Message: "first", At: "2026-09-05T01:00:00Z"})
	raise(b, Notice{Level: LevelError, Message: "second", At: "2026-09-05T02:00:00Z", Source: "a schedule"})

	// A second Inbox over the same directory is what the other process is.
	got := NewInbox(dir).List(0)
	if len(got) != 2 {
		t.Fatalf("kept %d notices, want 2", len(got))
	}
	if got[0].Message != "second" {
		t.Errorf("not newest first: %+v", got)
	}
	if got[0].Source != "a schedule" || got[0].Level != LevelError {
		t.Errorf("the notice lost its detail: %+v", got[0])
	}
	if got[0].Read {
		t.Error("a notice arrived already read")
	}
	if b.Unread() != 2 {
		t.Errorf("unread = %d, want 2", b.Unread())
	}
}

// Nothing is filtered on the way in. What is worth interrupting somebody for is
// a different question, answered by Notice.Push and read elsewhere; deciding
// here what they were allowed to have missed is not this file's call.
func TestEverythingIsKeptExceptTheEmpty(t *testing.T) {
	b := testInbox(t)
	raise(b, Notice{Level: LevelInfo, Message: "quiet but kept", Push: false})
	raise(b, Notice{Level: LevelInfo, Message: "   "})
	if got := b.List(0); len(got) != 1 || got[0].Message != "quiet but kept" {
		t.Errorf("got %+v", got)
	}
}

// A run that reports twice is one entry that changed, not two that disagree —
// the same rule the toasts follow, keyed the same way.
func TestAKeyReplacesRatherThanStacks(t *testing.T) {
	b := testInbox(t)
	raise(b, Notice{Level: LevelInfo, Message: "running", Key: "run:7", At: "2026-09-05T01:00:00Z"})
	raise(b, Notice{Level: LevelSuccess, Message: "done", Key: "run:7", At: "2026-09-05T01:00:30Z"})

	got := b.List(0)
	if len(got) != 1 {
		t.Fatalf("a keyed notice stacked: %+v", got)
	}
	if got[0].Message != "done" || got[0].Level != LevelSuccess {
		t.Errorf("the replacement did not win: %+v", got[0])
	}

	// And an update makes it news again.
	if err := b.MarkRead(); err != nil {
		t.Fatal(err)
	}
	raise(b, Notice{Level: LevelError, Message: "failed after all", Key: "run:7", At: "2026-09-05T01:01:00Z"})
	if b.Unread() != 1 {
		t.Error("a notice whose content changed stayed read")
	}
}

func TestMarkingReadOneAtATimeOrAllAtOnce(t *testing.T) {
	b := testInbox(t)
	raise(b, Notice{Message: "a", Key: "a"})
	raise(b, Notice{Message: "b", Key: "b"})

	if err := b.MarkRead("a"); err != nil {
		t.Fatal(err)
	}
	if b.Unread() != 1 {
		t.Errorf("unread = %d after marking one, want 1", b.Unread())
	}
	// No ids is what opening the centre does.
	if err := b.MarkRead(); err != nil {
		t.Fatal(err)
	}
	if b.Unread() != 0 {
		t.Errorf("unread = %d after marking all, want 0", b.Unread())
	}
	// Read state survives the process too, or every restart would light the
	// badge up again for things already seen.
	if NewInbox(filepath.Dir(b.path)).Unread() != 0 {
		t.Error("read state did not persist")
	}
}

// A nightly schedule must not grow the file forever, and what it drops is the
// oldest — a centre is a recent history read from the top.
func TestTheOldestAreDroppedPastTheCap(t *testing.T) {
	b := testInbox(t)
	for i := 0; i < inboxCap+40; i++ {
		raise(b, Notice{Message: "m", Key: string(rune('a'+i%26)) + time.Unix(int64(i), 0).Format(time.RFC3339Nano),
			At: time.Unix(int64(i), 0).UTC().Format(time.RFC3339Nano)})
	}
	got := b.List(0)
	if len(got) != inboxCap {
		t.Fatalf("kept %d, want the cap of %d", len(got), inboxCap)
	}
	oldest := got[len(got)-1].At
	if oldest < time.Unix(40, 0).UTC().Format(time.RFC3339Nano) {
		t.Errorf("the survivors are not the newest: oldest kept is %s", oldest)
	}
}

// Nothing here may take down the message it was filing. Every caller is on the
// path of some other surface's delivery.
func TestAnUnwritableStoreDoesNotPanic(t *testing.T) {
	// A file where the directory should be: the store creates missing
	// directories, so a merely absent path is writable and would not test this.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Inbox{path: filepath.Join(blocker, "n.json")}
	raise(b, Notice{Message: "still fine"})
	if got := b.List(0); got == nil {
		t.Error("List must answer with an empty slice, never nil")
	}
	if b.Unread() != 0 {
		t.Error("an unreadable store reported unread messages")
	}

	// And the nil store, which is what a Service built without a data
	// directory holds.
	var nilBox *Inbox
	nilBox.Sink()(context.Background(), Notice{Message: "x"})
	if got := nilBox.List(0); len(got) != 0 {
		t.Error("a nil inbox returned messages")
	}
	if err := nilBox.MarkRead(); err != nil {
		t.Errorf("nil MarkRead: %v", err)
	}
}

func TestClearEmptiesIt(t *testing.T) {
	b := testInbox(t)
	raise(b, Notice{Message: "a"})
	if err := b.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := b.List(0); len(got) != 0 {
		t.Errorf("clear left %d behind", len(got))
	}
}

// The centre must not depend on the backend having come up: the daemon writes
// to this file from its own process, and a window with no LLM configured is
// exactly the one that has been missing what it said.
func TestTheStoreCanBeOpenedWithoutAService(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERAI_DESKTOP_HOME", home)

	daemon := NewInbox(filepath.Join(home, "data"))
	raise(daemon, Notice{Message: "raised by the other process"})

	app := OpenInbox()
	items := app.List(0)
	if len(items) != 1 || items[0].Message != "raised by the other process" {
		t.Fatalf("OpenInbox read %+v, want the message the daemon filed", items)
	}
}
