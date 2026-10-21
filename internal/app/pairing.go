// Pairing a phone.
//
// The phone app needs a credential of its own. Handing it the bearer token in
// auth.json would work and would be wrong: that token is every program's, so a
// lost phone could only be shut out by changing it under everything else, and
// the phone would never be told apart from them in any log. So each phone gets
// a device token — minted for it, kept on disk only as a hash, and revocable
// one at a time.
//
// Getting it there is the pairing. Someone already signed in (the web page)
// asks for a code; the page shows it as six digits and as a QR code carrying
// the server's address too. The phone sends the code back to /api/pair/claim,
// the one route here that needs no credential, and gets its token. A code is
// good once, for five minutes.
//
// Six digits are a small space, so guessing is fenced in twice: per address
// like the password, and in total — past maxClaimMisses wrong codes from
// anywhere, every outstanding code is void and has to be asked for again.
package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/superai/internal/backend"
)

const (
	pairCodeTTL    = 5 * time.Minute
	maxClaimMisses = 20
	// lastSeenEvery bounds how often a device's last-seen time is written
	// back: every request would be a disk write per event-stream reconnect.
	lastSeenEvery = time.Minute
	devicesFile   = "devices.json"
)

// Device is one paired phone. TokenHash is sha256 of its token: the token
// itself exists only on the phone.
type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	// PushToken is the device token the app registered, and PushEnv the
	// APNs environment its build talks to ("sandbox" or "production"; empty
	// means the server's default). See app_alarms.go.
	PushToken string `json:"push_token,omitempty"`
	PushEnv   string `json:"push_env,omitempty"`
	// PushProvider is who delivers to PushToken: empty or "apns" for an
	// iPhone, a vendor channel ("mipush", "hms", …) for an Android phone.
	PushProvider string    `json:"push_provider,omitempty"`
	PushUpdated  time.Time `json:"push_updated,omitzero"`
}

// deviceStore is the paired devices and the codes waiting to be claimed.
type deviceStore struct {
	path string

	mu      sync.Mutex
	devices []Device
	codes   map[string]time.Time // code -> expiry
	misses  int
	now     func() time.Time
}

var (
	deviceStoresMu sync.Mutex
	deviceStores   = map[string]*deviceStore{}
)

// openDevices returns the one store for path. The desktop app's companion
// server and serve mode load credentials separately; they must not each keep
// a copy that the other's writes go unseen in.
func openDevices(path string) (*deviceStore, error) {
	deviceStoresMu.Lock()
	defer deviceStoresMu.Unlock()
	if s, ok := deviceStores[path]; ok {
		return s, nil
	}
	s := &deviceStore{path: path, codes: map[string]time.Time{}, now: time.Now}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &s.devices); err != nil {
			return nil, fmt.Errorf("%s is not readable as a device list: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	deviceStores[path] = s
	return s, nil
}

func devicesPath() string { return filepath.Join(backend.DataDir(), devicesFile) }

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func (s *deviceStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.devices, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// newCode mints a six-digit code. Leading zeros are kept: it is read off a
// screen, not parsed.
func (s *deviceStore) newCode() (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for c, exp := range s.codes {
		if now.After(exp) {
			delete(s.codes, c)
		}
	}
	var code string
	for {
		n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			panic("superai: cannot read random bytes for a pairing code: " + err.Error())
		}
		code = fmt.Sprintf("%06d", n.Int64())
		if _, taken := s.codes[code]; !taken {
			break
		}
	}
	exp := now.Add(pairCodeTTL)
	s.codes[code] = exp
	return code, exp
}

// claim trades a code for a new device and its token. ok is false for a code
// that is wrong, spent or expired — the caller does not learn which.
func (s *deviceStore) claim(code, name string) (Device, string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	code = strings.TrimSpace(code)
	exp, found := s.codes[code]
	if !found || now.After(exp) {
		s.misses++
		if s.misses >= maxClaimMisses {
			// Someone is working through the space. Whatever they were
			// aiming at is gone; the person pairing asks for a fresh code.
			s.codes = map[string]time.Time{}
			s.misses = 0
		}
		return Device{}, "", false, nil
	}
	delete(s.codes, code)

	name = strings.TrimSpace(name)
	if name == "" {
		name = "Phone"
	}
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	tok := randomSecret(32)
	d := Device{ID: randomSecret(8), Name: name, TokenHash: hashToken(tok), CreatedAt: now.UTC(), LastSeen: now.UTC()}
	s.devices = append(s.devices, d)
	if err := s.saveLocked(); err != nil {
		s.devices = s.devices[:len(s.devices)-1]
		return Device{}, "", false, err
	}
	return d, tok, true, nil
}

// authenticate says whether tok belongs to a paired device, noting when it was
// last seen.
func (s *deviceStore) authenticate(tok string) bool {
	if tok == "" {
		return false
	}
	h := hashToken(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.devices {
		if subtle.ConstantTimeCompare([]byte(s.devices[i].TokenHash), []byte(h)) == 1 {
			now := s.now().UTC()
			if now.Sub(s.devices[i].LastSeen) > lastSeenEvery {
				s.devices[i].LastSeen = now
				_ = s.saveLocked() // a missed last-seen is not worth refusing a request over
			}
			return true
		}
	}
	return false
}

// deviceFor is the device a bearer token belongs to.
func (s *deviceStore) deviceFor(tok string) (string, bool) {
	if tok == "" {
		return "", false
	}
	h := hashToken(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(h)) == 1 {
			return d.ID, true
		}
	}
	return "", false
}

// setPushToken records where device id is reached by push, replacing what it
// had. An empty token unregisters it. A token another device still holds with
// the same provider is taken from it: the same phone paired twice must not be
// woken twice.
func (s *deviceStore) setPushToken(id, token, env, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := -1
	for i := range s.devices {
		if s.devices[i].ID == id {
			at = i
		}
	}
	if at < 0 {
		return fmt.Errorf("没有这个设备")
	}
	now := s.now().UTC()
	if token == "" {
		env, provider = "", ""
	}
	if provider == pushAPNs {
		provider = ""
	}
	changed := false
	if token != "" {
		for i := range s.devices {
			if d := &s.devices[i]; i != at && d.PushToken == token && d.PushProvider == provider {
				d.PushToken, d.PushEnv, d.PushProvider, d.PushUpdated = "", "", "", now
				changed = true
			}
		}
	}
	if d := &s.devices[at]; d.PushToken != token || d.PushEnv != env || d.PushProvider != provider {
		d.PushToken, d.PushEnv, d.PushProvider, d.PushUpdated = token, env, provider, now
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

// pushTarget is one device to wake.
type pushTarget struct {
	DeviceID string
	Token    string
	Env      string
	// Provider is pushAPNs or a vendor channel.
	Provider string
}

// pushTargets is every device with an APNs token, the ones the APNs client
// sends to. A phone registered with any other provider is reached through
// pushToVendors (push_vendor.go).
func (s *deviceStore) pushTargets() []pushTarget {
	var out []pushTarget
	for _, t := range s.allPushTargets() {
		if t.Provider == pushAPNs {
			out = append(out, t)
		}
	}
	return out
}

// allPushTargets is every device with a push token, whatever delivers it.
func (s *deviceStore) allPushTargets() []pushTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []pushTarget
	for _, d := range s.devices {
		if d.PushToken != "" {
			p := d.PushProvider
			if p == "" {
				p = pushAPNs
			}
			out = append(out, pushTarget{DeviceID: d.ID, Token: d.PushToken, Env: d.PushEnv, Provider: p})
		}
	}
	return out
}

// dropPushToken forgets a token APNs refused, unless the device has meanwhile
// registered another one.
func (s *deviceStore) dropPushToken(id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.devices {
		if s.devices[i].ID == id && s.devices[i].PushToken == token {
			s.devices[i].PushToken, s.devices[i].PushEnv, s.devices[i].PushProvider, s.devices[i].PushUpdated = "", "", "", s.now().UTC()
			return s.saveLocked()
		}
	}
	return nil
}

func (s *deviceStore) list() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Device(nil), s.devices...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *deviceStore) revoke(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.devices {
		if s.devices[i].ID == id {
			s.devices = append(s.devices[:i], s.devices[i+1:]...)
			return true, s.saveLocked()
		}
	}
	return false, nil
}

const pairClaimPath = "/api/pair/claim"

// pairRoutes registers the claim, the one pairing route that is not an RPC
// method: the phone calling it has no credential yet.
func pairRoutes(mux *http.ServeMux, c *credentials) {
	throttle := newLoginThrottle()
	mux.HandleFunc(pairClaimPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		ip, now := clientIP(r), time.Now()
		if !throttle.allow(ip, now) {
			writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"error": "尝试次数过多，等几分钟再试"})
			return
		}
		var body struct {
			Code   string `json:"code"`
			Device string `json:"device"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "请求格式不对"})
			return
		}
		d, tok, ok, err := c.devices.claim(body.Code, body.Device)
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": "保存设备失败：" + err.Error()})
			return
		}
		if !ok {
			throttle.record(ip, now)
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "配对码不对或已过期"})
			return
		}
		out := map[string]any{"token": tok, "device_id": d.ID, "name": d.Name}
		// Where an agent joining with this code reaches the link, when this
		// core was told (agent_join.go).
		if l := strings.TrimSpace(os.Getenv(agentAdvertiseEnv)); l != "" {
			out["agent_link"] = l
		}
		writeJSON(w, out)
	})
}

// registerPushPath is RegisterPushToken. It sits among the RPC methods and
// takes its arguments the same way, but is a route of its own: the method has
// to know which paired device is calling, and only the request knows that.
const registerPushPath = "/api/rpc/RegisterPushToken"

// registerPushRoute records a paired phone's push token.
//
//	POST /api/rpc/RegisterPushToken  ["<hex device token>","sandbox"|"production"|""]
//	POST /api/rpc/RegisterPushToken  ["<token>","","mipush"|"hms"|"honor"|"oppo"|"vivo"|"sse"|"apns"]
//	→ {"ok":true,"device_id":"…"}
//
// Two arguments are an iPhone's APNs token, as the iOS app has always sent
// them; a third names the provider, so an Android phone can register the
// token its vendor channel gave it. Only a paired device's own token may call
// it (403 otherwise): a push token belongs to a device, and the shared token
// and a browser session are not one. An empty device token unregisters.
// Unpairing the device drops its push token with it.
func registerPushRoute(mux *http.ServeMux, c *credentials) {
	mux.HandleFunc(registerPushPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		var id string
		ok := false
		if c.devices != nil {
			id, ok = c.devices.deviceFor(strings.TrimSpace(bearer))
		}
		if !ok {
			http.Error(w, "RegisterPushToken: only a paired device can register for pushes", http.StatusForbidden)
			return
		}
		var args []string
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&args); err != nil || len(args) < 2 || len(args) > 3 {
			http.Error(w, "RegisterPushToken takes 2 or 3 arguments: token, env[, provider]", http.StatusBadRequest)
			return
		}
		provider := pushAPNs
		if len(args) == 3 {
			if p := strings.ToLower(strings.TrimSpace(args[2])); p != "" {
				provider = p
			}
		}
		if !pushProviders[provider] {
			http.Error(w, "provider must be one of apns, mipush, hms, honor, oppo, vivo, sse", http.StatusBadRequest)
			return
		}
		token, env := strings.TrimSpace(args[0]), strings.ToLower(strings.TrimSpace(args[1]))
		if provider == pushAPNs {
			token = strings.ToLower(token)
			if token != "" && !validPushToken(token) {
				http.Error(w, "token is not an APNs device token (hex)", http.StatusBadRequest)
				return
			}
			if env != "" && env != "sandbox" && env != "production" {
				http.Error(w, `env must be "sandbox" or "production"`, http.StatusBadRequest)
				return
			}
		} else {
			if token != "" && !validVendorToken(token) {
				http.Error(w, "token is not a push token", http.StatusBadRequest)
				return
			}
			env = ""
		}
		if err := c.devices.setPushToken(id, token, env, provider); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "device_id": id})
	})
}

// pushAPNs is Apple's push service, the provider of a token registered
// without one.
const pushAPNs = "apns"

// pushProviders is who RegisterPushToken takes tokens for. APNs has its
// client; the vendor channels and sse have their senders in push_vendor.go
// ("sse" is a phone with no vendor channel holding the event stream open, its
// token any id of its own).
var pushProviders = map[string]bool{
	pushAPNs: true, "mipush": true, "hms": true, "honor": true, "oppo": true, "vivo": true, pushSSE: true,
}

// validPushToken is hex of a plausible length: 32 bytes today, and Apple says
// not to count on that.
func validPushToken(t string) bool {
	if len(t) < 32 || len(t) > 400 || len(t)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(t)
	return err == nil
}

// validVendorToken is a vendor's registration id: printable, no spaces, at
// most 512 bytes. Each vendor has its own alphabet, so this checks no more.
func validVendorToken(t string) bool {
	if len(t) < 8 || len(t) > 512 {
		return false
	}
	for _, r := range t {
		if r <= ' ' || r > '~' {
			return false
		}
	}
	return true
}

// PairCode is a code a phone can be paired with.
type PairCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	// Link is the whole pairing link when this is the desktop app, which
	// knows where a phone can reach it (phonelink.go). Empty in serve mode,
	// where the page builds it from the address it was opened at.
	Link string `json:"link,omitempty"`
}

// PairedDevice is a paired phone as the settings page lists it.
type PairedDevice struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// PairPhone mints a code for pairing a phone.
func (a *App) PairPhone() (PairCode, error) {
	s, err := openDevices(devicesPath())
	if err != nil {
		return PairCode{}, err
	}
	code, exp := s.newCode()
	out := PairCode{Code: code, ExpiresAt: exp.UTC()}
	if a.ctx != nil {
		p, err := a.ensurePhoneLink()
		if err != nil {
			return PairCode{}, err
		}
		out.Link = p.pairLink(code)
	}
	return out, nil
}

// PairedDevices lists the paired phones, oldest first.
func (a *App) PairedDevices() ([]PairedDevice, error) {
	s, err := openDevices(devicesPath())
	if err != nil {
		return nil, err
	}
	out := []PairedDevice{}
	for _, d := range s.list() {
		out = append(out, PairedDevice{ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt, LastSeen: d.LastSeen})
	}
	return out, nil
}

// UnpairDevice revokes one phone's token, and with the device goes its push
// token: nothing is sent to a phone that was shut out.
func (a *App) UnpairDevice(id string) error {
	s, err := openDevices(devicesPath())
	if err != nil {
		return err
	}
	ok, err := s.revoke(id)
	if err == nil && !ok {
		err = fmt.Errorf("没有这个设备")
	}
	return err
}
