package app

import (
	"sort"
	"sync"
)

// openTools counts, per tool name, the calls a turn has announced and not yet
// had a result for. A turn that ends — answered, failed or stopped — with some
// still open has left them showing as running on every screen that follows the
// events, so the end of the turn closes them (closeEvents).
type openTools struct {
	mu sync.Mutex
	n  map[string]int
}

// call notes a tool_call event.
func (o *openTools) call(tool string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.n == nil {
		o.n = map[string]int{}
	}
	o.n[tool]++
}

// result notes a tool_result event.
func (o *openTools) result(tool string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.n[tool] > 0 {
		o.n[tool]--
	}
}

// closeEvents are the tool_result events that end every call still open, one
// per call, and empties the count. reason is what the person reads.
func (o *openTools) closeEvents(requestID, reason string) []map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	names := make([]string, 0, len(o.n))
	for name, c := range o.n {
		if c > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out []map[string]any
	for _, name := range names {
		for i := 0; i < o.n[name]; i++ {
			out = append(out, map[string]any{
				"requestId": requestID,
				"type":      "tool_result",
				"content":   reason,
				"tool":      name,
				"args":      nil,
				"result":    map[string]any{"ok": false, "error": reason, "closed": true},
				"debugType": "turn_ended",
			})
		}
	}
	o.n = nil
	return out
}
