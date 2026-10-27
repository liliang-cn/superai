package backend

// Today's totals, kept across restarts.
//
// The meter's totals — tokens, rounds, tool calls and the rest — used to be
// this process's, and every restart of the app or the pod put them back to 0:
// "the tokens and the rounds are gone again". They are now today's: written
// to a small file every few seconds while they change, read back when the
// process starts on the same day, and begun again at local midnight. What is
// about this instant — the runs in flight, CPU, heap, the two-minute
// histogram — is still the process's own.

import (
	"encoding/json"
	"os"
	"time"
)

// pulseKeepEvery is how often changed totals are written.
const pulseKeepEvery = 10 * time.Second

type pulseTotals struct {
	Day    string `json:"day"`
	Tokens int    `json:"tokens"`
	Cached int    `json:"cached"`
	Rounds int    `json:"rounds"`
	Calls  int    `json:"calls"`
	Reads  int    `json:"reads"`
	Writes int    `json:"writes"`
	Shells int    `json:"shells"`
	Fails  int    `json:"fails"`
	MCP    int    `json:"mcp"`
	Memory int    `json:"memory"`
	Errors int    `json:"errors"`
}

func pulseDay(t time.Time) string { return t.Local().Format("2006-01-02") }

// Keep makes the totals today's, kept at path: what it holds for today is
// where the counting goes on from. Call it before Start.
func (p *Pulse) Keep(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keepPath = path
	p.day = pulseDay(time.Now())
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var t pulseTotals
	if json.Unmarshal(raw, &t) != nil || t.Day != p.day {
		return
	}
	p.tokens, p.cached, p.rounds, p.calls = t.Tokens, t.Cached, t.Rounds, t.Calls
	p.reads, p.writes, p.shells, p.fails = t.Reads, t.Writes, t.Shells, t.Fails
	p.mcp, p.memory, p.errors = t.MCP, t.Memory, t.Errors
}

// totalsLocked is the totals as they are kept. Caller holds p.mu.
func (p *Pulse) totalsLocked() pulseTotals {
	return pulseTotals{Day: p.day, Tokens: p.tokens, Cached: p.cached, Rounds: p.rounds, Calls: p.calls,
		Reads: p.reads, Writes: p.writes, Shells: p.shells, Fails: p.fails, MCP: p.mcp, Memory: p.memory, Errors: p.errors}
}

// rollLocked begins the totals again when the day has changed. Caller holds
// p.mu.
func (p *Pulse) rollLocked(now time.Time) {
	if p.keepPath == "" {
		return
	}
	if d := pulseDay(now); d != p.day {
		p.day = d
		p.tokens, p.cached, p.rounds, p.calls = 0, 0, 0, 0
		p.reads, p.writes, p.shells, p.fails = 0, 0, 0, 0
		p.mcp, p.memory, p.errors = 0, 0, 0
		p.dirty = true
	}
}

// keep writes the totals when they changed since the last write.
func (p *Pulse) keep(force bool) {
	p.mu.Lock()
	if p.keepPath == "" {
		p.mu.Unlock()
		return
	}
	t := p.totalsLocked()
	if !force && t == p.kept {
		p.mu.Unlock()
		return
	}
	p.kept = t
	path := p.keepPath
	p.mu.Unlock()
	raw, _ := json.Marshal(t)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}
