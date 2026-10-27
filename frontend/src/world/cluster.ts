import * as THREE from "three";
import type { District, DistrictCtx, Label } from "./engine";
import { FIN, beam, box, contact, cyl, hash01, mat, ownMat, projector, rbox, screen, smooth } from "./kit";
import { INK, ghost, pose, robot, setState, type RobotState } from "./robots";
import { forgetActor, registerActor } from "./bubbles";
import { Packets, type Link } from "./packets";
import { CSS2DObject } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import type { Bee, CCActivity, CCActivitySession, CLIActivity, HAService, Hive, HiveTask, Host, HostAgent, LinkedAgent, RelayAgent } from "../canvas/data";
import { CLOUD_LIFT, SLAB_TOP, cliName, cloud, floorLink, forgetLines, haRoles, isStandby, kindLabel, planHosts, pod, serviceMarker, setLinesNight, setServiceState, slab, socket, unlinkedStatus, type HostPlan, type Plot } from "./hosts";
import type { CodingRun } from "../canvas/tiles";
import type { AttentionItem } from "../canvas/attention";
import type { Member } from "../components/HiveMeter";
import { translate } from "../lib/i18n";
import { boardLine, touchesNoted, type AgendaEntry } from "./agenda";
import { along, movesOf, tripTime, turn, type Stand } from "./travel";
import { getQueenName, queenTitle } from "../lib/queenName";
import { engineLabel, queenEngineNote } from "../lib/format";

// The cluster, as it is: the core in the middle with the queen at its console
// and the shared brain beside it; the nodes around it with their workers
// standing on them; linked machines further out with their coding agents.
// Machines are machines; every agent is a robot. Orders travel the cables as
// cubes of light and are carried by whoever does the work.

export interface WorldData {
  hive: Hive;
  runs: CodingRun[];
  bees: Bee[];
  linked: LinkedAgent[];
  attention: AttentionItem[];
  /** noted's day, still to come (NotedToday). */
  agenda: AgendaEntry[];
  dashboards: { id: string; name: string; refreshed_at?: string }[];
  skills: string[];
  mcp: { name: string; ok: boolean; tools: number }[];
  knowledge: number;
  meter: { total: Member; members: Member[] };
  busy: boolean;
  /** The hosts the hive runs on, each with what runs on it. Absent on a core
   *  from before hosts: the hive is drawn from the roster and the links. */
  hosts?: Host[] | null;
  /** The HA services that move between hosts. */
  services?: HAService[];
}

const short = (n: string) => n.replace(/^superai-worker-/, "w").replace(/^superai-/, "");
/** A tool as a screen line shows it: an MCP tool by its own name, not its server's. */
const toolName = (t?: string) => (t && t.startsWith("mcp__") ? t.split("__").pop() || t : t || "");
/** What a terminal session of a coding CLI is on, in one line: "superai: Bash". */
export function sessionLine(s: CCActivitySession, cli = "claude"): string {
  const what = s.state === "waiting"
    ? (s.wait === "permission" ? translate("waiting for your OK") + (s.tool ? ` · ${toolName(s.tool)}` : "") : translate("waiting for you"))
    : s.state === "working" ? toolName(s.tool) || translate("thinking") : translate("idle");
  return `${s.project || cli}: ${what}`;
}
/** Every coding CLI on a machine, by CLI: its cliActivity, with claude's
 *  terminal sessions from an older core's activity when that is all there is. */
export function clisOf(a: { activity?: CCActivity | null; cliActivity?: Record<string, CLIActivity> }): Record<string, CCActivity> {
  const out: Record<string, CCActivity> = { ...(a.cliActivity ?? {}) };
  if (!out.claude && a.activity?.sessions?.length) out.claude = a.activity;
  return out;
}
/** A robot's own terminal state from its CLI's sessions: what its label says. */
function termState(act: CCActivity | undefined) {
  const work = act?.working ?? 0, wait = act?.waiting ?? 0;
  const status = work ? translate(work === 1 ? "1 session working" : "{count} sessions working", { count: work }) : wait ? translate("waiting for you") : "";
  return { work, wait, status };
}
/** An HA service's pin says which it is and where to reach it: "openclaw · 192.168.123.252". */
const svcTitle = (s: HAService) => (s.vip ? `${s.name} · ${s.vip}` : s.name);
const clip = (s: string, n = 30) => { const t = s.replace(/\s+/g, " ").trim(); return t.length > n ? t.slice(0, n - 1) + "…" : t; };
/** The queen's name over her robot, with the engine thinking for her when it is not SuperAI's own: "queen · Claude Code",
 *  or who stands in when it cannot: "queen · Claude Code · dell · backup", "queen · SuperAI · standing in". */
const queenLabel = (engine?: string, now?: string) => {
  const name = getQueenName().name;
  const note = queenEngineNote(engine, now);
  return note ? `${name} · ${note}` : engine ? `${name} · ${engineLabel(engine)}` : name;
};
const CABLE = 0xc7cfd9;
const TRIMS = [0x2bb3a3, 0x3b82f6, 0x22a06b, 0xe0a020, 0xef6c4a, 0x5b7cfa];
let accentColor = 0xf2b416;
export function setClusterAccent(c: number) { accentColor = c; }

/** The light a route is drawn with: a dash and a gap, scrolled along the path. */
let dashTex: THREE.CanvasTexture | null = null;
function dashes() {
  if (dashTex) return dashTex;
  const c = document.createElement("canvas");
  c.width = 128; c.height = 32;
  const x = c.getContext("2d")!;
  const grad = x.createLinearGradient(0, 0, 70, 0);
  grad.addColorStop(0, "rgba(255,255,255,0)");
  grad.addColorStop(0.75, "rgba(255,255,255,1)");
  grad.addColorStop(1, "rgba(255,255,255,1)");
  x.fillStyle = grad;
  x.beginPath(); x.roundRect(4, 9, 66, 14, 7); x.fill();
  dashTex = new THREE.CanvasTexture(c);
  dashTex.wrapS = THREE.RepeatWrapping;
  dashTex.colorSpace = THREE.SRGBColorSpace;
  return dashTex;
}
let haloTex: THREE.CanvasTexture | null = null;
function halo() {
  if (haloTex) return haloTex;
  const c = document.createElement("canvas");
  c.width = 4; c.height = 64;
  const x = c.getContext("2d")!;
  const grad = x.createLinearGradient(0, 0, 0, 64);
  grad.addColorStop(0, "rgba(255,255,255,0)");
  grad.addColorStop(0.5, "rgba(255,255,255,1)");
  grad.addColorStop(1, "rgba(255,255,255,0)");
  x.fillStyle = grad; x.fillRect(0, 0, 4, 64);
  haloTex = new THREE.CanvasTexture(c);
  return haloTex;
}

/** A flat strip along a curve, its texture running the length of it. */
function ribbon(curve: THREE.Curve<THREE.Vector3>, width: number, y: number, perUnit: number) {
  const n = 96, pos: number[] = [], uv: number[] = [], idx: number[] = [];
  const len = curve.getLength();
  for (let i = 0; i < n; i++) {
    const t = i / (n - 1);
    const p = curve.getPointAt(t), d = curve.getTangentAt(t);
    const side = new THREE.Vector3(-d.z, 0, d.x).normalize().multiplyScalar(width / 2);
    pos.push(p.x + side.x, y, p.z + side.z, p.x - side.x, y, p.z - side.z);
    uv.push(t * len * perUnit, 0, t * len * perUnit, 1);
    if (i < n - 1) { const k = i * 2; idx.push(k, k + 1, k + 2, k + 1, k + 3, k + 2); }
  }
  const g = new THREE.BufferGeometry();
  g.setAttribute("position", new THREE.Float32BufferAttribute(pos, 3));
  g.setAttribute("uv", new THREE.Float32BufferAttribute(uv, 2));
  g.setIndex(idx);
  return g;
}

/** Every route's moving parts, so one tick runs them all. */
const routes = new Set<{ dash: THREE.MeshBasicMaterial; glow: THREE.MeshBasicMaterial; active: boolean; ok: boolean }>();
const ROUTE = 0x2f8bff;

/**
 * A route between two points: not a cable but a path of light on the floor,
 * the way a robot shows where it is going — dashes flowing from the core out,
 * faint at rest, bright and quick while work is on it.
 */
function cable(a: THREE.Vector3, b: THREE.Vector3, seed: number) {
  const mid = a.clone().lerp(b, 0.5);
  const side = new THREE.Vector3(-(b.z - a.z), 0, b.x - a.x).normalize().multiplyScalar((seed - 0.5) * 3);
  mid.add(side);
  const curve = new THREE.CatmullRomCurve3([a.clone().setY(0.1), mid.setY(0.1), b.clone().setY(0.1)]);
  const tex = dashes().clone();
  tex.needsUpdate = true;
  tex.wrapS = THREE.RepeatWrapping;
  const dash = new THREE.MeshBasicMaterial({ map: tex, color: ROUTE, transparent: true, opacity: 0.55, depthWrite: false, toneMapped: false, side: THREE.DoubleSide });
  const glow = new THREE.MeshBasicMaterial({ map: halo(), color: ROUTE, transparent: true, opacity: 0.12, depthWrite: false, toneMapped: false, side: THREE.DoubleSide });
  const tube = new THREE.Group();
  const under = new THREE.Mesh(ribbon(curve, 1.4, 0.05, 0.08), glow);
  const line = new THREE.Mesh(ribbon(curve, 0.32, 0.07, 0.55), dash);
  under.renderOrder = 1; line.renderOrder = 2;
  tube.add(under, line);
  const r = { dash, glow, active: false, ok: true };
  routes.add(r);
  tube.userData.dispose = () => routes.delete(r);
  return {
    tube, curve,
    setActive(on: boolean) { r.active = on; },
    setOk(ok: boolean) { r.ok = ok; dash.color.set(ok ? ROUTE : 0xb8c0cc); glow.color.set(ok ? ROUTE : 0xb8c0cc); },
  };
}

/** Runs the light along every route. */
function flowRoutes(dt: number, t: number, reduced: boolean) {
  for (const r of routes) {
    const speed = !r.ok ? 0 : r.active ? 1.6 : 0.35;
    if (!reduced) (r.dash.map as THREE.Texture).offset.x -= dt * speed;
    const target = !r.ok ? 0.3 : r.active ? 0.95 : 0.5;
    r.dash.opacity += (target - r.dash.opacity) * Math.min(1, dt * 4);
    r.glow.opacity += ((r.active ? 0.32 + 0.08 * Math.sin(t * 5) : 0.1) - r.glow.opacity) * Math.min(1, dt * 4);
  }
}

type Screen = ReturnType<typeof screen>;

/** Paints a computer's screen: who it is, what it is doing, the last lines
 *  of the work, and a cursor while it is busy. */
function paint(s: Screen, title: string, status: string, lines: string[], busy: boolean, _blink = true) {
  // Dark glass with white type and one blue, as the phone draws it: the
  // name and a blue prompt, what it is doing, a hairline, the last lines of
  // the work with a blue chevron each. The cursor is the screen's own quad,
  // so blinking it never repaints this. On a hologram everything is blue
  // light: deep enough to read against the white world, with a soft halo
  // round each letter.
  const holo = !!s.mesh.userData.holo;
  let caret: { x: number; y: number; w: number; h: number; color: string } | null = null;
  s.draw((c, w, h) => {
    const k = w / 768; // laid out on the phone's 768-wide screen
    const blue = holo ? "#2f6bff" : "#5b8dff";
    const ink = holo ? "#1d4ed8" : "rgba(255,255,255,0.95)";
    const text = holo ? "rgba(31,63,153,0.92)" : "rgba(255,255,255,0.86)";
    const dim = holo ? "rgba(47,107,255,0.62)" : "rgba(255,255,255,0.5)";
    const rule = holo ? "rgba(47,107,255,0.28)" : "rgba(255,255,255,0.09)";
    const idle = holo ? "rgba(47,107,255,0.25)" : "rgba(255,255,255,0.22)";
    const pad = 36 * k, lineH = 46 * k, top = 150 * k;
    const bg = c.createLinearGradient(0, 0, 0, h);
    if (holo) {
      // The pane: brightest where the beam comes in at the bottom, thinning
      // upward, with fine scan lines.
      bg.addColorStop(0, "rgba(91,141,255,0.05)"); bg.addColorStop(1, "rgba(91,141,255,0.17)");
      c.fillStyle = bg; c.fillRect(0, 0, w, h);
      c.fillStyle = "rgba(47,107,255,0.05)";
      for (let y = 0; y < h; y += 6 * k) c.fillRect(0, y, w, 2 * k);
      c.shadowColor = "rgba(91,141,255,0.75)";
      c.shadowBlur = 14 * k;
    } else {
      bg.addColorStop(0, "#17212c"); bg.addColorStop(1, "#0c131b");
      c.fillStyle = bg; c.fillRect(0, 0, w, h);
    }
    c.textBaseline = "top";
    const mono = (px: number, weight: number) => `${weight} ${px * k}px 'Geist Mono', 'SF Mono', Menlo, 'PingFang SC', monospace`;
    c.font = mono(38, 600);
    c.fillStyle = ink;
    c.fillText(title, pad, 30 * k);
    c.fillStyle = blue;
    c.fillText(" ~$", pad + c.measureText(title).width, 30 * k);
    c.font = mono(26, 400);
    c.fillStyle = dim;
    c.fillText(status, pad, 84 * k);
    // What it is on: a dot, blue while busy.
    c.fillStyle = busy ? blue : idle;
    c.beginPath(); c.arc(w - pad - 9 * k, 53 * k, 9 * k, 0, Math.PI * 2); c.fill();
    c.fillStyle = rule;
    c.fillRect(pad, 130 * k, w - pad * 2, 2 * k);
    c.font = mono(29, holo ? 500 : 400);
    const maxW = w - pad * 2 - 34 * k;
    const rows: [string, boolean][] = [];
    for (const l of lines) {
      let row = "", first = true;
      for (const ch of l) {
        if (c.measureText(row + ch).width > maxW) { rows.push([row, first]); first = false; row = ch; if (rows.length >= 5) break; }
        else row += ch;
      }
      if (rows.length >= 5) break;
      if (row) rows.push([row, first]);
    }
    const shown = rows.slice(0, 5);
    shown.forEach(([line, first], i) => {
      const y = top + i * lineH;
      if (first) { c.fillStyle = blue; c.fillText("›", pad, y); }
      c.fillStyle = text;
      c.fillText(line, pad + 34 * k, y);
    });
    if (busy) caret = { x: pad + 34 * k, y: top + shown.length * lineH + 6 * k, w: 16 * k, h: 30 * k, color: blue };
    if (holo) return;
    // A sheen across the glass from the top left; no raster, no glow.
    const sheen = c.createLinearGradient(0, 0, w * 0.6, h);
    sheen.addColorStop(0, "rgba(255,255,255,0.06)"); sheen.addColorStop(0.45, "rgba(255,255,255,0.012)"); sheen.addColorStop(0.46, "rgba(255,255,255,0)");
    c.fillStyle = sheen; c.fillRect(0, 0, w, h);
  });
  s.cursor(caret);
}

/** What a desk's light along its front edge says its computer is doing. */
type DeskLight = "idle" | "busy" | "lost";
const LIGHT: Record<DeskLight, number> = { idle: 0xd8dee6, busy: 0xf2b416, lost: 0xe2553f };
function setLight(station: THREE.Object3D, s: DeskLight) {
  ((station.userData.strip as THREE.Mesh).material as THREE.MeshBasicMaterial).color.set(LIGHT[s]);
}

/** A computer at a desk, monitor (or laptop) facing the room. Its screens and
 *  the light along its front edge are in userData; `spots` are where its
 *  robots stand, beside the desk, turned to the screen. */
function workstation(kind: "desktop" | "laptop" | "console") {
  const g = new THREE.Group();
  const wide = kind === "console" ? 10 : 6.2;
  // The surface: at a robot's hands where one types (a worker's shoulder is
  // about 1.57 up), higher at the queen's console, which she stands before.
  const surface = kind === "console" ? 1.75 : 1.15;
  const top = rbox(wide, 0.16, 3, FIN.satin(), 0.07);
  top.position.y = surface - 0.2;
  g.add(top);
  for (const [x, z] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) {
    // Slim aluminium legs, as a good desk has.
    const leg = cyl(0.06, 0.06, surface - 0.16, 16, FIN.alu(), true);
    leg.position.set(x * (wide / 2 - 0.35), 0, z * 1.2);
    g.add(leg);
  }
  const screens: Screen[] = [];
  // A monitor is a hologram: a slim projector on the desk, a faint beam, and
  // the screen hanging in the air above it with no glass and no frame.
  const monitor = (x: number, tilt = 0) => {
    const m = new THREE.Group();
    const sw = 3.05, sh = 1.8, mid = 0.75 + 1.025;
    const sc = screen(sw, sh, 640, true);
    sc.mesh.position.set(0, mid, 0);
    m.add(projector(1.3), beam(0.08, mid - sh / 2, 1.1, sw), sc.mesh);
    m.position.set(x, surface, -0.6);
    m.rotation.y = tilt;
    screens.push(sc);
    g.add(m);
  };
  if (kind === "laptop") {
    const base = rbox(3, 0.1, 2.1, FIN.alu(), 0.05);
    base.position.set(0, surface, 0.1);
    const lid = new THREE.Group();
    lid.position.set(0, surface + 0.12, -0.95);
    const back = rbox(3, 2, 0.07, FIN.alu(), 0.05);
    const glassL = rbox(2.92, 1.92, 0.02, FIN.glass(), 0.04, false);
    glassL.position.set(0, 0.04, 0.04);
    lid.add(glassL);
    const sc = screen(2.75, 1.7, 640);
    sc.mesh.position.set(0, 1, 0.068);
    lid.add(back, sc.mesh);
    lid.rotation.x = -0.18;
    screens.push(sc);
    g.add(base, lid);
  } else if (kind === "console") {
    monitor(-3.3, 0.25); monitor(0); monitor(3.3, -0.25);
  } else {
    monitor(0);
    const keys = rbox(2.4, 0.05, 0.8, FIN.alu(), 0.025, false);
    keys.position.set(0, surface, 0.65);
    g.add(keys);
  }
  // No computer case: what it is doing shows in a line of light along the
  // desk's front edge, just under the top.
  const strip = box(wide - 0.8, 0.035, 0.03, new THREE.MeshBasicMaterial({ color: LIGHT.idle, toneMapped: false }));
  strip.position.set(0, surface - 0.1375, 1.5);
  strip.receiveShadow = false;
  g.add(strip);
  // The soft dark where the desk meets the floor.
  g.add(contact(wide + 1.6, 4.2, 0.13));
  g.userData.screens = screens;
  g.userData.strip = strip;
  // Against the desk's front edge, close enough for the hands to reach the
  // keys, facing the screen.
  g.userData.spots = [new THREE.Vector3(0, 0, 1.95), new THREE.Vector3(-1.7, 0, 1.95), new THREE.Vector3(1.7, 0, 1.95)];
  return g;
}

/** A wrench, the icon of a skill: a handle and an open jaw. */
function wrench(color: number, size = 1) {
  const g = new THREE.Group();
  const m = mat(color, { rough: 0.25, metal: 0.55 });
  const handle = rbox(0.34, 2.1, 0.2, m, 0.09);
  handle.position.y = -1.05;
  const jaw = new THREE.Mesh(new THREE.TorusGeometry(0.46, 0.17, 12, 28, Math.PI * 1.55), m);
  jaw.rotation.z = Math.PI / 2 + Math.PI * 0.225;
  jaw.position.y = 0.32;
  jaw.castShadow = true;
  g.add(handle, jaw);
  g.rotation.z = -0.6;
  g.scale.setScalar(size);
  const holder = new THREE.Group();
  holder.add(g);
  return holder;
}

/** A plug, the icon of an MCP server: a body, two prongs, a bit of lead. */
function plug(color: number, size = 1) {
  const g = new THREE.Group();
  const m = mat(color, { rough: 0.3, metal: 0.2 });
  const body = rbox(1, 1.15, 0.75, m, 0.2);
  body.position.y = -0.6;
  const metal = mat(0xc9ced6, { rough: 0.2, metal: 0.9 });
  for (const x of [-0.22, 0.22]) {
    const prong = rbox(0.12, 0.55, 0.08, metal, 0.03);
    prong.position.set(x, 0.55, 0);
    g.add(prong);
  }
  const lead = new THREE.Mesh(new THREE.TorusGeometry(0.5, 0.09, 8, 24, Math.PI), mat(INK, { rough: 0.5 }));
  lead.position.set(0.5, -0.6, 0);
  lead.rotation.z = Math.PI;
  g.add(body, lead);
  g.scale.setScalar(size);
  const holder = new THREE.Group();
  holder.add(g);
  return holder;
}

/** An exclamation mark that floats over whatever needs you: honey when it is
 *  waiting for a decision or a reply, red when something broke. */
function beacon(bad: boolean) {
  const g = new THREE.Group();
  const color = bad ? 0xe2553f : 0xf2b416;
  // Just the mark: a bar and a dot, glowing a little.
  const m = ownMat(color, { emissive: color, glow: 0.5, rough: 0.3 });
  const bar = rbox(0.3, 0.95, 0.3, m, 0.13);
  bar.position.y = 0.55;
  const dot = new THREE.Mesh(new THREE.SphereGeometry(0.17, 16, 12), m);
  dot.position.y = 0.22;
  bar.castShadow = dot.castShadow = true;
  g.add(bar, dot);
  g.scale.setScalar(0.85);
  return g;
}

/** A low round stand an icon floats over. */
function pedestal(r = 1.2) {
  const g = new THREE.Group();
  const p = cyl(r, r * 1.1, 0.5, 64, FIN.satin());
  // A single fine ring of light round its top edge.
  const ring = new THREE.Mesh(new THREE.TorusGeometry(r * 0.97, 0.035, 8, 96), ownMat(0xd8e4ff, { emissive: ROUTE, glow: 0.9 }));
  ring.rotation.x = Math.PI / 2;
  ring.position.y = 0.5;
  g.add(p, ring, contact(r * 3.2, r * 3.2, 0.16));
  return g;
}

const coreAt0 = () => new THREE.Vector3(0, 0, -2.2);

type Work = { node: THREE.Mesh; curve: THREE.Curve<THREE.Vector3>; t: number; back: boolean; state: string; to: THREE.Object3D | null };

export const cluster: District<WorldData> = {
  key: "cluster", title: "", at: [0, 0], size: [90, 90], bare: true,
  build(ctx: DistrictCtx) {
    const g = ctx.group;

    // ── The core: the queen's machine, her console, the shared brain ───────
    const dais = cyl(7.5, 7.8, 0.35, 96, FIN.satin(), false);
    dais.receiveShadow = true;
    // Its edge a single fine line of ink, drawn round the top.
    const rim = new THREE.Mesh(new THREE.TorusGeometry(7.5, 0.03, 8, 160), mat(INK, { rough: 0.5 }));
    rim.rotation.x = Math.PI / 2;
    rim.position.y = 0.35;
    g.add(dais, rim, contact(19, 19, 0.12));
    const core = workstation("console");
    core.position.set(0, 0.35, -2.2);
    core.rotation.y = 0;
    g.add(core);
    ctx.pickable(core, { kind: "hall", id: "queen", title: queenTitle() });
    // The dais is the core itself: where this core is set up.
    ctx.pickable(dais, { kind: "settings", id: "core", title: "This core" });
    const queen = robot("queen");
    registerActor("queen", queen);
    // She faces you: she is the one you talk to.
    queen.position.set(0.8, 0.35, 1.4);
    queen.userData.base = 0.35;
    queen.rotation.y = Math.PI / 4;
    g.add(queen);
    ctx.pickable(queen, { kind: "hall", id: "queen", title: queenTitle() });
    const queenLab = ctx.label(queen, getQueenName().name, "", 3.4);

    const brain = new THREE.Group();
    for (let i = 0; i < 4; i++) {
      const disk = cyl(1.6, 1.6, 0.55, 64, i % 2 ? FIN.shell() : mat(0xe4e8ed, { rough: 0.35, clear: 0.4 }));
      disk.position.y = 0.35 + i * 0.62;
      const ring = cyl(1.62, 1.62, 0.06, 64, ownMat(0xd8dee6, { emissive: accentColor, glow: 0.0 }), false);
      ring.position.y = 0.35 + i * 0.62 + 0.5;
      brain.add(disk, ring);
    }
    brain.add(contact(5, 5, 0.16));
    brain.position.set(-4.6, 0, 3.4);
    g.add(brain);
    ctx.pickable(brain, { kind: "brain", id: "brain", title: "Shared brain" });
    ctx.label(brain, "CortexDB", translate("shared brain"), 3.4);

    // ── Skills: a wrench over a stand, a small one circling it per skill ──
    const rack = new THREE.Group();
    const skillStand = pedestal(1.3);
    const bigWrench = wrench(0xf2b416, 1.15);
    bigWrench.position.y = 3.2;
    rack.add(skillStand, bigWrench);
    const carts = new THREE.Group();
    carts.position.y = 3;
    rack.add(carts);
    rack.position.set(-9, 0, 13);
    g.add(rack);
    // Skills reach the workers through the core, so a route runs there too.
    const skillRoute = cable(coreAt0(), new THREE.Vector3(-9, 0, 13), 0.7);
    g.add(skillRoute.tube);
    ctx.pickable(skillStand, { kind: "skills", id: "rack", title: "Skills" });
    ctx.pickable(bigWrench, { kind: "skills", id: "rack", title: "Skills" });
    const rackLab = ctx.label(rack, translate("Skills"), "", 5.6);
    let skillSig = "";

    // ── MCP: a plug over a stand; each server a plug of its own out on the
    //    ring, joined to it by a path of light ──────────────────────────────
    const hub = new THREE.Group();
    const hubStand = pedestal(1.4);
    const bigPlug = plug(0x2f8bff, 1.2);
    bigPlug.position.y = 3.2;
    hub.add(hubStand, bigPlug);
    hub.position.set(10, 0, 10);
    g.add(hub);
    ctx.pickable(hubStand, { kind: "mcphub", id: "hub", title: "MCP servers" });
    ctx.pickable(bigPlug, { kind: "mcphub", id: "hub", title: "MCP servers" });
    const hubLab = ctx.label(hub, "MCP", "", 5.2);
    const hubCable = cable(coreAt0(), new THREE.Vector3(10, 0, 10), 0.3);
    g.add(hubCable.tube);
    const services = new THREE.Group();
    g.add(services);
    let mcpSig = "";
    let mcpLabels: Label[] = [];
    // The icons turn slowly and bob, as icons do.
    ctx.tick((_, t) => {
      if (ctx.reduced) return;
      bigWrench.rotation.y = t * 0.6;
      bigWrench.position.y = 3.2 + Math.sin(t * 1.4) * 0.15;
      bigPlug.rotation.y = -t * 0.6;
      bigPlug.position.y = 3.2 + Math.sin(t * 1.4 + 1) * 0.15;
      carts.rotation.y = t * 0.25;
      carts.children.forEach((c, i) => { c.rotation.y = -t * 0.25 + t * 0.8; c.position.y = Math.sin(t * 1.7 + i) * 0.12; });
      services.children.forEach((c, i) => { if (c.userData.icon) { const ic = c.userData.icon as THREE.Object3D; ic.rotation.y = t * 0.7 + i; ic.position.y = 2.3 + Math.sin(t * 1.5 + i) * 0.12; } });
    });

    // ── Tokens are spent as data on the move: packets run both ways along
    //    the spender's path to the core ──────────────────────────────────
    const coins = new Packets(g, ctx.reduced);
    const flames = new Map<string, { e: Link; anchor: THREE.Object3D; last: number }>();
    // The queen's own traffic: between her and the shared brain.
    const queenCurve = new THREE.CatmullRomCurve3([new THREE.Vector3(0.8, 0.1, 1.4), new THREE.Vector3(-2, 0.1, 3.2), new THREE.Vector3(-4.6, 0.1, 3.4)]);
    /** The path an agent's traffic runs on. */
    const pathOf = (name: string, o: THREE.Object3D): THREE.Curve<THREE.Vector3> | null => {
      if (o === queen) return queenCurve;
      const w = workers.get(name);
      if (w) return nodes.get(w.node)?.cable.curve ?? null;
      return machines.get(name)?.cable.curve ?? null;
    };
    const mouthOf = (o: THREE.Object3D) => {
      const p = o.getWorldPosition(new THREE.Vector3());
      return new THREE.Vector3(p.x, p.y + (o === queen ? 3.3 : o.userData.kind ? 2.2 : 2.6), p.z);
    };
    const floaters: { o: CSS2DObject; life: number; at: THREE.Vector3 }[] = [];
    const floatUp = (at: THREE.Vector3, n: number) => {
      if (n < 1) return;
      const el = document.createElement("div");
      el.className = "wl-burn";
      el.textContent = `−${n >= 1000 ? (n / 1000).toFixed(1) + "k" : Math.round(n)} tok`;
      const o = new CSS2DObject(el);
      o.position.copy(at);
      g.add(o);
      floaters.push({ o, life: 0, at: at.clone() });
    };
    const anchorOf = (name: string): THREE.Object3D | null =>
      workers.get(name)?.r ?? (/queen/.test(name) || name === "queen" ? queen : null) ?? machines.get(name)?.dev ?? null;
    ctx.tick((dt) => {
      coins.tick(dt);
      for (let i = floaters.length - 1; i >= 0; i--) {
        const f = floaters[i];
        f.life += dt;
        f.o.position.y = f.at.y + f.life * 1.4;
        (f.o.element as HTMLElement).style.opacity = String(Math.max(0, 1 - f.life / 2.2));
        if (f.life > 2.2) { f.o.removeFromParent(); f.o.element.remove(); floaters.splice(i, 1); }
      }
    });

    // ── Dashboards: a row of holograms, each standing on the light of a
    //    projector on the floor, like the desks' monitors ─────────────────
    const wall = new THREE.Group();
    wall.position.set(-21, 0, -3);
    wall.rotation.y = Math.PI / 2;
    g.add(wall);
    const wallScreens: { node: THREE.Group; sc: Screen; id: string }[] = [];
    for (let i = 0; i < 4; i++) {
      const node = new THREE.Group();
      const sw = 3.7, sh = 2.2, mid = 3.65;
      const bar = projector(2.4, 0.4);
      const sc = screen(sw, sh, 640, true);
      sc.mesh.position.set(0, mid, 0);
      node.add(bar, beam(0.08, mid - sh / 2, 1.9, sw), sc.mesh);
      node.position.set(-6.6 + i * 4.4, 0, 0);
      node.visible = false;
      wall.add(node);
      wallScreens.push({ node, sc, id: "" });
    }

    // ── noted: a board with what today still holds, on its own route from
    //    the core; a noted tool call runs along it ──────────────────────
    // No sign: a projector bar across two posts' tops, and the board a
    // hologram standing on its light.
    const board = new THREE.Group();
    for (const x of [-2.2, 2.2]) {
      const post = cyl(0.07, 0.07, 2.2, 16, FIN.alu(), true);
      const foot = rbox(0.5, 0.06, 0.5, FIN.alu(), 0.03);
      post.position.x = foot.position.x = x;
      board.add(post, foot);
    }
    const boardBar = projector(4.6, 0.3);
    boardBar.position.y = 2.2;
    const boardScreen = screen(5, 3, 640, true);
    boardScreen.mesh.position.set(0, 2.45 + 1.5, 0);
    board.add(boardBar, beam(2.28, 2.45, 4.2, 5), boardScreen.mesh, contact(6.5, 2, 0.14));
    board.position.set(-3.5, 0, -19);
    g.add(board);
    ctx.pickable(board, { kind: "records", id: "records", title: "Today" });
    const boardLab = ctx.label(board, translate("Today"), "", 6);
    const boardCable = cable(coreAt0(), new THREE.Vector3(-3.5, 0, -18), 0.55);
    g.add(boardCable.tube);
    let notedBusy = false, notedAcc = 0;
    ctx.tick((dt) => {
      if (!notedBusy) { notedAcc = 0; return; }
      notedAcc += dt;
      if (notedAcc >= 0.7) { notedAcc = 0; coins.burst(boardCable.curve, 1); }
    });

    // ── What needs you: a mark over the one it is about ─────────────────
    const beacons = new Map<string, { obj: THREE.Group; anchor: THREE.Object3D; lift: number }>();
    let beaconSig = "";

    // ── Bees: small standing agents circling the core ────────────────────
    const bees = new Map<string, { r: THREE.Group; label: Label; seed: number; mode: "orbit" | "rest" }>();

    // ── Nodes and the workers standing on them ───────────────────────────
    const nodes = new Map<string, { box: THREE.Group; label: Label; cable: ReturnType<typeof cable>; at: THREE.Vector3 }>();
    const workers = new Map<string, { r: THREE.Group; label: Label; node: string }>();
    // ── Linked machines and their coding agents ──────────────────────────
    const machines = new Map<string, { dev: THREE.Group; label: Label; cable: ReturnType<typeof cable>; at: THREE.Vector3; clis: Map<string, { r: THREE.Group; label: Label }> }>();
    // ── Relays: the named agents a relay serves (openclaw, hermes, pi), on a
    // pad of their own. Not a machine: no desk, no machine card.
    const relays = new Map<string, { pad: THREE.Group; label: Label; cable: ReturnType<typeof cable>; at: THREE.Vector3; bots: Map<string, { r: THREE.Group; label: Label }> }>();

    // ── Hosts: when the core lists them, each box is a plot of its own with
    // its pods and agents on it (hosts.ts plans where), and the maps above
    // hold what stands on the plots, so orders and tokens find them.
    const hostLayer = new THREE.Group();
    g.add(hostLayer);
    ctx.night(setLinesNight);
    let hostSig = "";
    let hostMode = false;
    let plan: HostPlan | null = null;
    // The HA services as last read: a host known only as their node is
    // called an HA node rather than a host of no kind.
    let svcNow: HAService[] = [];
    const hostTitle = (h: Host) => {
      const r = haRoles(h, svcNow);
      return `${h.name} · ${translate(kindLabel(h.kind, r.runs.length + r.standby.length > 0))}`;
    };
    let hostLabels: Label[] = [];
    let actors: string[] = [];
    // Robots that are not at a desk or on a pad: HA standbys (ghosts), and the
    // CLIs on the core's own host, on the dais.
    const extras = new Map<string, { r: THREE.Group; label: Label; a: HostAgent; host: Host }>();
    const plotViews = new Map<string, { p: Plot; label: Label; cable: ReturnType<typeof cable>; dev?: THREE.Group; crew: Map<string, HostAgent> }>();
    const serviceViews = new Map<string, { m: THREE.Group; label: Label; svc: HAService }>();
    // Every agent standing on a host in this plan, by what it is and where:
    // what a new plan is held against to see who moved.
    let placed: { key: string; host: string; r: THREE.Group }[] = [];
    const stands = (): Stand[] => placed.map(({ key, host, r }) => {
      const w = r.getWorldPosition(new THREE.Vector3());
      return { key, host, x: w.x, y: w.y, z: w.z };
    });
    // Robots on their way from the host they were on to the one they are on
    // now, in their plot's own coordinates.
    const trips = new Map<THREE.Group, { from: THREE.Vector3; to: THREE.Vector3; yaw: number; heading: number; t: number; dur: number }>();
    /** Sets each robot that moved off at its old spot, bound for its new one. */
    const travel = (before: Stand[]) => {
      if (ctx.reduced || !before.length) return;
      for (const mv of movesOf(before, stands())) {
        const r = placed.find((x) => x.key === mv.key)?.r;
        if (!r?.parent) continue;
        const from = r.parent.worldToLocal(new THREE.Vector3(mv.from.x, mv.from.y, mv.from.z));
        const to = new THREE.Vector3(r.position.x, (r.userData.base as number | undefined) ?? r.position.y, r.position.z);
        trips.set(r, { from, to, yaw: r.rotation.y, heading: Math.atan2(to.x - from.x, to.z - from.z), t: 0, dur: tripTime(mv.from, mv.to) });
        r.position.set(from.x, from.y, from.z);
        r.userData.base = from.y;
        r.rotation.y = Math.atan2(to.x - from.x, to.z - from.z);
        r.userData.walking = true;
      }
    };

    const coreAt = new THREE.Vector3(0, 0, -2.2);
    const cables = new THREE.Group();
    g.add(cables);
    const work = new Map<string, Work>();
    const cubeGeo = new THREE.SphereGeometry(0.32, 32, 20);

    const nodeSpot = (i: number, n: number) => {
      const a = THREE.MathUtils.degToRad(n === 1 ? 225 : 150 + (i * 150) / Math.max(1, n - 1));
      return new THREE.Vector3(Math.cos(a) * 14.5, 0, Math.sin(a) * 14.5);
    };
    const machineSpot = (i: number, n: number) => {
      const a = THREE.MathUtils.degToRad(n === 1 ? 45 : 95 - (i * 90) / Math.max(1, n - 1));
      return new THREE.Vector3(Math.cos(a) * 21, 0, Math.sin(a) * 21);
    };
    // Out past the nodes, on the far side from the machines.
    const relaySpot = (i: number, n: number) => {
      const a = THREE.MathUtils.degToRad(n === 1 ? 228 : 205 + (i * 50) / Math.max(1, n - 1));
      return new THREE.Vector3(Math.cos(a) * 23, 0, Math.sin(a) * 23);
    };
    /** A relay's pad: a low disc with a light around its rim. */
    const relayPad = () => {
      const pad = new THREE.Group();
      const disc = cyl(3.6, 3.8, 0.22, 64, 0xf4f6f9);
      disc.receiveShadow = true;
      const rim = new THREE.Mesh(new THREE.TorusGeometry(3.7, 0.06, 8, 96), new THREE.MeshBasicMaterial({ color: LIGHT.idle, toneMapped: false }));
      rim.rotation.x = Math.PI / 2;
      rim.position.y = 0.23;
      pad.add(disc, rim);
      pad.userData.strip = rim;
      return pad;
    };

    /** Who carries an order, and where along which cable it goes. */
    const route = (name: string): { curve: THREE.Curve<THREE.Vector3>; to: THREE.Object3D } | null => {
      const w = workers.get(name);
      if (w) { const n = nodes.get(w.node); if (n) return { curve: n.cable.curve, to: w.r }; }
      const host = name.includes(".") ? name.split(".").slice(1).join(".") : "";
      const cli = name.split(".")[0];
      for (const [h, m] of machines) {
        if (h === host || h === name || h.startsWith(host)) {
          const r = m.clis.get(cli) ?? [...m.clis.values()][0];
          if (r) return { curve: m.cable.curve, to: r.r };
        }
        // A named agent (openclaw) served by this machine.
        const own = m.clis.get(name);
        if (own) return { curve: m.cable.curve, to: own.r };
      }
      // A named agent served by a relay.
      for (const [h, x] of relays) {
        if (host && h !== host && !h.startsWith(host)) continue;
        const b = x.bots.get(cli) ?? x.bots.get(name);
        if (b) return { curve: x.cable.curve, to: b.r };
      }
      return null;
    };

    // What each screen shows; busy ones are repainted for their cursor.
    const displays = new Map<Screen, { title: string; status: string; lines: string[]; busy: boolean; sig: string }>();
    const show = (sc: Screen | undefined, title: string, status: string, lines: string[], busy: boolean) => {
      if (!sc) return;
      const sig = title + status + lines.join("|") + busy;
      const cur = displays.get(sc);
      if (cur?.sig === sig) return;
      displays.set(sc, { title, status, lines, busy, sig });
      paint(sc, title, status, lines, busy, true);
    };
    let blinkAt = 0;

    let calls = -1;
    let queenState: RobotState = "idle";
    let clock = 0;
    const lastCalls = new Map<string, number>();
    // Bits of work rising from the keys to the screen while a robot types.
    const bits = new THREE.Group();
    g.add(bits);
    const bitGeo = new THREE.BoxGeometry(0.12, 0.12, 0.12);
    const bitPool: THREE.Object3D[] = [];
    const bitMat = new THREE.MeshBasicMaterial({ color: accentColor, transparent: true, opacity: 0.9, toneMapped: false });
    let bitAt = 0;
    ctx.tick((dt) => {
      for (const [r, x] of trips) {
        x.t = Math.min(1, x.t + dt / x.dur);
        const p = along(x.from, x.to, x.t);
        r.position.x = p.x; r.position.z = p.z;
        r.userData.base = p.y;
        // Facing the way it goes, turning to its post over the last steps.
        r.rotation.y = turn(x.heading, x.yaw, (x.t - 0.82) / 0.18);
        if (x.t >= 1) {
          trips.delete(r);
          r.userData.walking = false;
          // Arrived: a ring on the floor where it lands.
          ctx.spark(r.getWorldPosition(new THREE.Vector3()).setY(0.2), ROUTE);
        }
      }
    });
    ctx.tick((dt, t) => {
      clock = t;
      flowRoutes(dt, t, ctx.reduced);
      if (!ctx.reduced) {
        if (t - bitAt > 0.18) {
          bitAt = t;
          const typing = [...workers.values()].map((w) => w.r).concat([...machines.values()].flatMap((m) => [...m.clis.values()].map((c) => c.r)))
            .filter((r) => r.userData.desk && r.userData.state === "work");
          for (const r of typing) {
            if (bits.children.length > 60) break;
            const b = (bitPool.pop() as THREE.Mesh | undefined) ?? new THREE.Mesh(bitGeo, bitMat);
            // From under the hands, on the keys.
            const p = r.localToWorld(new THREE.Vector3((Math.random() - 0.5) * 0.8, 0.95, 0.62));
            b.position.copy(p);
            b.userData.v = new THREE.Vector3((Math.random() - 0.5) * 0.3, 1.2 + Math.random() * 0.6, 0).applyQuaternion(r.getWorldQuaternion(new THREE.Quaternion()));
            b.userData.life = 0;
            bits.add(b);
          }
        }
        for (let i = bits.children.length - 1; i >= 0; i--) {
          const b = bits.children[i] as THREE.Mesh;
          b.userData.life += dt;
          b.position.addScaledVector(b.userData.v, dt);
          b.rotation.x += dt * 4; b.rotation.y += dt * 3;
          // Grows in, then shrinks away as it rises: no material per bit.
          const L = b.userData.life;
          b.scale.setScalar(Math.max(0.001, Math.min(1, L * 8) * (1 - smooth(Math.min(1, L)))));
          if (L > 1) { bits.remove(b); bitPool.push(b); }
        }
      }
      for (const b of beacons.values()) {
        const at = b.anchor.getWorldPosition(new THREE.Vector3());
        b.obj.position.set(at.x, at.y + b.lift + (ctx.reduced ? 0 : Math.sin(t * 2.6) * 0.15), at.z);
        if (!ctx.reduced) b.obj.rotation.y = t * 1.4;
      }
      if (t - blinkAt > 0.5) {
        blinkAt = t;
        const on = Math.floor(t * 2) % 2 === 0;
        for (const sc of displays.keys()) sc.caret.visible = on && !!sc.mesh.userData.caret;
      }
      pose(queen, t, ctx.reduced);
      for (const w of workers.values()) pose(w.r, t, ctx.reduced);
      for (const m of machines.values()) for (const c of m.clis.values()) pose(c.r, t, ctx.reduced);
      for (const x of relays.values()) for (const b of x.bots.values()) pose(b.r, t, ctx.reduced);
      for (const x of extras.values()) pose(x.r, t, ctx.reduced);
      for (const x of serviceViews.values()) if (!ctx.reduced) { const gem = x.m.userData.gem as THREE.Object3D; gem.rotation.y = t * 0.9; gem.position.y = (x.m.userData.gemY as number) + Math.sin(t * 1.5) * 0.08; }
      for (const b of bees.values()) {
        pose(b.r, t, ctx.reduced);
        if (b.mode === "orbit" && !ctx.reduced) {
          const a = t * 0.35 + b.seed * 6.28;
          b.r.position.set(Math.cos(a) * 10.5, 4.2 + Math.sin(t * 1.6 + b.seed * 6) * 0.35, Math.sin(a) * 10.5 - 1);
          b.r.rotation.y = -a;
        }
      }
      // Cubes of work along the cables: out to the worker, then into its
      // hands; finished ones ride back to the core.
      for (const [id, w] of work) {
        if (w.t < 1) {
          w.t = Math.min(1, w.t + dt / 1.8);
          const e = smooth(w.t);
          const k = w.back ? 1 - e : e;
          const p = w.curve.getPoint(k);
          w.node.position.set(p.x, 0.45 + Math.sin(w.t * Math.PI) * 0.35, p.z);
          w.node.rotation.y += dt * 4;
          w.node.visible = true;
        } else if (!w.back) {
          w.node.visible = false; // in the robot's hands now
        } else {
          g.remove(w.node);
          work.delete(id);
        }
      }
    });

    const send = (id: string, worker: string) => {
      const r = route(worker);
      if (!r) return;
      const node = new THREE.Mesh(cubeGeo, ownMat(accentColor, { emissive: accentColor, glow: 0.8, rough: 0.3 }));
      node.castShadow = true;
      g.add(node);
      ctx.pickable(node, { kind: "order", id, title: "Order" });
      work.set(id, { node, curve: r.curve, t: 0, back: false, state: "running", to: r.to });
    };
    const settle = (id: string, state: string) => {
      const w = work.get(id);
      if (!w || w.back) return;
      w.back = true;
      w.t = 0;
      const m = w.node.material as THREE.MeshStandardMaterial;
      const c = state === "done" ? 0x22a06b : 0xe2553f;
      m.color.set(c); m.emissive.set(c);
      if (w.to) ctx.spark(w.to.getWorldPosition(new THREE.Vector3()).setY(2.6), c);
    };

    /** Takes down everything standing for a member, machine, relay or host,
     *  for a switch between the two ways of drawing the hive or a new plan. */
    const clearCrew = () => {
      const drop = (c: ReturnType<typeof cable>) => { cables.remove(c.tube); c.tube.userData.dispose?.(); };
      for (const x of nodes.values()) { x.label.remove(); x.box.removeFromParent(); drop(x.cable); }
      nodes.clear();
      for (const [n, w] of workers) { w.label.remove(); w.r.removeFromParent(); forgetActor("worker:" + n); }
      workers.clear();
      for (const x of machines.values()) { x.label.remove(); x.clis.forEach((c) => { c.label.remove(); c.r.removeFromParent(); }); x.dev.removeFromParent(); drop(x.cable); }
      machines.clear();
      for (const [h, x] of relays) { x.label.remove(); x.bots.forEach((b, n) => { b.label.remove(); b.r.removeFromParent(); forgetActor(`agent:${n}.${h}`); }); x.pad.removeFromParent(); drop(x.cable); }
      relays.clear();
      for (const x of extras.values()) { x.label.remove(); x.r.removeFromParent(); }
      extras.clear();
      for (const x of plotViews.values()) { x.label.remove(); drop(x.cable); }
      plotViews.clear();
      for (const x of serviceViews.values()) x.label.remove();
      serviceViews.clear();
      hostLabels.forEach((l) => l.remove());
      hostLabels = [];
      actors.forEach(forgetActor);
      actors = [];
      forgetLines(hostLayer);
      hostLayer.clear();
      plan = null;
      placed = [];
      trips.clear();
    };
    /** Where the line from a plot's centre toward `to` leaves it. */
    const edgeOf = (p: Plot, to: THREE.Vector3, pad = 0.3) => {
      const dir = to.clone().sub(p.at).setY(0);
      if (dir.lengthSq() < 1e-6) return p.at.clone();
      dir.normalize();
      const t = Math.min(Math.abs(dir.x) > 1e-6 ? (p.w / 2) / Math.abs(dir.x) : Infinity, Math.abs(dir.z) > 1e-6 ? (p.d / 2) / Math.abs(dir.z) : Infinity);
      return p.at.clone().addScaledVector(dir, t + pad);
    };
    const trim = (n: string) => TRIMS[Math.floor(hash01(n) * TRIMS.length)];
    const actor = (key: string, r: THREE.Object3D) => { registerActor(key, r); actors.push(key); };

    /** Builds the plots, pods, stations and services for a new plan. */
    const buildHosts = (pl: HostPlan) => {
      plan = pl;
      hostBounds = pl.bounds;
      // HA services, first so a service is found by its marker before its
      // ghosts: a pin with its name and service IP at the edge of the host
      // it runs on, and a dashed line round the outside to each host it can
      // move to, ending in a small ring there.
      for (const sp of pl.services) {
        const m = serviceMarker(sp.height);
        m.position.copy(sp.at);
        hostLayer.add(m);
        ctx.pickable(m, { kind: "service", id: sp.svc.name, title: sp.svc.name });
        for (const b of sp.standbys) {
          hostLayer.add(floorLink(sp.at, b.to, b.ready ? "standby" : "unready", b.bend));
          const sk = socket(b.ready);
          sk.position.add(b.to);
          hostLayer.add(sk);
        }
        serviceViews.set(sp.svc.name, { m, label: ctx.label(m, svcTitle(sp.svc), "", sp.height + 0.75), svc: sp.svc });
      }

      for (const p of pl.plots) {
        const grp = new THREE.Group();
        grp.position.copy(p.at);
        // An HA cluster floats, a cloud with its agents on top.
        const ha = p.host.kind === "ha";
        if (ha) grp.position.y += CLOUD_LIFT;
        hostLayer.add(grp);
        const sl = ha ? cloud(p.w, p.d, p.host.state) : slab(p.w, p.d, p.host.state);
        grp.add(sl);
        ctx.pickable(sl, { kind: "host", id: p.host.id, title: p.host.name });
        // Its name and kind at the front edge, on the floor; a cluster's says
        // which node runs it now.
        const plate = new THREE.Object3D();
        plate.position.set(0, ha ? -0.9 : 0.3, p.d / 2 + 0.4);
        grp.add(plate);
        const label = ctx.label(plate, hostTitle(p.host), ha && p.host.on ? translate("on {name}", { name: p.host.on }) : "", 0);
        const c = cable(coreAt, edgeOf(p, new THREE.Vector3()), hash01(p.host.id));
        c.setOk(p.host.state === "live");
        cables.add(c.tube);
        nodes.set(p.host.id, { box: grp, label, cable: c, at: p.at });
        const crew = new Map<string, HostAgent>();
        const clis = new Map<string, { r: THREE.Group; label: Label }>();
        let dev: THREE.Group | undefined;
        if (p.station) {
          dev = p.station === "pad" ? relayPad() : workstation(p.station === "laptop" ? "laptop" : "desktop");
          if (p.small) dev.scale.setScalar(0.75);
          const at = new THREE.Vector3(p.cols.station, SLAB_TOP, p.station === "pad" ? 0 : p.small ? -1 : -1.9);
          dev.position.copy(at);
          grp.add(dev);
          ctx.pickable(dev, { kind: "host", id: p.host.id, title: p.host.name });
          p.crew.forEach((a, k) => {
            const n = a.kind === "cli" ? cliName(a) : a.name;
            const r = robot("cli", trim(n));
            grp.add(r);
            const via = a.via || p.via;
            ctx.pickable(r, { kind: a.kind === "cli" ? "cli" : "agent", id: `${n}.${via}`, title: `${n} · ${p.host.name}` });
            let pos: THREE.Vector3, face: THREE.Vector3;
            if (p.station === "pad") {
              // In a ring on the pad, facing out.
              const ang = (k / Math.max(1, p.crew.length)) * Math.PI * 2 + Math.PI / 2;
              pos = new THREE.Vector3(Math.cos(ang) * 2.1, 0.22, Math.sin(ang) * 2.1).add(at);
              face = pos.clone().sub(at).multiplyScalar(2).add(at);
            } else {
              // In a row before the desk, turned to its screen.
              const spread = Math.min(2.4, 4.8 / Math.max(1, p.crew.length - 1));
              const ang = (k - (p.crew.length - 1) / 2) * spread * 0.32;
              pos = new THREE.Vector3(Math.sin(ang) * 4.2, 0, 2.6 + Math.cos(ang) * 1.4).add(at);
              face = at.clone().add(new THREE.Vector3(0, 0, -0.6));
              r.userData.desk = true;
            }
            r.position.copy(pos);
            r.userData.base = pos.y;
            r.rotation.y = Math.atan2(face.x - pos.x, face.z - pos.z);
            clis.set(n, { r, label: ctx.label(r, n, "", k % 2 ? 2.9 : 2.1) });
            crew.set(n, a);
            placed.push({ key: `${a.kind}:${n}`, host: p.host.id, r });
            actor((a.kind === "cli" ? "cli:" : "agent:") + `${n}.${via}`, r);
            if (a.kind !== "cli") actor("agent:" + n, r);
          });
        }
        // Its pods, each with its SuperAI standing inside.
        p.pods.forEach((h, i) => {
          const pd = pod(h.state);
          pd.position.set(p.cols.pods[i], SLAB_TOP, p.station && !p.small ? 1.2 : 0.2);
          grp.add(pd);
          ctx.pickable(pd, { kind: "host", id: h.id, title: h.name });
          const sup = h.agents.filter((a) => a.kind === "superai").slice(0, 2);
          sup.forEach((a, k) => {
            const r = robot("worker");
            r.position.set(pd.position.x + (sup.length > 1 ? (k - 0.5) * 1.3 : 0), SLAB_TOP + 0.2, pd.position.z);
            r.userData.base = r.position.y;
            grp.add(r);
            ctx.pickable(r, { kind: "worker", id: a.name, title: a.name });
            workers.set(a.name, { r, label: ctx.label(r, short(a.name), "", 2.4), node: p.host.id });
            placed.push({ key: `superai:${a.name}`, host: p.host.id, r });
            actor("worker:" + a.name, r);
          });
        });
        // What stands on the plot itself.
        p.loose.forEach((a, k) => {
          const x = p.cols.loose + k * 2.4;
          const z = p.station ? 1.6 : 0.4;
          if (a.kind === "superai") {
            const r = robot("worker");
            r.position.set(x, SLAB_TOP, z);
            r.userData.base = SLAB_TOP;
            grp.add(r);
            ctx.pickable(r, { kind: "worker", id: a.name, title: a.name });
            workers.set(a.name, { r, label: ctx.label(r, short(a.name), "", 2.4), node: p.host.id });
            placed.push({ key: `superai:${a.name}`, host: p.host.id, r });
            actor("worker:" + a.name, r);
            return;
          }
          const n = a.kind === "cli" ? cliName(a) : a.name;
          const r = robot("cli", trim(n));
          r.position.set(x, SLAB_TOP, z);
          r.userData.base = SLAB_TOP;
          grp.add(r);
          if (isStandby(a)) {
            // Where an HA service would move to: its agent as a ghost.
            ghost(r, a.state === "standby-unready");
            ctx.pickable(r, { kind: "service", id: a.service || a.name, title: a.service || a.name });
            extras.set(`${a.name}@${p.host.id}`, { r, label: ctx.label(r, n, "", k % 2 ? 2.9 : 2.1), a, host: p.host });
            return;
          }
          const via = a.via || p.via;
          // On a cluster, the agent by its own name: whichever node runs it.
          const id = ha ? n : `${n}.${via}`;
          ctx.pickable(r, { kind: a.kind === "cli" ? "cli" : "agent", id, title: ha ? n : `${n} · ${p.host.name}` });
          clis.set(n, { r, label: ctx.label(r, n, "", k % 2 ? 2.9 : 2.1) });
          crew.set(n, a);
          placed.push({ key: `${a.kind}:${n}`, host: p.host.id, r });
          actor((a.kind === "cli" ? "cli:" : "agent:") + id, r);
          if (a.kind !== "cli" && !ha) actor("agent:" + n, r);
        });
        if (dev || clis.size) machines.set(p.station ? p.via : p.host.name, { dev: dev ?? grp, label, cable: c, at: p.at, clis });
        plotViews.set(p.host.id, { p, label, cable: c, dev, crew });
      }

      // The core's own host is the dais: its name on the front edge, a thin
      // line to the node it runs inside, its CLIs on the dais.
      if (pl.core) {
        const plate = new THREE.Object3D();
        plate.position.set(0, 0.45, 7.9);
        hostLayer.add(plate);
        hostLabels.push(ctx.label(plate, `${pl.core.name} · ${translate(kindLabel(pl.core.kind))}`, pl.coreParent ? translate("on {name}", { name: pl.coreParent.host.name }) : translate("this core"), 0));
        if (pl.coreParent) {
          const to = edgeOf(pl.coreParent, new THREE.Vector3());
          const from = to.clone().setY(0).normalize().multiplyScalar(7.7);
          hostLayer.add(floorLink(from, to, "parent"));
        }
        pl.coreCrew.slice(0, 3).forEach((a, k) => {
          const n = a.kind === "cli" ? cliName(a) : a.name;
          const r = robot("cli", trim(n));
          const ang = THREE.MathUtils.degToRad(4 + k * 20);
          r.position.set(Math.cos(ang) * 5.7, 0.35, Math.sin(ang) * 5.7);
          r.userData.base = 0.35;
          r.rotation.y = Math.atan2(-r.position.x, -r.position.z) + Math.PI;
          hostLayer.add(r);
          ctx.pickable(r, { kind: a.kind === "cli" ? "cli" : "agent", id: n, title: n });
          extras.set(`${a.name}@core`, { r, label: ctx.label(r, n, "", k % 2 ? 2.9 : 2.1), a, host: pl.core! });
          placed.push({ key: `${a.kind}:${n}`, host: pl.core!.id, r });
          actor((a.kind === "cli" ? "cli:" : "agent:") + n, r);
        });
      }

    };

    /** A robot's state from what it is doing for core and in its terminals,
     *  with what its host last said when nothing else does. */
    const crewState = (d: WorldData, n: string, host: string, term: Record<string, CCActivity>, a: HostAgent | undefined, run: HiveTask[], asking: string[]) => {
      const runs = d.runs.filter((r) => (r.remote || "core") === host && (r.agent === n || r.agent.startsWith(n + ".")));
      const live = runs.filter((r) => r.state === "running");
      const delegated = run.filter((t) => t.worker === `${n}.${host}` || t.worker === n || t.worker.startsWith(n + "."));
      const asks = asking.some((s) => s === `${n}.${host}` || s === n || s.startsWith(n + "."));
      const own = termState(term[n]);
      // A named agent the process detector also sees run carries what that
      // CLI is doing.
      const said = a?.activity || a?.state;
      const st: RobotState = asks ? "wait"
        : live.length || delegated.length || own.work || (!term[n] && said === "working") ? "work"
        : own.wait || (!term[n] && said === "waiting") ? "wait"
        : runs[0]?.state === "failed" ? "fail" : said === "lost" ? "lost" : "idle";
      const status = asks ? translate("needs your OK") : live.length ? clip(live[0].prompt, 24) : delegated.length ? clip(delegated[0].prompt, 24)
        : own.status || (st === "work" ? translate("working") : st === "wait" ? translate("waiting for you") : st === "fail" ? translate("failed")
          : st === "lost" ? translate("not answering") : said === "configured" ? translate("configured") : translate("idle"));
      return { st, status, live: live.length };
    };
    const tone = (st: RobotState) => (st === "wait" ? "wait" : st === "work" ? "work" : st === "fail" || st === "lost" ? "bad" : "idle") as "wait" | "work" | "bad" | "idle";

    /** The hive by host: a new plan when what stands where changes, and
     *  every robot, screen and label set from the newest figures. */
    const updateHosts = (d: WorldData, run: HiveTask[], failed: Set<string>, asking: string[]) => {
      const hosts = d.hosts ?? [];
      const svcs = d.services ?? [];
      svcNow = svcs;
      const sig = JSON.stringify([
        hosts.map((h) => [h.id, h.name, h.kind, h.parent, h.state, h.os, h.agents.map((a) => [a.name, a.kind, a.role, a.via, isStandby(a) ? a.state : ""])]),
        svcs.map((s) => [s.name, s.active, s.standbys]),
      ]);
      if (sig !== hostSig || !plan) {
        // Where everyone stood, so whoever is on another host now walks there.
        const before = plan ? stands() : [];
        hostSig = sig; clearCrew(); buildHosts(planHosts(hosts, svcs));
        travel(before);
      }
      const agentOf = new Map<string, HostAgent>();
      for (const h of hosts) for (const a of h.agents) if (a.kind === "superai") agentOf.set(a.name, a);

      // Workers, in their pods.
      for (const [name, w] of workers) {
        const m = d.hive.members.find((x) => x.name === name);
        const a = agentOf.get(name);
        const mine = run.filter((x) => x.worker === name);
        const pulse = d.meter.members.find((x) => x.name === name);
        if (pulse) {
          const was = lastCalls.get(name);
          if (was !== undefined && pulse.calls > was) { w.r.userData.gestureUntil = clock + 0.9; ctx.spark(w.r.getWorldPosition(new THREE.Vector3()).setY(3.2)); }
          lastCalls.set(name, pulse.calls);
        }
        const lost = (m?.state ?? a?.state) === "lost";
        const st: RobotState = lost ? "lost" : mine.length ? (mine[0].tool ? "work" : "think") : failed.has(name) ? "fail" : pulse?.live ? "think" : "idle";
        setState(w.r, st);
        if (st === "think" && mine.length) (w.r.userData.carry as THREE.Object3D).visible = true;
        w.label.set(short(name), st === "lost" ? translate("not answering") : mine.length ? clip(mine[0].tool ? `${mine[0].tool} · ${mine[0].prompt}` : mine[0].prompt, 28) : st === "fail" ? translate("failed") : translate("idle"), tone(st));
      }

      // Each plot: its station's screen and light, the agents at it, its label.
      for (const v of plotViews.values()) {
        const p = v.p;
        const la = d.linked.find((x) => x.name === p.via) ?? d.hive.relays?.find((x) => x.name === p.via);
        const term = la ? clisOf(la) : {};
        const m = machines.get(p.station ? p.via : p.host.name);
        let busy = false;
        for (const [n, c] of m?.clis ?? []) {
          const s = crewState(d, n, p.via, term, v.crew.get(n), run, asking);
          busy ||= s.st === "work";
          setState(c.r, s.st);
          c.label.set(n, s.status, tone(s.st));
        }
        const runsHere = d.runs.filter((r) => (r.remote || "core") === p.via);
        const liveHere = runsHere.filter((r) => r.state === "running");
        const onNode = run.some((x) => workers.get(x.worker)?.node === p.host.id);
        v.cable.setActive(onNode || liveHere.length > 0 || run.some((t) => route(t.worker)?.curve === v.cable.curve));
        if (v.dev?.userData.strip) setLight(v.dev, p.host.state === "lost" ? "lost" : busy || liveHere.length ? "busy" : "idle");
        const screens = v.dev?.userData.screens as Screen[] | undefined;
        if (screens?.[0]) {
          const running = Object.keys(term);
          const ccOn = running.flatMap((c) => (term[c].sessions ?? []).filter((s) => s.state !== "idle").map((s) => ({ s, c })));
          const said = [liveHere.length ? translate("{count} running", { count: liveHere.length }) : "",
            ...running.filter((c) => term[c].working || term[c].waiting).slice(0, 2).map((c) => `${c} · ${termState(term[c]).status}`)].filter(Boolean);
          const ready = m?.clis.size ?? 0;
          show(screens[0], p.host.name, p.host.state === "lost" ? translate("not answering") : said.length ? said.join(" · ") : translate(ready === 1 ? "1 agent ready" : "{count} agents ready", { count: ready }),
            [...ccOn.slice(0, 4).map(({ s, c }) => `${c} · ${sessionLine(s, c)}`), ...(liveHere.length ? liveHere : ccOn.length ? [] : runsHere.slice(0, 4)).map((r) => `${r.agent}: ${r.prompt}`)],
            liveHere.length > 0 || ccOn.some(({ s }) => s.state === "working"));
        }
        const pods = p.pods.length;
        const agents = p.host.agents.filter((a) => !isStandby(a)).length + p.pods.reduce((n, h) => n + h.agents.length, 0);
        // A cluster says which node runs it now.
        const status = p.host.kind === "ha" ? (p.host.on ? translate("on {name}", { name: p.host.on }) : translate("not running"))
          : p.host.state === "lost" ? translate("not answering") : p.host.state === "configured" ? unlinkedStatus(p.host, svcs, translate) || translate("not linked")
          : [pods ? translate(pods === 1 ? "1 pod" : "{count} pods", { count: pods }) : "", translate(agents === 1 ? "1 agent" : "{count} agents", { count: agents })].filter(Boolean).join(" · ");
        v.label.set(hostTitle(p.host), status, p.host.state === "lost" ? "bad" : onNode || busy || liveHere.length ? "work" : "idle");
      }

      // Ghosts and the CLIs on the dais.
      for (const x of extras.values()) {
        const n = x.a.kind === "cli" ? cliName(x.a) : x.a.name;
        if (isStandby(x.a)) {
          setState(x.r, "idle");
          x.label.set(n, x.a.state === "standby" ? translate("standby") : translate("standby · not ready"), x.a.state === "standby" ? "idle" : "bad");
          continue;
        }
        const s = crewState(d, n, "core", {}, x.a, run, asking);
        setState(x.r, s.st);
        x.label.set(n, s.status, tone(s.st));
      }

      // Services: what they run, at which address, and how they stand.
      for (const v of serviceViews.values()) {
        const s = svcs.find((x) => x.name === v.svc.name) ?? v.svc;
        setServiceState(v.m, s.state);
        const sb = s.standbys.map((b) => (b.ready ? b.host : `${b.host} (${translate("not ready")})`)).join(", ");
        // Running with a standby ready, the pin and its dashed lines say it
        // all; the label says more only when something is wrong.
        const st = s.state === "active" ? ""
          : s.state === "down" ? translate("down") : [translate(s.state), sb ? translate("standby {names}", { names: sb }) : ""].filter(Boolean).join(" · ");
        v.label.set(svcTitle(s), st, s.state === "down" ? "bad" : s.state === "degraded" ? "wait" : "idle");
      }
    };

    return {
      update(d) {
        const run = d.hive.tasks.filter((x) => x.state === "running");
        const failed = new Set(d.attention.filter((a) => a.kind === "failed" || a.kind === "lost").map((a) => a.title.split(" ")[0]));
        const asking = d.attention.filter((a) => a.kind === "approval").map((a) => a.title.split(" asks")[0].split(" · ")[0].trim());
        // The hosts, where the core lists them; else the hive the old way,
        // from the roster, the linked machines and the relays.
        const byHost = Array.isArray(d.hosts) && d.hosts.length > 0;
        if (byHost !== hostMode) { clearCrew(); hostMode = byHost; hostSig = ""; }
        if (byHost) updateHosts(d, run, failed, asking);
        else {
          // Nodes: from where the workers say they run; a worker that does not
          // say gets a node of its own, so it still has somewhere to stand.
          const members = d.hive.members.filter((m) => !/queen/.test(m.name));
          const nodeOf = (m: { name: string; node?: string }) => m.node || "node-" + short(m.name);
          const names = [...new Set(members.map(nodeOf))].sort();
          for (const [n, x] of nodes) if (!names.includes(n)) { x.label.remove(); g.remove(x.box); cables.remove(x.cable.tube); x.cable.tube.userData.dispose?.(); nodes.delete(n); }
          names.forEach((n, i) => {
            let x = nodes.get(n);
            const at = nodeSpot(i, names.length);
            if (!x || !x.at.equals(at)) {
              if (x) { x.label.remove(); g.remove(x.box); cables.remove(x.cable.tube); x.cable.tube.userData.dispose?.(); }
              const b = workstation("desktop");
              b.position.copy(at);
              b.rotation.y = 0;
              g.add(b);
              const c = cable(coreAt, at, hash01(n));
              cables.add(c.tube);
              const plate = new THREE.Object3D();
              plate.position.set(4, 2.6, 0.4);
              b.add(plate);
              x = { box: b, label: ctx.label(plate, n, translate("node"), 0), cable: c, at };
              nodes.set(n, x);
              ctx.pickable(b, { kind: "node", id: n, title: n });
            }
          });

          // Workers on their nodes.
          for (const [n, w] of workers) if (!members.some((m) => m.name === n)) { w.label.remove(); g.remove(w.r); workers.delete(n); forgetActor("worker:" + n); }
          const perNode = new Map<string, number>();
          const count = new Map<string, number>();
          members.forEach((m) => count.set(nodeOf(m), (count.get(nodeOf(m)) ?? 0) + 1));
          members.forEach((m) => {
            const node = nodeOf(m);
            const nx = nodes.get(node);
            if (!nx) return;
            let w = workers.get(m.name);
            if (!w) {
              const r = robot("worker");
              g.add(r);
              ctx.pickable(r, { kind: "worker", id: m.name, title: m.name });
              w = { r, label: ctx.label(r, short(m.name), "", 2.4), node };
              workers.set(m.name, w);
              registerActor("worker:" + m.name, r);
            }
            w.node = node;
            const k = perNode.get(node) ?? 0;
            perNode.set(node, k + 1);
            // At the desk, turned to the screen: one in the middle, two side by side.
            const n = count.get(node)!;
            const spots = nx.box.userData.spots as THREE.Vector3[];
            const spot = n === 1 ? spots[0] : n === 2 ? [spots[1], spots[2]][k % 2] : spots[k % 3];
            const world = nx.box.localToWorld(spot.clone());
            w.r.position.copy(world);
            w.r.userData.base = world.y;
            w.r.userData.desk = true;
            w.r.lookAt(nx.box.localToWorld(new THREE.Vector3(spot.x, 0, -0.6)).setY(world.y));
            const mine = run.filter((x) => x.worker === m.name);
            const pulse = d.meter.members.find((x) => x.name === m.name);
            // Every tool call: a hand up and a spark.
            if (pulse) {
              const was = lastCalls.get(m.name);
              if (was !== undefined && pulse.calls > was) {
                w.r.userData.gestureUntil = clock + 0.9;
                ctx.spark(w.r.getWorldPosition(new THREE.Vector3()).setY(3.2));
              }
              lastCalls.set(m.name, pulse.calls);
            }
            const st: RobotState = m.state === "lost" ? "lost" : mine.length ? (mine[0].tool ? "work" : "think") : failed.has(m.name) ? "fail" : pulse?.live ? "think" : "idle";
            setState(w.r, st);
            if (st === "think" && mine.length) (w.r.userData.carry as THREE.Object3D).visible = true;
            nx.cable.setActive(run.some((x) => workers.get(x.worker)?.node === node));
            w.label.set(short(m.name), st === "lost" ? translate("not answering") : mine.length ? clip(mine[0].tool ? `${mine[0].tool} · ${mine[0].prompt}` : mine[0].prompt, 28) : st === "fail" ? translate("failed") : translate("idle"),
              st === "lost" || st === "fail" ? "bad" : st === "work" || st === "think" ? "work" : "idle");
          });

          // Each node's screen: its workers and what they are doing.
          for (const [n, x] of nodes) {
            const on = members.filter((m) => nodeOf(m) === n);
            const doing = on.flatMap((m) => run.filter((t) => t.worker === m.name).map((t) => `${short(m.name)}: ${t.tool ? t.tool + " · " : ""}${t.prompt}`));
            const lost = on.some((m) => m.state === "lost");
            setLight(x.box, lost ? "lost" : doing.length ? "busy" : "idle");
            show((x.box.userData.screens as Screen[])[0], n, lost ? translate("a worker is not answering") : doing.length ? translate(doing.length > 1 ? "{count} orders running" : "1 order running", { count: doing.length }) : translate("idle"),
              doing.length ? doing : on.map((m) => `${short(m.name)} · ${m.state === "lost" ? translate("lost") : translate("ready")}`), doing.length > 0);
          }

          // Linked machines and the coding agents on them. A machine with
          // nothing to run is still one to look after, so every one is here.
          const linked = d.linked;
          for (const [h, x] of machines) if (!linked.some((a) => a.name === h)) {
            x.label.remove(); x.clis.forEach((c) => c.label.remove()); g.remove(x.dev); cables.remove(x.cable.tube); x.cable.tube.userData.dispose?.(); machines.delete(h);
          }
          linked.forEach((a, i) => {
            let x = machines.get(a.name);
            const at = machineSpot(i, linked.length);
            if (!x) {
              const dev = workstation(/mac|darwin/i.test(a.name + a.os) ? "laptop" : "desktop");
              dev.position.copy(at);
              dev.rotation.y = 0;
              g.add(dev);
              const c = cable(coreAt, at, hash01(a.name));
              cables.add(c.tube);
              ctx.pickable(dev, { kind: "machine", id: a.name, title: a.name });
              x = { dev, label: ctx.label(dev, a.name, `${a.os} ${a.arch}`, 4.6), cable: c, at, clis: new Map() };
              machines.set(a.name, x);
            }
            // The best-known agents first, so a machine with a dozen CLIs shows
            // the ones people look for.
            const FIRST = ["claude", "codex", "pi", "openclaw", "opencode", "hermes"];
            const rank = (n: string) => { const i = FIRST.indexOf(n); return i < 0 ? 99 : i; };
            // The terminal sessions people run on it, every coding CLI's: work
            // that is not SuperAI's, done by the same CLIs.
            const term = clisOf(a);
            const running = Object.keys(term);
            const ccWork = running.reduce((n, c) => n + (term[c].working ?? 0), 0);
            const ccOn = running.flatMap((c) => (term[c].sessions ?? []).filter((s) => s.state !== "idle").map((s) => ({ s, c })));
            const names = [...new Set([...(a.clis ?? []), ...(a.agents ?? []).map((n) => n.name), ...running])];
            // The ones running first, then the best known.
            const who = names.sort((x, y) => (term[y] ? 1 : 0) - (term[x] ? 1 : 0) || rank(x) - rank(y)).slice(0, 6);
            for (const [n, c] of x.clis) if (!who.includes(n)) { c.label.remove(); g.remove(c.r); x.clis.delete(n); }
            who.forEach((n, k) => {
              let c = x!.clis.get(n);
              if (!c) {
                const r = robot("cli", TRIMS[Math.floor(hash01(n) * TRIMS.length)]);
                g.add(r);
                const isCli = (a.clis ?? []).includes(n);
                ctx.pickable(r, { kind: isCli ? "cli" : "agent", id: isCli ? `${n}.${a.name}` : `${n}.${a.name}`, title: `${n} · ${a.name}` });
                c = { r, label: ctx.label(r, n, "", k % 2 ? 2.9 : 2.1) };
                x!.clis.set(n, c);
                registerActor((isCli ? "cli:" : "agent:") + `${n}.${a.name}`, r);
                if (!isCli) registerActor("agent:" + n, r);
              }
              // In a row in front of the machine.
              const spread = Math.min(2.4, 4.8 / Math.max(1, who.length - 1));
              const ang = (k - (who.length - 1) / 2) * spread * 0.32;
              const local = new THREE.Vector3(Math.sin(ang) * 4.2, 0, 2.6 + Math.cos(ang) * 1.4);
              const world = x!.dev.localToWorld(local.clone()).setY(0);
              c.r.position.copy(world);
              c.r.userData.base = 0;
              c.r.userData.desk = true;
              c.r.lookAt(x!.dev.localToWorld(new THREE.Vector3(0, 0, -0.6)).setY(0));
              const runs = d.runs.filter((r) => (r.remote || "core") === a.name && (r.agent === n || r.agent.startsWith(n + ".")));
              const live = runs.filter((r) => r.state === "running");
              const delegated = run.filter((t) => t.worker === `${n}.${a.name}` || t.worker === n || t.worker.startsWith(n + "."));
              const asks = asking.some((s) => s === `${n}.${a.name}` || s.startsWith(n + "."));
              const own = termState(term[n]);
              const st: RobotState = asks ? "wait"
                : live.length || delegated.length || own.work ? "work" : own.wait ? "wait" : runs[0]?.state === "failed" ? "fail" : "idle";
              setState(c.r, st);
              const status = asks ? "needs your OK" : live.length ? clip(live[0].prompt, 24) : delegated.length ? clip(delegated[0].prompt, 24)
                : own.status || (st === "fail" ? "failed" : "idle");
              c.label.set(n, status, st === "wait" ? "wait" : st === "work" ? "work" : st === "fail" ? "bad" : "idle");
            });
            const runsHere = d.runs.filter((r) => (r.remote || "core") === a.name);
            const liveHere = runsHere.filter((r) => r.state === "running");
            x.cable.setActive(liveHere.length > 0 || run.some((t) => route(t.worker)?.curve === x!.cable.curve));
            setLight(x.dev, liveHere.length || ccWork ? "busy" : "idle");
            // The screen: SuperAI's runs, and the terminal sessions beside them,
            // each by its project and the tool it is on.
            const said = [
              liveHere.length ? `${liveHere.length} running` : "",
              ...running.filter((c) => term[c].working || term[c].waiting).slice(0, 2).map((c) => {
                const s = termState(term[c]);
                return `${c} · ${s.status}`;
              }),
            ].filter(Boolean);
            show((x.dev.userData.screens as Screen[])[0], a.name, said.length ? said.join(" · ") : `${who.length} agents ready`,
              [...ccOn.slice(0, 4).map(({ s, c }) => `${c} · ${sessionLine(s, c)}`), ...(liveHere.length ? liveHere : ccOn.length ? [] : runsHere.slice(0, 4)).map((r) => `${r.agent}: ${r.prompt}`)],
              liveHere.length > 0 || ccWork > 0);
          });

          // Relays and the named agents they serve, each robot with what runs
          // as it on the relay: working while its processes are busy.
          const rl: RelayAgent[] = (d.hive.relays ?? []).filter((r) => r?.name && (r.agents ?? []).length > 0);
          for (const [h, x] of relays) if (!rl.some((r) => r.name === h)) {
            x.label.remove(); x.bots.forEach((b, n) => { b.label.remove(); g.remove(b.r); forgetActor(`agent:${n}.${h}`); });
            g.remove(x.pad); cables.remove(x.cable.tube); x.cable.tube.userData.dispose?.(); relays.delete(h);
          }
          rl.forEach((rel, i) => {
            const at = relaySpot(i, rl.length);
            let x = relays.get(rel.name);
            if (x && !x.at.equals(at)) { x.pad.position.copy(at); }
            if (!x) {
              const pad = relayPad();
              pad.position.copy(at);
              g.add(pad);
              const c = cable(coreAt, at, hash01(rel.name));
              cables.add(c.tube);
              const plate = new THREE.Object3D();
              plate.position.set(0, 3.4, -2.6);
              pad.add(plate);
              x = { pad, label: ctx.label(plate, "", rel.name, 0), cable: c, at, bots: new Map() };
              relays.set(rel.name, x);
            }
            x.at = at;
            const names = (rel.agents ?? []).map((n) => n.name).slice(0, 6);
            x.label.set(names.join(" · "), rel.name, "idle");
            for (const [n, b] of x.bots) if (!names.includes(n)) { b.label.remove(); g.remove(b.r); x.bots.delete(n); forgetActor(`agent:${n}.${rel.name}`); }
            const term = rel.cliActivity ?? {};
            let busyHere = false;
            names.forEach((n, k) => {
              let b = x!.bots.get(n);
              if (!b) {
                const r = robot("cli", TRIMS[Math.floor(hash01(n) * TRIMS.length)]);
                g.add(r);
                ctx.pickable(r, { kind: "agent", id: `${n}.${rel.name}`, title: `${n} · ${rel.name}` });
                b = { r, label: ctx.label(r, n, "", k % 2 ? 2.9 : 2.1) };
                x!.bots.set(n, b);
                registerActor(`agent:${n}.${rel.name}`, r);
                registerActor("agent:" + n, r);
              }
              // In a ring on the pad, facing out to the core.
              const ang = (k / Math.max(1, names.length)) * Math.PI * 2 + Math.PI / 2;
              const world = new THREE.Vector3(Math.cos(ang) * 2.1, 0.22, Math.sin(ang) * 2.1).add(at);
              b.r.position.copy(world);
              b.r.userData.base = 0.22;
              b.r.lookAt(coreAt.clone().setY(0.22));
              const delegated = run.filter((t) => t.worker === `${n}.${rel.name}` || t.worker === n || t.worker.startsWith(n + "."));
              const asks = asking.some((s) => s === `${n}.${rel.name}` || s === n || s.startsWith(n + "."));
              const own = termState(term[n]);
              const st: RobotState = asks ? "wait" : delegated.length || own.work ? "work" : own.wait ? "wait" : "idle";
              busyHere = busyHere || st === "work";
              setState(b.r, st);
              b.label.set(n, asks ? translate("needs your OK") : delegated.length ? clip(delegated[0].prompt, 24) : own.status || translate("idle"),
                st === "wait" ? "wait" : st === "work" ? "work" : "idle");
            });
            setLight(x.pad, busyHere ? "busy" : "idle");
            x.cable.setActive(run.some((t) => route(t.worker)?.curve === x!.cable.curve));
          });
        }

        // Bees: one small robot each, circling while out, resting on the dais
        // when not; one that waits for you hovers at the queen's side with its
        // hand up.
        const ids = d.bees.map((b) => b.id);
        for (const [id, b] of bees) if (!ids.includes(id)) { b.label.remove(); g.remove(b.r); bees.delete(id); forgetActor("bee:" + id); }
        d.bees.forEach((info, i) => {
          let b = bees.get(info.id);
          if (!b) {
            const r = robot("bee");
            g.add(r);
            ctx.pickable(r, { kind: "bee", id: info.id, title: info.name });
            b = { r, label: ctx.label(r, info.name, "", 1.6), seed: hash01(info.id), mode: "rest" };
            bees.set(info.id, b);
            registerActor("bee:" + info.id, r);
          }
          b.mode = info.running && !info.waitingFor ? "orbit" : "rest";
          if (b.mode === "rest") {
            const a = THREE.MathUtils.degToRad(55 + i * 38);
            const p = new THREE.Vector3(Math.cos(a) * 6.3, info.waitingFor ? 2.4 : 0.35, Math.sin(a) * 6.3);
            b.r.position.copy(p);
            b.r.userData.base = p.y;
            b.r.rotation.y = -a - Math.PI / 2;
          }
          setState(b.r, info.waitingFor ? "wait" : info.running ? "work" : info.paused ? "lost" : "idle");
          const next = info.nextDue ? new Date(info.nextDue) : null;
          const at = next && !isNaN(+next) ? next.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }) : "";
          b.label.set(info.name, info.waitingFor ? translate("waiting for you") : info.running ? translate("out now") : info.paused ? translate("paused") : at ? translate("next {at}", { at }) : translate("resting"),
            info.waitingFor ? "wait" : info.running ? "work" : "idle");
        });

        // Orders: a cube per running order, out along the cable; when it
        // ends, back to the core in the colour of how it went.
        for (const x of d.hive.tasks) {
          if ((x.dir ?? "out") !== "out") continue;
          if (x.state === "running" && !work.has(x.id)) send(x.id, x.worker);
          else if (x.state !== "running") settle(x.id, x.state);
        }

        // The queen: thinking while a turn runs, working while tools do.
        const live = d.busy || d.meter.total.live;
        queenState = asking.includes("SuperAI") || asking.includes("queen") ? "wait" : live ? (run.length ? "work" : "think") : "idle";
        setState(queen, queenState);
        if (calls >= 0 && d.meter.total.calls > calls) { ctx.spark(new THREE.Vector3(0.8, 4.6, 1.4)); queen.userData.gestureUntil = clock + 0.9; }
        calls = d.meter.total.calls;
        const doing = d.meter.members.find((m) => m.live && m.doing)?.doing;
        queenLab.set(queenLabel(d.hive.engine, d.hive.engineNow), live ? clip(doing || translate("thinking"), 26) : translate("idle"), live ? "work" : queenState === "wait" ? "wait" : "idle");
        setLight(core, live ? "busy" : "idle");
        const [s1, s2, s3] = core.userData.screens as Screen[];
        const fmt = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));
        show(s1, translate("hive tokens"), `${fmt(d.meter.total.tokens)}`, [`${Math.round(d.meter.total.tokPerSec)} tok/s`, translate("{count} tool calls", { count: d.meter.total.calls })], d.meter.total.tokPerSec > 0.5);
        show(s2, getQueenName().name, live ? translate("working") : translate("idle"), live ? [doing || translate("thinking…")] : [translate("say something below")], live);
        show(s3, translate("orders out"), String(run.length), run.slice(0, 5).map((t) => `${short(t.worker)}: ${t.prompt}`), run.length > 0);
        // Marks over whatever needs you, each tied to the one it is about.
        const needs = d.attention.filter((a) => a.level === "needs");
        const bSig = JSON.stringify([needs.map((a) => [a.kind, a.ref, a.title]), workers.size, bees.size, machines.size, hostSig]);
        if (bSig !== beaconSig) {
          beaconSig = bSig;
          for (const b of beacons.values()) g.remove(b.obj);
          beacons.clear();
          const cliRobot = (agent: string) => {
            const [cli, ...rest] = agent.split(".");
            const host = rest.join(".");
            for (const [h, m] of machines) if (!host || h === host || h.startsWith(host)) { const c = m.clis.get(cli); if (c) return c.r; }
            for (const [h, x] of relays) if (!host || h === host || h.startsWith(host)) { const b = x.bots.get(cli); if (b) return b.r; }
            return null;
          };
          const whoFor = (it: AttentionItem): THREE.Object3D | null => {
            switch (it.kind) {
              case "approval": {
                const asker = it.title.split(" asks")[0].split(" · ")[0].trim();
                return asker === "SuperAI" || asker === "queen" ? queen : cliRobot(asker) ?? queen;
              }
              case "bee": case "report": return bees.get(it.ref ?? "")?.r ?? null;
              case "lost": return workers.get(it.ref ?? "")?.r ?? null;
              case "failed": {
                const t = d.hive.tasks.find((x) => x.id === it.ref);
                const name = t?.worker ?? it.title.split(" ")[0];
                return workers.get(name)?.r ?? cliRobot(name) ?? (machines.get(name)?.dev ?? null);
              }
              case "run": {
                const r = d.runs.find((x) => x.id === it.ref);
                return r ? cliRobot(r.agent.includes(".") ? r.agent : `${r.agent}.${r.remote || "core"}`) : null;
              }
            }
            return null;
          };
          const per = new Map<THREE.Object3D, number>();
          for (const it of needs) {
            const anchor = whoFor(it);
            if (!anchor) continue;
            const k = per.get(anchor) ?? 0;
            per.set(anchor, k + 1);
            const bad = it.kind === "failed" || it.kind === "lost" || it.kind === "run";
            const obj = beacon(bad);
            g.add(obj);
            ctx.pickable(obj, { kind: it.kind, id: it.ref ?? "", title: it.title });
            const size = new THREE.Box3().setFromObject(anchor);
            beacons.set(`${it.kind}:${it.ref}`, { obj, anchor, lift: size.max.y - anchor.getWorldPosition(new THREE.Vector3()).y + 1.1 + k * 1.5 });
          }
        }

        // Skills: a small wrench per skill, circling the big one.
        const sk = d.skills.join("|");
        if (sk !== skillSig) {
          skillSig = sk;
          carts.clear();
          const n = Math.min(12, d.skills.length);
          d.skills.slice(0, 12).forEach((name, i) => {
            const a = (i / Math.max(1, n)) * Math.PI * 2;
            const w = wrench(TRIMS[Math.floor(hash01(name) * TRIMS.length)], 0.38);
            const slot = new THREE.Group();
            slot.position.set(Math.cos(a) * 2.2, 0, Math.sin(a) * 2.2);
            slot.add(w);
            carts.add(slot);
            ctx.pickable(w, { kind: "skill", id: name, title: name });
          });
        }
        rackLab.set(translate("Skills"), d.skills.length ? translate("{count} installed", { count: d.skills.length }) : translate("none yet — click to add"), "idle");

        // MCP servers: a box each, on an arc beyond the hub, cabled to it.
        const ms = JSON.stringify(d.mcp);
        if (ms !== mcpSig) {
          mcpSig = ms;
          services.traverse((o) => o.userData.dispose?.());
          services.clear();
          mcpLabels.forEach((l) => l.remove());
          mcpLabels = [];
          d.mcp.slice(0, 6).forEach((m, i) => {
            const a = THREE.MathUtils.degToRad(25 + i * 16);
            const at = new THREE.Vector3(Math.cos(a) * 26, 0, Math.sin(a) * 26);
            const boxNode = new THREE.Group();
            const stand = pedestal(0.9);
            const icon = plug(m.ok ? TRIMS[Math.floor(hash01(m.name) * TRIMS.length)] : 0xc3c9d1, 0.7);
            icon.position.y = 2.3;
            boxNode.add(stand, icon);
            boxNode.userData.icon = icon;
            boxNode.position.copy(at);
            services.add(boxNode);
            const c = cable(new THREE.Vector3(10, 0, 10), at, hash01(m.name));
            c.setOk(m.ok);
            services.add(c.tube);
            ctx.pickable(boxNode, { kind: "mcp", id: m.name, title: m.name });
            mcpLabels.push(ctx.label(boxNode, m.name, m.ok ? translate("{count} tools", { count: m.tools }) : translate("not connected"), 3.6));
          });
        }
        hubLab.set("MCP", translate("{ok}/{all} connected", { ok: d.mcp.filter((m) => m.ok).length, all: d.mcp.length }), "idle");

        // Dashboards on the wall.
        wallScreens.forEach((w, i) => {
          const dash = d.dashboards[i];
          w.node.visible = !!dash;
          if (!dash || w.id === dash.id + (dash.refreshed_at ?? "")) return;
          w.id = dash.id + (dash.refreshed_at ?? "");
          ctx.pickable(w.node, { kind: "dashboard", id: dash.id, title: dash.name });
          paint(w.sc, dash.name, dash.refreshed_at ? "updated " + new Date(dash.refreshed_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }) : "dashboard", [], false, false);
        });

        {
          // Flames on whoever is spending.
          for (const m of d.meter.members) {
            const anchor = anchorOf(m.name);
            if (!anchor) continue;
            let f = flames.get(m.name);
            if (!f || f.anchor !== anchor) {
              if (f) coins.remove(f.e);
              const a = anchor;
              const nm = m.name;
              f = { e: coins.link(() => pathOf(nm, a)), anchor, last: m.tokens };
              flames.set(m.name, f);
            }
            f.e.rate = m.live || m.tokPerSec > 0.5 ? 1.5 + Math.min(6, m.tokPerSec / 30) : 0;
            const spent = m.tokens - f.last;
            if (spent > 0 && f.last > 0) {
              const mouth = mouthOf(anchor);
              floatUp(mouth.clone().add(new THREE.Vector3(0, 1.3, 0)), spent);
              const c = pathOf(m.name, anchor);
              if (c) coins.burst(c, Math.min(5, 1 + Math.floor(Math.log2(spent) / 3)));
            }
            f.last = m.tokens;
          }
        }

        // noted's board: what today still holds; lit while a noted tool runs.
        notedBusy = d.meter.members.some((m) => m.live && touchesNoted(m.doing))
          || d.hive.tasks.some((x) => x.state === "running" && touchesNoted(x.tool));
        const left = d.agenda.filter((e) => e.kind !== "timer").length;
        show(boardScreen, translate("Today"), left ? translate("{count} left today", { count: left }) : translate("Nothing else today"),
          d.agenda.slice(0, 5).map(boardLine), notedBusy);
        boardLab.set(translate("Today"), left ? String(left) : "", notedBusy ? "work" : "idle");
        boardCable.setActive(notedBusy);
      },
    };
  },
};

/** Where the camera stands for each page. */
export const LENSES: Record<string, { x: number; z: number; zoom: number }> = {
  home: { x: 0, z: 3, zoom: 1.35 },
  hive: { x: -3, z: -4, zoom: 1.85 },
  agents: { x: 1, z: 2, zoom: 2.6 },
  coding: { x: 9, z: 12, zoom: 1.8 },
  knowledge: { x: -4.6, z: 3.4, zoom: 3.2 },
  skills: { x: -9, z: 13, zoom: 2.4 },
  mcp: { x: 16, z: 14, zoom: 1.8 },
  dashboards: { x: -21, z: -3, zoom: 2.2 },
  records: { x: -3.5, z: -19, zoom: 2.6 },
  stats: { x: 0, z: 3, zoom: 1.35 },
  tasks: { x: 0, z: 4, zoom: 1.15 },
  settings: { x: 0, z: -2.2, zoom: 2.4 },
};
/** Where the hosts stand, once a plan is drawn. */
let hostBounds: HostPlan["bounds"] | null = null;
/** Drawn by host, the hive stands on a ring further out: the pages that
 *  show the whole of it stand further back. */
const HOST_LENSES: Record<string, { x: number; z: number; zoom: number }> = {
  home: { x: 3, z: -6, zoom: 1.08 },
  hive: { x: 0, z: -8, zoom: 1.12 },
  stats: { x: 3, z: -6, zoom: 1.08 },
  tasks: { x: 0, z: -2, zoom: 0.95 },
};
export const lensOf = (view: string, byHost = false) => {
  const v = view === "chat" ? "home" : view;
  // The whole hive in view: its circle across the window's width (the
  // camera stands 104 / zoom away, 30° high, the window about 16:10).
  if (byHost && hostBounds && (v === "home" || v === "hive" || v === "stats")) {
    return { x: hostBounds.x, z: hostBounds.z, zoom: Math.min(1.35, Math.max(0.75, 104 / ((hostBounds.r + 10) / 0.43))) };
  }
  return (byHost ? HOST_LENSES[v] : undefined) ?? LENSES[v] ?? LENSES.home;
};
