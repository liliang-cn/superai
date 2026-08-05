# SuperAI Desktop — Design Spec

Date: 2026-06-21
Status: approved, in implementation

## Goal

A cross-platform (macOS + Windows) **Wails v2** desktop app that is a full
showcase of the AgentGo framework: a single-user local assistant whose backend
is a maximally-configured `agent.Service` — sandbox + browser + vision +
autonomy + deliverables + graph memory + skills + the SuperAI life-assistant
tools + CortexDB data import — driven by a fresh, modern sidebar UI.

It also exposes an **avatar-driver interface**: SuperAI does NOT render a 3D/2D
character itself, but broadcasts emotion + agent-lifecycle events over a local
protocol so any external 2D/3D renderer (Live2D / VRM / Unity / web) can be
driven by it.

Standalone repo at `~/Things/projects/ai/superai-desktop`, depending on the
published `github.com/liliang-cn/agent-go/v3` (currently v2.89.0) and
`cortexdb/v2` (v2.24.1). No Go workspace entanglement — it pins released
versions.

## Stack

- **Wails v2** (v2.12) — Go backend + React + Vite + TypeScript frontend, native
  webview on both macOS (WKWebView) and Windows (WebView2).
- Backend: AgentGo `agent.Service` + cortexdb + cortexbridge.
- Data dir: `~/.superai-desktop/` (cortex.db, workspace/, settings.json).
- Cross-platform: no OS-specific code in our layer; agent-go's sandbox already
  has unix/other build tags, chromedp + WebView2/WKWebView work on both.

## Backend: maximally-configured Service (`backend/agentsvc`)

```go
agent.New("SuperAI").
    WithLLM(providerFromSettings).
    WithSandbox(sandbox.NewLocal(WithWorkspace(~/.superai-desktop/workspace))).
    WithBrowser(browser.NewChromedp(headless)).
    WithVision(true).
    WithAutonomy(agent.AutonomyProfile{MaxRounds: 40, Scratchpad: true}).
    WithDeliverables(true).
    WithGraphMemory()        // if embedder configured; else WithMemory(file)
    WithSkills().
    Build()
// + RegisterFetchURLTool
// + life-assistant tools ported from examples/superai (add_schedule, list_schedules,
//   add_record, search_records, upsert_person, set_reminder, list_reminders)
// + cortexbridge.RegisterImportFlow + connectorbridge.Register (data import)
// + proactive reminder scheduler (port from superai)
```

No-embedder fallback to file memory (chat-only proxies still work), mirroring
examples/superai.

## Wails bridge (Go ⇆ JS)

Bound Go methods + `runtime.EventsEmit` for streaming:

- `Chat(message)` → drives `svc.RunStreamWithOptions`; emits Wails events:
  `chat:partial`, `chat:tool_call`, `chat:tool_result` (incl. `ptc_inner`),
  `chat:complete` / `chat:blocked` / `chat:error`.
- `RunAgentTask(task)` → same streaming, for the autonomous-agent view.
- `Settings.Get/Set` (LLM provider/model/key, workspace path, autonomy budget,
  memory mode, avatar port) persisted to settings.json.
- `Workspace.List` / `Workspace.Open` / `Deliverables` — workspace artifacts.
- `ImportData.Plan` / `ImportData.Run` — importflow/connector (PII desensitize toggle).
- Life-assistant CRUD: schedules / records / persons / reminders.
- Reminders: scheduler fires → Wails event `reminder:due` → desktop notification.

## Avatar driver interface (drive external 2D/3D)

A small package `backend/avatar`:

```go
type AvatarEvent struct {
    Type    string `json:"type"`    // "state" | "emotion" | "speech"
    State   string `json:"state,omitempty"`   // idle | thinking | working | speaking
    Emotion string `json:"emotion,omitempty"` // neutral|happy|sad|thinking|excited|... (from SuperAI persona tags)
    Text    string `json:"text,omitempty"`    // for speech/talking + optional viseme hints later
    Tool    string `json:"tool,omitempty"`    // current tool when state=working
    Ts      int64  `json:"ts"`
}

type AvatarDriver interface { Emit(AvatarEvent) }
```

- Default impl: **WebSocketDriver** — a local server at `ws://127.0.0.1:<avatarPort>/avatar`
  that broadcasts `AvatarEvent` JSON to all connected clients. Language/tech
  agnostic: any external Live2D/VRM/Unity/web renderer connects and reacts.
- The runtime maps to events: run start → `state:thinking`; each tool_call →
  `state:working` (+tool); final answer streaming → `state:speaking` (+text);
  done → `state:idle`. SuperAI's emotion tags (情绪: ...) → `emotion` events.
- A `NoopDriver` when no avatar is attached. The protocol (event schema + port)
  is documented in `docs/avatar-protocol.md` so third parties can implement it.
- v1 ships the protocol + server + a tiny reference HTML page that shows a 2D
  placeholder reacting to events (proof the interface works); real 3D is external.

## Frontend: fresh modern UI (sidebar nav)

- **Chat** — streaming chat + right-side live tool-trace panel (`▶ run code`, `↳ fs_write` …).
- **Agent** — give an autonomous task; watch sandbox+browser+vision steps; workspace/deliverables panel (open/export).
- **Memory** — graph-memory browse + graph_recall query; skills list/run.
- **Data** — importflow/connector: pick CSV/DB → preview MappingPlan → import into RAG+KG (PII desensitize toggle).
- **Life** — schedules / records / persons / reminders (cards) + proactive reminder notifications.
- **Avatar** — connection status + the avatar port/protocol, a "test event" button, embedded reference 2D placeholder.
- **Settings** — provider/model/key, workspace path, autonomy budget, memory mode, avatar port.

## Phasing (each phase builds + runs)

- **P0** Skeleton: Wails project (macOS+Windows build config), backend Service wired, Settings page, LLM connectivity smoke.
- **P1** Chat: streaming + tool-trace panel (incl. ptc_inner) + avatar driver wired (state/emotion events).
- **P2** Agent: autonomous task view + workspace/deliverables panel.
- **P3** Life: life-assistant tools + proactive reminders (desktop notifications).
- **P4** Memory + Data: graph browse / skills + importflow/connector import panel.
- **P5** Polish + package: `wails build` macOS .app + Windows .exe, icons, avatar reference page, docs.

## Non-goals (v1)

- Built-in 3D/2D character rendering (external, via the avatar protocol).
- Multi-tenant / auth / billing (it's a local single-user desktop app).
- Voice STT/TTS (the avatar protocol leaves room for viseme/text; not built now).

## Testing

- Backend agentsvc + avatar driver: Go unit tests (avatar event mapping, settings, tool registration) with MockLLM where an LLM is needed.
- `wails build` succeeds on macOS (and Windows build config present).
- Avatar protocol: a reference HTML client connects and renders events.
