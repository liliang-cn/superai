package backend

// Electing a queen when the queen is gone.
//
// Every worker already beats against the queen; her answer now carries her
// term and the live roster, so every worker always holds the same list of who
// is in the hive. When the queen stops answering for hiveMisses beats in a
// row, each worker picks the next queen from that list on its own, by the
// same rule, and so they all pick the same one without asking each other:
// the member whose name hashes lowest with the next term. "Random" in that
// nobody is preferred; the same everywhere in that the term and the names
// are the same everywhere.
//
// The one picked turns itself into the queen (Announcer.OnPromote) under the
// new term; the others point their beats at it. A pick that does not take
// over in time is passed over the same way, with the term after.
//
// A queen that comes back — a pod restarted, a partition healed — hears the
// higher term from the first worker that reaches her (ErrSuperseded) and
// steps down.

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// ErrSuperseded is a queen's answer to a worker that has already followed a
// queen of a later term: this one is stale and should step down.
type ErrSuperseded struct {
	Term  int64
	Queen string
}

func (e ErrSuperseded) Error() string {
	return fmt.Sprintf("superseded: the hive is on term %d under %s", e.Term, e.Queen)
}

// IsSuperseded reports whether err says the queen was superseded.
func IsSuperseded(err error) (ErrSuperseded, bool) {
	var s ErrSuperseded
	ok := errors.As(err, &s)
	return s, ok
}

// electionScore is where a member falls in term's draw: lowest wins.
func electionScore(term int64, name string) uint64 {
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], uint64(term))
	sum := sha256.Sum256(append(t[:], []byte(strings.ToLower(name))...))
	return binary.BigEndian.Uint64(sum[:8])
}

// ElectQueen is term's pick from roster, passing over the names in skip:
// the member with the lowest draw. False when nobody is left to pick.
func ElectQueen(roster []RosterEntry, term int64, skip map[string]bool) (RosterEntry, bool) {
	var best RosterEntry
	var bestScore uint64
	found := false
	for _, m := range roster {
		if m.Name == "" || m.URL == "" || skip[m.Name] {
			continue
		}
		s := electionScore(term, m.Name)
		if !found || s < bestScore || (s == bestScore && m.Name < best.Name) {
			best, bestScore, found = m, s, true
		}
	}
	return best, found
}

// Term is the queen's term: 0 for a queen never elected (the one a hive
// starts with), higher after each election.
func (h *Hive) Term() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.term
}

// SetTerm records the term this queen was elected in.
func (h *Hive) SetTerm(t int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t > h.term {
		h.term = t
	}
}
