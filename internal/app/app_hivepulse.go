package app

// The meter for the whole hive, not one process.
//
// Every process has its own Pulse; the Stats page used to show only the one it
// was served by, which on the queen is the process that gives orders and does
// little of the work. Each agent on the agent link now sends a summary of its
// meter every two seconds ("agent:pulse"); core keeps the latest one per agent
// and adds its own. Frames are not forwarded: five a second from every member
// is the line's whole budget, and the page needs totals and who is doing what,
// not every member's histogram.

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

// MemberPulse is one member's meter, in brief.
type MemberPulse struct {
	Name   string  `json:"name"`
	Live   bool    `json:"live"`
	Tokens int     `json:"tokens"`
	Cached int     `json:"cached"`
	Rounds int     `json:"rounds"`
	Calls  int     `json:"calls"`
	Fails  int     `json:"fails"`
	MCP    int     `json:"mcp"`
	CPU    float64 `json:"cpu"`
	Heap   uint64  `json:"heap"`
	// TokPerSec is the mean over the last thirty seconds.
	TokPerSec float64 `json:"tokPerSec"`
	// Doing is what its busiest open run is doing ("thinking", a tool name),
	// with that run's round; empty when idle.
	Doing string `json:"doing,omitempty"`
	Round int    `json:"round,omitempty"`
	At    string `json:"at"`
}

// HivePulse is every member's meter and their sum.
type HivePulse struct {
	Members []MemberPulse `json:"members"`
	Total   MemberPulse   `json:"total"`
}

// pulseSummary boils a snapshot down to what travels.
func pulseSummary(name string, s backend.PulseSnapshot) MemberPulse {
	m := MemberPulse{Name: name, Live: s.Live, Tokens: s.Tokens, Cached: s.Cached, Rounds: s.Rounds,
		Calls: s.Calls, Fails: s.Fails, MCP: s.MCP, CPU: s.CPU, Heap: s.Heap, At: s.Now}
	if n := len(s.Bins); n > 0 {
		from, sum := n-30, 0
		if from < 0 {
			from = 0
		}
		for _, b := range s.Bins[from:] {
			sum += b.Tokens
		}
		m.TokPerSec = float64(sum) / float64(n-from)
	}
	for _, r := range s.Runs {
		if r.Doing != "" && (m.Doing == "" || r.Doing != "waiting") {
			m.Doing, m.Round = r.Doing, r.Round
		}
	}
	return m
}

// sendPulseSummaries is the agent's side: its meter, every two seconds, while
// linked. A member with nothing new still sends, so core can tell quiet from
// gone.
func (a *App) sendPulseSummaries(ctx context.Context, name string, send func(string, map[string]any)) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			raw, _ := json.Marshal(pulseSummary(name, a.livePulse().Snapshot()))
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			send("agent:pulse", payload)
		}
	}
}

// notePulse is core's side: keep the latest summary from an agent and tell the
// pages.
func (a *App) notePulse(agent string, payloadJSON string) {
	var m MemberPulse
	if json.Unmarshal([]byte(payloadJSON), &m) != nil {
		return
	}
	m.Name = agent
	a.memberPulses.Store(agent, m)
	a.emit("hive:meter", map[string]any{"member": m})
}

// HivePulse is the whole hive's meter: this process and every linked agent
// that has reported in the last ten seconds, busiest first.
func (a *App) HivePulse() HivePulse {
	self := pulseSummary("queen", a.livePulse().Snapshot())
	if h := a.hiveSelfName(); h != "" {
		self.Name = h
	}
	out := HivePulse{Members: []MemberPulse{self}}
	linked := map[string]bool{}
	for _, c := range a.agents().list() {
		linked[c.name()] = true
	}
	cutoff := time.Now().Add(-10 * time.Second)
	a.memberPulses.Range(func(k, v any) bool {
		m := v.(MemberPulse)
		at, _ := time.Parse(time.RFC3339Nano, m.At)
		if !linked[m.Name] || at.Before(cutoff) {
			return true
		}
		out.Members = append(out.Members, m)
		return true
	})
	sort.SliceStable(out.Members[1:], func(i, j int) bool {
		x, y := out.Members[1+i], out.Members[1+j]
		if x.Live != y.Live {
			return x.Live
		}
		return x.Tokens > y.Tokens
	})
	t := MemberPulse{Name: "hive"}
	for _, m := range out.Members {
		t.Live = t.Live || m.Live
		t.Tokens += m.Tokens
		t.Cached += m.Cached
		t.Rounds += m.Rounds
		t.Calls += m.Calls
		t.Fails += m.Fails
		t.MCP += m.MCP
		t.TokPerSec += m.TokPerSec
	}
	out.Total = t
	return out
}

// hiveSelfName is this process's role in the hive ("queen"), or "" outside one.
func (a *App) hiveSelfName() string {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	if s == nil {
		return ""
	}
	return s.Hive.Role
}
