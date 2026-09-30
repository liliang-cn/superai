package backend

import (
	"strings"
	"testing"
)

func TestMailboxFilesOnceAndReadsInOrder(t *testing.T) {
	b := NewMailbox(3)
	m1, _ := NewHiveMessage("w1", "q", "one", "")
	m2, _ := NewHiveMessage("w2", "q", "two", "")
	if !b.Put(m1) || b.Put(m1) {
		t.Fatal("a retried message was filed twice")
	}
	b.Put(m2)
	if b.Unread() != 2 {
		t.Fatalf("unread %d", b.Unread())
	}
	got := b.Take(false, 1)
	if len(got) != 1 || got[0].Text != "one" || b.Unread() != 1 {
		t.Fatalf("limit did not leave the rest unread: %+v, %d", got, b.Unread())
	}
	if got := b.Take(false, 0); len(got) != 1 || got[0].Text != "two" {
		t.Fatalf("%+v", got)
	}
	if b.Unread() != 0 || len(b.Take(true, 0)) != 2 {
		t.Fatal("all should show read messages too")
	}
	for i := 0; i < 5; i++ {
		m, _ := NewHiveMessage("w1", "q", "x", "")
		b.Put(m)
	}
	if n := len(b.Take(true, 0)); n != 3 {
		t.Fatalf("the box kept %d, not its bound", n)
	}
}

func TestAMessageIsANoteNotADocument(t *testing.T) {
	if _, err := NewHiveMessage("a", "b", "  ", ""); err == nil {
		t.Fatal("an empty message was accepted")
	}
	_, err := NewHiveMessage("a", "b", strings.Repeat("x", maxMessageText+1), "")
	if err == nil || !strings.Contains(err.Error(), "shared memory") {
		t.Fatalf("a long message should point at the memory: %v", err)
	}
}
