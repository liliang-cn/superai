package app

// The schedule (日程安排).
//
// noted keeps one schedule: calendar events, to-dos with a time and alarms,
// each with a notify saying how it reaches the person — alarm (the phone rings
// it like an alarm clock), reminder (the phone posts a timed notification),
// notification (a plain notice: this process posts it, below) or none (shown
// only). The reads here are what the clients draw and ring from:
//
//   - NotedSchedule: the schedule page, a window of days, every item with
//     its notify. A repeating alarm's rings on one day are one row.
//   - NotedUpcoming: the home section, the next few items, a repeating alarm
//     as its next ring and at most a couple of alarms, so events always show.
//   - NotedRings: what the phones' ring engines schedule — every item that
//     notifies, as an alarm-style schedule with notify and
//     remindBeforeMinutes. NotedAlarms stays as it was for older apps.
//
// Changes go through CallTool on noted's own tools (create_event,
// create_alarm, create_task, update_event with notify, update_alarm, …);
// any of them pokes the alarm watcher, whose version covers the whole
// schedule, so the phones resync their rings.
//
// Items with notify notification are not rung by the phones: the fire
// watcher here asks noted's list_fires once a minute and posts each one that
// is due as a notice (PostNotification), which reaches the notification
// centre and every paired phone as a push, once per fire.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

const (
	scheduleDefaultDays = 7
	scheduleMaxDays     = 62
	upcomingDefault     = 5
	upcomingMax         = 20
	// upcomingAlarms caps the alarms in NotedUpcoming, so a morning of rings
	// cannot push the evening's meeting off the home screen.
	upcomingAlarms   = 2
	upcomingHorizon  = 7 * 24 * time.Hour
	firePollEvery    = 60 * time.Second
	fireLookBack     = 10 * time.Minute
	fireSource       = "noted"
	scheduleDeeplink = "superai://schedule"
)

// NotedRings is every schedule item that reaches the person, for the phones'
// ring engines (list_alarms, notify all):
//
//	POST /api/rpc/NotedRings  []
//	→ {"items":[{"kind":"alarm","id":"…","notify":"reminder","remindBeforeMinutes":10,"source":"event","schedule":{…},"nextFires":[…],…}],"version":"…"}
//
// Ring each schedule time less remindBeforeMinutes: notify alarm through
// AlarmKit / setAlarmClock, reminder as a timed local notification;
// notification is posted from here as a notice, none is never listed. A noted
// that predates notify answers its plain alarm list (items without notify:
// read mode).
func (a *App) NotedRings() (any, error) {
	return a.notedReadCompat("list_alarms", map[string]any{"notify": "all"}, map[string]any{})
}

// NotedSchedule is the schedule page: every calendar event, to-do with a time
// and alarm from the day of from for days days (list_events with tasks and
// alarms), plus the open to-dos with no time as unscheduled:
//
//	POST /api/rpc/NotedSchedule  ["2026-10-18", 7]
//	→ {"items":[{"kind":"event"|"task","source":"event"|"task"|"alarm","id":"…","title":"…","startTime":"…","notify":"reminder",…}],"unscheduled":[…],"from":"…","to":"…"}
//
// from is a date (YYYY-MM-DD) or RFC 3339; empty is today. days is 1-62,
// 0 for a week. A repeating alarm's rings on one day are one item, at its
// first ring, with ringTimes listing the day's rings (HH:MM) when there are
// several.
func (a *App) NotedSchedule(from string, days int) (any, error) {
	start, err := scheduleStart(from, time.Now())
	if err != nil {
		return nil, &callError{http.StatusBadRequest, "NotedSchedule: " + err.Error()}
	}
	if days == 0 {
		days = scheduleDefaultDays
	}
	if days < 1 || days > scheduleMaxDays {
		return nil, &callError{http.StatusBadRequest, fmt.Sprintf("NotedSchedule: days must be 1-%d", scheduleMaxDays)}
	}
	end := start.AddDate(0, 0, days)
	res, err := a.notedEvents(start, end)
	if err != nil {
		return nil, err
	}
	m, ok := res.(map[string]any)
	if !ok {
		return res, nil
	}
	m["items"] = collapseAlarmDays(schedItems(m["items"]))
	return m, nil
}

// NotedUpcoming is the home section 接下来的安排: the next items from now,
// within a week, by time — an event under way included, done to-dos left out,
// each repeating alarm as its next ring and at most two alarms in all:
//
//	POST /api/rpc/NotedUpcoming  [5]
//	→ {"items":[…as NotedSchedule's…],"more":true}
//
// limit is 1-20, 0 for 5. more says whether anything was left out.
func (a *App) NotedUpcoming(limit int) (any, error) {
	if limit == 0 {
		limit = upcomingDefault
	}
	if limit < 1 || limit > upcomingMax {
		return nil, &callError{http.StatusBadRequest, fmt.Sprintf("NotedUpcoming: limit must be 1-%d", upcomingMax)}
	}
	now := time.Now()
	y, mo, d := now.Date()
	res, err := a.notedEvents(time.Date(y, mo, d, 0, 0, 0, 0, now.Location()), now.Add(upcomingHorizon))
	if err != nil {
		return nil, err
	}
	m, _ := res.(map[string]any)
	items, more := upcoming(schedItems(m["items"]), now, limit)
	return map[string]any{"items": items, "more": more}, nil
}

// notedEvents is list_events over [from, to) with the to-dos and alarms in
// it; a noted that predates the unified schedule gets the plain call.
func (a *App) notedEvents(from, to time.Time) (any, error) {
	args := map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}
	full := map[string]any{"include_tasks": true, "include_alarms": true}
	for k, v := range args {
		full[k] = v
	}
	return a.notedReadCompat("list_events", full, args)
}

// notedReadCompat is notedRead with args, and with older args when noted
// refuses the new ones (an older noted rejects arguments it does not know).
func (a *App) notedReadCompat(tool string, args, older map[string]any) (any, error) {
	res, err := a.notedRead(tool, args)
	var ce *callError
	if err != nil && errors.As(err, &ce) && ce.status == http.StatusBadGateway {
		if res2, err2 := a.notedRead(tool, older); err2 == nil {
			return res2, nil
		}
	}
	return res, err
}

// scheduleStart reads NotedSchedule's from: "" today, a date, or RFC 3339;
// always the start of that day in now's zone.
func scheduleStart(from string, now time.Time) (time.Time, error) {
	from = strings.TrimSpace(from)
	t := now
	switch {
	case from == "":
	case len(from) == len("2006-01-02"):
		d, err := time.ParseInLocation("2006-01-02", from, now.Location())
		if err != nil {
			return time.Time{}, errors.New("from is not a date (YYYY-MM-DD) or RFC 3339")
		}
		t = d
	default:
		d, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return time.Time{}, errors.New("from is not a date (YYYY-MM-DD) or RFC 3339")
		}
		t = d.In(now.Location())
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), nil
}

func schedItems(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func field(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func isAlarmItem(m map[string]any) bool { return field(m, "source") == "alarm" }

func itemStart(m map[string]any) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, field(m, "startTime"))
	return t, err == nil
}

// collapseAlarmDays makes each alarm one item per day it rings: the first
// ring, with ringTimes when it rings more than once that day. Occurrences
// come by start time, so the first seen is the first ring.
func collapseAlarmDays(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	first := map[string]map[string]any{}
	for _, it := range items {
		start, ok := itemStart(it)
		if !isAlarmItem(it) || !ok {
			out = append(out, it)
			continue
		}
		key := field(it, "id") + "|" + start.Format("2006-01-02")
		if f, seen := first[key]; seen {
			times, _ := f["ringTimes"].([]string)
			if len(times) == 0 {
				fs, _ := itemStart(f)
				times = []string{fs.Format("15:04")}
			}
			f["ringTimes"] = append(times, start.Format("15:04"))
			continue
		}
		first[key] = it
		out = append(out, it)
	}
	return out
}

// upcoming picks NotedUpcoming's items from a window's occurrences.
func upcoming(items []map[string]any, now time.Time, limit int) ([]map[string]any, bool) {
	out := []map[string]any{}
	seen := map[string]bool{}
	alarms, more := 0, false
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	for _, it := range items {
		start, ok := itemStart(it)
		if !ok || it["completed"] == true {
			continue
		}
		end := start
		if e, err := time.Parse(time.RFC3339, field(it, "endTime")); err == nil {
			end = e
		}
		allDay := it["allDay"] == true
		switch {
		case allDay && start.Before(today):
			continue
		case !allDay && end.After(start) && !end.After(now): // an event that is over
			continue
		case !allDay && !end.After(start) && start.Before(now): // a moment that has passed
			continue
		}
		if isAlarmItem(it) {
			id := field(it, "id")
			if seen[id] {
				continue // a repeating alarm shows its next ring only
			}
			seen[id] = true
			if alarms == upcomingAlarms {
				more = true
				continue
			}
			alarms++
		}
		if len(out) == limit {
			more = true
			break
		}
		out = append(out, it)
	}
	return out, more
}

// ---- notification-mode fires ----

// fireWatcher posts the notification-mode fires noted says are due. Its
// parts are functions so a test can stand in for noted and the notices.
type fireWatcher struct {
	every time.Duration
	now   func() time.Time
	// fires asks noted for the notification fires in [from, to); false when
	// there is nobody to ask.
	fires func(ctx context.Context, from, to time.Time) ([]map[string]any, bool)
	post  func(req PostNotificationRequest) error
}

func (w *fireWatcher) run(ctx context.Context) {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		w.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// check posts every fire due in the last few minutes. PostNotification's key
// makes each one land once, across overlapping checks and restarts.
func (w *fireWatcher) check(ctx context.Context) {
	now := w.now()
	fs, ok := w.fires(ctx, now.Add(-fireLookBack), now.Add(time.Second))
	if !ok {
		return
	}
	for _, f := range fs {
		if err := w.post(fireNotice(f, now.Location())); err != nil {
			log.Printf("schedule: posting %s: %v", field(f, "id"), err)
		}
	}
}

// fireNotice is the notice for one fire: its title, and when the thing is.
func fireNotice(f map[string]any, loc *time.Location) PostNotificationRequest {
	body := ""
	if at, err := time.Parse(time.RFC3339, field(f, "atTime")); err == nil {
		at = at.In(loc)
		switch {
		case f["allDay"] == true:
			body = at.Format("1月2日") + " 全天"
		case field(f, "fireTime") == field(f, "atTime"):
			body = "现在 " + at.Format("15:04")
		default:
			body = at.Format("15:04") + " 开始"
		}
	}
	if loc := field(f, "location"); loc != "" {
		body += " · " + loc
	}
	if note := strings.TrimSpace(field(f, "note")); note != "" {
		body += "\n\n" + note
	}
	title := strings.TrimSpace(field(f, "title"))
	if title == "" {
		title = "日程"
	}
	return PostNotificationRequest{Title: title, Body: strings.TrimSpace(body), Source: fireSource, Kind: backend.KindInfo,
		Key: "fire:" + field(f, "id"), Deeplink: scheduleDeeplink, Time: field(f, "fireTime")}
}

// notedFires asks noted for the notification fires in [from, to). An
// unconfigured noted is skipped without a call; a failing one is logged once
// per reason.
func (a *App) notedFires(ctx context.Context, from, to time.Time) ([]map[string]any, bool) {
	acts := a.mcpActions()
	if ok, _ := acts.Configured(backend.NotedServer); !ok {
		return nil, false
	}
	res := acts.Call(ctx, backend.NotedServer, "list_fires", map[string]any{
		"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339), "notify": "notification",
	}, backend.MCPCallOptions{Unlisted: true, Quiet: true, Timeout: 15 * time.Second})
	why := ""
	if !res.OK {
		why = res.Code + ": " + res.Error
	}
	a.firesMu.Lock()
	if why != a.firesErr {
		if why != "" {
			log.Printf("schedule: list_fires: %s", why)
		}
		a.firesErr = why
	}
	a.firesMu.Unlock()
	if !res.OK {
		return nil, false
	}
	m, _ := res.Structured.(map[string]any)
	fs := schedItems(m["items"])
	sort.SliceStable(fs, func(i, j int) bool { return field(fs[i], "fireTime") < field(fs[j], "fireTime") })
	return fs, true
}

// startFireWatch runs the fire watcher where the alarm watcher runs: on the
// queen, the one the phones are paired to.
func (a *App) startFireWatch(ctx context.Context) {
	w := &fireWatcher{every: firePollEvery, now: time.Now, fires: a.notedFires,
		post: func(req PostNotificationRequest) error { _, err := a.PostNotification(req); return err }}
	go w.run(ctx)
}
