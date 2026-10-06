import * as THREE from "three";

// Work as data on the move: while an agent spends tokens, packets run both
// ways along its path to the core — the prompt going out (blue), the answer
// coming back (green) — the faster it spends, the denser the traffic. One
// pool of packets serves every path in the world.

const OUT = 0x2f8bff, BACK = 0x22c58b;
const GEO = new THREE.BoxGeometry(0.34, 0.14, 0.14);
const mats = new Map<number, THREE.MeshBasicMaterial>();
const matOf = (c: number) => {
  let m = mats.get(c);
  if (!m) { m = new THREE.MeshBasicMaterial({ color: c, transparent: true, opacity: 0.95, toneMapped: false }); mats.set(c, m); }
  return m;
};

type P = { m: THREE.Mesh; trail: THREE.Mesh; curve: THREE.Curve<THREE.Vector3>; t: number; dur: number; back: boolean; lift: number; arrive?: () => void };

/** Traffic on one path: `rate` packets a second each way. */
export interface Link { curve: () => THREE.Curve<THREE.Vector3> | null; rate: number; acc: number; arrive?: () => void }

export class Packets {
  private pool: P[] = [];
  private live: P[] = [];
  readonly links = new Set<Link>();
  constructor(private parent: THREE.Object3D, private reduced: boolean) {}

  link(curve: () => THREE.Curve<THREE.Vector3> | null, arrive?: () => void): Link {
    const l = { curve, rate: 0, acc: 0, arrive };
    this.links.add(l);
    return l;
  }
  remove(l: Link) { this.links.delete(l); }

  /** A burst both ways, for a moment of exchange. */
  burst(curve: THREE.Curve<THREE.Vector3>, n: number, arrive?: () => void) {
    if (this.reduced) return;
    for (let i = 0; i < n; i++) { this.spawn(curve, false, -i * 0.12); this.spawn(curve, true, -i * 0.12 - 0.3, arrive); }
  }

  private spawn(curve: THREE.Curve<THREE.Vector3>, back: boolean, delay = 0, arrive?: () => void) {
    let p = this.pool.pop();
    if (!p) {
      const m = new THREE.Mesh(GEO, matOf(OUT));
      const trail = new THREE.Mesh(GEO, new THREE.MeshBasicMaterial({ color: OUT, transparent: true, opacity: 0.3, toneMapped: false, depthWrite: false }));
      trail.scale.set(2.4, 0.6, 0.6);
      this.parent.add(m, trail);
      p = { m, trail, curve, t: 0, dur: 1, back, lift: 0 };
    }
    const len = curve.getLength();
    Object.assign(p, { curve, t: delay, dur: 0.5 + len / 26, back, lift: 0.35 + Math.random() * 0.25, arrive });
    const c = back ? BACK : OUT;
    p.m.material = matOf(c);
    (p.trail.material as THREE.MeshBasicMaterial).color.set(c);
    p.m.visible = p.trail.visible = delay >= 0;
    this.live.push(p);
  }

  tick(dt: number) {
    if (!this.reduced) {
      for (const l of this.links) {
        if (l.rate <= 0.01) continue;
        const c = l.curve();
        if (!c) continue;
        l.acc += dt * Math.min(10, l.rate);
        while (l.acc >= 1) {
          l.acc -= 1;
          // Out and back, in turn.
          this.spawn(c, Math.random() < 0.5, 0, l.arrive);
        }
      }
    }
    const pos = new THREE.Vector3(), ahead = new THREE.Vector3();
    for (let i = this.live.length - 1; i >= 0; i--) {
      const p = this.live[i];
      p.t += dt / p.dur;
      if (p.t < 0) continue;
      if (p.t >= 1) {
        if (p.back) p.arrive?.();
        p.m.visible = p.trail.visible = false;
        this.live.splice(i, 1);
        this.pool.push(p);
        continue;
      }
      p.m.visible = p.trail.visible = true;
      const k = p.back ? 1 - p.t : p.t;
      p.curve.getPointAt(k, pos);
      p.curve.getPointAt(Math.min(1, Math.max(0, k + (p.back ? -0.01 : 0.01))), ahead);
      pos.y += p.lift;
      ahead.y += p.lift;
      p.m.position.copy(pos);
      p.m.lookAt(ahead);
      p.m.rotateY(Math.PI / 2);
      // The trail sits just behind, pointing the same way.
      const dir = ahead.clone().sub(pos).normalize();
      p.trail.position.copy(pos).addScaledVector(dir, -0.45);
      p.trail.quaternion.copy(p.m.quaternion);
    }
  }
}
