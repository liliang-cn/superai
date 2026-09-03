import React, { useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";
import { EffectComposer } from "three/examples/jsm/postprocessing/EffectComposer.js";
import { RenderPass } from "three/examples/jsm/postprocessing/RenderPass.js";
import { UnrealBloomPass } from "three/examples/jsm/postprocessing/UnrealBloomPass.js";
import { EventsOn } from "../../wailsjs/runtime";
import { Pulse as readPulse } from "../../wailsjs/go/main/App";
import { useTween } from "../lib/useTween";

/**
 * The wheel.
 *
 * A wheel too big to see whole, its hub off the left edge of the screen, its
 * rim curving through the panel. Every second of the last two minutes is one
 * position on that rim, and from each one a column of light shoots outward
 * whose length is what that second burned. The wheel turns — the newest second
 * enters at the bottom and rolls up and away — so the columns stream past like
 * spokes seen from the axle.
 *
 * Eight lanes per second, each its own colour: tokens, the cached share of
 * them, reasoning, CPU, tool calls, failed calls, MCP calls and memory calls.
 * Tool calls also throw sparks out through the rim. The rim itself is a band
 * that carries the event stream as tiny text riding the arc, and its inner
 * edge is the heap, so the band breathes as the process allocates. Inside the
 * wheel, where the hub would be, sits the brain: CortexDB's own live view of
 * the knowledge graph, masked to a disc.
 *
 * Nothing here is invented. A column is a second that happened; a spark is a
 * call that was made; the rotation is the clock. Bloom and additive blending
 * make it look like a reactor, but a still frame is a reading, not a mood.
 * The one thing that moves with nothing to report is the dust, and that is
 * there so that idle looks like a reactor at rest rather than a dead screen.
 *
 * Data arrives two ways: one snapshot when the page opens, then frames pushed
 * over the app's event channel as things happen — five a second at most, one
 * every two seconds when idle. The page never polls.
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

const EMPTY: Snap = {
  now: "", since: "", live: false, runs: [], bins: [], tools: [], events: [],
  tokens: 0, cached: 0, rounds: 0, calls: 0, reads: 0, writes: 0, shells: 0, fails: 0, mcp: 0, memory: 0,
  errors: 0, peak: 0, peakThink: 0, heap: 0, heapLow: 0, heapHigh: 0, cpu: 0, goroutines: 0,
};

/** The histogram's period, in seconds — kept in step with pulseWindow in Go. */
const WINDOW = 120;

export const fmtK = (n: number) =>
  n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n));
export const fmtMB = (b: number) => (b / 1048576).toFixed(1) + " MB";

// ---------------------------------------------------------------------------
// The feed
// ---------------------------------------------------------------------------

/** ranges recomputes the window's scaling figures from its bins. */
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
  // Appended by sequence: a resync snapshot can overlap the frames around
  // it, and a line must not repeat.
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
 * The meter, as it happens.
 *
 * One snapshot on mount, frames from then on, and a snapshot again every half
 * minute in case a frame was lost to a reconnect — the SSE stream reconnects
 * on its own, but what was pushed while it was down is gone.
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

/** tokPerSec is the mean over the last ten seconds — the rate the core
 *  reports and the hot spot burns at. */
export function tokPerSec(snap: Snap): number {
  // Thirty seconds, not ten: tokens land in one lump when a model turn ends,
  // and against a ten-second window a forty-second turn reads as 0 tok/s for
  // three quarters of its life, which is the opposite of what happened.
  const tail = snap.bins.slice(-30);
  return tail.length ? tail.reduce((a, b) => a + b.tokens, 0) / tail.length : 0;
}

// ---------------------------------------------------------------------------
// The scene
// ---------------------------------------------------------------------------

/** Where the wheel sits, for a panel of this size. World units are CSS pixels
 *  at the focal plane, so the DOM disc and the WebGL rim agree to the pixel. */
function geometry(w: number, h: number) {
  const R = h * 0.78;
  const cx = -w * 0.14 - R;
  // The half-angle of rim that fits in the panel's height.
  const phi = Math.asin(Math.min(0.999, (h / 2 - 4) / R));
  return { R, cx, phi };
}

/** A soft round sprite, drawn once. */
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

/** A column's alpha along its length: bright at the rim, thinning to a point. */
function rayTexture(): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = 256;
  c.height = 4;
  const g = c.getContext("2d")!;
  const grad = g.createLinearGradient(0, 0, 256, 0);
  grad.addColorStop(0, "rgba(255,255,255,1)");
  grad.addColorStop(0.15, "rgba(255,255,255,.9)");
  grad.addColorStop(0.7, "rgba(255,255,255,.35)");
  grad.addColorStop(1, "rgba(255,255,255,0)");
  g.fillStyle = grad;
  g.fillRect(0, 0, 256, 4);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

/** A tool's name, as a texture for its label. */
function labelTexture(name: string): THREE.Texture {
  const c = document.createElement("canvas");
  c.width = 440;
  c.height = 56;
  const g = c.getContext("2d")!;
  g.font = "600 30px ui-monospace, Menlo, monospace";
  g.textAlign = "center";
  g.textBaseline = "middle";
  g.fillStyle = "rgba(200,220,240,0.9)";
  const short = name.length > 26 ? name.slice(0, 25) + "…" : name;
  g.fillText(short, 220, 28);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

// The lanes, in the order they sit within a second's slot.
const LANES = [
  { key: "tokens", color: new THREE.Color(0.5, 0.92, 1.0), thick: 2.6 },
  { key: "cached", color: new THREE.Color(1.0, 0.72, 0.3), thick: 1.8 },
  { key: "think", color: new THREE.Color(0.62, 1.0, 0.42), thick: 1.6 },
  { key: "cpu", color: new THREE.Color(0.72, 0.48, 1.0), thick: 1.4 },
  { key: "calls", color: new THREE.Color(1.0, 1.0, 1.0), thick: 3.2 },
  { key: "fails", color: new THREE.Color(1.0, 0.36, 0.48), thick: 3.2 },
  { key: "mcp", color: new THREE.Color(1.0, 0.45, 0.85), thick: 1.8 },
  { key: "memory", color: new THREE.Color(0.3, 1.0, 0.86), thick: 1.8 },
] as const;

const SPARK_COLOR: Record<string, THREE.Color> = {
  tool: new THREE.Color(1.0, 0.8, 0.4),
  error: new THREE.Color(1.0, 0.36, 0.48),
  model: new THREE.Color(0.5, 0.92, 1.0),
  think: new THREE.Color(0.62, 1.0, 0.42),
  compact: new THREE.Color(0.72, 0.48, 1.0),
};

interface Spark { theta: number; r: number; v: number; life: number; color: THREE.Color }

const MAX_SPARKS = 900;
const RIM_SEGS = 180;
const BAND_W = 4096;
const BAND_H = 128;
const DUST = 700;

/**
 * Wheel owns every three.js object and knows how to move them. A class rather
 * than hooks because a frame loop wants one mutable thing to poke, not a dozen
 * refs, and because disposing WebGL resources is a job for one place.
 */
class Wheel {
  renderer: THREE.WebGLRenderer;
  scene = new THREE.Scene();
  camera: THREE.PerspectiveCamera;
  composer: EffectComposer;
  bloom: UnrealBloomPass;
  rays: THREE.InstancedMesh;
  band: THREE.Mesh;
  bandGeo: THREE.BufferGeometry;
  bandCanvas: HTMLCanvasElement;
  bandTex: THREE.CanvasTexture;
  rimLine: THREE.Line;
  hot: THREE.Sprite;
  sparks: THREE.Points;
  sparkList: Spark[] = [];
  dust: THREE.Points;
  dustVel: Float32Array;
  nodes = new THREE.Group();
  nodeSprites = new Map<string, { dot: THREE.Sprite; label: THREE.Sprite; theta: number }>();
  web: THREE.LineSegments;
  dot = dotTexture();
  w = 0;
  h = 0;
  R = 300;
  cx = -500;
  phi = 0.7;
  reduce: boolean;
  seenSeq = 0;
  bandDrawnAt = 0;
  dummy = new THREE.Object3D();

  constructor(canvas: HTMLCanvasElement, reduce: boolean) {
    this.reduce = reduce;
    this.renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: false, powerPreference: "high-performance" });
    this.renderer.setClearColor(0x05070f, 1);
    this.camera = new THREE.PerspectiveCamera(30, 1, 1, 6000);
    this.camera.position.set(0, 0, 1000);
    this.camera.lookAt(0, 0, 0);

    this.composer = new EffectComposer(this.renderer);
    this.composer.addPass(new RenderPass(this.scene, this.camera));
    this.bloom = new UnrealBloomPass(new THREE.Vector2(1, 1), 1.15, 0.6, 0.1);
    this.composer.addPass(this.bloom);

    // --- rays: one instanced quad per lane per second ---
    const quad = new THREE.PlaneGeometry(1, 1);
    quad.translate(0.5, 0, 0); // grows from its base
    const rayMat = new THREE.MeshBasicMaterial({
      map: rayTexture(), transparent: true, blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    });
    this.rays = new THREE.InstancedMesh(quad, rayMat, WINDOW * LANES.length);
    this.rays.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    this.rays.frustumCulled = false;
    this.scene.add(this.rays);

    // --- the band: the rim, carrying the stream ---
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

    // The rim's bright edge, a line the bloom can catch.
    const rimGeo = new THREE.BufferGeometry();
    rimGeo.setAttribute("position", new THREE.BufferAttribute(new Float32Array((RIM_SEGS + 1) * 3), 3).setUsage(THREE.DynamicDrawUsage));
    this.rimLine = new THREE.Line(rimGeo, new THREE.LineBasicMaterial({
      color: new THREE.Color(0.55, 0.9, 1.0), transparent: true, opacity: 0.9, blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    }));
    this.rimLine.frustumCulled = false;
    this.scene.add(this.rimLine);

    // --- the hot spot on the newest second ---
    this.hot = new THREE.Sprite(new THREE.SpriteMaterial({
      map: this.dot, color: new THREE.Color(0.6, 0.95, 1.0), transparent: true, opacity: 0.85,
      blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    }));
    this.scene.add(this.hot);

    // --- sparks ---
    const sg = new THREE.BufferGeometry();
    sg.setAttribute("position", new THREE.BufferAttribute(new Float32Array(MAX_SPARKS * 3), 3).setUsage(THREE.DynamicDrawUsage));
    sg.setAttribute("color", new THREE.BufferAttribute(new Float32Array(MAX_SPARKS * 3), 3).setUsage(THREE.DynamicDrawUsage));
    sg.setDrawRange(0, 0);
    this.sparks = new THREE.Points(sg, new THREE.PointsMaterial({
      size: 10, map: this.dot, vertexColors: true, transparent: true, blending: THREE.AdditiveBlending,
      depthWrite: false, depthTest: false, sizeAttenuation: true,
    }));
    this.sparks.frustumCulled = false;
    this.scene.add(this.sparks);

    // --- dust: the atmosphere the light is seen through ---
    const dp = new Float32Array(DUST * 3);
    this.dustVel = new Float32Array(DUST);
    for (let i = 0; i < DUST; i++) {
      dp[i * 3] = (Math.random() - 0.5) * 2400;
      dp[i * 3 + 1] = (Math.random() - 0.5) * 1200;
      dp[i * 3 + 2] = (Math.random() - 0.5) * 300;
      this.dustVel[i] = 6 + Math.random() * 22;
    }
    const dg = new THREE.BufferGeometry();
    dg.setAttribute("position", new THREE.BufferAttribute(dp, 3).setUsage(THREE.DynamicDrawUsage));
    this.dust = new THREE.Points(dg, new THREE.PointsMaterial({
      size: 2.2, map: this.dot, color: new THREE.Color(0.35, 0.6, 0.9), transparent: true, opacity: 0.45,
      blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    }));
    this.dust.frustumCulled = false;
    this.scene.add(this.dust);

    // --- tool nodes and the web of calls ---
    this.scene.add(this.nodes);
    const wg = new THREE.BufferGeometry();
    wg.setAttribute("position", new THREE.BufferAttribute(new Float32Array(240 * 2 * 3), 3).setUsage(THREE.DynamicDrawUsage));
    wg.setDrawRange(0, 0);
    this.web = new THREE.LineSegments(wg, new THREE.LineBasicMaterial({
      color: new THREE.Color(1.0, 0.75, 0.4), transparent: true, opacity: 0.28, blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
    }));
    this.web.frustumCulled = false;
    this.scene.add(this.web);
  }

  resize(w: number, h: number, dpr: number) {
    if (w === this.w && h === this.h) return;
    this.w = w;
    this.h = h;
    const g = geometry(w, h);
    this.R = g.R;
    this.cx = g.cx;
    this.phi = g.phi;
    this.renderer.setPixelRatio(dpr);
    this.renderer.setSize(w, h, false);
    this.composer.setPixelRatio(dpr);
    this.composer.setSize(w, h);
    this.camera.aspect = w / h;
    // The focal plane is exactly the panel: one world unit is one CSS pixel
    // at z = 0, which is what lets a DOM disc sit inside a WebGL rim.
    this.camera.fov = (2 * Math.atan(h / 2 / 1000) * 180) / Math.PI;
    this.camera.updateProjectionMatrix();
  }

  /** theta places an age on the rim: newest at the bottom, oldest at the top. */
  theta(age: number): number {
    return -this.phi + (Math.max(0, Math.min(WINDOW, age)) / WINDOW) * 2 * this.phi;
  }

  rim(theta: number, r = this.R): [number, number] {
    return [this.cx + Math.cos(theta) * r, Math.sin(theta) * r];
  }

  /** ingest reacts to a new snapshot: new events become sparks and web
   *  lines, new tools become nodes. */
  ingest(s: Snap, nowSec: number) {
    const tools = s.tools.slice(0, 14);
    tools.forEach((t, i) => {
      const theta = -this.phi * 0.86 + (tools.length > 1 ? i / (tools.length - 1) : 0.5) * 1.72 * this.phi;
      let n = this.nodeSprites.get(t.name);
      if (!n) {
        const dot = new THREE.Sprite(new THREE.SpriteMaterial({
          map: this.dot, color: new THREE.Color(1.0, 0.78, 0.4), transparent: true, blending: THREE.AdditiveBlending, depthWrite: false, depthTest: false,
        }));
        const label = new THREE.Sprite(new THREE.SpriteMaterial({ map: labelTexture(t.name), transparent: true, depthWrite: false, depthTest: false }));
        label.scale.set(110, 14, 1);
        this.nodes.add(dot, label);
        n = { dot, label, theta };
        this.nodeSprites.set(t.name, n);
      }
      n.theta = theta;
      const [x, y] = this.rim(theta, this.R - 44);
      n.dot.position.set(x, y, 2);
      const [lx, ly] = this.rim(theta, this.R - 44 - 64);
      n.label.position.set(lx, ly, 2);
      const hot = Date.now() - new Date(t.lastAt).getTime() < 2500;
      const sz = hot ? 28 : 12;
      n.dot.scale.set(sz, sz, 1);
      (n.dot.material as THREE.SpriteMaterial).opacity = hot ? 1 : 0.7;
    });

    // Sparks for what just happened. A page that just opened does not replay
    // the whole tail as fireworks.
    const replay = this.seenSeq === 0 && s.events.length > 8;
    for (const e of s.events) {
      if (e.seq <= this.seenSeq || replay) continue;
      const age = nowSec - new Date(e.at).getTime() / 1000;
      const theta = this.theta(age);
      const color = SPARK_COLOR[e.kind] ?? SPARK_COLOR.tool;
      const count = e.kind === "tool" ? 6 : e.kind === "model" ? 10 : 3;
      for (let i = 0; i < count; i++) {
        if (this.sparkList.length >= MAX_SPARKS) this.sparkList.shift();
        this.sparkList.push({
          theta: theta + (Math.random() - 0.5) * 0.02,
          r: this.R + 4,
          v: 180 + Math.random() * 320,
          life: 1,
          color,
        });
      }
    }
    if (s.events.length) this.seenSeq = s.events[s.events.length - 1].seq;

    // The web: a line from each recent tool call's second to its tool's node.
    const pos = this.web.geometry.getAttribute("position") as THREE.BufferAttribute;
    let n = 0;
    for (let i = s.events.length - 1; i >= 0 && n < 240; i--) {
      const e = s.events[i];
      if (e.kind !== "tool") continue;
      const node = this.nodeSprites.get(e.name);
      if (!node) continue;
      const age = nowSec - new Date(e.at).getTime() / 1000;
      if (age > WINDOW) break;
      const [x, y] = this.rim(this.theta(age));
      pos.setXYZ(n * 2, x, y, 1);
      pos.setXYZ(n * 2 + 1, node.dot.position.x, node.dot.position.y, 1);
      n++;
    }
    pos.needsUpdate = true;
    this.web.geometry.setDrawRange(0, n * 2);
  }

  /** drawBand paints the event stream onto the rim texture. */
  drawBand(s: Snap, nowSec: number) {
    const g = this.bandCanvas.getContext("2d")!;
    g.clearRect(0, 0, BAND_W, BAND_H);
    g.fillStyle = "rgba(6,10,24,0.72)";
    g.fillRect(0, 0, BAND_W, BAND_H);
    // Ticks every ten seconds, so the band is a scale and not just a strip.
    g.strokeStyle = "rgba(94,224,255,0.35)";
    g.lineWidth = 2;
    const frac = nowSec % 10;
    for (let t = -frac; t <= WINDOW; t += 10) {
      const x = (t / WINDOW) * BAND_W;
      g.beginPath(); g.moveTo(x, 0); g.lineTo(x, 18); g.stroke();
    }
    g.font = "600 22px ui-monospace, Menlo, monospace";
    g.textBaseline = "middle";
    // Two rows, alternating, so neighbours a second apart do not overprint.
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

  /** frame advances everything by dt milliseconds and renders. */
  frame(s: Snap, dt: number, nowMs: number) {
    const nowSec = nowMs / 1000;
    const { R, phi } = this;
    const heat = Math.min(1, tokPerSec(s) / 600);
    const peak = Math.max(s.peak, 400);
    const peakThink = Math.max(s.peakThink, 200);
    const dTheta = (2 * phi) / WINDOW;

    // --- rays ---
    const fadeFloor = 0.12;
    let inst = 0;
    const color = new THREE.Color();
    const bySec = new Map<number, Bin>();
    for (const b of s.bins) bySec.set(b.sec, b);
    const frac = this.reduce ? 0 : nowSec - Math.floor(nowSec);
    for (let age = 0; age < WINDOW; age++) {
      const b = bySec.get(Math.floor(nowSec) - age);
      // The sub-second part turns the whole ring smoothly between seconds.
      const theta = this.theta(age + frac);
      const fade = fadeFloor + (1 - fadeFloor) * (1 - age / WINDOW);
      LANES.forEach((lane, li) => {
        let len = 0;
        if (b) {
          switch (lane.key) {
            case "tokens": len = b.tokens > 0 ? 50 + 620 * Math.sqrt(b.tokens / peak) : 0; break;
            case "cached": len = b.tokens > 0 && b.cached > 0 ? (50 + 620 * Math.sqrt(b.tokens / peak)) * Math.min(1, b.cached / b.tokens) : 0; break;
            case "think": len = b.think > 0 ? 30 + 380 * Math.sqrt(b.think / peakThink) : 0; break;
            case "cpu": len = b.cpu > 1 ? 20 + 300 * Math.min(1, b.cpu / 100) : 0; break;
            case "calls": len = b.calls > 0 ? 36 + 34 * Math.min(6, b.calls) : 0; break;
            case "fails": len = b.fails > 0 ? 36 + 34 * Math.min(6, b.fails) : 0; break;
            case "mcp": len = b.mcp > 0 ? 28 + 30 * Math.min(6, b.mcp) : 0; break;
            case "memory": len = b.memory > 0 ? 28 + 30 * Math.min(6, b.memory) : 0; break;
          }
        }
        const t = theta + ((li - (LANES.length - 1) / 2) / LANES.length) * dTheta * 0.85;
        const [x, y] = this.rim(t, R + 2);
        this.dummy.position.set(x, y, 0);
        this.dummy.rotation.set(0, 0, t);
        this.dummy.scale.set(Math.max(0.001, len), len > 0 ? lane.thick : 0.001, 1);
        this.dummy.updateMatrix();
        this.rays.setMatrixAt(inst, this.dummy.matrix);
        color.copy(lane.color).multiplyScalar(fade);
        this.rays.setColorAt(inst, color);
        inst++;
      });
    }
    this.rays.instanceMatrix.needsUpdate = true;
    if (this.rays.instanceColor) this.rays.instanceColor.needsUpdate = true;

    // --- the band and the rim line, with the heap on the inner edge ---
    const bp = this.bandGeo.getAttribute("position") as THREE.BufferAttribute;
    const rp = this.rimLine.geometry.getAttribute("position") as THREE.BufferAttribute;
    const lo = s.heapLow, hi = Math.max(s.heapHigh, lo + 1);
    let held = 0.5;
    for (let i = 0; i <= RIM_SEGS; i++) {
      const theta = -phi + (i / RIM_SEGS) * 2 * phi;
      const age = ((theta + phi) / (2 * phi)) * WINDOW;
      const b = bySec.get(Math.floor(nowSec - age));
      if (b && b.heap > 0) held = (b.heap - lo) / (hi - lo);
      const inner = R - 22 - 26 * held;
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

    // --- the hot spot ---
    {
      const [x, y] = this.rim(-phi, R);
      this.hot.position.set(x, y, 1);
      const breathe = this.reduce ? 0 : Math.sin(nowMs / 600) * 0.5 + 0.5;
      const sz = (s.live ? 170 : 90) + 520 * heat + breathe * 30;
      this.hot.scale.set(sz, sz, 1);
      (this.hot.material as THREE.SpriteMaterial).opacity = s.live ? 0.9 : 0.45;
    }

    // --- sparks ---
    const sp = this.sparks.geometry.getAttribute("position") as THREE.BufferAttribute;
    const sc = this.sparks.geometry.getAttribute("color") as THREE.BufferAttribute;
    let n = 0;
    for (let i = this.sparkList.length - 1; i >= 0; i--) {
      const k = this.sparkList[i];
      if (!this.reduce) {
        k.r += (k.v * dt) / 1000;
        k.life -= dt / 1400;
      }
      if (k.life <= 0) { this.sparkList.splice(i, 1); continue; }
      const [x, y] = this.rim(k.theta, k.r);
      sp.setXYZ(n, x, y, 3);
      sc.setXYZ(n, k.color.r * k.life, k.color.g * k.life, k.color.b * k.life);
      n++;
    }
    sp.needsUpdate = true;
    sc.needsUpdate = true;
    this.sparks.geometry.setDrawRange(0, n);

    // --- dust drifts outward, faster the hotter it is ---
    if (!this.reduce) {
      const dp = this.dust.geometry.getAttribute("position") as THREE.BufferAttribute;
      for (let i = 0; i < dp.count; i++) {
        let x = dp.getX(i) + (this.dustVel[i] * (0.4 + heat) * dt) / 1000;
        if (x > 1200) x = -1200;
        dp.setX(i, x);
      }
      dp.needsUpdate = true;
    }

    this.bloom.strength = 0.9 + heat * 0.7;
    this.composer.render();
  }

  dispose() {
    this.renderer.dispose();
    this.rays.geometry.dispose();
    (this.rays.material as THREE.Material).dispose();
    this.bandGeo.dispose();
    this.bandTex.dispose();
    this.dot.dispose();
    this.nodeSprites.forEach((n) => { (n.label.material as THREE.SpriteMaterial).map?.dispose(); });
  }
}

// ---------------------------------------------------------------------------
// The component
// ---------------------------------------------------------------------------

export default function Reactor({ snap, brain }: { snap: Snap; brain?: string | null }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const snapRef = useRef<Snap>(snap);
  const wheelRef = useRef<Wheel | null>(null);
  const [box, setBox] = useState({ w: 0, h: 0 });

  useEffect(() => {
    snapRef.current = snap;
    wheelRef.current?.ingest(snap, Date.now() / 1000);
  }, [snap]);

  useEffect(() => {
    const canvas = canvasRef.current;
    const host = hostRef.current;
    if (!canvas || !host) return;
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    let wheel: Wheel;
    try {
      wheel = new Wheel(canvas, reduce);
    } catch {
      // No WebGL. The numbers still work; this panel stays dark.
      return;
    }
    wheelRef.current = wheel;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const resize = () => {
      const w = host.clientWidth, h = host.clientHeight;
      if (w === 0 || h === 0) return;
      wheel.resize(w, h, dpr);
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

  // The brain disc: where the hub would be. Same geometry the wheel uses, so
  // the DOM circle and the WebGL rim are concentric to the pixel.
  const disc = useMemo(() => {
    if (!box.w) return null;
    const g = geometry(box.w, box.h);
    const r = g.R - 66;
    return { left: box.w / 2 + g.cx - r, top: box.h / 2 - r, size: r * 2 };
  }, [box]);

  const tokens = useTween(snap.tokens);
  const rate = tokPerSec(snap);
  const perSec = useTween(rate, 900);
  const thinking = snap.runs.find((r) => r.think);

  return (
    <div className="rx" ref={hostRef}>
      {brain && disc && (
        <iframe
          className="rx-brain"
          src={brain}
          title="knowledge graph"
          style={{ left: disc.left, top: disc.top, width: disc.size, height: disc.size }}
        />
      )}
      <canvas ref={canvasRef} className="rx-canvas" />

      <div className="rx-head">
        <span className={`rx-led ${snap.live ? "on" : ""}`} />
        <span className="rx-title">REACTOR</span>
        <span className={`rx-state ${snap.live ? "on" : ""}`}>{snap.live ? "burning" : "idle"}</span>
        {snap.runs.slice(0, 2).map((r) => (
          <span className="rx-run" key={r.runId}>
            {r.doing === "thinking" ? "◉ thinking" : r.doing === "waiting" ? "· waiting" : "▸ " + r.doing}
            {r.round > 0 && <em> r{r.round}</em>}
          </span>
        ))}
      </div>

      <div className="rx-core">
        <div className="rx-core-n">{fmtK(tokens)}</div>
        <div className="rx-core-l">tokens</div>
        <div className="rx-core-r">{perSec >= 1 ? fmtK(perSec) : perSec.toFixed(1)} tok/s</div>
      </div>

      {thinking && (
        <div className="rx-think">
          <span className="rx-think-l">thinking</span>
          <span className="rx-think-t">{thinking.think}</span>
        </div>
      )}

      {/* Every counter the meter keeps, in one strip. */}
      <div className="rx-strip">
        <span className="c-cyan"><b>{fmtK(snap.tokens)}</b>tokens</span>
        <span className="c-amber"><b>{fmtK(snap.cached)}</b>cached</span>
        <span className="c-cyan"><b>{snap.rounds}</b>turns</span>
        <span className="c-white"><b>{snap.calls}</b>calls</span>
        <span className="c-white"><b>{snap.reads}</b>reads</span>
        <span className="c-white"><b>{snap.writes}</b>writes</span>
        <span className="c-white"><b>{snap.shells}</b>shells</span>
        <span className="c-pink"><b>{snap.mcp}</b>mcp</span>
        <span className="c-teal"><b>{snap.memory}</b>memory</span>
        <span className={snap.fails ? "c-rose" : "c-dim"}><b>{snap.fails}</b>failed</span>
        <span className="c-violet"><b>{snap.cpu.toFixed(0)}%</b>cpu</span>
        <span className="c-cyan"><b>{fmtMB(snap.heap)}</b>heap</span>
        <span className="c-dim"><b>{snap.goroutines}</b>goroutines</span>
      </div>
    </div>
  );
}

/**
 * The tools, as figures.
 *
 * Busiest first. The count is since this process started, so this is a
 * portrait of what this install actually uses; the wheel above shows the last
 * two minutes of it.
 */
export function ToolTable({ snap, max = 14 }: { snap: Snap; max?: number }) {
  const top = snap.tools.slice(0, max);
  if (top.length === 0) return <div className="rx-empty">no tool has been called yet</div>;
  return (
    <div className="cr-table-wrap">
      <table className="cr-table">
        <thead><tr><th>tool</th><th className="num">calls</th><th className="num">errors</th><th>last</th></tr></thead>
        <tbody>
          {top.map((t) => {
            const hot = Date.now() - new Date(t.lastAt).getTime() < 3000;
            return (
              <tr key={t.name} className={`${hot ? "hot" : ""} ${t.errors ? "bad" : ""}`}>
                <td className="name" title={t.name}>{t.name}</td>
                <td className="num">{t.calls}</td>
                <td className="num">{t.errors}</td>
                <td>{new Date(t.lastAt).toTimeString().slice(0, 8)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
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
