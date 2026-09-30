package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
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
	// Via is "queen" on a message the queen passed on because its sender could
	// not reach the recipient: a worker outside the cluster cannot resolve the
	// in-cluster address a SuperAI worker is listed under, and the queen can
	// reach both.
	Via string `json:"via,omitempty"`
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
	// When a message last came in, and when a look found nothing: two looks
	// that found nothing with nothing arriving between them are someone
	// waiting on the inbox. See Empty.
	lastPut, lastEmpty time.Time
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
	b.lastPut = time.Now()
	b.msgs = append(b.msgs, m)
	if len(b.msgs) > b.max {
		b.msgs = b.msgs[len(b.msgs)-b.max:]
	}
	return true
}

// pollWindow is how close two empty looks have to be to count as polling.
const pollWindow = 5 * time.Minute

// Empty records a look that found nothing unread, and says what to tell the
// looker. The first time it is InboxEmpty. A second look with nothing arriving
// in between is a member waiting on its inbox — the one thing the first answer
// asked it not to do, and what workers in a relay did anyway, a check and a
// sleep in the shell a dozen times over — so it is told plainly to stop.
func (b *Mailbox) Empty() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	again := !b.lastEmpty.IsZero() && now.Sub(b.lastEmpty) < pollWindow && !b.lastPut.After(b.lastEmpty)
	b.lastEmpty = now
	if again {
		return InboxStillEmpty
	}
	return InboxEmpty
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

// mailHTTP never goes through a proxy from the environment: members are on
// the LAN or in the cluster, and a proxy that cannot reach one answers with an
// error of its own that reads as the recipient's.
var mailHTTP = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}

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

// HiveContact is somewhere a message can be delivered: a member's address and
// the bearer it accepts, or the queen, who is reached through the join.
type HiveContact struct {
	Name, URL, Token string
	Queen            bool
}

// Contacts is everyone a worker can write to: the queen, under her name once
// a welcome has given it, and the live peers.
func (a *Announcer) Contacts(ctx context.Context) map[string]HiveContact {
	out := map[string]HiveContact{}
	q := a.Queen()
	if q == "" {
		q = HiveQueenAlias
	}
	out[q] = HiveContact{Name: q, Queen: true}
	for n, ag := range a.Peers(ctx) {
		out[n] = HiveContact{Name: n, URL: ag.URL, Token: ag.Token}
	}
	return out
}

// SendHive delivers text from me to one contact, to the queen by role, or to
// every contact with "*", and says in a line per copy what became of it. The
// queen is reached through toQueen, everyone else at their own address;
// delivered is told of each copy that arrived.
func SendHive(ctx context.Context, me string, contacts map[string]HiveContact, to, text, replyTo string,
	toQueen func(context.Context, HiveMessage) error, delivered func(HiveContact, HiveMessage)) (string, error) {
	to = strings.TrimSpace(to)
	var targets []HiveContact
	switch {
	case to == HiveBroadcast:
		for _, c := range contacts {
			targets = append(targets, c)
		}
		sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
		if len(targets) == 0 {
			return "Nobody else is in the hive right now.", nil
		}
	case to == me:
		return "", fmt.Errorf("%s is you", to)
	default:
		c, found := contacts[to]
		if !found && to == HiveQueenAlias {
			for _, x := range contacts {
				if x.Queen {
					c, found = x, true
				}
			}
		}
		if !found {
			names := make([]string, 0, len(contacts))
			for n := range contacts {
				names = append(names, n)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return "", fmt.Errorf("there is nobody called %q, and nobody else is in the hive right now", to)
			}
			return "", fmt.Errorf("there is nobody called %q; you can write to: %s", to, strings.Join(names, ", "))
		}
		targets = []HiveContact{c}
	}

	lines := make([]string, len(targets))
	var wg sync.WaitGroup
	for i, c := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := NewHiveMessage(me, c.Name, text, replyTo)
			if err != nil {
				lines[i] = fmt.Sprintf("%s: not sent — %v", c.Name, err)
				return
			}
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			switch {
			case c.Queen && toQueen != nil:
				err = toQueen(sctx, m)
			case c.Queen:
				err = errors.New("no way to reach the queen from here")
			default:
				err = PostMessage(sctx, c.URL, c.Token, m)
				// Not reachable from here — an address that does not resolve
				// or a host that does not answer — is what the queen is for:
				// she can reach everyone on her roster. A refusal from the
				// recipient itself is not retried: it has answered.
				var ne net.Error
				var dnsErr *net.DNSError
				var opErr *net.OpError
				if err != nil && toQueen != nil && (errors.As(err, &dnsErr) || errors.As(err, &opErr) || (errors.As(err, &ne) && ne.Timeout())) {
					m.Via = "queen"
					// A clock of its own: an address that hung may have
					// spent the first one.
					qctx, qcancel := context.WithTimeout(ctx, 10*time.Second)
					err = toQueen(qctx, m)
					qcancel()
				}
			}
			if err != nil {
				lines[i] = fmt.Sprintf("%s: not delivered — %v", c.Name, err)
				return
			}
			lines[i] = fmt.Sprintf("%s: delivered (id %s)", c.Name, m.ID)
			if delivered != nil {
				delivered(c, m)
			}
		}()
	}
	wg.Wait()
	return strings.Join(lines, "\n"), nil
}
