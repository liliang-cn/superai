package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var shanghai, _ = time.LoadLocation("Asia/Shanghai")

func occ(id, source, title, start, end string, extra ...any) map[string]any {
	m := map[string]any{"id": id, "source": source, "title": title, "startTime": start, "kind": "event"}
	if end != "" {
		m["endTime"] = end
	}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

func titles(items []map[string]any) string {
	var out []string
	for _, it := range items {
		out = append(out, field(it, "title"))
	}
	return strings.Join(out, ",")
}

func TestCollapseAlarmDays(t *testing.T) {
	items := []map[string]any{
		occ("w", "alarm", "喝水", "2026-10-19T09:00:00+08:00", ""),
		occ("m", "event", "周会", "2026-10-19T10:00:00+08:00", "2026-10-19T11:00:00+08:00"),
		occ("w", "alarm", "喝水", "2026-10-19T10:00:00+08:00", ""),
		occ("w", "alarm", "喝水", "2026-10-19T11:00:00+08:00", ""),
		occ("w", "alarm", "喝水", "2026-10-20T09:00:00+08:00", ""),
		occ("g", "alarm", "起床", "2026-10-20T07:00:00+08:00", ""),
	}
	got := collapseAlarmDays(items)
	if titles(got) != "喝水,周会,喝水,起床" {
		t.Fatalf("collapsed: %s", titles(got))
	}
	if rt, _ := got[0]["ringTimes"].([]string); strings.Join(rt, " ") != "09:00 10:00 11:00" {
		t.Fatalf("ringTimes: %v", got[0]["ringTimes"])
	}
	if _, ok := got[2]["ringTimes"]; ok {
		t.Fatalf("a single ring has no ringTimes: %v", got[2])
	}
}

// The home section once showed nothing but the morning's alarms and missed
// the evening meeting.
func TestUpcomingKeepsEventsInView(t *testing.T) {
	now := time.Date(2026, 10, 19, 8, 30, 0, 0, shanghai)
	items := []map[string]any{
		occ("past", "event", "早会", "2026-10-19T07:00:00+08:00", "2026-10-19T08:00:00+08:00"),
		occ("now", "event", "进行中", "2026-10-19T08:00:00+08:00", "2026-10-19T09:00:00+08:00"),
		occ("bday", "event", "生日", "2026-10-19T00:00:00+08:00", "2026-10-20T00:00:00+08:00", "allDay", true),
		occ("w", "alarm", "喝水", "2026-10-19T09:00:00+08:00", ""),
		occ("w", "alarm", "喝水", "2026-10-19T09:30:00+08:00", ""),
		occ("w", "alarm", "喝水", "2026-10-19T10:00:00+08:00", ""),
		occ("p", "alarm", "吃药", "2026-10-19T12:00:00+08:00", ""),
		occ("d", "task", "交房租", "2026-10-19T12:30:00+08:00", "", "kind", "task", "completed", true),
		occ("k", "alarm", "接孩子", "2026-10-19T16:00:00+08:00", ""),
		occ("ev", "event", "晚上开会", "2026-10-19T19:00:00+08:00", "2026-10-19T20:00:00+08:00"),
		occ("t", "task", "写周报", "2026-10-20T18:00:00+08:00", "", "kind", "task"),
	}
	got, more := upcoming(items, now, 5)
	if titles(got) != "进行中,生日,喝水,吃药,晚上开会" || !more {
		t.Fatalf("upcoming: %s more=%v", titles(got), more)
	}
	got, more = upcoming(items, now, 10)
	if titles(got) != "进行中,生日,喝水,吃药,晚上开会,写周报" || !more {
		t.Fatalf("upcoming 10: %s more=%v", titles(got), more)
	}
}

func TestScheduleStart(t *testing.T) {
	now := time.Date(2026, 10, 18, 22, 15, 0, 0, shanghai)
	for in, want := range map[string]string{
		"":                          "2026-10-18T00:00:00+08:00",
		"2026-10-25":                "2026-10-25T00:00:00+08:00",
		"2026-10-20T23:30:00Z":      "2026-10-21T00:00:00+08:00", // 07:30 on the 21st here
		"2026-10-20T09:00:00+08:00": "2026-10-20T00:00:00+08:00",
	} {
		got, err := scheduleStart(in, now)
		if err != nil || got.Format(time.RFC3339) != want {
			t.Errorf("scheduleStart(%q) = %v %v, want %s", in, got, err, want)
		}
	}
	if _, err := scheduleStart("next week", now); err == nil {
		t.Error("nonsense from accepted")
	}
}

func TestFireWatcherPostsEachFire(t *testing.T) {
	now := time.Date(2026, 10, 19, 14, 50, 30, 0, shanghai)
	var asked [2]time.Time
	var posted []PostNotificationRequest
	w := &fireWatcher{
		now: func() time.Time { return now },
		fires: func(_ context.Context, from, to time.Time) ([]map[string]any, bool) {
			asked = [2]time.Time{from, to}
			return []map[string]any{
				{"id": "e1@1", "title": "交周报", "notify": "notification", "fireTime": "2026-10-19T14:50:00+08:00", "atTime": "2026-10-19T15:00:00+08:00", "location": "三楼", "note": "带上数据"},
				{"id": "t1@2", "title": "", "notify": "notification", "fireTime": "2026-10-19T14:45:00+08:00", "atTime": "2026-10-19T14:45:00+08:00"},
			}, true
		},
		post: func(req PostNotificationRequest) error { posted = append(posted, req); return nil },
	}
	w.check(context.Background())
	if !asked[0].Equal(now.Add(-fireLookBack)) || !asked[1].After(now) {
		t.Fatalf("asked for %v", asked)
	}
	if len(posted) != 2 {
		t.Fatalf("posted %d", len(posted))
	}
	p := posted[0]
	if p.Title != "交周报" || p.Body != "15:00 开始 · 三楼\n\n带上数据" || p.Key != "fire:e1@1" || p.Source != "noted" ||
		p.Kind != "info" || p.Deeplink != "superai://schedule" || p.Time != "2026-10-19T14:50:00+08:00" {
		t.Fatalf("notice: %+v", p)
	}
	if posted[1].Title != "日程" || posted[1].Body != "现在 14:45" {
		t.Fatalf("notice at the time: %+v", posted[1])
	}
	// Each one is a valid notice, keyed so a second check does not repeat it.
	if _, err := p.notice(); err != nil {
		t.Fatal(err)
	}
}

// A noted with the unified schedule, recording the arguments it was given;
// old makes it reject the arguments a noted before notify does not know.
func fakeScheduleNoted(t *testing.T, old bool, calls *[]string) string {
	t.Helper()
	var mu sync.Mutex
	record := func(tool string, args any) {
		b, _ := json.Marshal(args)
		mu.Lock()
		*calls = append(*calls, tool+" "+string(b))
		mu.Unlock()
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "noted", Version: "test"}, nil)
	type alarmsIn struct {
		Notify string `json:"notify,omitempty"`
	}
	type oldAlarmsIn struct{}
	type eventsIn struct {
		From          string `json:"from"`
		To            string `json:"to"`
		IncludeTasks  *bool  `json:"include_tasks,omitempty"`
		IncludeAlarms *bool  `json:"include_alarms,omitempty"`
	}
	type oldEventsIn struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	day := time.Now().In(shanghai).Format("2006-01-02")
	events := []any{
		map[string]any{"kind": "event", "source": "alarm", "id": "w", "title": "喝水", "startTime": day + "T09:00:00+08:00", "notify": "reminder"},
		map[string]any{"kind": "event", "source": "alarm", "id": "w", "title": "喝水", "startTime": day + "T10:00:00+08:00", "notify": "reminder"},
		map[string]any{"kind": "event", "source": "event", "id": "m", "title": "开会", "startTime": day + "T23:58:00+08:00", "endTime": day + "T23:59:00+08:00", "notify": "notification"},
	}
	if old {
		mcp.AddTool(s, &mcp.Tool{Name: "list_alarms"}, func(_ context.Context, _ *mcp.CallToolRequest, in oldAlarmsIn) (*mcp.CallToolResult, map[string]any, error) {
			record("list_alarms", in)
			return nil, map[string]any{"items": []any{map[string]any{"id": "a1", "mode": "alarm"}}, "version": "v1"}, nil
		})
		mcp.AddTool(s, &mcp.Tool{Name: "list_events"}, func(_ context.Context, _ *mcp.CallToolRequest, in oldEventsIn) (*mcp.CallToolResult, map[string]any, error) {
			record("list_events", in)
			return nil, map[string]any{"items": events[2:], "from": in.From, "to": in.To}, nil
		})
	} else {
		mcp.AddTool(s, &mcp.Tool{Name: "list_alarms"}, func(_ context.Context, _ *mcp.CallToolRequest, in alarmsIn) (*mcp.CallToolResult, map[string]any, error) {
			record("list_alarms", in)
			return nil, map[string]any{"items": []any{map[string]any{"id": "a1", "mode": "reminder", "notify": "notification", "source": "event"}}, "version": "v2"}, nil
		})
		mcp.AddTool(s, &mcp.Tool{Name: "list_events"}, func(_ context.Context, _ *mcp.CallToolRequest, in eventsIn) (*mcp.CallToolResult, map[string]any, error) {
			record("list_events", in)
			return nil, map[string]any{"items": events, "unscheduled": []any{}, "from": in.From, "to": in.To}, nil
		})
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp"
}

func TestScheduleRPCs(t *testing.T) {
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "unified", true: "older noted"}[old], func(t *testing.T) {
			t.Setenv("SUPERAI_HOME", t.TempDir())
			t.Setenv("NOTED_TOKEN", "x")
			var calls []string
			configureNoted(t, fakeScheduleNoted(t, old, &calls))
			app, _, srv := serveTestApp(t)
			t.Cleanup(func() { app.actionsV.Close() })

			code, body := rpc(t, srv, "NotedRings", `[]`)
			wantRing := `"notify":"notification"`
			if old {
				wantRing = `"mode":"alarm"`
			}
			if code != http.StatusOK || !strings.Contains(string(body), wantRing) {
				t.Fatalf("NotedRings: %d %s", code, body)
			}
			if !old && !strings.Contains(strings.Join(calls, "\n"), `list_alarms {"notify":"all"}`) {
				t.Fatalf("NotedRings asked %v", calls)
			}

			code, body = rpc(t, srv, "NotedSchedule", `["", 3]`)
			var sc struct {
				Items []map[string]any `json:"items"`
				From  string           `json:"from"`
				To    string           `json:"to"`
			}
			_ = json.Unmarshal(body, &sc)
			wantItems := 2 // the two rings of 喝水 are one row
			if old {
				wantItems = 1
			}
			if code != http.StatusOK || len(sc.Items) != wantItems {
				t.Fatalf("NotedSchedule: %d %s", code, body)
			}
			from, _ := time.Parse(time.RFC3339, sc.From)
			to, _ := time.Parse(time.RFC3339, sc.To)
			if to.Sub(from) != 72*time.Hour || from.Hour() != 0 {
				t.Fatalf("window %s .. %s", sc.From, sc.To)
			}
			if !old {
				if rt, _ := sc.Items[0]["ringTimes"].([]any); len(rt) != 2 {
					t.Fatalf("ringTimes: %v", sc.Items[0])
				}
			}
			if code, _ := rpc(t, srv, "NotedSchedule", `["", 90]`); code != http.StatusBadRequest {
				t.Fatalf("90 days: %d", code)
			}

			code, body = rpc(t, srv, "NotedUpcoming", `[0]`)
			if code != http.StatusOK || !strings.Contains(string(body), "开会") {
				t.Fatalf("NotedUpcoming: %d %s", code, body)
			}
		})
	}
}
