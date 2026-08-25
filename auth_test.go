package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testCreds(t *testing.T, password string) *credentials {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return &credentials{
		User:         "superai",
		PasswordHash: string(h),
		Token:        "token-for-programs",
		SessionKey:   "0123456789abcdef0123456789abcdef",
	}
}

// The whole point of signing instead of storing: a restart must not sign the
// user out, and nobody without the key can mint a cookie.
func TestASignedSessionSurvivesAndCannotBeForged(t *testing.T) {
	now := time.Now()
	v := issueSession("key-a", now.Add(time.Hour))

	if !validSession("key-a", v, now) {
		t.Fatal("a freshly issued session did not validate")
	}
	if validSession("key-b", v, now) {
		t.Error("a session validated under a different key — the signature is not doing anything")
	}
	if validSession("key-a", v, now.Add(2*time.Hour)) {
		t.Error("an expired session still validated")
	}
}

// Rotating session_key is the documented way to sign every device out. If a
// stale cookie survived it, the lever would not work.
func TestRotatingTheKeyInvalidatesEveryone(t *testing.T) {
	now := time.Now()
	old := issueSession("old-key", now.Add(sessionLifetime))
	if validSession("new-key", old, now) {
		t.Error("a cookie issued under the old key still opened the door")
	}
}

// Moving the expiry forward is the obvious forgery: the payload is right there
// in the cookie. It must not verify.
func TestEditingTheExpiryBreaksTheSignature(t *testing.T) {
	now := time.Now()
	v := issueSession("k", now.Add(-time.Hour)) // already expired
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("unexpected cookie shape: %q", v)
	}
	tampered := "99999999999." + parts[1] + "." + parts[2]
	if validSession("k", tampered, now) {
		t.Error("a hand-edited expiry was accepted")
	}
}

func TestGarbageCookiesAreRejectedNotPanicked(t *testing.T) {
	for _, v := range []string{"", ".", "abc", "1.2", "....", "9999999999.nonce.deadbeef"} {
		if validSession("k", v, time.Now()) {
			t.Errorf("%q was accepted", v)
		}
	}
}

func loginRequest(password string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"password":`+`"`+password+`"}`))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestLoginSetsACookieThatOpensTheAPI(t *testing.T) {
	c := testCreds(t, "hunter2")
	mux := http.NewServeMux()
	authRoutes(mux, c)
	mux.HandleFunc("/api/rpc/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	h := requireAuth(c, mux)

	// Before signing in: refused, and *without* a Basic challenge — that
	// header is exactly what makes the browser draw its own popup instead of
	// our page.
	pre := httptest.NewRecorder()
	h.ServeHTTP(pre, httptest.NewRequest(http.MethodPost, "/api/rpc/GetStatus", nil))
	if pre.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated RPC returned %d", pre.Code)
	}
	if got := pre.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("still challenging with Basic: %q", got)
	}

	in := httptest.NewRecorder()
	h.ServeHTTP(in, loginRequest("hunter2"))
	if in.Code != http.StatusOK {
		t.Fatalf("login with the right password returned %d: %s", in.Code, in.Body)
	}
	var cookie *http.Cookie
	for _, ck := range in.Result().Cookies() {
		if ck.Name == sessionCookie {
			cookie = ck
		}
	}
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("the session cookie is readable by script")
	}

	after := httptest.NewRequest(http.MethodPost, "/api/rpc/GetStatus", nil)
	after.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, after)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed-in RPC returned %d", rec.Code)
	}
}

func TestWrongPasswordGetsNoCookie(t *testing.T) {
	c := testCreds(t, "hunter2")
	mux := http.NewServeMux()
	authRoutes(mux, c)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, loginRequest("hunter3"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password returned %d", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("a failed login handed out a cookie")
	}
}

// MCP clients and scripts carry a bearer token; the login form must not have
// taken that away from them.
func TestBearerStillWorksForPrograms(t *testing.T) {
	c := testCreds(t, "hunter2")
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	h := requireAuth(c, inner)

	r := httptest.NewRequest(http.MethodPost, mcpPath, nil)
	r.Header.Set("Authorization", "Bearer "+c.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("a valid bearer token was refused: %d", rec.Code)
	}

	bad := httptest.NewRequest(http.MethodPost, mcpPath, nil)
	bad.Header.Set("Authorization", "Bearer not-the-token")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong bearer token returned %d", rec.Code)
	}
}

// The page has to load before it can ask for a password; everything that can
// act has to stay shut until it has one.
func TestOnlyTheStaticBundleIsPublic(t *testing.T) {
	public := []string{"/", "/index.html", "/assets/index-abc123.js", "/api/login", "/api/session", "/api/logout"}
	gated := []string{"/api/rpc/GetStatus", "/api/events", mcpPath, mcpPath + "/messages"}

	for _, p := range public {
		if gatedPath(p) {
			t.Errorf("%s is gated; the login screen cannot load", p)
		}
	}
	for _, p := range gated {
		if !gatedPath(p) {
			t.Errorf("%s is reachable without signing in", p)
		}
	}
}

func TestThrottleStopsAFloodAndForgetsAfterTheWindow(t *testing.T) {
	tr := newLoginThrottle()
	now := time.Now()
	for i := 0; i < throttleAfter; i++ {
		if !tr.allow("10.0.0.1", now) {
			t.Fatalf("blocked after only %d attempts", i)
		}
		tr.record("10.0.0.1", now)
	}
	if tr.allow("10.0.0.1", now) {
		t.Error("the flood was not stopped")
	}
	// A different address is a different person until proven otherwise.
	if !tr.allow("10.0.0.2", now) {
		t.Error("one guesser locked out everyone else")
	}
	if !tr.allow("10.0.0.1", now.Add(throttleWindow+time.Minute)) {
		t.Error("the lockout never expires — a typo would be permanent")
	}
}

func TestClientIPPrefersTheLastForwardedHop(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.4")
	if got := clientIP(r); got != "198.51.100.4" {
		t.Errorf("clientIP = %q; a client-supplied first hop must not be trusted", got)
	}

	bare := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	bare.RemoteAddr = "192.168.1.7:41234"
	if got := clientIP(bare); got != "192.168.1.7" {
		t.Errorf("clientIP = %q, want 192.168.1.7", got)
	}
}
