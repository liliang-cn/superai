package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liliang-cn/superai/internal/apns"
	"github.com/liliang-cn/superai/internal/backend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeAlarmsNoted is noted with alarms: list_alarms answers the current
// version, update_alarm moves it, get_holidays echoes its year.
func fakeAlarmsNoted(t *testing.T, token string, version *atomic.Int64) string {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "noted", Version: "test"}, nil)
	type noIn struct{}
	mcp.AddTool(s, &mcp.Tool{Name: "list_alarms"}, func(context.Context, *mcp.CallToolRequest, noIn) (*mcp.CallToolResult, map[string]any, error) {
		v := fmt.Sprintf("v%d", version.Load())
		return nil, map[string]any{"items": []any{map[string]any{"kind": "alarm", "id": "a1", "link": "noted://alarm/a1", "label": "接三宝"}}, "version": v}, nil
	})
	type updIn struct {
		ID      string `json:"id"`
		Enabled *bool  `json:"enabled,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "update_alarm"}, func(_ context.Context, _ *mcp.CallToolRequest, in updIn) (*mcp.CallToolResult, map[string]any, error) {
		version.Add(1)
		return nil, map[string]any{"kind": "alarm", "id": in.ID, "enabled": in.Enabled != nil && *in.Enabled}, nil
	})
	type holIn struct {
		Year int `json:"year,omitempty"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "get_holidays"}, func(_ context.Context, _ *mcp.CallToolRequest, in holIn) (*mcp.CallToolResult, map[string]any, error) {
		year := in.Year
		if year == 0 {
			year = -1 // "you left it to me"
		}
		return nil, map[string]any{"year": year, "region": "CN", "days": []any{map[string]any{"date": "2026-10-01", "kind": "holiday", "name": "国庆节"}}}, nil
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

type wake struct{ token, env, kind, version string }

// fakePusher records pushes; tokens in dead answer like Apple does for an
// uninstalled app.
type fakePusher struct {
	mu    sync.Mutex
	got   []wake
	dead  map[string]bool
	woken chan struct{}
}

func (p *fakePusher) Wake(_ context.Context, token, env string, data any) error {
	m, _ := data.(map[string]any)
	p.mu.Lock()
	p.got = append(p.got, wake{token, env, fmt.Sprint(m["kind"]), fmt.Sprint(m["version"])})
	p.mu.Unlock()
	if p.woken != nil {
		p.woken <- struct{}{}
	}
	if p.dead[token] {
		return fmt.Errorf("%w (410 Unregistered)", apns.ErrDeviceGone)
	}
	return nil
}

func (p *fakePusher) wakes() []wake {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]wake(nil), p.got...)
}

// pairedWithPush pairs two phones in the test home and registers push tokens.
func pairedWithPush(t *testing.T) *deviceStore {
	t.Helper()
	s, err := openDevices(devicesPath())
	if err != nil {
		t.Fatal(err)
	}
	for i, tok := range []string{strings.Repeat("aa", 32), strings.Repeat("bb", 32)} {
		code, _ := s.newCode()
		d, _, ok, err := s.claim(code, fmt.Sprintf("phone %d", i))
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
		env := []string{"sandbox", ""}[i]
		if err := s.setPushToken(d.ID, tok, env, ""); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func nextAlarmsEvent(t *testing.T, ch <-chan []byte) map[string]any {
	t.Helper()
	select {
	case b := <-ch:
		var ev struct {
			Name    string         `json:"name"`
			Payload map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(b, &ev)
		if ev.Name != alarmsEvent {
			t.Fatalf("event %s", b)
		}
		return ev.Payload
	case <-time.After(5 * time.Second):
		t.Fatal("no noted:alarms event")
	}
	return nil
}

func TestAlarmWatcherAnnouncesAndPushesChanges(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	t.Setenv("NOTED_TOKEN", "sekrit")
	var version atomic.Int64
	version.Store(1)
	configureNoted(t, fakeAlarmsNoted(t, "sekrit", &version))
	app, hub, srv := serveTestApp(t)
	t.Cleanup(func() { app.actionsV.Close() })
	store := pairedWithPush(t)

	events, off := hub.subscribe(eventFilter{alarmsEvent})
	defer off()
	p := &fakePusher{dead: map[string]bool{strings.Repeat("bb", 32): true}}
	w := app.alarmWatch()
	w.pusher = func() alarmPusher { return p }
	ctx := context.Background()

	// The first version ever is where it starts, not a change.
	w.check(ctx)
	if got := p.wakes(); len(got) != 0 {
		t.Fatalf("pushed on the first look: %v", got)
	}
	if raw, _ := os.ReadFile(filepath.Join(backend.DataDir(), alarmsStateFile)); strings.TrimSpace(string(raw)) != "v1" {
		t.Fatalf("version kept on disk = %q", raw)
	}
	// Nothing moved: nothing said.
	w.check(ctx)
	if len(p.wakes()) != 0 {
		t.Fatal("pushed without a change")
	}

	// A change: the event, and a push to each phone.
	version.Store(2)
	w.check(ctx)
	if ev := nextAlarmsEvent(t, events); ev["version"] != "v2" {
		t.Errorf("event payload %v", ev)
	}
	got := p.wakes()
	if len(got) != 2 {
		t.Fatalf("wakes = %v, want both phones", got)
	}
	for _, g := range got {
		if g.kind != "alarms" || g.version != "v2" {
			t.Errorf("push data %+v", g)
		}
		if g.token == strings.Repeat("aa", 32) && g.env != "sandbox" {
			t.Errorf("the sandbox phone was pushed in %q", g.env)
		}
	}
	// The phone Apple called dead is forgotten; the other keeps its token.
	targets := store.pushTargets()
	if len(targets) != 1 || targets[0].Token != strings.Repeat("aa", 32) {
		t.Errorf("targets after a dead token = %+v", targets)
	}

	// A restart remembers where it was: a change made while down is
	// announced, the same version is not.
	app.alarmsV = nil
	app.alarmsOnce = sync.Once{}
	w = app.alarmWatch()
	w.pusher = func() alarmPusher { return p }
	w.check(ctx)
	if n := len(p.wakes()); n != 2 {
		t.Errorf("same version after a restart pushed again (%d)", n)
	}
	version.Store(3)
	w.check(ctx)
	if ev := nextAlarmsEvent(t, events); ev["version"] != "v3" {
		t.Errorf("change while down: %v", ev)
	}

	// A button that runs an alarm tool sends the running watcher to look at
	// once, not a minute later.
	p.woken = make(chan struct{}, 4)
	w.every = time.Hour
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go w.run(runCtx)
	code, body := rpc(t, srv, "CallTool", `["noted","update_alarm",{"id":"a1","enabled":false},{"label":"关闭"}]`)
	if code != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("CallTool: %d %s", code, body)
	}
	if ev := nextAlarmsEvent(t, events); ev["version"] != "v4" {
		t.Errorf("after update_alarm: %v", ev)
	}
	select {
	case <-p.woken:
	case <-time.After(5 * time.Second):
		t.Fatal("no push after update_alarm")
	}
}

func TestAlarmWatcherWithoutPushConfigStillAnnounces(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	t.Setenv("NOTED_TOKEN", "sekrit")
	for _, k := range []string{"APNS_KEY", "APNS_KEY_PATH", "APNS_KEY_ID"} {
		t.Setenv(k, "")
	}
	var version atomic.Int64
	version.Store(1)
	configureNoted(t, fakeAlarmsNoted(t, "sekrit", &version))
	app, hub, _ := serveTestApp(t)
	t.Cleanup(func() { app.actionsV.Close() })
	app.settings = &backend.Settings{}
	pairedWithPush(t)

	if p := app.alarmPusher(); p != nil {
		t.Fatalf("a pusher with no key: %T", p)
	}
	events, off := hub.subscribe(eventFilter{alarmsEvent})
	defer off()
	w := app.alarmWatch()
	w.check(context.Background())
	version.Store(2)
	w.check(context.Background()) // must not panic or block on a nil pusher
	if ev := nextAlarmsEvent(t, events); ev["version"] != "v2" {
		t.Errorf("event %v", ev)
	}
}

func TestAlarmWatcherSkipsAnUnconfiguredNoted(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	app := NewApp()
	if v, ok := app.notedAlarmsVersion(context.Background()); ok || v != "" {
		t.Errorf("version without noted = %q, %v", v, ok)
	}
}

func TestAlarmPusherFromEnvironment(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	_, p8 := testP8(t)
	t.Setenv("APNS_KEY_PATH", "")
	t.Setenv("APNS_KEY", string(p8))
	t.Setenv("APNS_KEY_ID", "KID1234567")
	app := NewApp()
	app.settings = &backend.Settings{}
	p, ok := app.alarmPusher().(*apns.Client)
	if !ok || p.Topic() != backend.DefaultAPNsTopic {
		t.Fatalf("pusher = %v", p)
	}
	if again := app.alarmPusher(); again != alarmPusher(p) {
		t.Error("rebuilt with nothing changed")
	}
	t.Setenv("APNS_KEY_ID", "")
	if app.alarmPusher() != nil {
		t.Error("still pushing after the key id went")
	}
}

func TestTouchesAlarms(t *testing.T) {
	for _, c := range []struct {
		tool string
		args map[string]any
		want bool
	}{
		{"mcp_noted_create_alarm", nil, true},
		{"mcp_noted_delete_alarm", nil, true},
		{"mcp_noted_complete_task", nil, true}, // a done to-do stops ringing
		{"mcp_noted_update_event", nil, true},
		{"mcp_noted_list_events", nil, false},
		{"mcp_noted_create_note", nil, false},
		{"mcp_other_set_alarm", nil, false},
		{"execute_javascript", map[string]any{"code": "await mcp_noted_update_alarm({id:'a1'})"}, true},
		{"execute_javascript", map[string]any{"code": "await mcp_noted_list_tasks({})"}, false},
		{"execute_javascript", map[string]any{"code": "const t = await mcp_noted_list_tasks({}); await mcp_noted_update_task({id:t.items[0].id})"}, true},
	} {
		if got := touchesAlarms(c.tool, c.args); got != c.want {
			t.Errorf("touchesAlarms(%s, %v) = %v", c.tool, c.args, got)
		}
	}
}

func TestNotedAlarmsAndHolidaysRPCs(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	app, _, srv := serveTestApp(t)
	t.Cleanup(func() { app.actionsV.Close() })

	for _, m := range []string{"NotedAlarms", "NotedHolidays"} {
		args := `[]`
		if m == "NotedHolidays" {
			args = `[2026]`
		}
		if code, body := rpc(t, srv, m, args); code != http.StatusServiceUnavailable {
			t.Errorf("unconfigured %s: %d %s", m, code, body)
		}
	}

	t.Setenv("NOTED_TOKEN", "sekrit")
	var version atomic.Int64
	version.Store(5)
	configureNoted(t, fakeAlarmsNoted(t, "sekrit", &version))

	code, body := rpc(t, srv, "NotedAlarms", `[]`)
	var list struct {
		Items   []map[string]any `json:"items"`
		Version string           `json:"version"`
	}
	_ = json.Unmarshal(body, &list)
	if code != http.StatusOK || list.Version != "v5" || len(list.Items) != 1 || list.Items[0]["link"] != "noted://alarm/a1" {
		t.Errorf("NotedAlarms: %d %s", code, body)
	}

	code, body = rpc(t, srv, "NotedHolidays", `[2026]`)
	if code != http.StatusOK || !strings.Contains(string(body), `"year":2026`) || !strings.Contains(string(body), "国庆节") {
		t.Errorf("NotedHolidays(2026): %d %s", code, body)
	}
	code, body = rpc(t, srv, "NotedHolidays", `[0]`)
	if code != http.StatusOK || !strings.Contains(string(body), `"year":-1`) {
		t.Errorf("NotedHolidays(0) should leave the year to noted: %d %s", code, body)
	}

	t.Setenv("NOTED_TOKEN", "wrong")
	app.actionsV.Close()
	if code, _ := rpc(t, srv, "NotedAlarms", `[]`); code != http.StatusBadGateway {
		t.Errorf("rejected token: %d, want 502", code)
	}
}

func testP8(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestRegisterPushToken(t *testing.T) {
	h, c, _ := pairingServer(t)
	claim := func(name string) (string, string) {
		code := startCode(t, h, c)
		rec := call(h, http.MethodPost, pairClaimPath, "", `{"code":"`+code+`","device":"`+name+`"}`)
		var got struct {
			Token    string `json:"token"`
			DeviceID string `json:"device_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Token == "" {
			t.Fatalf("claim: %d %s", rec.Code, rec.Body)
		}
		return got.Token, got.DeviceID
	}
	phone, phoneID := claim("iPhone")
	ipad, ipadID := claim("iPad")
	tokA, tokB := strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	targets := func() map[string]pushTarget {
		out := map[string]pushTarget{}
		for _, t := range c.devices.pushTargets() {
			out[t.DeviceID] = t
		}
		return out
	}

	// Only a paired device registers: not the shared token, not nobody.
	if rec := call(h, http.MethodPost, registerPushPath, c.Token, `["`+tokA+`","sandbox"]`); rec.Code != http.StatusForbidden {
		t.Errorf("shared token: %d", rec.Code)
	}
	if rec := call(h, http.MethodPost, registerPushPath, "", `["`+tokA+`","sandbox"]`); rec.Code != http.StatusUnauthorized {
		t.Errorf("no credential: %d", rec.Code)
	}
	// Bad arguments.
	for _, body := range []string{`["nothex!","sandbox"]`, `["` + tokA + `","staging"]`, `["` + tokA + `"]`, `{}`} {
		if rec := call(h, http.MethodPost, registerPushPath, phone, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}

	rec := call(h, http.MethodPost, registerPushPath, phone, `["`+strings.ToUpper(tokA)+`","sandbox"]`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), phoneID) {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if got := targets()[phoneID]; got.Token != tokA || got.Env != "sandbox" {
		t.Errorf("registered %+v", got)
	}
	// A new token replaces the old one.
	call(h, http.MethodPost, registerPushPath, phone, `["`+tokB+`","production"]`)
	if got := targets(); len(got) != 1 || got[phoneID].Token != tokB || got[phoneID].Env != "production" {
		t.Errorf("replaced: %+v", got)
	}
	// The same token from another pairing moves there: one phone, one push.
	call(h, http.MethodPost, registerPushPath, ipad, `["`+tokB+`",""]`)
	if got := targets(); len(got) != 1 || got[ipadID].Token != tokB {
		t.Errorf("moved: %+v", got)
	}
	// A dead token is dropped, but not one registered since.
	call(h, http.MethodPost, registerPushPath, phone, `["`+tokA+`","sandbox"]`)
	_ = c.devices.dropPushToken(ipadID, tokA) // stale: iPad holds tokB
	if len(targets()) != 2 {
		t.Errorf("a stale drop removed a live token: %+v", targets())
	}
	_ = c.devices.dropPushToken(ipadID, tokB)
	if got := targets(); len(got) != 1 || got[phoneID].Token != tokA {
		t.Errorf("after drop: %+v", got)
	}
	// An empty token unregisters; unpairing takes the token along.
	call(h, http.MethodPost, registerPushPath, phone, `["",""]`)
	if len(targets()) != 0 {
		t.Errorf("unregistered still has %+v", targets())
	}
	call(h, http.MethodPost, registerPushPath, phone, `["`+tokA+`","sandbox"]`)
	if _, err := c.devices.revoke(phoneID); err != nil {
		t.Fatal(err)
	}
	if len(targets()) != 0 {
		t.Errorf("unpaired device still pushed: %+v", targets())
	}

	// And it survives a restart.
	call(h, http.MethodPost, registerPushPath, ipad, `["`+tokA+`","production"]`)
	raw, _ := os.ReadFile(c.devices.path)
	if !strings.Contains(string(raw), tokA) {
		t.Errorf("devices.json lacks the token")
	}
}
