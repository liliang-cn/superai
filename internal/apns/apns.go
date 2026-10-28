// Package apns sends the iOS app its pushes: silent ones ("something changed,
// come and look", no banner and no sound) and visible ones, the notifications
// a person reads on the lock screen.
//
// Token-based auth (a .p8 key and a short ES256 JWT) over the HTTP/2 that Go's
// standard client already speaks to Apple. No push library: the whole
// protocol is one POST per device, and the JWT is four lines of crypto/ecdsa.
package apns

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ErrDeviceGone says the device token is no longer any good (the app was
// deleted, or the token changed). The caller drops it and waits for the app
// to register a new one.
var ErrDeviceGone = errors.New("apns: device token no longer valid")

// Hosts are Apple's push endpoints by environment.
var Hosts = map[string]string{
	"sandbox":    "https://api.sandbox.push.apple.com",
	"production": "https://api.push.apple.com",
}

// Config is what Apple wants to know about the sender.
type Config struct {
	// Key is the .p8 key's PEM text. When empty, KeyPath is read instead.
	Key     []byte
	KeyPath string
	KeyID   string
	TeamID  string
	// Topic is the app's bundle id.
	Topic string
	// Env is the environment for a device that did not say which it is in:
	// "sandbox" or "production" (the default).
	Env string
}

// Client sends pushes. Safe for concurrent use.
type Client struct {
	cfg  Config
	key  *ecdsa.PrivateKey
	http *http.Client
	// hosts is Hosts unless a test points it at a fake.
	hosts map[string]string
	now   func() time.Time

	mu       sync.Mutex
	jwt      string
	jwtIssue time.Time
}

// New reads the key and checks that the config can sign anything at all.
func New(cfg Config) (*Client, error) {
	switch {
	case strings.TrimSpace(cfg.KeyID) == "":
		return nil, errors.New("apns: no key id")
	case strings.TrimSpace(cfg.TeamID) == "":
		return nil, errors.New("apns: no team id")
	case strings.TrimSpace(cfg.Topic) == "":
		return nil, errors.New("apns: no topic")
	}
	raw := cfg.Key
	if len(bytes.TrimSpace(raw)) == 0 {
		if cfg.KeyPath == "" {
			return nil, errors.New("apns: no key")
		}
		var err error
		if raw, err = os.ReadFile(cfg.KeyPath); err != nil {
			return nil, fmt.Errorf("apns key: %w", err)
		}
	}
	key, err := ParseKey(raw)
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:   cfg,
		key:   key,
		http:  &http.Client{Timeout: 10 * time.Second},
		hosts: Hosts,
		now:   time.Now,
	}, nil
}

// ParseKey reads a .p8 file's contents: PKCS#8 PEM holding a P-256 key.
func ParseKey(raw []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("apns key: not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns key: %w", err)
	}
	key, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apns key: not an EC key")
	}
	return key, nil
}

// Topic is the bundle id pushes are addressed to.
func (c *Client) Topic() string { return c.cfg.Topic }

// providerToken is the JWT, renewed every 50 minutes: Apple wants one between
// 20 and 60 minutes old, and refuses a sender that mints them more often.
func (c *Client) providerToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.jwt != "" && now.Sub(c.jwtIssue) < 50*time.Minute {
		return c.jwt, nil
	}
	signed, err := signES256(c.key,
		map[string]any{"alg": "ES256", "kid": c.cfg.KeyID},
		map[string]any{"iss": c.cfg.TeamID, "iat": now.Unix()})
	if err != nil {
		return "", err
	}
	c.jwt, c.jwtIssue = signed, now
	return signed, nil
}

// signES256 is a compact JWS: base64url header and claims, and the P-256
// signature over them as r‖s, 32 bytes each.
func signES256(key *ecdsa.PrivateKey, header, claims map[string]any) (string, error) {
	enc := base64.RawURLEncoding
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cl, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := enc.EncodeToString(h) + "." + enc.EncodeToString(cl)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + enc.EncodeToString(sig), nil
}

// Wake sends one device a silent background push carrying data under the
// "superleo" key (what the phone apps read). env is "sandbox" or "production"; empty takes the config's.
func (c *Client) Wake(ctx context.Context, deviceToken, env string, data any) error {
	payload, err := json.Marshal(map[string]any{
		"aps":      map[string]any{"content-available": 1},
		"superleo": data,
	})
	if err != nil {
		return err
	}
	return c.send(ctx, deviceToken, env, payload, map[string]string{
		"apns-push-type": "background",
		"apns-priority":  "5", // the only priority a background push may have
	})
}

// Alert is a notification the person sees: a banner, the lock screen, the
// notification centre.
type Alert struct {
	Title string
	Body  string
	// Sound plays the default sound. A brief that arrives every morning should
	// not; a reminder to leave the house should.
	Sound bool
	// ThreadID groups notifications in the notification centre.
	ThreadID string
	// InterruptionLevel is "passive", "active" (the default), "time-sensitive"
	// or "critical". Time-sensitive breaks through a Focus that allows it.
	InterruptionLevel string
	// Priority is 10 (deliver now, the default) or 5 (when convenient for the
	// device's battery).
	Priority int
	// CollapseID makes a later notification with the same id replace this one
	// on the device. Apple caps it at 64 bytes; a longer one is hashed down.
	CollapseID string
	// Badge is the number on the app's icon; nil leaves it as it is.
	Badge *int
	// Data goes under the "superleo" key (what the phone apps read), for the app to act on when tapped.
	Data any
	// Category names the notification category the app registered for this
	// kind of notification: the buttons and the text field it gets on the
	// lock screen.
	Category string
}

// Alert sends one device a visible notification. env is as for Wake.
func (c *Client) Alert(ctx context.Context, deviceToken, env string, a Alert) error {
	alert := map[string]any{"body": a.Body}
	if a.Title != "" {
		alert["title"] = a.Title
	}
	aps := map[string]any{"alert": alert}
	if a.Sound {
		aps["sound"] = "default"
	}
	if a.ThreadID != "" {
		aps["thread-id"] = a.ThreadID
	}
	if a.Badge != nil {
		aps["badge"] = *a.Badge
	}
	if a.InterruptionLevel != "" {
		aps["interruption-level"] = a.InterruptionLevel
	}
	if a.Category != "" {
		aps["category"] = a.Category
	}
	body := map[string]any{"aps": aps}
	if a.Data != nil {
		body["superleo"] = a.Data
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	prio := a.Priority
	if prio != 5 {
		prio = 10
	}
	headers := map[string]string{
		"apns-push-type": "alert",
		"apns-priority":  fmt.Sprint(prio),
	}
	if id := CollapseID(a.CollapseID); id != "" {
		headers["apns-collapse-id"] = id
	}
	return c.send(ctx, deviceToken, env, payload, headers)
}

// CollapseID fits an id into the 64 bytes Apple allows: as it is when it
// fits, its sha256 in hex (exactly 64) when it does not. Stable, so the same
// key always collapses onto the same notification.
func CollapseID(id string) string {
	if len(id) <= 64 {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// send is one POST to Apple, and what its answer means.
func (c *Client) send(ctx context.Context, deviceToken, env string, payload []byte, headers map[string]string) error {
	if env == "" {
		env = c.cfg.Env
	}
	host, ok := c.hosts[env]
	if !ok {
		host = c.hosts["production"]
	}
	token, err := c.providerToken()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/3/device/"+deviceToken, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", c.cfg.Topic)
	req.Header.Set("apns-expiration", fmt.Sprint(c.now().Add(24*time.Hour).Unix()))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("apns: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var reason struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &reason)
	switch {
	case resp.StatusCode == http.StatusGone,
		reason.Reason == "BadDeviceToken", reason.Reason == "Unregistered", reason.Reason == "DeviceTokenNotForTopic":
		return fmt.Errorf("%w (%d %s)", ErrDeviceGone, resp.StatusCode, reason.Reason)
	case reason.Reason == "ExpiredProviderToken":
		c.mu.Lock()
		c.jwt = ""
		c.mu.Unlock()
	}
	return fmt.Errorf("apns: %d %s", resp.StatusCode, reason.Reason)
}
