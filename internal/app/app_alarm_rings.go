package app

// AlarmRings: noted's alarms already expanded into the rings a phone
// schedules, with the CN statutory holidays and 调休 applied here, so a phone
// (the Android app) carries no holiday engine of its own. The rules are
// noted's, in internal/alarmplan with noted's shared fixtures.

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/alarmplan"
)

// holidayTTL is how long a year's calendar is used before noted is asked
// again. It changes once a year, when the State Council publishes the next.
const holidayTTL = 6 * time.Hour

type holidayYear struct {
	days    []alarmplan.Day
	version string
	at      time.Time
}

// holidayCache keeps each year's days from get_holidays.
type holidayCache struct {
	mu    sync.Mutex
	years map[int]holidayYear
}

// AlarmRingsResult is AlarmRings' answer.
type AlarmRingsResult struct {
	// Version is list_alarms' version: the one noted:alarms carries, so a
	// phone that heard it can tell whether it is already up to date.
	Version string `json:"version"`
	// HolidaysVersion moves when any year's calendar does.
	HolidaysVersion string `json:"holidaysVersion"`
	// HolidaysMissing is set when a CN alarm follows holidays and a year it
	// reaches could not be read: those days rang as ordinary days.
	HolidaysMissing bool   `json:"holidaysMissing,omitempty"`
	Days            int    `json:"days"`
	GeneratedAt     string `json:"generatedAt"`
	// Alarms is list_alarms' items as noted gave them, for the alarms page.
	Alarms  []json.RawMessage `json:"alarms"`
	Rings   []alarmplan.Ring  `json:"rings"`
	Skipped []alarmplan.Skip  `json:"skipped"`
}

// AlarmRings is every enabled alarm's rings for the next days days (0 or
// more than 180: 180), nearest first, holidays and 调休 applied:
//
//	POST /api/rpc/AlarmRings  [180]
//	→ {"version":"…","holidaysVersion":"…","days":180,"generatedAt":"…",
//	   "alarms":[…list_alarms items…],
//	   "rings":[{"id":"<uuid>","alarmId":"…","label":"起床","mode":"alarm"|"reminder",
//	             "fire":"2026-10-19T07:00:00+08:00","fireMs":…,"timeZone":"Asia/Shanghai",
//	             "listed":{"date":"2026-10-10","kind":"workday","name":"国庆节"}}],
//	   "skipped":[{"alarmId":"…","label":"…","date":"2026-10-01","name":"国庆节","fire":"…"}]}
//
// A ring's id is a hash of what it shows, so asking twice gives the same ids.
// 503 when noted is not configured, 502 when it fails, as NotedAlarms.
func (a *App) AlarmRings(days int) (any, error) {
	r, err := a.alarmRingsAt(time.Now(), days)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (a *App) alarmRingsAt(now time.Time, days int) (*AlarmRingsResult, error) {
	if days <= 0 || days > alarmplan.WindowDays {
		days = alarmplan.WindowDays
	}
	raw, err := a.notedRead("list_alarms", map[string]any{})
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(raw)
	var feed struct {
		Items   []json.RawMessage `json:"items"`
		Version any               `json:"version"`
	}
	_ = json.Unmarshal(b, &feed)
	out := &AlarmRingsResult{
		Version: versionOf(map[string]any{"version": feed.Version}), Days: days,
		GeneratedAt: now.Format(time.RFC3339), Alarms: feed.Items,
	}
	if out.Alarms == nil {
		out.Alarms = []json.RawMessage{}
	}
	var alarms []alarmplan.Alarm
	needs := false
	for _, it := range feed.Items {
		var al alarmplan.Alarm
		if json.Unmarshal(it, &al) != nil || al.ID == "" {
			continue
		}
		alarms = append(alarms, al)
		if al.On() && al.Region == alarmplan.RegionCN && al.Schedule.Kind == alarmplan.KindRecurring {
			needs = true
		}
	}
	var cal *alarmplan.Calendar
	if needs {
		var missing bool
		cal, out.HolidaysVersion, missing = a.ringCalendar(now, days)
		out.HolidaysMissing = missing
	}
	p := alarmplan.Expand(alarms, cal, now, days)
	out.Rings, out.Skipped = p.Rings, p.Skipped
	return out, nil
}

// ringCalendar is the CN calendar for every year the window touches, from
// the cache while fresh, else from noted, else the cache however old. missing
// is set when a year could not be had at all.
func (a *App) ringCalendar(now time.Time, days int) (cal *alarmplan.Calendar, version string, missing bool) {
	first := now.UTC().AddDate(0, 0, -1).Year()
	last := now.UTC().AddDate(0, 0, days+1).Year()
	c := &a.ringHolidays
	var all []alarmplan.Day
	for y := first; y <= last; y++ {
		c.mu.Lock()
		got, ok := c.years[y]
		c.mu.Unlock()
		if !ok || now.Sub(got.at) > holidayTTL || now.Before(got.at) {
			if fresh, err := a.fetchHolidays(y); err == nil {
				fresh.at = now
				got, ok = fresh, true
				c.mu.Lock()
				if c.years == nil {
					c.years = map[int]holidayYear{}
				}
				c.years[y] = fresh
				c.mu.Unlock()
			} else {
				log.Printf("alarms: holidays %d: %v", y, err)
			}
		}
		if !ok {
			missing = true
			continue
		}
		all = append(all, got.days...)
		if version == "" {
			version = got.version
		} else if got.version != "" && got.version != version {
			version += "+" + got.version
		}
	}
	return alarmplan.NewCalendar(all), version, missing
}

func (a *App) fetchHolidays(year int) (holidayYear, error) {
	raw, err := a.notedRead("get_holidays", map[string]any{"year": year})
	if err != nil {
		return holidayYear{}, err
	}
	b, _ := json.Marshal(raw)
	var got struct {
		Year    int             `json:"year"`
		Days    []alarmplan.Day `json:"days"`
		Version any             `json:"version"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		return holidayYear{}, err
	}
	// A year noted has no data for is empty days: every day ordinary, and
	// worth remembering as such until the cache expires.
	return holidayYear{days: got.Days, version: versionOf(map[string]any{"version": got.Version})}, nil
}
