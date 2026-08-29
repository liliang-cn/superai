package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/mcp"
	"github.com/liliang-cn/superai-desktop/backend"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound application object. Its exported methods are callable
// from the React frontend.
type App struct {
	ctx context.Context

	mu       sync.Mutex
	svc      *backend.Service
	settings *backend.Settings
	avatar   backend.AvatarDriver
	sse      *backend.SSEServer
	proxy    *backend.CLIProxy

	// loginCh is non-nil while a provider login is waiting for the user; it
	// carries the manually pasted callback URL.
	loginCh chan string

	// runs holds the chat turns currently in flight, keyed by the request id
	// every one of their events is tagged with. It has its own lock rather than
	// sharing a.mu: a.mu is held across a rebuild (which constructs a whole
	// Service), and "stop" must not queue behind that — the moment it is worth
	// pressing is exactly the moment the app is busy.
	runMu sync.Mutex
	runs  map[string]*chatRun

	// scheduler drives prompts that run on a clock. It is rebuilt with the
	// service, since a run is an agent turn against the current settings.
	scheduler    *agent.PromptScheduler
	schedulerErr string
	// scheduleLock is held while this process owns firing schedules; nil means
	// another process (the daemon) owns them and this one only manages.
	scheduleLock *backend.ScheduleLock
	// notifyOK records whether the user granted notification permission, asked
	// for once at startup rather than when a timer fires and nobody is looking.
	notifyOK bool

	// approvals holds the tool calls currently parked in front of the user.
	// Its own lock, like runs above and for the same reason: an answer must
	// not queue behind a rebuild, and the agent goroutine waiting on one of
	// these is holding up a turn.
	approvalMu sync.Mutex
	approvals  map[string]*pendingApproval

	// emitFn is an extra sink for frontend events. Tests set it, and serve mode
	// points it at the SSE hub, where it is the only sink there is: the Wails
	// runtime calls log.Fatalf when EventsEmit is handed a context it did not
	// create, so any path without a real window needs somewhere else to send
	// events. It does not stand in for the window — emit fans out to every
	// surface that is attached, and in the desktop app that includes both.
	emitFn func(name string, payload map[string]any)

	// graphs holds the live 3D view of the knowledge graph. It has its own lock
	// and is deliberately not started at boot: it is a loopback listener plus a
	// poller reading the whole brain every couple of seconds, and most sessions
	// never open that page. See GraphView.
	graphs backend.GraphViews

	// companion is the HTTP server behind "Open in Browser": this same app,
	// reachable from a real browser tab, started on demand. Guarded by a.mu.
	companion *companionServer
	// companionHub is that server's SSE fan-out, held separately and atomically
	// rather than reached through a.companion. emit reads it on every event,
	// from whatever goroutine a chat turn happens to be on, and a.mu is held
	// across a whole service rebuild — an event must never queue behind that.
	companionHub atomic.Pointer[eventHub]

	buildErr string
	proxyErr string
}

// NewApp creates a new App.
func NewApp() *App {
	return &App{avatar: backend.NoopDriver{}}
}

// startup loads settings, starts the avatar SSE server, and builds the backend
// Service. A build failure does not crash the app — it is surfaced via
// GetStatus so the Settings page can fix the config and rebuild.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.boot()
}

// startupHeadless boots the same backend with no Wails context: events go
// wherever emitFn points, and everything that needs a native window — dialogs,
// the notification permission prompt — stays off behind the existing nil-ctx
// guards. Serve mode must set emitFn before calling this.
func (a *App) startupHeadless() {
	a.boot()
}

func (a *App) boot() {
	s, _ := backend.LoadSettings() // returns defaults even on error
	a.settings = s

	sse := backend.NewSSEServer(s.AvatarPort)
	if err := sse.Start(); err != nil {
		a.avatar = backend.NoopDriver{}
	} else {
		a.sse = sse
		a.avatar = sse
	}

	a.syncProxy()
	a.rebuild()

	// Permission is requested at startup, not when a timer fires: by then the
	// user is typically not at the machine, which is the whole point of a
	// schedule.
	a.initNotifications()
	a.startScheduler()
}

// syncProxy brings the embedded CLIProxyAPI in line with the current settings:
// started when enabled (and restarted when the port changes), stopped otherwise.
// A failure here is non-fatal — it is reported through GetStatus and the agent
// falls back to the configured LLM endpoint.
func (a *App) syncProxy() {
	a.mu.Lock()
	defer a.mu.Unlock()

	want := a.settings != nil && a.settings.CLIProxyEnabled
	if !want {
		if a.proxy != nil {
			_ = a.proxy.Close()
			a.proxy = nil
		}
		a.proxyErr = ""
		return
	}
	if a.proxy != nil && a.proxy.Port() == a.settings.CLIProxyPort {
		return
	}
	if a.proxy != nil {
		_ = a.proxy.Close()
		a.proxy = nil
	}

	p, err := backend.StartCLIProxy(a.settings.CLIProxyPort)
	if err != nil {
		a.proxyErr = err.Error()
		return
	}
	a.proxyErr = ""
	a.proxy = p
}

// shutdown releases backend resources.
func (a *App) shutdown(ctx context.Context) {
	// Before the lock: stopping the cron loop must not race the service it runs
	// its turns against.
	a.stopScheduler()
	// Also before the lock, and both for the same reason as the scheduler: each
	// takes a lock that is not a.mu — the view waits for its HTTP server to
	// drain, and stopCompanion takes a.mu itself to unhook the server. An open
	// browser tab must not outlive the app it is a window onto.
	_ = a.graphs.Close()
	a.stopCompanion()

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.svc != nil {
		_ = a.svc.Close()
		a.svc = nil
	}
	if a.sse != nil {
		_ = a.sse.Close()
		a.sse = nil
	}
	if a.proxy != nil {
		_ = a.proxy.Close()
		a.proxy = nil
	}
}

// rebuild (re)constructs the backend Service from current settings.
func (a *App) rebuild() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.svc != nil {
		_ = a.svc.Close()
		a.svc = nil
	}
	svc, err := backend.NewService(a.effective())
	if err != nil {
		a.buildErr = err.Error()
		return
	}
	a.buildErr = ""
	a.svc = svc
	// The service is built with its approval gate already installed but with
	// nobody to ask; this is where the app volunteers. Done on every rebuild
	// because SaveSettings constructs a whole new Service, and a gate that
	// lost its approver would start denying every shell command with "no way
	// to ask" while the window is sitting right there.
	svc.ToolGate().SetApprover(a.askToolApproval)
}

// restartScheduler rebinds the timers to the current service. Callers must not
// hold a.mu.
func (a *App) restartScheduler() {
	a.startScheduler()
}

// effective returns the settings the backend Service is actually built from:
// the user's settings, with the brain redirected at the embedded CLIProxyAPI
// when it is running. Callers must hold a.mu.
func (a *App) effective() *backend.Settings {
	if a.settings == nil {
		return nil
	}
	s := *a.settings
	if a.proxy != nil {
		s.LLMBaseURL = a.proxy.BaseURL()
		s.LLMKey = a.proxy.Key()
	}
	return &s
}

// GetSettings returns the current settings.
func (a *App) GetSettings() backend.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.settings == nil {
		return backend.Settings{}
	}
	return *a.settings
}

// SaveSettings persists new settings and rebuilds the Service.
func (a *App) SaveSettings(s backend.Settings) error {
	if err := s.Save(); err != nil {
		return err
	}
	a.mu.Lock()
	a.settings = &s
	a.mu.Unlock()
	a.syncProxy()
	a.rebuild()
	// The scheduler runs turns against the service that was just replaced.
	a.restartScheduler()
	return nil
}

// CLIProxyStatus reports the embedded CLIProxyAPI state for the Settings page.
func (a *App) CLIProxyStatus() map[string]any {
	a.mu.Lock()
	p := a.proxy
	perr := a.proxyErr
	a.mu.Unlock()

	st := map[string]any{
		"running": p != nil,
		"error":   perr,
		"baseURL": p.BaseURL(),
		"authDir": p.AuthDir(),
		"config":  p.ConfigPath(),
		"models":  []string{},
	}
	if p != nil {
		models, err := p.Models(context.Background())
		if err != nil {
			st["error"] = err.Error()
		} else {
			st["models"] = models
		}
	}
	return st
}

// ensureProxy returns the running proxy, starting it if needed. Login and the
// status panel both need it before the user has saved anything, so it must not
// depend on the persisted enable flag.
func (a *App) ensureProxy() (*backend.CLIProxy, error) {
	a.mu.Lock()
	if a.proxy != nil {
		p := a.proxy
		a.mu.Unlock()
		return p, nil
	}
	port := backend.DefaultCLIProxyPort
	if a.settings != nil && a.settings.CLIProxyPort > 0 {
		port = a.settings.CLIProxyPort
	}
	a.mu.Unlock()

	p, err := backend.StartCLIProxy(port)

	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.proxyErr = err.Error()
		return nil, err
	}
	// Another goroutine may have won the race; keep the first one.
	if a.proxy != nil {
		go p.Close()
		return a.proxy, nil
	}
	a.proxyErr = ""
	a.proxy = p
	return p, nil
}

// SetUIRules receives the transcript's rendering rules from the frontend (which
// owns the AIGUI registry and plugin list) and rebuilds the agent only when they
// actually changed, so a restart with the same UI is not paying for a rebuild.
func (a *App) SetUIRules(rules string) string {
	changed, err := backend.SaveUIRules(rules)
	if err != nil {
		return err.Error()
	}
	if changed {
		a.rebuild()
		// rebuild() closes the old agent.Service, and a closed Service owns a
		// closed store. The scheduler holds the Service it was started with, so
		// leaving it bound to the old one means every scheduled run from here on
		// writes its history nowhere — silently, until agent-go v3.5.x started
		// refusing to run on a closed Service. SaveSettings has always paired the
		// two; this path was the one that forgot.
		a.restartScheduler()
	}
	return "ok"
}

// CLIProxyProviders lists the providers the user can log into.
func (a *App) CLIProxyProviders() []backend.CLIProxyProvider {
	return backend.CLIProxyProviders()
}

// CLIProxyLogin runs a provider OAuth flow for the embedded proxy. It returns
// immediately; progress is delivered as "cliproxy:login" events with a status of
// started / prompt / done / error. On success the credential lands in the auth
// dir and the proxy hot-loads it, so the new models appear without a restart.
//
// Only one login may be in flight at a time.
func (a *App) CLIProxyLogin(provider, projectID string) string {
	a.mu.Lock()
	busy := a.loginCh != nil
	a.mu.Unlock()

	// Logging in only needs the proxy's config and auth dir, so start it on
	// demand rather than making the user save settings first.
	p, err := a.ensureProxy()
	if err != nil {
		a.emitLogin("error", provider, "could not start the CLI proxy: "+err.Error())
		return "error"
	}
	if busy {
		a.emitLogin("error", provider, "another login is already in progress")
		return "error"
	}

	// Buffered so a late submission never blocks the frontend call.
	ch := make(chan string, 1)
	a.mu.Lock()
	a.loginCh = ch
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.loginCh = nil
			a.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()

		a.emitLogin("started", provider, "Opening your browser to sign in…")

		prompt := func(text string) (string, error) {
			a.emitLogin("prompt", provider, text)
			select {
			case v := <-ch:
				return v, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		saved, err := p.Login(ctx, provider, projectID, prompt)
		if err != nil {
			a.emitLogin("error", provider, err.Error())
			return
		}
		_ = saved // the path is an implementation detail; the UI just needs the outcome
		a.emitLogin("done", provider, "Signed in.")
	}()

	return "ok"
}

// CLIProxyAccounts lists the signed-in provider credentials (no tokens).
func (a *App) CLIProxyAccounts() []backend.CLIProxyAccount {
	a.mu.Lock()
	p := a.proxy
	a.mu.Unlock()
	if p == nil {
		return []backend.CLIProxyAccount{}
	}
	accounts, err := p.Accounts()
	if err != nil {
		return []backend.CLIProxyAccount{}
	}
	return accounts
}

// CLIProxySetAccountEnabled enables or disables one credential without deleting
// it — the way to park an account or switch which one serves a shared model.
func (a *App) CLIProxySetAccountEnabled(file string, enabled bool) string {
	a.mu.Lock()
	p := a.proxy
	a.mu.Unlock()
	if p == nil {
		return "CLI proxy is not running"
	}
	if err := p.SetAccountDisabled(file, !enabled); err != nil {
		return err.Error()
	}
	return "ok"
}

// CLIProxySignOut deletes a credential, signing that provider account out.
func (a *App) CLIProxySignOut(file string) string {
	a.mu.Lock()
	p := a.proxy
	a.mu.Unlock()
	if p == nil {
		return "CLI proxy is not running"
	}
	if err := p.RemoveAccount(file); err != nil {
		return err.Error()
	}
	return "ok"
}

// CLIProxySubmitPrompt answers the pending login prompt (the pasted callback URL).
func (a *App) CLIProxySubmitPrompt(value string) string {
	a.mu.Lock()
	ch := a.loginCh
	a.mu.Unlock()
	if ch == nil {
		return "no login in progress"
	}
	select {
	case ch <- value:
		return "ok"
	default:
		return "not waiting for input"
	}
}

// windowEmit is the Wails leg of emit, held in a variable only so a test can
// stand in for a window it has no way to create: the real runtime calls
// log.Fatalf if it is handed a context it did not make, so the fan-out below
// cannot otherwise be checked at all.
var windowEmit = func(ctx context.Context, name string, payload map[string]any) {
	runtime.EventsEmit(ctx, name, payload)
}

// emit delivers one payload to every surface currently attached to this app.
//
// Every surface, not the first one found. This used to return after emitFn, on
// the assumption that a process has either a window or a socket. "Open in
// Browser" breaks that assumption: the desktop app keeps its window and gains a
// companion server, and the same chat has to stream into both at once. An
// early return there is silent — nothing errors, the window simply stops
// updating the moment a browser tab exists.
//
// Each leg carries its own guard for the case where it is not attached. Before
// startup there is no window, so the Wails call is skipped rather than made
// against a nil context (which the Wails runtime treats as fatal).
func (a *App) emit(name string, payload map[string]any) {
	if a.emitFn != nil {
		a.emitFn(name, payload)
	}
	if hub := a.companionHub.Load(); hub != nil {
		hub.broadcast(name, payload)
	}
	if a.ctx != nil {
		windowEmit(a.ctx, name, payload)
	}
}

func (a *App) emitLogin(status, provider, message string) {
	a.emit("cliproxy:login", map[string]any{
		"status":   status,
		"provider": provider,
		"message":  message,
	})
}

// GetStatus reports backend readiness for the frontend.
func (a *App) GetStatus() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := map[string]any{
		"ready":          a.svc != nil,
		"error":          a.buildErr,
		"skills":         []string{},
		"memoryMode":     "",
		"browser":        false,
		"avatarPort":     0,
		"cliproxy":       a.proxy != nil,
		"cliproxyError":  a.proxyErr,
		"scheduler":      a.scheduler != nil,
		"schedulerError": a.schedulerErr,
		"notifications":  a.notifyOK,
	}
	if a.settings != nil {
		st["avatarPort"] = a.settings.AvatarPort
	}
	if a.svc != nil {
		st["skills"] = a.svc.InstalledSkills()
		st["memoryMode"] = a.svc.MemoryMode
		st["browser"] = a.svc.HasBrowser()
	}
	return st
}

// GraphView starts the live 3D view of the knowledge graph and reports where it
// is: a URL on this machine's loopback serving the brain SuperAI actually uses,
// updating itself as the graph changes.
//
// Started here rather than at boot. The view is a listener and a poller that
// reads the whole graph every couple of seconds, which is a fair price for a
// page someone is looking at and a poor one for every session that never opens
// it. Once started it stays up for the life of the process — reopening the page
// returns the same URL instead of binding another port.
//
// A failure comes back inside the payload rather than as an error, because the
// reason is what the page has to show: "the shared brain is not answering" and
// "there is nothing in the local one yet" are opposite problems and the user
// can only act on the difference.
func (a *App) GraphView() map[string]any {
	// The settings are read under a.mu and the view is started without it:
	// starting one reads the whole brain first, and against a shared brain that
	// is a network call. Holding the lock that a settings save also takes would
	// mean a slow or unreachable server freezes the Settings page too.
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()

	ctx := a.ctx
	if ctx == nil {
		// Serve mode and tests have no Wails context. The view's lifetime is
		// owned by a.graphs.Close either way, so the parent context only has to
		// be a valid one.
		ctx = context.Background()
	}
	return a.graphs.GraphStatus(ctx, s).Map()
}

// SendChat streams a chat turn. Streaming output is delivered via Wails events
// ("chat:event", "chat:done", "chat:error", "chat:cancelled"); avatar
// lifecycle/emotion events are pushed through the avatar driver.
//
// Exactly one of chat:done / chat:error / chat:cancelled ends a turn.
//
// The return value is the request id every one of those events is tagged with
// under "requestId" — one conversation may have several asks in flight, and
// without the tag the second ask's stream lands in the first one's bubble. An
// empty string means nothing was started (the error event carries the reason).
func (a *App) SendChat(sessionID, message string, imagePaths []string) string {
	requestID := uuid.NewString()

	a.mu.Lock()
	svc := a.svc
	driver := a.avatar
	buildErr := a.buildErr
	a.mu.Unlock()

	if svc == nil {
		// The id is returned even though nothing was started. Withholding it
		// (returning "") orphans the error event below: the caller cannot bind
		// an id it never received, so the reason the backend is unready — the
		// build error — becomes unreachable and the UI can only show a generic
		// message. Handing the id back costs nothing and lets the failure be
		// reported against the ask that caused it.
		a.emit("chat:error", map[string]any{
			"requestId": requestID,
			"error":     "backend not ready: " + buildErr,
		})
		return requestID
	}

	// Registered here rather than inside the goroutine: SendChat returns the id
	// to the frontend, and a stop pressed before the goroutine got scheduled
	// would otherwise be told the run does not exist.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	a.trackRun(requestID, cancel)

	go func() {
		defer cancel()
		defer a.untrackRun(requestID)

		driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateThinking})

		final, err := svc.Stream(ctx, sessionID, message, imagePaths, func(ev *agent.Event) {
			// Every type is forwarded, unfiltered: "thinking" and "state_update"
			// are the only progress the UI has while PTC writes its code.
			a.emit("chat:event", map[string]any{
				"requestId": requestID,
				"type":      string(ev.Type),
				"content":   ev.Content,
				"tool":      ev.ToolName,
				"args":      ev.ToolArgs,
				"result":    ev.ToolResult,
				"debugType": ev.DebugType,
			})
			switch ev.Type {
			case agent.EventTypeToolCall:
				driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateWorking, Tool: ev.ToolName})
			case agent.EventTypePartial:
				if strings.TrimSpace(ev.Content) != "" {
					driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateSpeaking})
				}
			}
		})

		// A stopped turn is not a failed one. Reported as its own event so the
		// transcript can say "you stopped this" and keep whatever was already
		// streamed, instead of painting a red error over a half-written answer.
		//
		// ctx.Err() is in the test as well as the stop flag: the ten-minute
		// deadline above ends the turn the same way the button does, and the
		// agent loop reports both as an ordinary workflow_cancelled event with
		// a nil error — so without it, a timed-out turn would settle as a
		// successful answer with nothing in it.
		if a.runCancelled(requestID) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateIdle})
			partial, _ := backend.SplitEmotion(final)
			a.emit("chat:cancelled", map[string]any{"requestId": requestID, "final": partial})
			return
		}

		if err != nil {
			driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateIdle})
			a.emit("chat:error", map[string]any{"requestId": requestID, "error": err.Error()})
			return
		}

		reply, emotion := backend.SplitEmotion(final)
		if emotion != "" {
			driver.Emit(backend.AvatarEvent{Type: "emotion", Emotion: emotion})
		}
		if strings.TrimSpace(reply) != "" {
			driver.Emit(backend.AvatarEvent{Type: "speech", Text: reply})
		}
		driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateIdle})
		a.emit("chat:done", map[string]any{"requestId": requestID, "final": reply, "emotion": emotion})
	}()

	return requestID
}

// chatRun is one streaming turn, as far as stopping it is concerned.
//
// cancelled is remembered rather than inferred from the error, because there is
// nothing reliable to infer it from: a cancelled turn does not fail. The agent
// loop notices its context is gone, emits "Execution cancelled" as an ordinary
// event and closes the stream, so Service.Stream returns a nil error exactly as
// it does for a turn that finished. Only the side that pulled the plug knows.
type chatRun struct {
	cancel    context.CancelFunc
	cancelled bool
}

// trackRun registers an in-flight turn so CancelChat can find it.
func (a *App) trackRun(requestID string, cancel context.CancelFunc) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	if a.runs == nil {
		a.runs = make(map[string]*chatRun)
	}
	a.runs[requestID] = &chatRun{cancel: cancel}
}

// untrackRun forgets a finished turn. Every goroutine that calls trackRun must
// defer this, or the map grows for the lifetime of the process and a request id
// that is long over still answers "ok" to a stop.
func (a *App) untrackRun(requestID string) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	delete(a.runs, requestID)
}

// runCancelled reports whether this turn was stopped by the user. It must be
// read before untrackRun — after that the answer is always false.
func (a *App) runCancelled(requestID string) bool {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	r := a.runs[requestID]
	return r != nil && r.cancelled
}

// CancelChat stops one streaming turn. The id is the one SendChat returned.
//
// The reply is "ok" when a turn was actually stopped, and a sentence explaining
// why not otherwise — a stop that lands after the answer did is not an error,
// and the caller needs to be able to tell the two apart.
func (a *App) CancelChat(requestID string) string {
	a.runMu.Lock()
	r := a.runs[requestID]
	if r != nil {
		r.cancelled = true
	}
	a.runMu.Unlock()

	if r == nil {
		return "that request is not running — it has already finished, or the id is unknown"
	}
	// Outside the lock: cancel runs the context's own callbacks, and nothing is
	// gained by holding up a second stop while they do.
	r.cancel()
	return "ok"
}

// CancelAllChats stops every streaming turn — the "stop everything" the user
// wants when several asks are in flight and all of them are going nowhere.
func (a *App) CancelAllChats() string {
	a.runMu.Lock()
	pending := make([]context.CancelFunc, 0, len(a.runs))
	for _, r := range a.runs {
		r.cancelled = true
		pending = append(pending, r.cancel)
	}
	a.runMu.Unlock()

	if len(pending) == 0 {
		return "nothing is running"
	}
	for _, cancel := range pending {
		cancel()
	}
	return fmt.Sprintf("ok: stopped %d", len(pending))
}

// Deliverables returns the files a conversation produced. An empty session id
// returns everything in the workspace.
func (a *App) Deliverables(sessionID string) []agent.Deliverable {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	d, err := svc.Deliverables(context.Background(), sessionID)
	if err != nil {
		return nil
	}
	return d
}

// ReadWorkspaceFile reads a workspace-relative file (empty string on error).
func (a *App) ReadWorkspaceFile(path string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return ""
	}
	out, err := svc.ReadWorkspaceFile(path)
	if err != nil {
		return ""
	}
	return out
}

// ChatSessions lists past conversations, most recent first.
func (a *App) ChatSessions() []backend.ChatSessionInfo {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return []backend.ChatSessionInfo{}
	}
	return svc.Sessions(200)
}

// ChatHistory returns a past conversation's visible messages so the transcript
// can be restored.
func (a *App) ChatHistory(sessionID string) []backend.ChatTurn {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return []backend.ChatTurn{}
	}
	return svc.SessionTurns(sessionID)
}

// SearchMCPServers finds installable MCP servers in the official registry, so
// the panel can offer the same catalogue the agent searches.
func (a *App) SearchMCPServers(query string) map[string]any {
	candidates, err := backend.SearchMCPServers(context.Background(), query, 20)
	if err != nil {
		return map[string]any{"error": err.Error(), "servers": []backend.MCPCandidate{}}
	}
	return map[string]any{"error": "", "servers": candidates}
}

// SearchSkills finds skills available on this machine.
func (a *App) SearchSkills(query string) []backend.SkillCandidate {
	return backend.SearchSkills(query, 50)
}

// InstallMCPServer adds a server from the registry and starts it.
func (a *App) InstallMCPServer(name, command string, args []string, env map[string]string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return "backend not ready"
	}
	if err := svc.InstallMCPServer(context.Background(), name, command, args, env); err != nil {
		return err.Error()
	}
	return "ok"
}

// RemoveMCPServer forgets a server, so it is gone on the next launch too.
func (a *App) RemoveMCPServer(name string) string {
	if err := backend.RemoveMCPServer(name); err != nil {
		return err.Error()
	}
	return "ok"
}

// InstallSkill copies a skill found on this machine into SuperAI.
func (a *App) InstallSkill(name, sourcePath string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return "backend not ready"
	}
	if err := svc.InstallSkillFromPath(context.Background(), name, sourcePath); err != nil {
		return err.Error()
	}
	return "ok"
}

// RemoveSkill uninstalls a skill.
func (a *App) RemoveSkill(name string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if err := backend.RemoveSkill(name); err != nil {
		return err.Error()
	}
	if svc != nil {
		svc.ReloadSkills(context.Background())
	}
	return "ok"
}

// DeleteChatSession removes a past conversation permanently.
func (a *App) DeleteChatSession(sessionID string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return "backend not ready"
	}
	if err := svc.DeleteSession(sessionID); err != nil {
		return err.Error()
	}
	return "ok"
}

// ReadWorkspaceFileDataURL returns a workspace file as a data: URL so the
// webview can render PDFs and images natively instead of showing raw bytes.
func (a *App) ReadWorkspaceFileDataURL(path string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return ""
	}
	out, err := svc.ReadWorkspaceFileDataURL(path)
	if err != nil {
		return ""
	}
	return out
}

// OpenWorkspaceFileExternal opens a deliverable with the OS default app — the
// way out for formats the webview cannot preview (Word, Excel, archives).
func (a *App) OpenWorkspaceFileExternal(path string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return "backend not ready"
	}
	if err := svc.OpenWorkspaceFileExternal(path); err != nil {
		return err.Error()
	}
	return "ok"
}

// EmitAvatarTest pushes a test emotion + speech event for the avatar test button.
// Life returns the life-assistant data (schedules/records/persons/reminders) for the Life panel.
func (a *App) Life() backend.LifeData {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return backend.LifeData{Persons: map[string]map[string]any{}}
	}
	return svc.Life()
}

// MemoryRecall queries long-term memory and returns the formatted recall context.
func (a *App) MemoryRecall(query string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return ""
	}
	out, _ := svc.MemoryRecall(context.Background(), query)
	return out
}

// MemorySkills returns the installed skill IDs.
func (a *App) MemorySkills() []string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.InstalledSkills()
}

// Skills returns the installed skills with descriptions for the Skills panel.
func (a *App) Skills() []backend.SkillInfo {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.SkillsDetailed()
}

// MCP returns the configured MCP servers (built-in + user) for the MCP panel.
func (a *App) MCP() []mcp.ServerStatus {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.MCPServers()
}

// ImportCSV imports a CSV file into RAG+KG; returns a JSON report (or an error string).
func (a *App) ImportCSV(path, hint string) string {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return "backend not ready"
	}
	out, err := svc.ImportCSV(context.Background(), path, hint)
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}

// EmitAvatarTest pushes a test emotion + speech event for the avatar test button.
func (a *App) EmitAvatarTest(emotion string) {
	a.mu.Lock()
	driver := a.avatar
	a.mu.Unlock()
	if emotion == "" {
		emotion = "happy"
	}
	driver.Emit(backend.AvatarEvent{Type: "emotion", Emotion: emotion})
	driver.Emit(backend.AvatarEvent{Type: "speech", Text: "这是一条头像测试事件 (" + emotion + ")"})
	driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateSpeaking})
}
