package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v2/pkg/agent"
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

	buildErr string
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

	s, _ := backend.LoadSettings() // returns defaults even on error
	a.settings = s

	sse := backend.NewSSEServer(s.AvatarPort)
	if err := sse.Start(); err != nil {
		a.avatar = backend.NoopDriver{}
	} else {
		a.sse = sse
		a.avatar = sse
	}

	a.rebuild()
}

// shutdown releases backend resources.
func (a *App) shutdown(ctx context.Context) {
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
}

// rebuild (re)constructs the backend Service from current settings.
func (a *App) rebuild() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.svc != nil {
		_ = a.svc.Close()
		a.svc = nil
	}
	svc, err := backend.NewService(a.settings)
	if err != nil {
		a.buildErr = err.Error()
		return
	}
	a.buildErr = ""
	a.svc = svc
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
	a.rebuild()
	return nil
}

// GetStatus reports backend readiness for the frontend.
func (a *App) GetStatus() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := map[string]any{
		"ready":      a.svc != nil,
		"error":      a.buildErr,
		"skills":     []string{},
		"memoryMode": "",
		"browser":    false,
		"avatarPort": 0,
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

// SendChat streams a chat turn. Streaming output is delivered via Wails events
// ("chat:event", "chat:done", "chat:error"); avatar lifecycle/emotion events
// are pushed through the avatar driver. Returns "ok" once the goroutine starts.
func (a *App) SendChat(sessionID, message string) string {
	a.mu.Lock()
	svc := a.svc
	driver := a.avatar
	a.mu.Unlock()

	if svc == nil {
		runtime.EventsEmit(a.ctx, "chat:error", map[string]any{"error": "backend not ready: " + a.buildErr})
		return "error"
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateThinking})

		final, err := svc.Stream(ctx, sessionID, message, func(ev *agent.Event) {
			runtime.EventsEmit(a.ctx, "chat:event", map[string]any{
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

		if err != nil {
			driver.Emit(backend.AvatarEvent{Type: "state", State: backend.AvatarStateIdle})
			runtime.EventsEmit(a.ctx, "chat:error", map[string]any{"error": err.Error()})
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
		runtime.EventsEmit(a.ctx, "chat:done", map[string]any{"final": reply, "emotion": emotion})
	}()

	return "ok"
}

// Deliverables returns the agent's produced artifacts.
func (a *App) Deliverables() []agent.Deliverable {
	a.mu.Lock()
	svc := a.svc
	a.mu.Unlock()
	if svc == nil {
		return nil
	}
	d, err := svc.Deliverables(context.Background())
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
