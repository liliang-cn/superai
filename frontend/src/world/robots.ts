import * as THREE from "three";
import { FIN, box, contact, cyl, damp, mat, ownMat, rbox } from "./kit";

// The agents: every agent in SuperAI is a robot, and only agents are. The
// machines they run on stay machines. A robot says what it is doing with its
// body — arms, eyes, antenna — so its state reads without a word.

export type RobotState = "idle" | "work" | "think" | "wait" | "fail" | "lost";
export type RobotKind = "queen" | "worker" | "bee" | "cli";

export const INK = 0x16202b;
export const SHELL = 0xfbfcfd;
const JOINT = 0x2a3442;
const FACE = 0x18212b;
const EYE_IDLE = 0xbfe3ff;
const EYE_BAD = 0xe2553f;
const EYE_X = 0.12;

let accent = 0xf2b416;
/** The theme's colour, for eyes and held work. */
export function setAccent(c: number) { accent = c; }

let capsule: THREE.BufferGeometry | null = null;
let collarGeo: THREE.BufferGeometry | null = null;
let faceGeo: THREE.BufferGeometry | null = null;
/** A rounded rectangle round the origin. */
function rounded(w: number, d: number, r: number) {
  const x = -w / 2, y = -d / 2, s = new THREE.Shape();
  s.moveTo(x + r, y); s.lineTo(x + w - r, y); s.absarc(x + w - r, y + r, r, -Math.PI / 2, 0, false);
  s.lineTo(x + w, y + d - r); s.absarc(x + w - r, y + d - r, r, 0, Math.PI / 2, false);
  s.lineTo(x + r, y + d); s.absarc(x + r, y + d - r, r, Math.PI / 2, Math.PI, false);
  s.lineTo(x, y + r); s.absarc(x + r, y + r, r, Math.PI, Math.PI * 1.5, false);
  return s;
}
// The skull's rounding: small enough that its flat front (0.58 x 0.38) holds
// the whole face, so no edge of the glass stands off the curve.
const SKULL_R = 0.14;
/** The visor band: a ring under the head with the skull's rounded corners and
 *  bevelled edges, not a flat plate. */
const collar = () => (collarGeo ??= (() => {
  const g = new THREE.ExtrudeGeometry(rounded(0.82, 0.68, SKULL_R - 0.02), { depth: 0.05, bevelEnabled: true, bevelThickness: 0.025, bevelSize: 0.025, bevelSegments: 6, curveSegments: 24 });
  g.rotateX(-Math.PI / 2);
  return g;
})());
/** The face: a pane of dark glass 0.56 x 0.36, its corners 0.09 round, its
 *  back sunk in the skull's front and its face a hair proud of it. */
const glassFace = () => (faceGeo ??= (() => {
  const b = 0.008;
  const g = new THREE.ExtrudeGeometry(rounded(0.56 - b * 2, 0.36 - b * 2, 0.09 - b), { depth: 0.03, bevelEnabled: true, bevelThickness: b, bevelSize: b, bevelSegments: 3, curveSegments: 16 });
  g.translate(0, 0, -0.022);
  return g;
})());
const arm = () => (capsule ??= (() => { const c = new THREE.CapsuleGeometry(0.11, 0.42, 8, 20); c.translate(0, -0.3, 0); return c; })());
let hg: THREE.BufferGeometry | null = null, eg: THREE.BufferGeometry | null = null, bg: THREE.BufferGeometry | null = null;
const handGeo = () => (hg ??= new THREE.SphereGeometry(0.13, 24, 16));
// The eyes: flat upright lights on the glass, fully rounded.
const eyeGeo = () => (eg ??= new THREE.ShapeGeometry(rounded(0.1, 0.13, 0.05), 12));
const bulbGeo = () => (bg ??= new THREE.SphereGeometry(0.08, 20, 14));

/**
 * One robot. `trim` colours its visor band — how CLIs of different makers
 * tell apart without anyone's logo.
 */
export function robot(kind: RobotKind, trim = accent) {
  const g = new THREE.Group();
  // Satin white with a light clear coat; joints in a soft dark graphite.
  const shell = ownMat(SHELL, { rough: 0.4, clear: 0.6, clearRough: 0.22 });
  const joint = mat(JOINT, { rough: 0.38, metal: 0.25 });

  if (kind === "bee") {
    // A small hovering one: a thruster where the legs would be, a rotor on top.
    const thruster = cyl(0.18, 0.3, 0.3, 28, joint, false);
    thruster.position.y = 0.05;
    const glow = new THREE.Mesh(new THREE.CircleGeometry(0.22, 32), ownMat(accent, { emissive: accent, glow: 0.8 }));
    glow.rotation.x = Math.PI / 2;
    glow.position.y = 0.04;
    g.add(thruster, glow);
    g.userData.glow = glow;
  } else {
    for (const x of [-0.2, 0.2]) {
      const leg = cyl(0.12, 0.13, 0.45, 24, joint, true);
      leg.position.set(x, 0, 0);
      const foot = rbox(0.28, 0.12, 0.36, joint, 0.05);
      foot.position.set(x, 0, 0.05);
      g.add(leg, foot);
    }
    // Where it stands, the floor darkens a little.
    g.add(contact(1.5, 1.2, 0.2));
  }
  const torso = rbox(0.92, 0.82, 0.62, shell, 0.18);
  torso.position.y = 0.42;
  const chest = rbox(0.42, 0.22, 0.05, ownMat(FACE, { emissive: trim, glow: 0.0 }), 0.05, false);
  chest.position.set(0, 0.74, 0.31);
  // A neck from the torso up into the collar: the head stands clear of the
  // torso so it can nod, and without this it floated over it.
  const neck = cyl(0.2, 0.2, 0.16, 32, joint, false);
  neck.position.y = 1.18;
  g.add(torso, chest, neck);

  const arms: THREE.Group[] = [];
  for (const x of [-0.58, 0.58]) {
    const pivot = new THREE.Group();
    pivot.position.set(x, 1.12, 0);
    const a = new THREE.Mesh(arm(), shell);
    a.castShadow = true;
    const hand = new THREE.Mesh(handGeo(), joint);
    hand.castShadow = true;
    hand.position.y = -0.62;
    pivot.add(a, hand);
    arms.push(pivot);
    g.add(pivot);
  }

  const head = new THREE.Group();
  head.position.y = 1.32;
  const skull = rbox(0.86, 0.66, 0.72, shell, SKULL_R);
  // The visor: black glass set into the skull's flat front, so the studio
  // light runs across it.
  const face = new THREE.Mesh(glassFace(), FIN.glass());
  face.position.set(0, 0.33, 0.36);
  const band = new THREE.Mesh(collar(), mat(trim, { rough: 0.32, clear: 0.5 }));
  band.position.y = 0.02;
  const eyeMat = ownMat(EYE_IDLE, { emissive: EYE_IDLE, glow: 0.9 });
  const eyes: THREE.Mesh[] = [];
  // Light on the glass, not parts stuck on it.
  for (const x of [-EYE_X, EYE_X]) {
    const e = new THREE.Mesh(eyeGeo(), eyeMat);
    e.position.set(x, 0.33, 0.378);
    eyes.push(e);
    head.add(e);
  }
  const antMat = ownMat(INK, { emissive: accent, glow: 0 });
  const stalk = cyl(0.025, 0.025, 0.28, 12, FIN.aluDark(), false);
  stalk.position.y = 0.66;
  const bulb = new THREE.Mesh(bulbGeo(), antMat);
  bulb.position.y = 0.98;
  head.add(skull, face, band, stalk, bulb);
  g.add(head);

  if (kind === "bee") {
    const rotor = new THREE.Group();
    rotor.position.y = 1.1;
    for (let i = 0; i < 2; i++) {
      const blade = box(1.3, 0.03, 0.14, mat(JOINT));
      blade.rotation.y = (i * Math.PI) / 2;
      rotor.add(blade);
    }
    head.add(rotor);
    g.userData.rotor = rotor;
  }
  // What it holds while working: a glowing cube of work.
  const carry = new THREE.Group();
  carry.position.set(0, 0.62, 0.62);
  const cube = rbox(0.38, 0.38, 0.38, ownMat(accent, { emissive: accent, glow: 0.6, rough: 0.3 }), 0.06, false);
  cube.position.y = -0.19;
  carry.add(cube);
  carry.visible = false;
  g.add(carry);

  const seed = Math.random() * 10;
  Object.assign(g.userData, { kind, arms, head, eyes, eyeMat, antMat, chest, carry, cube, shell, seed, state: "idle" as RobotState });
  g.scale.setScalar(kind === "queen" ? 2.1 : kind === "bee" ? 0.85 : kind === "cli" ? 1.15 : 1.4);
  return g;
}

/** Sets a robot's colours for its state; `pose` does the moving. */
export function setState(r: THREE.Object3D, state: RobotState) {
  const u = r.userData;
  if (u.state === state) return;
  u.state = state;
  const eye = state === "fail" ? EYE_BAD : state === "work" || state === "wait" || state === "think" ? accent : EYE_IDLE;
  (u.eyeMat as THREE.MeshStandardMaterial).color.set(state === "lost" ? 0x3a4450 : eye);
  (u.eyeMat as THREE.MeshStandardMaterial).emissive.set(state === "lost" ? 0 : eye);
  (u.shell as THREE.MeshStandardMaterial).color.set(state === "lost" ? 0xc9cfd6 : SHELL);
  // At a desk the work is on the screen, not in the hands.
  (u.carry as THREE.Object3D).visible = state === "work" && !u.desk;
  const chest = (u.chest as THREE.Mesh).material as THREE.MeshStandardMaterial;
  chest.emissiveIntensity = state === "work" ? 0.8 : 0;
}

/** Moves a robot for its state. Called every frame. */
export function pose(r: THREE.Object3D, t: number, reduced = false) {
  // A tool call just happened: a quick hand up, over everything else.
  const gesture = !reduced && (r.userData.gestureUntil ?? 0) > t;
  const u = r.userData;
  const s: RobotState = u.state;
  const [l, rr] = u.arms as THREE.Group[];
  const head = u.head as THREE.Group;
  const k = t + u.seed;
  const still = reduced;
  // Defaults: arms down, head level.
  let la = 0, ra = 0, lz = 0.08, rz = -0.08, hx = 0, hy = 0, bob = 0;
  if (s === "idle") {
    if (!still) { la = Math.sin(k * 1.1) * 0.05; ra = -la; hy = Math.sin(k * 0.35) * 0.22; bob = (Math.sin(k * 1.6) + 1) * 0.008; }
  } else if (s === "work" && u.desk) {
    // Typing in earnest: hands taking quick turns on the keys, the body
    // keeping time, the head sweeping the screen, the eyes reading along.
    // Reached forward and down onto the keys: from the shoulder at 1.12 the
    // hand lands about 0.18 lower and 0.6 ahead, where the desk puts them.
    const tap = still ? 0 : 0.12;
    la = -1.28 + Math.sin(k * 19) * tap; ra = -1.28 + Math.sin(k * 19 + Math.PI) * tap; lz = 0.14; rz = -0.14;
    hx = 0.16 + (still ? 0 : Math.sin(k * 1.3) * 0.05); hy = still ? 0 : Math.sin(k * 0.8) * 0.22;
    bob = still ? 0 : Math.abs(Math.sin(k * 9.5)) * 0.035;
    if (!still) (u.eyes as THREE.Mesh[]).forEach((e, i) => { e.position.x = (i ? EYE_X : -EYE_X) + Math.sin(k * 3.2) * 0.04; });
  } else if (s === "work") {
    la = ra = -1.15; lz = 0.25; rz = -0.25;
    if (!still) { bob = Math.abs(Math.sin(k * 5)) * 0.04; hy = Math.sin(k * 2.2) * 0.12; (u.cube as THREE.Object3D).rotation.y = k * 1.4; }
  } else if (s === "think") {
    // A hand to the chin, the head to one side, the antenna going.
    ra = -2.2; rz = -0.55; hx = -0.1; hy = 0.25;
    if (!still) { la = Math.sin(k) * 0.05; hy += Math.sin(k * 0.7) * 0.08; }
    (u.antMat as THREE.MeshStandardMaterial).emissiveIntensity = still ? 1 : (Math.sin(k * 8) > 0 ? 1.4 : 0.1);
  } else if (s === "wait") {
    rz = -2.6; ra = -0.2;
    if (!still) rz += Math.sin(k * 4) * 0.25;
    (u.eyeMat as THREE.MeshStandardMaterial).emissiveIntensity = still ? 1 : 0.6 + 0.6 * Math.max(0, Math.sin(k * 5));
  } else if (s === "fail") {
    hx = 0.45; la = ra = 0.1; lz = 0.02; rz = -0.02;
  } else if (s === "lost") {
    hx = 0.35;
  }
  if (s !== "think") (u.antMat as THREE.MeshStandardMaterial).emissiveIntensity = s === "work" ? 0.6 : 0;
  if (gesture) { ra = -2.9; rz = -0.35; }
  // Eating a coin: a quick chomp.
  if (!reduced && (u.chompUntil ?? 0) > t) hx = 0.25 * Math.abs(Math.sin(t * 28));
  // Joints follow where the pose wants them rather than jumping there, so a
  // change of state is a movement, not a cut.
  const dt = Math.min(0.1, Math.max(0, t - (u.lastT ?? t)));
  u.lastT = t;
  const j = (u.joints ??= { la, ra, lz, rz, hx, hy, bob, tilt: 0 }) as Record<string, number>;
  const rate = reduced ? 1e3 : 11;
  const tilt = s === "work" && !u.desk && !reduced ? Math.sin(k * 2) * 0.03 : 0;
  const want: Record<string, number> = { la, ra, lz, rz, hx, hy, bob, tilt };
  for (const key in want) j[key] = damp(j[key], want[key], key === "bob" ? 18 : rate, dt);
  l.rotation.set(j.la, 0, j.lz);
  rr.rotation.set(j.ra, 0, j.rz);
  if (s !== "work") (u.eyes as THREE.Mesh[]).forEach((e, i) => { e.position.x = i ? EYE_X : -EYE_X; });
  head.rotation.set(j.hx, j.hy, 0);
  // (A robot at a desk was turned with lookAt, whose Euler angles may carry a
  // half-turn in z; only free-standing ones sway.)
  if (!u.desk) r.rotation.z = j.tilt;
  // Blink now and then: the lights squeeze shut and open again.
  if (!still && s !== "lost") {
    const ph = k % 4.2;
    const blink = ph < 0.18 ? 1 - 0.85 * Math.sin((ph / 0.18) * Math.PI) : 1;
    (u.eyes as THREE.Mesh[]).forEach((e) => (e.scale.y = blink));
  }
  if (u.kind === "bee") {
    if (!still && u.rotor) (u.rotor as THREE.Object3D).rotation.y = k * (s === "lost" ? 0 : 18);
  } else {
    (u.base ??= r.position.y);
    r.position.y = (u.base as number) + j.bob;
  }
}

/**
 * Makes a robot a ghost of itself: where an agent would run but does not now
 * (an HA service's standby host). Every material its own, see-through, so
 * state colours still apply; `faint` for a standby that is not ready.
 */
export function ghost(r: THREE.Object3D, faint = false) {
  const u = r.userData;
  if (u.ghost === (faint ? 2 : 1)) return;
  const done = new Map<THREE.Material, THREE.Material>();
  const own = (m: THREE.Material) => {
    let c = done.get(m);
    if (!c) {
      c = u.ghost ? m : m.clone();
      c.transparent = true;
      c.opacity = faint ? 0.16 : 0.3;
      c.depthWrite = false;
      done.set(m, c);
    }
    return c;
  };
  r.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (!mesh.isMesh) return;
    mesh.material = Array.isArray(mesh.material) ? mesh.material.map(own) : own(mesh.material);
    mesh.castShadow = false;
  });
  for (const k of ["eyeMat", "antMat", "shell"]) if (u[k] && done.has(u[k])) u[k] = done.get(u[k]);
  u.ghost = faint ? 2 : 1;
}
