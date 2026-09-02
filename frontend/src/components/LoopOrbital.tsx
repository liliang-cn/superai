import React, { useEffect, useRef } from "react";
import * as THREE from "three";

/**
 * The loop, as an orbit.
 *
 * agent-go has exactly one loop — assemble context, call the model, run the
 * tools, lint the answer, checkpoint, hand off to the next segment — and every
 * run is a lap around it. This draws that literally: six stations on a ring,
 * the one in flight lit, and particles streaming station to station in
 * proportion to what the run is actually doing (tool calls, tokens, rejections).
 *
 * It is the one place on the page allowed to move on its own. Everything else
 * animates when its number changes; this breathes.
 */

export interface OrbitalTelemetry {
  /** 0..5 — which station is lit. */
  stage: number;
  /** Rough activity, 0..1: drives particle count and speed. */
  activity: number;
  /** 0..1 share of turns rejected at the lint gate: tints the gate. */
  rejected: number;
  /** True while a run is in flight. Idle rings dim and slow. */
  live: boolean;
}

const STATIONS = ["context", "model", "tools", "lint", "checkpoint", "segment"];

// The orbital follows the app theme. Light is paper and cobalt, dark is the
// cockpit; the same signal colours as the CSS, so the canvas and the panel
// around it read as one instrument.
type Palette = { alive: number; rose: number; dim: number; ring: number; ringLive: number; disc: number; mote: number; discOpacity: number };
const DARK: Palette = { alive: 0x5ee0ff, rose: 0xff5c7a, dim: 0x2a3550, ring: 0x1f2d4d, ringLive: 0x2a5a80, disc: 0x0b1224, mote: 0x3a4a6e, discOpacity: 0.55 };
const LIGHT: Palette = { alive: 0x1f5eff, rose: 0xd6335a, dim: 0xb9c3d6, ring: 0xc8d1e2, ringLive: 0x8fb0ff, disc: 0xe9eef7, mote: 0x9aa4b8, discOpacity: 0.7 };
const isDark = () => document.documentElement.dataset.theme === "dark";

export default function LoopOrbital({ t }: { t: OrbitalTelemetry }) {
  const host = useRef<HTMLDivElement | null>(null);
  const tel = useRef(t);
  tel.current = t;

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;

    let pal: Palette = isDark() ? DARK : LIGHT;
    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(38, 1, 0.1, 100);
    camera.position.set(0, 3.2, 6.4);
    camera.lookAt(0, 0, 0);
    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
    renderer.setPixelRatio(Math.min(2, window.devicePixelRatio || 1));
    el.appendChild(renderer.domElement);

    const R = 2.4;
    const group = new THREE.Group();
    scene.add(group);

    // The ring: a thin torus, the road the loop runs on.
    const ring = new THREE.Mesh(
      new THREE.TorusGeometry(R, 0.012, 8, 160),
      new THREE.MeshBasicMaterial({ color: pal.ring, transparent: true, opacity: 0.9 }),
    );
    ring.rotation.x = Math.PI / 2;
    group.add(ring);

    // A faint inner ring and a grid disc: depth without clutter.
    const inner = new THREE.Mesh(
      new THREE.TorusGeometry(R * 0.62, 0.006, 6, 120),
      new THREE.MeshBasicMaterial({ color: pal.ring, transparent: true, opacity: 0.45 }),
    );
    inner.rotation.x = Math.PI / 2;
    group.add(inner);
    const disc = new THREE.Mesh(
      new THREE.RingGeometry(R * 0.2, R * 1.35, 96, 1),
      new THREE.MeshBasicMaterial({ color: pal.disc, transparent: true, opacity: pal.discOpacity, side: THREE.DoubleSide }),
    );
    disc.rotation.x = -Math.PI / 2;
    disc.position.y = -0.02;
    group.add(disc);

    // Stations.
    const stations: THREE.Mesh[] = [];
    const halos: THREE.Mesh[] = [];
    const pos = (i: number) => {
      const a = (i / STATIONS.length) * Math.PI * 2 - Math.PI / 2;
      return new THREE.Vector3(Math.cos(a) * R, 0, Math.sin(a) * R);
    };
    for (let i = 0; i < STATIONS.length; i++) {
      const p = pos(i);
      const m = new THREE.Mesh(
        new THREE.SphereGeometry(0.11, 24, 24),
        new THREE.MeshBasicMaterial({ color: pal.dim }),
      );
      m.position.copy(p);
      group.add(m);
      stations.push(m);
      const h = new THREE.Mesh(
        new THREE.SphereGeometry(0.28, 24, 24),
        new THREE.MeshBasicMaterial({ color: pal.alive, transparent: true, opacity: 0 }),
      );
      h.position.copy(p);
      group.add(h);
      halos.push(h);
    }

    // Particles on the ring.
    const N = 220;
    const pg = new THREE.BufferGeometry();
    const parr = new Float32Array(N * 3);
    const phase = new Float32Array(N);
    const speed = new Float32Array(N);
    for (let i = 0; i < N; i++) {
      phase[i] = Math.random();
      speed[i] = 0.6 + Math.random() * 0.9;
    }
    pg.setAttribute("position", new THREE.BufferAttribute(parr, 3));
    const pm = new THREE.PointsMaterial({ color: pal.alive, size: 0.045, transparent: true, opacity: 0.9, sizeAttenuation: true });
    const points = new THREE.Points(pg, pm);
    group.add(points);

    // A few slow motes off the ring, for atmosphere.
    const M = 90;
    const mg = new THREE.BufferGeometry();
    const marr = new Float32Array(M * 3);
    for (let i = 0; i < M; i++) {
      const r = R * (0.3 + Math.random() * 1.1), a = Math.random() * Math.PI * 2;
      marr[i * 3] = Math.cos(a) * r; marr[i * 3 + 1] = (Math.random() - 0.5) * 1.2; marr[i * 3 + 2] = Math.sin(a) * r;
    }
    mg.setAttribute("position", new THREE.BufferAttribute(marr, 3));
    const motes = new THREE.PointsMaterial({ color: pal.mote, size: 0.02, transparent: true, opacity: 0.6 });
    group.add(new THREE.Points(mg, motes));

    // Re-skin when the app flips theme: the attribute on <html> is the one
    // source of truth, so watch it rather than poll.
    const applyPalette = () => {
      pal = isDark() ? DARK : LIGHT;
      (inner.material as THREE.MeshBasicMaterial).color.set(pal.ring);
      (disc.material as THREE.MeshBasicMaterial).color.set(pal.disc);
      (disc.material as THREE.MeshBasicMaterial).opacity = pal.discOpacity;
      pm.color.set(pal.alive);
      motes.color.set(pal.mote);
    };
    const mo = new MutationObserver(applyPalette);
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

    const resize = () => {
      const w = el.clientWidth || 1, h = el.clientHeight || 1;
      renderer.setSize(w, h, false);
      camera.aspect = w / h;
      camera.updateProjectionMatrix();
    };
    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(el);

    let raf = 0;
    let last = performance.now();
    let tt = 0;
    const tmpV = new THREE.Vector3();
    const render = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      const { stage, activity, rejected, live } = tel.current;
      const act = live ? 0.3 + activity * 0.7 : 0.18;
      if (!reduce) tt += dt * (live ? 0.18 : 0.07);
      group.rotation.y = tt;

      // Stations: the lit one breathes, the rest sit dim. Lint tints toward
      // rose as rejections rise; checkpoint/segment lean lime once done.
      for (let i = 0; i < STATIONS.length; i++) {
        const on = live && i === stage;
        const base = i === 3 ? new THREE.Color(pal.alive).lerp(new THREE.Color(pal.rose), Math.min(1, rejected * 1.6)) : new THREE.Color(pal.alive);
        (stations[i].material as THREE.MeshBasicMaterial).color.copy(on ? base : new THREE.Color(pal.dim).lerp(base, 0.35));
        const pulse = reduce ? 0.5 : 0.5 + 0.5 * Math.sin(now / 320 + i);
        (halos[i].material as THREE.MeshBasicMaterial).opacity = on ? 0.18 + pulse * 0.22 : 0;
        (halos[i].material as THREE.MeshBasicMaterial).color.copy(base);
        const s = on ? 1 + pulse * 0.25 : 1;
        stations[i].scale.setScalar(s);
        halos[i].scale.setScalar(on ? 1 + pulse * 0.5 : 1);
      }

      // Particles: advance along the ring; only a share proportional to
      // activity is visible — the rest park at the origin (size 0 via y=-9).
      const visible = Math.floor(N * act);
      for (let i = 0; i < N; i++) {
        if (!reduce) phase[i] = (phase[i] + dt * speed[i] * (0.05 + act * 0.25)) % 1;
        const a = phase[i] * Math.PI * 2 - Math.PI / 2;
        if (i < visible) {
          tmpV.set(Math.cos(a) * R, Math.sin(phase[i] * 40 + i) * 0.03, Math.sin(a) * R);
        } else {
          tmpV.set(0, -9, 0);
        }
        parr[i * 3] = tmpV.x; parr[i * 3 + 1] = tmpV.y; parr[i * 3 + 2] = tmpV.z;
      }
      pg.attributes.position.needsUpdate = true;
      pm.opacity = live ? 0.95 : 0.55;
      (ring.material as THREE.MeshBasicMaterial).color.set(live ? pal.ringLive : pal.ring);

      renderer.render(scene, camera);
      raf = requestAnimationFrame(render);
    };
    raf = requestAnimationFrame(render);

    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      mo.disconnect();
      renderer.dispose();
      el.removeChild(renderer.domElement);
    };
  }, []);

  return (
    <div className="orbital" ref={host} aria-hidden>
      <div className="orbital-labels">
        {STATIONS.map((s, i) => (
          <span key={s} className={i === t.stage && t.live ? "on" : ""}>{s}</span>
        ))}
      </div>
    </div>
  );
}
