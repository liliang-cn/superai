import * as THREE from "three";
import { RoundedBoxGeometry } from "three/examples/jsm/geometries/RoundedBoxGeometry.js";

// The world's parts: a small set of low-poly pieces in a few flat colours,
// lit rather than textured, so a whole campus of them stays cheap. See
// docs/design/world.md for the look.

export const C = {
  sky: 0xeaf1e4,
  ground: 0xe1ead8,
  plot: 0xeaf2e2,
  road: 0xefe6d2,
  wood: 0xc99b6b,
  woodDeep: 0x9c7349,
  meadow: 0xe8efe0,
  wax: 0xfbefc8,
  pollen: 0xffb23e,
  dash: 0xffffff,
  wall: 0xf8fafd,
  wallShade: 0xe6ebf3,
  roof: 0x2f5bea,
  roofTop: 0xe3e9f2,
  roofDeep: 0x1d3fb8,
  door: 0x9aa7bb,
  glass: 0xbfd3f5,
  honey: 0xf2b416,
  honeyDeep: 0xc98a00,
  crate: 0xe7c08a,
  tape: 0xc99a5c,
  leaf: 0x5cc98a,
  leafDeep: 0x3fae70,
  trunk: 0xa47a55,
  alert: 0xe2553f,
  ink: 0x16202b,
  dark: 0x2a3442,
  white: 0xffffff,
};

const mats = new Map<string, THREE.MeshStandardMaterial>();

/** A shared material: the same colour and finish is one material, however
 *  many things wear it. */
export function mat(color: number, opts: { rough?: number; metal?: number; emissive?: number; glow?: number; opacity?: number } = {}) {
  const key = `${color}|${opts.rough ?? 0.78}|${opts.metal ?? 0}|${opts.emissive ?? 0}|${opts.glow ?? 0}|${opts.opacity ?? 1}`;
  let m = mats.get(key);
  if (!m) {
    m = new THREE.MeshStandardMaterial({
      color, roughness: opts.rough ?? 0.78, metalness: opts.metal ?? 0,
      emissive: opts.emissive ?? 0x000000, emissiveIntensity: opts.glow ?? 0,
      transparent: (opts.opacity ?? 1) < 1, opacity: opts.opacity ?? 1,
    });
    mats.set(key, m);
  }
  return m;
}

/** A material of its own, for something whose colour or glow is animated. */
export function ownMat(color: number, opts: { rough?: number; emissive?: number; glow?: number; opacity?: number } = {}) {
  return new THREE.MeshStandardMaterial({
    color, roughness: opts.rough ?? 0.7, emissive: opts.emissive ?? 0x000000, emissiveIntensity: opts.glow ?? 0,
    transparent: (opts.opacity ?? 1) < 1, opacity: opts.opacity ?? 1,
  });
}

const geos = new Map<string, THREE.BufferGeometry>();
function geo(key: string, make: () => THREE.BufferGeometry) {
  let g = geos.get(key);
  if (!g) { g = make(); geos.set(key, g); }
  return g;
}

/** A rounded box standing on y=0. */
export function rbox(w: number, h: number, d: number, color: number | THREE.Material, radius?: number, shadow = true) {
  const r = radius ?? Math.min(w, h, d) * 0.08;
  const g = geo(`rb${w}|${h}|${d}|${r}`, () => {
    const b = new RoundedBoxGeometry(w, h, d, 2, r);
    b.translate(0, h / 2, 0);
    return b;
  });
  const m = new THREE.Mesh(g, typeof color === "number" ? mat(color) : color);
  m.castShadow = shadow;
  m.receiveShadow = true;
  return m;
}

/** A plain box standing on y=0 (for thin trims, where rounding is invisible). */
export function box(w: number, h: number, d: number, color: number | THREE.Material, shadow = false) {
  const g = geo(`bx${w}|${h}|${d}`, () => { const b = new THREE.BoxGeometry(w, h, d); b.translate(0, h / 2, 0); return b; });
  const m = new THREE.Mesh(g, typeof color === "number" ? mat(color) : color);
  m.castShadow = shadow;
  m.receiveShadow = true;
  return m;
}

export function cyl(rTop: number, rBot: number, h: number, seg: number, color: number | THREE.Material, shadow = true) {
  const g = geo(`cy${rTop}|${rBot}|${h}|${seg}`, () => { const c = new THREE.CylinderGeometry(rTop, rBot, h, seg); c.translate(0, h / 2, 0); return c; });
  const m = new THREE.Mesh(g, typeof color === "number" ? mat(color) : color);
  m.castShadow = shadow;
  m.receiveShadow = true;
  return m;
}

/** A tree: a trunk and a faceted crown, slightly different each time. */
export function tree(seed = Math.random()) {
  const g = new THREE.Group();
  const s = 0.8 + seed * 0.5;
  const trunk = cyl(0.12 * s, 0.16 * s, 0.9 * s, 6, C.trunk);
  const crown = new THREE.Mesh(geo(`crown`, () => new THREE.IcosahedronGeometry(1, 1)), mat(seed > 0.5 ? C.leaf : C.leafDeep, { rough: 0.9 }));
  (crown.material as THREE.MeshStandardMaterial).flatShading = true;
  crown.scale.set(0.75 * s, 0.95 * s, 0.75 * s);
  crown.position.y = 1.45 * s;
  crown.castShadow = true;
  g.add(trunk, crown);
  g.userData.sway = seed * Math.PI * 2;
  return g;
}

/** A parcel of work. */
export function crate(size = 0.9) {
  const g = new THREE.Group();
  g.add(rbox(size, size * 0.85, size, C.crate, size * 0.06));
  const tape = box(size * 1.01, size * 0.12, size * 0.2, C.tape);
  tape.position.y = size * 0.36;
  g.add(tape);
  return g;
}

/** A honey drone: a body, four arms and four rotors. Rotors are listed in
 *  userData so a tick can spin them. */
export function drone(color = C.honey) {
  const g = new THREE.Group();
  const body = rbox(1.1, 0.42, 1.1, color, 0.18);
  body.position.y = 0;
  g.add(body);
  const eye = rbox(0.5, 0.16, 0.12, C.ink, 0.05, false);
  eye.position.set(0, 0.12, 0.55);
  g.add(eye);
  const rotors: THREE.Object3D[] = [];
  for (const [x, z] of [[1, 1], [1, -1], [-1, 1], [-1, -1]]) {
    const arm = box(0.9, 0.08, 0.12, C.dark);
    arm.position.set(x * 0.45, 0.28, z * 0.45);
    arm.rotation.y = Math.atan2(z, x) * -1;
    const hub = cyl(0.07, 0.07, 0.16, 8, C.dark, false);
    hub.position.set(x * 0.85, 0.28, z * 0.85);
    const rotor = new THREE.Mesh(geo("rotor", () => new THREE.CylinderGeometry(0.42, 0.42, 0.02, 16)), mat(0xffffff, { opacity: 0.55 }));
    rotor.position.set(x * 0.85, 0.46, z * 0.85);
    rotors.push(rotor);
    g.add(arm, hub, rotor);
  }
  g.userData.rotors = rotors;
  return g;
}

/** A small forklift-like rover that moves crates at a bench. */
export function rover() {
  const g = new THREE.Group();
  const body = rbox(1.1, 0.7, 1.5, C.honey, 0.14);
  body.position.y = 0.25;
  const cab = rbox(0.9, 0.55, 0.7, C.dark, 0.1);
  cab.position.set(0, 0.95, -0.25);
  const mast = box(0.9, 1.9, 0.12, C.dark, true);
  mast.position.set(0, 0.2, 0.82);
  const forks = box(0.8, 0.08, 0.9, C.dark);
  forks.position.set(0, 0.35, 1.25);
  g.add(body, cab, mast, forks);
  for (const [x, z] of [[0.55, 0.45], [-0.55, 0.45], [0.55, -0.45], [-0.55, -0.45]]) {
    const w = cyl(0.24, 0.24, 0.2, 12, C.ink, false);
    w.rotation.z = Math.PI / 2;
    w.position.set(x, 0.24, z);
    g.add(w);
  }
  g.userData.forks = forks;
  return g;
}

/** A map pin, standing over something that wants you. */
export function pin(color = C.roof) {
  const g = new THREE.Group();
  const cone = new THREE.Mesh(geo("pincone", () => { const c = new THREE.ConeGeometry(0.32, 0.9, 16); c.rotateX(Math.PI); c.translate(0, 0.45, 0); return c; }), mat(color, { rough: 0.4 }));
  const head = new THREE.Mesh(geo("pinhead", () => new THREE.SphereGeometry(0.42, 20, 14)), mat(color, { rough: 0.4 }));
  head.position.y = 1.05;
  const dot = new THREE.Mesh(geo("pindot", () => new THREE.SphereGeometry(0.17, 12, 10)), mat(C.white));
  dot.position.set(0, 1.05, 0.33);
  cone.castShadow = head.castShadow = true;
  g.add(cone, head, dot);
  return g;
}

/** A building: white walls, a blue roof, dock doors along the front. The
 *  doors' panels are in userData so a district can light them. */
export function building(w: number, d: number, h: number, opts: { doors?: number; roof?: "flat" | "saw"; trim?: number } = {}) {
  const g = new THREE.Group();
  const walls = rbox(w, h, d, C.wall, 0.25);
  g.add(walls);
  const base = box(w + 0.1, 0.25, d + 0.1, C.wallShade);
  g.add(base);
  const trim = opts.trim ?? C.roof;
  const band = box(w + 0.12, 0.35, d + 0.12, trim);
  band.position.y = h - 0.35;
  g.add(band);
  if (opts.roof === "saw") {
    const n = Math.max(2, Math.round(w / 4));
    for (let i = 0; i < n; i++) {
      const tooth = new THREE.Mesh(geo(`saw${(w / n).toFixed(2)}|${d}`, () => {
        const s = new THREE.Shape();
        const tw = w / n;
        s.moveTo(0, 0); s.lineTo(tw, 0); s.lineTo(tw, 1.4); s.lineTo(0, 0);
        const e = new THREE.ExtrudeGeometry(s, { depth: d + 0.4, bevelEnabled: false });
        e.translate(0, 0, -(d + 0.4) / 2);
        return e;
      }), mat(C.roofTop, { rough: 0.7 }));
      tooth.position.set(-w / 2 + (i * w) / n, h, 0);
      tooth.castShadow = true;
      g.add(tooth);
    }
  } else {
    const rim = rbox(w + 0.5, 0.3, d + 0.5, mat(trim, { rough: 0.6 }), 0.1);
    rim.position.y = h;
    const roof = rbox(w + 0.1, 0.36, d + 0.1, mat(C.roofTop, { rough: 0.7 }), 0.1);
    roof.position.y = h + 0.05;
    g.add(rim, roof);
  }
  const panels: THREE.Mesh[] = [];
  const doors = opts.doors ?? 0;
  for (let i = 0; i < doors; i++) {
    const x = -w / 2 + ((i + 0.5) * w) / doors;
    const frame = box(2.3, 2.9, 0.2, trim);
    frame.position.set(x, 0, d / 2 + 0.02);
    const panel = box(1.8, 2.5, 0.12, ownMat(C.door));
    panel.position.set(x, 0, d / 2 + 0.1);
    panels.push(panel);
    g.add(frame, panel);
  }
  g.userData.panels = panels;
  return g;
}

/** A flat pad on the ground, the plot a district stands on. */
export function plot(w: number, d: number) {
  const p = rbox(w, 0.12, d, C.plot, 0.6, false);
  p.receiveShadow = true;
  return p;
}

/** A canvas-backed screen: a plane whose picture a district redraws. */
export function screen(w: number, h: number, px = 256) {
  const canvas = document.createElement("canvas");
  canvas.width = px;
  canvas.height = Math.round((px * h) / w);
  const tex = new THREE.CanvasTexture(canvas);
  tex.colorSpace = THREE.SRGBColorSpace;
  tex.anisotropy = 4;
  const m = new THREE.Mesh(new THREE.PlaneGeometry(w, h), new THREE.MeshBasicMaterial({ map: tex, toneMapped: false }));
  m.userData.isScreen = true;
  m.userData.w = w;
  const draw = (paint: (ctx: CanvasRenderingContext2D, w: number, h: number) => void) => {
    const ctx = canvas.getContext("2d")!;
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    paint(ctx, canvas.width, canvas.height);
    tex.needsUpdate = true;
  };
  return { mesh: m, draw };
}

/** A ring thrown off something that just did a thing; grows and fades. */
export function spark(color = C.honey) {
  const m = new THREE.Mesh(geo("spark", () => { const t = new THREE.TorusGeometry(1, 0.06, 6, 40); t.rotateX(Math.PI / 2); return t; }),
    new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.9 }));
  m.userData.life = 0;
  return m;
}

export const ease = (t: number) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);
export const hash01 = (s: string) => {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return ((h >>> 0) % 10000) / 10000;
};

/** A lamp post. */
export function lamp() {
  const g = new THREE.Group();
  const post = cyl(0.08, 0.1, 3.2, 8, C.woodDeep, true);
  const arm = box(0.9, 0.08, 0.08, C.woodDeep);
  arm.position.set(0.4, 3.1, 0);
  const head = rbox(0.5, 0.14, 0.3, mat(0xffffff, { emissive: 0xfff3c4, glow: 0.4 }), 0.05, false);
  head.position.set(0.8, 3.0, 0);
  g.add(post, arm, head);
  return g;
}

/** A run of fence: white posts, a blue rail. */
export function fence(len: number) {
  const g = new THREE.Group();
  const n = Math.max(2, Math.round(len / 1.6));
  for (let i = 0; i <= n; i++) {
    const p = box(0.12, 1.1, 0.12, C.wall, true);
    p.position.x = -len / 2 + (i * len) / n;
    g.add(p);
  }
  const rail = box(len, 0.1, 0.08, C.roof);
  rail.position.y = 0.95;
  const low = box(len, 0.08, 0.06, C.wallShade);
  low.position.y = 0.45;
  g.add(rail, low);
  return g;
}

/** A pallet with a few crates on it. */
export function pallet(n = 3, seed = 0.5) {
  const g = new THREE.Group();
  const base = box(2, 0.22, 1.5, C.trunk, true);
  g.add(base);
  for (let i = 0; i < n; i++) {
    const c = crate(0.85);
    c.position.set(-0.48 + (i % 2) * 0.96, 0.22 + Math.floor(i / 2) * 0.74, (seed - 0.5) * 0.3);
    c.rotation.y = (seed - 0.5) * 0.3 * (i + 1);
    g.add(c);
  }
  return g;
}

// ─── The apiary's own pieces ────────────────────────────────────────────────

/** A bee: a striped honey body, a dark head, two wings that beat. The wings
 *  are in userData so a tick can flap them; `carry` is where it holds things. */
export function bee(color = C.honey, scale = 1) {
  const g = new THREE.Group();
  const body = new THREE.Mesh(geo("beebody", () => { const s = new THREE.SphereGeometry(0.5, 18, 14); s.scale(0.8, 0.75, 1.15); return s; }), mat(color, { rough: 0.45 }));
  body.castShadow = true;
  g.add(body);
  for (const z of [-0.18, 0.18]) {
    const band = new THREE.Mesh(geo("beeband", () => { const c = new THREE.CylinderGeometry(0.405, 0.405, 0.16, 18, 1, true); c.rotateX(Math.PI / 2); return c; }), mat(C.ink, { rough: 0.5 }));
    band.position.z = z;
    band.scale.set(1, 0.94, 1);
    g.add(band);
  }
  const head = new THREE.Mesh(geo("beehead", () => new THREE.SphereGeometry(0.3, 14, 12)), mat(C.ink, { rough: 0.5 }));
  head.position.set(0, 0.06, 0.62);
  head.castShadow = true;
  g.add(head);
  for (const x of [-0.12, 0.12]) {
    const ant = cyl(0.02, 0.02, 0.35, 5, C.ink, false);
    ant.position.set(x, 0.2, 0.75);
    ant.rotation.x = 0.6;
    ant.rotation.z = x * 2;
    g.add(ant);
  }
  const wings: THREE.Object3D[] = [];
  for (const x of [-1, 1]) {
    const pivot = new THREE.Group();
    pivot.position.set(x * 0.18, 0.32, 0.05);
    const w = new THREE.Mesh(geo("beewing", () => { const s = new THREE.SphereGeometry(0.5, 14, 8); s.scale(0.9, 0.06, 0.5); s.translate(0.45, 0, 0); return s; }), mat(0xffffff, { opacity: 0.7, rough: 0.2 }));
    w.scale.x = x;
    pivot.add(w);
    wings.push(pivot);
    g.add(pivot);
  }
  const carry = new THREE.Group();
  carry.position.set(0, -0.55, 0.1);
  g.add(carry);
  g.userData.wings = wings;
  g.userData.carry = carry;
  g.userData.body = body;
  g.scale.setScalar(scale);
  return g;
}

/** Wings beat; a bee at rest folds them. */
export function flap(b: THREE.Object3D, t: number, on = true) {
  const ws = b.userData.wings as THREE.Object3D[] | undefined;
  ws?.forEach((w, i) => { w.rotation.z = on ? Math.sin(t * 38 + i) * 0.55 * (i ? -1 : 1) : (i ? -0.15 : 0.15); });
}

/** A ball of pollen, the work an order carries out. */
export function pollen() {
  const m = new THREE.Mesh(geo("pollen", () => new THREE.IcosahedronGeometry(0.32, 1)), mat(C.pollen, { rough: 0.8 }));
  (m.material as THREE.MeshStandardMaterial).flatShading = true;
  m.castShadow = true;
  return m;
}

/** A drop of honey, what an order brings home. */
export function honeyDrop() {
  const m = new THREE.Mesh(geo("drop", () => { const s = new THREE.SphereGeometry(0.28, 14, 12); s.scale(1, 1.25, 1); return s; }), mat(C.honey, { rough: 0.15, emissive: C.honey, glow: 0.25 }));
  return m;
}

/** A hive box: two or three white supers on a stand, a wooden roof, an
 *  entrance with a landing board. The frames (one strip per super front) are
 *  in userData so a district can light them per order. */
export function hiveBox(supers = 3, roof = C.wood) {
  const g = new THREE.Group();
  const stand = box(3.4, 0.5, 3, C.woodDeep, true);
  g.add(stand);
  const frames: THREE.Mesh[] = [];
  for (let i = 0; i < supers; i++) {
    const s = rbox(3, 1.15, 2.6, C.white, 0.08);
    s.position.y = 0.5 + i * 1.18;
    g.add(s);
    const strip = box(2.2, 0.32, 0.06, ownMat(C.wax));
    strip.position.set(0, 0.5 + i * 1.18 + 0.42, 1.31);
    frames.push(strip);
    g.add(strip);
  }
  const top = 0.5 + supers * 1.18;
  const lid = rbox(3.4, 0.35, 3, mat(roof, { rough: 0.7 }), 0.08);
  lid.position.y = top;
  g.add(lid);
  const entrance = box(1.4, 0.18, 0.08, C.ink);
  entrance.position.set(0, 0.58, 1.31);
  const board = box(1.8, 0.08, 0.7, C.wood, true);
  board.position.set(0, 0.48, 1.6);
  g.add(entrance, board);
  g.userData.frames = frames;
  g.userData.top = top;
  return g;
}

/** The queen's hive: a great skep of stacked straw rings on a comb floor,
 *  with her chamber on top. The chamber's material is in userData. */
export function greatHive() {
  const g = new THREE.Group();
  // A floor of comb around it.
  for (let q = -2; q <= 2; q++) for (let r = -2; r <= 2; r++) {
    if (Math.abs(q + r) > 2) continue;
    const x = (q + r / 2) * 3.1, z = r * 2.7;
    if (Math.hypot(x, z) < 4) continue;
    const cell = cyl(1.5, 1.5, 0.22, 6, (q * 7 + r * 3) % 4 === 0 ? C.honey : C.wax, false);
    cell.position.set(x, 0, z);
    cell.rotation.y = Math.PI / 6;
    g.add(cell);
  }
  const rings = 7;
  for (let i = 0; i < rings; i++) {
    const k = i / (rings - 1);
    const r = 5.6 * Math.cos(k * 1.25) + 0.6;
    const ring = new THREE.Mesh(geo(`skep${i}`, () => new THREE.TorusGeometry(r, 0.62, 10, 40)), mat(i % 2 ? 0xe9c27a : 0xf0cf8c, { rough: 0.85 }));
    ring.rotation.x = Math.PI / 2;
    ring.position.y = 0.6 + i * 1.05;
    ring.castShadow = true;
    ring.receiveShadow = true;
    g.add(ring);
  }
  const core = cyl(4.8, 5.8, 7, 24, mat(0xe9c27a, { rough: 0.9 }));
  g.add(core);
  const door = new THREE.Mesh(geo("skepdoor", () => { const c = new THREE.CircleGeometry(1.1, 20, 0, Math.PI); return c; }), mat(C.ink));
  door.position.set(Math.sin(Math.PI / 4) * 6.25, 0.25, Math.cos(Math.PI / 4) * 6.25);
  door.rotation.y = Math.PI / 4;
  g.add(door);
  const chamberMat = ownMat(C.honey, { emissive: C.honey, glow: 0.08, rough: 0.3 });
  const chamber = cyl(1.8, 1.8, 1.2, 6, chamberMat);
  chamber.position.y = 7.5;
  chamber.rotation.y = Math.PI / 6;
  g.add(chamber);
  g.userData.chamber = chamber;
  g.userData.chamberMat = chamberMat;
  g.userData.top = 8.8;
  return g;
}

/** A flower: a stem, five petals and a heart. */
export function flower(petal = 0xffffff, seed = 0.5) {
  const g = new THREE.Group();
  const h = 0.9 + seed * 0.7;
  const stem = cyl(0.05, 0.06, h, 5, C.leafDeep, false);
  g.add(stem);
  const leaf = new THREE.Mesh(geo("leaf", () => { const s = new THREE.SphereGeometry(0.22, 8, 6); s.scale(1, 0.25, 0.5); s.translate(0.22, 0, 0); return s; }), mat(C.leaf));
  leaf.position.y = h * 0.4;
  leaf.rotation.y = seed * 6;
  g.add(leaf);
  const head = new THREE.Group();
  head.position.y = h;
  for (let i = 0; i < 5; i++) {
    const p = new THREE.Mesh(geo("petal", () => { const s = new THREE.SphereGeometry(0.2, 8, 6); s.scale(1, 0.35, 0.65); s.translate(0.2, 0, 0); return s; }), mat(petal, { rough: 0.6 }));
    p.rotation.y = (i * Math.PI * 2) / 5;
    head.add(p);
  }
  const heart = new THREE.Mesh(geo("heart", () => new THREE.SphereGeometry(0.12, 8, 6)), mat(C.honey));
  heart.position.y = 0.05;
  head.add(heart);
  head.rotation.x = -0.25;
  g.add(head);
  g.userData.sway = seed * 6;
  return g;
}

/** A small house with a pitched roof, a door and a window. */
export function cottage(w = 7, d = 5.5, h = 3.4, roof = C.wood) {
  const g = new THREE.Group();
  g.add(rbox(w, h, d, C.white, 0.15));
  const rise = d * 0.42;
  const shape = new THREE.Shape();
  shape.moveTo(-d / 2 - 0.5, 0); shape.lineTo(0, rise); shape.lineTo(d / 2 + 0.5, 0); shape.lineTo(-d / 2 - 0.5, 0);
  const r = new THREE.Mesh(geo(`roof${w}|${d}`, () => { const e = new THREE.ExtrudeGeometry(shape, { depth: w + 0.6, bevelEnabled: false }); e.translate(0, 0, -(w + 0.6) / 2); e.rotateY(Math.PI / 2); return e; }), mat(roof, { rough: 0.75 }));
  r.position.y = h;
  r.castShadow = true;
  g.add(r);
  const door = box(1.2, 2.1, 0.12, C.woodDeep);
  door.position.set(-w / 4, 0, d / 2 + 0.02);
  const win = box(1.4, 1, 0.1, mat(0xcfe3ff, { rough: 0.2 }));
  win.position.set(w / 4, 1.3, d / 2 + 0.03);
  const step = box(1.8, 0.2, 0.9, C.woodDeep, true);
  step.position.set(-w / 4, 0, d / 2 + 0.45);
  g.add(door, win, step);
  g.userData.door = new THREE.Vector3(-w / 4, 0, d / 2 + 1.6);
  return g;
}

/** A wooden fence: posts and two rails. */
export function woodFence(len: number) {
  const g = new THREE.Group();
  const n = Math.max(2, Math.round(len / 1.8));
  for (let i = 0; i <= n; i++) {
    const p = box(0.16, 1.1, 0.16, C.wood, true);
    p.position.x = -len / 2 + (i * len) / n;
    g.add(p);
  }
  for (const y of [0.45, 0.9]) {
    const rail = box(len, 0.1, 0.08, C.woodDeep);
    rail.position.y = y;
    g.add(rail);
  }
  return g;
}

/** A wooden sign on two posts; its face is a canvas screen. */
export function signboard(w = 5, h = 3) {
  const g = new THREE.Group();
  for (const x of [-w / 2 + 0.3, w / 2 - 0.3]) {
    const p = box(0.22, 2.2 + h, 0.22, C.woodDeep, true);
    p.position.x = x;
    g.add(p);
  }
  const back = rbox(w, h, 0.25, C.wood, 0.08);
  back.position.y = 2;
  g.add(back);
  const s = screen(w - 0.5, h - 0.5, 360);
  s.mesh.position.set(0, 2 + h / 2, 0.14);
  g.add(s.mesh);
  return { node: g, draw: s.draw };
}

/** A honey jar whose honey level a district sets (0–1). */
export function jar() {
  const g = new THREE.Group();
  const glass = cyl(0.9, 0.9, 2.2, 20, mat(0xffffff, { opacity: 0.35, rough: 0.1 }), false);
  const honey = cyl(0.82, 0.82, 1, 20, mat(C.honey, { rough: 0.2, emissive: C.honeyDeep, glow: 0.15 }), false);
  honey.position.y = 0.05;
  const lid = cyl(0.95, 0.95, 0.3, 20, C.wood, true);
  lid.position.y = 2.2;
  g.add(honey, glass, lid);
  g.userData.honey = honey;
  return g;
}
