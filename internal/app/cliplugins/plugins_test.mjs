// node --test internal/app/cliplugins/plugins_test.mjs (TestCLIPluginsInNode
// runs it with go test). The opencode plugin and the pi extension: what each
// event becomes, that nothing a prompt or a tool's arguments said is sent,
// and that a dead socket costs nothing.

import test from "node:test"
import assert from "node:assert/strict"
import http from "node:http"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"

const dir = fs.mkdtempSync(path.join("/tmp", "clip-"))
const sock = path.join(dir, "cc-hook.sock")
process.env.SUPERAI_HOOK_SOCKET = sock
delete process.env.SUPERAI_CLI_RUN

const got = []
const server = http.createServer((req, res) => {
  let b = ""
  req.on("data", (c) => (b += c))
  req.on("end", () => { got.push({ url: req.url, body: JSON.parse(b) }); res.setHeader("Content-Type", "application/json"); res.end('{"reply":""}') })
})
await new Promise((r) => server.listen(sock, r))
test.after(() => { server.close(); fs.rmSync(dir, { recursive: true, force: true }) })

const until = async (n) => { for (let i = 0; i < 200 && got.length < n; i++) await new Promise((r) => setTimeout(r, 10)) }

const opencode = (await import("./opencode.js")).default
const pi = (await import("./pi.js")).default

test("opencode events in SuperAI's terms", () => {
  const tr = opencode.server.translate
  const kids = new Set()
  const d = "/Users/me/code/superai/"
  assert.deepEqual(tr({ type: "session.created", properties: { info: { id: "s1", directory: "/Users/me/code/web" } } }, d, kids),
    { session: "s1", cwd: "web", event: "SessionStart" })
  // A subagent's session is its parent's work: never its own.
  assert.equal(tr({ type: "session.created", properties: { info: { id: "k1", parentID: "s1" } } }, d, kids), null)
  assert.equal(tr({ type: "session.status", properties: { sessionID: "k1", status: { type: "busy" } } }, d, kids), null)
  assert.deepEqual(tr({ type: "session.status", properties: { sessionID: "s1", status: { type: "busy" } } }, d, kids),
    { session: "s1", cwd: "superai", event: "Working" })
  assert.equal(tr({ type: "session.status", properties: { sessionID: "s1", status: { type: "idle" } } }, d, kids).event, "Stop")
  assert.equal(tr({ type: "session.idle", properties: { sessionID: "s1" } }, d, kids).event, "Stop")
  const perm = tr({ type: "permission.asked", properties: { id: "p", sessionID: "s1", permission: "bash", patterns: ["rm -rf ARG-SECRET"], metadata: { command: "ARG-SECRET" } } }, d, kids)
  assert.deepEqual(perm, { session: "s1", cwd: "superai", event: "PermissionRequest", tool: "bash" })
  assert.equal(tr({ type: "permission.replied", properties: { sessionID: "s1", requestID: "p", reply: "once" } }, d, kids).event, "PermissionAnswered")
  assert.equal(tr({ type: "question.asked", properties: { id: "q", sessionID: "s1", questions: [{ question: "PROMPT-SECRET?" }] } }, d, kids).event, "WaitInput")
  assert.equal(tr({ type: "session.deleted", properties: { info: { id: "s1" } } }, d, kids).event, "SessionEnd")
  assert.equal(tr({ type: "message.part.updated", properties: { part: { sessionID: "s1", text: "PROMPT-SECRET" } } }, d, kids), null)
  assert.equal(tr(null, d, kids), null)
})

test("opencode hooks reach the socket in order, without arguments", async () => {
  got.length = 0
  const hooks = await opencode.server({ directory: "/Users/me/code/superai", worktree: "/Users/me/code/superai" })
  await hooks["chat.message"]({ sessionID: "s1", agent: "build" }, { message: { text: "PROMPT-SECRET" }, parts: [{ text: "PROMPT-SECRET" }] })
  await hooks["tool.execute.before"]({ tool: "bash", sessionID: "s1", callID: "c" }, { args: { command: "echo ARG-SECRET" } })
  await hooks["tool.execute.after"]({ tool: "bash", sessionID: "s1", callID: "c", args: { command: "echo ARG-SECRET" } }, { title: "t", output: "OUT-SECRET", metadata: {} })
  await hooks.event({ event: { type: "session.status", properties: { sessionID: "s1", status: { type: "idle" } } } })
  await until(4)
  const pid = process.pid
  assert.deepEqual(got.map((g) => g.body), [
    { cli: "opencode", pid, event: "UserPromptSubmit", session: "s1", cwd: "superai" },
    { cli: "opencode", pid, event: "PreToolUse", session: "s1", cwd: "superai", tool: "bash" },
    { cli: "opencode", pid, event: "PostToolUse", session: "s1", tool: "bash" },
    { cli: "opencode", pid, session: "s1", cwd: "superai", event: "Stop" },
  ])
  assert.ok(got.every((g) => g.url === "/cc/hook"))
  assert.ok(!JSON.stringify(got).includes("SECRET") && !JSON.stringify(got).includes("/Users/me"))
})

test("pi events in SuperAI's terms", async () => {
  got.length = 0
  const handlers = {}
  pi({ on: (name, fn) => { handlers[name] = fn } })
  const ctx = { cwd: "/Users/me/code/pets", sessionManager: { getSessionId: () => "pi1" } }
  for (const ev of [
    { type: "session_start", reason: "startup" },
    { type: "agent_start" },
    { type: "tool_execution_start", toolCallId: "1", toolName: "bash", args: { command: "ARG-SECRET" } },
    { type: "tool_execution_start", toolCallId: "1/1", toolName: "read", args: {}, parentToolCallId: "1" },
    { type: "tool_execution_end", toolCallId: "1", toolName: "bash", result: "OUT-SECRET", isError: false },
    { type: "ui_prompt_start", reason: "ui_prompt", kind: "confirm", title: "PROMPT-SECRET" },
    { type: "ui_prompt_end", reason: "ui_prompt", kind: "confirm" },
    { type: "ui_prompt_start", reason: "ui_prompt", kind: "input" },
    { type: "agent_settled", aborted: false },
    { type: "session_shutdown", reason: "quit" },
  ]) await handlers[ev.type](ev, ctx)
  await until(9)
  assert.deepEqual(got.map((g) => g.body.event), ["SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
    "PermissionRequest", "PermissionAnswered", "WaitInput", "Stop", "SessionEnd"])
  assert.ok(got.every((g) => g.body.cli === "pi" && g.body.session === "pi1" && g.body.cwd === "pets"))
  assert.ok(!JSON.stringify(got).includes("SECRET"))
  // A context that cannot say its session sends nothing, and throws nothing.
  await handlers.agent_start({ type: "agent_start" }, { sessionManager: { getSessionId: () => { throw new Error("x") } } })
})

test("no socket, a SuperAI run: nothing sent, nothing thrown", async () => {
  process.env.SUPERAI_HOOK_SOCKET = path.join(dir, "nobody.sock")
  process.env.SUPERAI_HOME = path.join(dir, "nohome")
  const hooks = await opencode.server({ directory: "/x" })
  const t0 = Date.now()
  await hooks["tool.execute.before"]({ tool: "bash", sessionID: "s9", callID: "c" }, { args: {} })
  assert.ok(Date.now() - t0 < 50)
  process.env.SUPERAI_HOOK_SOCKET = sock
  process.env.SUPERAI_CLI_RUN = "1"
  got.length = 0
  assert.deepEqual(await opencode.server({ directory: "/x" }), {})
  const handlers = {}
  pi({ on: (name, fn) => { handlers[name] = fn } })
  assert.deepEqual(handlers, {})
  delete process.env.SUPERAI_CLI_RUN
})
