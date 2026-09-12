package backend

// A reminder for one moment should not come back next year.
//
// cron has no way to say "once". `ReminderToCron` turns "remind me at 16:38
// today" into `38 16 25 8 *` — the 25th of August at 16:38, every year — and
// the tool used to hand the model a note admitting as much: "cron 无法表达「只
// 一次」，这条会每年同一天重复；不需要时删掉它."
//
// Nobody deletes it. A one-word test on a Tuesday afternoon left a schedule
// whose next run was 2027, sitting in the list beside the real ones, and the
// only thing standing between the user and a mystery notification a year later
// was a sentence in a tool result they never saw.
//
// So the reminder is marked as one-shot when it is created and deleted once it
// has fired. The marker lives in the note because that is what survives a
// restart, is visible to a person reading the list, and needs no second store
// to fall out of step with the first.

import "strings"

// oneShotMark is what a one-shot reminder's note carries.
//
// Inside the note rather than as a prefix on the title: the note is a label a
// person reads, and "提醒（一次性）：倒垃圾" says the useful thing — this fires
// once — where a hidden marker would say nothing to anybody.
const oneShotMark = "（一次性）"

// reminderNote labels a reminder for the schedule list.
func reminderNote(title string, oneShot bool) string {
	if oneShot {
		return "提醒" + oneShotMark + "：" + title
	}
	return "提醒：" + title
}

// markOneShot labels any schedule as firing once.
//
// set_reminder has reminderNote; schedule_prompt names its own schedules and
// only needs the mark added, so both end up with a note the same test reads.
func markOneShot(label string, oneShot bool) string {
	if oneShot && label != "" && !isOneShotNote(label) {
		return label + oneShotMark
	}
	return label
}

// isOneShotNote reports whether a schedule was created to fire once.
func isOneShotNote(note string) bool {
	return strings.Contains(note, oneShotMark)
}

// finishedOneShots returns the ids of one-shot schedules that just fired.
//
// PromptRun carries no schedule id, so the prompt is the join. That is exact
// enough here: a reminder's prompt is generated from its title, and two
// reminders with the same title are the same reminder — the duplicate guard
// in schedule_once.go is what makes sure of it.
func finishedOneShots(existing []ScheduleRow, firedPrompt string) []string {
	var ids []string
	for _, s := range existing {
		if s.Prompt == firedPrompt && isOneShotNote(s.Note) {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// ScheduleRow is the part of a schedule this file needs, so the helpers above
// can be tested without standing up a scheduler.
type ScheduleRow struct {
	ID     string
	Prompt string
	Note   string
}

// liveReminders drops rows whose schedule is gone.
//
// The reminder rows are a second copy, kept so `list_reminders` can answer for
// a reminder the user just watched being created. Deleting the schedule never
// deleted the row, so the list filled with reminders that would never fire
// again: four of the five it showed had been deleted hours earlier. Rather
// than another delete path to forget, the copy is reconciled when it is read —
// the scheduler is the truth, and this is only its index.
func liveReminders(rows []map[string]any, liveIDs map[string]bool) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		id, _ := r["id"].(string)
		// A row with no id predates this and cannot be checked; keeping it is
		// the lesser wrong, since dropping it would silently lose data.
		if id == "" || liveIDs[id] {
			out = append(out, r)
		}
	}
	return out
}

// reconcileReminders drops rows whose schedule no longer exists.
//
// A scheduler that cannot be reached returns the rows untouched: an index that
// might be stale beats an empty list, which would read as "you have no
// reminders" to both the model and the user.
func (s *Service) reconcileReminders(rows []map[string]any) []map[string]any {
	sch, err := s.reminderScheduler()
	if err != nil {
		return rows
	}
	list, err := sch.List()
	if err != nil {
		return rows
	}
	live := make(map[string]bool, len(list))
	for _, t := range list {
		live[t.ID] = true
	}
	return liveReminders(rows, live)
}

// DropFinishedOneShots deletes the one-shot schedules that firedPrompt just
// completed, and returns how many went.
//
// Called after the run rather than from inside it: deleting a task from its
// own execution is asking the scheduler to modify the list it is walking.
func (s *Service) DropFinishedOneShots(firedPrompt string) int {
	sch, err := s.reminderScheduler()
	if err != nil {
		return 0
	}
	list, err := sch.List()
	if err != nil {
		return 0
	}
	rows := make([]ScheduleRow, 0, len(list))
	for _, t := range list {
		rows = append(rows, ScheduleRow{ID: t.ID, Prompt: t.Prompt, Note: t.Note})
	}
	dropped := 0
	for _, id := range finishedOneShots(rows, firedPrompt) {
		if sch.Delete(id) == nil {
			dropped++
		}
	}
	return dropped
}
