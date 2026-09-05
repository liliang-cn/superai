package backend

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The notification centre: what SuperAI said while you were not looking.
//
// Every message already goes through Notices (notice.go), and until now every
// surface it reached was a surface you had to be watching. A toast fades. The
// banner is replaced by the next one. The webhook leaves the machine entirely.
// So a scheduled run that finished at three in the morning, a reminder that
// came due, a task that failed — each was announced exactly once, to nobody,
// and then gone.
//
// The inbox is one more subscriber, and the only one that keeps what it is
// given. It is the difference between an app that tells you things and an app
// you can ask what it told you.
//
// It is written to disk rather than held in memory because the case it exists
// for is the case where the process was restarted, or was never the one that
// raised the notice: the scheduler daemon and the app take turns holding the
// timer, so half of what is worth reading was raised by the process you are
// not talking to.

// InboxFile is where the notification centre keeps its messages, under the
// data directory.
const InboxFile = "notifications.json"

// inboxCap is how many messages are kept.
//
// A notification centre is a recent history, not an archive: past a few
// hundred, nobody scrolls, and the ones that matter are the ones near the top.
// The cap is what stops a nightly schedule from growing the file forever.
const inboxCap = 300

// Notification is one notice, kept.
type Notification struct {
	ID      string `json:"id"`
	Level   Level  `json:"level"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
	// Session is the conversation this came out of, for opening it from the
	// list.
	Session string `json:"session,omitempty"`
	At      string `json:"at"`
	Read    bool   `json:"read"`
}

// Inbox is the store behind the notification centre.
type Inbox struct {
	mu   sync.Mutex
	path string
}

// OpenInbox opens the store without a Service.
//
// The centre is a file, not a service, and this is what makes that matter: a
// window whose LLM is not configured yet — or one whose backend failed to
// start — is precisely a window that should still be able to show what the
// scheduler daemon has been raising in its own process. Telling that user
// "nothing yet" would be a lie about the one surface whose whole job is to
// have kept things.
func OpenInbox() *Inbox {
	return NewInbox(filepath.Join(DataDir(), "data"))
}

// NewInbox opens (or creates) the store under dir.
func NewInbox(dir string) *Inbox {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return &Inbox{path: filepath.Join(dir, InboxFile)}
}

// Sink is the Notices subscriber that files everything it is given.
//
// Everything, including the messages a toast would not be worth: the whole
// point is being able to look back, and a filter here would be this file
// deciding on the reader's behalf what they were allowed to have missed. What
// is worth interrupting somebody for is a separate question, already answered
// by Notice.Push and read by the surfaces that leave the machine.
func (b *Inbox) Sink() Subscriber {
	if b == nil {
		return func(context.Context, Notice) {}
	}
	return func(_ context.Context, n Notice) {
		if strings.TrimSpace(n.Message) == "" {
			return
		}
		if err := b.add(n); err != nil {
			// A notification centre that cannot write is not a reason to lose
			// the notice — it has already reached the other surfaces.
			logInboxError(err)
		}
	}
}

func (b *Inbox) add(n Notice) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	items, err := b.readLocked()
	if err != nil {
		return err
	}

	id := strings.TrimSpace(n.Key)
	if id == "" {
		// No key means nothing to deduplicate against, so the timestamp and
		// the text are the identity. Two identical messages in the same second
		// are the same message reported twice.
		id = n.At + "|" + n.Message
	}
	entry := Notification{
		ID: id, Level: n.Level, Title: n.Title, Message: n.Message,
		Source: n.Source, Session: n.Session, At: n.At,
	}
	if entry.At == "" {
		entry.At = time.Now().Format(time.RFC3339)
	}

	// A key that is already here replaces, exactly as it does for toasts: a run
	// that reports twice is one entry that changed, not two that disagree. It
	// goes back to unread, because a message whose content changed is news
	// again.
	replaced := false
	for i := range items {
		if items[i].ID == entry.ID {
			items[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, entry)
	}
	return b.writeLocked(items)
}

// List returns the kept messages, newest first.
func (b *Inbox) List(limit int) []Notification {
	if b == nil {
		return []Notification{}
	}
	b.mu.Lock()
	items, err := b.readLocked()
	b.mu.Unlock()
	if err != nil {
		return []Notification{}
	}
	// Newest first: a notification centre is read from the top.
	sort.SliceStable(items, func(i, j int) bool { return items[i].At > items[j].At })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		return []Notification{}
	}
	return items
}

// Unread is how many are unread, for the badge.
func (b *Inbox) Unread() int {
	n := 0
	for _, item := range b.List(0) {
		if !item.Read {
			n++
		}
	}
	return n
}

// MarkRead marks the given ids read, or every message when none are given.
func (b *Inbox) MarkRead(ids ...string) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	items, err := b.readLocked()
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for i := range items {
		if len(want) == 0 || want[items[i].ID] {
			items[i].Read = true
		}
	}
	return b.writeLocked(items)
}

// Clear empties the centre.
func (b *Inbox) Clear() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeLocked(nil)
}

// readLocked re-reads from disk every time, and that is the point.
//
// The app and the scheduler daemon are separate processes taking turns at the
// same timer, so either may have written since this one last looked. An
// in-memory copy would show whichever half of the messages this process
// happened to raise itself.
func (b *Inbox) readLocked() ([]Notification, error) {
	raw, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []Notification
	if err := json.Unmarshal(raw, &items); err != nil {
		// A file that cannot be parsed is a file that would otherwise block
		// every future notice. Starting over loses the history and keeps the
		// feature; refusing loses both.
		logInboxError(err)
		return nil, nil
	}
	return items, nil
}

func (b *Inbox) writeLocked(items []Notification) error {
	if len(items) > inboxCap {
		sort.SliceStable(items, func(i, j int) bool { return items[i].At > items[j].At })
		items = items[:inboxCap]
	}
	if items == nil {
		items = []Notification{}
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return err
	}
	// Written beside and renamed: a reader in the other process must never see
	// half a file, and rename is the only way to promise that.
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

// logInboxError reports a store failure without letting it reach the caller.
//
// Every caller here is on the path of some other message being delivered, and
// a notification centre that broke a scheduled run's own notice by failing to
// file it would be worse than one that quietly loses a row.
func logInboxError(err error) {
	if err != nil {
		log.Printf("superai: notification centre: %v", err)
	}
}
