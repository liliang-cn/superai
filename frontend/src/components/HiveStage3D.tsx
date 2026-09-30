import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { EffectComposer } from "three/examples/jsm/postprocessing/EffectComposer.js";
import { RenderPass } from "three/examples/jsm/postprocessing/RenderPass.js";
import { UnrealBloomPass } from "three/examples/jsm/postprocessing/UnrealBloomPass.js";
import { OutputPass } from "three/examples/jsm/postprocessing/OutputPass.js";
import { palette, type Palette } from "./hivePalette";
import { doing, ease, hash01, HiveFx, QUEEN, runningOn, SELF, short } from "./hiveFx";
import type { StageHandle, StagePulse, StageProps, StageWorker } from "./hiveFx";
import type { Look } from "./HiveStage";

// The hive in three dimensions, in the same two looks as the flat stage.
//
// Orbit: a star with planets on rings tilted a little against each other, in a
// field of stars. Mycelium: a root in the middle and workers hanging round it in
// space, joined by filaments that branch through a few junctions. Tree: a trunk
// rising from the root, and the workers as blossoms on its canopy. Nothing is a
// solid box: bodies are spheres that glow, paths are light, and the bloom does
// the rest.
//
// Like the flat one it keeps nothing in React state; the frame loop reads refs
// and HiveFx decides what is travelling where.

interface Props extends StageProps {
  look: Look;
}

type V3 = THREE.Vector3;

function glowTexture(): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0, "rgba(255,255,255,1)");
  grad.addColorStop(0.22, "rgba(255,255,255,0.6)");
  grad.addColorStop(1, "rgba(255,255,255,0)");
  g.fillStyle = grad;
  g.fillRect(0, 0, 64, 64);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

function labelSprite(name: string, sub: string, text: string, subColor: string) {
  const c = document.createElement("canvas");
  const g = c.getContext("2d")!;
  const f1 = "600 30px ui-sans-serif, system-ui, sans-serif";
  const f2 = "22px ui-monospace, SFMono-Regular, monospace";
  g.font = f1;
  const w1 = g.measureText(name).width;
  g.font = f2;
  const w2 = sub ? g.measureText(sub).width : 0;
  c.width = Math.ceil(Math.max(w1, w2)) + 20;
  c.height = sub ? 76 : 44;
  const g2 = c.getContext("2d")!;
  g2.textAlign = "center";
  g2.font = f1;
  g2.fillStyle = text;
  g2.fillText(name, c.width / 2, 30);
  if (sub) {
    g2.font = f2;
    g2.fillStyle = subColor;
    g2.fillText(sub, c.width / 2, 64);
  }
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  const sp = new THREE.Sprite(new THREE.SpriteMaterial({ map: tex, transparent: true, depthWrite: false, depthTest: false }));
  sp.userData.aspect = c.width / c.height;
  sp.userData.lines = sub ? 2 : 1;
  return sp;
}

function disposeSprite(sp: THREE.Sprite) {
  (sp.material as THREE.SpriteMaterial).map?.dispose();
  sp.material.dispose();
}

/** A point `u` of the way along a list of evenly sampled points. */
function alongPts(pts: V3[], u: number, out: V3): V3 {
  const f = Math.max(0, Math.min(1, u)) * (pts.length - 1);
  const i = Math.min(pts.length - 2, Math.floor(f));
  return out.lerpVectors(pts[i], pts[i + 1], f - i);
}

function arcPts(a: V3, b: V3, lift: number, n = 32): V3[] {
  const mid = new THREE.Vector3().addVectors(a, b).multiplyScalar(0.5);
  mid.y += lift;
  return new THREE.QuadraticBezierCurve3(a.clone(), mid, b.clone()).getPoints(n);
}

interface Body {
  mesh: THREE.Mesh;
  halo: THREE.Sprite;
  label: THREE.Sprite;
  labelKey: string;
  radius: number;
}

const HiveStage3D = forwardRef<StageHandle, Props>(function HiveStage3D({ role, self, workers, tasks, ready, look }, ref) {
  const box = useRef<HTMLDivElement>(null);
  const live = useRef({ role, self, workers, tasks, look });
  live.current = { role, self, workers, tasks, look };
  const fx = useRef(new HiveFx());
  const born = useRef(new Map<string, number>());

  useImperativeHandle(ref, () => ({ pulse: (p: StagePulse) => fx.current.pulse(p, live.current.role, palette()) }), []);
  useEffect(() => {
    if (ready) fx.current.tasks(tasks, live.current.role, palette());
  }, [tasks, ready]);

  useEffect(() => {
    const host = box.current;
    if (!host) return;
    let pal: Palette = palette();
    const light = pal.light;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: "high-performance" });
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    // Opaque on a dark ground. Bloom on a transparent canvas leaves a visible
    // edge where the glow's alpha stops, a dark blotch round the scene; drawing
    // the stage's own colour avoids it. A light ground has no bloom, so it can
    // stay transparent and show the page through.
    const stageBg = getComputedStyle(document.documentElement).getPropertyValue("--stage-bg").trim() || "#000000";
    renderer.setClearColor(new THREE.Color(light ? "#ffffff" : stageBg), light ? 0 : 1);
    renderer.domElement.style.display = "block";
    host.appendChild(renderer.domElement);

    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(42, 1, 0.1, 400);
    camera.position.set(0, 6.5, 13);
    const controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.06;
    controls.minDistance = 5;
    controls.maxDistance = 34;
    controls.enablePan = false;
    controls.autoRotate = !still;
    controls.autoRotateSpeed = 0.45;
    let resumeAt = 0;
    controls.addEventListener("start", () => {
      controls.autoRotate = false;
    });
    controls.addEventListener("end", () => {
      resumeAt = performance.now() + 6000;
    });

    scene.add(new THREE.AmbientLight(0xffffff, light ? 1.4 : 0.35));
    const sun = new THREE.PointLight(0xffffff, light ? 50 : 140, 60, 1.6);
    scene.add(sun);

    let composer: EffectComposer | null = null;
    let bloom: UnrealBloomPass | null = null;
    if (!light) {
      composer = new EffectComposer(renderer);
      composer.addPass(new RenderPass(scene, camera));
      bloom = new UnrealBloomPass(new THREE.Vector2(256, 256), 0.55, 0.35, 0.3);
      composer.addPass(bloom);
      composer.addPass(new OutputPass());
    }

    const glow = glowTexture();
    const sphereGeo = new THREE.SphereGeometry(1, 32, 24);

    // Stars far away, for the orbits.
    const starGeo = new THREE.BufferGeometry();
    const sp: number[] = [];
    for (let i = 0; i < 900; i++) {
      const u = hash01("u" + i, 1) * 2 - 1;
      const th = hash01("t" + i, 2) * Math.PI * 2;
      const r = 90 + hash01("r" + i, 3) * 60;
      const s = Math.sqrt(1 - u * u);
      sp.push(Math.cos(th) * s * r, u * r, Math.sin(th) * s * r);
    }
    starGeo.setAttribute("position", new THREE.Float32BufferAttribute(sp, 3));
    const starField = new THREE.Points(starGeo, new THREE.PointsMaterial({ color: 0xffffff, size: 0.55, transparent: true, opacity: 0.6 }));
    scene.add(starField);

    // Light that moves: flights, beads, sparks. A pool of sprites handed out
    // afresh each frame and hidden when unused.
    const pool: THREE.Sprite[] = [];
    let used = 0;
    const spark = (p: V3, size: number, color: string, alpha: number) => {
      let s = pool[used];
      if (!s) {
        s = new THREE.Sprite(new THREE.SpriteMaterial({ map: glow, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending }));
        pool.push(s);
        scene.add(s);
      }
      used++;
      const m = s.material as THREE.SpriteMaterial;
      m.color.set(color);
      m.opacity = alpha;
      m.blending = light ? THREE.NormalBlending : THREE.AdditiveBlending;
      s.position.copy(p);
      s.scale.set(size, size, 1);
      s.visible = true;
    };

    // Everything that depends on who is in the hive and in which look, rebuilt
    // when either changes.
    const world = new THREE.Group();
    scene.add(world);
    let bodies = new Map<string, Body>();
    let lines: THREE.Line[] = [];
    let sig = "";

    const makeBody = (radius: number): Body => {
      const mesh = new THREE.Mesh(
        sphereGeo,
        new THREE.MeshStandardMaterial({ color: light ? 0xffffff : 0x0b1216, emissive: new THREE.Color(pal.green), emissiveIntensity: 0.6, roughness: 0.55, metalness: 0.05 }),
      );
      mesh.scale.setScalar(radius);
      const halo = new THREE.Sprite(new THREE.SpriteMaterial({ map: glow, transparent: true, depthWrite: false, blending: light ? THREE.NormalBlending : THREE.AdditiveBlending }));
      const label = labelSprite(" ", "", pal.text, pal.dim);
      world.add(mesh, halo, label);
      return { mesh, halo, label, labelKey: "", radius };
    };

    const clear = () => {
      for (const b of bodies.values()) {
        world.remove(b.mesh, b.halo, b.label);
        (b.mesh.material as THREE.Material).dispose();
        b.halo.material.dispose();
        disposeSprite(b.label);
      }
      bodies = new Map();
      for (const l of lines) {
        world.remove(l);
        l.geometry.dispose();
        (l.material as THREE.Material).dispose();
      }
      lines = [];
    };

    // Orbit: which ring each worker is on.
    let orbitRings: string[][] = [];
    const ringRadius = (k: number) => 4.4 + k * 2.4;
    const ringTilt = (k: number) => 0.12 * Math.sin(k * 2.1 + 0.6);
    // Mycelium: where each worker hangs, and the filament out to it.
    const spots = new Map<string, V3>();
    const paths = new Map<string, V3[]>();
    const lineOf = new Map<string, THREE.Line>();

    const rebuild = () => {
      clear();
      spots.clear();
      paths.clear();
      lineOf.clear();
      const { role: rl, workers: ws, look: lk } = live.current;
      const names = rl === "queen" ? ws.map((w) => w.name) : rl === "worker" ? [SELF] : [];
      bodies.set(QUEEN, makeBody(lk === "orbit" ? 1.0 : lk === "tree" ? 0.6 : 0.7));
      for (const n of names) bodies.set(n, makeBody(lk === "orbit" ? 0.34 : 0.26));
      starField.visible = lk === "orbit" && !light;

      if (lk === "orbit") {
        orbitRings = [];
        for (const n of [...names].sort()) {
          let k = 0;
          while ((orbitRings[k] ??= []).length >= 4 + k * 2) k++;
          orbitRings[k].push(n);
        }
        orbitRings.forEach((_, k) => {
          const pts: V3[] = [];
          for (let i = 0; i <= 128; i++) {
            const a = (i / 128) * Math.PI * 2;
            pts.push(new THREE.Vector3(Math.cos(a) * ringRadius(k), 0, Math.sin(a) * ringRadius(k)));
          }
          const l = new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), new THREE.LineBasicMaterial({ color: new THREE.Color(pal.dim), transparent: true, opacity: 0.25 }));
          l.rotation.x = ringTilt(k);
          l.userData.ring = k;
          world.add(l);
          lines.push(l);
        });
        const R = ringRadius(Math.max(0, orbitRings.length - 1));
        camera.position.set(0, R * 0.85 + 3, R * 1.55 + 5);
      } else if (lk === "tree") {
        // A trunk rising from the root at the bottom, and the workers as
        // blossoms spread over a dome of canopy above it.
        const order = [...names].sort();
        const n = order.length;
        const R = Math.min(6.5, 3.4 + n * 0.25);
        const base = new THREE.Vector3(0, -3, 0);
        const fork = new THREE.Vector3(0, 0.2, 0);
        order.forEach((name, i) => {
          // The upper half of a sphere, by the golden angle.
          const y = 0.15 + 0.8 * (1 - (i + 0.5) / Math.max(1, n));
          const r = Math.sqrt(Math.max(0, 1 - y * y));
          const th = i * 2.399963;
          spots.set(name, new THREE.Vector3(Math.cos(th) * r * R, fork.y + y * R * 0.75, Math.sin(th) * r * R));
        });
        const trunkPts = new THREE.CatmullRomCurve3([base.clone(), new THREE.Vector3(0.15, -1.4, 0.1), fork.clone()]).getPoints(24);
        const trunk = new THREE.Line(new THREE.BufferGeometry().setFromPoints(trunkPts), new THREE.LineBasicMaterial({ color: new THREE.Color(pal.green), transparent: true, opacity: 0.6 }));
        trunk.userData.trunk = true;
        world.add(trunk);
        lines.push(trunk);
        const az = (m: string) => Math.atan2(spots.get(m)!.z, spots.get(m)!.x);
        const byAzimuth = [...order].sort((a, b) => az(a) - az(b));
        for (let i = 0; i < byAzimuth.length; i += 3) {
          const tr = byAzimuth.slice(i, i + 3);
          const mean = new THREE.Vector3();
          tr.forEach((m) => mean.add(spots.get(m)!));
          mean.divideScalar(tr.length);
          const junction = fork.clone().lerp(mean, 0.5);
          for (const m of tr) {
            const end = spots.get(m)!;
            const branch = new THREE.CatmullRomCurve3([
              fork.clone(),
              fork.clone().lerp(junction, 0.5).add(new THREE.Vector3(0, 0.25, 0)),
              junction.clone(),
              junction.clone().lerp(end, 0.5).add(new THREE.Vector3(0, 0.3 + hash01(m, 7) * 0.3, 0)),
              end.clone(),
            ]).getPoints(40);
            paths.set(m, [...trunkPts, ...branch.slice(1)]);
            const l = new THREE.Line(new THREE.BufferGeometry().setFromPoints(branch), new THREE.LineBasicMaterial({ color: new THREE.Color(pal.green), transparent: true, opacity: 0.4 }));
            world.add(l);
            lines.push(l);
            lineOf.set(m, l);
          }
        }
        camera.position.set(0, R * 0.9 + 1, R * 2.1 + 4);
      } else {
        // Workers on a shell round the root, spread by the golden angle and
        // squashed toward the horizontal so the picture is wide, not tall.
        const order = [...names].sort();
        const n = order.length;
        const R = Math.min(7, 3.6 + n * 0.28);
        order.forEach((name, i) => {
          const y = n === 1 ? 0 : 1 - ((i + 0.5) / n) * 2;
          const r = Math.sqrt(Math.max(0, 1 - y * y));
          const th = i * 2.399963;
          const d = R * (0.85 + hash01(name, 2) * 0.25);
          spots.set(name, new THREE.Vector3(Math.cos(th) * r * d, y * d * 0.45, Math.sin(th) * r * d));
        });
        // Trunks: grouped by direction, three to a trunk, each forking from a
        // junction partway out.
        const az = (m: string) => Math.atan2(spots.get(m)!.z, spots.get(m)!.x);
        const byAzimuth = [...order].sort((a, b) => az(a) - az(b));
        for (let i = 0; i < byAzimuth.length; i += 3) {
          const tr = byAzimuth.slice(i, i + 3);
          const mean = new THREE.Vector3();
          tr.forEach((m) => mean.add(spots.get(m)!));
          mean.divideScalar(tr.length).multiplyScalar(0.45);
          for (const m of tr) {
            const end = spots.get(m)!;
            const jitter = (seed: number, amp: number) =>
              new THREE.Vector3((hash01(m, seed) - 0.5) * amp, (hash01(m, seed + 1) - 0.5) * amp, (hash01(m, seed + 2) - 0.5) * amp);
            const pts = new THREE.CatmullRomCurve3([
              new THREE.Vector3(0, 0, 0),
              mean.clone().multiplyScalar(0.5).add(jitter(10, 0.5)),
              mean.clone(),
              mean.clone().lerp(end, 0.5).add(jitter(20, 0.8)),
              end.clone(),
            ]).getPoints(64);
            paths.set(m, pts);
            const l = new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), new THREE.LineBasicMaterial({ color: new THREE.Color(pal.green), transparent: true, opacity: 0.35 }));
            world.add(l);
            lines.push(l);
            lineOf.set(m, l);
          }
        }
        camera.position.set(0, R * 1.1 + 2, R * 2.0 + 4);
      }
      controls.target.set(0, 0, 0);
    };

    const resize = () => {
      const r = host.getBoundingClientRect();
      const w = Math.max(200, Math.floor(r.width));
      const h = Math.max(160, Math.floor(r.height));
      renderer.setSize(w, h, false);
      renderer.domElement.style.width = `${w}px`;
      renderer.domElement.style.height = `${h}px`;
      composer?.setSize(w, h);
      bloom?.setSize(w, h);
      camera.aspect = w / h;
      camera.updateProjectionMatrix();
    };
    const ro = new ResizeObserver(resize);
    ro.observe(host);
    resize();

    const pos = new Map<string, V3>();
    const tmp = new THREE.Vector3();
    const xAxis = new THREE.Vector3(1, 0, 0);
    let raf = 0;
    let palAt = 0;
    let first = true;

    const frame = (now: number) => {
      const t = still ? 0 : now / 1000;
      if (now - palAt > 2000) {
        pal = palette();
        palAt = now;
      }
      const { role: rl, workers: ws, self: me, tasks: ts, look: lk } = live.current;
      for (const w of ws) if (!born.current.has(w.name)) born.current.set(w.name, first ? now - 60000 : now);
      first = false;
      const growth = (n: string) => Math.min(1, (now - (born.current.get(n) ?? 0)) / 1400);
      const s2 = `${lk}|${rl}|${ws.map((w) => w.name).join(",")}`;
      if (s2 !== sig) {
        sig = s2;
        rebuild();
      }
      if (!controls.autoRotate && !still && resumeAt && now > resumeAt) {
        controls.autoRotate = true;
        resumeAt = 0;
      }
      controls.update();
      fx.current.prune(now);
      used = 0;

      const busy = (id: string) => runningOn(id, rl, ts);
      const info = (id: string): StageWorker | undefined => ws.find((w) => w.name === id);
      const working = ts.some((k) => k.state === "running");
      const names = rl === "queen" ? ws.map((w) => w.name) : rl === "worker" ? [SELF] : [];

      // Where everyone is this frame.
      pos.clear();
      pos.set(QUEEN, lk === "tree" ? new THREE.Vector3(0, -3, 0) : new THREE.Vector3(0, 0, 0));
      if (lk === "orbit") {
        orbitRings.forEach((ring, k) => {
          const R = ringRadius(k);
          const speed = 0.16 / Math.pow(1 + k * 0.8, 1.5);
          ring.forEach((n, i) => {
            const a = (i / ring.length) * Math.PI * 2 + k * 0.7 + t * speed;
            const out = 1 + (1 - ease(growth(n))) * 0.9;
            pos.set(n, new THREE.Vector3(Math.cos(a) * R * out, 0, Math.sin(a) * R * out).applyAxisAngle(xAxis, ringTilt(k)));
          });
        });
        for (const l of lines) {
          const ring = orbitRings[l.userData.ring as number] ?? [];
          const active = ring.some((n) => busy(n));
          const m = l.material as THREE.LineBasicMaterial;
          m.color.set(active ? pal.accent : pal.dim);
          m.opacity = active ? 0.6 : 0.22;
        }
      } else {
        for (const n of names) {
          const s = spots.get(n);
          if (s) pos.set(n, s.clone().add(new THREE.Vector3(0, Math.sin(t * 0.8 + hash01(n, 9) * 6) * 0.12, 0)));
        }
        for (const l of lines) {
          if (!l.userData.trunk) continue;
          const m = l.material as THREE.LineBasicMaterial;
          m.color.set(working ? pal.accent : pal.green);
          m.opacity = working ? 0.95 : 0.6;
        }
        for (const [n, l] of lineOf) {
          const active = !!busy(n);
          const lost = info(n)?.state === "lost";
          const m = l.material as THREE.LineBasicMaterial;
          m.color.set(active ? pal.accent : lost ? pal.dim : pal.green);
          m.opacity = active ? 0.95 : lost ? 0.12 : 0.35;
          // Filaments grow out when a worker joins.
          l.geometry.setDrawRange(0, Math.floor(l.geometry.attributes.position.count * ease(Math.min(1, growth(n) * 1.3))));
          if (active) {
            const pts = paths.get(n)!;
            for (let k = 0; k < 4; k++) spark(alongPts(pts, (t * 0.35 + k / 4) % 1, tmp), 0.35, pal.accent, 0.95);
          }
        }
        for (const k of ts) {
          if (k.state !== "running" || k.dir !== "peer" || !k.from) continue;
          const a = pos.get(k.from);
          const b = pos.get(k.worker);
          if (!a || !b) continue;
          const pts = arcPts(a, b, 1.2, 24);
          for (let i = 0; i < 24; i += 2) spark(pts[i], 0.16, pal.amber, 0.7);
        }
      }

      // Bodies.
      for (const [id, b] of bodies) {
        const p = pos.get(id);
        if (!p) continue;
        const isQueen = id === QUEEN;
        const lost = !isQueen && info(id)?.state === "lost";
        const task = isQueen ? undefined : busy(id);
        const g = isQueen ? 1 : ease(growth(id));
        const shown = lk === "orbit" || isQueen || growth(id) > 0.5;
        b.mesh.visible = b.halo.visible = b.label.visible = shown;
        if (!shown) continue;
        const tone = isQueen ? pal.accent : lost ? pal.dim : task ? pal.accent : pal.green;
        const beat = isQueen ? (working ? 1 + 0.08 * Math.sin(t * 5) : 1 + 0.03 * Math.sin(t * 1.4)) : task ? 1.15 + 0.08 * Math.sin(t * 5) : 1;
        b.mesh.position.copy(p);
        b.mesh.scale.setScalar(b.radius * beat * Math.max(0.05, g));
        const mm = b.mesh.material as THREE.MeshStandardMaterial;
        mm.emissive.set(tone);
        mm.emissiveIntensity = isQueen ? (lk === "orbit" ? 1.4 : 1.1) : lost ? 0.08 : task ? 1.3 : 0.5;
        if (isQueen) sun.position.copy(p);
        const hm = b.halo.material as THREE.SpriteMaterial;
        hm.color.set(tone);
        hm.opacity = lost ? 0 : isQueen ? (light ? 0.45 : 0.6) : task ? (light ? 0.45 : 0.6) : light ? 0.15 : 0.22;
        const hs = b.radius * (isQueen ? (lk === "orbit" ? 3.8 : 3.2) : task ? 4.5 : 2.8) * beat * g;
        b.halo.position.copy(p);
        b.halo.scale.set(hs, hs, 1);

        // Moons for a planet at work: one per tool called so far, up to five.
        if (lk === "orbit" && task) {
          for (let m = 0; m < Math.min(5, task.tools); m++) {
            const a = t * (1.6 + m * 0.3) + m * 1.3;
            spark(tmp.set(p.x + Math.cos(a) * 0.8, p.y + Math.sin(a * 0.7) * 0.2, p.z + Math.sin(a) * 0.8), 0.22, pal.amber, 0.95);
          }
        }

        const name = isQueen ? (rl === "worker" ? "queen" : me || "queen") : id === SELF ? me || "worker" : short(id);
        const w = info(id);
        const sub = isQueen ? "" : lost ? "lost" : task ? doing(task) : w?.engine ? w.engine.replace(/^cli · /, "") : "";
        const key = `${name}|${sub}|${task ? 1 : 0}|${pal.text}`;
        if (b.labelKey !== key) {
          b.labelKey = key;
          world.remove(b.label);
          disposeSprite(b.label);
          b.label = labelSprite(name, sub, lost ? pal.dim : pal.text, task ? pal.accent : pal.dim);
          world.add(b.label);
        }
        const dist = camera.position.distanceTo(p);
        const lh = Math.max(0.4, dist * 0.045) * ((b.label.userData.lines as number) === 2 ? 1 : 0.6);
        b.label.scale.set(lh * (b.label.userData.aspect as number), lh, 1);
        b.label.position.set(p.x, p.y - b.radius * beat - lh * 0.6 - 0.1, p.z);
      }

      // Traffic: comets in the orbits, beads down the filaments.
      const route = (from: string, to: string): V3[] | null => {
        if (lk !== "orbit") {
          if (from === QUEEN && paths.has(to)) return paths.get(to)!;
          if (to === QUEEN && paths.has(from)) return [...paths.get(from)!].reverse();
        }
        const a = pos.get(from);
        const b = pos.get(to);
        return a && b ? arcPts(a, b, lk === "orbit" ? 0.9 + a.distanceTo(b) * 0.12 : 1.2) : null;
      };
      const tail = lk === "orbit" ? 12 : 5;
      const gap = lk === "orbit" ? 0.016 : 0.012;
      for (const f of fx.current.flights) {
        if (now < f.t0) continue;
        const pts = route(f.from, f.to);
        if (!pts) continue;
        const u = ease((now - f.t0) / f.dur);
        for (let k = tail - 1; k >= 0; k--) {
          alongPts(pts, Math.max(0, u - k * gap), tmp);
          spark(tmp, (lk === "orbit" ? 0.6 : 0.5) * f.size * (1 - k / tail) + 0.05, f.color, f.alpha * (1 - k / tail));
        }
      }
      for (const l of fx.current.landings) {
        const p = pos.get(l.at);
        if (!p || now < l.t0) continue;
        const u = (now - l.t0) / (l.big ? 1100 : 600);
        spark(p, (l.big ? 2.2 : 1.2) + u * (l.big ? 4 : 1.6), l.color, (1 - u) * (l.big ? 0.8 : 0.45));
      }
      for (const s of fx.current.sparks) {
        const p = pos.get(s.at);
        if (!p || now < s.t0) continue;
        const u = (now - s.t0) / 700;
        for (let k = 0; k < 7; k++) {
          const a = s.a + (k * Math.PI * 2) / 7;
          spark(tmp.set(p.x + Math.cos(a) * (0.4 + u * 1.2), p.y + u * 0.6, p.z + Math.sin(a) * (0.4 + u * 1.2)), 0.18 * (1 - u) + 0.04, pal.amber, 1 - u);
        }
      }

      for (let i = used; i < pool.length; i++) pool[i].visible = false;
      if (composer) composer.render();
      else renderer.render(scene, camera);
    };

    const loop = (now: number) => {
      frame(now);
      raf = requestAnimationFrame(loop);
    };
    const visible = () => {
      cancelAnimationFrame(raf);
      if (!document.hidden) raf = requestAnimationFrame(loop);
    };
    document.addEventListener("visibilitychange", visible);
    raf = requestAnimationFrame(loop);

    return () => {
      cancelAnimationFrame(raf);
      document.removeEventListener("visibilitychange", visible);
      ro.disconnect();
      controls.dispose();
      clear();
      for (const s of pool) s.material.dispose();
      starGeo.dispose();
      (starField.material as THREE.Material).dispose();
      glow.dispose();
      sphereGeo.dispose();
      composer?.dispose();
      renderer.dispose();
      renderer.domElement.remove();
    };
  }, []);

  return <div className="relative h-full min-h-[180px] w-full overflow-hidden" ref={box} />;
});

export default HiveStage3D;
