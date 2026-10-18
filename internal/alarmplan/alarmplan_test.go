package alarmplan

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

type fixtures struct {
	WindowDays     int              `json:"windowDays"`
	MaxOccurrences int              `json:"maxOccurrences"`
	DefaultEndTime string           `json:"defaultEndTime"`
	Holidays       map[string][]Day `json:"holidays"`
	Cases          []struct {
		Name     string   `json:"name"`
		Now      string   `json:"now"`
		Alarm    Alarm    `json:"alarm"`
		Limit    int      `json:"limit"`
		Expected []string `json:"expected"`
		Total    int      `json:"total"`
	} `json:"cases"`
}

func loadFixtures(t *testing.T) fixtures {
	t.Helper()
	raw, err := os.ReadFile("testdata/alarm-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixtures
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestSharedFixtures runs noted's cases: the rings the core hands a phone are
// the ones noted's own engine and the iOS engine compute.
func TestSharedFixtures(t *testing.T) {
	f := loadFixtures(t)
	if f.WindowDays != WindowDays || f.MaxOccurrences != MaxOccurrences || f.DefaultEndTime != DefaultEndTime {
		t.Fatalf("fixture constants %d/%d/%s differ from the engine's", f.WindowDays, f.MaxOccurrences, f.DefaultEndTime)
	}
	if len(f.Cases) < 30 {
		t.Fatalf("only %d cases", len(f.Cases))
	}
	cal := NewCalendar(f.Holidays["CN"])
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, c.Now)
			if err != nil {
				t.Fatal(err)
			}
			got := Occurrences(c.Alarm, cal, now, c.Limit, 0)
			if len(got) != len(c.Expected) {
				t.Fatalf("first %d: got %d rings %v, want %v", c.Limit, len(got), got, c.Expected)
			}
			for i, w := range c.Expected {
				want, err := time.Parse(time.RFC3339, w)
				if err != nil {
					t.Fatal(err)
				}
				if !got[i].Equal(want) {
					t.Errorf("ring %d: %s, want %s", i, got[i].Format(time.RFC3339), w)
				}
			}
			if all := Occurrences(c.Alarm, cal, now, 0, 0); len(all) != c.Total {
				t.Errorf("total %d, want %d", len(all), c.Total)
			}
		})
	}
}

func on(b bool) *bool { return &b }

func TestExpandAppliesHolidaysAndNamesSkips(t *testing.T) {
	cal := NewCalendar([]Day{
		{Date: "2026-10-01", Kind: "holiday", Name: "国庆节"},
		{Date: "2026-10-02", Kind: "holiday", Name: "国庆节"},
		{Date: "2026-10-10", Kind: "workday", Name: "国庆节"}, // a Saturday worked
	})
	now, _ := time.Parse(time.RFC3339, "2026-09-30T08:00:00+08:00")
	work := Alarm{ID: "w", Label: "上班", Mode: "alarm", Region: "CN", HolidayPolicy: PolicyWorkdayOnly,
		Schedule: Schedule{Kind: KindRecurring, Anchor: "2026-09-01", Time: "07:00", TimeZone: "Asia/Shanghai", Recurrence: &Recurrence{Freq: FreqDaily, Interval: 1}}}
	off := work
	off.ID, off.Enabled = "off", on(false)
	once := Alarm{ID: "o", Label: "取快递", Mode: "reminder",
		Schedule: Schedule{Kind: KindOneShot, At: "2026-10-01T18:00:00+08:00", TimeZone: "Asia/Shanghai"}}

	p := Expand([]Alarm{work, off, once}, cal, now, 14)
	if len(p.Rings) == 0 {
		t.Fatal("no rings")
	}
	var dates []string
	for i, r := range p.Rings {
		if i > 0 && r.FireMs < p.Rings[i-1].FireMs {
			t.Fatalf("not nearest first at %d", i)
		}
		if r.AlarmID == "off" {
			t.Fatal("a disabled alarm rang")
		}
		if r.AlarmID == "w" {
			dates = append(dates, r.Fire[:10])
		}
	}
	got := strings.Join(dates, " ")
	if strings.Contains(got, "2026-10-01") || strings.Contains(got, "2026-10-02") || strings.Contains(got, "2026-10-03") {
		t.Errorf("rang on a day off: %s", got)
	}
	if !strings.Contains(got, "2026-10-10") {
		t.Errorf("missed the 调休 Saturday: %s", got)
	}
	if p.Rings[0].AlarmID != "o" && p.Rings[1].AlarmID != "o" {
		// 10-01 07:00 is gone, so the first rings are the one-shot and 10-05.
		t.Errorf("one-shot reminder not near the front: %+v", p.Rings[:2])
	}
	for _, r := range p.Rings {
		if r.AlarmID == "o" && r.Mode != ModeReminder {
			t.Errorf("reminder mode lost: %+v", r)
		}
		if r.Fire[:10] == "2026-10-10" && (r.Listed == nil || r.Listed.Kind != "workday") {
			t.Errorf("the 调休 ring does not say so: %+v", r)
		}
	}
	if len(p.Skipped) != 2 || p.Skipped[0].Date != "2026-10-01" || p.Skipped[0].Name != "国庆节" || p.Skipped[1].Date != "2026-10-02" {
		t.Errorf("skipped: %+v", p.Skipped)
	}

	// The same input is the same plan, ids included: a phone resyncing
	// changes nothing.
	again := Expand([]Alarm{work, off, once}, cal, now, 14)
	for i := range p.Rings {
		if p.Rings[i].ID != again.Rings[i].ID {
			t.Fatalf("ring %d's id moved", i)
		}
	}
	// A relabelled alarm is new rings.
	work.Label = "上班!"
	if Expand([]Alarm{work}, cal, now, 14).Rings[0].ID == p.Rings[1].ID {
		t.Error("a new label kept the old id")
	}
}

func TestExpandWindow(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-13T08:00:00+08:00")
	daily := Alarm{ID: "d", Label: "x", Schedule: Schedule{Kind: KindRecurring, Anchor: "2026-10-13", Time: "07:00", TimeZone: "Asia/Shanghai", Recurrence: &Recurrence{Freq: FreqDaily}}}
	if n := len(Expand([]Alarm{daily}, nil, now, 7).Rings); n != 7 {
		t.Errorf("7 days: %d rings", n)
	}
	if n := len(Expand([]Alarm{daily}, nil, now, 0).Rings); n != 180 {
		t.Errorf("default window: %d rings", n)
	}
	if n := len(Expand([]Alarm{daily}, nil, now, 999).Rings); n != 180 {
		t.Errorf("clamped window: %d rings", n)
	}
	far := Alarm{ID: "f", Schedule: Schedule{Kind: KindOneShot, At: "2026-12-25T09:00:00+08:00", TimeZone: "Asia/Shanghai"}}
	if n := len(Expand([]Alarm{far}, nil, now, 30).Rings); n != 0 {
		t.Errorf("one-shot past a short window: %d", n)
	}
	if n := len(Expand([]Alarm{far}, nil, now, 0).Rings); n != 1 {
		t.Errorf("one-shot in the full window: %d", n)
	}
}

func TestRingIDIsAUUID(t *testing.T) {
	at, _ := time.Parse(time.RFC3339, "2026-10-14T07:00:00+08:00")
	id := RingID("a1", at, "alarm", "起床", "")
	if len(id) != 36 || id[14] != '5' || !strings.ContainsRune("89ab", rune(id[19])) {
		t.Errorf("not a v5 UUID: %s", id)
	}
	if id != RingID("a1", at.UTC(), "alarm", "起床", "") {
		t.Error("the zone the time is written in moved the id")
	}
}

func TestExpandFollowsNotify(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-10-13T08:00:00+08:00")
	at := Schedule{Kind: KindOneShot, At: "2026-10-14T15:00:00+08:00", TimeZone: "Asia/Shanghai"}
	p := Expand([]Alarm{
		{ID: "a", Label: "闹钟", Notify: "alarm", Schedule: at},
		{ID: "r", Label: "开会", Mode: ModeReminder, Notify: "reminder", RemindBefore: 10, Schedule: at},
		{ID: "n", Label: "通知", Mode: ModeReminder, Notify: "notification", Schedule: at},
		{ID: "x", Label: "不提醒", Notify: "none", Schedule: at},
		{ID: "u", Label: "未知", Notify: "loud", Schedule: at},
		{ID: "o", Label: "旧闹钟", Schedule: at},
	}, nil, now, 7)
	got := map[string]string{}
	for _, r := range p.Rings {
		got[r.AlarmID] = r.Mode + " " + r.Fire
	}
	want := map[string]string{
		"a": "alarm 2026-10-14T15:00:00+08:00",
		"r": "reminder 2026-10-14T14:50:00+08:00",
		"u": "reminder 2026-10-14T15:00:00+08:00",
		"o": "alarm 2026-10-14T15:00:00+08:00",
	}
	if len(got) != len(want) {
		t.Fatalf("rings: %v", got)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
}
