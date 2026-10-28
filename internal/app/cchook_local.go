package app

// The machine's side of the Claude Code relay: the socket `superleo hook`
// talks to, what this machine knows about its terminal sessions, and typing
// an answer from the phone into the right one.
//
// Delivery is anywhere's chain, from agentexec: tmux when the session runs in
// a pane, else the iTerm2 tab that owns its tty — but only after reach says
// that tty is still this session's and still has an agent in it, because
// macOS hands a closed tab's tty to the next tab that opens and typing there
// puts the words in a stranger's session — else parked for the session's next
// Stop hook. Each reply goes to the one session it names; nothing here reads
// the text to decide where it goes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liliang-cn/agentexec/iterm"
	"github.com/liliang-cn/agentexec/reach"
	"github.com/liliang-cn/agentexec/replies"
	"github.com/liliang-cn/agentexec/tmux"
	"github.com/liliang-cn/superleo/internal/backend"
)

// ccHookPath is the one route on the hook socket.
const ccHookPath = "/cc/hook"

// ccSessionEvent is the agent link event a hook becomes on its way to core.
const ccSessionEvent = "cc:session"

// Session states, as the hooks reveal them.
const (
	ccWorking    = "working"    // started, or just handed a reply
	ccFinished   = "finished"   // Stop: the turn ended, it waits for the next prompt
	ccFailed     = "failed"     // StopFailure: an API error ended the turn
	ccPermission = "permission" // stopped at a permission prompt
	ccWaiting    = "waiting"    // idle_prompt: has been waiting on input a while
	ccEnded      = "ended"      // SessionEnd: Claude Code exited
)

// maxSightings is how many terminal reports reach decides ownership from.
const maxSightings = 2000

// maxCCReply bounds one answer from the phone.
const maxCCReply = 4000

// ccLocalSession is one Claude Code session on this machine.
type ccLocalSession struct {
	ID    string
	Cwd   string
	TTY   string
	Pane  string
	State string
	Seen  time.Time
}

// ccLocal is this machine's Claude Code sessions and the replies parked for
// them. Its terminal access is functions so a test needs no tmux or iTerm2.
type ccLocal struct {
	mu        sync.Mutex
	sessions  map[string]*ccLocalSession
	sightings []reach.Sighting
	queue     *replies.Queue

	liveTTYs  func() map[string]bool
	paneAlive func(pane string) bool
	typeTmux  func(pane, text string) error
	typeITerm func(ctx context.Context, tty, text string) error
	ottyPane  func(ctx context.Context, sessionID string) string
	typeOtty  func(ctx context.Context, pane, text string) error

	// perms are the permission requests held for core's word, by request id
	// (cchook_perm.go).
	perms map[string]chan ccPermVerdict
}

func newCCLocal() *ccLocal {
	return &ccLocal{
		sessions:  map[string]*ccLocalSession{},
		perms:     map[string]chan ccPermVerdict{},
		queue:     replies.New(),
		liveTTYs:  reach.AgentTTYs,
		paneAlive: func(p string) bool { _, err := tmux.SessionForPane(p); return err == nil },
		typeTmux: func(pane, text string) error {
			if err := tmux.SendText(pane, text); err != nil {
				return err
			}
			return tmux.SendKey(pane, "Enter")
		},
		typeITerm: iterm.SendText,
		ottyPane:  ottyPaneFor,
		typeOtty:  ottyType,
	}
}

var (
	ccLocalOnce sync.Once
	ccLocalV    *ccLocal
)

// ccLocalState is the process's one registry: the socket and the agent link
// both reach it.
func ccLocalState() *ccLocal {
	ccLocalOnce.Do(func() { ccLocalV = newCCLocal() })
	return ccLocalV
}

// observe records one hook event and returns the reply to hand back on a
// Stop, if one is parked. It marks the event resumed when it does: the turn
// goes straight on, so it is not news.
func (l *ccLocal) observe(ev *ccHookEvent, now time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sessions[ev.Session]
	if s == nil {
		s = &ccLocalSession{ID: ev.Session}
		l.sessions[ev.Session] = s
	}
	s.Seen = now
	// superleo-cchook sends only the directory's last element; the full one
	// comes with the session's other events.
	if ev.Cwd != "" && (!ccActivityOnly[ev.Event] || s.Cwd == "") {
		s.Cwd = ev.Cwd
	}
	if ev.TTY != "" {
		s.TTY = ev.TTY
		l.sightings = append(l.sightings, reach.Sighting{SessionID: ev.Session, TTY: ev.TTY})
		if n := len(l.sightings); n > maxSightings {
			l.sightings = append([]reach.Sighting(nil), l.sightings[n-maxSightings:]...)
		}
	}
	if ev.Pane != "" {
		s.Pane = ev.Pane
	}
	reply := ""
	switch ev.Event {
	case "SessionStart":
		s.State = ccWorking
	case "SessionEnd":
		s.State = ccEnded
	case "Stop":
		s.State = ccFinished
		if reply = l.queue.TryTake(ev.Session); reply != "" {
			ev.Resumed = true
			s.State = ccWorking
		}
	case "StopFailure":
		s.State = ccFailed
	case "Notification":
		if ev.Kind == "permission_prompt" {
			s.State = ccPermission
		} else {
			s.State = ccWaiting
		}
	case "PermissionRequest":
		s.State = ccPermission
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		s.State = ccWorking
	}
	// Ended sessions are kept a while so a late answer gets a clear "it
	// ended" rather than "never heard of it", then forgotten.
	for id, old := range l.sessions {
		if old.State == ccEnded && now.Sub(old.Seen) > time.Hour {
			delete(l.sessions, id)
		}
	}
	return reply
}

// CCDelivery is what became of an answer sent to a terminal session.
type CCDelivery struct {
	// Via is how it went: "tmux", "terminal" (typed into the iTerm2 tab) or
	// "queued" (handed back by the session's next Stop hook).
	Via string `json:"via"`
	// Immediate is true when the agent has it now rather than at a turn end.
	Immediate bool `json:"immediate"`
	// Note says anything worth knowing about it, in words for the person.
	Note string `json:"note,omitempty"`
}

// Errors a reply can meet, said for the person who sent it.
var (
	errCCUnknown = errors.New("this machine has not heard from that Claude Code session since SuperLeo started; it may have ended")
	errCCEnded   = errors.New("that Claude Code session has ended")
)

// deliver types text into the session by the best route that reaches it, or
// says why none does.
func (l *ccLocal) deliver(ctx context.Context, sessionID, text string) (CCDelivery, error) {
	l.mu.Lock()
	s := l.sessions[sessionID]
	var snap ccLocalSession
	if s != nil {
		snap = *s
	}
	sightings := append([]reach.Sighting(nil), l.sightings...)
	l.mu.Unlock()

	switch {
	case s == nil:
		return CCDelivery{}, errCCUnknown
	case snap.State == ccEnded:
		return CCDelivery{}, errCCEnded
	case snap.State == ccPermission:
		// Typing into a permission menu answers the menu: Return picks the
		// highlighted option. Held for the turn's end instead.
		l.queue.Push(sessionID, text)
		return CCDelivery{Via: "queued", Note: "it is waiting at a permission prompt; your answer is handed over when this turn ends"}, nil
	}

	if snap.Pane != "" && l.paneAlive(snap.Pane) {
		err := l.typeTmux(snap.Pane, text)
		if err == nil {
			l.setState(sessionID, ccWorking)
			return CCDelivery{Via: "tmux", Immediate: true}, nil
		}
		log.Printf("cc relay: typing into tmux pane %s: %v", snap.Pane, err)
	}
	if l.ottyPane != nil {
		if pane := l.ottyPane(ctx, sessionID); pane != "" {
			err := l.typeOtty(ctx, pane, text)
			if err == nil {
				l.setState(sessionID, ccWorking)
				return CCDelivery{Via: "otty", Immediate: true}, nil
			}
			log.Printf("cc relay: typing into otty pane %s: %v", pane, err)
			if errors.Is(err, errOttyLocked) && snap.State != ccWorking {
				return CCDelivery{}, err
			}
		}
	}
	at := reach.For(sightings, l.liveTTYs(), sessionID)
	if at.Gone {
		return CCDelivery{}, fmt.Errorf("that Claude Code session is gone: %s", at.Reason)
	}
	if at.TTY != "" {
		err := l.typeITerm(ctx, at.TTY, text)
		if err == nil {
			l.setState(sessionID, ccWorking)
			return CCDelivery{Via: "terminal", Immediate: true}, nil
		}
		log.Printf("cc relay: typing into %s: %v", at.TTY, err)
	}
	if snap.State == ccWorking {
		l.queue.Push(sessionID, text)
		return CCDelivery{Via: "queued", Note: "handed over when its current turn ends"}, nil
	}
	return CCDelivery{}, errors.New("cannot type into that session's terminal (tmux, Otty and iTerm2 can be reached; this one is none of them) and it is not in a turn, so there is no turn end to hand the answer to")
}

func (l *ccLocal) setState(id, state string) {
	l.mu.Lock()
	if s := l.sessions[id]; s != nil {
		s.State = state
	}
	l.mu.Unlock()
}

// ReplyToSession types an answer into a Claude Code session on this machine.
// Core calls it over the agent link for a phone that answered a notice; it
// goes to the one session named, by anywhere's delivery chain.
func (a *App) ReplyToSession(sessionID, text string) (CCDelivery, error) {
	sessionID, text = strings.TrimSpace(sessionID), strings.TrimSpace(text)
	switch {
	case sessionID == "":
		return CCDelivery{}, errors.New("which session?")
	case text == "":
		return CCDelivery{}, errors.New("the answer is empty")
	case utf8.RuneCountInString(text) > maxCCReply:
		return CCDelivery{}, fmt.Errorf("the answer is longer than %d characters", maxCCReply)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d, err := ccLocalState().deliver(ctx, sessionID, text)
	if err != nil {
		log.Printf("cc relay: reply to %s not delivered: %v", sessionID, err)
	} else {
		log.Printf("cc relay: reply to %s delivered via %s (%d chars)", sessionID, d.Via, utf8.RuneCountInString(text))
	}
	return d, err
}

// ccReport is a hook event as it goes up the agent link: the event, plus
// whether anybody is at this machine.
type ccReport struct {
	ccHookEvent
	Host string `json:"host,omitempty"`
	// Idle is how long since this machine last saw keyboard or mouse input, in
	// seconds; -1 where that cannot be told (no HID, not a Mac).
	Idle int `json:"idle"`
	// Away is Idle past ccAwayAfter.
	Away bool `json:"away"`
}

// ccAwayAfter is how long without input means nobody is at the machine.
const ccAwayAfter = 3 * time.Minute

// handleCCHook takes one event from `superleo hook`, answers with any parked
// reply at once, and sends the event on afterwards: the hook is holding a
// terminal and must not wait for the network.
func (a *App) handleCCHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var ev ccHookEvent
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&ev); err != nil || strings.TrimSpace(ev.Session) == "" {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	// Another CLI's plugin or hooks (cli_activity.go): only its activity
	// record moves; the phone's notices and replies are Claude Code's.
	if c := cliName(ev.CLI); c != "" && c != "claude" {
		writeJSON(w, map[string]string{"reply": ""})
		observeCLIActivity(ev)
		return
	}
	reply := ccLocalState().observe(&ev, time.Now())
	writeJSON(w, map[string]string{"reply": reply})
	observeCCActivity(ev)
	if ccActivityOnly[ev.Event] {
		return // it travels only in the activity snapshot, throttled
	}
	go a.forwardCCEvent(ev)
}

// forwardCCEvent sends the event to core over the agent link, or, on a
// SuperLeo that is not linked to one, raises it here.
func (a *App) forwardCCEvent(ev ccHookEvent) {
	host, _ := os.Hostname()
	idle := ccIdleSeconds()
	rep := ccReport{ccHookEvent: ev, Host: strings.Split(host, ".")[0], Idle: idle,
		Away: idle >= 0 && time.Duration(idle)*time.Second >= ccAwayAfter}
	raw, err := json.Marshal(rep)
	if err != nil {
		return
	}
	if tap := a.agentTap.Load(); tap != nil {
		var payload map[string]any
		if json.Unmarshal(raw, &payload) == nil {
			(*tap)(ccSessionEvent, payload)
		}
		return
	}
	a.noteCCSession("", string(raw))
}

// serveCCHookSocket listens for `superleo hook` on a unix socket in the data
// directory until ctx ends. Owner-only: the socket is how a terminal session
// reports, and an answer parked for it comes back the same way.
func (a *App) serveCCHookSocket(ctx context.Context) {
	path := filepath.Join(backend.DataDir(), ccHookSocketName)
	if _, err := os.Stat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			log.Printf("cc relay: another SuperLeo already listens on %s; hooks go there", path)
			return
		}
		_ = os.Remove(path) // left behind by a process that is gone
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("cc relay: %v", err)
		return
	}
	lis, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("cc relay: listening on %s: %v", path, err)
		return
	}
	_ = os.Chmod(path, 0o600)
	mux := http.NewServeMux()
	mux.HandleFunc(ccHookPath, a.handleCCHook)
	mux.HandleFunc(ccPermissionPath, a.handleCCPermission)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = os.Remove(path)
	}()
	a.startCCActivity(ctx)
	a.startCLIActivity(ctx)
	log.Printf("cc relay: hooks on %s", path)
	if err := srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("cc relay: %v", err)
	}
}

// ccIdleSeconds is hidIdleSeconds, a variable so a test can say who is at
// the machine.
var ccIdleSeconds = hidIdleSeconds

var hidIdleRE = regexp.MustCompile(`"HIDIdleTime"\s*=\s*(\d+)`)

// hidIdleSeconds is how long this Mac has had no keyboard or mouse input, or
// -1 when that cannot be told.
func hidIdleSeconds() int {
	if runtime.GOOS != "darwin" {
		return -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem", "-d", "4", "-k", "HIDIdleTime").Output()
	if err != nil {
		return -1
	}
	return parseHIDIdle(string(out))
}

// parseHIDIdle reads ioreg's HIDIdleTime (nanoseconds) as seconds, -1 when
// it is not there.
func parseHIDIdle(out string) int {
	m := hidIdleRE.FindStringSubmatch(out)
	if m == nil {
		return -1
	}
	ns, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return -1
	}
	return int(ns / int64(time.Second))
}
