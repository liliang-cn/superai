package backend

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
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

// A peer listed under an address that does not resolve from here — a SuperAI
// worker's in-cluster name, seen from a worker outside the cluster — is
// reached through the queen instead.
func TestAnUnreachablePeerIsReachedThroughTheQueen(t *testing.T) {
	// An address nothing answers at: a port that was just closed.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + ln.Addr().String()
	ln.Close()
	contacts := map[string]HiveContact{
		"queen":            {Name: "queen", Queen: true},
		"superai-worker-7": {Name: "superai-worker-7", URL: dead},
	}
	var viaQueen []HiveMessage
	toQueen := func(_ context.Context, m HiveMessage) error { viaQueen = append(viaQueen, m); return nil }
	var seen []HiveMessage
	out, err := SendHive(context.Background(), "openclaw", contacts, "superai-worker-7", "what is your hostname?", "", toQueen,
		func(_ HiveContact, m HiveMessage) { seen = append(seen, m) })
	if err != nil || !strings.Contains(out, "delivered") {
		t.Fatalf("out %q err %v", out, err)
	}
	if len(viaQueen) != 1 || viaQueen[0].To != "superai-worker-7" || viaQueen[0].Via != "queen" {
		t.Fatalf("not handed to the queen: %+v", viaQueen)
	}
	if len(seen) != 1 || seen[0].Via != "queen" {
		t.Fatalf("the sender was not told it went through the queen: %+v", seen)
	}
}

// Looking twice with nothing arriving in between is waiting on the inbox, and
// the second answer says to stop; a message in between resets it.
func TestLookingAgainAtAnEmptyInboxIsToldToStop(t *testing.T) {
	b := NewMailbox(10)
	if got := b.Empty(); got != InboxEmpty {
		t.Fatalf("first look: %q", got)
	}
	if got := b.Empty(); got != InboxStillEmpty {
		t.Fatalf("second look with nothing new: %q", got)
	}
	m, _ := NewHiveMessage("w1", "w2", "leg 1", "")
	b.Put(m)
	b.Take(false, 0)
	if got := b.Empty(); got != InboxEmpty {
		t.Fatalf("a look after something arrived was called polling: %q", got)
	}
}

func TestAWaitReturnsTheMomentTheMessageArrives(t *testing.T) {
	b := NewMailbox(10)
	got := make(chan []HiveMessage, 1)
	go func() { got <- b.Wait(context.Background(), "w1", time.Minute) }()
	for !b.Waiting("w1") {
		time.Sleep(time.Millisecond)
	}
	if b.Waiting("w9") {
		t.Fatal("a wait for w1 claims w9's mail")
	}
	other, _ := NewHiveMessage("w9", "w2", "not this one", "")
	b.Put(other)
	m, _ := NewHiveMessage("w1", "w2", "leg 1: 18e99f0d6bff", "")
	start := time.Now()
	b.Put(m)
	select {
	case msgs := <-got:
		if len(msgs) != 1 || msgs[0].Text != "leg 1: 18e99f0d6bff" {
			t.Fatalf("got %+v", msgs)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("took %s after the message was filed", time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait never returned")
	}
	if b.Waiting("w1") {
		t.Fatal("still registered as waiting after it returned")
	}
	if b.Unread() != 1 {
		t.Fatalf("the wait took more than it waited for: %d unread", b.Unread())
	}
}

func TestAWaitForAnyoneTakesWhatIsAlreadyThere(t *testing.T) {
	b := NewMailbox(10)
	m, _ := NewHiveMessage("w3", "w2", "already here", "")
	b.Put(m)
	if msgs := b.Wait(context.Background(), "", time.Minute); len(msgs) != 1 {
		t.Fatalf("got %+v", msgs)
	}
}

func TestAWaitEndsAtItsDeadlineWithNothing(t *testing.T) {
	b := NewMailbox(10)
	start := time.Now()
	if msgs := b.Wait(context.Background(), "", 50*time.Millisecond); msgs != nil {
		t.Fatalf("got %+v", msgs)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not keep to its deadline")
	}
	if out := WaitResult(b, context.Background(), "w1", float64(0.05)); !strings.Contains(out, "Nothing from w1") {
		t.Fatalf("WaitResult said %q", out)
	}
}
