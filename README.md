# SuperAI Desktop

A cross-platform (macOS + Windows) **Wails v2** desktop app that is a full
showcase of the [AgentGo](https://github.com/liliang-cn/agent-go) framework: a
local, single-user AI assistant whose backend is a maximally-configured
`agent.Service` — **sandbox + browser + vision + autonomy + deliverables +
graph memory + skills + life-assistant tools + CortexDB data import** — behind a
fresh, modern React UI.

It also drives **external 2D/3D avatars**: SuperAI doesn't render a character
itself, but broadcasts emotion + agent-lifecycle events over a local SSE
protocol that any external renderer (Live2D / VRM / Unity / web) can consume.

## Status

Foundation (P0/P1) in place and building:

- Go backend (`backend/`): full `agent.Service`, settings, life-assistant tools,
  CortexDB import tools, and the avatar SSE driver.
- Wails bindings + live streaming (`chat:event` / `chat:done` / `chat:error`).
- Modern React UI: Chat (streaming + live tool trace), Agent (autonomous task +
  deliverables), Settings, Avatar; Memory/Data/Life are styled placeholders.
- `wails build` produces a native macOS `.app`.

## Develop

```bash
wails dev      # hot-reload dev (Go + Vite)
wails build    # native app -> build/bin/SuperAI.app (or SuperAI.exe on Windows)
go build ./... # backend only
```

Configure the LLM in **Settings** (or via env: `LLM_BASE` / `LLM_KEY` /
`LLM_MODEL`, or `DASHSCOPE_API_KEY`). With an embedding provider configured it
uses CortexDB graph memory; otherwise it falls back to file memory.

## Avatar protocol (drive an external 2D/3D character)

```
GET http://127.0.0.1:<avatar_port>/avatar/events   # text/event-stream of AvatarEvent JSON
GET http://127.0.0.1:<avatar_port>/avatar          # reference 2D placeholder page
```

`AvatarEvent`: `{ type: "state"|"emotion"|"speech", state, emotion, text, tool, ts }`.

## Architecture

See `docs/superpowers/specs/2026-06-21-superai-desktop-design.md`.
