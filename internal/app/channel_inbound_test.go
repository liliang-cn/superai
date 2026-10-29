package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// inboundRig is an app with one webhook channel, served behind the real gate.
type inboundRig struct {
	app   *App
	srv   *httptest.Server
	ch    backend.Channel
	turns chan [2]string // session, text
}

func newInboundRig(t *testing.T, mode string, outURL string, answer func(text string) (string, error)) *inboundRig {
	t.Helper()
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	ch := backend.Channel{
		ID: "hk1", Type: backend.ChannelWebhook, Name: "hook", Enabled: true,
		Events: []string{}, InboundToken: "the-right-token", ReplyMode: mode, URL: outURL, Secret: "out-secret",
	}
	a := &App{settings: &backend.Settings{Channels: []backend.Channel{ch}}, emitFn: func(string, map[string]any) {}}
	rig := &inboundRig{app: a, ch: ch, turns: make(chan [2]string, 16)}

	oldTurn, oldWait, oldState := channelTurn, channelSyncWait, channelInbound
	channelTurn = func(_ *App, _ context.Context, session, text string) (string, error) {
		select {
		case rig.turns <- [2]string{session, text}:
		default:
		}
		return answer(text)
	}
	channelInbound = &inboundState{recent: map[string][]time.Time{}, running: map[string]int{}, results: map[string]*inboundResult{}, throttle: newLoginThrottle()}
	t.Cleanup(func() { channelTurn, channelSyncWait, channelInbound = oldTurn, oldWait, oldState })

	mux := http.NewServeMux()
	channelInboundRoute(mux, a)
	mux.HandleFunc("/api/rpc/", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "rpc reached") })
	rig.srv = httptest.NewServer(requireAuth(testCreds(t, "pw"), mux))
	t.Cleanup(rig.srv.Close)
	return rig
}

func (r *inboundRig) post(t *testing.T, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return do(t, req)
}

func do(t *testing.T, req *http.Request) (int, map[string]any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{"raw": string(raw)}
	}
	return resp.StatusCode, m
}

func TestInboundSyncAnswersInTheResponse(t *testing.T) {
	rig := newInboundRig(t, backend.ReplySync, "", func(text string) (string, error) { return "pong: " + text + "\nMOOD: happy", nil })
	code, body := rig.post(t, "/in/hook/hk1", "the-right-token", `{"text":"ping"}`)
	if code != 200 || body["answer"] != "pong: ping" || body["status"] != "done" || body["conversation"] != "hook:hk1" {
		t.Fatalf("%d %v", code, body)
	}
	if !strings.HasPrefix(body["request_id"].(string), "req_") {
		t.Errorf("request_id = %v", body["request_id"])
	}
	if got := <-rig.turns; got != [2]string{"hook:hk1", "ping"} {
		t.Errorf("turn = %v", got)
	}
	// A conversation of the caller's, and plain text with the other header.
	req, _ := http.NewRequest(http.MethodPost, rig.srv.URL+"/in/hook/hk1", strings.NewReader(`{"message":"again","conversation_id":"my-thread"}`))
	req.Header.Set("X-Hook-Token", "the-right-token")
	if code, body := do(t, req); code != 200 || body["conversation"] != "my-thread" {
		t.Fatalf("%d %v", code, body)
	}
	if got := <-rig.turns; got[0] != "my-thread" {
		t.Errorf("session = %q", got[0])
	}
	if st := backend.ChannelStatusOf("hk1"); st.InboundToday < 2 {
		t.Errorf("inbound count = %d", st.InboundToday)
	}
}

func TestInboundSyncThatRunsLongHandsBackARequestID(t *testing.T) {
	release := make(chan struct{})
	rig := newInboundRig(t, backend.ReplySync, "", func(string) (string, error) { <-release; return "late answer", nil })
	channelSyncWait = 50 * time.Millisecond
	code, body := rig.post(t, "/in/hook/hk1", "the-right-token", `{"text":"slow"}`)
	if code != http.StatusAccepted || body["status"] != "running" {
		t.Fatalf("%d %v", code, body)
	}
	result := body["result"].(string)
	if result != "/in/hook/hk1/result/"+body["request_id"].(string) {
		t.Errorf("result path = %s", result)
	}
	get := func(token string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodGet, rig.srv.URL+result, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		return do(t, req)
	}
	if code, b := get("the-right-token"); code != 200 || b["status"] != "running" {
		t.Fatalf("still running: %d %v", code, b)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		code, b := get("the-right-token")
		if b["status"] == "done" {
			if code != 200 || b["answer"] != "late answer" {
				t.Fatalf("%d %v", code, b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the answer never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ := get("wrong"); code != http.StatusUnauthorized {
		t.Errorf("result with a wrong token: %d", code)
	}
}

func TestInboundCallbackPostsTheAnswerToTheSendURL(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	var auth string
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got, auth = append(got, m), r.Header.Get("Authorization")
		mu.Unlock()
	}))
	defer recv.Close()
	rig := newInboundRig(t, backend.ReplyCallback, recv.URL, func(text string) (string, error) { return "done: " + text, nil })
	code, body := rig.post(t, "/in/hook/hk1", "the-right-token", `{"text":"work","conversation":"c1"}`)
	if code != http.StatusAccepted || body["status"] != "accepted" || body["answer"] != nil {
		t.Fatalf("%d %v", code, body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no callback")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cb := got[0]
	if cb["event"] != "reply" || cb["message"] != "done: work" || cb["request_id"] != body["request_id"] || cb["conversation"] != "c1" || auth != "Bearer out-secret" {
		t.Errorf("callback = %v auth %q", cb, auth)
	}
}

func TestInboundRefusesWrongTokensAndBadBodies(t *testing.T) {
	rig := newInboundRig(t, backend.ReplySync, "", func(string) (string, error) { return "x", nil })
	for name, c := range map[string]struct {
		path, token, body string
		want              int
	}{
		"no token":        {"/in/hook/hk1", "", `{"text":"hi"}`, 401},
		"wrong token":     {"/in/hook/hk1", "the-wrong-token", `{"text":"hi"}`, 401},
		"unknown channel": {"/in/hook/nope", "the-right-token", `{"text":"hi"}`, 401},
		"program token":   {"/in/hook/hk1", "token-for-programs", `{"text":"hi"}`, 401},
		"empty text":      {"/in/hook/hk1", "the-right-token", `{"text":"  "}`, 400},
		"not json":        {"/in/hook/hk1", "the-right-token", `{"text":`, 400},
		"oversized":       {"/in/hook/hk1", "the-right-token", `{"text":"` + strings.Repeat("a", channelInboundMax) + `"}`, 413},
	} {
		if code, body := rig.post(t, c.path, c.token, c.body); code != c.want {
			t.Errorf("%s: %d %v, want %d", name, code, body, c.want)
		}
	}
	select {
	case turn := <-rig.turns:
		t.Errorf("a refused request ran a turn: %v", turn)
	default:
	}
	// The channel token opens its own door and no other.
	req, _ := http.NewRequest(http.MethodPost, rig.srv.URL+"/api/rpc/GetSettings", strings.NewReader(`[]`))
	req.Header.Set("Authorization", "Bearer the-right-token")
	if code, _ := do(t, req); code != http.StatusUnauthorized {
		t.Errorf("channel token reached /api/rpc: %d", code)
	}
	// A disabled channel does not answer.
	rig.app.settings.Channels[0].Enabled = false
	if code, _ := rig.post(t, "/in/hook/hk1", "the-right-token", `{"text":"hi"}`); code != 401 {
		t.Errorf("disabled channel: %d", code)
	}
}

func TestInboundThrottlesGuessesAndFloods(t *testing.T) {
	rig := newInboundRig(t, backend.ReplySync, "", func(string) (string, error) { return "x", nil })
	for range throttleAfter {
		rig.post(t, "/in/hook/hk1", "guess", `{"text":"hi"}`)
	}
	if code, _ := rig.post(t, "/in/hook/hk1", "the-right-token", `{"text":"hi"}`); code != http.StatusTooManyRequests {
		t.Errorf("after %d wrong tokens the right one got %d, want 429", throttleAfter, code)
	}

	rig2 := newInboundRig(t, backend.ReplySync, "", func(string) (string, error) { return "x", nil })
	ok := 0
	for range channelRatePerMinute + 5 {
		if code, _ := rig2.post(t, "/in/hook/hk1", "the-right-token", `{"text":"hi"}`); code == 200 {
			ok++
		}
	}
	if ok != channelRatePerMinute {
		t.Errorf("%d requests went through in a minute, want %d", ok, channelRatePerMinute)
	}
}

func TestInboundTurnFailureIsReported(t *testing.T) {
	rig := newInboundRig(t, backend.ReplySync, "", func(string) (string, error) { return "", errors.New("model is down") })
	code, body := rig.post(t, "/in/hook/hk1", "the-right-token", `{"text":"hi"}`)
	if code != http.StatusBadGateway || body["error"] != "model is down" || body["status"] != "failed" {
		t.Fatalf("%d %v", code, body)
	}
}

func TestChannelRPCs(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := &App{settings: &backend.Settings{}, emitFn: func(string, map[string]any) {}}
	str := func(s string) *string { return &s }
	v, err := a.SaveChannel(backend.ChannelInput{Type: "webhook", Name: str("relay"), URL: str("https://hook.example.test/in"), Secret: str("outsecret123")})
	if err != nil {
		t.Fatal(err)
	}
	if v.InboundPath != "/in/hook/"+v.ID || v.TokenMask == "" || v.SecretMask != "••••t123" || !v.TwoWay {
		t.Errorf("view = %+v", v)
	}
	list := a.ListChannels()
	raw, _ := json.Marshal(list)
	tok, _ := a.RevealChannelToken(v.ID)
	if tok == "" || strings.Contains(string(raw), tok) || strings.Contains(string(raw), "outsecret123") {
		t.Errorf("the list leaks a secret: %s", raw)
	}
	// The legacy field follows the first webhook.
	if a.GetSettings().WebhookURL != "https://hook.example.test/in" {
		t.Errorf("webhook_url = %q", a.GetSettings().WebhookURL)
	}
	v2, err := a.RegenerateChannelToken(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tok2, _ := a.RevealChannelToken(v.ID); tok2 == tok || v2.TokenMask == v.TokenMask {
		t.Error("token did not change")
	}
	if _, err := a.SaveChannel(backend.ChannelInput{ID: v.ID, Enabled: new(bool)}); err != nil {
		t.Fatal(err)
	}
	if c, _ := a.channelByID(v.ID); c.Enabled || c.Secret != "outsecret123" {
		t.Errorf("after switching off: %+v", c)
	}
	if res := a.TestChannel("nope"); res.OK {
		t.Error("test of a missing channel passed")
	}
	if err := a.DeleteChannel(v.ID); err != nil {
		t.Fatal(err)
	}
	if len(a.ListChannels().Channels) != 0 || a.GetSettings().WebhookURL != "" {
		t.Errorf("after delete: %+v", a.ListChannels())
	}
}
