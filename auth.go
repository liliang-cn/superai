// Authentication for serve mode.
//
// The desktop build needs none: a window belongs to whoever is sitting at the
// machine. Serve mode is a different thing wearing the same face — the same
// agent, with the same shell tools, the same workspace and the same billing
// account, now reachable by anything that can open a TCP connection. On a home
// LAN that includes every device on it.
//
// A reverse proxy in front of the public hostname is not enough on its own. It
// leaves the LAN and the tailnet unguarded, it cannot say who a request was,
// and it stops existing the moment someone reaches the port another way.
//
// So: no unauthenticated mode. If no credential exists, one is generated on
// first start and written where the operator can read it — refusing to start
// would be safer still and would also brick a laptop the person is standing in
// front of.
//
// Two shapes because there are two kinds of caller. A person gets a login form
// and a session cookie. A program — an MCP client, a script — gets a bearer
// token, which is what those already know how to carry.
//
// The cookie is signed, not stored. A server-side session table would be one
// more thing to persist, and losing it on every deploy would log the user out
// every deploy. Instead the cookie carries its own expiry and an HMAC over it
// keyed by a secret in auth.json, so a restart keeps you signed in and a forged
// cookie does not verify. The cost is honest: signing out clears the cookie in
// the browser but cannot invalidate a copy someone already took. Rotating
// session_key in auth.json invalidates every session at once, which is the
// lever to pull if a laptop goes missing.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/liliang-cn/superai-desktop/backend"
)

// authFileName sits in the data directory, beside settings.json.
const authFileName = "auth.json"

// sessionCookie is the browser's proof it typed the password.
const sessionCookie = "superai_session"

// sessionLifetime is long on purpose: this is one person's own assistant on
// their own machine, and being thrown back to a password box every week is
// friction without a threat model behind it.
const sessionLifetime = 30 * 24 * time.Hour

type credentials struct {
	// User is a display name. Cosmetic: there is one account, and the login
	// form asks only for the password.
	User string `json:"user"`
	// PasswordHash is bcrypt. The plaintext is printed once, at creation, and
	// then only exists wherever the operator put it.
	PasswordHash string `json:"password_hash"`
	// Token authenticates programs: `Authorization: Bearer <token>`.
	Token string `json:"token"`
	// SessionKey signs session cookies. Change it to sign everyone out.
	SessionKey string `json:"session_key"`
}

func authPath() string { return filepath.Join(backend.DataDir(), authFileName) }

// loadOrCreateCredentials reads auth.json, generating it on first run.
func loadOrCreateCredentials() (*credentials, error) {
	p := authPath()
	if b, err := os.ReadFile(p); err == nil {
		var c credentials
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, fmt.Errorf("%s is not readable as credentials: %w", p, err)
		}
		if c.PasswordHash == "" && c.Token == "" {
			return nil, fmt.Errorf("%s has neither a password nor a token", p)
		}
		if c.User == "" {
			c.User = "superai"
		}
		// Installs that predate the login form have no signing key. Mint one
		// and write it back rather than failing: the password they already
		// have still works, and this is what makes it usable in a browser.
		if c.SessionKey == "" {
			c.SessionKey = randomSecret(32)
			if err := writeCredentials(p, &c); err != nil {
				return nil, fmt.Errorf("adding a session key to %s: %w", p, err)
			}
			log.Printf("added a session signing key to %s", p)
		}
		return &c, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	password := randomSecret(12)
	c := &credentials{User: "superai", Token: randomSecret(24), SessionKey: randomSecret(32)}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	c.PasswordHash = string(hash)
	if err := writeCredentials(p, c); err != nil {
		return nil, err
	}

	// Printed once, on the run that created them. After this the password is
	// only a bcrypt hash on disk, and this log line is the only copy.
	log.Printf("no credentials found — generated a set and wrote %s", p)
	log.Printf("  browser:  password %s", password)
	log.Printf("  programs: Authorization: Bearer %s", c.Token)
	log.Printf("  change them by editing that file (password_hash is bcrypt) and restarting")
	return c, nil
}

func writeCredentials(path string, c *credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func randomSecret(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// A credential that is not random is not a credential.
		panic("superai: cannot read random bytes for a credential: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// issueSession mints a cookie value: an expiry and a nonce, signed.
//
// The nonce is what makes two sessions minted in the same second different
// strings, so signing out of one tab and back in does not hand out a value the
// browser already has cached somewhere.
func issueSession(key string, expiry time.Time) string {
	payload := strconv.FormatInt(expiry.Unix(), 10) + "." + randomSecret(8)
	return payload + "." + signSession(key, payload)
}

func signSession(key, payload string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

// validSession reports whether value was signed by key and has not expired.
func validSession(key, value string, now time.Time) bool {
	i := strings.LastIndex(value, ".")
	if i < 0 {
		return false
	}
	payload, mac := value[:i], value[i+1:]
	// Constant-time: the comparison is reachable from the network, and one
	// that returns early on the first wrong byte answers "how much of this
	// signature is right" to anyone timing it.
	if subtle.ConstantTimeCompare([]byte(mac), []byte(signSession(key, payload))) != 1 {
		return false
	}
	// Only read the expiry after the signature checks out — before that it is
	// attacker-supplied text, not a claim about anything.
	dot := strings.Index(payload, ".")
	if dot < 0 {
		return false
	}
	exp, err := strconv.ParseInt(payload[:dot], 10, 64)
	if err != nil {
		return false
	}
	return now.Unix() < exp
}

// loginThrottle slows down guessing at the one password that exists.
//
// bcrypt already makes each attempt cost about a tenth of a second, which is
// most of the defence; this stops a client from running thousands of those in
// parallel and, just as importantly, stops them from pinning the CPU that the
// agent needs.
type loginThrottle struct {
	mu   sync.Mutex
	fail map[string]int
	next time.Time
}

const (
	throttleAfter  = 8
	throttleWindow = 15 * time.Minute
)

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{fail: map[string]int{}}
}

func (t *loginThrottle) allow(ip string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.After(t.next) {
		t.fail = map[string]int{}
		t.next = now.Add(throttleWindow)
	}
	return t.fail[ip] < throttleAfter
}

func (t *loginThrottle) record(ip string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.next.IsZero() {
		t.next = now.Add(throttleWindow)
	}
	t.fail[ip]++
}

func clientIP(r *http.Request) string {
	// Behind our own reverse proxy, so the last forwarded hop is the one it
	// saw. Absent that, whatever opened the socket.
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		parts := strings.Split(f, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// authRoutes registers the three endpoints the login form talks to. They are
// the only unauthenticated API: everything else needs what they hand out.
func authRoutes(mux *http.ServeMux, c *credentials) {
	throttle := newLoginThrottle()

	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"authed": hasSession(c, r)})
	})

	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		ip, now := clientIP(r), time.Now()
		if !throttle.allow(ip, now) {
			writeJSONStatus(w, http.StatusTooManyRequests,
				map[string]any{"error": "尝试次数过多，等几分钟再试"})
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "请求格式不对"})
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(body.Password)) != nil {
			throttle.record(ip, now)
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "密码不对"})
			return
		}
		expiry := now.Add(sessionLifetime)
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    issueSession(c.SessionKey, expiry),
			Path:     "/",
			Expires:  expiry,
			HttpOnly: true, // script cannot read it, so an XSS cannot post it away
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, map[string]any{"ok": true})
	})
}

// isHTTPS reports whether the browser's leg of the connection was encrypted.
// TLS terminates at the reverse proxy, so the socket here is plaintext and the
// forwarded header is the only witness.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func hasSession(c *credentials, r *http.Request) bool {
	ck, err := r.Cookie(sessionCookie)
	return err == nil && validSession(c.SessionKey, ck.Value, time.Now())
}

func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, http.StatusOK, v) }

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// requireAuth gates the API. The page itself is not gated — it has to load to
// draw the password box, and it is a stock JS bundle with nothing in it. What
// is gated is everything that can act: the RPC surface, the event stream, MCP.
func requireAuth(c *credentials, next http.Handler) http.Handler {
	want := []byte(c.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gatedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok &&
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(bearer)), want) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if hasSession(c, r) {
			next.ServeHTTP(w, r)
			return
		}
		// 401 with no WWW-Authenticate: the browser must render our login
		// form, not its own credential popup.
		writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "sign in"})
	})
}

// gatedPath lists what a stranger must not reach. Anything that is not the
// static bundle or the sign-in endpoints is on this side of the line.
func gatedPath(p string) bool {
	switch {
	case p == "/api/login", p == "/api/logout", p == "/api/session":
		return false
	// The handoff is how a browser holding nothing gets a session; gating it on
	// already having one would make it useless. It carries its own credential
	// (see companion.go) and hands out nothing without one.
	case p == handoffPath:
		return false
	case strings.HasPrefix(p, "/api/"), p == mcpPath, strings.HasPrefix(p, mcpPath+"/"):
		return true
	// The proxied graph view. It is the whole brain with no gate of its own, so
	// forgetting this line would publish everything CortexDB holds to anyone
	// who can reach the port.
	case p == backend.GraphProxyPrefix, strings.HasPrefix(p, backend.GraphProxyPrefix+"/"):
		return true
	}
	return false
}
