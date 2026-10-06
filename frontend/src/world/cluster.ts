import * as THREE from "three";
import type { District, DistrictCtx, Label } from "./engine";
import { box, cyl, hash01, mat, ownMat, rbox, screen } from "./kit";
import { INK, pose, robot, setState, type RobotState } from "./robots";
import { forgetActor, registerActor } from "./bubbles";
import { Packets, type Link } from "./packets";
import { CSS2DObject } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import type { Bee, Hive, LinkedAgent } from "../canvas/data";
import type { CodingRun } from "../canvas/tiles";
import type { AttentionItem } from "../canvas/attention";
import type { Member } from "../components/HiveMeter";

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
  dashboards: { id: string; name: string; refreshed_at?: string }[];
  skills: string[];
  mcp: { name: string; ok: boolean; tools: number }[];
  knowledge: number;
  meter: { total: Member; members: Member[] };
  busy: boolean;
}

const short = (n: string) => n.replace(/^superai-worker-/, "w").replace(/^superai-/, "");
const clip = (s: string, n = 30) => { const t = s.replace(/\s+/g, " ").trim(); return t.length > n ? t.slice(0, n - 1) + "…" : t; };
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
function paint(s: Screen, title: string, status: string, lines: string[], busy: boolean, blink: boolean) {
  // An old terminal: green phosphor on near-black, a prompt per line, a block
  // cursor, scanlines and a soft glow round the edges.
  s.draw((c, w, h) => {
    c.fillStyle = "#06120a"; c.fillRect(0, 0, w, h);
    const ink = busy ? "#5cff8f" : "#3fd170";
    c.shadowColor = ink; c.shadowBlur = busy ? 14 : 8;
    c.fillStyle = ink;
    c.font = "700 40px 'Geist Mono', 'SF Mono', Menlo, monospace";
    c.fillText(`${title}:~$`, 24, 56);
    c.font = "500 30px 'Geist Mono', 'SF Mono', Menlo, 'PingFang SC', monospace";
    c.globalAlpha = 0.85;
    c.fillText(`# ${status}`, 24, 100);
    c.globalAlpha = 1;
    const wrapped: string[] = [];
    for (const l of lines) for (let i = 0; i < l.length && wrapped.length < 8; i += 30) wrapped.push((i ? "  " : "> ") + l.slice(i, i + 30));
    const shown = wrapped.slice(0, 5);
    shown.forEach((l, i) => c.fillText(l, 24, 146 + i * 38));
    if (blink) { c.fillRect(24 + (busy ? 0 : 0), 146 + shown.length * 38 - 28, 18, 32); }
    c.shadowBlur = 0;
    // Scanlines and the glass's dark corners.
    c.fillStyle = "rgba(0,0,0,0.22)";
    for (let y = 0; y < h; y += 4) c.fillRect(0, y, w, 1.5);
    const v = c.createRadialGradient(w / 2, h / 2, h * 0.3, w / 2, h / 2, w * 0.7);
    v.addColorStop(0, "rgba(0,0,0,0)"); v.addColorStop(1, "rgba(0,0,0,0.55)");
    c.fillStyle = v; c.fillRect(0, 0, w, h);
  });
}

/** A computer at a desk, monitor (or laptop) facing the room. Its screens and
 *  the light on its case are in userData; `spots` are where its robots
 *  stand, beside the desk, turned to the screen. */
function workstation(kind: "desktop" | "laptop" | "console") {
  const g = new THREE.Group();
  const wide = kind === "console" ? 10 : 6.2;
  const top = rbox(wide, 0.2, 3, mat(0xffffff, { rough: 0.5 }), 0.08);
  top.position.y = 1.55;
  g.add(top);
  for (const [x, z] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) {
    const leg = box(0.14, 1.55, 0.14, mat(INK), true);
    leg.position.set(x * (wide / 2 - 0.3), 0, z * 1.25);
    g.add(leg);
  }
  const screens: Screen[] = [];
  const monitor = (x: number, tilt = 0) => {
    const m = new THREE.Group();
    const foot = rbox(1, 0.08, 0.7, mat(0xdfe4ea), 0.04, false);
    const neck = box(0.16, 0.8, 0.12, mat(0xdfe4ea), true);
    neck.position.set(0, 0.05, -0.15);
    const bezel = rbox(3.3, 2.05, 0.14, mat(0x1b2430, { rough: 0.4 }), 0.06);
    bezel.position.set(0, 0.75, -0.2);
    const sc = screen(3.05, 1.8, 640);
    sc.mesh.position.set(0, 0.75 + 1.025, -0.12);
    m.add(foot, neck, bezel, sc.mesh);
    m.position.set(x, 1.75, -0.6);
    m.rotation.y = tilt;
    screens.push(sc);
    g.add(m);
  };
  if (kind === "laptop") {
    const base = rbox(3, 0.12, 2.1, mat(0xe8ebef, { rough: 0.4, metal: 0.2 }), 0.06);
    base.position.set(0, 1.75, 0.1);
    const lid = new THREE.Group();
    lid.position.set(0, 1.87, -0.95);
    const back = rbox(3, 2, 0.08, mat(0xe8ebef, { rough: 0.4, metal: 0.2 }), 0.05);
    const sc = screen(2.75, 1.7, 640);
    sc.mesh.position.set(0, 1, 0.05);
    lid.add(back, sc.mesh);
    lid.rotation.x = -0.18;
    screens.push(sc);
    g.add(base, lid);
  } else if (kind === "console") {
    monitor(-3.3, 0.25); monitor(0); monitor(3.3, -0.25);
  } else {
    monitor(0);
    const keys = rbox(2.4, 0.07, 0.8, mat(0x2a3442), 0.03, false);
    keys.position.set(0, 1.75, 0.65);
    g.add(keys);
  }
  // The case on the floor beside the desk, with its light.
  const tower = rbox(1.2, 2.5, 2.3, mat(0xffffff, { rough: 0.5 }), 0.12);
  tower.position.set(wide / 2 + 0.9, 0, -0.2);
  const strip = box(0.08, 1.6, 0.06, ownMat(0xd8dee6, { emissive: accentColor, glow: 0 }));
  strip.position.set(wide / 2 + 0.9, 0.5, 0.96);
  if (kind !== "laptop") g.add(tower, strip);
  g.userData.tower = tower;
  g.userData.screens = screens;
  g.userData.strip = strip;
  // In front of the desk, at the keyboard, facing the screen.
  g.userData.spots = [new THREE.Vector3(0, 0, 2.2), new THREE.Vector3(-1.7, 0, 2.2), new THREE.Vector3(1.7, 0, 2.2)];
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
  const p = cyl(r, r * 1.1, 0.5, 32, mat(0xffffff, { rough: 0.5 }));
  const ring = cyl(r * 1.02, r * 1.02, 0.08, 32, ownMat(0xd8dee6, { emissive: ROUTE, glow: 0.4 }), false);
  ring.position.y = 0.45;
  g.add(p, ring);
  return g;
}

const coreAt0 = () => new THREE.Vector3(0, 0, -2.2);

type Work = { node: THREE.Mesh; curve: THREE.Curve<THREE.Vector3>; t: number; back: boolean; state: string; to: THREE.Object3D | null };

export const cluster: District<WorldData> = {
  key: "cluster", title: "", at: [0, 0], size: [90, 90], bare: true,
  build(ctx: DistrictCtx) {
    const g = ctx.group;

    // ── The core: the queen's machine, her console, the shared brain ───────
    const dais = cyl(7.5, 7.8, 0.35, 48, mat(0xffffff, { rough: 0.6 }), false);
    dais.receiveShadow = true;
    const rim = cyl(7.85, 7.85, 0.12, 48, mat(INK, { rough: 0.6 }), false);
    g.add(dais, rim);
    const core = workstation("console");
    core.position.set(0, 0.35, -2.2);
    core.rotation.y = 0;
    g.add(core);
    ctx.pickable(core, { kind: "hall", id: "queen", title: "The queen" });
    // The core's own case: where this core is set up.
    ctx.pickable(core.userData.tower, { kind: "settings", id: "core", title: "This core" });
    const queen = robot("queen");
    registerActor("queen", queen);
    // She faces you: she is the one you talk to.
    queen.position.set(0.8, 0.35, 1.4);
    queen.userData.base = 0.35;
    queen.rotation.y = Math.PI / 4;
    g.add(queen);
    ctx.pickable(queen, { kind: "hall", id: "queen", title: "The queen" });
    const queenLab = ctx.label(queen, "queen", "", 3.4);

    const brain = new THREE.Group();
    for (let i = 0; i < 4; i++) {
      const disk = cyl(1.6, 1.6, 0.55, 32, mat(i % 2 ? 0xffffff : 0xeef2f6, { rough: 0.4 }));
      disk.position.y = 0.35 + i * 0.62;
      const ring = cyl(1.62, 1.62, 0.08, 32, ownMat(0xd8dee6, { emissive: accentColor, glow: 0.0 }), false);
      ring.position.y = 0.35 + i * 0.62 + 0.5;
      brain.add(disk, ring);
    }
    brain.position.set(-4.6, 0, 3.4);
    g.add(brain);
    ctx.pickable(brain, { kind: "brain", id: "brain", title: "Shared brain" });
    ctx.label(brain, "CortexDB", "shared brain", 3.4);

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
    const rackLab = ctx.label(rack, "Skills", "", 5.6);
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

    // ── Dashboards: a wall of screens ────────────────────────────────────
    const wall = new THREE.Group();
    wall.position.set(-21, 0, -3);
    wall.rotation.y = Math.PI / 2;
    g.add(wall);
    const wallScreens: { node: THREE.Group; sc: Screen; id: string }[] = [];
    for (let i = 0; i < 4; i++) {
      const node = new THREE.Group();
      const post = box(0.2, 2.6, 0.2, mat(INK), true);
      const bezel = rbox(4, 2.5, 0.18, mat(0x1b2430, { rough: 0.4 }), 0.06);
      bezel.position.y = 2.4;
      const sc = screen(3.7, 2.2, 640);
      sc.mesh.position.set(0, 3.65, 0.1);
      node.add(post, bezel, sc.mesh);
      node.position.set(-6.6 + i * 4.4, 0, 0);
      node.visible = false;
      wall.add(node);
      wallScreens.push({ node, sc, id: "" });
    }

    // ── The calendar: a board with what is coming ───────────────────────
    const board = new THREE.Group();
    const boardPost = box(0.25, 2.2, 0.25, mat(INK), true);
    const boardBezel = rbox(5.4, 3.4, 0.2, mat(0xffffff, { rough: 0.5 }), 0.1);
    boardBezel.position.y = 2;
    const boardScreen = screen(5, 3, 640);
    boardScreen.mesh.position.set(0, 3.7, 0.11);
    board.add(boardPost, boardBezel, boardScreen.mesh);
    board.position.set(-3.5, 0, -19);
    g.add(board);
    ctx.pickable(board, { kind: "records", id: "records", title: "Calendar" });
    ctx.label(board, "Calendar", "", 6);

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

    const coreAt = new THREE.Vector3(0, 0, -2.2);
    const cables = new THREE.Group();
    g.add(cables);
    const work = new Map<string, Work>();
    const cubeGeo = new THREE.SphereGeometry(0.32, 20, 14);

    const nodeSpot = (i: number, n: number) => {
      const a = THREE.MathUtils.degToRad(n === 1 ? 225 : 150 + (i * 150) / Math.max(1, n - 1));
      return new THREE.Vector3(Math.cos(a) * 14.5, 0, Math.sin(a) * 14.5);
    };
    const machineSpot = (i: number, n: number) => {
      const a = THREE.MathUtils.degToRad(n === 1 ? 45 : 95 - (i * 90) / Math.max(1, n - 1));
      return new THREE.Vector3(Math.cos(a) * 21, 0, Math.sin(a) * 21);
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
    const bitMat = new THREE.MeshBasicMaterial({ color: accentColor, transparent: true, opacity: 0.9, toneMapped: false });
    let bitAt = 0;
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
            const b = new THREE.Mesh(bitGeo, bitMat.clone());
            const p = r.localToWorld(new THREE.Vector3((Math.random() - 0.5) * 0.6, 1.4, 0.9));
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
          (b.material as THREE.MeshBasicMaterial).opacity = Math.max(0, 0.9 - b.userData.life * 0.9);
          if (b.userData.life > 1) { bits.remove(b); (b.material as THREE.Material).dispose(); }
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
        for (const [sc, x] of displays) if (x.busy) paint(sc, x.title, x.status, x.lines, true, on);
      }
      pose(queen, t, ctx.reduced);
      for (const w of workers.values()) pose(w.r, t, ctx.reduced);
      for (const m of machines.values()) for (const c of m.clis.values()) pose(c.r, t, ctx.reduced);
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
          const k = w.back ? 1 - w.t : w.t;
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

    return {
      update(d) {
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
            x = { box: b, label: ctx.label(plate, n, "node", 0), cable: c, at };
            nodes.set(n, x);
            ctx.pickable(b, { kind: "node", id: n, title: n });
          }
        });

        // Workers on their nodes.
        for (const [n, w] of workers) if (!members.some((m) => m.name === n)) { w.label.remove(); g.remove(w.r); workers.delete(n); forgetActor("worker:" + n); }
        const perNode = new Map<string, number>();
        const count = new Map<string, number>();
        members.forEach((m) => count.set(nodeOf(m), (count.get(nodeOf(m)) ?? 0) + 1));
        const run = d.hive.tasks.filter((x) => x.state === "running");
        const failed = new Set(d.attention.filter((a) => a.kind === "failed" || a.kind === "lost").map((a) => a.title.split(" ")[0]));
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
          const strip = nx.box.userData.strip as THREE.Mesh;
          (strip.material as THREE.MeshStandardMaterial).emissiveIntensity = run.some((x) => workers.get(x.worker)?.node === node) ? 0.9 : 0;
          w.label.set(short(m.name), st === "lost" ? "not answering" : mine.length ? clip(mine[0].tool ? `${mine[0].tool} · ${mine[0].prompt}` : mine[0].prompt, 28) : st === "fail" ? "failed" : "idle",
            st === "lost" || st === "fail" ? "bad" : st === "work" || st === "think" ? "work" : "idle");
        });

        // Each node's screen: its workers and what they are doing.
        for (const [n, x] of nodes) {
          const on = members.filter((m) => nodeOf(m) === n);
          const doing = on.flatMap((m) => run.filter((t) => t.worker === m.name).map((t) => `${short(m.name)}: ${t.tool ? t.tool + " · " : ""}${t.prompt}`));
          const lost = on.some((m) => m.state === "lost");
          show((x.box.userData.screens as Screen[])[0], n, lost ? "a worker is not answering" : doing.length ? `${doing.length} order${doing.length > 1 ? "s" : ""} running` : "idle",
            doing.length ? doing : on.map((m) => `${short(m.name)} · ${m.state === "lost" ? "lost" : "ready"}`), doing.length > 0);
        }

        // Linked machines and the coding agents on them.
        const linked = d.linked.filter((a) => a.clis?.length || a.agents?.length);
        for (const [h, x] of machines) if (!linked.some((a) => a.name === h)) {
          x.label.remove(); x.clis.forEach((c) => c.label.remove()); g.remove(x.dev); cables.remove(x.cable.tube); x.cable.tube.userData.dispose?.(); machines.delete(h);
        }
        const asking = d.attention.filter((a) => a.kind === "approval").map((a) => a.title.split(" asks")[0].split(" · ")[0].trim());
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
          const FIRST = ["claude", "codex", "pi", "openclaw", "gemini", "opencode", "hermes"];
          const rank = (n: string) => { const i = FIRST.indexOf(n); return i < 0 ? 99 : i; };
          const who = [...(a.clis ?? []), ...(a.agents ?? []).map((n) => n.name)].sort((x, y) => rank(x) - rank(y)).slice(0, 5);
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
            const st: RobotState = asking.some((s) => s === `${n}.${a.name}` || s.startsWith(n + ".")) ? "wait"
              : live.length || delegated.length ? "work" : runs[0]?.state === "failed" ? "fail" : "idle";
            setState(c.r, st);
            c.label.set(n, st === "wait" ? "needs your OK" : live.length ? clip(live[0].prompt, 24) : delegated.length ? clip(delegated[0].prompt, 24) : st === "fail" ? "failed" : "idle",
              st === "wait" ? "wait" : st === "work" ? "work" : st === "fail" ? "bad" : "idle");
          });
          const runsHere = d.runs.filter((r) => (r.remote || "core") === a.name);
          const liveHere = runsHere.filter((r) => r.state === "running");
          x.cable.setActive(liveHere.length > 0 || run.some((t) => route(t.worker)?.curve === x!.cable.curve));
          show((x.dev.userData.screens as Screen[])[0], a.name, liveHere.length ? `${liveHere.length} running` : `${who.length} agents ready`,
            (liveHere.length ? liveHere : runsHere.slice(0, 4)).map((r) => `${r.agent}: ${r.prompt}`), liveHere.length > 0);
        });

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
          b.label.set(info.name, info.waitingFor ? "waiting for you" : info.running ? "out now" : info.paused ? "paused" : at ? "next " + at : "resting",
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
        queenLab.set("queen", live ? clip(doing || "thinking", 26) : "idle", live ? "work" : queenState === "wait" ? "wait" : "idle");
        const coreStrip = (core.userData.strip as THREE.Mesh).material as THREE.MeshStandardMaterial;
        coreStrip.emissiveIntensity = live ? 1 : 0.15;
        const [s1, s2, s3] = core.userData.screens as Screen[];
        const fmt = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));
        show(s1, "hive tokens", `${fmt(d.meter.total.tokens)}`, [`${Math.round(d.meter.total.tokPerSec)} tok/s`, `${d.meter.total.calls} tool calls`], d.meter.total.tokPerSec > 0.5);
        show(s2, "queen", live ? "working" : "idle", live ? [doing || "thinking…"] : ["say something below"], live);
        show(s3, "orders out", String(run.length), run.slice(0, 5).map((t) => `${short(t.worker)}: ${t.prompt}`), run.length > 0);
        // Marks over whatever needs you, each tied to the one it is about.
        const needs = d.attention.filter((a) => a.level === "needs");
        const bSig = JSON.stringify([needs.map((a) => [a.kind, a.ref, a.title]), workers.size, bees.size, machines.size]);
        if (bSig !== beaconSig) {
          beaconSig = bSig;
          for (const b of beacons.values()) g.remove(b.obj);
          beacons.clear();
          const cliRobot = (agent: string) => {
            const [cli, ...rest] = agent.split(".");
            const host = rest.join(".");
            for (const [h, m] of machines) if (!host || h === host || h.startsWith(host)) { const c = m.clis.get(cli); if (c) return c.r; }
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
        rackLab.set("Skills", d.skills.length ? `${d.skills.length} installed` : "none yet — click to add", "idle");

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
            mcpLabels.push(ctx.label(boxNode, m.name, m.ok ? `${m.tools} tools` : "not connected", 3.6));
          });
        }
        hubLab.set("MCP", `${d.mcp.filter((m) => m.ok).length}/${d.mcp.length} connected`, "idle");

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

        // The calendar board: the next few things.
        const soon = d.attention.filter((a) => a.level === "soon" && a.at).sort((a, b) => +new Date(a.at!) - +new Date(b.at!)).slice(0, 3);
        show(boardScreen, "Coming up", soon.length ? `${soon.length} on the calendar` : "nothing scheduled",
          soon.map((a) => `${new Date(a.at!).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit", hour12: false })} ${a.title}`), false);
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
export const lensOf = (view: string) => LENSES[view === "chat" ? "home" : view] ?? LENSES.home;
