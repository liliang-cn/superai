import type { WorldData } from "../world/cluster";
import type { HAService, Host } from "../canvas/data";

// The embed's side of its conversation with a native host (the Android app).
// Everything is a JSON object with a "type"; strings are parsed, objects are
// taken as they are.
//
// Host → embed
//   {type:"data", data: WorldData (partial is fine), queen?: {name, aliases?}}
//   {type:"camera", op: "zoomIn"|"zoomOut"|"rotateLeft"|"rotateRight"|"home"|"reset"}
//   {type:"focus", kind, id}         selects a thing and flies to it, quietly
//   {type:"select", kind: null}      clears the selection
//   {type:"visible", on: boolean}    stops or restarts drawing
//   {type:"lens", x, z, zoom}        where the camera looks
//
// Embed → host
//   {type:"ready", version, webgl: boolean, error?}
//   {type:"pick", kind, id, title}   a thing was tapped; {type:"pick", id:null} on empty ground

export const PROTOCOL = 1;

export type CameraOp = "zoomIn" | "zoomOut" | "rotateLeft" | "rotateRight" | "home" | "reset";

export type HostMessage =
  | { type: "data"; data?: Partial<WorldData>; queen?: { name?: string; aliases?: string[] } }
  | { type: "camera"; op: CameraOp }
  | { type: "focus"; kind: string; id: string }
  | { type: "select"; kind: null }
  | { type: "visible"; on: boolean }
  | { type: "lens"; x: number; z: number; zoom: number };

export type EmbedMessage =
  | { type: "ready"; version: number; webgl: boolean; error?: string }
  | { type: "pick"; kind: string; id: string; title: string }
  | { type: "pick"; id: null };

export function parse(raw: unknown): HostMessage | null {
  let v: unknown = raw;
  if (typeof raw === "string") {
    try { v = JSON.parse(raw); } catch { return null; }
  }
  if (!v || typeof v !== "object" || typeof (v as { type?: unknown }).type !== "string") return null;
  return v as HostMessage;
}

function arr<T>(v: unknown): T[] { return Array.isArray(v) ? (v as T[]) : []; }
const num = (v: unknown) => (typeof v === "number" && isFinite(v) ? v : 0);
const str = (v: unknown) => (typeof v === "string" ? v : "");

/** A whole WorldData from whatever the host sent: every list there, every
 *  figure a number, so a field a host leaves out never stops the scene. */
export function normalize(d: Partial<WorldData> | undefined): WorldData {
  const x = (d ?? {}) as Partial<WorldData>;
  const h = (x.hive ?? {}) as Partial<WorldData["hive"]>;
  const member = (m: unknown) => {
    const o = (m ?? {}) as Record<string, unknown>;
    return {
      name: str(o.name), live: !!o.live, tokens: num(o.tokens), cached: num(o.cached), rounds: num(o.rounds),
      calls: num(o.calls), fails: num(o.fails), mcp: num(o.mcp), cpu: num(o.cpu), heap: num(o.heap),
      tokPerSec: num(o.tokPerSec), doing: typeof o.doing === "string" ? o.doing : undefined, at: str(o.at),
    };
  };
  const meter = (x.meter ?? {}) as Partial<WorldData["meter"]>;
  const members = arr<unknown>(meter.members).map(member).filter((m) => m.name);
  // No total from the host: the members' sum, as the desktop's meter makes it.
  const total = meter.total ? member(meter.total) : members.reduce((t, m) => ({
    ...t, live: t.live || m.live, tokens: t.tokens + m.tokens, cached: t.cached + m.cached, rounds: t.rounds + m.rounds,
    calls: t.calls + m.calls, fails: t.fails + m.fails, mcp: t.mcp + m.mcp, tokPerSec: t.tokPerSec + m.tokPerSec,
  }), member({ name: "hive" }));
  return {
    hive: {
      loaded: h.loaded ?? true,
      role: (h.role ?? "") as WorldData["hive"]["role"],
      name: str(h.name),
      engine: str(h.engine),
      engineNow: str(h.engineNow),
      members: arr<WorldData["hive"]["members"][number]>(h.members).filter((m) => m && typeof m.name === "string"),
      tasks: arr<WorldData["hive"]["tasks"][number]>(h.tasks).filter((t) => t && typeof t.id === "string")
        .map((t) => ({ ...t, worker: str(t.worker), prompt: str(t.prompt), tools: num(t.tools), started_at: str(t.started_at) })),
      // HiveStatus.relays: the named agents each relay serves, and what runs there.
      relays: arr<NonNullable<WorldData["hive"]["relays"]>[number]>(h.relays).filter((r) => r && typeof r.name === "string")
        .map((r) => ({ ...r, host: str(r.host), agents: arr<{ name: string; about: string }>(r.agents).filter((a) => a && typeof a.name === "string"),
          cliActivity: r.cliActivity && typeof r.cliActivity === "object" ? r.cliActivity : {} })),
    },
    runs: arr<WorldData["runs"][number]>(x.runs).map((r) => ({ ...r, agent: str(r.agent), prompt: str(r.prompt), state: str(r.state), started: str(r.started) })),
    bees: arr<WorldData["bees"][number]>(x.bees).filter((b) => b && typeof b.id === "string").map((b) => ({ ...b, name: str(b.name), goal: str(b.goal), paused: !!b.paused })),
    linked: arr<WorldData["linked"][number]>(x.linked).filter((a) => a && typeof a.name === "string")
      .map((a) => ({ ...a, host: str(a.host), os: str(a.os), arch: str(a.arch), version: str(a.version), clis: arr<string>(a.clis), agents: arr<{ name: string; about: string }>(a.agents), connected: str(a.connected), lastSeen: str(a.lastSeen) })),
    attention: arr<WorldData["attention"][number]>(x.attention).map((a) => ({ ...a, level: a.level, kind: str(a.kind), title: str(a.title) })),
    agenda: arr<WorldData["agenda"][number]>(x.agenda).filter((e) => e && typeof e.title === "string"),
    dashboards: arr<WorldData["dashboards"][number]>(x.dashboards),
    skills: arr<unknown>(x.skills).map(str).filter(Boolean),
    mcp: arr<WorldData["mcp"][number]>(x.mcp).map((m) => ({ name: str(m.name), ok: !!m.ok, tools: num(m.tools) })),
    knowledge: num(x.knowledge),
    meter: { total, members },
    busy: !!x.busy,
    // Hosts.hosts / HiveTopology: left out by a host that does not send
    // them, and then the hive is drawn the old way.
    hosts: Array.isArray(x.hosts) ? x.hosts.filter((h) => h && typeof h.id === "string")
      .map((h) => ({ ...h, name: str(h.name) || h.id, kind: str(h.kind) as Host["kind"], state: (str(h.state) || "configured") as Host["state"],
        agents: arr<Host["agents"][number]>(h.agents).filter((a) => a && typeof a.name === "string").map((a) => ({ ...a, kind: a.kind, state: str(a.state) })) })) : undefined,
    services: arr<HAService>(x.services).filter((s) => s && typeof s.name === "string")
      .map((s) => ({ ...s, provider: str(s.provider), state: str(s.state), units: arr<string>(s.units), standbys: arr<HAService["standbys"][number]>(s.standbys) })),
  };
}
