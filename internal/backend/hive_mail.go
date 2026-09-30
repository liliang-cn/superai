package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Messages between members of a hive.
//
// An order (hive_command, hive_map, hive_ask) starts a turn on the other side
// and waits for its answer. A message does neither: it is put in the
// recipient's inbox and the sender carries on. That is what lets a worker tell
// the queen something mid-order — "found the cause, it is the disk", "I am
// blocked on credentials" — or a worker hand a peer a fact it will want later,
// without either of them stopping to hold a conversation.
//
// A message goes straight to its recipient's own /api/hive/message, behind the
// same credential gate as everything else. A copy of one between two workers
// goes to the queen as well, marked observed, so the queen's picture of the
// hive shows it; the queen does not keep that copy.

// HiveQueenAlias is what a worker may call the queen without knowing her name.
const HiveQueenAlias = "queen"

// HiveBroadcast as a recipient means every other member.
const HiveBroadcast = "*"

// maxMessageText bounds one message. A message is a note, not a document; a
// document belongs in the shared memory, where the recipient can find it.
const maxMessageText = 8000

// HiveMessage is one note from one member to another.
type HiveMessage struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Text    string    `json:"text"`
	ReplyTo string    `json:"reply_to,omitempty"`
	At      time.Time `json:"at"`
	// Observed marks the queen's copy of a message between two workers: shown,
	// never put in anyone's inbox.
	Observed bool `json:"observed,omitempty"`
	// Read is the recipient's own bookkeeping and never travels.
	Read bool `json:"-"`
}

// NewHiveMessage stamps a message ready to send.
func NewHiveMessage(from, to, text, replyTo string) (HiveMessage, error) {
	text = strings.TrimSpace(text)
	switch {
	case text == "":
		return HiveMessage{}, errors.New("the message is empty")
	case len(text) > maxMessageText:
		return HiveMessage{}, fmt.Errorf("the message is %d bytes; keep it under %d, and put anything longer in the shared memory", len(text), maxMessageText)
	}
	return HiveMessage{
		ID: uuid.NewString(), From: strings.TrimSpace(from), To: strings.TrimSpace(to),
		Text: text, ReplyTo: strings.TrimSpace(replyTo), At: time.Now().UTC(),
	}, nil
}

// Valid says what is wrong with a message that came in over the wire.
func (m HiveMessage) Valid() error {
	switch {
	case m.ID == "":
		return errors.New("message has no id")
	case m.From == "" || m.To == "":
		return errors.New("message needs both from and to")
	case strings.TrimSpace(m.Text) == "":
		return errors.New("message is empty")
	case len(m.Text) > maxMessageText:
		return fmt.Errorf("message longer than %d bytes", maxMessageText)
	}
	return nil
}

// Mailbox is one member's inbox. In memory and bounded: a message is for the
// member that is running now, and one that restarts starts with an empty box.
type Mailbox struct {
	mu   sync.Mutex
	msgs []HiveMessage
	max  int
}

// NewMailbox keeps the last max messages.
func NewMailbox(max int) *Mailbox {
	if max <= 0 {
		max = 200
	}
	return &Mailbox{max: max}
}

// Put files a message. A message already filed — the same id sent twice by a
// retry — is not filed again; the result says whether this one was new.
func (b *Mailbox) Put(m HiveMessage) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, x := range b.msgs {
		if x.ID == m.ID {
			return false
		}
	}
	m.Read = false
	b.msgs = append(b.msgs, m)
	if len(b.msgs) > b.max {
		b.msgs = b.msgs[len(b.msgs)-b.max:]
	}
	return true
}

// Unread counts what has not been read.
func (b *Mailbox) Unread() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, m := range b.msgs {
		if !m.Read {
			n++
		}
	}
	return n
}

// Take returns the unread messages, oldest first, and marks them read. With
// all it returns the last limit messages whether read or not, and marks
// nothing. limit <= 0 means no limit.
func (b *Mailbox) Take(all bool, limit int) []HiveMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []HiveMessage{}
	for i := range b.msgs {
		if all || !b.msgs[i].Read {
			out = append(out, b.msgs[i])
			if !all {
				b.msgs[i].Read = true
			}
		}
	}
	if limit > 0 && len(out) > limit {
		if all {
			out = out[len(out)-limit:]
		} else {
			// Unread past the limit stay unread for the next call.
			for _, m := range out[limit:] {
				for i := range b.msgs {
					if b.msgs[i].ID == m.ID {
						b.msgs[i].Read = false
					}
				}
			}
			out = out[:limit]
		}
	}
	return out
}

// Get returns one message by id.
func (b *Mailbox) Get(id string) (HiveMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.msgs {
		if m.ID == id {
			return m, true
		}
	}
	return HiveMessage{}, false
}

// Mark sets whether a message has been read: read when it went into a turn,
// unread again when that turn ended without taking it in.
func (b *Mailbox) Mark(id string, read bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.msgs {
		if b.msgs[i].ID == id {
			b.msgs[i].Read = read
		}
	}
}

// TakeFrom returns the unread messages from one sender, oldest first, and
// marks them read.
func (b *Mailbox) TakeFrom(from string) []HiveMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []HiveMessage{}
	for i := range b.msgs {
		if !b.msgs[i].Read && b.msgs[i].From == from {
			out = append(out, b.msgs[i])
			b.msgs[i].Read = true
		}
	}
	return out
}

var mailHTTP = &http.Client{Timeout: 10 * time.Second}

// PostMessage delivers one message to a member at base, authenticating with
// token. The recipient's refusal comes back as the error, in its own words.
func PostMessage(ctx context.Context, base, token string, m HiveMessage) error {
	body, _ := json.Marshal(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/hive/message", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := mailHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return nil
}
