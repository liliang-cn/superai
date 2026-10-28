// SuperAI activity for pi. Installed by `superai hook install` (and a
// SuperAI node on start); SuperAI overwrites this file when it changes.
//
// It tells the SuperAI on this machine, over its local socket, what this pi
// session is doing: working, waiting for you, or idle, and the name of the
// tool it is on. Never a prompt, a message, a file, or a tool's arguments:
// only the session's id, the last element of its directory, and the tool's
// name.
//
// Only notification events are used, and every handler returns at once
// without waiting on the socket; nothing here throws.

import http from "node:http"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"

const CLI = "pi"
// The socket of the node that installed this file; replaced at install.
const INSTALLED = "__SUPERAI_SOCKET__"

function sockets() {
  const out = []
  const add = (p) => { if (p && !p.startsWith("__") && !out.includes(p)) out.push(p) }
  add((process.env.SUPERAI_HOOK_SOCKET || "").trim())
  const home = (process.env.SUPERAI_HOME || "").trim()
  if (home) add(path.join(home.replace(/^~(?=\/)/, os.homedir()), "cc-hook.sock"))
  add(INSTALLED)
  add(path.join(os.homedir(), ".superai-node", "cc-hook.sock"))
  add(path.join(os.homedir(), ".superai", "cc-hook.sock"))
  return out
}

function pick() {
  for (const s of sockets()) {
    try { if (fs.statSync(s).isSocket()) return s } catch {}
  }
  return ""
}

let chain = Promise.resolve()
let queued = 0

function post(sock, body) {
  return new Promise((resolve) => {
    try {
      const req = http.request({ socketPath: sock, path: "/cc/hook", method: "POST", timeout: 300,
        headers: { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(body) } }, (res) => { res.resume(); res.on("end", resolve); res.on("error", resolve) })
      req.on("error", resolve)
      req.on("timeout", () => { req.destroy(); resolve() })
      req.end(body)
    } catch { resolve() }
  })
}

function send(ev) {
  try {
    if (!ev || !ev.session || queued > 64) return
    const sock = pick()
    if (!sock) return
    const body = JSON.stringify({ cli: CLI, pid: process.pid, pane: process.env.TMUX_PANE || "", ...ev })
    queued++
    chain = chain.then(() => post(sock, body)).catch(() => {}).then(() => { queued-- })
  } catch {}
}

function project(dir) {
  try { return dir ? path.basename(String(dir).replace(/\/+$/, "")) : "" } catch { return "" }
}

// translate is one pi event in SuperAI's terms, or null.
function translate(event, ctx) {
  let session = "", cwd = ""
  try { session = String(ctx.sessionManager.getSessionId() || "") } catch {}
  try { cwd = project(ctx.cwd) } catch {}
  if (!session || !event) return null
  const base = { session, cwd }
  switch (event.type) {
    case "session_start":
      return { ...base, event: "SessionStart" }
    case "session_shutdown":
      return { ...base, event: "SessionEnd" }
    case "agent_start":
      return { ...base, event: "UserPromptSubmit" }
    case "tool_execution_start":
      if (event.parentToolCallId) return null // a call another tool made: its parent is the one on
      return { ...base, event: "PreToolUse", tool: String(event.toolName || "").slice(0, 64) }
    case "tool_execution_end":
      if (event.parentToolCallId) return null
      return { ...base, event: "PostToolUse", tool: String(event.toolName || "").slice(0, 64) }
    case "ui_prompt_start":
      return event.kind === "confirm" ? { ...base, event: "PermissionRequest" } : { ...base, event: "WaitInput" }
    case "ui_prompt_end":
      return { ...base, event: "PermissionAnswered" }
    case "agent_settled":
      return { ...base, event: "Stop" }
  }
  return null
}

const EVENTS = ["session_start", "session_shutdown", "agent_start", "tool_execution_start", "tool_execution_end",
  "ui_prompt_start", "ui_prompt_end", "agent_settled"]

export default function superaiActivity(pi) {
  if (process.env.SUPERAI_CLI_RUN) return // a run SuperAI started reports itself
  for (const name of EVENTS) {
    try {
      pi.on(name, (event, ctx) => {
        try { const ev = translate(event, ctx); if (ev) send(ev) } catch {}
      })
    } catch {}
  }
}

// For the tests (cliplugins/plugins_test.mjs).
superaiActivity.translate = translate
