package backend

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The avatar's sprites, drawn by scripts/gen-avatars.py.
//
// Embedded rather than served from disk: a renderer is pointed at this server
// by URL, and a URL that works only when the binary is run from the right
// directory is a URL that works on the machine it was built on.
//
//go:embed avatars/*.png
var avatarSprites embed.FS

// AvatarEvent is the language/tech-agnostic protocol unit broadcast to external
// 2D/3D renderers (Live2D / VRM / Unity / web) so they can drive a character.
//
// Type:
//   - "state"   — State is one of idle | thinking | working | speaking
//   - "emotion" — Emotion is one of neutral|happy|sad|thinking|excited|...
//   - "speech"  — Text carries the spoken/streamed text
type AvatarEvent struct {
	Type    string `json:"type"`
	State   string `json:"state,omitempty"`
	Emotion string `json:"emotion,omitempty"`
	Text    string `json:"text,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Ts      int64  `json:"ts"`
}

// Avatar state constants.
//
// The first four are what the agent does. The last two are what has happened to
// it, and they were both invisible until now: a turn parked on a tool approval
// looked exactly like one that was thinking, and a turn that died looked
// exactly like one that had finished. Those are the two moments a person most
// needs to be told about, because both of them are waiting on the person.
const (
	AvatarStateIdle     = "idle"
	AvatarStateThinking = "thinking"
	AvatarStateWorking  = "working"
	AvatarStateSpeaking = "speaking"
	AvatarStateWaiting  = "waiting"
	AvatarStateError    = "error"
)

// AvatarDriver receives avatar lifecycle/emotion events.
type AvatarDriver interface {
	Emit(AvatarEvent)
}

// NoopDriver discards all events (used when no avatar server is attached).
type NoopDriver struct{}

// Emit implements AvatarDriver.
func (NoopDriver) Emit(AvatarEvent) {}

// SSEServer is an AvatarDriver backed by a local HTTP server-sent-events
// endpoint. Any external renderer connects to GET /avatar/events and receives
// each AvatarEvent as `data: <json>\n\n`.
type SSEServer struct {
	port int
	web  fs.FS

	srv *http.Server
	ln  net.Listener

	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

// NewSSEServer constructs an SSE avatar server bound (on Start) to
// 127.0.0.1:port.
//
// web is the built frontend (frontend/dist), holding avatar.html and the
// hashed assets it loads. The page is a Vite entry built beside the app rather
// than a string in this file: it renders a reply with the same AIGUI plugins
// the chat window uses, and a second hand-written renderer would have gone on
// printing markdown as asterisks. A nil FS leaves the page unavailable while
// the event stream and the sprites keep working — an external renderer needs
// neither.
func NewSSEServer(port int, web fs.FS) *SSEServer {
	return &SSEServer{
		port:    port,
		web:     web,
		clients: make(map[chan []byte]struct{}),
	}
}

// Start binds the listener and serves the avatar endpoints in a goroutine.
func (s *SSEServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/avatar/events", s.handleEvents)
	mux.HandleFunc("/avatar", s.handlePage)
	mux.HandleFunc("/avatar/sprites/", s.handleSprite)
	// The page's own script and stylesheet. Vite writes them under /assets
	// with hashed names, and the page asks for them at the root, so they are
	// served from the root here too rather than under /avatar.
	if s.web != nil {
		mux.Handle("/assets/", http.FileServer(http.FS(s.web)))
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return err
	}
	s.ln = ln
	s.srv = &http.Server{Handler: mux}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// Close shuts down the server and disconnects all clients.
func (s *SSEServer) Close() error {
	s.mu.Lock()
	for ch := range s.clients {
		delete(s.clients, ch)
		close(ch)
	}
	s.mu.Unlock()
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}

// Emit stamps Ts and fans the event out to every connected client without
// blocking; if a client's buffer is full the event is dropped for that client.
func (s *SSEServer) Emit(ev AvatarEvent) {
	if ev.Ts == 0 {
		ev.Ts = time.Now().UnixMilli()
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.mu.Lock()
	for ch := range s.clients {
		select {
		case ch <- payload:
		default:
			// client buffer full — drop to stay non-blocking
		}
	}
	s.mu.Unlock()
}

func (s *SSEServer) addClient() chan []byte {
	ch := make(chan []byte, 64)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *SSEServer) removeClient(ch chan []byte) {
	s.mu.Lock()
	if _, ok := s.clients[ch]; ok {
		delete(s.clients, ch)
		close(ch)
	}
	s.mu.Unlock()
}

func (s *SSEServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := s.addClient()
	defer s.removeClient(ch)

	// Greet the new client with the current idle state.
	hello, _ := json.Marshal(AvatarEvent{Type: "state", State: AvatarStateIdle, Ts: time.Now().UnixMilli()})
	fmt.Fprintf(w, "data: %s\n\n", hello)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (s *SSEServer) handlePage(w http.ResponseWriter, r *http.Request) {
	if s.web == nil {
		http.Error(w, "avatar page not built into this binary", http.StatusServiceUnavailable)
		return
	}
	page, err := fs.ReadFile(s.web, "avatar.html")
	if err != nil {
		http.Error(w, "avatar page missing from the build", http.StatusServiceUnavailable)
		return
	}
	// Same reason index.html is uncached in the desktop app: each build emits
	// new hashed asset names, and a cached page keeps asking for the old ones.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

// handleSprite serves one character's sheet.
//
// Public and uncached-by-name: the sheets are a kilobyte each and change only
// when the generator is re-run, so a long cache would mostly serve to make a
// regenerated sprite invisible until someone thought to hard-refresh.
func (s *SSEServer) handleSprite(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/avatar/sprites/")
	// Only a bare file name: the path is otherwise a way to read the embedded
	// filesystem, and there is nothing else in it worth the risk of finding out.
	if name == "" || strings.ContainsAny(name, "/\\") || !strings.HasSuffix(name, ".png") {
		http.NotFound(w, r)
		return
	}
	data, err := avatarSprites.ReadFile("avatars/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// AvatarCharacters names the sheets that ship, for a UI that offers a choice.
func AvatarCharacters() []string { return []string{"cat", "bunny", "robot"} }

// AvatarEmotions is the order the sheets' rows are in. A renderer needs it to
// know which row to show, and it is part of the sprite contract.
func AvatarEmotions() []string {
	return []string{
		"neutral", "happy", "sad", "thinking", "excited",
		"sleepy", "confused", "love", "angry", "surprised",
	}
}

// AvatarStates is every state a renderer may be sent.
func AvatarStates() []string {
	return []string{
		AvatarStateIdle, AvatarStateThinking, AvatarStateWorking,
		AvatarStateSpeaking, AvatarStateWaiting, AvatarStateError,
	}
}

// AvatarProxyPrefix is where the avatar server is mounted on the serve mux.
const AvatarProxyPrefix = "/avatar"

// NewAvatarProxy forwards /avatar/* to the avatar server on this host.
//
// The avatar bridge binds 127.0.0.1 and nothing else, which is right for a
// renderer running on the same machine and useless for a phone: served over the
// network, the pet asked its own browser for 127.0.0.1:47615 — the phone
// itself — and got nothing. Its sprites never loaded and its event stream never
// connected, so letting it out did nothing at all.
//
// Proxied here it is same-origin, which also means it inherits the session gate
// rather than exposing a second unauthenticated port to the network.
func NewAvatarProxy(target func() string) http.Handler {
	proxy := &httputil.ReverseProxy{
		// The event stream is the point; buffering it would hold every state
		// change until something else flushed the connection.
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			raw := target()
			if raw == "" {
				return
			}
			u, err := url.Parse(raw)
			if err != nil {
				return
			}
			r.Out.URL.Scheme = u.Scheme
			r.Out.URL.Host = u.Host
			r.Out.Host = u.Host
			// No prefix to strip: the avatar server serves these very paths.
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "the avatar bridge is not running: "+err.Error(), http.StatusServiceUnavailable)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target() == "" {
			http.Error(w, "the avatar bridge is not running", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
