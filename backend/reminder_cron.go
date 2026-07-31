package backend

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Turning a reminder into a schedule.
//
// set_reminder used to append a row to a JSON file while its description told
// the model "SuperAI will remind you when it is due" — so the model promised
// something nothing implemented. Now that there is a real scheduler, a reminder
// becomes a scheduled prompt, and the promise holds.
//
// The times a person gives a reminder are not cron expressions ("17:00",
// "每天早上八点", "2026-08-01T09:00:00+08:00"), so they are translated here. A
// value that cannot be translated is refused rather than stored to sit silent
// for ever, which is the failure mode this whole change exists to remove.

var (
	// hhmm matches a clock time, optionally with a leading "每天".
	hhmm = regexp.MustCompile(`^(\d{1,2})[:：](\d{2})$`)
	// bareCron is a five- or six-field expression the scheduler can take as is.
	bareCron = regexp.MustCompile(`^[\d*/,\-\s?LW#]+$`)
)

// ReminderSchedule is the cron expression and human note for a reminder.
type ReminderSchedule struct {
	Cron string
	// OneShot is true when the reminder names a specific date, which cron cannot
	// express as "only once" — it repeats yearly. The caller should say so, or
	// delete the schedule after it fires.
	OneShot bool
	Note    string
}

// ReminderToCron translates a reminder time into a schedule.
//
// remindAt accepts a clock time ("8:00", "17:30"), an RFC3339 timestamp, or a
// raw cron expression for anyone who wants one. recurrence is "daily" or "none";
// a clock time with no recurrence is treated as daily, because "remind me at
// eight" almost never means "once, and if I miss it never again".
func ReminderToCron(remindAt, recurrence string) (ReminderSchedule, error) {
	remindAt = strings.TrimSpace(remindAt)
	recurrence = strings.ToLower(strings.TrimSpace(recurrence))
	if remindAt == "" {
		return ReminderSchedule{}, fmt.Errorf("remind_at is required")
	}

	// Trim the Chinese prefixes a model tends to include with a clock time.
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(remindAt, "每天"), "每日"))

	if m := hhmm.FindStringSubmatch(trimmed); m != nil {
		hour, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		if hour > 23 || minute > 59 {
			return ReminderSchedule{}, fmt.Errorf("%q is not a valid time of day", remindAt)
		}
		return ReminderSchedule{
			Cron: fmt.Sprintf("%d %d * * *", minute, hour),
			Note: fmt.Sprintf("每天 %02d:%02d", hour, minute),
		}, nil
	}

	if ts, err := time.Parse(time.RFC3339, remindAt); err == nil {
		local := ts.Local()
		if recurrence == "daily" {
			return ReminderSchedule{
				Cron: fmt.Sprintf("%d %d * * *", local.Minute(), local.Hour()),
				Note: fmt.Sprintf("每天 %02d:%02d", local.Hour(), local.Minute()),
			}, nil
		}
		// Cron has no "once": the closest is this day and month, which recurs
		// annually. Reported so the caller can be honest about it.
		return ReminderSchedule{
			Cron:    fmt.Sprintf("%d %d %d %d *", local.Minute(), local.Hour(), local.Day(), int(local.Month())),
			OneShot: true,
			Note:    local.Format("2006-01-02 15:04"),
		}, nil
	}

	// A raw expression, for anyone who would rather write one. Validated by the
	// scheduler when the task is created, so a typo is refused there.
	if fields := strings.Fields(remindAt); len(fields) >= 5 && bareCron.MatchString(remindAt) {
		return ReminderSchedule{Cron: remindAt, Note: remindAt}, nil
	}

	return ReminderSchedule{}, fmt.Errorf("看不懂的提醒时间 %q：用 HH:MM（每天）、RFC3339 时间戳，或 cron 表达式", remindAt)
}

// ReminderPrompt is what the agent is asked to do when a reminder fires.
//
// It is a prompt rather than a canned string because a reminder should be able to
// do something ("提醒我买菜" versus "提醒我看看昨天的部署有没有问题"), and because
// the agent has the tools to deliver it wherever the user has set up — a message,
// a mail, a notification.
func ReminderPrompt(title string) string {
	title = strings.TrimSpace(title)
	return fmt.Sprintf("现在是提醒时间。提醒事项：%s\n\n"+
		"请用一句话把这个提醒告诉用户。如果提醒内容本身要求你先做点什么"+
		"（查一下、算一下、看一下），先做完再告诉用户结果。", title)
}
