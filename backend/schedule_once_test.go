package backend

import (
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// The failure this guard exists for, in the words it actually happened in: one
// sentence, two tools, two schedules, and the weaker one had silently dropped
// "每周一".
func TestASecondToolCannotScheduleTheSameSubject(t *testing.T) {
	c := newScheduleClaims()

	if held := c.claim("提醒我交周报", "30 9 * * 1"); held != nil {
		t.Fatalf("first claim was refused: %+v", held)
	}
	c.record("提醒我交周报", "sched-1", "30 9 * * 1")

	held := c.claim("交周报", "30 9 * * *")
	if held == nil {
		t.Fatal("second tool was allowed to schedule the same subject")
	}
	if held.id != "sched-1" || held.cron != "30 9 * * 1" {
		t.Errorf("the refusal must name the schedule that exists, got %+v", held)
	}
	if msg := duplicateScheduleMessage(held); msg == "" {
		t.Error("a refusal with nothing to say leaves the model guessing")
	}
}

// Different jobs are different claims, including when the cadence is the only
// thing that differs — "每天备份" and "每周备份" are two schedules someone may
// legitimately want.
func TestUnrelatedSubjectsAreNotBlocked(t *testing.T) {
	c := newScheduleClaims()
	c.claim("每天备份数据库", "0 3 * * *")
	c.record("每天备份数据库", "a", "0 3 * * *")

	for _, other := range []string{"每周备份数据库到异地", "看看昨天的部署", "买菜"} {
		if held := c.claim(other, "0 0 * * *"); held != nil {
			t.Errorf("%q was refused because of %+v", other, held)
		}
		c.record(other, "x", "0 0 * * *")
	}
}

// A write that failed must not leave the subject locked: the retry is the whole
// point of telling the model it failed.
func TestAFailedWriteReleasesItsClaim(t *testing.T) {
	c := newScheduleClaims()
	c.claim("给妈妈打电话", "0 20 * * *")
	c.release("给妈妈打电话")

	if held := c.claim("给妈妈打电话", "0 20 * * *"); held != nil {
		t.Fatalf("retry blocked by an attempt that created nothing: %+v", held)
	}
}

// Release only drops attempts. A schedule that exists stays claimed, or the
// second tool gets its window back by racing.
func TestReleaseDoesNotDropASucceededClaim(t *testing.T) {
	c := newScheduleClaims()
	c.claim("站会提醒", "0 15 * * 1-5")
	c.record("站会提醒", "sched-9", "0 15 * * 1-5")
	c.release("站会提醒")

	if held := c.claim("站会提醒", "0 15 * * 1-5"); held == nil || held.id != "sched-9" {
		t.Fatalf("a created schedule stopped holding its subject: %+v", held)
	}
}

func TestSubjectKeyIgnoresPunctuationAndCase(t *testing.T) {
	if scheduleSubjectKey("Deploy Check!") != scheduleSubjectKey("deploy, check") {
		t.Error("punctuation and case should not make two subjects")
	}
	// Too short to mean anything: no key, so no claim and no false blocking.
	if scheduleSubjectKey("。") != "" || scheduleSubjectKey(" a ") != "" {
		t.Error("a subject of one character must not claim anything")
	}
}

// The exact pair that got through twice: neither wording contains the other,
// and only stripping "提醒我" makes them recognisable as one job.
func TestTheTwoWordingsThatGotThroughAreOneJob(t *testing.T) {
	c := newScheduleClaims()
	if held := c.claim("提醒我交周报", "30 9 * * 1"); held != nil {
		t.Fatalf("first claim refused: %+v", held)
	}
	c.record("提醒我交周报", "sched-1", "30 9 * * 1")

	if held := c.claim("每周一交周报", "30 9 * * *"); held == nil {
		t.Fatal(`"每周一交周报" was allowed alongside "提醒我交周报" — the duplicate this guard exists for`)
	}
}

// Same minute is only half the test. Two different jobs that happen to fire
// together stay two.
func TestSameMinuteDifferentJobsAreNotMerged(t *testing.T) {
	c := newScheduleClaims()
	c.claim("备份数据库", "30 9 * * *")
	c.record("备份数据库", "a", "30 9 * * *")

	if held := c.claim("给妈妈打电话", "30 9 * * *"); held != nil {
		t.Errorf("unrelated job at the same minute was refused: %+v", held)
	}
}

// The three rows that were live on 2026-08-25, for one Tuesday meeting.
//
// The claim was in memory and ninety seconds wide, so it stopped nothing here:
// the asks were minutes apart and a deploy restarted the process between them.
// set_reminder wrote a DAILY schedule for a weekly meeting — wrong six days out
// of seven — beside two correct weekly ones.
func TestTheThreeRowsForOneTuesdayMeeting(t *testing.T) {
	live := []agent.ScheduledPrompt{{
		ID:       "502b956d",
		Schedule: "0 15 * * 2",
		Note:     "Rene Slack 每周例会",
		Prompt:   "提醒我：现在是奥地利时间 09:00（北京时间 15:00），该和 Rene 在 Slack 上开每周例会了。",
	}}

	// The wording the second ask produced. A different label, the same job.
	if held := alreadyScheduled(live, "提醒我：现在是奥地利时间 09:00（北京时间 15:00），该和 Rene 在 Slack 上开每周例会了。"); held == nil {
		t.Error("a second identical request was allowed to write a third row")
	} else if held.id != "502b956d" {
		t.Errorf("refusal named %q, not the schedule that exists", held.id)
	}

	// set_reminder's version: the same subject under a "提醒：" label, and the
	// daily cron it fell back to. Cron must not be part of the test, or this —
	// the wrongest of the three — is the one that gets through.
	if held := alreadyScheduled(live, "现在是奥地利时间 09:00（北京时间 15:00），该和 Rene 在 Slack 上开每周例会了。"); held == nil {
		t.Error("the daily fallback was allowed alongside the weekly schedule")
	}
}

// A durable check that refused everything would be worse than none: the model
// would stop being able to schedule anything after the first thing.
func TestUnrelatedJobsStillSchedule(t *testing.T) {
	live := []agent.ScheduledPrompt{
		{ID: "a", Schedule: "0 15 * * 2", Note: "Rene Slack 每周例会", Prompt: "提醒我和 Rene 开会"},
		{ID: "b", Schedule: "0 3 * * *", Note: "每天备份数据库", Prompt: "每天备份数据库"},
	}
	for _, subject := range []string{"给妈妈打电话", "看看昨天的部署有没有问题", "每周备份数据库到异地"} {
		if held := alreadyScheduled(live, subject); held != nil {
			t.Errorf("%q was refused because of %+v", subject, held)
		}
	}
}

// Nothing scheduled, nothing to collide with.
func TestAnEmptyScheduleListBlocksNothing(t *testing.T) {
	if held := alreadyScheduled(nil, "提醒我交周报"); held != nil {
		t.Errorf("refused against an empty list: %+v", held)
	}
}
