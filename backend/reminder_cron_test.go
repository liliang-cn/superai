package backend

import (
	"strings"
	"testing"
	"time"
)

// The Chinese prefixes a model tends to put in front of a clock time. These are
// matched against user and model input, so they are data: nothing here may be
// translated, and until now nothing checked that.
func TestClockTimesWithChinesePrefixes(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantCron string
	}{
		{"8:00", "0 8 * * *"},
		{"08:00", "0 8 * * *"},
		{"17:30", "30 17 * * *"},
		{"每天8:00", "0 8 * * *"},
		{"每天 8:00", "0 8 * * *"},
		{"每日17:30", "30 17 * * *"},
		{"每日 07:05", "5 7 * * *"},
		{"8：00", "0 8 * * *"}, // full-width colon, which a Chinese IME produces
		{"0:00", "0 0 * * *"},
		{"23:59", "59 23 * * *"},
	} {
		got, err := ReminderToCron(tc.in, "daily")
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.Cron != tc.wantCron {
			t.Errorf("%q -> cron %q, want %q", tc.in, got.Cron, tc.wantCron)
		}
		if got.OneShot {
			t.Errorf("%q was reported as one-shot; a daily clock time repeats", tc.in)
		}
	}
}

// "Remind me at eight" almost never means "once, and never again if I miss it".
func TestAClockTimeWithNoRecurrenceIsStillDaily(t *testing.T) {
	got, err := ReminderToCron("9:15", "")
	if err != nil {
		t.Fatalf("ReminderToCron: %v", err)
	}
	if got.Cron != "15 9 * * *" {
		t.Errorf("cron = %q, want a daily one", got.Cron)
	}
}

// A time that cannot be translated must be refused, not stored to sit silent
// for ever — which is the failure this conversion exists to prevent.
func TestUntranslatableTimesAreRefused(t *testing.T) {
	for _, in := range []string{
		"", "   ", "25:00", "8:99", "tomorrow morning", "明天早上", "每天", "8", "8:0",
	} {
		if got, err := ReminderToCron(in, "daily"); err == nil {
			t.Errorf("%q was accepted as %+v", in, got)
		}
	}
}

// A timestamp names one moment. Cron cannot say "only once", so the closest it
// can do repeats yearly — and the caller has to be told, or a reminder that was
// meant for one day comes back every year.
func TestATimestampWithoutRecurrenceIsFlaggedOneShot(t *testing.T) {
	ts := time.Date(2026, 3, 9, 14, 30, 0, 0, time.Local).Format(time.RFC3339)
	got, err := ReminderToCron(ts, "none")
	if err != nil {
		t.Fatalf("ReminderToCron: %v", err)
	}
	if !got.OneShot {
		t.Error("a dated reminder was not flagged one-shot, so it will silently repeat every year")
	}
	if got.Cron != "30 14 9 3 *" {
		t.Errorf("cron = %q, want the day and month pinned", got.Cron)
	}
}

// The same timestamp asked to recur drops the date.
func TestATimestampAskedToRecurBecomesDaily(t *testing.T) {
	ts := time.Date(2026, 3, 9, 14, 30, 0, 0, time.Local).Format(time.RFC3339)
	got, err := ReminderToCron(ts, "daily")
	if err != nil {
		t.Fatalf("ReminderToCron: %v", err)
	}
	if got.OneShot {
		t.Error("a daily reminder was flagged one-shot")
	}
	if got.Cron != "30 14 * * *" {
		t.Errorf("cron = %q, want a daily one", got.Cron)
	}
}

// The note is a GUI label as well as part of the tool result, which is why it
// stays Chinese. Asserted so a later sweep does not translate it by accident.
func TestTheNoteIsTheChineseLabelTheUIShows(t *testing.T) {
	got, err := ReminderToCron("7:05", "daily")
	if err != nil {
		t.Fatalf("ReminderToCron: %v", err)
	}
	if !strings.HasPrefix(got.Note, "每天") {
		t.Errorf("note = %q, want the 每天 label the UI renders", got.Note)
	}
	if !strings.Contains(got.Note, "07:05") {
		t.Errorf("note = %q, want a zero-padded time", got.Note)
	}
}

// Anyone who wants to write cron themselves should be able to.
func TestARawCronExpressionIsPassedThrough(t *testing.T) {
	got, err := ReminderToCron("0 9 * * 1-5", "daily")
	if err != nil {
		t.Fatalf("ReminderToCron: %v", err)
	}
	if got.Cron != "0 9 * * 1-5" {
		t.Errorf("cron = %q, want it passed through untouched", got.Cron)
	}
}
