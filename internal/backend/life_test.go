package backend

import (
	"path/filepath"
	"testing"
)

func testLifeService(t *testing.T) *Service {
	t.Helper()
	return &Service{store: newLifeStore(filepath.Join(t.TempDir(), "life.json"))}
}

func TestAddScheduleStoresRow(t *testing.T) {
	s := testLifeService(t)

	rec, err := s.AddSchedule("  和设计过稿  ", " 2026-09-03T09:30:00+08:00 ", " 会议室 ",
		[]string{" 小张 ", "", "  "})
	if err != nil {
		t.Fatalf("AddSchedule: %v", err)
	}
	if got := rec["title"]; got != "和设计过稿" {
		t.Errorf("title not trimmed: %q", got)
	}
	if got := rec["start_at"]; got != "2026-09-03T09:30:00+08:00" {
		t.Errorf("start_at not trimmed: %q", got)
	}
	// A blank participant is dropped rather than stored as an empty name.
	parts, ok := rec["participants"].([]string)
	if !ok || len(parts) != 1 || parts[0] != "小张" {
		t.Errorf("participants = %#v, want [小张]", rec["participants"])
	}
	if rec["id"] == "" || rec["id"] == nil {
		t.Error("id not assigned")
	}

	life := s.Life()
	if len(life.Schedules) != 1 {
		t.Fatalf("Life().Schedules = %d rows, want 1", len(life.Schedules))
	}
	if life.Schedules[0]["title"] != "和设计过稿" {
		t.Errorf("stored row does not match returned row: %#v", life.Schedules[0])
	}
}

// The tool schema has always declared both required. Enforcing it in the shared
// method is what stops a `ui` button, which never sees that schema, from
// writing a calendar entry with no time on it.
func TestAddScheduleRejectsMissingFields(t *testing.T) {
	for _, tc := range []struct{ name, title, startAt string }{
		{"no title", "   ", "2026-09-03T09:30:00+08:00"},
		{"no start", "标题", "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testLifeService(t)
			if _, err := s.AddSchedule(tc.title, tc.startAt, "", nil); err == nil {
				t.Fatal("expected an error")
			}
			if got := len(s.Life().Schedules); got != 0 {
				t.Errorf("rejected entry was still stored (%d rows)", got)
			}
		})
	}
}

func TestAddRecordStampsOccurredAt(t *testing.T) {
	s := testLifeService(t)

	rec, err := s.AddRecord("note", "标题", " 正文 ", []string{"tag", " "}, " superai ")
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}
	if rec["body"] != "正文" {
		t.Errorf("body not trimmed: %q", rec["body"])
	}
	if rec["project"] != "superai" {
		t.Errorf("project not trimmed: %q", rec["project"])
	}
	if got, _ := rec["occurred_at"].(string); got == "" {
		t.Error("occurred_at not stamped")
	}
	tags, ok := rec["tags"].([]string)
	if !ok || len(tags) != 1 || tags[0] != "tag" {
		t.Errorf("tags = %#v, want [tag]", rec["tags"])
	}
}

func TestAddRecordRejectsMissingFields(t *testing.T) {
	s := testLifeService(t)
	if _, err := s.AddRecord("", "", "正文", nil, ""); err == nil {
		t.Error("expected an error for a missing type")
	}
	if _, err := s.AddRecord("note", "", "   ", nil, ""); err == nil {
		t.Error("expected an error for a missing body")
	}
	if got := len(s.Life().Records); got != 0 {
		t.Errorf("rejected records were stored (%d rows)", got)
	}
}

// Empty lists must serialize as `[]`, not `null`: the rows go to the Life panel
// and to the model as JSON, and both read participants/tags as a list.
func TestListFieldsAreNeverNil(t *testing.T) {
	s := testLifeService(t)
	sch, err := s.AddSchedule("标题", "2026-09-03T09:30:00+08:00", "", nil)
	if err != nil {
		t.Fatalf("AddSchedule: %v", err)
	}
	if p, ok := sch["participants"].([]string); !ok || p == nil {
		t.Errorf("participants = %#v, want an empty slice", sch["participants"])
	}
	rec, err := s.AddRecord("note", "", "正文", nil, "")
	if err != nil {
		t.Fatalf("AddRecord: %v", err)
	}
	if tg, ok := rec["tags"].([]string); !ok || tg == nil {
		t.Errorf("tags = %#v, want an empty slice", rec["tags"])
	}
}

// A nil Service must answer rather than panic: the App methods call straight
// through before the agent has finished booting.
func TestLifeWritesOnUnbuiltService(t *testing.T) {
	var s *Service
	if _, err := s.AddSchedule("t", "now", "", nil); err == nil {
		t.Error("expected an error from a nil service")
	}
	if _, err := s.AddRecord("note", "", "b", nil, ""); err == nil {
		t.Error("expected an error from a nil service")
	}
}
