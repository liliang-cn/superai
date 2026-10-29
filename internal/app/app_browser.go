package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/liliang-cn/superleo/internal/backend"
)

// The built-in browser, as the page sees it (backend/browser.go has the rest).
//
// The desktop window runs on the machine the browser does and connects to its
// stream directly. A browser tab elsewhere — the web app, the companion —
// cannot reach a loopback port on this machine, so it goes through
// /api/browser/stream, which this process relays to the same stream behind
// the same sign-in as the rest of /api.

const browserStreamPath = "/api/browser/stream"

// BrowserStatus reports whether there is a browser, whether it is ready, and
// where its live stream is.
func (a *App) BrowserStatus() backend.BrowserStatus {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	st := svc.BrowserStatus()
	// The desktop window cannot use agent-browser's stream as it is: the
	// stream answers 403 to any page not served over http from loopback, and
	// the window's origin is wails://. It goes through a relay of its own on
	// loopback instead (browserLocalRelay). A page elsewhere ignores this URL
	// and uses /api/browser/stream.
	if st.Ready && st.StreamURL != "" {
		if u, err := a.browserLocalRelay(); err == nil {
			st.StreamURL = u
		} else {
			log.Printf("superleo: browser: local relay: %v", err)
		}
	}
	return st
}

// browserLocalRelay serves the stream to the desktop window: loopback only,
// behind a token minted per process, and only to the window's own origin, so
// a web page open in some browser on this machine cannot reach it.
func (a *App) browserLocalRelay() (string, error) {
	a.browserRelayOnce.Do(func() {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			a.browserRelayErr = err
			return
		}
		token := hex.EncodeToString(b[:])
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			a.browserRelayErr = err
			return
		}
		up := websocket.Upgrader{
			ReadBufferSize: 4 << 10, WriteBufferSize: 64 << 10,
			CheckOrigin: func(r *http.Request) bool {
				o, err := url.Parse(r.Header.Get("Origin"))
				return r.Header.Get("Origin") == "" || (err == nil && (o.Scheme == "wails" || o.Hostname() == "wails.localhost"))
			},
		}
		srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			q := r.URL.Query()
			q.Del("t")
			r.URL.RawQuery = q.Encode()
			a.relayBrowserStream(w, r, &up)
		})}
		go func() { _ = srv.Serve(ln) }()
		a.browserRelayURL = fmt.Sprintf("ws://%s/?t=%s", ln.Addr(), token)
	})
	return a.browserRelayURL, a.browserRelayErr
}

// BrowserConnect attaches the browser now, for the panel's connect button:
// in the person's own Chrome it is otherwise attached by the model's first
// call, and a panel opened before that had nothing to show.
func (a *App) BrowserConnect() error {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	return svc.BrowserConnect(context.Background())
}

// SetBrowserMode turns the browser on — in the person's own Chrome
// ("chrome") or one of its own ("own") — or off. It is opt-in, and this is the
// switch the settings page and the browser card both press. Saving rebuilds
// the service, which fetches the binary and mounts it in the background.
func (a *App) SetBrowserMode(mode string) error {
	switch mode {
	case backend.BrowserModeChrome, backend.BrowserModeOwn, backend.BrowserModeOff:
	default:
		return fmt.Errorf("browser mode %q: want chrome, own or off", mode)
	}
	s := a.GetSettings()
	s.BrowserMode = mode
	return a.SaveSettings(s)
}

// The relay checks the origin itself rather than taking gorilla's default:
// requireAuth has already let the request in, and the companion and the web
// app serve the page from this same host, which is what the default demands
// anyway. Spelled out so a later change to either cannot loosen it unseen.
var browserUpgrader = websocket.Upgrader{
	ReadBufferSize:  4 << 10,
	WriteBufferSize: 64 << 10,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // not a browser page: a signed-in program
		}
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host
	},
}

// handleBrowserStream relays the page's WebSocket to agent-browser's stream:
// frames, URL and tab updates one way, mouse and keyboard the other, each
// message passed through unchanged.
func (a *App) handleBrowserStream(w http.ResponseWriter, r *http.Request) {
	a.relayBrowserStream(w, r, &browserUpgrader)
}

// relayBrowserStream passes one page's socket through to the stream. The
// upstream dial carries no Origin: agent-browser refuses every one but a
// loopback http page, and the page is checked here, by upgrader, instead.
func (a *App) relayBrowserStream(w http.ResponseWriter, r *http.Request, upgrader *websocket.Upgrader) {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	addr := svc.BrowserStreamAddr()
	if addr == "" {
		http.Error(w, "no browser is running here", http.StatusServiceUnavailable)
		return
	}
	// The page's own pacing (maxFps, ack) is a query on the URL agent-browser
	// reads, so it is carried across.
	upstreamURL := "ws://" + addr + "/"
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}
	up, _, err := websocket.DefaultDialer.Dial(upstreamURL, nil)
	if err != nil {
		http.Error(w, "browser stream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer up.Close()
	down, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade has answered the request
	}
	defer down.Close()

	var once sync.Once
	done := make(chan struct{})
	stop := func() { once.Do(func() { close(done) }) }
	pipe := func(from, to *websocket.Conn) {
		defer stop()
		for {
			kind, msg, err := from.ReadMessage()
			if err != nil {
				return
			}
			_ = to.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := to.WriteMessage(kind, msg); err != nil {
				return
			}
		}
	}
	go pipe(up, down)
	go pipe(down, up)
	<-done
	// Closing both ends ends whichever pipe is still reading.
	_ = up.Close()
	_ = down.Close()
	log.Printf("superleo: browser stream relay for %s closed", r.RemoteAddr)
}
