package backend

import "testing"

// The schedule that prompted this: "立即提醒" on a Tuesday afternoon became
// `38 16 25 8 *` — 16:38 on 25 August, every year — and its next run was 2027.
func TestAOneShotIsRemovedOnceItHasFired(t *testing.T) {
	note := reminderNote("立即提醒", true)
	if !isOneShotNote(note) {
		t.Fatalf("a one-shot was not marked: %q", note)
	}
	// The label is for a person reading the list, so it has to say something.
	if !contains(note, "立即提醒") {
		t.Errorf("the note lost the reminder's title: %q", note)
	}

	live := []ScheduleRow{
		{ID: "one-shot", Prompt: ReminderPrompt("立即提醒"), Note: note},
		{ID: "weekly", Prompt: ReminderPrompt("站会"), Note: reminderNote("站会", false)},
	}
	got := finishedOneShots(live, ReminderPrompt("立即提醒"))
	if len(got) != 1 || got[0] != "one-shot" {
		t.Fatalf("finishedOneShots = %v, want [one-shot]", got)
	}
}

// A repeating reminder that fires must survive. Deleting one would turn every
// daily reminder into a single-use one, silently.
func TestARepeatingReminderIsNotRemoved(t *testing.T) {
	live := []ScheduleRow{{ID: "daily", Prompt: ReminderPrompt("站会"), Note: reminderNote("站会", false)}}
	if got := finishedOneShots(live, ReminderPrompt("站会")); len(got) != 0 {
		t.Errorf("a repeating reminder was scheduled for deletion: %v", got)
	}
}

// A one-shot firing must not take an unrelated one with it.
func TestOnlyTheReminderThatFiredIsRemoved(t *testing.T) {
	live := []ScheduleRow{
		{ID: "a", Prompt: ReminderPrompt("倒垃圾"), Note: reminderNote("倒垃圾", true)},
		{ID: "b", Prompt: ReminderPrompt("交房租"), Note: reminderNote("交房租", true)},
	}
	got := finishedOneShots(live, ReminderPrompt("倒垃圾"))
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("finishedOneShots = %v, want [a]", got)
	}
}

// Four of the five reminders on screen had been deleted hours earlier: the row
// was written on create and never removed on delete.
func TestReminderRowsWithoutASchedulAreDropped(t *testing.T) {
	rows := []map[string]any{
		{"id": "gone-1", "title": "站会"},
		{"id": "still-here", "title": "Rene Slack 每周例会"},
		{"id": "gone-2", "title": "交周报"},
		{"title": "written before ids existed"},
	}
	out := liveReminders(rows, map[string]bool{"still-here": true})
	if len(out) != 2 {
		t.Fatalf("kept %d rows, want 2 (the live one and the one with no id)", len(out))
	}
	if out[0]["id"] != "still-here" {
		t.Errorf("dropped the reminder that still exists: %+v", out)
	}
	// A row from before ids is unverifiable; losing it would be worse than
	// showing it.
	if out[1]["title"] != "written before ids existed" {
		t.Errorf("an unverifiable row was thrown away: %+v", out)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
