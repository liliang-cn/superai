import React, { useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";
import { EffectComposer } from "three/examples/jsm/postprocessing/EffectComposer.js";
import { RenderPass } from "three/examples/jsm/postprocessing/RenderPass.js";
import { UnrealBloomPass } from "three/examples/jsm/postprocessing/UnrealBloomPass.js";
import { EventsOn } from "../../wailsjs/runtime";
import { Pulse as readPulse } from "../../wailsjs/go/main/App";
import { useTween } from "../lib/useTween";

/**
 * The reactor.
 *
 * The brain in the middle - CortexDB's own live view of the knowledge graph,
 * turning - and around it a ring of standing light: one pillar per figure.
 * Tokens today, tokens this process, tokens this task, cached, turns, tool
 * calls, MCP servers, skills, graph nodes, CPU, heap. Each pillar's length is
 * its figure's order of magnitude and its tip carries the figure itself, so
 * the wheel is read the way a wall of gauges is read: shape first, number
 * second. It is full whether or not anything is running, because most of
 * these are what the install has, not what it is doing.
 *
 * What it is doing is the live layer on top. The band inside the pillars is
 * the last two minutes, one tick a second, with every event riding it as text
 * from twelve o'clock round; its inner edge is the heap. A tool call, a model
 * turn, a line of reasoning throws sparks from twelve o'clock; a pillar whose
 * figure just grew throws them along its own length.
 *
 * Nothing here is invented. The one thing that moves with nothing to report
 * is the dust, so that idle looks like a reactor at rest and not a dead
 * screen. Data arrives by push: one snapshot on open, then frames over the
 * app's event channel as things happen. The page never polls.
 */

// ---- shapes, mirroring backend/pulse.go ----
export interface Bin {
  sec: number; tokens: number; cached: number; calls: number; rounds: number;
  reads: number; writes: number; shells: number; fails: number; mcp: number; memory: number;
  think: number; heap: number; cpu: number;
}
export interface Ev { seq: number; at: string; kind: string; name: string; text: string; n?: number; ms?: number; bad?: boolean; inner?: boolean }
export interface Tool { name: string; calls: number; errors: number; lastAt: string }
export interface Run { runId: string; model: string; round: number; session?: string; doing: string; since: string; seen: string; think?: string }
export interface Snap {
  now: string; since: string; live: boolean;
  runs: Run[]; bins: Bin[]; tools: Tool[]; events: Ev[];
  tokens: number; cached: number; rounds: number; calls: number;
  reads: number; writes: number; shells: number; fails: number; mcp: number; memory: number;
  errors: number; peak: number; peakThink: number;
  heap: number; heapLow: number; heapHigh: number; cpu: number; goroutines: number;
}
interface Frame {
  at: string; live: boolean; runs: Run[]; bins: Bin[]; tools: Tool[]; new: Ev[];
  tokens: number; cached: number; rounds: number; calls: number;
  reads: number; writes: number; shells: number; fails: number; mcp: number; memory: number;
  errors: number; heap: number; cpu: number; goroutines: number;
}

/** One standing pillar: a figure, its name, and the colour of its family.
 *  text overrides the formatted value when a unit belongs on the tip. */
export interface Pillar { key: string; label: string; value: number; color: string; text?: string }

const EMPTY: Snap = {
  now: "", since: "", live: false, runs: [], bins: [], tools: [], events: [],
  tokens: 0, cached: 0, rounds: 0, calls: 0, reads: 0, writes: 0, shells: 0, fails: 0, mcp: 0, memory: 0,
  errors: 0, peak: 0, peakThink: 0, heap: 0, heapLow: 0, heapHigh: 0, cpu: 0, goroutines: 0,
};

/** The histogram's period, in seconds - kept in step with pulseWindow in Go. */
const WINDOW = 120;

export const fmtK = (n: number) =>
  n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n));
export const fmtMB = (b: number) => (b / 1048576).toFixed(1) + " MB";

// ---------------------------------------------------------------------------
// The feed
// ---------------------------------------------------------------------------

function ranges(bins: Bin[]) {
  let peak = 0, peakThink = 0, lo = 0, hi = 0;
  for (const b of bins) {
    if (b.tokens > peak) peak = b.tokens;
    if (b.think > peakThink) peakThink = b.think;
    if (b.heap > 0) {
      if (lo === 0 || b.heap < lo) lo = b.heap;
      if (b.heap > hi) hi = b.heap;
    }
  }
  return { peak, peakThink, heapLow: lo, heapHigh: hi };
}

/** fold applies a pushed frame to the window it has. */
function fold(prev: Snap, f: Frame): Snap {
  const bins = prev.bins.slice();
  for (const b of f.bins ?? []) {
    const i = bins.findIndex((x) => x.sec === b.sec);
    if (i >= 0) bins[i] = b;
    else bins.push(b);
  }
  bins.sort((a, b) => a.sec - b.sec);
  const newest = bins.length ? bins[bins.length - 1].sec : 0;
  const kept = bins.filter((b) => b.sec > newest - WINDOW);
  const last = prev.events.length ? prev.events[prev.events.length - 1].seq : 0;
  const fresh = (f.new ?? []).filter((e) => e.seq > last);
  const events = prev.events.concat(fresh).slice(-240);
  return {
    ...prev,
    now: f.at, live: f.live, runs: f.runs ?? [], tools: f.tools ?? [], bins: kept, events,
    tokens: f.tokens, cached: f.cached, rounds: f.rounds, calls: f.calls,
    reads: f.reads, writes: f.writes, shells: f.shells, fails: f.fails, mcp: f.mcp, memory: f.memory,
    errors: f.errors, heap: f.heap, cpu: f.cpu, goroutines: f.goroutines,
    ...ranges(kept),
  };
}

/**
 * The meter, as it happens: one snapshot on mount, frames from then on, and a
 * snapshot again every half minute in case a frame was lost to a reconnect.
 */
export function usePulse(): Snap {
  const [snap, setSnap] = useState<Snap>(EMPTY);
  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const s = (await readPulse()) as unknown as Snap;
        if (alive && s) setSnap({ ...s, events: s.events ?? [], runs: s.runs ?? [], tools: s.tools ?? [], bins: s.bins ?? [] });
      } catch {
        /* the frames will fill it in, or the next resync will */
      }
    };
    load();
    const off = EventsOn("pulse:frame", (p: { frame?: Frame }) => {
      if (p?.frame) setSnap((prev) => fold(prev, p.frame!));
    });
    const resync = window.setInterval(load, 30000);
    return () => {
      alive = false;
      if (typeof off === "function") off();
      window.clearInterval(resync);
    };
  }, []);
  return snap;
}

/** tokPerSec is the mean over the last thirty seconds: tokens land in one
 *  lump when a turn ends, and a shorter window reads 0 for most of a turn. */
export function tokPerSec(snap: Snap): number {
  const tail = snap.bins.slice(-30);
  return tail.length ? tail.reduce((a, b) => a + b.tokens, 0) / tail.length : 0;
}

// ---------------------------------------------------------------------------
// The scene
// ---------------------------------------------------------------------------

/** World units are CSS pixels at the focal plane, so the DOM disc and the
 *  WebGL ring agree to the pixel. The ring is whole and centred. */
function geometry(w: number, h: number) {
  return { R: Math.min(w, h) * 0.5 * 0.52 };
}

function dotTexture(): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = c.height = 64;
  const g = c.getContext("2d")!;
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0, "rgba(255,255,255,1)");
  grad.addColorStop(0.35, "rgba(255,255,255,.55)");
  grad.addColorStop(1, "rgba(255,255,255,0)");
  g.fillStyle = grad;
  g.fillRect(0, 0, 64, 64);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

/** A pillar's alpha along its length: solid at the ring, thinning to a point. */
function rayTexture(): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = 256;
  c.height = 4;
  const g = c.getContext("2d")!;
  const grad = g.createLinearGradient(0, 0, 256, 0);
  grad.addColorStop(0, "rgba(255,255,255,1)");
  grad.addColorStop(0.6, "rgba(255,255,255,.8)");
  grad.addColorStop(1, "rgba(255,255,255,0)");
  g.fillStyle = grad;
  g.fillRect(0, 0, 256, 4);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

const SPARK_COLOR: Record<string, THREE.Color> = {
  tool: new THREE.Color(1.0, 0.8, 0.4),
  error: new THREE.Color(1.0, 0.36, 0.48),
  model: new THREE.Color(0.5, 0.92, 1.0),
  think: new THREE.Color(0.62, 1.0, 0.42),
  compact: new THREE.Color(0.72, 0.48, 1.0),
};

interface Spark { theta: number; r: number; v: number; life: number; color: THREE.Color }
interface Post { pillar: Pillar; len: number; target: number; color: THREE.Color; el: HTMLDivElement; text: string; theta: number }

const MAX_PILLARS = 32;
const MAX_SPARKS = 900;
const RIM_SEGS = 180;
const BAND_W = 4096;
const BAND_H = 128;
const DUST = 600;

/**
 * Wheel owns every three.js object and knows how to move them. A class rather
 * than hooks because a frame loop wants one mutable thing to poke, and because
 * disposing WebGL resources is a job for one place.
 */
class Wheel {
  renderer: THREE.WebGLRenderer;
  scene = new THREE.Scene();
  camera: THREE.PerspectiveCamera;
  composer: EffectComposer;
  bloom: UnrealBloomPass;
  posts: THREE.InstancedMesh;
  postList: Post[] = [];
  band: THREE.Mesh;
  bandGeo: THREE.BufferGeometry;
  bandCanvas: HTMLCanvasElement;
  bandTex: THREE.CanvasTexture;
  rimLine: THREE.Line;
  sparks: THREE.Points;
  sparkList: Spark[] = [];
  dust: THREE.Points;
  dustVel: Float32Array;
  dot = dotTexture();
  w = 0;
  h = 0;
  R = 300;
  dpr = 0;
  reduce: boolean;
  seenSeq = 0;
  bandDrawnAt = 0;
  dummy = new THREE.Object3D();
  // tips holds the DOM labels at the pillars' ends. Text in the DOM rather
  // than on a sprite: a sprite is a texture and a texture is soft at any
  // size the browser did not pick, and these are the figures the wheel is
  // for.
  tips: HTMLDivElement;

  constructor(canvas: HTMLCanvasElement, tips: HTMLDivElement, reduce: boolean) {
    this.reduce = reduce;
    this.tips = tips;
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: false, powerPreference: "high-performance" });
    // Pure black: the canvas is screen-blended over the panel, and black is
    // the one colour that adds nothing.
    this.renderer.setClearColor(0x000000, 1);
    this.camera = new THREE.PerspectiveCamera(30, 1, 1, 6000);
    this.camera.position.set(0, 0, 1000);
    this.camera.lookAt(0, 0, 0);

    this.composer = new EffectComposer(this.renderer);
    this.composer.addPass(new RenderPass(this.scene, this.camera));
    this.bloom = new UnrealBloomPass(new THREE.Vector2(1, 1), 0.8, 0.45, 0.4);
    this.composer.addPass(this.bloom);

    // --- pillars: two instanced quads each, a wide soft body and a bright core ---
    const quad = new THREE.PlaneGeometry(1, 1);
    quad.translate(0.5, 0, 0);
    const postMat = new THREE.MeshBasicMaterial({
      map: rayTexture(), transparent: true, blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    });
    this.posts = new THREE.InstancedMesh(quad, postMat, MAX_PILLARS * 2);
    this.posts.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    this.posts.frustumCulled = false;
    this.scene.add(this.posts);

    // --- the band: the last two minutes, riding the ring ---
    this.bandCanvas = document.createElement("canvas");
    this.bandCanvas.width = BAND_W;
    this.bandCanvas.height = BAND_H;
    this.bandTex = new THREE.CanvasTexture(this.bandCanvas);
    this.bandTex.colorSpace = THREE.SRGBColorSpace;
    this.bandGeo = new THREE.BufferGeometry();
    const verts = new Float32Array((RIM_SEGS + 1) * 2 * 3);
    const uvs = new Float32Array((RIM_SEGS + 1) * 2 * 2);
    const idx: number[] = [];
    for (let i = 0; i <= RIM_SEGS; i++) {
      uvs[i * 4] = i / RIM_SEGS; uvs[i * 4 + 1] = 0;
      uvs[i * 4 + 2] = i / RIM_SEGS; uvs[i * 4 + 3] = 1;
      if (i < RIM_SEGS) {
        const a = i * 2, b = i * 2 + 1, c = i * 2 + 2, d = i * 2 + 3;
        idx.push(a, b, c, b, d, c);
      }
    }
    this.bandGeo.setAttribute("position", new THREE.BufferAttribute(verts, 3).setUsage(THREE.DynamicDrawUsage));
    this.bandGeo.setAttribute("uv", new THREE.BufferAttribute(uvs, 2));
    this.bandGeo.setIndex(idx);
    this.band = new THREE.Mesh(this.bandGeo, new THREE.MeshBasicMaterial({
      map: this.bandTex, transparent: true, side: THREE.DoubleSide, depthWrite: false, depthTest: false,
    }));
    this.band.frustumCulled = false;
    this.scene.add(this.band);

    const rimGeo = new THREE.BufferGeometry();
    rimGeo.setAttribute("position", new THREE.BufferAttribute(new Float32Array((RIM_SEGS + 1) * 3), 3).setUsage(THREE.DynamicDrawUsage));
    this.rimLine = new THREE.Line(rimGeo, new THREE.LineBasicMaterial({
      color: new THREE.Color(0.55, 0.9, 1.0), transparent: true, opacity: 0.9, blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    }));
    this.rimLine.frustumCulled = false;
    this.scene.add(this.rimLine);

    // --- sparks ---
    const sg = new THREE.BufferGeometry();
    sg.setAttribute("position", new THREE.BufferAttribute(new Float32Array(MAX_SPARKS * 3), 3).setUsage(THREE.DynamicDrawUsage));
    sg.setAttribute("color", new THREE.BufferAttribute(new Float32Array(MAX_SPARKS * 3), 3).setUsage(THREE.DynamicDrawUsage));
    sg.setDrawRange(0, 0);
    this.sparks = new THREE.Points(sg, new THREE.PointsMaterial({
      size: 9, map: this.dot, vertexColors: true, transparent: true, blending: THREE.AdditiveBlending,
      depthWrite: false, depthTest: false, sizeAttenuation: true,
    }));
    this.sparks.frustumCulled = false;
    this.scene.add(this.sparks);

    // --- dust ---
    const dp = new Float32Array(DUST * 3);
    this.dustVel = new Float32Array(DUST);
    for (let i = 0; i < DUST; i++) {
      dp[i * 3] = (Math.random() - 0.5) * 2400;
      dp[i * 3 + 1] = (Math.random() - 0.5) * 1400;
      dp[i * 3 + 2] = (Math.random() - 0.5) * 300;
      this.dustVel[i] = 4 + Math.random() * 16;
    }
    const dg = new THREE.BufferGeometry();
    dg.setAttribute("position", new THREE.BufferAttribute(dp, 3).setUsage(THREE.DynamicDrawUsage));
    this.dust = new THREE.Points(dg, new THREE.PointsMaterial({
      size: 2, map: this.dot, color: new THREE.Color(0.35, 0.6, 0.9), transparent: true, opacity: 0.4,
      blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    }));
    this.dust.frustumCulled = false;
    this.scene.add(this.dust);
  }

  resize(w: number, h: number, dpr: number) {
    // dpr is compared too: dragging the window from a Retina screen to an
    // external one changes it without changing the box, and a buffer left at
    // the old ratio is a scene rendered at the wrong scale.
    if (w === this.w && h === this.h && dpr === this.dpr) return;
    this.w = w;
    this.h = h;
    this.dpr = dpr;
    this.R = geometry(w, h).R;
    this.renderer.setPixelRatio(dpr);
    this.renderer.setSize(w, h, false);
    this.composer.setPixelRatio(dpr);
    this.composer.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.fov = (2 * Math.atan(h / 2 / 1000) * 180) / Math.PI;
    this.camera.updateProjectionMatrix();
  }

  /** Twelve o'clock, where the newest second and the first pillar sit. */
  get th0(): number { return Math.PI / 2; }

  /** theta places an age on the band: newest at the top, then clockwise. */
  theta(age: number): number {
    return this.th0 - (Math.max(0, Math.min(WINDOW, age)) / WINDOW) * 2 * Math.PI;
  }

  rim(theta: number, r = this.R): [number, number] {
    return [Math.cos(theta) * r, Math.sin(theta) * r];
  }

  /** setPillars gives the wheel its figures. A figure that grew throws sparks
   *  along its pillar; a figure whose text changed gets a new label. */
  setPillars(list: Pillar[]) {
    const n = Math.min(MAX_PILLARS, list.length);
    const top = list.reduce((m, p) => Math.max(m, p.value), 1);
    // Log scale, against the largest of them: a wheel of figures spanning
    // five orders of magnitude has to be read as magnitudes or the small
    // ones vanish. The number at the tip is the reading; the length is the
    // shape.
    const span = Math.log10(1 + top);
    // The longest pillar, label included, stays inside the panel: a figure
    // whose tip is off the screen is a figure nobody can read.
    const room = Math.max(60, Math.min(this.w, this.h) / 2 - this.R - 74);
    const kept = new Set<string>();
    list.slice(0, n).forEach((p, i) => {
      kept.add(p.key);
      const theta = this.th0 - ((i + 0.5) / n) * 2 * Math.PI;
      const target = 24 + (room - 24) * (span > 0 ? Math.log10(1 + Math.max(0, p.value)) / span : 0);
      const value = p.text ?? fmtK(p.value);
      const text = value + " " + p.label + " " + p.color;
      let post = this.postList.find((x) => x.pillar.key === p.key);
      if (!post) {
        const el = document.createElement("div");
        el.className = "rx-tip";
        el.innerHTML = `<b></b><span></span>`;
        this.tips.appendChild(el);
        post = { pillar: p, len: 0, target, color: new THREE.Color(p.color), el, text: "", theta };
        this.postList.push(post);
      } else if (p.value > post.pillar.value) {
        this.burst(theta, this.R + 4, post.len, post.color, 14);
      }
      if (text !== post.text) {
        (post.el.firstChild as HTMLElement).textContent = value;
        (post.el.lastChild as HTMLElement).textContent = p.label;
        post.el.style.color = p.color;
        post.text = text;
      }
      post.pillar = p;
      post.target = target;
      post.theta = theta;
      post.color.set(p.color);
    });
    for (let i = this.postList.length - 1; i >= 0; i--) {
      if (!kept.has(this.postList[i].pillar.key)) {
        const gone = this.postList.splice(i, 1)[0];
        gone.el.remove();
      }
    }
  }

  burst(theta: number, r: number, along: number, color: THREE.Color, count: number) {
    for (let i = 0; i < count; i++) {
      if (this.sparkList.length >= MAX_SPARKS) this.sparkList.shift();
      this.sparkList.push({
        theta: theta + (Math.random() - 0.5) * 0.03,
        r: r + Math.random() * Math.max(0, along),
        v: 120 + Math.random() * 240,
        life: 1,
        color,
      });
    }
  }

  /** ingest reacts to a new snapshot: new events become sparks at twelve. */
  ingest(s: Snap) {
    const replay = this.seenSeq === 0 && s.events.length > 8;
    for (const e of s.events) {
      if (e.seq <= this.seenSeq || replay) continue;
      const color = SPARK_COLOR[e.kind] ?? SPARK_COLOR.tool;
      this.burst(this.th0, this.R + 2, 0, color, e.kind === "model" ? 14 : e.kind === "tool" ? 8 : 3);
    }
    if (s.events.length) this.seenSeq = s.events[s.events.length - 1].seq;
  }

  /** drawBand paints the event stream onto the ring texture. */
  drawBand(s: Snap, nowSec: number) {
    const g = this.bandCanvas.getContext("2d")!;
    g.clearRect(0, 0, BAND_W, BAND_H);
    g.fillStyle = "rgba(6,10,24,0.78)";
    g.fillRect(0, 0, BAND_W, BAND_H);
    g.strokeStyle = "rgba(94,224,255,0.35)";
    g.lineWidth = 2;
    const frac = nowSec % 10;
    for (let t = 10 - frac; t < WINDOW; t += 10) {
      const x = (t / WINDOW) * BAND_W;
      g.beginPath(); g.moveTo(x, 0); g.lineTo(x, 18); g.stroke();
    }
    g.strokeStyle = "rgba(255,255,255,0.7)";
    g.lineWidth = 3;
    g.beginPath(); g.moveTo(2, 0); g.lineTo(2, BAND_H); g.stroke();
    g.font = "600 22px ui-monospace, Menlo, monospace";
    g.textBaseline = "middle";
    let row = 0;
    for (let i = s.events.length - 1; i >= 0; i--) {
      const e = s.events[i];
      const age = nowSec - new Date(e.at).getTime() / 1000;
      if (age < 0) continue;
      if (age > WINDOW) break;
      const x = (age / WINDOW) * BAND_W;
      const y = row % 2 === 0 ? 44 : 92;
      row++;
      const label = e.kind === "model" ? `${fmtK(e.n ?? 0)} tok` : e.kind === "think" ? e.text.slice(0, 40) : e.name;
      g.fillStyle = e.bad ? "rgba(255,92,122,0.95)" :
        e.kind === "model" ? "rgba(120,225,255,0.95)" :
        e.kind === "think" ? "rgba(157,255,106,0.8)" :
        e.kind === "compact" ? "rgba(184,122,255,0.9)" : "rgba(255,200,110,0.95)";
      g.fillText(label, x + 6, y);
      g.beginPath(); g.arc(x, y, 3, 0, Math.PI * 2); g.fill();
    }
    this.bandTex.needsUpdate = true;
  }

  frame(s: Snap, dt: number, nowMs: number) {
    const nowSec = nowMs / 1000;
    const { R } = this;
    const heat = Math.min(1, tokPerSec(s) / 600);
    const color = new THREE.Color();

    // --- pillars ---
    let inst = 0;
    const k = this.reduce ? 1 : Math.min(1, dt / 220);
    for (const post of this.postList) {
      post.len += (post.target - post.len) * k;
      const [x, y] = this.rim(post.theta, R + 4);
      // The body: wide, soft.
      this.dummy.position.set(x, y, 0);
      this.dummy.rotation.set(0, 0, post.theta);
      this.dummy.scale.set(Math.max(1, post.len), 11, 1);
      this.dummy.updateMatrix();
      this.posts.setMatrixAt(inst, this.dummy.matrix);
      this.posts.setColorAt(inst, color.copy(post.color).multiplyScalar(0.32));
      inst++;
      // The core: narrow, bright - what the bloom catches.
      this.dummy.scale.set(Math.max(1, post.len * 0.96), 2.2, 1);
      this.dummy.updateMatrix();
      this.posts.setMatrixAt(inst, this.dummy.matrix);
      this.posts.setColorAt(inst, color.copy(post.color));
      inst++;
      // The label sits just past the tip, in CSS pixels from the panel's
      // centre; world y is up and CSS y is down.
      const [lx, ly] = this.rim(post.theta, R + 4 + post.len + 12);
      post.el.style.transform = `translate(${this.w / 2 + lx}px, ${this.h / 2 - ly}px) translate(-50%, -50%)`;
    }
    this.dummy.scale.set(0.001, 0.001, 1);
    this.dummy.position.set(0, 0, -10);
    this.dummy.updateMatrix();
    for (let i = inst; i < MAX_PILLARS * 2; i++) this.posts.setMatrixAt(i, this.dummy.matrix);
    this.posts.instanceMatrix.needsUpdate = true;
    if (this.posts.instanceColor) this.posts.instanceColor.needsUpdate = true;

    // --- the band and the rim line, the heap on the inner edge ---
    const bySec = new Map<number, Bin>();
    for (const b of s.bins) bySec.set(b.sec, b);
    const bp = this.bandGeo.getAttribute("position") as THREE.BufferAttribute;
    const rp = this.rimLine.geometry.getAttribute("position") as THREE.BufferAttribute;
    const lo = s.heapLow, hi = Math.max(s.heapHigh, lo + 1);
    let held = 0.5;
    for (let i = 0; i <= RIM_SEGS; i++) {
      const theta = this.th0 - (i / RIM_SEGS) * 2 * Math.PI;
      const age = (i / RIM_SEGS) * WINDOW;
      const b = bySec.get(Math.floor(nowSec - age));
      if (b && b.heap > 0) held = (b.heap - lo) / (hi - lo);
      const inner = R - 18 - 18 * held;
      const [ix, iy] = this.rim(theta, inner);
      const [ox, oy] = this.rim(theta, R);
      bp.setXYZ(i * 2, ix, iy, 0);
      bp.setXYZ(i * 2 + 1, ox, oy, 0);
      rp.setXYZ(i, ox, oy, 0.5);
    }
    bp.needsUpdate = true;
    rp.needsUpdate = true;
    if (nowMs - this.bandDrawnAt > 120) {
      this.bandDrawnAt = nowMs;
      this.drawBand(s, nowSec);
    }

    // --- sparks ---
    const sp = this.sparks.geometry.getAttribute("position") as THREE.BufferAttribute;
    const sc = this.sparks.geometry.getAttribute("color") as THREE.BufferAttribute;
    let n = 0;
    for (let i = this.sparkList.length - 1; i >= 0; i--) {
      const q = this.sparkList[i];
      if (!this.reduce) {
        q.r += (q.v * dt) / 1000;
        q.life -= dt / 1400;
      }
      if (q.life <= 0) { this.sparkList.splice(i, 1); continue; }
      const [x, y] = this.rim(q.theta, q.r);
      sp.setXYZ(n, x, y, 3);
      sc.setXYZ(n, q.color.r * q.life, q.color.g * q.life, q.color.b * q.life);
      n++;
    }
    sp.needsUpdate = true;
    sc.needsUpdate = true;
    this.sparks.geometry.setDrawRange(0, n);

    // --- dust ---
    if (!this.reduce) {
      const dp = this.dust.geometry.getAttribute("position") as THREE.BufferAttribute;
      for (let i = 0; i < dp.count; i++) {
        let x = dp.getX(i) + (this.dustVel[i] * (0.4 + heat) * dt) / 1000;
        if (x > 1200) x = -1200;
        dp.setX(i, x);
      }
      dp.needsUpdate = true;
    }

    this.bloom.strength = 0.6 + heat * 0.35;
    this.composer.render();
  }

  dispose() {
    this.renderer.dispose();
    this.posts.geometry.dispose();
    (this.posts.material as THREE.Material).dispose();
    this.bandGeo.dispose();
    this.bandTex.dispose();
    this.dot.dispose();
    this.postList.forEach((p) => p.el.remove());
  }
}

// ---------------------------------------------------------------------------
// The component
// ---------------------------------------------------------------------------

export default function Reactor({ snap, pillars, brain }: { snap: Snap; pillars: Pillar[]; brain?: string | null }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const tipsRef = useRef<HTMLDivElement>(null);
  const snapRef = useRef<Snap>(snap);
  const pillarsRef = useRef<Pillar[]>(pillars);
  const wheelRef = useRef<Wheel | null>(null);
  const [box, setBox] = useState({ w: 0, h: 0 });

  useEffect(() => {
    snapRef.current = snap;
    wheelRef.current?.ingest(snap);
  }, [snap]);

  useEffect(() => {
    pillarsRef.current = pillars;
    wheelRef.current?.setPillars(pillars);
  }, [pillars]);

  useEffect(() => {
    const canvas = canvasRef.current;
    const host = hostRef.current;
    const tips = tipsRef.current;
    if (!canvas || !host || !tips) return;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    let wheel: Wheel;
    try {
      wheel = new Wheel(canvas, tips, reduce);
    } catch {
      return; // no WebGL: the charts beside it still work
    }
    wheelRef.current = wheel;
    const resize = () => {
      const w = host.clientWidth, h = host.clientHeight;
      if (w === 0 || h === 0) return;
      // Read the ratio every time rather than once: a window dragged between
      // displays changes it, and nothing else would notice.
      wheel.resize(w, h, Math.min(window.devicePixelRatio || 1, 2));
      wheel.setPillars(pillarsRef.current);
      setBox({ w, h });
    };
    const ro = new ResizeObserver(resize);
    ro.observe(host);
    resize();
    let raf = 0;
    let last = performance.now();
    const loop = (now: number) => {
      const dt = Math.min(64, now - last);
      last = now;
      if (wheel.w > 0) wheel.frame(snapRef.current, dt, Date.now());
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      wheel.dispose();
      wheelRef.current = null;
    };
  }, []);

  // The brain disc, where the hub is: the same geometry the wheel uses, so
  // the DOM circle and the WebGL ring are concentric to the pixel.
  const disc = useMemo(() => {
    if (!box.w) return null;
    const r = geometry(box.w, box.h).R - 44;
    return { left: box.w / 2 - r, top: box.h / 2 - r, size: r * 2 };
  }, [box]);

  const tokens = useTween(snap.tokens);
  const perSec = useTween(tokPerSec(snap), 900);
  const thinking = snap.runs.find((r) => r.think);

  return (
    <div className="rx" ref={hostRef}>
      {brain && disc && (
        <iframe className="rx-brain" src={brain} title="knowledge graph"
          style={{ left: disc.left, top: disc.top, width: disc.size, height: disc.size }} />
      )}
      <canvas ref={canvasRef} className="rx-canvas" />
      <div className="rx-tips" ref={tipsRef} />
      <div className="rx-head">
        <span className={`rx-led ${snap.live ? "on" : ""}`} />
        <span className="rx-title">REACTOR</span>
        <span className={`rx-state ${snap.live ? "on" : ""}`}>{snap.live ? "burning" : "idle"}</span>
        {snap.runs.slice(0, 2).map((r) => (
          <span className="rx-run" key={r.runId}>
            {r.doing === "thinking" ? "thinking" : r.doing === "waiting" ? "waiting" : r.doing}
            {r.round > 0 && <em> r{r.round}</em>}
          </span>
        ))}
      </div>
      <div className="rx-core">
        <div className="rx-core-n">{fmtK(tokens)}</div>
        <div className="rx-core-l">tokens this process</div>
        <div className="rx-core-r">{perSec >= 1 ? fmtK(perSec) : perSec.toFixed(1)} tok/s</div>
      </div>
      {thinking && (
        <div className="rx-think bottom">
          <span className="rx-think-l">thinking</span>
          <span className="rx-think-t">{thinking.think}</span>
        </div>
      )}
    </div>
  );
}

/** Every counter the meter keeps, as a row of figures. */
export function Counters({ snap }: { snap: Snap }) {
  const rows: [string, string, string][] = [
    ["tokens", fmtK(snap.tokens), "c-cyan"],
    ["cached", fmtK(snap.cached), "c-amber"],
    ["turns", String(snap.rounds), "c-cyan"],
    ["calls", String(snap.calls), "c-white"],
    ["reads", String(snap.reads), "c-white"],
    ["writes", String(snap.writes), "c-white"],
    ["shells", String(snap.shells), "c-white"],
    ["mcp", String(snap.mcp), "c-pink"],
    ["memory", String(snap.memory), "c-teal"],
    ["failed", String(snap.fails), snap.fails ? "c-rose" : "c-dim"],
    ["cpu", snap.cpu.toFixed(0) + "%", "c-violet"],
    ["heap", fmtMB(snap.heap), "c-cyan"],
    ["goroutines", String(snap.goroutines), "c-dim"],
  ];
  return (
    <div className="rx-counters">
      {rows.map(([k, v, c]) => (
        <span className={c} key={k}><b>{v}</b>{k}</span>
      ))}
    </div>
  );
}

/** The narration: what just happened, newest first. */
export function PulseTicker({ snap, max = 60 }: { snap: Snap; max?: number }) {
  const events = snap.events.slice(-max).reverse();
  if (events.length === 0) return <div className="rx-empty">nothing has run since this process started</div>;
  return (
    <div className="rx-lines">
      {events.map((e) => (
        <div className={`rx-line ${e.kind} ${e.bad ? "bad" : ""}`} key={e.seq}>
          <span className="rx-at">{new Date(e.at).toTimeString().slice(0, 8)}</span>
          <span className="rx-kind">{e.kind}</span>
          <span className="rx-name">{e.name}{e.inner ? " ↪" : ""}</span>
          <span className="rx-text">{e.text}</span>
          {e.n ? <span className="rx-n">{fmtK(e.n)} tok</span> : null}
        </div>
      ))}
    </div>
  );
}
