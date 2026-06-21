package backend

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

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
const (
	AvatarStateIdle     = "idle"
	AvatarStateThinking = "thinking"
	AvatarStateWorking  = "working"
	AvatarStateSpeaking = "speaking"
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

	srv *http.Server
	ln  net.Listener

	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

// NewSSEServer constructs an SSE avatar server bound (on Start) to
// 127.0.0.1:port.
func NewSSEServer(port int) *SSEServer {
	return &SSEServer{
		port:    port,
		clients: make(map[chan []byte]struct{}),
	}
}

// Start binds the listener and serves the avatar endpoints in a goroutine.
func (s *SSEServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/avatar/events", s.handleEvents)
	mux.HandleFunc("/avatar", s.handlePage)

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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(avatarRefHTML))
}

// avatarRefHTML is a tiny reference renderer proving the avatar protocol works:
// a 2D placeholder that reacts to state + emotion + speech events.
const avatarRefHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>SuperAI Avatar (reference)</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; font-family: system-ui, sans-serif; background:#0e1116; color:#e6edf3;
         height:100vh; display:flex; flex-direction:column; align-items:center; justify-content:center; gap:18px; }
  #face { width:200px; height:200px; border-radius:50%; background:#1f6feb; display:flex;
          align-items:center; justify-content:center; font-size:84px; transition:all .25s ease;
          box-shadow:0 0 0 0 rgba(31,111,235,.5); }
  #face.thinking { background:#8957e5; }
  #face.working  { background:#d29922; animation:pulse 1.2s infinite; }
  #face.speaking { background:#2ea043; }
  #face.idle     { background:#30363d; }
  @keyframes pulse { 0%{box-shadow:0 0 0 0 rgba(210,153,34,.5);} 100%{box-shadow:0 0 0 24px rgba(210,153,34,0);} }
  #state,#emotion { font-size:14px; opacity:.85; }
  #speech { max-width:520px; min-height:24px; text-align:center; line-height:1.5; }
  .tag { padding:2px 10px; border-radius:999px; background:#161b22; border:1px solid #30363d; }
</style>
</head>
<body>
  <div id="face">🙂</div>
  <div><span class="tag" id="state">state: idle</span> <span class="tag" id="emotion">emotion: neutral</span></div>
  <div id="speech"></div>
<script>
  var faces = { neutral:"🙂", happy:"😄", sad:"😢", thinking:"🤔", excited:"🤩",
                "开心":"😄","思考":"🤔","惊讶":"😮","关心":"🥰","抱歉":"🙇","中性":"🙂" };
  var face = document.getElementById("face");
  var es = new EventSource("/avatar/events");
  es.onmessage = function(e){
    var ev; try { ev = JSON.parse(e.data); } catch(_) { return; }
    if (ev.type === "state") {
      face.className = ev.state || "idle";
      document.getElementById("state").textContent = "state: " + (ev.state || "idle");
    } else if (ev.type === "emotion") {
      document.getElementById("emotion").textContent = "emotion: " + (ev.emotion || "neutral");
      if (faces[ev.emotion]) face.textContent = faces[ev.emotion];
    } else if (ev.type === "speech") {
      document.getElementById("speech").textContent = ev.text || "";
    }
  };
  es.onerror = function(){ document.getElementById("state").textContent = "state: disconnected"; };
</script>
</body>
</html>`
