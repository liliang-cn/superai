import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { EffectComposer } from "three/examples/jsm/postprocessing/EffectComposer.js";
import { RenderPass } from "three/examples/jsm/postprocessing/RenderPass.js";
import { UnrealBloomPass } from "three/examples/jsm/postprocessing/UnrealBloomPass.js";
import { OutputPass } from "three/examples/jsm/postprocessing/OutputPass.js";
import { palette } from "./hivePalette";
import type { StageHandle, StagePulse, StageTask, StageWorker } from "./hiveFx";
import { mailTag } from "./hiveFx";

// The hive in three dimensions.
//
// The same picture as the flat stage, told with the same inputs — who is in the
// hive, which orders are running, and the flicker of what they are doing — but
// with somewhere to put things. The workers hang on a shell around the queen,
// orders and answers fly between them on arcs, and the camera drifts around the
// whole thing until someone takes hold of it.
//
// Like the flat one it keeps nothing in React state. The frame loop reads refs,
// and an event costs one push onto an array.

interface Props {
  role: "" | "queen" | "worker";
  self: string;
  workers: StageWorker[];
  tasks: StageTask[];
  ready: boolean;
}

const QUEEN = "\u0000queen";
const SELF = "\u0000self";
const short = (n: string) => n.replace(/^superai-/, "");

type Flight = { from: string; to: string; t0: number; dur: number; color: string; size: number; alpha: number };
type Ring = { at: string; t0: number; color: string; big: boolean };
type Tag = { at: string; text: string; t0: number; color: string; sprite?: THREE.Sprite };
type Spark = { at: string; t0: number; a: number };

interface NodeObj {
  id: string;
  kind: "queen" | "worker";
  group: THREE.Group;
  body: THREE.Mesh;
  edges: THREE.LineSegments;
  halo: THREE.Mesh;
  core: THREE.Mesh;
  label: THREE.Sprite;
  labelKey: string;
  link?: THREE.Line;
  home: THREE.Vector3;
  radius: number;
  phase: number;
}

function glowTexture(): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0, "rgba(255,255,255,1)");
  grad.addColorStop(0.25, "rgba(255,255,255,0.55)");
  grad.addColorStop(1, "rgba(255,255,255,0)");
  g.fillStyle = grad;
  g.fillRect(0, 0, 64, 64);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

function ringTexture(): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = c.height = 128;
  const g = c.getContext("2d")!;
  g.strokeStyle = "#fff";
  g.lineWidth = 5;
  g.beginPath();
  for (let i = 0; i < 6; i++) {
    const a = (Math.PI / 3) * i - Math.PI / 6;
    const x = 64 + 54 * Math.cos(a);
    const y = 64 + 54 * Math.sin(a);
    if (i === 0) g.moveTo(x, y);
    else g.lineTo(x, y);
  }
  g.closePath();
  g.stroke();
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

/** The honeycomb the whole thing sits on, faded out toward its edge. */
function floorTexture(line: string): THREE.Texture {
  const S = 1024;
  const c = document.createElement("canvas");
  c.width = c.height = S;
  const g = c.getContext("2d")!;
  g.strokeStyle = line;
  g.lineWidth = 2;
  const r = 34;
  const dx = r * Math.sqrt(3);
  for (let row = -1, y = 0; y < S + r * 2; row++, y += r * 1.5) {
    for (let x = (row % 2 ? dx / 2 : 0) - dx; x < S + dx; x += dx) {
      g.beginPath();
      for (let i = 0; i < 6; i++) {
        const a = (Math.PI / 3) * i - Math.PI / 6;
        const px = x + (r - 1) * Math.cos(a);
        const py = y + (r - 1) * Math.sin(a);
        if (i === 0) g.moveTo(px, py);
        else g.lineTo(px, py);
      }
      g.closePath();
      g.stroke();
    }
  }
  // Fade to nothing at the rim so the floor has no edge to see.
  g.globalCompositeOperation = "destination-in";
  const fade = g.createRadialGradient(S / 2, S / 2, S * 0.08, S / 2, S / 2, S / 2);
  fade.addColorStop(0, "rgba(0,0,0,1)");
  fade.addColorStop(1, "rgba(0,0,0,0)");
  g.fillStyle = fade;
  g.fillRect(0, 0, S, S);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 4;
  return t;
}

function textSprite(text: string, color: string, size: number, bold = true) {
  const c = document.createElement("canvas");
  const g = c.getContext("2d")!;
  const font = `${bold ? "600 " : ""}${size * 2}px ui-monospace, SFMono-Regular, monospace`;
  g.font = font;
  const w = Math.ceil(g.measureText(text).width) + 16;
  c.width = w;
  c.height = size * 3;
  const g2 = c.getContext("2d")!;
  g2.font = font;
  g2.textAlign = "center";
  g2.textBaseline = "middle";
  g2.fillStyle = color;
  g2.fillText(text, w / 2, c.height / 2);
  const tex = new THREE.CanvasTexture(c);
  tex.colorSpace = THREE.SRGBColorSpace;
  const mat = new THREE.SpriteMaterial({ map: tex, transparent: true, depthWrite: false, depthTest: false });
  const sp = new THREE.Sprite(mat);
  sp.userData.aspect = w / c.height;
  return sp;
}

function disposeSprite(sp: THREE.Sprite) {
  (sp.material as THREE.SpriteMaterial).map?.dispose();
  sp.material.dispose();
}

/** Where worker i of n hangs: a shell around the queen, spread by the golden
 *  angle so any number of them sits evenly and none directly behind another. */
function shell(i: number, n: number): THREE.Vector3 {
  const R = Math.min(6.4, 3.4 + 0.2 * n);
  const y = n === 1 ? 0 : 1 - ((i + 0.5) / n) * 2;
  const r = Math.sqrt(Math.max(0, 1 - y * y));
  const th = i * 2.399963;
  return new THREE.Vector3(Math.cos(th) * r * R, y * R * 0.42, Math.sin(th) * r * R);
}

const HiveHex3D = forwardRef<StageHandle, Props>(function HiveHex3D({ role, self, workers, tasks, ready }, ref) {
  const box = useRef<HTMLDivElement>(null);
  const live = useRef({ role, self, workers, tasks });
  live.current = { role, self, workers, tasks };
  const flights = useRef<Flight[]>([]);
  const rings = useRef<Ring[]>([]);
  const tags = useRef<Tag[]>([]);
  const sparks = useRef<Spark[]>([]);
  const seen = useRef<Map<string, { state: string; tools: number }> | null>(null);

  useImperativeHandle(
    ref,
    () => ({
      pulse: (p: StagePulse) => {
        const now = performance.now();
        const rl = live.current.role;
        const pal = palette();
        if (p.kind === "message") {
          if (!p.src || !p.dst) return;
          flights.current.push({ from: p.src, to: p.dst, t0: now, dur: 1000, color: pal.mail, size: 0.3, alpha: 1 });
          tags.current.push({ at: p.src, text: mailTag(p.text), t0: now, color: pal.mail });
          rings.current.push({ at: p.dst, t0: now + 1000, color: pal.mail, big: true });
          return;
        }
        let from: string;
        let to: string;
        if (rl === "worker") {
          if (p.dir !== "in") return;
          from = SELF;
          to = QUEEN;
        } else if (rl === "queen") {
          if (p.dir === "peer" && p.from) {
            from = p.worker;
            to = p.from;
          } else {
            from = p.worker;
            to = QUEEN;
          }
        } else return;
        const grow = Math.min(3, Math.log10(1 + (p.bytes ?? 0)));
        let f: Flight;
        switch (p.kind) {
          case "tool":
            f = { from, to, t0: now, dur: 520, color: pal.amber, size: 0.34, alpha: 1 };
            tags.current.push({ at: from, text: `⚙ ${p.tool || "tool"}`, t0: now, color: pal.amber });
            break;
          case "result":
            f = { from, to, t0: now, dur: 480, color: pal.green, size: 0.24 + grow * 0.05, alpha: 1 };
            break;
          case "text":
            f = { from, to, t0: now, dur: 380, color: pal.accent, size: 0.15 + grow * 0.03, alpha: 0.85 };
            break;
          default:
            f = { from, to, t0: now, dur: 700, color: pal.accent, size: 0.14, alpha: 0.4 };
        }
        flights.current.push(f);
        if (flights.current.length > 120) flights.current.splice(0, flights.current.length - 120);
        if (p.kind !== "thinking") rings.current.push({ at: to, t0: now + f.dur, color: f.color, big: false });
      },
    }),
    [],
  );

  // Orders and answers, from the changes in the task list.
  useEffect(() => {
    if (!ready) return;
    const now = performance.now();
    const known = seen.current;
    if (known === null) {
      seen.current = new Map(tasks.map((t) => [t.id, { state: t.state, tools: t.tools }]));
      return;
    }
    const pal = palette();
    const target = (t: StageTask) => (live.current.role === "worker" ? SELF : t.worker);
    const origin = (t: StageTask) => (t.dir === "peer" && t.from ? t.from : QUEEN);
    for (const t of tasks) {
      if (live.current.role === "worker" && t.dir === "peer") {
        known.set(t.id, { state: t.state, tools: t.tools });
        continue;
      }
      const before = known.get(t.id);
      if (!before) {
        flights.current.push({ from: origin(t), to: target(t), t0: now, dur: 800, color: pal.accent, size: 0.42, alpha: 1 });
      } else {
        for (let i = before.tools; i < t.tools; i++) sparks.current.push({ at: target(t), t0: now + i * 90, a: Math.random() * 6.28 });
        if (before.state === "running" && t.state !== "running") {
          const color = t.state === "done" ? pal.green : t.state === "failed" ? pal.red : pal.amber;
          flights.current.push({ from: target(t), to: origin(t), t0: now, dur: 800, color, size: 0.42, alpha: 1 });
          rings.current.push({ at: target(t), t0: now + 700, color, big: true });
        }
      }
      known.set(t.id, { state: t.state, tools: t.tools });
    }
  }, [tasks, ready]);

  useEffect(() => {
    const host = box.current;
    if (!host) return;
    let pal = palette();
    const light = pal.light;

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: "high-performance" });
    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    renderer.setClearColor(0x000000, 0);
    renderer.domElement.style.display = "block";
    host.appendChild(renderer.domElement);

    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(42, 1, 0.1, 200);
    camera.position.set(0, 3.7, 8.6);
    const controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.06;
    controls.minDistance = 4;
    controls.maxDistance = 22;
    controls.maxPolarAngle = Math.PI * 0.62;
    controls.enablePan = false;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    controls.autoRotate = !still;
    controls.autoRotateSpeed = 0.7;
    let resumeAt = 0;
    controls.addEventListener("start", () => {
      controls.autoRotate = false;
    });
    controls.addEventListener("end", () => {
      resumeAt = performance.now() + 6000;
    });

    scene.add(new THREE.AmbientLight(0xffffff, light ? 1.6 : 0.8));
    const key = new THREE.PointLight(0xffffff, light ? 60 : 90, 40, 1.6);
    key.position.set(4, 7, 5);
    scene.add(key);

    // Post-processing only on a dark ground: bloom on paper is a smudge.
    let composer: EffectComposer | null = null;
    let bloom: UnrealBloomPass | null = null;
    if (!light) {
      composer = new EffectComposer(renderer);
      composer.addPass(new RenderPass(scene, camera));
      bloom = new UnrealBloomPass(new THREE.Vector2(256, 256), 0.62, 0.5, 0.32);
      composer.addPass(bloom);
      composer.addPass(new OutputPass());
    }

    const glow = glowTexture();
    const ringTex = ringTexture();
    const floorTex = floorTexture(light ? "rgba(28,25,23,0.2)" : "rgba(160,200,220,0.10)");
    const floor = new THREE.Mesh(
      new THREE.PlaneGeometry(26, 26),
      new THREE.MeshBasicMaterial({ map: floorTex, transparent: true, depthWrite: false }),
    );
    floor.rotation.x = -Math.PI / 2;
    floor.position.y = -2.7;
    scene.add(floor);

    const world = new THREE.Group();
    scene.add(world);

    // A pool of additive sprites for everything that is light: flights, trails,
    // sparks. Handed out fresh each frame and hidden when unused.
    const pool: THREE.Sprite[] = [];
    let used = 0;
    const lightSprite = (pos: THREE.Vector3, size: number, color: THREE.Color, alpha: number, tex = glow) => {
      let sp = pool[used];
      if (!sp) {
        sp = new THREE.Sprite(
          new THREE.SpriteMaterial({ map: tex, transparent: true, depthWrite: false, blending: THREE.AdditiveBlending }),
        );
        pool.push(sp);
        scene.add(sp);
      }
      used++;
      const m = sp.material as THREE.SpriteMaterial;
      if (m.map !== tex) {
        m.map = tex;
        m.needsUpdate = true;
      }
      m.color.copy(color);
      m.opacity = light ? Math.min(1, alpha * 0.9) : alpha;
      m.blending = light ? THREE.NormalBlending : THREE.AdditiveBlending;
      sp.position.copy(pos);
      sp.scale.set(size, size, 1);
      sp.visible = true;
      return sp;
    };

    let nodes = new Map<string, NodeObj>();
    let sig = "";
    const colorOf = (s: string) => new THREE.Color(s);

    const clearNodes = () => {
      for (const n of nodes.values()) {
        world.remove(n.group);
        n.group.traverse((o) => {
          const m = o as THREE.Mesh;
          m.geometry?.dispose?.();
          const mat = m.material as THREE.Material | THREE.Material[] | undefined;
          if (Array.isArray(mat)) mat.forEach((x) => x.dispose());
          else mat?.dispose?.();
        });
        disposeSprite(n.label);
        if (n.link) {
          world.remove(n.link);
          n.link.geometry.dispose();
          (n.link.material as THREE.Material).dispose();
        }
      }
      nodes = new Map();
    };

    const makeNode = (id: string, kind: "queen" | "worker", home: THREE.Vector3, radius: number, idx: number): NodeObj => {
      const group = new THREE.Group();
      group.position.copy(home);
      const geo = new THREE.CylinderGeometry(radius, radius, radius * 0.5, 6);
      const body = new THREE.Mesh(
        geo,
        new THREE.MeshStandardMaterial({
          color: light ? 0xf3efe6 : 0x0a1418,
          emissive: colorOf(pal.green),
          emissiveIntensity: 0.25,
          metalness: 0.35,
          roughness: 0.45,
        }),
      );
      body.rotation.y = Math.PI / 6;
      group.add(body);
      const edges = new THREE.LineSegments(
        new THREE.EdgesGeometry(geo),
        new THREE.LineBasicMaterial({ color: colorOf(pal.green), transparent: true, opacity: 0.95 }),
      );
      edges.rotation.y = Math.PI / 6;
      group.add(edges);
      const core = new THREE.Mesh(
        new THREE.SphereGeometry(radius * 0.22, 16, 16),
        new THREE.MeshBasicMaterial({ color: colorOf(pal.green) }),
      );
      core.position.y = radius * 0.05;
      group.add(core);
      const halo = new THREE.Mesh(
        new THREE.TorusGeometry(radius * 1.35, radius * 0.035, 8, 6),
        new THREE.MeshBasicMaterial({ color: colorOf(pal.accent), transparent: true, opacity: 0 }),
      );
      halo.rotation.x = Math.PI / 2;
      group.add(halo);
      const label = textSprite(" ", pal.text, 22);
      label.position.set(0, radius * 0.9 + 0.55, 0);
      group.add(label);
      world.add(group);
      return { id, kind, group, body, edges, halo, core, label, labelKey: "", home, radius, phase: idx * 1.7 };
    };

    const rebuild = () => {
      clearNodes();
      const { role: rl, workers: ws } = live.current;
      if (rl === "queen") {
        nodes.set(QUEEN, makeNode(QUEEN, "queen", new THREE.Vector3(0, 0, 0), 0.8, 0));
        ws.forEach((w, i) => {
          const n = makeNode(w.name, "worker", shell(i, ws.length), ws.length > 14 ? 0.38 : 0.5, i + 1);
          const q = nodes.get(QUEEN)!;
          const g = new THREE.BufferGeometry().setFromPoints([q.home, n.home]);
          n.link = new THREE.Line(g, new THREE.LineBasicMaterial({ color: colorOf(pal.dim), transparent: true, opacity: 0.22 }));
          world.add(n.link);
          nodes.set(w.name, n);
        });
      } else if (rl === "worker") {
        nodes.set(QUEEN, makeNode(QUEEN, "queen", new THREE.Vector3(0, 2.4, 0), 0.8, 0));
        const me = makeNode(SELF, "worker", new THREE.Vector3(0, -1.3, 0), 0.72, 1);
        const g = new THREE.BufferGeometry().setFromPoints([nodes.get(QUEEN)!.home, me.home]);
        me.link = new THREE.Line(g, new THREE.LineBasicMaterial({ color: colorOf(pal.dim), transparent: true, opacity: 0.3 }));
        world.add(me.link);
        nodes.set(SELF, me);
      } else {
        nodes.set(SELF, makeNode(SELF, "worker", new THREE.Vector3(0, 0, 0), 0.8, 0));
      }
    };

    const setLabel = (n: NodeObj, name: string, sub: string, color: string) => {
      const k = `${name}|${sub}|${color}`;
      if (n.labelKey === k) return;
      n.labelKey = k;
      const old = n.label;
      const c = document.createElement("canvas");
      const g = c.getContext("2d")!;
      const f1 = "600 30px ui-sans-serif, system-ui, sans-serif";
      const f2 = "22px ui-monospace, SFMono-Regular, monospace";
      g.font = f1;
      const w1 = g.measureText(name).width;
      g.font = f2;
      const w2 = g.measureText(sub).width;
      c.width = Math.ceil(Math.max(w1, w2)) + 20;
      c.height = 76;
      const g2 = c.getContext("2d")!;
      g2.textAlign = "center";
      g2.font = f1;
      g2.fillStyle = pal.text;
      g2.fillText(name, c.width / 2, 30);
      g2.font = f2;
      g2.fillStyle = color;
      g2.fillText(sub, c.width / 2, 62);
      const tex = new THREE.CanvasTexture(c);
      tex.colorSpace = THREE.SRGBColorSpace;
      const mat = new THREE.SpriteMaterial({ map: tex, transparent: true, depthWrite: false, depthTest: false });
      const sp = new THREE.Sprite(mat);
      sp.userData.aspect = c.width / c.height;
      sp.position.copy(old.position);
      n.group.remove(old);
      disposeSprite(old);
      n.group.add(sp);
      n.label = sp;
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

    const tmp = new THREE.Vector3();
    const posOf = (id: string): THREE.Vector3 | null => {
      const n = nodes.get(id);
      return n ? n.group.position : null;
    };
    const arc = (a: THREE.Vector3, b: THREE.Vector3, t: number, out: THREE.Vector3) => {
      const u = 1 - t;
      const cx = (a.x + b.x) / 2;
      const cy = (a.y + b.y) / 2 + 0.6 + a.distanceTo(b) * 0.14;
      const cz = (a.z + b.z) / 2;
      out.set(u * u * a.x + 2 * u * t * cx + t * t * b.x, u * u * a.y + 2 * u * t * cy + t * t * b.y, u * u * a.z + 2 * u * t * cz + t * t * b.z);
      return out;
    };
    const ease = (t: number) => (t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2);
    const runningOn = (id: string) => {
      const { role: rl, tasks: ts } = live.current;
      return ts.find(
        (k) =>
          k.state === "running" &&
          (id === SELF ? rl === "worker" && k.dir === "in" : k.worker === id && (k.dir === "out" || k.dir === "peer")),
      );
    };
    const tags3 = tags.current;
    void tags3;

    let raf = 0;
    let palAt = 0;
    const frame = (nowMs: number) => {
      const t = nowMs / 1000;
      if (nowMs - palAt > 2000) {
        pal = palette();
        palAt = nowMs;
      }
      const { role: rl, workers: ws, self: me } = live.current;
      const s2 = `${rl}|${ws.map((w) => w.name).join(",")}`;
      if (s2 !== sig) {
        sig = s2;
        rebuild();
      }
      if (!controls.autoRotate && !still && resumeAt && nowMs > resumeAt) {
        controls.autoRotate = true;
        resumeAt = 0;
      }
      controls.update();

      used = 0;
      const cam = camera.position;

      // Nodes.
      for (const n of nodes.values()) {
        const info = ws.find((w) => w.name === n.id);
        const lost = n.id !== QUEEN && n.id !== SELF && info?.state === "lost";
        const task = n.kind === "worker" ? runningOn(n.id) : undefined;
        const busyQueen = n.kind === "queen" && live.current.tasks.some((k) => k.state === "running");
        const tone = lost ? pal.dim : n.kind === "queen" ? pal.accent : task ? pal.accent : pal.green;
        const bob = still ? 0 : Math.sin(t * 0.9 + n.phase) * 0.1;
        n.group.position.y = n.home.y + bob;
        n.body.rotation.y = Math.PI / 6 + (task && !still ? t * 0.9 : 0);
        n.edges.rotation.y = n.body.rotation.y;

        const bm = n.body.material as THREE.MeshStandardMaterial;
        bm.emissive.set(tone);
        bm.emissiveIntensity = lost ? 0.03 : task ? 0.9 + 0.5 * Math.sin(t * 5) : busyQueen ? 0.55 + 0.25 * Math.sin(t * 3) : 0.28;
        (n.edges.material as THREE.LineBasicMaterial).color.set(tone);
        (n.edges.material as THREE.LineBasicMaterial).opacity = lost ? 0.35 : 0.95;
        (n.core.material as THREE.MeshBasicMaterial).color.set(tone);
        const ph = task?.phase;
        const pulse = ph === "thinking" ? 0.6 + 0.4 * Math.sin(t * 9) : ph === "tool" ? (Math.sin(t * 20) > 0 ? 1 : 0.4) : 1;
        n.core.scale.setScalar(task ? 1 + 0.5 * pulse : 1 + 0.08 * Math.sin(t * 1.6 + n.phase));

        const hm = n.halo.material as THREE.MeshBasicMaterial;
        hm.color.set(pal.accent);
        hm.opacity = task ? 0.9 : 0;
        n.halo.rotation.z = t * 1.6;
        n.halo.scale.setScalar(1 + 0.06 * Math.sin(t * 4));

        if (n.link) {
          const lm = n.link.material as THREE.LineBasicMaterial;
          lm.color.set(task ? pal.accent : pal.dim);
          lm.opacity = task ? 0.85 : lost ? 0.07 : 0.24;
          // Light running down the edge of a worker that is working.
          if (task) {
            const a = n.link.geometry.attributes.position;
            const from = new THREE.Vector3().fromBufferAttribute(a, 0);
            const to = new THREE.Vector3().fromBufferAttribute(a, 1);
            for (let k = 0; k < 4; k++) {
              const u = (t * 0.7 + k / 4) % 1;
              tmp.lerpVectors(from, to, u);
              lightSprite(tmp, 0.22, colorOf(pal.accent), 0.7);
            }
          }
        }

        // Its words, always turned to the camera.
        let sub = lost ? "lost" : n.kind === "queen" ? "" : info?.engine ? `idle · ${info.engine}` : "idle";
        let subColor = pal.dim;
        if (task) {
          const secs = Math.max(0, Math.round((Date.now() - Date.parse(task.started_at)) / 1000));
          sub = `${task.phase === "tool" ? `⚙ ${task.tool || "tool"}` : task.phase === "writing" ? "writing…" : "thinking…"}  ${secs}s`;
          subColor = pal.accent;
        }
        const name = n.id === QUEEN ? (rl === "worker" ? "queen" : me || "queen") : n.id === SELF ? me || "worker" : short(n.id);
        setLabel(n, short(name), sub || " ", subColor);
        const dist = cam.distanceTo(n.group.position);
        const h = Math.max(0.5, dist * 0.085);
        const asp = (n.label.userData.aspect as number) || 2;
        n.label.scale.set(h * asp * 0.56, h * 0.56, 1);
        n.label.position.y = n.radius * 0.9 + 0.5 + h * 0.24;
      }

      // Flights: an order out, an answer home, and the flicker between.
      flights.current = flights.current.filter((f) => nowMs - f.t0 < f.dur);
      const a = new THREE.Vector3();
      const b = new THREE.Vector3();
      for (const f of flights.current) {
        const pa = posOf(f.from);
        const pb = posOf(f.to);
        if (!pa || !pb || nowMs < f.t0) continue;
        a.copy(pa);
        b.copy(pb);
        const u = (nowMs - f.t0) / f.dur;
        const c = colorOf(f.color);
        for (let k = 0; k < 6; k++) {
          const tt = Math.max(0, ease(u) - k * 0.035);
          arc(a, b, tt, tmp);
          lightSprite(tmp, f.size * (1 - k * 0.11) * 1.6, c, f.alpha * (1 - k / 6));
        }
      }

      // Where a beam lands: a hexagon that opens and fades.
      rings.current = rings.current.filter((r) => nowMs - r.t0 < (r.big ? 900 : 450));
      for (const r of rings.current) {
        const p = posOf(r.at);
        if (!p || nowMs < r.t0) continue;
        const u = (nowMs - r.t0) / (r.big ? 900 : 450);
        lightSprite(p, (r.big ? 1.8 : 1.1) + u * (r.big ? 4.2 : 1.8), colorOf(r.color), (1 - u) * 0.9, ringTex);
      }

      // The tool's name, drifting up from the worker that called it.
      tags.current = tags.current.filter((g) => {
        const alive = nowMs - g.t0 < 1500;
        if (!alive && g.sprite) {
          scene.remove(g.sprite);
          disposeSprite(g.sprite);
        }
        return alive;
      });
      for (const g of tags.current) {
        const p = posOf(g.at);
        if (!p) continue;
        if (!g.sprite) {
          g.sprite = textSprite(g.text, g.color, 26);
          scene.add(g.sprite);
        }
        const u = (nowMs - g.t0) / 1500;
        const d = cam.distanceTo(p);
        const h = Math.max(0.45, d * 0.07);
        g.sprite.position.set(p.x + 0.9, p.y + 0.6 + u * 1.1, p.z);
        g.sprite.scale.set(h * (g.sprite.userData.aspect as number) * 0.6, h * 0.6, 1);
        (g.sprite.material as THREE.SpriteMaterial).opacity = 1 - u * u;
      }

      // Sparks: a tool called, thrown outward.
      sparks.current = sparks.current.filter((s) => nowMs - s.t0 < 650);
      for (const s of sparks.current) {
        const p = posOf(s.at);
        if (!p || nowMs < s.t0) continue;
        const u = (nowMs - s.t0) / 650;
        for (let k = 0; k < 6; k++) {
          const ang = s.a + (k * Math.PI * 2) / 6;
          tmp.set(p.x + Math.cos(ang) * (0.9 + u * 1.3), p.y + 0.2 + u * 0.7 * (k % 2 ? 1 : -0.4), p.z + Math.sin(ang) * (0.9 + u * 1.3));
          lightSprite(tmp, 0.16 * (1 - u) + 0.04, colorOf(pal.amber), 1 - u);
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
      clearNodes();
      for (const g of tags.current) if (g.sprite) disposeSprite(g.sprite);
      for (const sp of pool) sp.material.dispose();
      glow.dispose();
      ringTex.dispose();
      floorTex.dispose();
      floor.geometry.dispose();
      (floor.material as THREE.Material).dispose();
      composer?.dispose();
      renderer.dispose();
      renderer.domElement.remove();
    };
  }, []);

  return <div className="relative h-full min-h-[200px] w-full overflow-hidden" ref={box} />;
});

export default HiveHex3D;
