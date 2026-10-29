package backend

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture is a receiver that records every request and answers with body.
type capture struct {
	mu     sync.Mutex
	reqs   []*http.Request
	bodies [][]byte
	status int
	answer string
}

func newCapture(t *testing.T, status int, answer string) (*capture, *httptest.Server) {
	c := &capture{status: status, answer: answer}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs = append(c.reqs, r)
		c.bodies = append(c.bodies, b)
		c.mu.Unlock()
		w.WriteHeader(c.status)
		_, _ = io.WriteString(w, c.answer)
	}))
	t.Cleanup(srv.Close)
	return c, srv
}

func (c *capture) one(t *testing.T) (*http.Request, map[string]any) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(c.reqs))
	}
	var m map[string]any
	if err := json.Unmarshal(c.bodies[0], &m); err != nil {
		t.Fatalf("body is not JSON: %s", c.bodies[0])
	}
	return c.reqs[0], m
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

var samplePayload = WebhookPayload{Event: WebhookEventSchedule, Kind: ChannelEventSchedule, Title: "早报", Message: "今天晴，**25°C**。", SentAt: "2026-10-29T08:00:00+08:00"}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestFeishuPayloadAndSignature(t *testing.T) {
	cap, srv := newCapture(t, 200, `{"code":0,"msg":"success","data":{}}`)
	now := time.Unix(1599360473, 0)
	err := sendFeishu(context.Background(), srv.Client(), Channel{Type: ChannelFeishu, URL: srv.URL + "/open-apis/bot/v2/hook/x", Secret: "demo"}, samplePayload, now)
	if err != nil {
		t.Fatal(err)
	}
	r, body := cap.one(t)
	if r.URL.Path != "/open-apis/bot/v2/hook/x" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Errorf("request %s %s", r.URL.Path, r.Header.Get("Content-Type"))
	}
	if body["msg_type"] != "text" || asJSON(body["content"]) != `{"text":"早报\n今天晴，**25°C**。"}` {
		t.Errorf("body = %v", body)
	}
	// The documented algorithm: HMAC-SHA256 keyed by "timestamp\nsecret" over
	// an empty message, base64.
	mac := hmac.New(sha256.New, []byte("1599360473\ndemo"))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if body["timestamp"] != "1599360473" || body["sign"] != want {
		t.Errorf("timestamp %v sign %v, want %s", body["timestamp"], body["sign"], want)
	}

	// No secret, no signature fields.
	cap2, srv2 := newCapture(t, 200, `{"code":0}`)
	_ = sendFeishu(context.Background(), srv2.Client(), Channel{URL: srv2.URL}, samplePayload, now)
	if _, b := cap2.one(t); b["sign"] != nil || b["timestamp"] != nil {
		t.Errorf("unsigned body has signature: %v", b)
	}

	// Feishu answers 200 with a code on failure.
	_, bad := newCapture(t, 200, `{"code":19021,"msg":"sign match fail or timestamp is not within one hour from current time"}`)
	if err := sendFeishu(context.Background(), bad.Client(), Channel{URL: bad.URL}, samplePayload, now); err == nil || !strings.Contains(err.Error(), "19021") {
		t.Errorf("err = %v", err)
	}
}

func TestWeComPayload(t *testing.T) {
	cap, srv := newCapture(t, 200, `{"errcode":0,"errmsg":"ok"}`)
	long := strings.Repeat("长", 1000) // 3000 bytes
	err := sendWeCom(context.Background(), srv.Client(), Channel{URL: srv.URL + "/cgi-bin/webhook/send?key=K"}, WebhookPayload{Title: "SuperLeo", Message: long})
	if err != nil {
		t.Fatal(err)
	}
	r, body := cap.one(t)
	if r.URL.Query().Get("key") != "K" || body["msgtype"] != "text" {
		t.Errorf("request %s body %v", r.URL, body)
	}
	content := body["text"].(map[string]any)["content"].(string)
	if len(content) > 2048 || !strings.HasSuffix(content, "…") {
		t.Errorf("content is %d bytes; WeCom takes 2048", len(content))
	}
	if weComURL("abc") != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc" {
		t.Errorf("bare key URL = %s", weComURL("abc"))
	}
	_, bad := newCapture(t, 200, `{"errcode":93000,"errmsg":"invalid webhook url"}`)
	if err := sendWeCom(context.Background(), bad.Client(), Channel{URL: bad.URL}, samplePayload); err == nil || !strings.Contains(err.Error(), "93000") {
		t.Errorf("err = %v", err)
	}
}

func TestDingTalkPayloadAndSignature(t *testing.T) {
	cap, srv := newCapture(t, 200, `{"errcode":0,"errmsg":"ok"}`)
	now := time.UnixMilli(1577262236757)
	secret := "SECabc"
	err := sendDingTalk(context.Background(), srv.Client(), Channel{URL: srv.URL + "/robot/send?access_token=T", Secret: secret}, samplePayload, now)
	if err != nil {
		t.Fatal(err)
	}
	r, body := cap.one(t)
	q := r.URL.Query()
	// The documented algorithm: HMAC-SHA256 keyed by the secret over
	// "timestamp\nsecret", base64, URL-encoded into the query.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("1577262236757\n" + secret))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if q.Get("access_token") != "T" || q.Get("timestamp") != "1577262236757" || q.Get("sign") != want {
		t.Errorf("query = %v, want sign %s", q, want)
	}
	if !strings.Contains(r.URL.RawQuery, "sign="+url.QueryEscape(want)) {
		t.Errorf("sign is not URL-encoded in %s", r.URL.RawQuery)
	}
	md := body["markdown"].(map[string]any)
	if body["msgtype"] != "markdown" || md["title"] != "早报" || md["text"] != "#### 早报\n\n今天晴，**25°C**。" {
		t.Errorf("body = %v", body)
	}
	if u, _ := dingTalkURL("tok", "", now); u != "https://oapi.dingtalk.com/robot/send?access_token=tok" {
		t.Errorf("bare token URL = %s", u)
	}
	_, bad := newCapture(t, 200, `{"errcode":310000,"errmsg":"sign not match"}`)
	if err := sendDingTalk(context.Background(), bad.Client(), Channel{URL: bad.URL, Secret: "x"}, samplePayload, now); err == nil || !strings.Contains(err.Error(), "sign not match") {
		t.Errorf("err = %v", err)
	}
}

func TestSlackPayload(t *testing.T) {
	cap, srv := newCapture(t, 200, "ok")
	if err := sendSlack(context.Background(), srv.Client(), Channel{URL: srv.URL}, samplePayload); err != nil {
		t.Fatal(err)
	}
	if _, body := cap.one(t); asJSON(body) != `{"text":"*早报*\n今天晴，**25°C**。"}` {
		t.Errorf("body = %v", asJSON(body))
	}
	_, bad := newCapture(t, 400, "invalid_payload")
	if err := sendSlack(context.Background(), bad.Client(), Channel{URL: bad.URL}, samplePayload); err == nil || !strings.Contains(err.Error(), "invalid_payload") {
		t.Errorf("err = %v", err)
	}
}

func TestBarkPayload(t *testing.T) {
	cap, srv := newCapture(t, 200, `{"code":200,"message":"success","timestamp":1}`)
	p := WebhookPayload{Event: WebhookEventNeedsYou, Kind: ChannelEventNeedsYou, Title: "SuperLeo needs your OK", Message: "rm -rf build"}
	if err := sendBark(context.Background(), srv.Client(), Channel{Server: srv.URL, DeviceKey: "DK"}, p); err != nil {
		t.Fatal(err)
	}
	r, body := cap.one(t)
	if r.URL.Path != "/push" || body["device_key"] != "DK" || body["title"] != p.Title || body["body"] != p.Message || body["group"] != "SuperLeo" || body["level"] != "timeSensitive" {
		t.Errorf("request %s body %v", r.URL.Path, body)
	}
	_, bad := newCapture(t, 200, `{"code":400,"message":"failed to get device token"}`)
	if err := sendBark(context.Background(), bad.Client(), Channel{Server: bad.URL, DeviceKey: "x"}, p); err == nil {
		t.Error("bark failure not reported")
	}
}

func TestTelegramChannelSendsToEveryChat(t *testing.T) {
	var mu sync.Mutex
	var got []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOK/sendMessage" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		mu.Lock()
		got = append(got, r.PostForm)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	old := telegramAPIBase
	telegramAPIBase = srv.URL
	defer func() { telegramAPIBase = old }()

	c := Channel{Type: ChannelTelegram, BotToken: "TOK", AllowedChats: []int64{1, 2}}
	if err := sendTelegramChannel(context.Background(), srv.Client(), c, samplePayload); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Get("text") != "早报\n今天晴，**25°C**。" {
		t.Fatalf("sent %v", got)
	}
	c.BotToken = "WRONG"
	err := sendTelegramChannel(context.Background(), srv.Client(), c, samplePayload)
	if err == nil || strings.Contains(err.Error(), "WRONG") {
		t.Errorf("err = %v (must fail without repeating the token)", err)
	}
}

func TestGenericWebhookKeepsItsContract(t *testing.T) {
	cap, srv := newCapture(t, 204, "")
	if err := sendWebhook(context.Background(), srv.Client(), Channel{URL: srv.URL, Secret: "s"}, samplePayload); err != nil {
		t.Fatal(err)
	}
	r, body := cap.one(t)
	if r.Header.Get("Authorization") != "Bearer s" || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", r.Header)
	}
	if body["event"] != "schedule" || body["kind"] != "schedule" || body["title"] != "早报" || body["sent_at"] != samplePayload.SentAt {
		t.Errorf("body %v", body)
	}
}

func TestDispatcherRoutesByEventAndRecordsStatus(t *testing.T) {
	all, allSrv := newCapture(t, 200, "ok")
	onlyFailed, failedSrv := newCapture(t, 200, "ok")
	_, brokenSrv := newCapture(t, 500, "boom")
	n := NewNotifier(&Settings{Channels: []Channel{
		{ID: "all", Type: ChannelSlack, Enabled: true, Events: ChannelEvents, URL: allSrv.URL},
		{ID: "failed", Type: ChannelSlack, Enabled: true, Events: []string{ChannelEventFailed}, URL: failedSrv.URL},
		{ID: "off", Type: ChannelSlack, Enabled: false, Events: ChannelEvents, URL: failedSrv.URL},
		{ID: "broken", Type: ChannelSlack, Enabled: true, Events: ChannelEvents, URL: brokenSrv.URL},
	}})
	n.Send(context.Background(), WebhookPayload{Event: WebhookEventNotify, Message: "fyi"})
	if all.count() != 1 || onlyFailed.count() != 0 {
		t.Fatalf("notify reached all=%d failed=%d", all.count(), onlyFailed.count())
	}
	n.Send(context.Background(), WebhookPayload{Event: WebhookEventSchedule, Message: "Run failed: x", Error: "x"})
	if all.count() != 2 || onlyFailed.count() != 1 {
		t.Fatalf("failure reached all=%d failed=%d", all.count(), onlyFailed.count())
	}
	if st := ChannelStatusOf("broken"); st.LastError == "" || st.LastErrorAt == "" {
		t.Errorf("broken status = %+v", st)
	}
	if st := ChannelStatusOf("all"); st.LastOKAt == "" || st.LastError != "" {
		t.Errorf("all status = %+v", st)
	}
}

func TestNoticeEventsMapToChannelEvents(t *testing.T) {
	for _, c := range []struct {
		n    Notice
		want string
	}{
		{Notice{Message: "mid-task"}, ChannelEventReminder},
		{Notice{Source: "daily brief", Level: LevelSuccess}, ChannelEventSchedule},
		{Notice{Source: "daily brief", Level: LevelError}, ChannelEventFailed},
		{Notice{Source: ReminderPrompt("倒垃圾"), Event: ChannelEventReminder}, ChannelEventReminder},
		{Notice{Kind: KindReminder}, ChannelEventReminder},
	} {
		if got := channelEventFor(c.n); got != c.want {
			t.Errorf("%+v → %s, want %s", c.n, got, c.want)
		}
	}
	if !IsReminderPrompt(ReminderPrompt("倒垃圾")) || IsReminderPrompt("summarise the news") {
		t.Error("IsReminderPrompt")
	}
}
