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
// Two shapes because there are two kinds of caller. A browser gets HTTP Basic
// (native prompt, cached by the browser, no login page to build and no session
// store to get wrong). A program — an MCP client, a script — gets a bearer
// token, which is what those already know how to carry.

package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/liliang-cn/superai-desktop/backend"
)

// authFileName sits in the data directory, beside settings.json.
const authFileName = "auth.json"

type credentials struct {
	// User is the name the Basic prompt expects. Cosmetic: there is one account.
	User string `json:"user"`
	// PasswordHash is bcrypt. The plaintext is printed once, at creation, and
	// then only exists wherever the operator put it.
	PasswordHash string `json:"password_hash"`
	// Token authenticates programs: `Authorization: Bearer <token>`.
	Token string `json:"token"`
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
		return &c, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	password := randomSecret(12)
	token := randomSecret(24)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	c := &credentials{User: "superai", PasswordHash: string(hash), Token: token}

	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o600); err != nil {
		return nil, err
	}

	// Printed once, on the run that created them. After this the password is
	// only a bcrypt hash on disk, and this log line is the only copy.
	log.Printf("no credentials found — generated a set and wrote %s", p)
	log.Printf("  browser:  user %s  password %s", c.User, password)
	log.Printf("  programs: Authorization: Bearer %s", token)
	log.Printf("  change them by editing that file (password_hash is bcrypt) and restarting")
	return c, nil
}

func randomSecret(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// A credential that is not random is not a credential.
		panic("superai: cannot read random bytes for a credential: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// requireAuth gates every request. Basic for people, Bearer for programs.
func requireAuth(c *credentials, next http.Handler) http.Handler {
	want := []byte(c.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			// Constant-time: this endpoint is reachable from the network, and a
			// compare that returns early on the first wrong byte answers "how
			// much of the token is right" to anyone timing it.
			if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(bearer)), want) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			challenge(w)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(c.User)) != 1 ||
			bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(pass)) != nil {
			challenge(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="SuperAI", charset="UTF-8"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
