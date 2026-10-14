package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superai/internal/apns"
	"github.com/liliang-cn/superai/internal/backend"
)

// noAPNs makes sure nothing in the environment configures pushes.
func noAPNs(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APNS_KEY", "APNS_KEY_PATH", "APNS_KEY_ID"} {
		t.Setenv(k, "")
	}
}

type noticeEvents struct {
	mu  sync.Mutex
	got []map[string]any
}

func (e *noticeEvents) emit(name string, payload map[string]any) {
	if name != backend.NoticeEvent {
		return
	}
	e.mu.Lock()
	e.got = append(e.got, payload)
	e.mu.Unlock()
}

func (e *noticeEvents) list() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any(nil), e.got...)
}

// postServer is serve mode's gate over the real RPC dispatcher, in a fresh
// home with no agent built: a reminder must not need the model to be up.
func postServer(t *testing.T) (http.Handler, *credentials, *App, *noticeEvents) {
	t.Helper()
	t.Setenv("SUPERAI_HOME", t.TempDir())
	noAPNs(t)
	c := testCreds(t, "hunter2")
	var err error
	if c.devices, err = openDevices(devicesPath()); err != nil {
		t.Fatal(err)
	}
	if c.integrations, err = openIntegrations(integrationsPath()); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	ev := &noticeEvents{}
	a.emitFn = ev.emit
	mux := http.NewServeMux()
	authRoutes(mux, c)
	pairRoutes(mux, c)
	registerPushRoute(mux, c)
	mux.HandleFunc("/api/rpc/", func(w http.ResponseWriter, r *http.Request) { rpcCall(a, w, r) })
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("events")) })
	mux.HandleFunc(mcpPath, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("mcp")) })
	return requireAuth(c, mux), c, a, ev
}

func post(t *testing.T, h http.Handler, bearer string, req any) (int, PostedNotification, string) {
	t.Helper()
	b, _ := json.Marshal([]any{req})
	rec := call(h, http.MethodPost, postNotificationPath, bearer, string(b))
	var got PostedNotification
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	return rec.Code, got, rec.Body.String()
}

func TestPostNotificationValidates(t *testing.T) {
	h, c, _, _ := postServer(t)
	long := func(n int) string { return strings.Repeat("字", n) }
	for name, body := range map[string]string{
		"no title":         `[{"body":"x"}]`,
		"blank title":      `[{"title":"   "}]`,
		"long title":       `[{"title":"` + long(121) + `"}]`,
		"long body":        `[{"title":"t","body":"` + long(4001) + `"}]`,
		"unknown kind":     `[{"title":"t","kind":"urgent"}]`,
		"bad time":         `[{"title":"t","time":"tomorrow 8am"}]`,
		"not a link":       `[{"title":"t","deeplink":"just words"}]`,
		"javascript link":  `[{"title":"t","deeplink":"javascript:alert(1)"}]`,
		"four actions":     `[{"title":"t","actions":[{"label":"a","say":"a"},{"label":"b","say":"b"},{"label":"c","say":"c"},{"label":"d","say":"d"}]}]`,
		"action no say":    `[{"title":"t","actions":[{"label":"a"}]}]`,
		"not an object":    `["title"]`,
		"no argument":      `[]`,
		"two arguments":    `[{"title":"t"},{"title":"u"}]`,
		"long key":         `[{"title":"t","key":"` + strings.Repeat("k", 201) + `"}]`,
		"long action say":  `[{"title":"t","actions":[{"label":"a","say":"` + long(1001) + `"}]}]`,
		"long action name": `[{"title":"t","actions":[{"label":"` + long(41) + `","say":"a"}]}]`,
	} {
		rec := call(h, http.MethodPost, postNotificationPath, c.Token, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// The edges are allowed, and the defaults fill in.
	code, got, raw := post(t, h, c.Token, map[string]any{
		"title": long(120), "body": long(4000), "deeplink": "noted://alarm/a1",
		"actions": []map[string]string{{"label": "a", "say": "a"}, {"label": "b", "say": "b"}, {"label": "c", "say": "c"}},
	})
	if code != http.StatusOK || !got.OK || got.Duplicate || !strings.HasPrefix(got.ID, "ntf_") {
		t.Fatalf("valid post: %d %s", code, raw)
	}
	if !strings.Contains(raw, `"duplicate":false`) {
		t.Errorf("the answer must say duplicate:false outright: %s", raw)
	}
}

// What reaches the page and the centre is everything the phone will need when
// it reads the item back.
func TestAPostedNotificationReachesThePageAndTheCentre(t *testing.T) {
	h, c, a, ev := postServer(t)
	code, got, raw := post(t, h, c.Token, PostNotificationRequest{
		Title: "该出发接三宝了", Body: "**17:10** 出发，带水壶", Source: "openclaw", Kind: "reminder",
		Key: "pickup-2026-10-14", Deeplink: "noted://alarm/a1", Time: "2026-10-14T17:05:00+08:00",
		Actions: []backend.NoticeAction{{Label: "推迟 10 分钟", Say: "接三宝的提醒推迟 10 分钟"}},
	})
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	items := a.Notifications()
	if len(items) != 1 {
		t.Fatalf("centre has %+v", items)
	}
	n := items[0]
	want, _ := time.Parse(time.RFC3339, "2026-10-14T17:05:00+08:00")
	at, _ := time.Parse(time.RFC3339, n.At)
	if n.ID != got.ID || n.Title != "该出发接三宝了" || n.Message != "**17:10** 出发，带水壶" || n.Source != "openclaw" ||
		n.Kind != "reminder" || n.Deeplink != "noted://alarm/a1" || n.Read || !at.Equal(want) {
		t.Errorf("kept %+v", n)
	}
	if len(n.Actions) != 1 || n.Actions[0].Label != "推迟 10 分钟" || n.Actions[0].Say != "接三宝的提醒推迟 10 分钟" {
		t.Errorf("actions %+v", n.Actions)
	}
	if a.UnreadNotifications() != 1 {
		t.Errorf("unread = %d", a.UnreadNotifications())
	}
	events := ev.list()
	// The iOS app reads message (body as a fallback) and id: the body is
	// under message in both, and the event names the item it was filed as.
	if len(events) != 1 || events[0]["message"] != "**17:10** 出发，带水壶" {
		t.Errorf("notice event message %v", events)
	}
	if len(events) != 1 || events[0]["id"] != got.ID || events[0]["kind"] != "reminder" ||
		events[0]["deeplink"] != "noted://alarm/a1" || events[0]["title"] != "该出发接三宝了" || events[0]["key"] != "pickup-2026-10-14" {
		t.Errorf("notice events %v", events)
	}
	// From outside, so the webhook leaves it alone (its sender already posts
	// to the chat the webhook relays to); pushed, so the phones do not.
	if n, err := (PostNotificationRequest{Title: "t"}).notice(); err != nil || !n.External || !n.Push {
		t.Errorf("posted notice %+v %v", n, err)
	}
	// Marked read by the id it was posted under.
	a.MarkNotificationsRead([]string{got.ID})
	if a.UnreadNotifications() != 0 {
		t.Error("the posted id does not mark it read")
	}
}

func TestPostsAreDeliveredOncePerSourceAndKeyForADay(t *testing.T) {
	h, c, a, ev := postServer(t)
	memo := PostNotificationRequest{Title: "今日备忘", Body: "三件事", Source: "openclaw", Kind: "brief", Key: "memo-2026-10-14"}
	_, first, _ := post(t, h, c.Token, memo)
	_, again, raw := post(t, h, c.Token, memo)
	if !again.OK || !again.Duplicate || again.ID != first.ID {
		t.Errorf("retry: %s (first %s)", raw, first.ID)
	}
	// Another source's key is its own; no key is never a duplicate.
	other := memo
	other.Source = "cron"
	if _, got, _ := post(t, h, c.Token, other); got.Duplicate {
		t.Error("another source's key was taken as a duplicate")
	}
	nokey := memo
	nokey.Key = ""
	_, k1, _ := post(t, h, c.Token, nokey)
	_, k2, _ := post(t, h, c.Token, nokey)
	if k1.Duplicate || k2.Duplicate || k1.ID == k2.ID {
		t.Errorf("key-less posts: %+v %+v", k1, k2)
	}
	if n := len(a.Notifications()); n != 4 {
		t.Errorf("centre has %d, want 4 (the duplicate is not delivered)", n)
	}
	if n := len(ev.list()); n != 4 {
		t.Errorf("page heard %d, want 4", n)
	}

	// A restart forgets nothing: the window is on disk.
	restarted := postedKeysAt(postedKeysPath())
	if id, dup, err := restarted.claim("openclaw", "memo-2026-10-14", "ntf_new"); err != nil || !dup || id != first.ID {
		t.Errorf("after restart: %q %v %v", id, dup, err)
	}
	// And a day later the same key is news again.
	restarted.now = func() time.Time { return time.Now().Add(postDedupeWindow + time.Minute) }
	if id, dup, err := restarted.claim("openclaw", "memo-2026-10-14", "ntf_tomorrow"); err != nil || dup || id != "ntf_tomorrow" {
		t.Errorf("a day later: %q %v %v", id, dup, err)
	}
	raw2, _ := os.ReadFile(postedKeysPath())
	if strings.Contains(string(raw2), first.ID) {
		t.Error("expired keys are not pruned")
	}
}

func TestAnIntegrationTokenOnlyPostsNotifications(t *testing.T) {
	h, c, a, _ := postServer(t)
	minted, err := a.CreateIntegrationToken("openclaw")
	if err != nil {
		t.Fatal(err)
	}
	tok := minted.Token
	if !strings.HasPrefix(tok, integrationTokenPrefix) || minted.ID == "" || minted.Name != "openclaw" {
		t.Fatalf("minted %+v", minted)
	}
	if _, err := a.CreateIntegrationToken("  "); err == nil {
		t.Error("a nameless token was minted")
	}

	if code, got, raw := post(t, h, tok, map[string]string{"title": "起立喝水", "source": "openclaw"}); code != http.StatusOK || !got.OK {
		t.Fatalf("integration post: %d %s", code, raw)
	}
	for _, p := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/rpc/Notifications", `[]`},
		{http.MethodPost, "/api/rpc/CreateIntegrationToken", `["more"]`},
		{http.MethodPost, "/api/rpc/SendChat", `[]`},
		{http.MethodPost, "/api/rpc/PostNotification/", `[]`},
		{http.MethodGet, "/api/events", ``},
		{http.MethodPost, mcpPath, `{}`},
	} {
		if rec := call(h, p.method, p.path, tok, p.body); rec.Code != http.StatusForbidden {
			t.Errorf("integration token on %s: %d", p.path, rec.Code)
		}
	}
	// Nor is it any good on the agent link.
	if credentialsCheck(c)(tok) {
		t.Error("an integration token connects an agent")
	}

	// The master token and a paired phone may post too.
	if code, _, raw := post(t, h, c.Token, map[string]string{"title": "master"}); code != http.StatusOK {
		t.Errorf("master: %d %s", code, raw)
	}
	code, _ := c.devices.newCode()
	_, phone, ok, err := c.devices.claim(code, "iPhone")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	if code, _, raw := post(t, h, phone, map[string]string{"title": "phone"}); code != http.StatusOK {
		t.Errorf("phone: %d %s", code, raw)
	}

	// Listed without the secret, with when it was last used.
	list, err := a.ListIntegrationTokens()
	if err != nil || len(list) != 1 || list[0].ID != minted.ID || list[0].LastUsed.IsZero() {
		t.Fatalf("list %+v %v", list, err)
	}
	b, _ := json.Marshal(list)
	raw, _ := os.ReadFile(integrationsPath())
	if strings.Contains(string(b), tok) || strings.Contains(string(raw), tok) {
		t.Error("the secret is stored or listed")
	}

	// Revoked, it is a stranger.
	if err := a.RevokeIntegrationToken(minted.ID); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := post(t, h, tok, map[string]string{"title": "after"}); code != http.StatusUnauthorized {
		t.Errorf("revoked token: %d", code)
	}
	if err := a.RevokeIntegrationToken(minted.ID); err == nil {
		t.Error("revoking twice succeeded")
	}
}

// The CLI writes the file beside a running server; the server must honour a
// token minted that way, and stop honouring one revoked that way, without a
// restart.
func TestTokensMintedByAnotherProcessWorkAtOnce(t *testing.T) {
	h, _, _, _ := postServer(t)
	other := &integrationStore{path: integrationsPath(), now: time.Now} // the CLI's own copy
	minted, tok, err := other.create("openclaw")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, raw := post(t, h, tok, map[string]string{"title": "hi"}); code != http.StatusOK {
		t.Fatalf("token minted elsewhere: %d %s", code, raw)
	}
	time.Sleep(10 * time.Millisecond) // a different mtime even on a coarse clock
	if ok, err := other.revoke(minted.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if code, _, _ := post(t, h, tok, map[string]string{"title": "hi"}); code != http.StatusUnauthorized {
		t.Errorf("token revoked elsewhere: %d", code)
	}
}

type sentAlert struct {
	token, env string
	alert      apns.Alert
}

// fakeAlerts records visible pushes; tokens in dead answer as Apple does for
// an uninstalled app.
type fakeAlerts struct {
	mu   sync.Mutex
	got  []sentAlert
	dead map[string]bool
}

func (p *fakeAlerts) Alert(_ context.Context, token, env string, a apns.Alert) error {
	p.mu.Lock()
	p.got = append(p.got, sentAlert{token, env, a})
	p.mu.Unlock()
	if p.dead[token] {
		return fmt.Errorf("%w (410 Unregistered)", apns.ErrDeviceGone)
	}
	return nil
}

func (p *fakeAlerts) sent() []sentAlert {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sentAlert(nil), p.got...)
}

// fakePush is the apns subscriber over a fake Apple, and a way to wait for it.
func fakePush(store *deviceStore, p noticePusher) (backend.Subscriber, chan struct{}) {
	done := make(chan struct{}, 8)
	np := noticePush{
		pusher:  func() noticePusher { return p },
		devices: func() (*deviceStore, error) { return store, nil },
		done:    func() { done <- struct{}{} },
	}
	return np.sink(), done
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the push never finished")
	}
}

func TestPushedNoticesReachEveryPhone(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	store := pairedWithPush(t)
	dead := strings.Repeat("bb", 32)
	p := &fakeAlerts{dead: map[string]bool{dead: true}}
	sink, done := fakePush(store, p)

	// Not pushed: nothing leaves the machine.
	sink(context.Background(), backend.Notice{Message: "settings saved"})
	select {
	case <-done:
		t.Fatal("an unpushed notice was sent")
	case <-time.After(50 * time.Millisecond):
	}

	sink(context.Background(), backend.Notice{
		ID: "ntf_1", Title: "该出发接三宝了", Message: "**17:10** 出发", Source: "openclaw",
		Kind: backend.KindReminder, Key: "pickup", Deeplink: "noted://alarm/a1", Push: true,
	})
	waitDone(t, done)
	got := p.sent()
	if len(got) != 2 {
		t.Fatalf("sent %+v", got)
	}
	envs := map[string]string{}
	for _, s := range got {
		envs[s.token] = s.env
		a := s.alert
		if a.Title != "该出发接三宝了" || a.Body != "17:10 出发" || !a.Sound || a.Priority != 10 ||
			a.InterruptionLevel != "time-sensitive" || a.ThreadID != "reminder" || a.CollapseID != "pickup" {
			t.Errorf("alert %+v", a)
		}
		d, _ := a.Data.(map[string]any)
		if d["kind"] != "notice" || d["id"] != "ntf_1" || d["deeplink"] != "noted://alarm/a1" || d["source"] != "openclaw" {
			t.Errorf("data %v", d)
		}
	}
	if envs[strings.Repeat("aa", 32)] != "sandbox" || envs[dead] != "" {
		t.Errorf("envs %v", envs)
	}
	// The dead token is forgotten; the live one stays.
	if ts := store.pushTargets(); len(ts) != 1 || ts[0].Token != strings.Repeat("aa", 32) {
		t.Errorf("targets after a dead token: %+v", ts)
	}
}

func TestAlertsByKind(t *testing.T) {
	for _, c := range []struct {
		kind     string
		prio     int
		sound    bool
		level    string
		threadID string
	}{
		{backend.KindReminder, 10, true, "time-sensitive", "reminder"},
		{backend.KindAlert, 10, true, "time-sensitive", "alert"},
		{backend.KindBrief, 10, false, "active", "brief"},
		{backend.KindInfo, 5, false, "active", "info"},
		{"", 5, false, "active", "info"},
	} {
		a, ok := alertFor(backend.Notice{Message: "x", Kind: c.kind, Push: true})
		if !ok || a.Priority != c.prio || a.Sound != c.sound || a.InterruptionLevel != c.level || a.ThreadID != c.threadID {
			t.Errorf("%q: %+v", c.kind, a)
		}
		d, _ := a.Data.(map[string]any)
		if _, has := d["deeplink"]; has {
			t.Errorf("%q: a deeplink nobody gave: %v", c.kind, d)
		}
	}
	if _, ok := alertFor(backend.Notice{Push: true}); ok {
		t.Error("an empty notice makes an alert")
	}
	// A notice nobody posted is named the way the centre names it.
	a, _ := alertFor(backend.Notice{Message: "m", Key: "run:1", Push: true})
	if d, _ := a.Data.(map[string]any); d["id"] != "run:1" {
		t.Errorf("id %v", d["id"])
	}
}

func TestPlainTextStripsMarkdownAndCuts(t *testing.T) {
	for in, want := range map[string]string{
		"# 今日\n\n- **三件事**\n- 看 [文档](https://x.y) 和 `code`\n> 引用": "今日 三件事 看 文档 和 code 引用",
		"```go\nfmt.Println(1)\n```\n![图](a.png) ~~删~~ __粗__ *斜*": "fmt.Println(1) 图 删 粗 斜",
		"| a | b |\n|---|---|\n| 1 | 2 |":                         "a b 1 2",
		"1. one\n2) two\n- [x] done\n---":                         "one two done",
		"   ":                                                     "",
	} {
		if got := plainText(in, alertBodyChars); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
	cut := plainText(strings.Repeat("长", 500), alertBodyChars)
	if r := []rune(cut); len(r) != alertBodyChars || !strings.HasSuffix(cut, "…") {
		t.Errorf("cut to %d runes: %q", len(r), cut)
	}
}

// With APNs not configured — the default — a post still reaches the page and
// the centre, and the phones are skipped without a fuss.
func TestWithoutAPNsEverythingElseStillWorks(t *testing.T) {
	h, c, a, ev := postServer(t)
	pairedWithPush(t)
	if a.alarmPusher() != nil {
		t.Fatal("APNs configured in a test home")
	}
	if code, got, raw := post(t, h, c.Token, map[string]string{"title": "价格提醒", "kind": "alert", "body": "NVDA < 160"}); code != http.StatusOK || !got.OK {
		t.Fatalf("%d %s", code, raw)
	}
	if len(a.Notifications()) != 1 || len(ev.list()) != 1 {
		t.Errorf("centre %d, page %d", len(a.Notifications()), len(ev.list()))
	}
	// The real subscriber, run to the end on this goroutine: no client, no
	// panic, and the phones keep their tokens.
	np := noticePush{
		pusher: func() noticePusher {
			p, _ := a.alarmPusher().(noticePusher)
			return p
		},
		devices: func() (*deviceStore, error) { return openDevices(devicesPath()) },
	}
	np.send(context.Background(), backend.Notice{Title: "t", Message: "m", Push: true})
	if s, _ := openDevices(devicesPath()); len(s.pushTargets()) != 2 {
		t.Error("tokens dropped without a push being sent")
	}
}

// notify_user, in the real service the app builds, reaches the phones through
// the same subscriber: the app subscribes it on every rebuild, and the tool
// marks its notice for pushing.
func TestNotifyUserReachesThePhones(t *testing.T) {
	noAPNs(t)
	a, _ := gateTestApp(t, "true", false)
	subs := strings.Join(a.svc.Notices().Subscribers(), ",")
	if !strings.Contains(subs, "apns") {
		t.Fatalf("subscribers %s", subs)
	}
	store := pairedWithPush(t)
	p := &fakeAlerts{}
	sink, done := fakePush(store, p)
	// Same name, so this replaces the real one rather than adding beside it.
	a.svc.Notices().Subscribe("apns", sink)

	if _, err := a.svc.Agent().GetToolRegistry().Call(context.Background(), "notify_user", map[string]any{"message": "第一段跑完了，**3/8**"}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	got := p.sent()
	if len(got) != 2 {
		t.Fatalf("sent %+v", got)
	}
	if a := got[0].alert; a.Body != "第一段跑完了，3/8" || a.Title != "" || a.Priority != 5 || a.Sound {
		t.Errorf("alert %+v", a)
	}
	// And it is in the centre, which is where tapping it leads.
	if items := a.Notifications(); len(items) != 1 || items[0].Message != "第一段跑完了，**3/8**" {
		t.Errorf("centre %+v", items)
	}
}

// The app icon's number goes with every push, counted with the new notice
// filed, so it is right while the app is closed.
func TestPushesCarryTheUnreadBadge(t *testing.T) {
	t.Setenv("SUPERAI_HOME", t.TempDir())
	store := pairedWithPush(t)
	box := backend.OpenInbox()
	p := &fakeAlerts{}
	done := make(chan struct{}, 8)
	pub := backend.NewNotices(nil)
	pub.Subscribe("inbox", box.Sink())
	pub.Subscribe("apns", noticePush{
		pusher:  func() noticePusher { return p },
		devices: func() (*deviceStore, error) { return store, nil },
		unread:  box.UnreadIncluding,
		done:    func() { done <- struct{}{} },
	}.sink())

	pub.Raise(context.Background(), backend.Notice{ID: "ntf_a", Title: "a", Push: true})
	waitDone(t, done)
	pub.Raise(context.Background(), backend.Notice{ID: "ntf_b", Title: "b", Push: true})
	waitDone(t, done)
	_ = box.MarkRead("ntf_a", "ntf_b")
	pub.Raise(context.Background(), backend.Notice{Message: "notify_user 的话", Push: true})
	waitDone(t, done)

	var badges []int
	for _, s := range p.sent() {
		if s.token != strings.Repeat("aa", 32) {
			continue
		}
		if s.alert.Badge == nil {
			t.Fatalf("no badge on %+v", s.alert)
		}
		badges = append(badges, *s.alert.Badge)
	}
	if fmt.Sprint(badges) != "[1 2 1]" {
		t.Errorf("badges %v, want [1 2 1]", badges)
	}
}
