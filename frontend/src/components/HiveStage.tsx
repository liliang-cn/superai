import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { palette, type Palette } from "./hivePalette";
import { doing, ease, hash01, HiveFx, QUEEN, runningOn, SELF, short } from "./hiveFx";
import type { StageHandle, StagePulse, StageProps, StageWorker } from "./hiveFx";

export type { StageHandle, StagePulse, StageTask, StageWorker } from "./hiveFx";

// The hive, drawn flat, in one of two looks.
//
// Orbit: the queen is a star and the workers are planets going round her, each
// ring a little slower than the one inside it. Orders go out as comets and come
// back as comets; a working planet burns brighter and a tool call throws a
// flare off it.
//
// Mycelium: the queen is the root and the workers grow out of her on filaments
// that branch the way a fungus does — a few trunks, then a fork to each worker.
// Work shows as light running down the filament, and a worker asking another
// grows a new thread between them for as long as the question is open.
//
// Tree: the queen is the root and the trunk, and the workers are blossoms at the
// ends of its branches. Work is sap — light climbing the branch to the worker
// doing it — and a finished order drops back down to the root.
//
// All of them read the same events from HiveFx; they differ only in where things are,
// what path light takes between two of them, and what a node looks like.

export type Look = "orbit" | "mycelium" | "tree";

type Pt = { x: number; y: number };
/** Where a node is this frame. `z` is depth for the orbits, -1 far to 1 near. */
type Placed = Pt & { z: number; r: number };

interface Props extends StageProps {
  look: Look;
}

const rgba = (hex: string, a: number) => {
  const m = /^#([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return hex;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 255},${(n >> 8) & 255},${n & 255},${a})`;
};

/** A point `u` of the way along a polyline, by length. */
function alongLine(pts: Pt[], u: number): Pt {
  if (pts.length === 1) return pts[0];
  let total = 0;
  const seg: number[] = [];
  for (let i = 1; i < pts.length; i++) {
    const d = Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y);
    seg.push(d);
    total += d;
  }
  let want = Math.max(0, Math.min(1, u)) * total;
  for (let i = 0; i < seg.length; i++) {
    if (want <= seg[i] || i === seg.length - 1) {
      const k = seg[i] ? want / seg[i] : 0;
      return { x: pts[i].x + (pts[i + 1].x - pts[i].x) * k, y: pts[i].y + (pts[i + 1].y - pts[i].y) * k };
    }
    want -= seg[i];
  }
  return pts[pts.length - 1];
}

/** A curve from a to b that bows sideways by `bow` of its length, as points. */
function bowed(a: Pt, b: Pt, bow: number, n = 24): Pt[] {
  const mx = (a.x + b.x) / 2 - (b.y - a.y) * bow;
  const my = (a.y + b.y) / 2 + (b.x - a.x) * bow;
  const out: Pt[] = [];
  for (let i = 0; i <= n; i++) {
    const u = i / n;
    const v = 1 - u;
    out.push({ x: v * v * a.x + 2 * v * u * mx + u * u * b.x, y: v * v * a.y + 2 * v * u * my + u * u * b.y });
  }
  return out;
}

function strokeLine(ctx: CanvasRenderingContext2D, pts: Pt[], upTo = 1) {
  if (pts.length < 2) return;
  const n = Math.max(1, Math.floor((pts.length - 1) * upTo));
  ctx.beginPath();
  ctx.moveTo(pts[0].x, pts[0].y);
  for (let i = 1; i <= n; i++) ctx.lineTo(pts[i].x, pts[i].y);
  ctx.stroke();
}

const HiveStage = forwardRef<StageHandle, Props>(function HiveStage({ role, self, workers, tasks, ready, look }, ref) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const live = useRef({ role, self, workers, tasks, look });
  live.current = { role, self, workers, tasks, look };
  const fx = useRef(new HiveFx());
  // When each worker first appeared here, for growing it in. The first batch is
  // not new, so it is stamped far in the past.
  const born = useRef(new Map<string, number>());

  useImperativeHandle(ref, () => ({ pulse: (p: StagePulse) => fx.current.pulse(p, live.current.role, palette()) }), []);

  useEffect(() => {
    if (ready) fx.current.tasks(tasks, live.current.role, palette());
  }, [tasks, ready]);

  useEffect(() => {
    const cv = canvas.current;
    const host = box.current;
    if (!cv || !host) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;
    let W = 0;
    let H = 0;
    let raf = 0;
    let pal: Palette = palette();
    let palAt = 0;
    let first = true;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const resize = () => {
      const r = host.getBoundingClientRect();
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      W = Math.max(260, Math.floor(r.width));
      H = Math.max(180, Math.floor(r.height));
      cv.width = W * dpr;
      cv.height = H * dpr;
      cv.style.width = `${W}px`;
      cv.style.height = `${H}px`;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };
    const ro = new ResizeObserver(resize);
    ro.observe(host);
    resize();

    // A fixed field of stars for the orbits, in the unit square.
    const stars = Array.from({ length: 160 }, (_, i) => ({
      x: hash01("x" + i, 7),
      y: hash01("y" + i, 11),
      s: 0.4 + hash01("s" + i, 3) * 1.1,
      tw: hash01("t" + i, 5) * 6.28,
    }));

    const frame = (now: number) => {
      if (now - palAt > 2000) {
        pal = palette();
        palAt = now;
      }
      const t = still ? 0 : now / 1000;
      const { role: rl, workers: ws, self: me, tasks: ts, look: lk } = live.current;
      for (const w of ws) if (!born.current.has(w.name)) born.current.set(w.name, first ? now - 60000 : now);
      first = false;
      const growth = (name: string) => Math.min(1, (now - (born.current.get(name) ?? 0)) / 1400);

      fx.current.prune(now);
      ctx.clearRect(0, 0, W, H);
      const cx = W / 2;
      const cy = H / 2;
      const unit = Math.max(0.6, Math.min(1.5, Math.min(W, H) / 360));
      const busy = (id: string) => runningOn(id, rl, ts);
      const names: string[] = rl === "queen" ? ws.map((w) => w.name) : rl === "worker" ? [SELF] : [];
      const info = (id: string): StageWorker | undefined => ws.find((w) => w.name === id);

      const pos = new Map<string, Placed>();
      // The path from the queen out to each worker, as points.
      const paths = new Map<string, Pt[]>();

      // ---- small helpers that draw ----
      const glowDot = (x: number, y: number, r: number, color: string, alpha: number) => {
        const g = ctx.createRadialGradient(x, y, 0, x, y, r * 3);
        g.addColorStop(0, rgba(color, alpha));
        g.addColorStop(0.3, rgba(color, alpha * 0.55));
        g.addColorStop(1, rgba(color, 0));
        ctx.fillStyle = g;
        ctx.beginPath();
        ctx.arc(x, y, r * 3, 0, Math.PI * 2);
        ctx.fill();
      };
      const aura = (x: number, y: number, r: number, color: string, a: number) => {
        const g = ctx.createRadialGradient(x, y, 0, x, y, r);
        g.addColorStop(0, rgba(color, a));
        g.addColorStop(1, rgba(color, 0));
        ctx.fillStyle = g;
        ctx.beginPath();
        ctx.arc(x, y, r, 0, Math.PI * 2);
        ctx.fill();
      };
      const blob = (x: number, y: number, r: number, color: string, seed: string, alpha: number) => {
        // A soft, slightly irregular body: a circle nudged by two slow waves.
        const p1 = hash01(seed, 30) * 6.28;
        ctx.save();
        ctx.beginPath();
        for (let i = 0; i <= 36; i++) {
          const a = (i / 36) * Math.PI * 2;
          const rr = r * (1 + 0.08 * Math.sin(a * 3 + p1 + t * 0.9) + 0.05 * Math.sin(a * 5 - t * 1.3));
          const px = x + Math.cos(a) * rr;
          const py = y + Math.sin(a) * rr;
          if (i === 0) ctx.moveTo(px, py);
          else ctx.lineTo(px, py);
        }
        const g = ctx.createRadialGradient(x - r * 0.3, y - r * 0.3, r * 0.05, x, y, r * 1.1);
        g.addColorStop(0, pal.light ? "#ffffff" : rgba("#ffffff", 0.9 * alpha));
        g.addColorStop(0.4, rgba(color, 0.95 * alpha));
        g.addColorStop(1, rgba(color, 0.35 * alpha));
        ctx.fillStyle = g;
        ctx.shadowColor = color;
        ctx.shadowBlur = 12 * pal.glow * alpha;
        ctx.fill();
        ctx.restore();
      };
      const label = (x: number, y: number, name: string, sub: string, subColor: string, lost = false) => {
        ctx.save();
        ctx.textAlign = "center";
        ctx.textBaseline = "middle";
        ctx.font = `600 ${Math.max(10, 11.5 * unit)}px ui-sans-serif, system-ui, sans-serif`;
        ctx.fillStyle = lost ? pal.dim : pal.text;
        ctx.globalAlpha = lost ? 0.6 : 0.92;
        ctx.fillText(name, x, y);
        if (sub) {
          ctx.font = `${Math.max(9, 10 * unit)}px ui-monospace, SFMono-Regular, monospace`;
          ctx.fillStyle = subColor;
          ctx.fillText(sub, x, y + 13 * unit);
        }
        ctx.restore();
      };
      const subOf = (n: string) => {
        const w = info(n);
        const task = busy(n);
        return w?.state === "lost" ? "lost" : task ? doing(task) : w?.engine ? w.engine.replace(/^cli · /, "") : "";
      };
      const nameOf = (n: string) => (n === SELF ? me || "worker" : short(n));
      const queenName = rl === "worker" ? "queen" : me || "queen";
      const working = ts.some((k) => k.state === "running");

      if (lk === "orbit") {
        // ---- a star, and planets on tilted rings ----
        const tilt = 0.4;
        const rings: string[][] = [];
        for (const n of [...names].sort()) {
          let k = 0;
          while ((rings[k] ??= []).length >= 4 + k * 2) k++;
          rings[k].push(n);
        }
        const K = Math.max(1, rings.length);
        // Room below the lowest planet for its two lines of label.
        const rxMax = Math.min(W * 0.45, (H * 0.5 - 34 * unit) / tilt);
        const star = Math.max(10, Math.min(26, rxMax * 0.09));
        const rxMin = Math.max(star * 3.2, rxMax * (K === 1 ? 0.72 : 0.42));
        const radius = (k: number) => (K === 1 ? rxMin : rxMin + ((rxMax - rxMin) * k) / (K - 1));
        pos.set(QUEEN, { x: cx, y: cy, z: 0, r: star });
        rings.forEach((ring, k) => {
          const rx = radius(k);
          const speed = 0.16 / Math.pow(1 + k * 0.8, 1.5);
          ring.forEach((n, i) => {
            const a = (i / ring.length) * Math.PI * 2 + k * 0.7 + t * speed;
            const g = ease(growth(n));
            // A new planet spirals in from outside its orbit.
            const out = 1 + (1 - g) * 0.9;
            const z = Math.sin(a);
            const pr = Math.max(5, Math.min(14, star * 0.46)) * (1 + z * 0.16);
            pos.set(n, { x: cx + Math.cos(a) * rx * out, y: cy + Math.sin(a) * rx * tilt * out, z, r: pr * (0.3 + 0.7 * g) });
          });
        });
        for (const n of names) paths.set(n, bowed(pos.get(QUEEN)!, pos.get(n)!, 0.18));

        if (!pal.light) {
          for (const s of stars) {
            ctx.globalAlpha = 0.16 + 0.22 * (0.5 + 0.5 * Math.sin(t * 1.3 + s.tw));
            ctx.fillStyle = pal.text;
            ctx.fillRect(s.x * W, s.y * H, s.s, s.s);
          }
          ctx.globalAlpha = 1;
        }
        rings.forEach((ring, k) => {
          const rx = radius(k);
          const active = ring.some((n) => busy(n));
          const lost = ring.every((n) => info(n)?.state === "lost");
          ctx.save();
          ctx.strokeStyle = active ? pal.accent : pal.dim;
          ctx.globalAlpha = active ? 0.42 : lost ? 0.08 : 0.2;
          ctx.lineWidth = 1;
          ctx.setLineDash(lost ? [2, 6] : []);
          ctx.beginPath();
          ctx.ellipse(cx, cy, rx, rx * tilt, 0, 0, Math.PI * 2);
          ctx.stroke();
          ctx.restore();
        });

        const drawStar = () => {
          const q = pos.get(QUEEN)!;
          const beat = working ? 1 + 0.08 * Math.sin(t * 5) : 1 + 0.03 * Math.sin(t * 1.4);
          aura(q.x, q.y, q.r * 4.2 * beat, pal.accent, pal.light ? 0.3 : 0.5);
          // Rays, turning slowly.
          ctx.save();
          ctx.translate(q.x, q.y);
          ctx.rotate(t * 0.08);
          ctx.strokeStyle = rgba(pal.accent, pal.light ? 0.18 : 0.22);
          ctx.lineWidth = 1;
          for (let i = 0; i < 12; i++) {
            const a = (i / 12) * Math.PI * 2;
            const len = q.r * (1.9 + 0.5 * Math.sin(t * 1.7 + i));
            ctx.beginPath();
            ctx.moveTo(Math.cos(a) * q.r * 1.3, Math.sin(a) * q.r * 1.3);
            ctx.lineTo(Math.cos(a) * len * 1.5, Math.sin(a) * len * 1.5);
            ctx.stroke();
          }
          ctx.restore();
          const core = ctx.createRadialGradient(q.x - q.r * 0.3, q.y - q.r * 0.3, 0, q.x, q.y, q.r * beat);
          core.addColorStop(0, pal.light ? "#fff8ee" : "#ffffff");
          core.addColorStop(0.45, pal.accent);
          core.addColorStop(1, rgba(pal.accent, 0.85));
          ctx.save();
          ctx.shadowColor = pal.accent;
          ctx.shadowBlur = 30 * pal.glow;
          ctx.fillStyle = core;
          ctx.beginPath();
          ctx.arc(q.x, q.y, q.r * beat, 0, Math.PI * 2);
          ctx.fill();
          ctx.restore();
          label(q.x, q.y + q.r * 2 + 8 * unit, queenName, "", pal.dim);
        };

        const drawPlanet = (n: string) => {
          const p = pos.get(n)!;
          const lost = info(n)?.state === "lost";
          const task = busy(n);
          const base = lost ? pal.dim : task ? pal.accent : pal.green;
          if (task) aura(p.x, p.y, p.r * 3.4, pal.accent, pal.light ? 0.3 : 0.45);
          // Lit from the star: the bright side faces the middle.
          const d = Math.max(1, Math.hypot(cx - p.x, cy - p.y));
          const lx = p.x + ((cx - p.x) / d) * p.r * 0.5;
          const ly = p.y + ((cy - p.y) / d) * p.r * 0.5;
          const body = ctx.createRadialGradient(lx, ly, p.r * 0.1, p.x, p.y, p.r);
          body.addColorStop(0, pal.light ? "#ffffff" : rgba(base, 1));
          body.addColorStop(0.5, rgba(base, lost ? 0.35 : 0.9));
          body.addColorStop(1, rgba(base, lost ? 0.12 : pal.light ? 0.45 : 0.28));
          ctx.save();
          ctx.fillStyle = body;
          ctx.beginPath();
          ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
          ctx.fill();
          if (lost) {
            ctx.strokeStyle = rgba(pal.dim, 0.5);
            ctx.setLineDash([2, 3]);
            ctx.stroke();
          }
          ctx.restore();
          if (task) {
            // A ring round a planet at work, like the ones round a gas giant,
            // and a moon for every tool it has called so far, up to five.
            ctx.save();
            ctx.strokeStyle = rgba(pal.accent, 0.8);
            ctx.lineWidth = 1.2;
            ctx.beginPath();
            ctx.ellipse(p.x, p.y, p.r * 1.9, p.r * 0.55, -0.35, 0, Math.PI * 2);
            ctx.stroke();
            ctx.fillStyle = pal.amber;
            for (let m = 0; m < Math.min(5, task.tools); m++) {
              const a = t * (1.6 + m * 0.3) + m * 1.3;
              ctx.beginPath();
              ctx.arc(p.x + Math.cos(a) * p.r * 2.4, p.y + Math.sin(a) * p.r * 0.9, 1.6 * unit, 0, Math.PI * 2);
              ctx.fill();
            }
            ctx.restore();
          }
          label(p.x, p.y + p.r + 12 * unit, nameOf(n), subOf(n), task ? pal.accent : pal.dim, lost);
        };

        // Far planets, then the star, then near ones: depth by drawing order.
        const byDepth = [...names].sort((a, b) => pos.get(a)!.z - pos.get(b)!.z);
        for (const n of byDepth) if (pos.get(n)!.z < 0) drawPlanet(n);
        drawStar();
        for (const n of byDepth) if (pos.get(n)!.z >= 0) drawPlanet(n);
      } else if (lk === "tree") {
        // ---- a root, a trunk, branches, and a blossom on each twig ----
        const root = Math.max(9, Math.min(18, Math.min(W, H) * 0.04));
        const q = { x: cx, y: H - root - 30 * unit, z: 0, r: root };
        pos.set(QUEEN, q);
        const fork = { x: cx, y: H * 0.58 };
        // The canopy: an arch over the fork, left to right in name order, every
        // other blossom set a little in so neighbours do not touch.
        const order = [...names].sort();
        const n = order.length;
        const rx = W * 0.42;
        const ry = Math.min(H * 0.46, fork.y - 34 * unit);
        const sway = (seed: string, amp: number) => Math.sin(t * 0.7 + hash01(seed, 40) * 6.28) * amp * unit;
        order.forEach((name, i) => {
          const a = Math.PI * (1.08 + (0.84 * (i + 0.5)) / Math.max(1, n));
          const d = n > 5 && i % 2 ? 0.78 : 1;
          pos.set(name, {
            x: fork.x + Math.cos(a) * rx * d + sway(name, 3),
            y: fork.y + Math.sin(a) * ry * d + sway(name + "y", 1.5),
            z: 0,
            r: Math.max(5, root * 0.55),
          });
        });
        const trunk = bowed(q, fork, 0.04 + Math.sin(t * 0.5) * 0.01, 16);
        // Branches: up to three neighbours share one, which splits into a twig
        // for each.
        const limbs: { pts: Pt[]; names: string[] }[] = [];
        const twigs: { pts: Pt[]; name: string }[] = [];
        for (let i = 0; i < n; i += 3) {
          const group = order.slice(i, i + 3);
          const ps = group.map((m) => pos.get(m)!);
          const mx = ps.reduce((s, p) => s + p.x, 0) / ps.length;
          const my = ps.reduce((s, p) => s + p.y, 0) / ps.length;
          const j = { x: fork.x + (mx - fork.x) * 0.52 + sway(group[0] + "j", 2), y: fork.y + (my - fork.y) * 0.52 };
          const side = mx < cx ? 1 : -1;
          const limb = bowed(fork, j, 0.12 * side, 16);
          limbs.push({ pts: limb, names: group });
          for (const m of group) {
            const tw = bowed(j, pos.get(m)!, 0.1 * side, 12);
            twigs.push({ pts: tw, name: m });
            paths.set(m, [...trunk, ...limb.slice(1), ...tw.slice(1)]);
          }
        }

        const bark = pal.light ? "#6b5a48" : pal.green;
        const drawWood = (pts: Pt[], width: number, active: boolean, lost: boolean, upTo: number) => {
          ctx.save();
          ctx.lineCap = "round";
          ctx.lineJoin = "round";
          ctx.lineWidth = width;
          if (active) {
            ctx.strokeStyle = rgba(pal.accent, 0.9);
            ctx.shadowColor = pal.accent;
            ctx.shadowBlur = 12 * pal.glow;
          } else {
            ctx.strokeStyle = rgba(lost ? pal.dim : bark, lost ? 0.2 : pal.light ? 0.55 : 0.42);
            if (lost) ctx.setLineDash([3, 5]);
          }
          strokeLine(ctx, pts, upTo);
          ctx.restore();
        };

        // Roots, spreading under the queen: decoration, so the tree stands on
        // something.
        ctx.save();
        ctx.strokeStyle = rgba(bark, pal.light ? 0.3 : 0.25);
        ctx.lineCap = "round";
        for (let k = 0; k < 5; k++) {
          const a = Math.PI * (0.12 + k * 0.19);
          ctx.lineWidth = (2.6 - Math.abs(k - 2) * 0.6) * unit;
          ctx.beginPath();
          ctx.moveTo(q.x, q.y);
          ctx.quadraticCurveTo(q.x + Math.cos(a) * 18 * unit, q.y + 10 * unit, q.x + Math.cos(a) * 46 * unit, q.y + Math.sin(a) * 14 * unit + 12 * unit);
          ctx.stroke();
        }
        ctx.restore();

        drawWood(trunk, 7 * unit, working, false, 1);
        for (const l of limbs) {
          const g = Math.max(...l.names.map((m) => growth(m)));
          drawWood(l.pts, 3.6 * unit, l.names.some((m) => busy(m)), l.names.every((m) => info(m)?.state === "lost"), ease(Math.min(1, g * 1.6)));
        }
        for (const tw of twigs) {
          drawWood(tw.pts, 1.8 * unit, !!busy(tw.name), info(tw.name)?.state === "lost", ease(Math.max(0, growth(tw.name) * 1.6 - 0.6)));
        }

        // Leaves along each twig, turning a little in the wind.
        ctx.save();
        for (const tw of twigs) {
          const g = growth(tw.name);
          if (g < 0.6) continue;
          const lost = info(tw.name)?.state === "lost";
          ctx.fillStyle = rgba(lost ? pal.dim : pal.green, lost ? 0.12 : pal.light ? 0.35 : 0.3);
          for (let k = 0; k < 3; k++) {
            const at = alongLine(tw.pts, 0.3 + k * 0.22);
            const a = hash01(tw.name, 50 + k) * 6.28 + Math.sin(t * 1.1 + k) * 0.25;
            ctx.beginPath();
            ctx.ellipse(at.x + Math.cos(a) * 6 * unit, at.y + Math.sin(a) * 6 * unit, 6 * unit, 2.6 * unit, a, 0, Math.PI * 2);
            ctx.fill();
          }
        }
        ctx.restore();

        // Sap: light climbing every busy branch.
        for (const m of names) {
          const pts = paths.get(m);
          if (!busy(m) || !pts) continue;
          for (let k = 0; k < 4; k++) {
            const p = alongLine(pts, (t * 0.3 + k / 4) % 1);
            glowDot(p.x, p.y, 2.2 * unit, pal.accent, 0.85);
          }
        }

        // A worker asking another: an arc of light between the two blossoms.
        for (const k of ts) {
          if (k.state !== "running" || k.dir !== "peer" || !k.from) continue;
          const a = pos.get(k.from);
          const b = pos.get(k.worker);
          if (!a || !b) continue;
          ctx.save();
          ctx.strokeStyle = rgba(pal.amber, 0.85);
          ctx.lineWidth = 1.4 * unit;
          ctx.shadowColor = pal.amber;
          ctx.shadowBlur = 10 * pal.glow;
          ctx.setLineDash([4 * unit, 5 * unit]);
          ctx.lineDashOffset = -t * 30;
          strokeLine(ctx, bowed(a, b, -0.25), ease(Math.min(1, (Date.now() - Date.parse(k.started_at)) / 1500)));
          ctx.restore();
        }

        // The root.
        const beat = working ? 1 + 0.1 * Math.sin(t * 4) : 1 + 0.04 * Math.sin(t * 1.2);
        aura(q.x, q.y, q.r * 3.4 * beat, pal.accent, pal.light ? 0.3 : 0.5);
        blob(q.x, q.y, q.r * beat, pal.accent, "queen", 1);
        label(q.x + q.r + 30 * unit, q.y, queenName, "", pal.dim);

        // Blossoms, opening once their twig has reached them.
        for (const m of names) {
          const p = pos.get(m)!;
          const g = growth(m);
          if (g < 0.55) continue;
          const lost = info(m)?.state === "lost";
          const task = busy(m);
          const tone = lost ? pal.dim : task ? pal.accent : pal.green;
          if (task) aura(p.x, p.y, p.r * 3.4, pal.accent, pal.light ? 0.28 : 0.42);
          const open = ease(Math.min(1, (g - 0.55) / 0.45));
          // Five petals, then the heart.
          ctx.save();
          ctx.fillStyle = rgba(tone, lost ? 0.15 : 0.35);
          for (let k = 0; k < 5; k++) {
            const a = (k / 5) * Math.PI * 2 + t * (task ? 0.8 : 0.1) + hash01(m, 60) * 6.28;
            ctx.beginPath();
            ctx.ellipse(p.x + Math.cos(a) * p.r * 1.05 * open, p.y + Math.sin(a) * p.r * 1.05 * open, p.r * 0.75 * open, p.r * 0.45 * open, a, 0, Math.PI * 2);
            ctx.fill();
          }
          ctx.restore();
          blob(p.x, p.y, p.r * 0.8 * open * (task ? 1.15 + 0.1 * Math.sin(t * 5) : 1), tone, m, lost ? 0.4 : 1);
          label(p.x, p.y + p.r * 1.9 + 8 * unit, nameOf(m), subOf(m), task ? pal.accent : pal.dim, lost);
        }
      } else {
        // ---- a root, trunks, and a fork to each worker ----
        const rx = W * 0.4;
        const ry = H * 0.36;
        const root = Math.max(9, Math.min(20, Math.min(W, H) * 0.045));
        pos.set(QUEEN, { x: cx, y: cy, z: 0, r: root });
        const order = [...names].sort();
        order.forEach((n, i) => {
          const spread = 2.2 / Math.max(3, order.length);
          const a = (i / Math.max(1, order.length)) * Math.PI * 2 - Math.PI / 2 + (hash01(n, 1) - 0.5) * spread;
          const d = 0.8 + hash01(n, 2) * 0.2;
          pos.set(n, { x: cx + Math.cos(a) * rx * d, y: cy + Math.sin(a) * ry * d, z: 0, r: Math.max(5, root * 0.55) });
        });
        // A filament: the straight line pushed sideways by a couple of slow waves,
        // the same on every frame but for a little drift.
        const filament = (a: Pt, b: Pt, seed: string, amp: number, n = 20): Pt[] => {
          const out: Pt[] = [];
          const dx = b.x - a.x;
          const dy = b.y - a.y;
          const len = Math.hypot(dx, dy) || 1;
          const nx = -dy / len;
          const ny = dx / len;
          const f1 = 1.5 + hash01(seed, 3) * 2;
          const f2 = 3 + hash01(seed, 4) * 3;
          const p1 = hash01(seed, 5) * 6.28;
          for (let i = 0; i <= n; i++) {
            const u = i / n;
            const w = (Math.sin(u * f1 * Math.PI + p1 + t * 0.35) * 0.7 + Math.sin(u * f2 * Math.PI + p1 * 2 + t * 0.5) * 0.3) * amp * Math.sin(u * Math.PI);
            out.push({ x: a.x + dx * u + nx * w, y: a.y + dy * u + ny * w });
          }
          return out;
        };
        // Trunks: consecutive workers, up to three to a trunk, forking from a
        // junction partway out.
        const q = pos.get(QUEEN)!;
        const trunks: { pts: Pt[]; names: string[] }[] = [];
        const branches: { pts: Pt[]; name: string }[] = [];
        for (let i = 0; i < order.length; i += 3) {
          const tr = order.slice(i, i + 3);
          const ps = tr.map((n) => pos.get(n)!);
          const mx = ps.reduce((s, p) => s + p.x, 0) / ps.length;
          const my = ps.reduce((s, p) => s + p.y, 0) / ps.length;
          const j = { x: cx + (mx - cx) * 0.46, y: cy + (my - cy) * 0.46 };
          const trunk = filament(q, j, tr.join("|"), Math.hypot(j.x - cx, j.y - cy) * 0.12);
          trunks.push({ pts: trunk, names: tr });
          for (const n of tr) {
            const p = pos.get(n)!;
            const br = filament(j, p, n, Math.hypot(p.x - j.x, p.y - j.y) * 0.14, 16);
            branches.push({ pts: br, name: n });
            paths.set(n, [...trunk, ...br.slice(1)]);
          }
        }

        // Fine hairs off the filaments: faint, so the network looks grown rather
        // than drawn.
        ctx.save();
        ctx.lineWidth = 0.7;
        ctx.strokeStyle = rgba(pal.light ? "#5b4a3a" : pal.dim, pal.light ? 0.16 : 0.22);
        for (const b of branches) {
          const g = growth(b.name);
          for (let h = 0; h < 3; h++) {
            const at = alongLine(b.pts, (0.25 + h * 0.25) * g);
            const a = hash01(b.name, 10 + h) * Math.PI * 2 + Math.sin(t * 0.4 + h) * 0.15;
            const len = (10 + hash01(b.name, 20 + h) * 18) * unit * g;
            ctx.beginPath();
            ctx.moveTo(at.x, at.y);
            ctx.quadraticCurveTo(at.x + Math.cos(a) * len * 0.6, at.y + Math.sin(a) * len * 0.6 + 4, at.x + Math.cos(a + 0.4) * len, at.y + Math.sin(a + 0.4) * len);
            ctx.stroke();
          }
        }
        ctx.restore();

        const drawFilament = (pts: Pt[], width: number, active: boolean, lost: boolean, upTo: number) => {
          ctx.save();
          ctx.lineCap = "round";
          ctx.lineJoin = "round";
          if (active) {
            ctx.strokeStyle = rgba(pal.accent, 0.9);
            ctx.shadowColor = pal.accent;
            ctx.shadowBlur = 12 * pal.glow;
            ctx.lineWidth = width * 1.3;
          } else {
            ctx.strokeStyle = rgba(lost ? pal.dim : pal.light ? "#6b5a48" : pal.green, lost ? 0.18 : pal.light ? 0.4 : 0.34);
            ctx.lineWidth = width;
            if (lost) ctx.setLineDash([3, 5]);
          }
          strokeLine(ctx, pts, upTo);
          ctx.restore();
        };
        for (const tr of trunks) {
          const g = Math.max(...tr.names.map((n) => growth(n)));
          drawFilament(tr.pts, 2.4 * unit, tr.names.some((n) => busy(n)), tr.names.every((n) => info(n)?.state === "lost"), ease(Math.min(1, g * 1.6)));
        }
        for (const b of branches) {
          drawFilament(b.pts, 1.4 * unit, !!busy(b.name), info(b.name)?.state === "lost", ease(Math.max(0, growth(b.name) * 1.6 - 0.6)));
        }

        // Nutrients: light running out along every busy path, for as long as it
        // is busy.
        for (const n of names) {
          const pts = paths.get(n);
          if (!busy(n) || !pts) continue;
          for (let k = 0; k < 4; k++) {
            const p = alongLine(pts, (t * 0.35 + k / 4) % 1);
            glowDot(p.x, p.y, 2.2 * unit, pal.accent, 0.8);
          }
        }

        // A worker asking another: a new thread between them while it is open.
        for (const k of ts) {
          if (k.state !== "running" || k.dir !== "peer" || !k.from) continue;
          const a = pos.get(k.from);
          const b = pos.get(k.worker);
          if (!a || !b) continue;
          ctx.save();
          ctx.strokeStyle = rgba(pal.amber, 0.85);
          ctx.lineWidth = 1.4 * unit;
          ctx.shadowColor = pal.amber;
          ctx.shadowBlur = 10 * pal.glow;
          const since = Math.min(1, (Date.now() - Date.parse(k.started_at)) / 1500);
          strokeLine(ctx, filament(a, b, k.id, Math.hypot(b.x - a.x, b.y - a.y) * 0.18), ease(since));
          ctx.restore();
        }

        // The root.
        const beat = working ? 1 + 0.1 * Math.sin(t * 4) : 1 + 0.04 * Math.sin(t * 1.2);
        aura(q.x, q.y, q.r * 3.6 * beat, pal.accent, pal.light ? 0.3 : 0.5);
        blob(q.x, q.y, q.r * beat, pal.accent, "queen", 1);
        label(q.x, q.y + q.r * 1.9 + 8 * unit, queenName, "", pal.dim);

        // The workers: spores at the tips, appearing once their filament has
        // reached them.
        for (const n of names) {
          const p = pos.get(n)!;
          const g = growth(n);
          if (g < 0.55) continue;
          const lost = info(n)?.state === "lost";
          const task = busy(n);
          const tone = lost ? pal.dim : task ? pal.accent : pal.green;
          if (task) aura(p.x, p.y, p.r * 3.4, pal.accent, pal.light ? 0.28 : 0.42);
          const s = ease(Math.min(1, (g - 0.55) / 0.45));
          blob(p.x, p.y, p.r * s * (task ? 1.15 + 0.1 * Math.sin(t * 5) : 1), tone, n, lost ? 0.4 : 1);
          label(p.x, p.y + p.r + 12 * unit, nameOf(n), subOf(n), task ? pal.accent : pal.dim, lost);
        }
      }

      // ---- the traffic, the same in every look ----
      const route = (from: string, to: string): Pt[] | null => {
        if (from === QUEEN && paths.has(to)) return paths.get(to)!;
        if (to === QUEEN && paths.has(from)) return [...paths.get(from)!].reverse();
        const a = pos.get(from);
        const b = pos.get(to);
        return a && b ? bowed(a, b, lk === "orbit" ? -0.22 : 0.18) : null;
      };
      // A comet in the orbits — a long tail — and a bead of light on a filament.
      const tail = lk === "orbit" ? 10 : 5;
      const gap = lk === "orbit" ? 0.018 : 0.012;
      const head = lk === "orbit" ? 4.2 : 3.4;
      for (const f of fx.current.flights) {
        if (now < f.t0) continue;
        const pts = route(f.from, f.to);
        if (!pts) continue;
        const u = ease((now - f.t0) / f.dur);
        for (let k = tail - 1; k >= 0; k--) {
          const p = alongLine(pts, Math.max(0, u - k * gap));
          glowDot(p.x, p.y, Math.max(0.6, head * unit * f.size * (1 - k / tail)), f.color, f.alpha * (1 - k / tail) * (k === 0 ? 1 : 0.7));
        }
      }
      for (const l of fx.current.landings) {
        const p = pos.get(l.at);
        if (!p || now < l.t0) continue;
        const u = (now - l.t0) / (l.big ? 1100 : 600);
        const rr = p.r * (1.4 + u * (l.big ? 3.5 : 1.6));
        ctx.save();
        ctx.strokeStyle = l.color;
        ctx.globalAlpha = (1 - u) * (l.big ? 0.9 : 0.5);
        ctx.lineWidth = l.big ? 2.4 * (1 - u) + 0.6 : 1.2;
        ctx.shadowColor = l.color;
        ctx.shadowBlur = (l.big ? 16 : 6) * pal.glow;
        ctx.beginPath();
        if (lk === "orbit") ctx.ellipse(p.x, p.y, rr, rr * 0.55, 0, 0, Math.PI * 2);
        else ctx.arc(p.x, p.y, rr, 0, Math.PI * 2);
        ctx.stroke();
        ctx.restore();
      }
      for (const s of fx.current.sparks) {
        const p = pos.get(s.at);
        if (!p || now < s.t0) continue;
        const u = (now - s.t0) / 700;
        for (let k = 0; k < 7; k++) {
          const a = s.a + (k * Math.PI * 2) / 7;
          const d = p.r * 1.2 + u * 26 * unit;
          glowDot(p.x + Math.cos(a) * d, p.y + Math.sin(a) * d * (lk === "orbit" ? 0.6 : 1), 1.6 * unit * (1 - u) + 0.4, pal.amber, 1 - u);
        }
      }
      for (const g of fx.current.tags) {
        const p = pos.get(g.at);
        if (!p) continue;
        const u = (now - g.t0) / 1600;
        ctx.save();
        ctx.globalAlpha = 1 - u * u;
        ctx.fillStyle = g.color;
        ctx.font = `600 ${Math.max(9, 11 * unit)}px ui-monospace, SFMono-Regular, monospace`;
        ctx.textAlign = "left";
        ctx.textBaseline = "middle";
        ctx.fillText(`⚙ ${g.text}`, p.x + p.r + 8 * unit, p.y - 4 - u * 14 * unit);
        ctx.restore();
      }
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
    if (still) {
      const iv = window.setInterval(() => frame(performance.now()), 1000);
      frame(performance.now());
      return () => {
        window.clearInterval(iv);
        ro.disconnect();
        document.removeEventListener("visibilitychange", visible);
      };
    }
    raf = requestAnimationFrame(loop);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      document.removeEventListener("visibilitychange", visible);
    };
  }, []);

  return (
    <div className="relative h-full min-h-[180px] w-full overflow-hidden" ref={box}>
      <canvas ref={canvas} className="absolute left-0 top-0" />
    </div>
  );
});

export default HiveStage;
