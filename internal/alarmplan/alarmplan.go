// Package alarmplan turns noted's alarms into the rings a phone schedules:
// every enabled alarm expanded over the next days with the CN statutory
// holidays and 调休 applied. The rules are noted's (docs/alarms.md, "Engine
// rules"), ported from its internal/alarm and pinned by the same shared
// fixtures (testdata/alarm-fixtures.json, a copy of noted's
// docs/alarm-fixtures.json), so a phone that asks the core for its rings
// rings exactly what noted's nextFires promise.
//
// The definitions and the holiday data stay in noted; this package only reads
// what list_alarms and get_holidays return.
package alarmplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Schedule kinds, modes, frequencies and holiday policies, as noted writes them.
const (
	KindOneShot   = "oneShot"
	KindRecurring = "recurring"

	ModeAlarm    = "alarm"
	ModeReminder = "reminder"

	FreqMinutely = "minutely"
	FreqHourly   = "hourly"
	FreqDaily    = "daily"
	FreqWeekly   = "weekly"
	FreqMonthly  = "monthly"

	PolicyNone            = "none"
	PolicySkipHoliday     = "skipHoliday"
	PolicyWorkdayOnly     = "workdayOnly"
	PolicyFollowStatutory = "followStatutory"

	RegionCN = "CN"
)

const (
	// WindowDays is how far ahead an alarm is expanded: today and the next
	// WindowDays days, in the alarm's zone.
	WindowDays = 180
	// MaxOccurrences caps one alarm's expansion.
	MaxOccurrences = 200
	// DefaultEndTime ends a minutely or hourly day without its own endTime.
	DefaultEndTime = "22:00"
)

// Recurrence is how a recurring schedule repeats.
type Recurrence struct {
	Freq       string   `json:"freq"`
	Interval   int      `json:"interval"`
	ByDay      []string `json:"byDay,omitempty"`
	ByMonthDay int      `json:"byMonthDay,omitempty"`
	ByTime     []string `json:"byTime,omitempty"`
	Until      string   `json:"until,omitempty"`
	Count      int      `json:"count,omitempty"`
	EndTime    string   `json:"endTime,omitempty"`
}

// Schedule is when an alarm rings. At is RFC 3339 text, as list_alarms gives
// it (empty on a recurring alarm).
type Schedule struct {
	Kind       string      `json:"kind"`
	At         string      `json:"at,omitempty"`
	Anchor     string      `json:"anchor,omitempty"`
	Time       string      `json:"time,omitempty"`
	TimeZone   string      `json:"timeZone"`
	Recurrence *Recurrence `json:"recurrence,omitempty"`
}

// Alarm is one item of list_alarms: the fields the rings depend on. Enabled
// is a pointer so an alarm that does not say is on, as in the fixtures.
type Alarm struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Mode          string   `json:"mode"`
	Enabled       *bool    `json:"enabled,omitempty"`
	Schedule      Schedule `json:"schedule"`
	HolidayPolicy string   `json:"holidayPolicy"`
	Region        string   `json:"region"`
	Note          string   `json:"note,omitempty"`
	// Notify is how the item reaches the person (list_alarms notify all):
	// alarm, reminder, notification or none; empty for a noted that predates
	// it, then Mode says. RemindBefore is the lead taken off every ring.
	Notify       string `json:"notify,omitempty"`
	RemindBefore int    `json:"remindBeforeMinutes,omitempty"`
}

// Rings reports how the phone rings the item, if at all: ModeAlarm or
// ModeReminder; "" for notification (posted as a notice by the core) and
// none. An unknown notify is a reminder, never an alarm.
func (a Alarm) Rings() string {
	switch a.Notify {
	case "":
		if a.Mode == ModeReminder {
			return ModeReminder
		}
		return ModeAlarm
	case ModeAlarm:
		return ModeAlarm
	case "notification", "none":
		return ""
	}
	return ModeReminder
}

// On reports whether the alarm rings at all.
func (a Alarm) On() bool { return a.Enabled == nil || *a.Enabled }

// Day is one listed day of the holiday calendar: a day off ("holiday") or a
// weekend day worked instead ("workday", 调休补班).
type Day struct {
	Date string `json:"date"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// Calendar is the CN calendar by date. A nil or empty one lists nothing:
// Monday to Friday work, weekends rest.
type Calendar struct{ days map[string]Day }

// NewCalendar builds a calendar; a later day with the same date wins.
func NewCalendar(days []Day) *Calendar {
	c := &Calendar{days: make(map[string]Day, len(days))}
	for _, d := range days {
		if d.Date != "" && (d.Kind == "holiday" || d.Kind == "workday") {
			c.days[d.Date] = d
		}
	}
	return c
}

// Lookup is the listed day for a YYYY-MM-DD date, if any.
func (c *Calendar) Lookup(date string) (Day, bool) {
	if c == nil {
		return Day{}, false
	}
	d, ok := c.days[date]
	return d, ok
}

func (c *Calendar) isHoliday(date string) bool {
	d, ok := c.Lookup(date)
	return ok && d.Kind == "holiday"
}

func (c *Calendar) isWorkday(date string, w time.Weekday) bool {
	if d, ok := c.Lookup(date); ok {
		return d.Kind == "workday"
	}
	return w != time.Saturday && w != time.Sunday
}

var weekdays = []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}

var dayNames = map[string]string{
	"MO": "MON", "MON": "MON", "MONDAY": "MON",
	"TU": "TUE", "TUE": "TUE", "TUESDAY": "TUE",
	"WE": "WED", "WED": "WED", "WEDNESDAY": "WED",
	"TH": "THU", "THU": "THU", "THURSDAY": "THU",
	"FR": "FRI", "FRI": "FRI", "FRIDAY": "FRI",
	"SA": "SAT", "SAT": "SAT", "SATURDAY": "SAT",
	"SU": "SUN", "SUN": "SUN", "SUNDAY": "SUN",
}

func dayName(w time.Weekday) string { return weekdays[(int(w)+6)%7] }

// parseClock reads "HH:MM" as minutes after midnight.
func parseClock(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(m) != 2 || len(h) < 1 || len(h) > 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// day is a civil date, held at midnight UTC so day arithmetic is exact.
type day = time.Time

func civil(t time.Time) day { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

func daysBetween(a, b day) int { return int(b.Sub(a).Hours() / 24) }

func monday(d day) day { return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)) }

// Zone is the alarm's zone, UTC when it names none the system knows.
func Zone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil || name == "" {
		return time.UTC
	}
	return loc
}

// Occurrences are the times an alarm rings strictly after now, oldest first,
// at most limit (0 or more than MaxOccurrences: MaxOccurrences), within
// windowDays days of today in the alarm's zone (0 or more than WindowDays:
// WindowDays). cal is only read when the alarm's region is CN. A disabled
// alarm rings never.
func Occurrences(a Alarm, cal *Calendar, now time.Time, limit, windowDays int) []time.Time {
	out := []time.Time{}
	if !a.On() {
		return out
	}
	if limit <= 0 || limit > MaxOccurrences {
		limit = MaxOccurrences
	}
	if windowDays <= 0 || windowDays > WindowDays {
		windowDays = WindowDays
	}
	s := a.Schedule
	loc := Zone(s.TimeZone)
	switch s.Kind {
	case KindOneShot:
		at, err := time.Parse(time.RFC3339, s.At)
		// As noted's nextFires: a one-shot alarm is not bound by the full
		// window, only by a shorter one asked for.
		beyond := windowDays < WindowDays && civil(at.In(loc)).After(civil(now.In(loc)).AddDate(0, 0, windowDays))
		if err == nil && at.After(now) && !beyond {
			out = append(out, at.In(loc))
		}
		return out
	case KindRecurring:
	default:
		return out
	}
	if s.Recurrence == nil {
		return out
	}
	rc := *s.Recurrence
	anchor, err := time.Parse("2006-01-02", s.Anchor)
	if err != nil {
		return out
	}
	interval := max(rc.Interval, 1)
	times := dayTimes(s, rc)
	if len(times) == 0 {
		return out
	}
	if a.Region != RegionCN {
		cal = nil
	}

	today := civil(now.In(loc))
	last := today.AddDate(0, 0, windowDays)
	if rc.Until != "" {
		if u, err := time.Parse("2006-01-02", rc.Until); err == nil && u.Before(last) {
			last = u
		}
	}
	subDay := rc.Freq == FreqMinutely || rc.Freq == FreqHourly
	if subDay && rc.Until == "" && rc.Count == 0 && anchor.Before(last) {
		last = anchor // without until or count, a minutely/hourly alarm runs on its anchor day only
	}
	first := anchor
	if rc.Count == 0 && today.After(first) {
		first = today // with a count, rings before today still count, so start at the anchor
	}

	rung := 0
	for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
		if !matches(d, anchor, rc, interval) || !passes(d, a.HolidayPolicy, a.Region, cal) {
			continue
		}
		for _, m := range times {
			fire := time.Date(d.Year(), d.Month(), d.Day(), m/60, m%60, 0, 0, loc)
			rung++
			if fire.After(now) {
				out = append(out, fire)
				if len(out) >= limit {
					return out
				}
			}
			if rc.Count > 0 && rung >= rc.Count {
				return out
			}
		}
	}
	return out
}

func dayTimes(s Schedule, rc Recurrence) []int {
	if rc.Freq == FreqMinutely || rc.Freq == FreqHourly {
		start, ok := parseClock(s.Time)
		if !ok {
			return nil
		}
		end, ok := parseClock(rc.EndTime)
		if !ok {
			end, _ = parseClock(DefaultEndTime)
		}
		step := max(rc.Interval, 1)
		if rc.Freq == FreqHourly {
			step *= 60
		}
		var out []int
		for m := start; m <= end; m += step {
			out = append(out, m)
		}
		return out
	}
	if rc.Freq == FreqDaily && len(rc.ByTime) > 0 {
		seen := map[int]bool{}
		var out []int
		for _, v := range rc.ByTime {
			if m, ok := parseClock(v); ok && !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
		sort.Ints(out)
		return out
	}
	m, ok := parseClock(s.Time)
	if !ok {
		return nil
	}
	return []int{m}
}

func matches(d, anchor day, rc Recurrence, interval int) bool {
	if d.Before(anchor) {
		return false
	}
	switch rc.Freq {
	case FreqMinutely, FreqHourly:
		return true
	case FreqDaily:
		return daysBetween(anchor, d)%interval == 0
	case FreqWeekly:
		byDay := rc.ByDay
		if len(byDay) == 0 {
			byDay = []string{dayName(anchor.Weekday())}
		}
		name := dayName(d.Weekday())
		hit := false
		for _, b := range byDay {
			if dayNames[strings.ToUpper(strings.TrimSpace(b))] == name {
				hit = true
			}
		}
		return hit && (daysBetween(monday(anchor), monday(d))/7)%interval == 0
	case FreqMonthly:
		md := rc.ByMonthDay
		if md == 0 {
			md = anchor.Day()
		}
		months := (d.Year()-anchor.Year())*12 + int(d.Month()) - int(anchor.Month())
		return d.Day() == md && months%interval == 0
	}
	return false
}

func passes(d day, policy, region string, cal *Calendar) bool {
	date := d.Format("2006-01-02")
	switch policy {
	case PolicySkipHoliday:
		if region != RegionCN {
			return true // no local holiday data: ring as usual
		}
		return !cal.isHoliday(date)
	case PolicyWorkdayOnly, PolicyFollowStatutory:
		if region != RegionCN {
			return d.Weekday() != time.Saturday && d.Weekday() != time.Sunday
		}
		return cal.isWorkday(date, d.Weekday())
	}
	return true
}

// Ring is one time a phone should ring.
type Ring struct {
	// ID is derived from everything the ring shows, so the same ring has the
	// same id on every expansion and a ring whose label changed is a new one.
	ID       string `json:"id"`
	AlarmID  string `json:"alarmId"`
	Label    string `json:"label"`
	Note     string `json:"note,omitempty"`
	Mode     string `json:"mode"`
	Fire     string `json:"fire"`
	FireMs   int64  `json:"fireMs"`
	TimeZone string `json:"timeZone"`
	// Listed is the calendar's entry for the ring's day when it has one: a
	// 调休 workday an alarm rings on, say.
	Listed *Day `json:"listed,omitempty"`
}

// Skip is a ring a holiday took away: what the alarm would have rung on that
// day without its holiday policy.
type Skip struct {
	AlarmID string `json:"alarmId"`
	Label   string `json:"label"`
	Date    string `json:"date"`
	Name    string `json:"name"`
	Fire    string `json:"fire"`
}

// Plan is the expansion of every alarm.
type Plan struct {
	Rings   []Ring `json:"rings"`
	Skipped []Skip `json:"skipped"`
}

// Expand is every enabled alarm's rings after now within days days, nearest
// first, and the listed holidays within the window that silence a ring.
func Expand(alarms []Alarm, cal *Calendar, now time.Time, days int) Plan {
	p := Plan{Rings: []Ring{}, Skipped: []Skip{}}
	for _, a := range alarms {
		mode := a.Rings()
		if !a.On() || mode == "" {
			continue
		}
		loc := Zone(a.Schedule.TimeZone)
		rang := map[string]bool{}
		lead := time.Duration(a.RemindBefore) * time.Minute
		for _, f := range Occurrences(a, cal, now.Add(lead), 0, days) {
			f = f.Add(-lead)
			r := Ring{
				ID: RingID(a.ID, f, mode, a.Label, a.Note), AlarmID: a.ID, Label: a.Label, Note: a.Note, Mode: mode,
				Fire: f.Format(time.RFC3339), FireMs: f.UnixMilli(), TimeZone: a.Schedule.TimeZone,
			}
			date := f.In(loc).Format("2006-01-02")
			rang[date] = true
			if a.Region == RegionCN {
				if d, ok := cal.Lookup(date); ok {
					r.Listed = &d
				}
			}
			p.Rings = append(p.Rings, r)
		}
		if a.Region != RegionCN || a.Schedule.Kind != KindRecurring || a.HolidayPolicy == PolicyNone || a.HolidayPolicy == "" {
			continue
		}
		plain := a
		plain.HolidayPolicy = PolicyNone
		for _, f := range Occurrences(plain, cal, now, 0, days) {
			date := f.In(loc).Format("2006-01-02")
			if rang[date] {
				continue
			}
			if d, ok := cal.Lookup(date); ok && d.Kind == "holiday" {
				rang[date] = true // one skip per day, however many times it rings
				p.Skipped = append(p.Skipped, Skip{AlarmID: a.ID, Label: a.Label, Date: date, Name: d.Name, Fire: f.Format(time.RFC3339)})
			}
		}
	}
	sort.SliceStable(p.Rings, func(i, j int) bool {
		if p.Rings[i].FireMs != p.Rings[j].FireMs {
			return p.Rings[i].FireMs < p.Rings[j].FireMs
		}
		return p.Rings[i].ID < p.Rings[j].ID
	})
	sort.SliceStable(p.Skipped, func(i, j int) bool { return p.Skipped[i].Fire < p.Skipped[j].Fire })
	return p
}

// RingID is a UUID (version 5 and RFC 4122 variant bits over the first 16
// bytes of a SHA-256) of the ring's content.
func RingID(alarmID string, fire time.Time, mode, label, note string) string {
	in := fmt.Sprintf("%s|%d|%s|%s|%s", alarmID, fire.Unix(), mode, label, note)
	b := sha256.Sum256([]byte(in))
	u := b[:16]
	u[6] = (u[6] & 0x0f) | 0x50
	u[8] = (u[8] & 0x3f) | 0x80
	h := hex.EncodeToString(u)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
