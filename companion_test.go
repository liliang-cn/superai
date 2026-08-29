package main

import (
	"bufio"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// companionTestApp returns an App with its own data directory (so the handoff
// does not read or write the real auth.json) and an OS-assigned companion port
// (so two test runs, or a running app, do not fight over 43119).
func companionTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("SUPERAI_DESKTOP_HOME", t.TempDir())
	prev := companionPort
	companionPort = 0
	t.Cleanup(func() { companionPort = prev })

	app := NewApp()
	t.Cleanup(app.stopCompanion)
	return app
}

// The regression this whole change is most likely to cause: pointing events at
// the browser server and thereby taking them away from the window. Both legs
// have to fire for one emit.
//
// The window leg is windowEmit rather than the real Wails runtime because the
// runtime calls log.Fatalf on a context it did not create — there is no window
// to be had in a test binary. What is actually under test is emit's control
// flow, which is where the bug would live.
func TestEmitReachesWindowAndCompanionTogether(t *testing.T) {
	app := companionTestApp(t)

	prev := windowEmit
	window := make(chan string, 4)
	windowEmit = func(_ context.Context, name string, _ map[string]any) { window <- name }
	t.Cleanup(func() { windowEmit = prev })
	app.ctx = context.Background() // stands in for a live Wails window

	if _, err := app.OpenInBrowser(); err != nil {
		t.Fatalf("OpenInBrowser: %v", err)
	}
	hub := app.companionHub.Load()
	if hub == nil {
		t.Fatal("the companion server is running but its hub is not in the emit path")
	}
	sub, off := hub.subscribe()
	defer off()

	app.emit("chat:event", map[string]any{"type": "token"})

	select {
	case name := <-window:
		if name != "chat:event" {
			t.Fatalf("window got %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the window stopped receiving events while a companion server was up")
	}
	select {
	case b := <-sub:
		if !strings.Contains(string(b), `"chat:event"`) {
			t.Fatalf("hub envelope = %s", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the companion hub received nothing")
	}
}

// Serve mode still has exactly one sink, and it is emitFn. Nothing about the
// fan-out may start requiring a window that is not there.
func TestEmitWithOnlyEmitFnStillWorks(t *testing.T) {
	app := NewApp()
	got := make(chan string, 1)
	app.emitFn = func(name string, _ map[string]any) { got <- name }

	app.emit("chat:event", nil)
	select {
	case name := <-got:
		if name != "chat:event" {
			t.Fatalf("emitFn got %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("emitFn received nothing")
	}
}

// Clicking the button twice must not leave two listeners behind. The origin is
// the stable part; the token is not, on purpose.
func TestOpenInBrowserIsIdempotent(t *testing.T) {
	app := companionTestApp(t)

	first, err := app.OpenInBrowser()
	if err != nil {
		t.Fatalf("first OpenInBrowser: %v", err)
	}
	srv := app.companion

	second, err := app.OpenInBrowser()
	if err != nil {
		t.Fatalf("second OpenInBrowser: %v", err)
	}
	if app.companion != srv {
		t.Fatal("the second click replaced the server instead of reusing it")
	}
	if originOf(t, first) != originOf(t, second) {
		t.Fatalf("origin moved between clicks: %s then %s", first, second)
	}
	if tokenOf(t, first) == tokenOf(t, second) {
		t.Fatal("both clicks handed out the same token, so it is not single-use")
	}
}

func TestHandoffTokenIsSingleUseAndExpires(t *testing.T) {
	s := newHandoffStore()
	now := time.Now()

	tok := s.mint(now)
	if !s.redeem(tok, now) {
		t.Fatal("a freshly minted token did not redeem")
	}
	if s.redeem(tok, now) {
		t.Error("the same token redeemed twice")
	}

	stale := s.mint(now)
	if s.redeem(stale, now.Add(handoffTTL+time.Second)) {
		t.Error("an expired token still redeemed")
	}
	if s.redeem("", now) || s.redeem("not-a-token", now) {
		t.Error("a token nobody minted redeemed")
	}
	if len(s.tokens) != 0 {
		t.Errorf("%d tokens left behind; spent and expired ones should both be gone", len(s.tokens))
	}
}

// End to end against the real listener: the URL the button hands out signs the
// browser in exactly once, and everything else still needs that session.
func TestCompanionHandoffSignsInOnceAndGatesTheRest(t *testing.T) {
	app := companionTestApp(t)

	link, err := app.OpenInBrowser()
	if err != nil {
		t.Fatalf("OpenInBrowser: %v", err)
	}
	origin := originOf(t, link)

	// Nothing yet: an RPC from a client that never clicked the button is 401.
	resp, err := http.Post(origin+"/api/rpc/GetSettings", "application/json", strings.NewReader("[]"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated RPC = %d, want 401", resp.StatusCode)
	}

	// A bad token is turned away without a cookie, and lands where a stranger
	// lands rather than on an error page.
	bad := get(t, origin+handoffPath+"?t=deadbeef")
	if bad.StatusCode != http.StatusSeeOther {
		t.Fatalf("bad handoff = %d, want 303", bad.StatusCode)
	}
	if c := sessionCookieOf(bad); c != "" {
		t.Fatal("a token nobody minted was given a session")
	}

	// The real one works.
	ok := get(t, link)
	if ok.StatusCode != http.StatusSeeOther || ok.Header.Get("Location") != "/" {
		t.Fatalf("handoff = %d -> %q, want 303 -> /", ok.StatusCode, ok.Header.Get("Location"))
	}
	cookie := sessionCookieOf(ok)
	if cookie == "" {
		t.Fatal("the handoff did not issue a session cookie")
	}

	// Replaying the same URL — a reloaded tab, a copied link — gets nothing.
	replay := get(t, link)
	if c := sessionCookieOf(replay); c != "" {
		t.Fatal("the handoff URL worked a second time")
	}

	// And the cookie it did issue is the one requireAuth accepts.
	req, _ := http.NewRequest(http.MethodPost, origin+"/api/rpc/GetSettings", strings.NewReader("[]"))
	req.Header.Set("Cookie", sessionCookie+"="+cookie)
	authed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	authed.Body.Close()
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("RPC with the handed-off session = %d, want 200", authed.StatusCode)
	}

	// And a tab holding that session receives live events over SSE — the whole
	// reason the fan-out in emit had to stop being an either/or.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/events", nil)
	sreq.Header.Set("Cookie", sessionCookie+"="+cookie)
	stream, err := http.DefaultClient.Do(sreq)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("SSE with the handed-off session = %d, want 200", stream.StatusCode)
	}
	br := bufio.NewReader(stream.Body)
	if line, err := br.ReadString('\n'); err != nil || !strings.HasPrefix(line, ":") {
		t.Fatalf("opening SSE frame = %q, %v", line, err)
	}

	frames := make(chan string, 1)
	go func() {
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "data: ") {
				frames <- strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				return
			}
		}
	}()
	app.emit("chat:event", map[string]any{"type": "token", "text": "hi"})
	select {
	case data := <-frames:
		if !strings.Contains(data, `"chat:event"`) || !strings.Contains(data, `"hi"`) {
			t.Fatalf("SSE frame = %s", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the browser tab received no events from the running app")
	}

	// Shutting down takes the port with it, and the hub out of the emit path.
	app.stopCompanion()
	if app.companionHub.Load() != nil {
		t.Error("the hub is still in the emit path after shutdown")
	}
	if _, err := http.Get(origin + "/"); err == nil {
		t.Error("the companion server is still listening after shutdown")
	}
}

// A tab opened this way must not be able to open more listeners.
func TestOpenInBrowserIsDeniedOverRPC(t *testing.T) {
	if _, denied := rpcDenied["OpenInBrowser"]; !denied {
		t.Fatal("OpenInBrowser is callable over HTTP; a browser client can start servers")
	}
}

func get(t *testing.T, u string) *http.Response {
	t.Helper()
	// No redirect following: the cookie and the 303 are the thing being checked.
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func sessionCookieOf(resp *http.Response) string {
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

func originOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Scheme + "://" + u.Host
}

func tokenOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	tok := u.Query().Get("t")
	if tok == "" {
		t.Fatalf("no handoff token in %q", raw)
	}
	return tok
}
