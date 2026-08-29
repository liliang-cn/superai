// "Open in Browser": the running desktop app, in a real browser tab.
//
// `superai-desktop serve` already serves this app over HTTP, but it is a
// different process — main.go branches on argv[1] and the windowed app never
// reaches serveMain, so a desktop process runs no HTTP server at all. Telling
// someone to quit the app and relaunch it from a terminal with a flag is not a
// button. So the button starts one, in this process, against the App that is
// already up: same settings, same agent, same conversations, same runs in
// flight. What the tab gets is not a copy, it is the same thing seen from a
// second window.
//
// Two consequences worth stating, because both have bitten:
//
//   - Events have to reach both surfaces now. See App.emit — the window keeps
//     its events while the tab gets the same stream over SSE.
//   - The socket has no idea who is on the other end, so it is gated exactly
//     like serve mode is (auth.go). The person who clicked the button, though,
//     never chose a password and has no reason to know one exists. That is
//     what the handoff below is for.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// companionPort is what the button tries first. Deliberately not 43117: that is
// serve mode's default, and someone running both should end up with two
// listeners rather than a collision they have to read a log to understand. A
// var, not a const, so tests can ask for an OS-assigned port instead of
// fighting over a fixed one.
var companionPort = 43119

// handoffPath trades a one-shot token for the session cookie the login form
// would have issued. It is the only route added on top of serve mode's surface.
const handoffPath = "/api/handoff"

// handoffTTL is how long a minted token stays usable. It only has to survive
// the trip from a click to the browser finishing one request, so a minute is
// already generous. Short matters because the token travels in a URL, and URLs
// end up in history, in screenshots, and in whatever the browser syncs.
const handoffTTL = time.Minute

// handoffStore holds the tokens that have been minted and not yet spent.
//
// In memory and nowhere else: a token is meant to be alive for the length of a
// browser launch, and one that outlived a restart would be a credential lying
// on disk for no reason.
type handoffStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time // token -> expiry
}

func newHandoffStore() *handoffStore {
	return &handoffStore{tokens: map[string]time.Time{}}
}

// mint returns a fresh token. 32 bytes from crypto/rand — the same generator
// and the same width as the bearer token in auth.json, because this is the same
// kind of secret and gets guessed at the same way.
func (s *handoffStore) mint(now time.Time) string {
	tok := randomSecret(32)
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, exp := range s.tokens {
		if now.After(exp) {
			delete(s.tokens, t)
		}
	}
	s.tokens[tok] = now.Add(handoffTTL)
	return tok
}

// redeem spends a token, reporting whether it was live.
//
// One call each, whatever the outcome. The second arrival with the same string
// — a reloaded tab, a URL someone copied out of the address bar, a link pasted
// into a chat — gets nothing, so the value stops being a credential the moment
// it has been used once.
func (s *handoffStore) redeem(tok string, now time.Time) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	live := false
	for t, exp := range s.tokens {
		// Constant-time against each candidate rather than one map probe. The
		// set is tiny and short-lived so the scan costs nothing, and it means
		// there is no argument to have about what a hash lookup's timing says
		// about the prefix of a token.
		if subtle.ConstantTimeCompare([]byte(t), []byte(tok)) != 1 {
			if now.After(exp) {
				delete(s.tokens, t)
			}
			continue
		}
		delete(s.tokens, t)
		live = !now.After(exp)
	}
	return live
}

// handoffRoute registers the redemption endpoint.
//
// Unauthenticated by necessity — its whole job is to be reachable by a browser
// that has nothing yet. What it hands out is the ordinary signed session cookie
// from auth.go, so this adds no new kind of trust: it is a second way to prove
// the same one thing, and the proof is a 256-bit value that only this process
// mints, only on an explicit call, and only accepts once.
//
// A nil store means "nothing here ever mints tokens" — serve mode — and every
// arrival is rejected.
func handoffRoute(mux *http.ServeMux, c *credentials, s *handoffStore) {
	mux.HandleFunc(handoffPath, func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		if s != nil && s.redeem(r.URL.Query().Get("t"), now) {
			expiry := now.Add(sessionLifetime)
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookie,
				Value:    issueSession(c.SessionKey, expiry),
				Path:     "/",
				Expires:  expiry,
				HttpOnly: true,
				Secure:   isHTTPS(r),
				SameSite: http.SameSiteLaxMode,
			})
		}
		// Redirect either way. On success it gets the token out of the address
		// bar before the user is looking at the page — out of the history entry
		// they will actually keep, and out of the next screenshot. On failure
		// it lands them exactly where a stranger lands, which is the login form.
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
}

// companionServer is the on-demand HTTP server and the pieces that belong to it.
type companionServer struct {
	srv     *http.Server
	hub     *eventHub
	handoff *handoffStore
	origin  string
	done    chan struct{}
}

// OpenInBrowser starts the companion server if it is not already running and
// returns a URL that signs the browser in and lands it on the app.
//
// Bound, so the frontend can call it; denied over RPC (see rpcDenied), because
// a caller that is already in a browser has nothing to gain from it and should
// not be able to open listeners.
func (a *App) OpenInBrowser() (string, error) {
	c, err := a.ensureCompanion()
	if err != nil {
		return "", err
	}
	// A new token per click, not one URL kept around. The idempotent part is
	// the server and its port — clicking twice does not bind a second listener
	// and the origin never moves — but a token that could be handed out twice
	// would not be single-use, which is the property the whole handoff rests on.
	tok := c.handoff.mint(time.Now())
	return c.origin + handoffPath + "?t=" + url.QueryEscape(tok), nil
}

func (a *App) ensureCompanion() (*companionServer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.companion != nil {
		return a.companion, nil
	}

	creds, err := loadOrCreateCredentials()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	hub := newEventHub()
	handoff := newHandoffStore()
	mux, err := newAPIMux(a, hub, creds, handoff)
	if err != nil {
		return nil, fmt.Errorf("assets: %w", err)
	}
	ln, err := listenLocal(companionPort)
	if err != nil {
		return nil, err
	}

	c := &companionServer{
		srv:     &http.Server{Handler: requireAuth(creds, mux)},
		hub:     hub,
		handoff: handoff,
		origin:  "http://" + ln.Addr().String(),
		done:    make(chan struct{}),
	}
	// Published before anything can connect, so a tab that opens fast cannot
	// miss events from a turn the window is already streaming.
	a.companionHub.Store(hub)
	a.companion = c
	go func() {
		defer close(c.done)
		if err := c.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("open-in-browser: %v", err)
		}
	}()
	return c, nil
}

// listenLocal binds 127.0.0.1 and nothing else, for the reason serve.go spells
// out: this is an agent with shell tools, and loopback is the only place one
// belongs without a deliberate decision in front of it.
func listenLocal(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err == nil {
		return ln, nil
	}
	// Preferred port is taken — a second copy of the app, a `serve` process,
	// something unrelated. Take whatever the OS offers rather than failing: the
	// caller is handed the real address back and nothing downstream assumed
	// 43119 in the first place.
	return net.Listen("tcp", "127.0.0.1:0")
}

// stopCompanion shuts the server down and takes its hub back out of the emit
// path. Safe to call when nothing is running.
func (a *App) stopCompanion() {
	a.mu.Lock()
	c := a.companion
	a.companion = nil
	a.mu.Unlock()
	if c == nil {
		return
	}
	a.companionHub.Store(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.srv.Shutdown(ctx); err != nil {
		// A graceful shutdown waits for open requests, and an SSE stream is an
		// open request that never finishes on its own. Once the clock runs out,
		// cut them.
		_ = c.srv.Close()
	}
	<-c.done
}
