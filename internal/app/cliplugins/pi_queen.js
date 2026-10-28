// SuperLeo's queen, for pi. SuperLeo hands this file to pi with -e when pi
// thinks for the queen (queen_engine_cli.go); it is not installed anywhere
// pi loads it from by itself, and does nothing in a pi started otherwise.
//
// It registers the MCP servers SuperLeo names in SUPERLEO_QUEEN_SERVERS, with
// every ${NAME} in them filled in from the environment here, so a token
// never travels on a command line. When SUPERLEO_APPROVE_URL is set the run
// is attended: a call that is not a read, and is not one of the tools
// SUPERLEO_QUEEN_ALLOWED lets through, is put to the person on SuperLeo's
// approval card first, and blocked when they say no.

import http from "node:http"

const READS = new Set(["read", "grep", "find", "ls", "codemode", "tool_search",
  "list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"])

function parse(raw, fallback) {
  try { return raw ? JSON.parse(raw) : fallback } catch { return fallback }
}

// expand fills ${NAME} in every string of v from the environment.
function expand(v) {
  if (typeof v === "string") {
    return v.replace(/\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g, (m, name) => process.env[name] ?? m)
  }
  if (Array.isArray(v)) return v.map(expand)
  if (v && typeof v === "object") {
    const out = {}
    for (const [k, x] of Object.entries(v)) out[k] = expand(x)
    return out
  }
  return v
}

// unasked is whether a tool may run without the person: a read, or a tool
// of a server the allowed rules name (mcp__hive is all of hive's).
function unasked(tool, allowed) {
  if (READS.has(tool)) return true
  return allowed.some((rule) => tool === rule || tool.startsWith(rule + "__"))
}

// decide asks SuperLeo about one call and resolves to {behavior, message}.
// No timeout here: the card waits for a person, and SuperLeo ends the wait.
function decide(url, tool, input) {
  return new Promise((resolve) => {
    const deny = (message) => resolve({ behavior: "deny", message })
    try {
      const body = JSON.stringify({ tool_name: tool, input: input ?? {} })
      const req = http.request(url + "/decide", { method: "POST",
        headers: { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(body) } }, (res) => {
        let text = ""
        res.setEncoding("utf8")
        res.on("data", (c) => { text += c })
        res.on("end", () => {
          const got = parse(text, null)
          if (res.statusCode === 200 && got && got.behavior) resolve(got)
          else deny("SuperLeo could not ask about this call")
        })
        res.on("error", () => deny("SuperLeo could not ask about this call"))
      })
      req.on("error", () => deny("SuperLeo could not be reached to ask about this call"))
      req.end(body)
    } catch {
      deny("SuperLeo could not ask about this call")
    }
  })
}

export default function superleoQueen(pi) {
  const servers = parse(process.env.SUPERLEO_QUEEN_SERVERS, {})
  for (const [name, cfg] of Object.entries(servers)) {
    try { pi.registerMcpServer(name, expand(cfg)) } catch {}
  }
  const url = (process.env.SUPERLEO_APPROVE_URL || "").trim()
  if (!url) return
  const allowed = (process.env.SUPERLEO_QUEEN_ALLOWED || "").split(",").map((s) => s.trim()).filter(Boolean)
  pi.on("tool_call", async (event) => {
    const tool = String(event.toolName || "")
    if (unasked(tool, allowed)) return undefined
    const got = await decide(url, tool, event.input)
    if (got.behavior === "allow") return undefined
    return { block: true, reason: got.message || "Denied by the SuperLeo user." }
  })
}

// For the tests (cliplugins/plugins_test.mjs).
superleoQueen.expand = expand
superleoQueen.unasked = unasked
