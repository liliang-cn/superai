package backend

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
	mux.HandleFunc("/avatar/sprites/", s.handleSprite)

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

// avatarRefHTML is a tiny reference renderer proving the avatar protocol works:
// a 2D placeholder that reacts to state + emotion + speech events.
const avatarRefHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>SuperAI Avatar (reference)</title>
<style>
  :root { color-scheme: dark; --bg:#0e1116; --fg:#e6edf3; --dim:#8b949e; --line:#30363d; }
  * { box-sizing: border-box; }
  body { margin:0; font-family: system-ui, -apple-system, "PingFang SC", sans-serif;
         background:var(--bg); color:var(--fg); height:100vh;
         display:flex; flex-direction:column; align-items:center; justify-content:center; gap:16px; }

  /* The stage. A ring of light behind the character carries the state, so the
     sprite itself never has to be redrawn to say "working" — the sheet has
     five emotions and no states, and inventing twenty more frames to encode
     four states would be four times the art for something a colour says. */
  #stage { position:relative; width:200px; height:200px; display:grid; place-items:center; }
  #glow { position:absolute; inset:18px; border-radius:50%; background:#30363d; transition:background .3s ease; }
  #stage.thinking #glow { background:#8957e5; }
  #stage.working  #glow { background:#d29922; animation:pulse 1.2s infinite; }
  #stage.speaking #glow { background:#2ea043; }
  #stage.idle     #glow { background:#30363d; }
  /* Waiting is on you, not on the agent, so it does not settle — it keeps
     blinking until someone answers. */
  #stage.waiting  #glow { background:#1f6feb; animation:blink 1s steps(2) infinite; }
  #stage.error    #glow { background:#da3633; }
  @keyframes blink { 50% { opacity:.35; } }
  @keyframes pulse { 0%{box-shadow:0 0 0 0 rgba(210,153,34,.45);} 100%{box-shadow:0 0 0 28px rgba(210,153,34,0);} }

  /* The character. One sprite window onto a sheet that is 4 frames wide and 5
     emotions tall; the animation walks the frames with steps(), so the browser
     runs it and no timer here has to keep up with it. */
  #sprite {
    position:relative; width:120px; height:144px;   /* 20x24 at 6x */
    background-image:url("/avatar/sprites/cat.png");
    background-repeat:no-repeat;
    background-size:960px 1440px;                   /* 160x240 at 6x */
    image-rendering:pixelated;
    animation:stand 1.4s steps(4) infinite;
  }
  /* The sheet is two strips of four: standing, then walking. An animation is a
     window travelling four frame-widths across one of them, and a state picks
     which. steps(4) over exactly four widths lands back where it started. */
  @keyframes stand { from { background-position-x:0; }     to { background-position-x:-480px; } }
  @keyframes walk  { from { background-position-x:-480px; } to { background-position-x:-960px; } }

  /* Working is the one state with somewhere to be. */
  #stage.working #sprite { animation-name:walk; animation-duration:.6s; }
  #stage.speaking #sprite { animation-duration:.8s; }
  #stage.thinking #sprite { animation-duration:1.1s; }
  #stage.waiting #sprite { animation-duration:1.8s; }
  /* An error stops the character where it is: something has gone wrong and a
     cheerful breathing loop under a red light is the wrong thing to watch. */
  #stage.error #sprite { animation-play-state:paused; }

  .tags { display:flex; gap:8px; }
  .tag { padding:3px 11px; border-radius:999px; background:#161b22; border:1px solid var(--line); font-size:13px; }
  #speech { max-width:520px; min-height:24px; text-align:center; line-height:1.55; padding:0 20px; }
  #pick { display:flex; gap:6px; }
  #pick button {
    font:inherit; font-size:12px; color:var(--dim); background:#161b22;
    border:1px solid var(--line); border-radius:7px; padding:5px 12px; cursor:pointer;
  }
  #pick button.on { color:var(--fg); border-color:#58a6ff; }
</style>
</head>
<body>
  <div id="stage" class="idle">
    <div id="glow"></div>
    <div id="sprite"></div>
  </div>
  <div class="tags">
    <span class="tag" id="state">state: idle</span>
    <span class="tag" id="emotion">emotion: neutral</span>
  </div>
  <div id="speech"></div>
  <div id="pick"></div>
<script>
  var CHARS = ["cat", "bunny", "robot"];
  // The sheet's rows, in the order it was generated in. A name not in this
  // list leaves the row where it is rather than falling back to row 0: the
  // mood tag is optional and free-form enough that a model will eventually
  // invent one, and snapping the face to neutral every time it does would look
  // like the avatar had broken.
  var ROWS = { neutral:0, happy:1, sad:2, thinking:3, excited:4,
               sleepy:5, confused:6, love:7, angry:8, surprised:9 };

  var stage = document.getElementById("stage");
  var sprite = document.getElementById("sprite");
  var pick = document.getElementById("pick");

  function setChar(name) {
    sprite.style.backgroundImage = 'url("/avatar/sprites/' + name + '.png")';
    try { localStorage.setItem("superai-avatar-char", name); } catch (_) {}
    [].forEach.call(pick.children, function (b) { b.className = b.textContent === name ? "on" : ""; });
  }
  function setEmotion(name) {
    var row = ROWS[name];
    if (row === undefined) return;
    // 144px per row at 6x; negative because the window moves down the sheet.
    sprite.style.backgroundPositionY = (-row * 144) + "px";
  }

  CHARS.forEach(function (name) {
    var b = document.createElement("button");
    b.textContent = name;
    b.onclick = function () { setChar(name); };
    pick.appendChild(b);
  });
  var saved;
  try { saved = localStorage.getItem("superai-avatar-char"); } catch (_) {}
  setChar(CHARS.indexOf(saved) >= 0 ? saved : "cat");

  var es = new EventSource("/avatar/events");
  es.onmessage = function (e) {
    var ev; try { ev = JSON.parse(e.data); } catch (_) { return; }
    if (ev.type === "state") {
      stage.className = ev.state || "idle";
      document.getElementById("state").textContent = "state: " + (ev.state || "idle");
    } else if (ev.type === "emotion") {
      document.getElementById("emotion").textContent = "emotion: " + (ev.emotion || "neutral");
      setEmotion(ev.emotion);
    } else if (ev.type === "speech") {
      document.getElementById("speech").textContent = ev.text || "";
    }
  };
  es.onerror = function () { document.getElementById("state").textContent = "state: disconnected"; };
</script>
</body>
</html>`
