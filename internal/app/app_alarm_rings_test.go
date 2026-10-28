package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeRingsNoted is noted with a workday alarm, a disabled one and a
// one-shot reminder, and a 2026 calendar with 国庆 and its 调休 Saturday.
// holidayCalls counts get_holidays.
func fakeRingsNoted(t *testing.T, token string, holidayCalls *atomic.Int64) string {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "noted", Version: "test"}, nil)
	type noIn struct{}
	mcp.AddTool(s, &mcp.Tool{Name: "list_alarms"}, func(context.Context, *mcp.CallToolRequest, noIn) (*mcp.CallToolResult, map[string]any, error) {
		items := []any{
			map[string]any{"kind": "alarm", "id": "w", "link": "noted://alarm/w", "label": "上班", "mode": "alarm", "enabled": true,
				"holidayPolicy": "workdayOnly", "region": "CN", "nextFires": []any{},
				"schedule": map[string]any{"kind": "recurring", "anchor": "2026-09-01", "time": "07:00", "timeZone": "Asia/Shanghai",
					"recurrence": map[string]any{"freq": "daily", "interval": 1}}},
			map[string]any{"kind": "alarm", "id": "off", "label": "关着", "mode": "alarm", "enabled": false, "holidayPolicy": "none", "region": "CN",
				"schedule": map[string]any{"kind": "recurring", "anchor": "2026-09-01", "time": "06:00", "timeZone": "Asia/Shanghai",
					"recurrence": map[string]any{"freq": "daily", "interval": 1}}},
			map[string]any{"kind": "alarm", "id": "r", "label": "取快递", "mode": "reminder", "enabled": true, "holidayPolicy": "none", "region": "",
				"schedule": map[string]any{"kind": "oneShot", "at": "2026-10-01T18:00:00+08:00", "anchor": "", "time": "", "timeZone": "Asia/Shanghai"}},
		}
		return nil, map[string]any{"items": items, "version": 7}, nil
	})
	type holIn struct {
		Year int `json:"year,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "get_holidays"}, func(_ context.Context, _ *mcp.CallToolRequest, in holIn) (*mcp.CallToolResult, map[string]any, error) {
		holidayCalls.Add(1)
		days := []any{}
		if in.Year == 2026 {
			days = []any{
				map[string]any{"date": "2026-10-01", "kind": "holiday", "name": "国庆节"},
				map[string]any{"date": "2026-10-02", "kind": "holiday", "name": "国庆节"},
				map[string]any{"date": "2026-10-10", "kind": "workday", "name": "国庆节"},
			}
		}
		return nil, map[string]any{"year": in.Year, "region": "CN", "days": days, "version": "holiday-cn.2026"}, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp"
}

func TestAlarmRingsExpandsWithHolidays(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	app, _, srv := serveTestApp(t)
	t.Cleanup(func() { app.actionsV.Close() })

	if code, body := rpc(t, srv, "AlarmRings", `[14]`); code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured: %d %s", code, body)
	}

	t.Setenv("NOTED_TOKEN", "sekrit")
	var calls atomic.Int64
	configureNoted(t, fakeRingsNoted(t, "sekrit", &calls))

	now, _ := time.Parse(time.RFC3339, "2026-09-30T08:00:00+08:00")
	got, err := app.alarmRingsAt(now, 14)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "7" || got.Days != 14 || len(got.Alarms) != 3 || got.HolidaysMissing {
		t.Fatalf("result: %+v", got)
	}
	var dates []string
	for _, r := range got.Rings {
		if r.AlarmID == "off" {
			t.Fatal("a disabled alarm rang")
		}
		if r.AlarmID == "w" {
			dates = append(dates, r.Fire[:10])
		}
	}
	all := strings.Join(dates, " ")
	if strings.Contains(all, "2026-10-01") || strings.Contains(all, "2026-10-04") || !strings.Contains(all, "2026-10-10") || !strings.HasPrefix(all, "2026-10-05") {
		t.Errorf("workday rings: %s", all)
	}
	if got.Rings[0].AlarmID != "r" || got.Rings[0].Mode != "reminder" {
		t.Errorf("first ring: %+v", got.Rings[0])
	}
	if len(got.Skipped) != 2 || got.Skipped[0].Name != "国庆节" {
		t.Errorf("skipped: %+v", got.Skipped)
	}
	if calls.Load() == 0 {
		t.Fatal("no holidays read")
	}

	// Asked again within the hour: the calendar comes from the cache, and
	// the ids are the same.
	before := calls.Load()
	again, _ := app.alarmRingsAt(now.Add(time.Hour), 14)
	if calls.Load() != before {
		t.Errorf("holidays fetched again: %d then %d", before, calls.Load())
	}
	for i := range got.Rings {
		if got.Rings[i].ID != again.Rings[i].ID {
			t.Fatalf("ring %d's id moved between two asks", i)
		}
	}
	// Over the wire, with the default window.
	code, body := rpc(t, srv, "AlarmRings", `[0]`)
	var wire struct {
		Days  int `json:"days"`
		Rings []struct {
			ID     string `json:"id"`
			FireMs int64  `json:"fireMs"`
			Listed *struct {
				Kind string `json:"kind"`
			} `json:"listed"`
		} `json:"rings"`
		Alarms []map[string]any `json:"alarms"`
	}
	if err := json.Unmarshal(body, &wire); code != http.StatusOK || err != nil || wire.Days != 180 || len(wire.Alarms) != 3 || wire.Alarms[0]["link"] != "noted://alarm/w" {
		t.Fatalf("rpc: %d %s", code, body)
	}
	// Close the MCP session before the fake noted, which waits for it.
	app.actionsV.Close()
}
