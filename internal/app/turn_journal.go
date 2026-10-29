package app

// The turn journal: every chat turn this app runs, as it ran.
//
// Each turn is taken from the events every client is sent (emit), so no path
// that answers a person can be missed: the queen's own model, an engine, a
// SuperLeo answering in an engine's place, a worker. Each step is logged as it
// happens ("turn 1a2b3c4d …"), and the last turns are kept in memory with
// their timelines for TurnTraces / TurnTrace:
//
//	POST /api/rpc/TurnTraces  [20]
//	POST /api/rpc/TurnTrace   ["<requestId>"]
//
// A turn that has said nothing for a while is logged as quiet, with what it
// last did, so one that hangs is seen in the log and not only as a "Thinking…"
// that never ends on someone's screen.

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	journalTurns     = 200
	journalEvents    = 300
	journalQuietTick = 30 * time.Second
	journalQuietFor  = time.Minute
)

// TraceEvent is one step of a turn.
type TraceEvent struct {
	At string `json:"at"`
	// Ms is how long after the turn started this step came.
	Ms     int64  `json:"ms"`
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Engine string `json:"engine,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Text   string `json:"text,omitempty"`
}

// TurnTrace is a turn as the journal saw it.
type TurnTrace struct {
	ID      string `json:"id"`
	Session string `json:"session,omitempty"`
	Message string `json:"message,omitempty"`
	Started string `json:"started"`
	// State is running, done, error or cancelled.
	State string `json:"state"`
	// Ms is how long the turn took, or has taken so far.
	Ms int64 `json:"ms"`
	// FirstWordsMs is when its answer began to arrive.
	FirstWordsMs int64 `json:"firstWordsMs,omitempty"`
	// QuietMs is how long since its last step, while it runs.
	QuietMs int64 `json:"quietMs,omitempty"`
	// Engines is who was given the turn, in order: an engine, the next one
	// when it could not, "superleo" when SuperLeo stood in.
	Engines []string `json:"engines,omitempty"`
	// By is who answered it, as a person reads it.
	By      string       `json:"by,omitempty"`
	Tools   int          `json:"tools,omitempty"`
	Chars   int          `json:"chars,omitempty"`
	CostUSD float64      `json:"costUsd,omitempty"`
	Error   string       `json:"error,omitempty"`
	Events  []TraceEvent `json:"events,omitempty"`

	started, last time.Time
	quietSaid     bool
}

type turnJournal struct {
	mu    sync.Mutex
	turns map[string]*TurnTrace
	order []string
	once  sync.Once
}

func (a *App) journal() *turnJournal {
	j := &a.turnLog
	j.once.Do(func() {
		j.turns = map[string]*TurnTrace{}
		go j.watchQuiet()
	})
	return j
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (j *turnJournal) note(t *TurnTrace, now time.Time, e TraceEvent) {
	e.At, e.Ms = now.Format(time.RFC3339Nano), now.Sub(t.started).Milliseconds()
	t.last, t.quietSaid = now, false
	if len(t.Events) < journalEvents {
		t.Events = append(t.Events, e)
	}
}

// record takes one event sent to the clients into the journal.
func (j *turnJournal) record(name string, p map[string]any) {
	if !strings.HasPrefix(name, "chat:") {
		return
	}
	id, _ := p["requestId"].(string)
	if id == "" {
		return
	}
	str := func(k string) string { s, _ := p[k].(string); return s }
	now := time.Now()
	j.mu.Lock()
	defer j.mu.Unlock()
	t := j.turns[id]
	if t == nil {
		t = &TurnTrace{ID: id, State: "running", started: now, last: now, Started: now.Format(time.RFC3339)}
		j.turns[id] = t
		j.order = append(j.order, id)
		if len(j.order) > journalTurns {
			delete(j.turns, j.order[0])
			j.order = j.order[1:]
		}
	}
	if s := str("sessionId"); s != "" && t.Session == "" {
		t.Session = s
	}
	// An engine that could not take the turn names itself as it hands it on,
	// though it may have said nothing before.
	if f := str("failed"); f != "" && !slices.Contains(t.Engines, f) {
		t.Engines = append(t.Engines, f)
	}
	engine := str("engine")
	who := engine
	if f := str("standInFor"); f != "" {
		who = queenNowSuperLeo
	}
	if who != "" && (len(t.Engines) == 0 || t.Engines[len(t.Engines)-1] != who) {
		t.Engines = append(t.Engines, who)
	}
	// A step that does not say whose it is belongs to whoever has the turn.
	if who == "" {
		who = lastOf(t.Engines)
	}
	turn := shortID(id)
	switch name {
	case "chat:turn":
		t.Message = cliClip(str("message"), 200)
		j.note(t, now, TraceEvent{Name: name, Text: t.Message})
		slog.Info("turn started", "turn", turn, "session", t.Session, "message", cliClip(str("message"), 80))
	case "chat:event":
		typ := str("type")
		switch typ {
		case "partial":
			c := str("content")
			// The stand-in note SuperLeo or the next engine opens with is not
			// its answer starting.
			if str("standInFor") != "" || str("inPlaceOf") != "" {
				break
			}
			if t.Chars == 0 && c != "" {
				t.FirstWordsMs = now.Sub(t.started).Milliseconds()
				j.note(t, now, TraceEvent{Name: name, Type: "first_words", Engine: who})
				slog.Info("turn first words", "turn", turn, "after", now.Sub(t.started).Round(100*time.Millisecond), "engine", who)
			}
			t.Chars += len([]rune(c))
			t.last, t.quietSaid = now, false
		case "tool_call":
			t.Tools++
			tool := str("tool")
			j.note(t, now, TraceEvent{Name: name, Type: typ, Engine: who, Tool: tool})
			slog.Debug("turn tool call", "turn", turn, "tool", tool, "engine", who)
		case "tool_result":
			ok := true
			if r, _ := p["result"].(map[string]any); r != nil {
				if v, has := r["ok"].(bool); has {
					ok = v
				}
			}
			e := TraceEvent{Name: name, Type: typ, Engine: who, Tool: str("tool")}
			if !ok {
				e.Text = "failed"
				slog.Warn("turn tool failed", "turn", turn, "tool", e.Tool, "engine", who)
			}
			j.note(t, now, e)
		case "model_wire":
			// Every step goes in the timeline; the log keeps the ones worth a
			// line: a queue, an error, a retry, a restart, a call gone quiet.
			c := cliClip(str("content"), 300)
			j.note(t, now, TraceEvent{Name: name, Type: typ, Engine: who, Text: c})
			if wireWorthALine(p["wire"]) {
				slog.Warn("turn model call", "turn", turn, "wire", cliClip(c, 240), "engine", who)
			}
		case "state_update", "workflow_error", "workflow_blocked":
			c := cliClip(str("content"), 300)
			j.note(t, now, TraceEvent{Name: name, Type: typ, Engine: who, Text: c})
			level := slog.LevelInfo
			if typ != "state_update" {
				level = slog.LevelWarn
			}
			slog.Log(context.Background(), level, "turn "+typ, "turn", turn, "content", cliClip(c, 200), "engine", who)
		default:
			// thinking, traces, workflow steps: a step, not worth a line.
			t.last, t.quietSaid = now, false
		}
	case "chat:done", "chat:error", "chat:cancelled":
		t.State = map[string]string{"chat:done": "done", "chat:error": "error", "chat:cancelled": "cancelled"}[name]
		t.Ms = now.Sub(t.started).Milliseconds()
		if c, ok := p["costUsd"].(float64); ok {
			t.CostUSD = c
		}
		t.By = turnByline(engine, str("standInFor"))
		e := TraceEvent{Name: name, Engine: who}
		if t.State == "error" {
			t.Error = cliClip(str("error"), 500)
			e.Text = t.Error
		}
		j.note(t, now, e)
		attrs := []any{"turn", turn, "state", t.State, "took", now.Sub(t.started).Round(100 * time.Millisecond)}
		if t.By != "" {
			attrs = append(attrs, "by", t.By)
		}
		if len(t.Engines) > 1 {
			attrs = append(attrs, "tried", strings.Join(t.Engines, " → "))
		}
		if t.Tools > 0 {
			attrs = append(attrs, "tools", t.Tools)
		}
		if t.CostUSD > 0 {
			attrs = append(attrs, "cost_usd", fmt.Sprintf("%.4f", t.CostUSD))
		}
		level := slog.LevelInfo
		if t.Error != "" {
			level = slog.LevelWarn
			attrs = append(attrs, "error", cliClip(t.Error, 200))
		}
		slog.Log(context.Background(), level, "turn "+t.State, attrs...)
	}
}

// wireWorthALine is whether a model call's wire step is logged: not the
// request, the first bytes or a plain 2xx of a call going well, nor a slot
// had at once.
func wireWorthALine(raw any) bool {
	w, _ := raw.(map[string]any)
	kind, _ := w["kind"].(string)
	num := func(k string) float64 {
		switch v := w[k].(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		}
		return 0
	}
	switch kind {
	case "request", "first_byte":
		return false
	case "response":
		return num("status") >= 400
	case "slot":
		return num("wait_ms") >= 1000
	}
	return kind != ""
}

// turnByline is who answered, as the log and the trace say it.
func turnByline(engine, standInFor string) string {
	switch {
	case standInFor != "":
		return "SuperLeo, in place of " + standInFor
	case engine != "":
		return engine
	}
	return "SuperLeo"
}

// watchQuiet logs a turn that has gone quiet, once each time it does.
func (j *turnJournal) watchQuiet() {
	for range time.Tick(journalQuietTick) {
		for _, q := range j.sweepQuiet(time.Now()) {
			slog.Warn("turn quiet", "turn", q.Turn, "quiet", q.Quiet, "running", q.Running, "last", q.Last, "engine", q.Engine)
		}
	}
}

// quietTurn is a running turn that has gone quiet: how long for, how long it
// has been running, and the last thing it did and who did it.
type quietTurn struct {
	Turn           string
	Quiet, Running time.Duration
	Last, Engine   string
}

// sweepQuiet is what watchQuiet says at now: one for each running turn that
// has gone quiet since it last said one.
func (j *turnJournal) sweepQuiet(now time.Time) []quietTurn {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []quietTurn
	for _, id := range j.order {
		t := j.turns[id]
		if t.State != "running" || t.quietSaid || now.Sub(t.last) < journalQuietFor {
			continue
		}
		t.quietSaid = true
		last := "nothing since it started"
		if n := len(t.Events); n > 0 {
			e := t.Events[n-1]
			last = strings.Join(strings.Fields(e.Name+" "+e.Type+" "+e.Tool+" "+cliClip(e.Text, 80)), " ")
		}
		out = append(out, quietTurn{Turn: shortID(id), Quiet: now.Sub(t.last).Round(time.Second),
			Running: now.Sub(t.started).Round(time.Second), Last: last, Engine: lastOf(t.Engines)})
	}
	return out
}

func lastOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

func (j *turnJournal) view(t *TurnTrace, events bool) TurnTrace {
	out := *t
	out.Engines = slices.Clone(t.Engines)
	if t.State == "running" {
		out.Ms = time.Since(t.started).Milliseconds()
		out.QuietMs = time.Since(t.last).Milliseconds()
	}
	if events {
		out.Events = slices.Clone(t.Events)
	} else {
		out.Events = nil
	}
	return out
}

// TurnTraces is the latest turns, newest first, without their steps: what
// was asked, who was given it and who answered, how long it took, and for
// one still running how long it has been quiet.
func (a *App) TurnTraces(limit int) []TurnTrace {
	j := a.journal()
	j.mu.Lock()
	defer j.mu.Unlock()
	if limit <= 0 || limit > len(j.order) {
		limit = len(j.order)
	}
	out := make([]TurnTrace, 0, limit)
	for i := len(j.order) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, j.view(j.turns[j.order[i]], false))
	}
	return out
}

// TurnTrace is one turn with every step it took, by its request id (or the
// first characters of it, as the log prints it).
func (a *App) TurnTrace(id string) (TurnTrace, error) {
	j := a.journal()
	j.mu.Lock()
	defer j.mu.Unlock()
	id = strings.TrimSpace(id)
	if t := j.turns[id]; t != nil {
		return j.view(t, true), nil
	}
	if id != "" {
		for i := len(j.order) - 1; i >= 0; i-- {
			if strings.HasPrefix(j.order[i], id) {
				return j.view(j.turns[j.order[i]], true), nil
			}
		}
	}
	return TurnTrace{}, fmt.Errorf("no turn %q in the last %d", id, journalTurns)
}
