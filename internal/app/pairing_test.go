package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pairingServer is serve mode's gate and routes over a device store in a
// temporary directory, the way a real install is wired.
func pairingServer(t *testing.T) (http.Handler, *credentials, string) {
	t.Helper()
	c := testCreds(t, "hunter2")
	path := filepath.Join(t.TempDir(), devicesFile)
	s, err := openDevices(path)
	if err != nil {
		t.Fatal(err)
	}
	c.devices = s
	mux := http.NewServeMux()
	authRoutes(mux, c)
	pairRoutes(mux, c)
	mux.HandleFunc("/api/rpc/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return requireAuth(c, mux), c, path
}

func call(h http.Handler, method, path, bearer, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	r.RemoteAddr = "10.0.0.7:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func startCode(t *testing.T, _ http.Handler, c *credentials) string {
	t.Helper()
	code, _ := c.devices.newCode()
	if len(code) != 6 {
		t.Fatalf("code %q", code)
	}
	return code
}

func TestAPairedPhoneGetsItsOwnTokenAndCanBeShutOut(t *testing.T) {
	h, c, path := pairingServer(t)

	// Nobody signed in can ask for a code: it is an RPC method, behind the gate.
	if rec := call(h, http.MethodPost, "/api/rpc/PairPhone", "", "[]"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("PairPhone without a credential: %d", rec.Code)
	}
	code := startCode(t, h, c)

	rec := call(h, http.MethodPost, pairClaimPath, "", `{"code":"`+code+`","device":"李亮的 iPhone"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Token    string `json:"token"`
		DeviceID string `json:"device_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Token == "" || got.Token == c.Token {
		t.Fatalf("token %q", got.Token)
	}

	// The token opens the API.
	if rec := call(h, http.MethodPost, "/api/rpc/ChatSessions", got.Token, "[]"); rec.Code != http.StatusOK {
		t.Fatalf("device token refused: %d", rec.Code)
	}
	// A code is good once.
	if rec := call(h, http.MethodPost, pairClaimPath, "", `{"code":"`+code+`","device":"again"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a spent code was taken again: %d", rec.Code)
	}

	// Only the hash is on disk.
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), got.Token) || !strings.Contains(string(raw), hashToken(got.Token)) {
		t.Fatalf("devices.json: %s", raw)
	}

	// Listed by name, and revoking shuts it out without touching the others.
	if l := c.devices.list(); len(l) != 1 || l[0].Name != "李亮的 iPhone" {
		t.Fatalf("device list: %+v", l)
	}
	if ok, err := c.devices.revoke(got.DeviceID); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	if rec := call(h, http.MethodPost, "/api/rpc/ChatSessions", got.Token, "[]"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a revoked device still gets in: %d", rec.Code)
	}
	if rec := call(h, http.MethodPost, "/api/rpc/ChatSessions", c.Token, "[]"); rec.Code != http.StatusOK {
		t.Fatalf("revoking a phone shut out the main token: %d", rec.Code)
	}
}

func TestAnExpiredCodeIsRefused(t *testing.T) {
	h, c, _ := pairingServer(t)
	clock := time.Now()
	c.devices.now = func() time.Time { return clock }
	code := startCode(t, h, c)
	clock = clock.Add(pairCodeTTL + time.Second)
	if rec := call(h, http.MethodPost, pairClaimPath, "", `{"code":"`+code+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired code accepted: %d", rec.Code)
	}
}

// Wrong guesses from many places add up: past the cap every outstanding code
// is void, so a spread-out search through a million codes cannot finish.
func TestEnoughWrongCodesVoidEveryCode(t *testing.T) {
	_, c, _ := pairingServer(t)
	code, _ := c.devices.newCode()
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	for range maxClaimMisses {
		if _, _, ok, _ := c.devices.claim(wrong, "x"); ok {
			t.Fatal("a wrong code was accepted")
		}
	}
	if _, _, ok, _ := c.devices.claim(code, "x"); ok {
		t.Fatal("a code survived a flood of wrong guesses")
	}
}

// One address is turned away after a few misses, like the password form.
func TestOneAddressCannotKeepGuessing(t *testing.T) {
	h, _, _ := pairingServer(t)
	var last int
	for range throttleAfter + 1 {
		last = call(h, http.MethodPost, pairClaimPath, "", `{"code":"123456"}`).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after %d misses: %d", throttleAfter+1, last)
	}
}

func TestDevicesSurviveARestart(t *testing.T) {
	_, c, path := pairingServer(t)
	code, _ := c.devices.newCode()
	_, tok, ok, err := c.devices.claim(code, "iPad")
	if !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	deviceStoresMu.Lock()
	delete(deviceStores, path) // what a new process would see
	deviceStoresMu.Unlock()
	again, err := openDevices(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.authenticate(tok) {
		t.Fatal("a paired device was forgotten across a restart")
	}
}
