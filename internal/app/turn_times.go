package app

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// When each ask in a conversation was made and how long its answer took.
//
// agent-go keeps no time on a message, so a reopened conversation could say
// what was asked but not when, nor how long the hive took over it — which is
// the first thing anyone wants to know when it felt slow. Each finished turn
// appends one line here, and ChatHistory lays the lines back onto the turns.
//
// A line names its ask by a hash of the text rather than by position: the
// transcript can lose turns to compaction or gain ones the agent injected, and
// a position would then point at the wrong message.
//
// The line also says who answered, when it was not SuperAI's own agent: the
// queen engine's name, what that turn cost, and — for a turn SuperAI's agent
// took because the engine could not — which engine it stood in for.
type turnTime struct {
	Hash      string    `json:"h"`
	At        time.Time `json:"at"`
	ElapsedMs int64     `json:"ms"`
	turnBy
}

// turnBy is who answered a turn; the zero value is SuperAI's own agent.
type turnBy struct {
	Engine     string  `json:"e,omitempty"`
	CostUSD    float64 `json:"c,omitempty"`
	StoodInFor string  `json:"f,omitempty"`
	// BackupFor is the first-choice engine a backup Engine answered for.
	BackupFor string `json:"b,omitempty"`
}

var turnTimesMu sync.Mutex

func turnTimesPath(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(backend.DataDir(), "turn-times", hex.EncodeToString(sum[:12])+".jsonl")
}

func askHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:8])
}

// recordTurnTime notes one finished turn. Failing to write it loses a
// timestamp, never the turn, so errors are dropped.
func recordTurnTime(sessionID, ask string, at time.Time, elapsed time.Duration) {
	recordTurnTimeBy(sessionID, ask, at, elapsed, turnBy{})
}

// recordTurnTimeBy is recordTurnTime with who answered.
func recordTurnTimeBy(sessionID, ask string, at time.Time, elapsed time.Duration, by turnBy) {
	line, err := json.Marshal(turnTime{Hash: askHash(ask), At: at.UTC(), ElapsedMs: elapsed.Milliseconds(), turnBy: by})
	if err != nil {
		return
	}
	turnTimesMu.Lock()
	defer turnTimesMu.Unlock()
	p := turnTimesPath(sessionID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

func readTurnTimes(sessionID string) []turnTime {
	turnTimesMu.Lock()
	defer turnTimesMu.Unlock()
	f, err := os.Open(turnTimesPath(sessionID))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []turnTime
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var t turnTime
		if json.Unmarshal(sc.Bytes(), &t) == nil {
			out = append(out, t)
		}
	}
	return out
}

// withTurnTimes stamps each ask with when it was made and the answer after it
// with when it came and how long it took. Records are matched in order, so the
// same question asked twice gets each its own time.
func withTurnTimes(sessionID string, turns []backend.ChatTurn) []backend.ChatTurn {
	times := readTurnTimes(sessionID)
	if len(times) == 0 {
		return turns
	}
	next := 0
	var open *turnTime
	for i := range turns {
		t := &turns[i]
		if t.Kind != "" {
			continue
		}
		switch t.Role {
		case "user":
			open = nil
			h := askHash(t.Content)
			for j := next; j < len(times); j++ {
				if times[j].Hash == h {
					t.At = times[j].At.Format(time.RFC3339)
					open, next = &times[j], j+1
					break
				}
			}
		case "assistant":
			if open != nil {
				t.At = open.At.Add(time.Duration(open.ElapsedMs) * time.Millisecond).Format(time.RFC3339)
				t.ElapsedMs = open.ElapsedMs
				t.Engine, t.CostUSD, t.BackupFor, t.StandInFor = open.Engine, open.CostUSD, open.BackupFor, open.StoodInFor
				if open.StoodInFor != "" {
					t.Content = standInNote(open.StoodInFor) + t.Content
				}
				open = nil
			}
		}
	}
	return turns
}
