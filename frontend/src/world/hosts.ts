import * as THREE from "three";
import type { HAService, Host, HostAgent } from "../canvas/data";
import { contact, ownMat, rbox } from "./kit";

// The hive as the boxes it runs on. Every host that stands on its own (a VM,
// a Mac, a bare machine, a container nothing contains) is a plot on the
// floor; a pod stands on its node's plot as a small framed platform; what
// runs on a host stands on it. An HA service is a pin with its service IP
// standing at the outer edge of the host it runs on now, with a dashed line
// round the outside of the ring to each host it can move to (where its
// agent, if it has one, stands as a ghost). This file plans where things go
// and makes the parts; cluster.ts puts the agents on them.

const KIND: Record<string, string> = { pod: "Pod", container: "Container", mac: "Mac", vm: "VM", metal: "Bare metal", ha: "HA" };
/** What a host is, as a label says it: "VM", "Pod", "Host" when it has not
 *  said; "HA node" for one known only as an HA service's node, whose kind
 *  nothing has said either. */
export const kindLabel = (k?: string, haNode = false) => KIND[k ?? ""] ?? (haNode ? "HA node" : "Host");

/** The HA services a host runs now and the ones it stands by for. */
export function haRoles(h: Host, services: HAService[]) {
  return {
    runs: services.filter((s) => isHost(h, s.active)).map((s) => s.name),
    standby: services.filter((s) => !isHost(h, s.active) && s.standbys.some((b) => isHost(h, b.host))).map((s) => s.name),
  };
}

/** What a host's label says when nothing on it has linked: what the HA
 *  services have it doing ("runs cortexdb · standby for openclaw"), else
 *  that only the settings reach it. Empty when neither. */
export function unlinkedStatus(h: Host, services: HAService[], t: (k: string, v?: Record<string, string | number>) => string) {
  const r = haRoles(h, services);
  const said = [r.runs.length ? t("runs {names}", { names: r.runs.join(", ") }) : "", r.standby.length ? t("standby for {names}", { names: r.standby.join(", ") }) : ""].filter(Boolean);
  if (said.length) return said.join(" · ");
  if (h.agents.some((a) => a.kind === "agent" && a.state === "configured")) return t("reached over ssh");
  return "";
}

/** Whether a host name (an HA service's, a link's) means this host. */
export const isHost = (h: Host, name?: string) => {
  const n = (name ?? "").trim().toLowerCase();
  return !!n && (h.id === n || h.name.toLowerCase() === n || (h.hostname ?? "").toLowerCase() === n);
};

/** A SuperLeo that is this core: the queen, or a core in no hive. */
export const isCore = (a: HostAgent) => a.kind === "superai" && (a.role === "queen" || a.role === "standalone");
/** An HA service's agent on a host it is not running on now. */
export const isStandby = (a: HostAgent) => a.state === "standby" || a.state === "standby-unready";

/** The coding CLI's own name: "claude.mac" is claude. */
export const cliName = (a: HostAgent) => (a.via && a.name.endsWith("." + a.via) ? a.name.slice(0, -a.via.length - 1) : a.name.split(".")[0]);

export interface Plot {
  host: Host;
  /** Hosts inside it, at any depth: its pods. */
  pods: Host[];
  /** What its SuperLeo is drawn as: a desk (machine agent), a laptop (a Mac),
   *  a pad (a relay, kept for its named agents), or nothing. */
  station: "desk" | "laptop" | "pad" | "";
  /** A desk with no crew at it: drawn smaller. */
  small: boolean;
  /** The SuperLeo there that runs its CLIs and agents for core. */
  via: string;
  /** The CLIs and named agents at its station, best known first. */
  crew: HostAgent[];
  /** Agents standing on the plot itself: SuperLeos that are not its machine
   *  agent, named agents with no station, and HA standbys (as ghosts). */
  loose: HostAgent[];
  w: number;
  d: number;
  /** Centre, world space. */
  at: THREE.Vector3;
  /** Local x of the station, of each pod, and where the loose ones start. */
  cols: { station: number; pods: number[]; loose: number };
}

export interface ServiceSpot {
  svc: HAService;
  /** Where its pin stands: at the outer edge of the plot it runs on. */
  at: THREE.Vector3;
  /** How tall its pin is: the pins in one row step up toward the back, so
   *  their labels stand one above the other. */
  height: number;
  active?: Plot;
  /** Each host it can move to, with where its line lands there and the
   *  point the line bends round, outside the ring. */
  standbys: { plot: Plot; ready: boolean; to: THREE.Vector3; bend: THREE.Vector3 }[];
}

export interface HostPlan {
  /** Where the hosts and services are, for a lens that shows them all. */
  bounds: { x: number; z: number; r: number };
  /** The host this core runs on: drawn as the dais, not as a plot. */
  core?: Host;
  /** The plot the core's host sits inside (the queen pod's node). */
  coreParent?: Plot;
  /** CLIs and named agents on the core's own host. */
  coreCrew: HostAgent[];
  plots: Plot[];
  services: ServiceSpot[];
}

const FIRST = ["claude", "codex", "pi", "openclaw", "opencode", "hermes"];
const rank = (n: string) => { const i = FIRST.indexOf(n); return i < 0 ? 99 : i; };
/** The best known first, at most `n`: which ones stand there does not move
 *  as their sessions come and go. */
function best(list: HostAgent[], n: number) {
  const own = (a: HostAgent) => (a.kind === "cli" ? cliName(a) : a.name);
  return [...list].sort((x, y) => rank(own(x)) - rank(own(y)) || own(x).localeCompare(own(y))).slice(0, n);
}

const PAD = 1.3, GAP = 5;
const W_STATION = { desk: 10, laptop: 10, pad: 8.6, "": 0 };
const W_POD = 4.4, W_LOOSE = 2.4;

/**
 * Where everything goes. Plots stand on a ring round the core, the k3s nodes
 * first and at the back, hosts that share an HA service side by side; each
 * keeps clear of what stands round the core already.
 */
export function planHosts(hosts: Host[], services: HAService[]): HostPlan {
  const byId = new Map(hosts.map((h) => [h.id, h]));
  const top = (h: Host) => { let x = h; for (let i = 0; i < 8 && x.parent && byId.has(x.parent); i++) x = byId.get(x.parent)!; return x; };
  const core = hosts.find((h) => h.agents.some(isCore));
  const kids = new Map<string, Host[]>();
  for (const h of hosts) {
    if (!h.parent || !byId.has(h.parent)) continue;
    const t = top(h);
    if (t === h) continue;
    kids.set(t.id, [...(kids.get(t.id) ?? []), h]);
  }
  // The core's host is the dais, unless pods stand inside it.
  const coreIsPlot = !!core && (kids.get(core.id)?.length ?? 0) > 0;
  const tops = hosts.filter((h) => top(h) === h && (h !== core || coreIsPlot));
  const svcOf = (h: Host) => services.find((s) => isHost(h, s.active) || s.standbys.some((b) => isHost(h, b.host)))?.name ?? "~";
  tops.sort((a, b) => Number(!kids.has(a.id)) - Number(!kids.has(b.id)) || svcOf(a).localeCompare(svcOf(b))
    || Number(a.kind !== "mac") - Number(b.kind !== "mac") || a.name.localeCompare(b.name, undefined, { numeric: true }));

  const plots: Plot[] = tops.map((host) => {
    const pods = (kids.get(host.id) ?? []).filter((p) => p !== core);
    const sup = host.agents.filter((a) => a.kind === "superai");
    const machine = sup.find((a) => a.role === "machine" || a.role === "standalone") ?? sup.find((a) => a.role === "relay");
    // A CLI that is also a named agent here (the process detector sees
    // openclaw run) is that agent, once: a core from before it said so
    // still lists both.
    const namedHere = new Set(host.agents.filter((a) => a.kind === "agent").map((a) => a.name));
    const others = host.agents.filter((a) => a.kind !== "superai" && !(a.kind === "cli" && namedHere.has(cliName(a))));
    const station: Plot["station"] = machine
      ? (machine.role === "relay" && !others.some((a) => a.kind === "cli") ? "pad" : host.kind === "mac" || host.os === "darwin" ? "laptop" : "desk")
      : others.some((a) => a.kind === "cli") ? (host.kind === "mac" ? "laptop" : "desk") : "";
    const live = others.filter((a) => !isStandby(a));
    const crew = station ? best(live, 6) : [];
    const loose = [
      ...sup.filter((a) => a !== machine),
      ...(station ? [] : best(live, 4)),
      ...others.filter(isStandby),
    ];
    const cols = { station: 0, pods: [] as number[], loose: 0 };
    let x = PAD;
    // A machine agent with nothing at its desk has a smaller desk, and no
    // room for a crew.
    const small = !!station && !crew.length && station !== "pad";
    const sw = small ? 5.6 : W_STATION[station];
    if (station) { cols.station = x + sw / 2; x += sw; }
    for (let i = 0; i < pods.length; i++) { cols.pods.push(x + W_POD / 2); x += W_POD; }
    cols.loose = x + W_LOOSE / 2;
    x += loose.length * W_LOOSE;
    const w = Math.max(6, x + PAD);
    cols.station -= w / 2; cols.pods = cols.pods.map((p) => p - w / 2); cols.loose -= w / 2;
    const d = station && !small ? 9.5 : pods.length || station ? 7 : 5;
    return { host, pods, station, small, via: machine?.name ?? others[0]?.via ?? host.name, crew, loose, w, d, at: new THREE.Vector3(), cols };
  });

  ring(plots);

  const coreParent = core?.parent ? plots.find((p) => p.host.id === top(core).id) : undefined;
  const coreCrew = core && !coreIsPlot ? best(core.agents.filter((a) => a.kind !== "superai" && !isStandby(a)), 4) : [];
  const spots = placeServices(services, plots);
  // The core, every plot and every service, in one circle: a pin with room
  // for its label beside it, and the lines bowing out to their standbys.
  const pts = [new THREE.Vector3(), ...plots.flatMap((p) => [p.at.clone().add(new THREE.Vector3(p.w / 2, 0, p.d / 2)), p.at.clone().sub(new THREE.Vector3(p.w / 2, 0, p.d / 2))]),
    ...spots.flatMap((s) => [s.at.clone().add(new THREE.Vector3(7, 0, 0)), ...s.standbys.map((b) => b.bend)])];
  const box = new THREE.Box3().setFromPoints(pts);
  const c = box.getCenter(new THREE.Vector3());
  const r = Math.max(...pts.map((p) => Math.hypot(p.x - c.x, p.z - c.z)));
  return { bounds: { x: c.x, z: c.z, r }, core: coreIsPlot ? undefined : core, coreParent, coreCrew, plots, services: spots };
}

/** Round the core, centred at the back (225°, away from the camera), each
 *  plot as far out as it must be to clear the furniture round the dais. */
function ring(plots: Plot[]) {
  if (!plots.length) return;
  const deg = THREE.MathUtils.degToRad;
  // How far a plot reaches along the ring and out from it, at angle a.
  const along = (p: Plot, a: number) => p.w * Math.abs(Math.sin(a)) + p.d * Math.abs(Math.cos(a));
  const out = (p: Plot, a: number) => p.w * Math.abs(Math.cos(a)) + p.d * Math.abs(Math.sin(a));
  // The MCP servers stand at r 26 on the front right; elsewhere the wall,
  // the board and the skills reach about 22.
  const clear = (a: number) => { const d = ((THREE.MathUtils.radToDeg(a) % 360) + 360) % 360; return d > 10 && d < 120 ? 28.5 : 24; };
  let R = 27;
  let angles = plots.map(() => deg(225));
  for (let pass = 0; pass < 3; pass++) {
    const ext = plots.map((p, i) => along(p, angles[i]) + GAP);
    const total = ext.reduce((a, b) => a + b, 0);
    R = Math.max(27, total / (Math.PI * 2));
    let cur = deg(225) - total / R / 2;
    angles = ext.map((e) => { const a = cur + e / R / 2; cur += e / R; return a; });
  }
  plots.forEach((p, i) => {
    const a = angles[i];
    const r = Math.max(R, clear(a) + out(p, a) / 2);
    p.at.set(Math.cos(a) * r, 0, Math.sin(a) * r);
  });
}

/** A plot's outer edge, the side facing away from the core: its outward
 *  normal, the direction along it, and how far out from the centre it is. */
export function outerEdge(p: Plot) {
  const alongX = Math.abs(p.at.x) >= Math.abs(p.at.z);
  const n = alongX ? new THREE.Vector3(Math.sign(p.at.x) || 1, 0, 0) : new THREE.Vector3(0, 0, Math.sign(p.at.z) || 1);
  const t = alongX ? new THREE.Vector3(0, 0, 1) : new THREE.Vector3(1, 0, 0);
  return { n, t, half: alongX ? p.w / 2 : p.d / 2, len: alongX ? p.d : p.w };
}

const PIN_OUT = 2.4, PIN_STEP = 4.2;
/** How high a service pin's gem stands, the lowest of a row. */
export const PIN_GEM = 1.9;

/**
 * Each service's pin at the outer edge of the plot it runs on, the
 * services of one plot in a row along that edge (by name, so they keep
 * their places). A service running on no plot drawn stands at its first
 * standby's edge; one on none at all waits at the back. A standby's line
 * lands on that plot's outer edge, the lines landing on one plot side by
 * side, and bows outward, each a little more than the last, so lines
 * between the same two hosts nest instead of crossing.
 */
function placeServices(services: HAService[], plots: Plot[]): ServiceSpot[] {
  const deg = THREE.MathUtils.degToRad;
  const spots: (ServiceSpot & { home?: Plot })[] = services.map((svc) => {
    const active = plots.find((p) => isHost(p.host, svc.active));
    const standbys = svc.standbys.flatMap((b) => {
      const plot = plots.find((p) => isHost(p.host, b.host));
      return plot && plot !== active ? [{ plot, ready: b.ready, to: new THREE.Vector3(), bend: new THREE.Vector3() }] : [];
    });
    return { svc, at: new THREE.Vector3(), height: PIN_GEM, active, standbys, home: active ?? standbys[0]?.plot };
  });
  const row = new Map<Plot, typeof spots>();
  for (const s of [...spots].sort((a, b) => a.svc.name.localeCompare(b.svc.name))) {
    if (s.home) row.set(s.home, [...(row.get(s.home) ?? []), s]);
    else s.at.set(Math.cos(deg(225)), 0, Math.sin(deg(225))).multiplyScalar(40);
  }
  for (const [p, list] of row) {
    const e = outerEdge(p);
    list.forEach((s, k) => {
      s.at.copy(p.at).addScaledVector(e.n, e.half + PIN_OUT).addScaledVector(e.t, (k - (list.length - 1) / 2) * PIN_STEP);
    });
    // Nearest the viewer (the camera looks from +x +z) lowest.
    [...list].sort((a, b) => (b.at.x * 0.66 + b.at.z) - (a.at.x * 0.66 + a.at.z)).forEach((s, k) => { s.height = PIN_GEM + k * 1.5; });
  }
  // Where the lines land, spread along each plot's outer edge.
  const landing = new Map<Plot, { s: ServiceSpot; b: ServiceSpot["standbys"][number] }[]>();
  for (const s of spots) for (const b of s.standbys) if (b.plot !== s.home) landing.set(b.plot, [...(landing.get(b.plot) ?? []), { s, b }]);
  for (const [p, list] of landing) {
    const e = outerEdge(p);
    const step = Math.min(2.2, (e.len - 1.5) / Math.max(1, list.length - 1));
    list.sort((x, y) => x.s.svc.name.localeCompare(y.s.svc.name));
    list.forEach(({ s, b }, k) => {
      b.to.copy(p.at).addScaledVector(e.n, e.half + 0.35).addScaledVector(e.t, (k - (list.length - 1) / 2) * step);
      const mid = s.at.clone().lerp(b.to, 0.5);
      const out = mid.clone().setY(0);
      if (out.lengthSq() < 1e-6) out.set(1, 0, 0);
      b.bend.copy(mid).addScaledVector(out.normalize(), 3 + s.at.distanceTo(b.to) * 0.18 + k * 1.8);
    });
  }
  return spots.map(({ home: _home, ...s }) => s);
}

// ── Parts ────────────────────────────────────────────────────────────────

/** Fine lines on the floor (rims, frames, links) change colour at night,
 *  where ink on the dark floor would vanish. */
const lines = new Set<{ m: THREE.LineBasicMaterial; day: number; night: number }>();
let nightNow = false;
export function setLinesNight(on: boolean) {
  nightNow = on;
  for (const l of lines) l.m.color.setHex(on ? l.night : l.day);
}
function lineMat(day: number, night: number, opacity: number, dashed: boolean) {
  const m = dashed
    ? new THREE.LineDashedMaterial({ color: nightNow ? night : day, dashSize: 0.55, gapSize: 0.4, transparent: true, opacity, depthWrite: false })
    : new THREE.LineBasicMaterial({ color: nightNow ? night : day, transparent: true, opacity, depthWrite: false });
  const rec = { m, day, night };
  lines.add(rec);
  m.userData.forget = () => lines.delete(rec);
  return m;
}
/** Lets go of the line materials under `o`, when it is taken down. */
export function forgetLines(o: THREE.Object3D) {
  o.traverse((x) => { const m = (x as THREE.Line).material as THREE.Material | undefined; if (m && !Array.isArray(m)) m.userData.forget?.(); });
}

const INK_LINE = 0x16202b, NIGHT_LINE = 0xa9b8ca, BLUE = 0x2f8bff;

/** A rounded rectangle's outline, flat at height y. */
function outline(w: number, d: number, r: number, y: number, mat: THREE.Material) {
  const s = new THREE.Shape();
  const x0 = -w / 2, z0 = -d / 2;
  s.moveTo(x0 + r, z0); s.lineTo(x0 + w - r, z0); s.absarc(x0 + w - r, z0 + r, r, -Math.PI / 2, 0, false);
  s.lineTo(x0 + w, z0 + d - r); s.absarc(x0 + w - r, z0 + d - r, r, 0, Math.PI / 2, false);
  s.lineTo(x0 + r, z0 + d); s.absarc(x0 + r, z0 + d - r, r, Math.PI / 2, Math.PI, false);
  s.lineTo(x0, z0 + r); s.absarc(x0 + r, z0 + r, r, Math.PI, Math.PI * 1.5, false);
  const pts = s.getPoints(10).map((p) => new THREE.Vector3(p.x, y, p.y));
  const line = new THREE.LineLoop(new THREE.BufferGeometry().setFromPoints(pts), mat);
  line.computeLineDistances();
  line.raycast = () => {};
  return line;
}

export const SLAB_TOP = 0.16;

/**
 * A host's plot: a low slab with a fine ink rim. Lost, it is dimmed; known
 * only from the settings (or as an HA service's standby nobody links from),
 * it is drawn lighter, its rim dashed.
 */
export function slab(w: number, d: number, state: Host["state"]) {
  const g = new THREE.Group();
  const faint = state === "configured";
  const top = rbox(w, SLAB_TOP, d, ownMat(state === "lost" ? 0xe3e7ec : 0xf6f8fa, { rough: 0.6, opacity: faint ? 0.55 : 1 }), 0.5, false);
  top.receiveShadow = true;
  const rim = outline(w - 0.02, d - 0.02, 0.5, SLAB_TOP + 0.005, lineMat(INK_LINE, NIGHT_LINE, faint ? 0.45 : state === "lost" ? 0.3 : 0.55, faint));
  g.add(top, rim);
  if (!faint) g.add(contact(w + 2, d + 2, 0.08));
  g.userData.top = top;
  return g;
}

/** How high an HA cluster's cloud floats. */
export const CLOUD_LIFT = 1.8;

/**
 * An HA cluster: a cloud the size of a plot, its agents standing on top
 * (at SLAB_TOP, like on a slab), floating CLOUD_LIFT above the floor with
 * its shadow there. Puffs of white, flattened, along the plot and either side.
 */
export function cloud(w: number, d: number, state: Host["state"]) {
  const g = new THREE.Group();
  const faint = state === "configured";
  const m = ownMat(state === "lost" ? 0xe3e7ec : 0xffffff, { rough: 0.95, opacity: faint ? 0.6 : 1 });
  const flat = 0.42;
  const puff = (x: number, z: number, r: number) => {
    const s = new THREE.Mesh(new THREE.SphereGeometry(r, 28, 18), m);
    s.scale.set(1, flat, 1);
    // The tops meet just under the robots' feet.
    s.position.set(x, SLAB_TOP - r * flat * 0.82, z);
    s.castShadow = !faint;
    s.receiveShadow = true;
    g.add(s);
    return s;
  };
  const n = Math.max(3, Math.round(w / 1.9));
  for (let i = 0; i < n; i++) {
    const t = n === 1 ? 0.5 : i / (n - 1);
    const x = (t - 0.5) * (w - 1.6);
    const edge = 1 - Math.abs(t - 0.5) * 1.1;
    puff(x, (i % 2 ? 0.35 : -0.3) * (d / 4), 1.55 + 0.55 * edge);
    puff(x + 0.6, d * 0.3, 1.05 + 0.35 * edge);
    puff(x - 0.5, -d * 0.3, 1.0 + 0.35 * edge);
  }
  puff(-w / 2 + 0.4, 0, 1.15);
  puff(w / 2 - 0.4, 0, 1.15);
  const shadow = contact(w + 1, d + 1.5, faint ? 0.05 : 0.12);
  shadow.position.y = -CLOUD_LIFT;
  g.add(shadow);
  g.userData.top = g.children[0];
  return g;
}

/** A word laid flat on the floor, in ink by day and pale at night. */
function tag(text: string, width: number) {
  const c = document.createElement("canvas");
  c.width = 512; c.height = 96;
  const x = c.getContext("2d")!;
  x.fillStyle = "#fff";
  x.font = "600 54px 'Geist Mono', 'SF Mono', Menlo, monospace";
  x.textBaseline = "middle";
  // Spaced capitals, an instrument's engraving.
  const t = text.toUpperCase().split("").join(" ");
  x.fillText(t, 8, 50);
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.anisotropy = 8;
  const m = new THREE.MeshBasicMaterial({ map: tex, color: nightNow ? NIGHT_LINE : INK_LINE, transparent: true, opacity: 0.7, depthWrite: false, toneMapped: false });
  const rec = { m: m as unknown as THREE.LineBasicMaterial, day: INK_LINE, night: NIGHT_LINE };
  lines.add(rec);
  m.userData.forget = () => lines.delete(rec);
  const p = new THREE.Mesh(new THREE.PlaneGeometry(width, width * (96 / 512)), m);
  p.rotation.x = -Math.PI / 2;
  p.raycast = () => {};
  return p;
}

/**
 * A pod: a small platform with the outline of its box drawn in fine lines
 * over it, its kind engraved on the front edge. Lost, the lines fade.
 */
export function pod(state: Host["state"]) {
  const g = new THREE.Group();
  const s = 3.4, h = 3.9;
  const base = rbox(s, 0.2, s, ownMat(state === "lost" ? 0xe6eaee : 0xfbfcfd, { rough: 0.5 }), 0.1);
  const frameMat = lineMat(INK_LINE, NIGHT_LINE, state === "lost" ? 0.18 : state === "configured" ? 0.25 : 0.42, state === "configured");
  const frame = new THREE.LineSegments(new THREE.EdgesGeometry(new THREE.BoxGeometry(s, h, s)), frameMat);
  frame.position.y = 0.2 + h / 2;
  frame.computeLineDistances();
  frame.raycast = () => {};
  const word = tag("pod", 1.5);
  word.position.set(-s / 2 + 0.95, 0.205, s / 2 - 0.3);
  g.add(base, frame, word);
  g.userData.top = 0.2;
  return g;
}

/** A link on the floor between two points: solid for where a service runs,
 *  dashed for where it can go, faint red-grey for a standby that is not
 *  ready. It bows toward `bend` when given, else runs straight. */
export function floorLink(a: THREE.Vector3, b: THREE.Vector3, how: "active" | "standby" | "unready" | "parent", bend?: THREE.Vector3) {
  const mid = bend ? bend.clone() : a.clone().lerp(b, 0.5);
  const curve = new THREE.QuadraticBezierCurve3(a.clone().setY(0.09), mid.setY(0.09), b.clone().setY(0.09));
  const m = how === "active" ? lineMat(BLUE, 0x5b9dff, 0.9, false)
    : how === "standby" ? lineMat(0x6b7a8c, NIGHT_LINE, 0.75, true)
    : how === "unready" ? lineMat(0xc0796b, 0xd08a7c, 0.6, true)
    : lineMat(INK_LINE, NIGHT_LINE, 0.4, true);
  const line = new THREE.Line(new THREE.BufferGeometry().setFromPoints(curve.getPoints(40)), m);
  line.computeLineDistances();
  line.raycast = () => {};
  return line;
}

/** A small open ring on the floor where a standby line lands. */
export function socket(ready: boolean) {
  const m = new THREE.Mesh(new THREE.TorusGeometry(0.34, 0.035, 6, 40), ownMat(0xffffff, { emissive: ready ? 0x6b7a8c : 0xc0796b, glow: 0.5 }));
  m.rotation.x = Math.PI / 2;
  m.position.y = 0.1;
  m.raycast = () => {};
  return m;
}

const SVC: Record<string, number> = { active: BLUE, degraded: 0xf2b416, down: 0xe2553f };
/**
 * An HA service: a pin at the edge of the host it runs on, a slim post with
 * a ring of light at its foot and a diamond on top, blue while it runs,
 * honey with no standby ready, red when down.
 */
export function serviceMarker(height = PIN_GEM) {
  const g = new THREE.Group();
  const foot = rbox(1.15, 0.1, 1.15, ownMat(0xf6f8fa, { rough: 0.55 }), 0.25);
  const glow = ownMat(0xd8e4ff, { emissive: BLUE, glow: 0.9 });
  const ring = new THREE.Mesh(new THREE.TorusGeometry(0.44, 0.03, 8, 48), glow);
  ring.rotation.x = Math.PI / 2;
  ring.position.y = 0.11;
  const post = new THREE.Mesh(new THREE.CylinderGeometry(0.03, 0.03, height - 0.3, 12), ownMat(0x9aa1a9, { rough: 0.35, metal: 0.6 }));
  post.position.y = (height - 0.3) / 2 + 0.1;
  const gem = new THREE.Mesh(new THREE.OctahedronGeometry(0.3, 0), ownMat(BLUE, { emissive: BLUE, glow: 0.55, rough: 0.25 }));
  gem.position.y = height;
  gem.castShadow = true;
  g.add(foot, ring, post, gem, contact(2, 2, 0.1));
  g.userData.gem = gem;
  g.userData.gemY = height;
  g.userData.glow = glow;
  return g;
}
export function setServiceState(m: THREE.Object3D, state: string) {
  const c = SVC[state] ?? 0xb8c0cc;
  const gem = (m.userData.gem as THREE.Mesh).material as THREE.MeshStandardMaterial;
  gem.color.setHex(c); gem.emissive.setHex(c);
  ((m.userData.glow as THREE.MeshStandardMaterial).emissive).setHex(c);
}
