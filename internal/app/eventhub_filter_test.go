package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// A subscriber that names the events it wants gets those and nothing else;
// one that names none still gets everything.
func TestEventHubFiltersByNamePrefix(t *testing.T) {
	h := newEventHub()
	watch, offWatch := h.subscribe(parseEventFilter(httptest.NewRequest("GET", "/api/events?only=chat:,tool:approval", nil)))
	defer offWatch()
	all, offAll := h.subscribe(parseEventFilter(httptest.NewRequest("GET", "/api/events", nil)))
	defer offAll()

	h.broadcast("hive:meter", map[string]any{"n": 1})
	h.broadcast("pulse:frame", map[string]any{"n": 2})
	h.broadcast("chat:event", map[string]any{"type": "partial"})
	h.broadcast("tool:approval:closed", map[string]any{"id": "a"})

	names := func(ch <-chan []byte) []string {
		var out []string
		for {
			select {
			case b := <-ch:
				var e struct{ Name string }
				_ = json.Unmarshal(b, &e)
				out = append(out, e.Name)
			default:
				return out
			}
		}
	}
	if got := names(watch); len(got) != 2 || got[0] != "chat:event" || got[1] != "tool:approval:closed" {
		t.Fatalf("filtered subscriber got %v", got)
	}
	if got := names(all); len(got) != 4 {
		t.Fatalf("unfiltered subscriber got %v, want all four", got)
	}
}
