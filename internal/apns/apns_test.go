package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
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

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *ecdsa.PrivateKey) {
	t.Helper()
	key, p8 := testKey(t)
	c, err := New(Config{Key: p8, KeyID: "KID1234567", TeamID: "NUDL4XSA6J", Topic: "cn.superleo.superai.ios"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c.hosts = map[string]string{"sandbox": srv.URL + "/sandbox", "production": srv.URL + "/production"}
	return c, key
}

// verifyJWT checks a provider token the way Apple does: the header names the
// key, the claims name the team, and the signature verifies.
func verifyJWT(t *testing.T, tok string, pub *ecdsa.PublicKey) (map[string]any, map[string]any) {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts", len(parts))
	}
	enc := base64.RawURLEncoding
	var header, claims map[string]any
	for i, out := range []*map[string]any{&header, &claims} {
		raw, err := enc.DecodeString(parts[i])
		if err != nil {
			t.Fatalf("jwt part %d: %v", i, err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("jwt part %d: %v", i, err)
		}
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature: %d bytes, %v", len(sig), err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, sum[:], r, s) {
		t.Fatal("jwt signature does not verify")
	}
	return header, claims
}

func TestWakeSendsASilentPush(t *testing.T) {
	var got *http.Request
	var body []byte
	c, key := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		body, _ = io.ReadAll(r.Body)
	})
	if err := c.Wake(context.Background(), "abcd1234", "sandbox", map[string]any{"kind": "alarms", "version": "v7"}); err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("nothing reached the server")
	}
	if got.Method != http.MethodPost || got.URL.Path != "/sandbox/3/device/abcd1234" {
		t.Errorf("request %s %s", got.Method, got.URL.Path)
	}
	for h, want := range map[string]string{
		"apns-push-type": "background",
		"apns-priority":  "5",
		"apns-topic":     "cn.superleo.superai.ios",
	} {
		if v := got.Header.Get(h); v != want {
			t.Errorf("%s = %q, want %q", h, v, want)
		}
	}
	if exp := got.Header.Get("apns-expiration"); exp == "" || exp == "0" {
		t.Errorf("apns-expiration = %q", exp)
	}
	tok, ok := strings.CutPrefix(got.Header.Get("authorization"), "bearer ")
	if !ok {
		t.Fatalf("authorization = %q", got.Header.Get("authorization"))
	}
	header, claims := verifyJWT(t, tok, &key.PublicKey)
	if header["alg"] != "ES256" || header["kid"] != "KID1234567" {
		t.Errorf("jwt header %v", header)
	}
	iat, _ := claims["iat"].(float64)
	if claims["iss"] != "NUDL4XSA6J" || time.Since(time.Unix(int64(iat), 0)) > time.Minute {
		t.Errorf("jwt claims %v", claims)
	}

	var p struct {
		APS     map[string]any `json:"aps"`
		SuperAI map[string]any `json:"superai"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("payload %s: %v", body, err)
	}
	if p.APS["content-available"] != float64(1) || len(p.APS) != 1 {
		t.Errorf("aps %v: a silent push carries content-available and nothing else", p.APS)
	}
	if p.SuperAI["kind"] != "alarms" || p.SuperAI["version"] != "v7" {
		t.Errorf("superai %v", p.SuperAI)
	}
}

func TestWakeDefaultsToTheConfiguredEnvironment(t *testing.T) {
	var path atomic.Value
	c, _ := testClient(t, func(_ http.ResponseWriter, r *http.Request) { path.Store(r.URL.Path) })
	if err := c.Wake(context.Background(), "t1", "", nil); err != nil {
		t.Fatal(err)
	}
	if p := path.Load(); p != "/production/3/device/t1" {
		t.Errorf("no env and none configured went to %v, want production", p)
	}
	c.cfg.Env = "sandbox"
	if err := c.Wake(context.Background(), "t1", "", nil); err != nil {
		t.Fatal(err)
	}
	if p := path.Load(); p != "/sandbox/3/device/t1" {
		t.Errorf("configured sandbox went to %v", p)
	}
}

func TestProviderTokenIsReusedThenRenewed(t *testing.T) {
	var seen []string
	c, _ := testClient(t, func(_ http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("authorization"))
	})
	now := time.Now()
	c.now = func() time.Time { return now }
	for range 2 {
		if err := c.Wake(context.Background(), "t", "sandbox", nil); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(51 * time.Minute)
	if err := c.Wake(context.Background(), "t", "sandbox", nil); err != nil {
		t.Fatal(err)
	}
	if seen[0] != seen[1] {
		t.Error("a fresh token for every push: Apple throttles that")
	}
	if seen[2] == seen[1] {
		t.Error("token not renewed after 50 minutes")
	}
}

func TestWakeReportsDeadTokens(t *testing.T) {
	for _, tc := range []struct {
		status int
		reason string
	}{{410, "Unregistered"}, {400, "BadDeviceToken"}, {400, "DeviceTokenNotForTopic"}} {
		c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, `{"reason":"`+tc.reason+`"}`)
		})
		if err := c.Wake(context.Background(), "devtok", "production", nil); !errors.Is(err, ErrDeviceGone) {
			t.Errorf("%d %s: err = %v, want ErrDeviceGone", tc.status, tc.reason, err)
		}
	}
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"reason":"TooManyRequests"}`)
	})
	if err := c.Wake(context.Background(), "devtok", "production", nil); err == nil || errors.Is(err, ErrDeviceGone) {
		t.Errorf("throttling should be a plain error, got %v", err)
	}
}

func TestNewReadsAKeyFileAndRefusesHalfAConfig(t *testing.T) {
	_, p8 := testKey(t)
	path := filepath.Join(t.TempDir(), "AuthKey_TEST.p8")
	if err := os.WriteFile(path, p8, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{KeyPath: path, KeyID: "K", TeamID: "T", Topic: "b"}); err != nil {
		t.Errorf("key file: %v", err)
	}
	for name, cfg := range map[string]Config{
		"no key":    {KeyID: "K", TeamID: "T", Topic: "b"},
		"no key id": {Key: p8, TeamID: "T", Topic: "b"},
		"no team":   {Key: p8, KeyID: "K", Topic: "b"},
		"no topic":  {Key: p8, KeyID: "K", TeamID: "T"},
		"not PEM":   {Key: []byte("hello"), KeyID: "K", TeamID: "T", Topic: "b"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAlertSendsAVisibleNotification(t *testing.T) {
	var got *http.Request
	var body []byte
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		body, _ = io.ReadAll(r.Body)
	})
	three := 3
	err := c.Alert(context.Background(), "abcd1234", "", Alert{
		Title:             "该出发接三宝了",
		Body:              "17:10 出发",
		Sound:             true,
		ThreadID:          "reminder",
		InterruptionLevel: "time-sensitive",
		CollapseID:        "pickup-2026-10-14",
		Badge:             &three,
		Data:              map[string]any{"kind": "notice", "id": "n1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/production/3/device/abcd1234" {
		t.Errorf("path %s (an empty env takes the config's, production)", got.URL.Path)
	}
	for h, want := range map[string]string{
		"apns-push-type":   "alert",
		"apns-priority":    "10",
		"apns-topic":       "cn.superleo.superai.ios",
		"apns-collapse-id": "pickup-2026-10-14",
	} {
		if v := got.Header.Get(h); v != want {
			t.Errorf("%s = %q, want %q", h, v, want)
		}
	}
	if !strings.HasPrefix(got.Header.Get("authorization"), "bearer ") {
		t.Errorf("authorization = %q", got.Header.Get("authorization"))
	}
	var p struct {
		APS struct {
			Alert             map[string]string `json:"alert"`
			Sound             string            `json:"sound"`
			ThreadID          string            `json:"thread-id"`
			InterruptionLevel string            `json:"interruption-level"`
			ContentAvailable  int               `json:"content-available"`
			Badge             *int              `json:"badge"`
		} `json:"aps"`
		SuperAI map[string]any `json:"superai"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	if p.APS.Alert["title"] != "该出发接三宝了" || p.APS.Alert["body"] != "17:10 出发" {
		t.Errorf("alert %v", p.APS.Alert)
	}
	if p.APS.Sound != "default" || p.APS.ThreadID != "reminder" || p.APS.InterruptionLevel != "time-sensitive" {
		t.Errorf("aps %+v", p.APS)
	}
	if p.APS.Badge == nil || *p.APS.Badge != 3 {
		t.Errorf("badge %v", p.APS.Badge)
	}
	if p.APS.ContentAvailable != 0 {
		t.Error("a visible alert is not a background push")
	}
	if p.SuperAI["kind"] != "notice" || p.SuperAI["id"] != "n1" {
		t.Errorf("superai %v", p.SuperAI)
	}
}

func TestAQuietAlertHasNoSoundAndLowPriority(t *testing.T) {
	var got *http.Request
	var body []byte
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		body, _ = io.ReadAll(r.Body)
	})
	if err := c.Alert(context.Background(), "abcd1234", "sandbox", Alert{Body: "fyi", Priority: 5}); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("apns-priority") != "5" || got.Header.Get("apns-collapse-id") != "" {
		t.Errorf("headers %v", got.Header)
	}
	var p map[string]map[string]any
	_ = json.Unmarshal(body, &p)
	if _, ok := p["aps"]["sound"]; ok {
		t.Errorf("sound in %s", body)
	}
	if _, ok := p["aps"]["badge"]; ok {
		t.Errorf("badge nobody gave in %s", body)
	}
	if alert, _ := p["aps"]["alert"].(map[string]any); alert["title"] != nil || alert["body"] != "fyi" {
		t.Errorf("alert %s", body)
	}
	if _, ok := p["superai"]; ok {
		t.Errorf("no data was given: %s", body)
	}
}

func TestALongCollapseIDIsHashedToFit(t *testing.T) {
	long := strings.Repeat("k", 65)
	got := CollapseID(long)
	if len(got) != 64 || got == long[:64] || got != CollapseID(long) {
		t.Errorf("CollapseID = %q", got)
	}
	if CollapseID("short") != "short" {
		t.Error("a short id is changed")
	}
}

func TestAlertToADeadTokenSaysSo(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
	})
	err := c.Alert(context.Background(), "abcd1234", "", Alert{Body: "x"})
	if !errors.Is(err, ErrDeviceGone) {
		t.Errorf("err = %v", err)
	}
}

// Zero is a badge too: it clears the icon.
func TestAZeroBadgeIsSent(t *testing.T) {
	var body []byte
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { body, _ = io.ReadAll(r.Body) })
	zero := 0
	if err := c.Alert(context.Background(), "abcd1234", "", Alert{Body: "x", Badge: &zero}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"badge":0`) {
		t.Errorf("payload %s", body)
	}
}
