// SuperAI activity for opencode. Installed by `superai hook install` (and a
// SuperAI node on start); SuperAI overwrites this file when it changes.
//
// It tells the SuperAI on this machine, over its local socket, what each
// opencode session is doing: working, waiting for you (a permission or a
// question), or idle, and the name of the tool it is on. Never a prompt, a
// message, a file, or a tool's arguments: only the session's id, the last
// element of its directory, and the tool's name.
//
// Fire and forget: nothing here is awaited by opencode, and nothing throws.
// No SuperAI listening costs one failed connect per event.

import http from "node:http"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"

const CLI = "opencode"
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

// send queues one event; events go one at a time, in order.
function send(ev) {
  try {
    if (!ev || !ev.session || queued > 64) return
    const sock = pick()
    if (!sock) return
    const body = JSON.stringify({ cli: CLI, ...ev })
    queued++
    chain = chain.then(() => post(sock, body)).catch(() => {}).then(() => { queued-- })
  } catch {}
}

/** The last element of a directory: all of a path that leaves this machine's opencode. */
function project(dir) {
  try { return dir ? path.basename(String(dir).replace(/\/+$/, "")) : "" } catch { return "" }
}

// translate is one opencode event in SuperAI's terms, or null.
function translate(event, dir, children) {
  const p = (event && event.properties) || {}
  const sid = p.sessionID || (p.info && p.info.id) || ""
  if (!sid || children.has(sid)) return null
  const base = { session: sid, cwd: project(dir) }
  switch (event.type) {
    case "session.created":
      if (p.info && p.info.parentID) { children.add(sid); return null } // a subagent's session is its parent's work
      return { ...base, event: "SessionStart", cwd: project((p.info && p.info.directory) || dir) }
    case "session.deleted":
      return { ...base, event: "SessionEnd" }
    case "session.status": {
      const t = p.status && p.status.type
      if (t === "busy" || t === "retry") return { ...base, event: "Working" }
      if (t === "idle") return { ...base, event: "Stop" }
      return null
    }
    case "session.idle":
      return { ...base, event: "Stop" }
    case "permission.asked":
    case "permission.updated":
      // Which kind of permission, by name (bash, edit, ...): never its patterns.
      return { ...base, event: "PermissionRequest", tool: String(p.permission || p.type || "").slice(0, 64) }
    case "permission.replied":
    case "question.replied":
    case "question.rejected":
      return { ...base, event: "PermissionAnswered" }
    case "question.asked":
      return { ...base, event: "WaitInput" }
  }
  return null
}

const SuperAIActivity = async (input) => {
  if (process.env.SUPERAI_CLI_RUN) return {} // a run SuperAI started reports itself
  const dir = (input && (input.directory || input.worktree)) || process.cwd()
  const children = new Set()
  return {
    event: async ({ event }) => {
      try { const ev = translate(event, dir, children); if (ev) send(ev) } catch {}
    },
    "chat.message": async (inp) => {
      try { if (inp && inp.sessionID && !children.has(inp.sessionID)) send({ event: "UserPromptSubmit", session: inp.sessionID, cwd: project(dir) }) } catch {}
    },
    "tool.execute.before": async (inp) => {
      try { if (inp && !children.has(inp.sessionID)) send({ event: "PreToolUse", session: inp.sessionID, cwd: project(dir), tool: String(inp.tool || "").slice(0, 64) }) } catch {}
    },
    "tool.execute.after": async (inp) => {
      try { if (inp && !children.has(inp.sessionID)) send({ event: "PostToolUse", session: inp.sessionID, tool: String(inp.tool || "").slice(0, 64) }) } catch {}
    },
  }
}

// For the tests (cliplugins/plugins_test.mjs): the parts that decide what is sent.
SuperAIActivity.translate = translate
SuperAIActivity.project = project

export default { id: "superai-activity", server: SuperAIActivity }
