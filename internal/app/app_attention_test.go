package app

import (
	"testing"
	"time"
)

func TestUpcomingMeetingsAreShownOnceWithinTheWeek(t *testing.T) {
	now := time.Date(2026, 10, 3, 18, 0, 0, 0, time.FixedZone("CST", 8*3600))
	schedules := []map[string]any{
		{"id": "a", "title": "与Rene的会议", "start_at": "2026-10-06T09:00:00+02:00", "participants": []any{"Rene"}, "location": "Vienna"},
		{"id": "b", "title": "和Rene开会", "start_at": "2026-10-06T09:00:00+02:00", "participants": []any{"Rene"}},
		{"id": "c", "title": "long gone", "start_at": "2026-09-01T09:00:00+02:00"},
		{"id": "d", "title": "too far", "start_at": "2026-11-01T09:00:00+02:00"},
		{"id": "e", "title": "bad time", "start_at": "next tuesday"},
	}
	got := upcomingEvents(schedules, now)
	if len(got) != 1 {
		t.Fatalf("want the one meeting once, got %+v", got)
	}
	m := got[0]
	if m.Ref != "a" || m.Zone != "+02:00" || m.Place != "Vienna" || m.Detail != "With Rene" || m.Level != "soon" {
		t.Fatalf("meeting: %+v", m)
	}
	if !m.At.Equal(time.Date(2026, 10, 6, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("time %v", m.At)
	}
}
