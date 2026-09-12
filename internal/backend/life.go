package backend

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The life-assistant writes, as methods rather than tool closures.
//
// Two callers need them now: the agent, through add_schedule / add_record, and
// a button the model drew in a `ui` block, through the App methods of the same
// name. They must agree on the stored shape — a row written by a click that
// list_schedules then reads back differently is a bug that only shows up later,
// in the model's own summary of the day.
//
// The validation lives here for the same reason. It used to be only in the tool
// schemas, which the agent respects and a UI action never sees.

// AddSchedule appends one calendar entry and returns the row as stored.
func (s *Service) AddSchedule(title, startAt, location string, participants []string) (map[string]any, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("life store is unavailable")
	}
	title = strings.TrimSpace(title)
	startAt = strings.TrimSpace(startAt)
	if title == "" {
		return nil, errors.New("title is required")
	}
	if startAt == "" {
		return nil, errors.New("start_at is required")
	}
	rec := map[string]any{
		"id":           short(uuid.NewString()),
		"title":        title,
		"start_at":     startAt,
		"location":     strings.TrimSpace(location),
		"participants": trimmed(participants),
	}
	s.store.mu.Lock()
	s.store.Schedules = append(s.store.Schedules, rec)
	s.store.mu.Unlock()
	s.store.save()
	return rec, nil
}

// AddRecord appends one diary / work / note / habit entry and returns the row
// as stored. `occurred_at` is stamped here, not taken from the caller: a record
// is written when it happens, and a model backdating its own notes would make
// search_records answer with a timeline nobody lived.
func (s *Service) AddRecord(kind, title, body string, tags []string, project string) (map[string]any, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("life store is unavailable")
	}
	kind = strings.TrimSpace(kind)
	body = strings.TrimSpace(body)
	if kind == "" {
		return nil, errors.New("type is required")
	}
	if body == "" {
		return nil, errors.New("body is required")
	}
	rec := map[string]any{
		"id":          short(uuid.NewString()),
		"type":        kind,
		"title":       strings.TrimSpace(title),
		"body":        body,
		"tags":        trimmed(tags),
		"project":     strings.TrimSpace(project),
		"occurred_at": time.Now().Format(time.RFC3339),
	}
	s.store.mu.Lock()
	s.store.Records = append(s.store.Records, rec)
	s.store.mu.Unlock()
	s.store.save()
	return rec, nil
}

// trimmed drops blank entries and surrounding space, and never returns nil:
// the rows are serialized to JSON, and a nil slice becomes `null` where every
// reader expects a list.
func trimmed(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
