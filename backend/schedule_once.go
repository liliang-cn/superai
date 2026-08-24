package backend

// One request, one schedule.
//
// Two tools can write a schedule — set_reminder for "every day at HH:MM" and a
// specific moment, schedule_prompt for every cadence that cannot express. A
// model handed both sometimes calls both for one sentence, and the result is
// two rows where the user asked for one, the weaker tool having quietly dropped
// the part it could not say: "每周一早上九点半" became `30 9 * * *` beside a
// correct `30 9 * * 1`.
//
// Prompt wording moves that probability around without ever reaching zero, and
// the failure is silent — a duplicate looks like a schedule, fires like a
// schedule, and is wrong on six days out of seven.
//
// So the tools share a claim: whoever writes a schedule for a given subject
// first wins, and a second attempt within the window is refused with the id of
// the one that exists. The model is told plainly, which is also what makes it
// report the truth to the user instead of announcing two.

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

// scheduleClaimWindow is how long one subject stays claimed.
//
// Long enough to span a turn with several tool calls in it, short enough that
// deliberately scheduling the same words again — after reading the reply,
// deciding it was wrong, deleting it — is not fought by the machine.
const scheduleClaimWindow = 90 * time.Second

type scheduleClaims struct {
	mu   sync.Mutex
	seen map[string]scheduleClaim
}

type scheduleClaim struct {
	id   string
	cron string
	at   time.Time
}

func newScheduleClaims() *scheduleClaims {
	return &scheduleClaims{seen: map[string]scheduleClaim{}}
}

// claim reserves subject, or reports the schedule that already holds it.
//
// cron is part of the test, not just recorded: two tools answering one request
// schedule the same job for the same minute, and that coincidence is stronger
// evidence than any wording comparison.
func (c *scheduleClaims) claim(subject, cron string) (held *scheduleClaim) {
	key := scheduleSubjectKey(subject)
	if key == "" {
		return nil
	}
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.seen {
		if now.Sub(v.at) > scheduleClaimWindow {
			delete(c.seen, k)
		}
	}
	// Containment, not equality. The two tools are handed different wordings of
	// the same job — set_reminder got "交周报" while schedule_prompt got
	// "提醒我交周报" — so an exact match never fires and the guard would be
	// decoration. One key containing the other is the cheap test that catches
	// that shape. The floor is two runes: the case this exists for was "交周报"
	// inside "提醒我交周报", three characters, and a Chinese subject is often
	// shorter than an English one.
	for k, v := range c.seen {
		if sameJob(key, k, cron, v.cron) {
			return &v
		}
	}
	// Reserved before the write, so two tool calls racing cannot both find it
	// free. Filled in by record once the scheduler answers with an id.
	c.seen[key] = scheduleClaim{at: now, cron: cron}
	return nil
}

// sameJob decides whether two claims are one request answered twice.
func sameJob(keyA, keyB, cronA, cronB string) bool {
	if keyA == keyB {
		return true
	}
	// One phrasing inside the other, once "remind me" and friends are gone.
	if containsSubject(keyA, keyB) || containsSubject(keyB, keyA) {
		return true
	}
	// Different phrasings, same minute. A person asking for two unrelated
	// things at the identical minute inside ninety seconds is rare; two tools
	// splitting one request is what this file exists for. Some overlap is still
	// required, so "back up the database" and "call mum" both at 09:30 stay two
	// schedules.
	if sameFireTime(cronA, cronB) && sharesRun(keyA, keyB, 2) {
		return true
	}
	return false
}

// sameFireTime compares the minute and hour fields of two cron expressions.
func sameFireTime(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	if len(fa) < 2 || len(fb) < 2 {
		return false
	}
	return fa[0] == fb[0] && fa[1] == fb[1]
}

// sharesRun reports whether the two keys have any common run of n runes.
func sharesRun(a, b string, n int) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < n || len(rb) < n {
		return false
	}
	seen := make(map[string]struct{}, len(ra))
	for i := 0; i+n <= len(ra); i++ {
		seen[string(ra[i:i+n])] = struct{}{}
	}
	for i := 0; i+n <= len(rb); i++ {
		if _, ok := seen[string(rb[i:i+n])]; ok {
			return true
		}
	}
	return false
}

// record attaches the created schedule to a claim already taken.
func (c *scheduleClaims) record(subject, id, cron string) {
	key := scheduleSubjectKey(subject)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[key] = scheduleClaim{id: id, cron: cron, at: time.Now()}
}

// release drops a claim whose write failed, so a retry is not blocked by the
// attempt that did not produce anything.
func (c *scheduleClaims) release(subject string) {
	key := scheduleSubjectKey(subject)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.seen[key]; ok && v.id == "" {
		delete(c.seen, key)
	}
}

// scheduleSubjectKey reduces what a schedule is *about* to something two tools
// describing the same thing agree on.
//
// They will not agree on wording — one gets "提醒我交周报" and the other
// "每周一早上九点半提醒我交周报" — so punctuation, spacing and case go, and what
// remains is compared on the letters and digits alone. Cadence words are not
// stripped: dropping them would make "每天备份" and "每周备份" the same subject,
// and those are two schedules a person may legitimately want.
func scheduleSubjectKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// These say how the person asked, not what the job is, and only one of the
	// two tools tends to receive them. Removing them is what lets "提醒我交周报"
	// and "每周一交周报" recognise each other.
	for _, filler := range []string{
		"请提醒我", "提醒我", "记得提醒我", "别忘了", "记得", "帮我",
		"remind me to ", "remind me ", "please remind me to ", "remember to ",
	} {
		s = strings.ReplaceAll(s, filler, "")
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	key := b.String()
	if len([]rune(key)) < 2 {
		return ""
	}
	return key
}

// containsSubject reports whether outer contains inner as a subject — inner
// being long enough that the containment means something.
func containsSubject(outer, inner string) bool {
	if len([]rune(inner)) < 2 {
		return false
	}
	return strings.Contains(outer, inner)
}

// duplicateScheduleMessage is what a refused second write tells the model.
func duplicateScheduleMessage(held *scheduleClaim) string {
	if held.id == "" {
		return "Another tool just scheduled this. Do not schedule it again — report the one that exists."
	}
	return fmt.Sprintf(
		"This is already scheduled: id %s, cron %s. Do not schedule it twice — one of two rows for one request is always the wrong one. Tell the user about this one.",
		held.id, held.cron)
}
