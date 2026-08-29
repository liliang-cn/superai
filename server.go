// Serve mode: the same app without the window.
//
//	superai-desktop serve -port 43117
//
// runs the identical App — same settings, same agent, same schedules out of the
// same database — behind a plain HTTP server instead of a WKWebView, so the
// frontend can be a browser tab on this machine. Three surfaces:
//
//   - POST /api/rpc/<Method>   — every exported App method, args as a JSON
//     array, result as JSON. This is the same contract Wails builds from the
//     same reflection, which is what keeps the two frontends one frontend:
//     the browser shim (frontend/src/lib/webshim.ts) fakes window.go over it.
//   - GET  /api/events         — one SSE stream carrying every event the app
//     would have emitted to the window, as {"name":..., "payload":...}.
//   - everything else          — the embedded frontend/dist.
//
// The listener binds 127.0.0.1 and nothing else. Exposing this beyond the
// machine means exposing an unauthenticated agent with tools; anyone who wants
// that puts an authenticating reverse proxy or an SSH tunnel in front, on
// purpose, rather than being handed a flag that does it by accident.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"
)

// rpcDenied lists bound methods that only make sense with a native window on
// top. They stay bound in the desktop app; over HTTP they answer 404 with the
// reason instead of crashing into a nil Wails context.
var rpcDenied = map[string]string{
	"PickFiles":           "needs a native file dialog; not available in the browser",
	"ExportWorkspaceFile": "needs a native save dialog; use ReadWorkspaceFileDataURL and download instead",
	// Whoever is calling this over HTTP is already in a browser, so the button
	// has nothing to offer them — and opening listeners is not something a
	// remote caller should be able to ask for on the strength of one session.
	"OpenInBrowser": "you are already in a browser",
}

// eventHub fans one emitted event out to every connected SSE client. A slow
// client loses events rather than blocking the emitter: emit is called from
// the middle of chat turns, and a stalled browser tab must not stall the agent.
type eventHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[chan []byte]struct{})}
}

func (h *eventHub) broadcast(name string, payload map[string]any) {
	b, err := json.Marshal(map[string]any{"name": name, "payload": payload})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default: // drop for this subscriber rather than block the run
		}
	}
}

func (h *eventHub) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *eventHub) serveSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// An opening comment makes EventSource fire `open` immediately, so the
	// frontend knows the stream is live before the first real event.
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ch, off := h.subscribe()
	defer off()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-keepalive.C:
			// Comment frames keep idle proxies and browsers from closing us.
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}

// rpcCall dispatches one POST /api/rpc/<Method> onto the App by reflection —
// the same shape Wails derives from the same struct, so the browser gets
// exactly the binding surface the window gets, no hand-maintained route table
// to drift out of sync.
func rpcCall(app *App, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/rpc/")
	if reason, denied := rpcDenied[name]; denied {
		http.Error(w, name+": "+reason, http.StatusNotFound)
		return
	}
	m := reflect.ValueOf(app).MethodByName(name)
	if !m.IsValid() {
		http.Error(w, "no such method: "+name, http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var raw []json.RawMessage
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			http.Error(w, "arguments must be a JSON array: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	mt := m.Type()
	if mt.NumIn() != len(raw) {
		http.Error(w, fmt.Sprintf("%s takes %d argument(s), got %d", name, mt.NumIn(), len(raw)), http.StatusBadRequest)
		return
	}
	args := make([]reflect.Value, len(raw))
	for i := range raw {
		v := reflect.New(mt.In(i))
		if err := json.Unmarshal(raw[i], v.Interface()); err != nil {
			http.Error(w, fmt.Sprintf("argument %d: %v", i, err), http.StatusBadRequest)
			return
		}
		args[i] = v.Elem()
	}

	out := m.Call(args)

	// Wails semantics: a trailing error return rejects the promise; everything
	// before it resolves it. Mirror that as 500-with-message vs 200-with-JSON.
	var result any
	for _, o := range out {
		if err, isErr := o.Interface().(error); isErr {
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			continue
		}
		result = o.Interface()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// spaHandler serves the embedded frontend build, falling back to index.html
// for any path that is not a real file — the frontend is a single page.
func spaHandler() (http.Handler, error) {
	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := dist.Open(p); err == nil {
				f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	}), nil
}

func newAPIMux(app *App, hub *eventHub, creds *credentials, handoff *handoffStore) (*http.ServeMux, error) {
	mux := http.NewServeMux()
	// Sign-in lives on the mux rather than in the middleware so it is a route
	// like any other; requireAuth lets exactly these four through. See auth.go.
	authRoutes(mux, creds)
	// The desktop app's "Open in Browser" hands the tab it opens a one-shot
	// token here, so nobody is asked for a password they never chose. nil in
	// serve mode: nothing there mints tokens, so every arrival is turned away.
	handoffRoute(mux, creds, handoff)
	mux.HandleFunc("/api/rpc/", func(w http.ResponseWriter, r *http.Request) { rpcCall(app, w, r) })
	mux.HandleFunc("/api/events", hub.serveSSE)
	// SuperAI as an MCP server — see mcp.go for why it lives in this process
	// and why it adds no gate of its own.
	mux.Handle(mcpPath, newMCPHandler(app, mcpServerVersion))
	spa, err := spaHandler()
	if err != nil {
		return nil, err
	}
	mux.Handle("/", noCache(spa))
	return mux, nil
}

// serveMain is the `superai-desktop serve` entry point.
func serveMain(argv []string) {
	fl := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fl.Int("port", 43117, "listen on 127.0.0.1:<port>")
	_ = fl.Parse(argv)

	log.SetPrefix("superai-serve ")

	hub := newEventHub()
	app := NewApp()
	app.emitFn = hub.broadcast // before startup: no window may ever see an event
	app.startupHeadless()
	defer app.shutdown(context.Background())

	// Serve mode is the desktop app with the window replaced by a socket, and
	// the socket has no idea who is on the other end. See auth.go.
	creds, err := loadOrCreateCredentials()
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	mux, err := newAPIMux(app, hub, creds, nil)
	if err != nil {
		log.Fatalf("assets: %v", err)
	}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(*port))
	srv := &http.Server{Addr: addr, Handler: requireAuth(creds, mux)}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("serving on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
	fmt.Fprintln(os.Stderr, "superai-serve: stopped")
}
