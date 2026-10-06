import * as THREE from "three";
import { box, cyl, mat, ownMat, rbox } from "./kit";

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

let accent = 0xf2b416;
/** The theme's colour, for eyes and held work. */
export function setAccent(c: number) { accent = c; }

let capsule: THREE.BufferGeometry | null = null;
const arm = () => (capsule ??= (() => { const c = new THREE.CapsuleGeometry(0.11, 0.42, 4, 10); c.translate(0, -0.3, 0); return c; })());

/**
 * One robot. `trim` colours its visor band — how CLIs of different makers
 * tell apart without anyone's logo.
 */
export function robot(kind: RobotKind, trim = accent) {
  const g = new THREE.Group();
  const shell = ownMat(SHELL, { rough: 0.45 });
  const joint = mat(JOINT, { rough: 0.5 });

  if (kind === "bee") {
    // A small hovering one: a thruster where the legs would be, a rotor on top.
    const thruster = cyl(0.18, 0.3, 0.3, 14, joint, false);
    thruster.position.y = 0.05;
    const glow = new THREE.Mesh(new THREE.CircleGeometry(0.22, 16), ownMat(accent, { emissive: accent, glow: 0.8 }));
    glow.rotation.x = Math.PI / 2;
    glow.position.y = 0.04;
    g.add(thruster, glow);
    g.userData.glow = glow;
  } else {
    for (const x of [-0.2, 0.2]) {
      const leg = cyl(0.12, 0.13, 0.45, 10, joint, true);
      leg.position.set(x, 0, 0);
      const foot = rbox(0.28, 0.12, 0.36, joint, 0.05);
      foot.position.set(x, 0, 0.05);
      g.add(leg, foot);
    }
  }
  const torso = rbox(0.92, 0.82, 0.62, shell, 0.18);
  torso.position.y = 0.42;
  const chest = rbox(0.42, 0.22, 0.05, ownMat(FACE, { emissive: trim, glow: 0.0 }), 0.05, false);
  chest.position.set(0, 0.85, 0.31);
  g.add(torso, chest);

  const arms: THREE.Group[] = [];
  for (const x of [-0.58, 0.58]) {
    const pivot = new THREE.Group();
    pivot.position.set(x, 1.12, 0);
    const a = new THREE.Mesh(arm(), shell);
    a.castShadow = true;
    const hand = new THREE.Mesh(new THREE.SphereGeometry(0.13, 10, 8), joint);
    hand.position.y = -0.62;
    pivot.add(a, hand);
    arms.push(pivot);
    g.add(pivot);
  }

  const head = new THREE.Group();
  head.position.y = 1.32;
  const skull = rbox(0.86, 0.66, 0.72, shell, 0.2);
  const face = rbox(0.7, 0.42, 0.06, mat(FACE, { rough: 0.25 }), 0.08, false);
  face.position.set(0, 0.12, 0.36);
  const band = box(0.88, 0.07, 0.74, mat(trim, { rough: 0.5 }));
  band.position.y = 0.02;
  const eyeMat = ownMat(EYE_IDLE, { emissive: EYE_IDLE, glow: 0.9 });
  const eyes: THREE.Mesh[] = [];
  for (const x of [-0.15, 0.15]) {
    const e = new THREE.Mesh(new THREE.CapsuleGeometry(0.055, 0.08, 4, 8), eyeMat);
    e.rotation.z = Math.PI / 2;
    e.position.set(x, 0.33, 0.4);
    eyes.push(e);
    head.add(e);
  }
  const antMat = ownMat(INK, { emissive: accent, glow: 0 });
  const stalk = cyl(0.025, 0.025, 0.28, 6, joint, false);
  stalk.position.y = 0.66;
  const bulb = new THREE.Mesh(new THREE.SphereGeometry(0.08, 10, 8), antMat);
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
    if (!still) { la = Math.sin(k * 1.1) * 0.06; ra = -la; hy = Math.sin(k * 0.35) * 0.25; }
  } else if (s === "work" && u.desk) {
    // Typing in earnest: hands taking quick turns on the keys, the body
    // keeping time, the head sweeping the screen, the eyes reading along.
    const tap = still ? 0 : 0.22;
    la = -1.05 + Math.sin(k * 19) * tap; ra = -1.05 + Math.sin(k * 19 + Math.PI) * tap; lz = 0.18; rz = -0.18;
    hx = 0.16 + (still ? 0 : Math.sin(k * 1.3) * 0.05); hy = still ? 0 : Math.sin(k * 0.8) * 0.22;
    bob = still ? 0 : Math.abs(Math.sin(k * 9.5)) * 0.035;
    if (!still) (u.eyes as THREE.Mesh[]).forEach((e, i) => { e.position.x = (i ? 0.15 : -0.15) + Math.sin(k * 3.2) * 0.04; });
  } else if (s === "work") {
    la = ra = -1.15; lz = 0.25; rz = -0.25;
    if (!still) { bob = Math.abs(Math.sin(k * 5)) * 0.04; hy = Math.sin(k * 2.2) * 0.12; (u.cube as THREE.Object3D).rotation.y = k * 1.4; r.rotation.z = Math.sin(k * 2) * 0.03; }
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
  l.rotation.set(la, 0, lz);
  rr.rotation.set(ra, 0, rz);
  if (s !== "work") (u.eyes as THREE.Mesh[]).forEach((e, i) => { e.position.x = i ? 0.15 : -0.15; });
  head.rotation.set(hx, hy, 0);
  // Blink now and then.
  if (!still && s !== "lost") {
    const blink = (k % 4.2) < 0.12 ? 0.15 : 1;
    (u.eyes as THREE.Mesh[]).forEach((e) => (e.scale.x = blink));
  }
  if (u.kind === "bee") {
    if (!still && u.rotor) (u.rotor as THREE.Object3D).rotation.y = k * (s === "lost" ? 0 : 18);
  } else {
    (u.base ??= r.position.y);
    r.position.y = (u.base as number) + bob;
  }
}
