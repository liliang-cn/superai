import React, { useEffect, useRef } from "react";

// The hive, drawn while it works.
//
// One canvas, redrawn every frame from two things: what the panel knows (who is
// in the hive, which orders are running) and a handful of short-lived effects
// spawned when that changes. The effects are the point. A row that turns from
// "running" to "done" is an update; a packet that leaves the queen, a worker
// that spins while it thinks and throws a spark for every tool it calls, and a
// result that flies home and bursts, is something you can watch from across the
// room and know whether the hive is busy.
//
// Nothing here is React state. The frame loop reads refs, so an order arriving
// costs one push onto an array rather than a re-render per animation frame.

export interface StageTask {
  id: string;
  worker: string;
  dir: "out" | "in" | "peer";
  /** Who asked, when it was a worker and not the queen. */
  from?: string;
  state: "running" | "done" | "failed" | "cancelled";
  phase?: string;
  tool?: string;
  tools: number;
  started_at: string;
}

export interface StageWorker {
  name: string;
  state: "live" | "lost";
}

interface Props {
  role: "" | "queen" | "worker";
  self: string;
  workers: StageWorker[];
  tasks: StageTask[];
  /** False until the first batch has loaded. That batch is history, and is not
   *  replayed as if every old order had just been given. */
  ready: boolean;
}

type Pt = { x: number; y: number };
type Packet = { from: string; to: string; t0: number; dur: number; color: string };
type Burst = { at: string; t0: number; color: string };
type Spark = { at: string; t0: number; a: number };

const QUEEN = "\u0000queen";
const SELF = "\u0000self";

const short = (n: string) => n.replace(/^superai-/, "");

function hexPath(ctx: CanvasRenderingContext2D, c: Pt, r: number, rot = 0) {
  ctx.beginPath();
  for (let i = 0; i < 6; i++) {
    const a = rot + (Math.PI / 3) * i - Math.PI / 6;
    const x = c.x + r * Math.cos(a);
    const y = c.y + r * Math.sin(a);
    if (i === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  }
  ctx.closePath();
}

const ease = (t: number) => (t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2);

function palette() {
  const css = getComputedStyle(document.documentElement);
  const v = (n: string, d: string) => css.getPropertyValue(n).trim() || d;
  // Everything comes from the theme's own variables, so a finish or a switch to
  // light changes the stage with the rest of the page. The stage used to be
  // painted in fixed dark colours, which on a light page was a black box with
  // pale grid lines and orange text that could not be read on it.
  const light = document.documentElement.dataset.theme === "light";
  return {
    light,
    accent: v("--accent", "#d2793f"),
    green: v("--green", "#5ec48f"),
    red: v("--red", "#ef6a76"),
    amber: v("--amber", "#e0a64f"),
    dim: v("--text-2", "#8f887c"),
    text: v("--text-0", "#ece7dd"),
    border: v("--border", "#2e2925"),
    panel: v("--bg-1", "#1a1714"),
    // Halos read as light on a dark ground and as smudges on a light one, so
    // they are scaled down there.
    glow: light ? 0.35 : 1,
  };
}

export default function HiveStage({ role, self, workers, tasks, ready }: Props) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const box = useRef<HTMLDivElement>(null);

  // What the frame loop reads. Updated by the effects below, never by render.
  const live = useRef({ role, self, workers, tasks });
  live.current = { role, self, workers, tasks };
  const packets = useRef<Packet[]>([]);
  const bursts = useRef<Burst[]>([]);
  const sparks = useRef<Spark[]>([]);
  // What has been animated already, so a re-render of the same task is not a
  // second packet. `null` until the first batch, which is history and is not
  // replayed.
  const seen = useRef<Map<string, { state: string; tools: number }> | null>(null);

  // Turn changes into effects.
  useEffect(() => {
    if (!ready) return;
    const now = performance.now();
    const target = (t: StageTask) => (live.current.role === "worker" ? SELF : t.worker);
    const origin = (t: StageTask) => (t.dir === "peer" && t.from ? t.from : QUEEN);
    const known = seen.current;
    if (known === null) {
      seen.current = new Map(tasks.map((t) => [t.id, { state: t.state, tools: t.tools }]));
      return;
    }
    const p = palette();
    for (const t of tasks) {
      // A worker's own view has no peers on it to draw.
      if (live.current.role === "worker" && t.dir === "peer") {
        known.set(t.id, { state: t.state, tools: t.tools });
        continue;
      }
      const before = known.get(t.id);
      if (!before) {
        // A new order: a packet leaves the queen.
        if (live.current.role === "queen" || live.current.role === "worker") {
          packets.current.push({ from: origin(t), to: target(t), t0: now, dur: 700, color: p.accent });
        }
      } else {
        for (let i = before.tools; i < t.tools; i++) {
          sparks.current.push({ at: target(t), t0: now + i * 90, a: Math.random() * Math.PI * 2 });
        }
        if (before.state === "running" && t.state !== "running") {
          const color = t.state === "done" ? p.green : t.state === "failed" ? p.red : p.amber;
          packets.current.push({ from: target(t), to: origin(t), t0: now, dur: 700, color });
          bursts.current.push({ at: target(t), t0: now + 650, color });
        }
      }
      known.set(t.id, { state: t.state, tools: t.tools });
    }
  }, [tasks, ready]);

  // The frame loop.
  useEffect(() => {
    const cv = canvas.current;
    const host = box.current;
    if (!cv || !host) return;
    const ctx = cv.getContext("2d");
    if (!ctx) return;

    let W = 0;
    let H = 0;
    let raf = 0;
    let pal = palette();
    let palAt = 0;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const resize = () => {
      const r = host.getBoundingClientRect();
      const dpr = Math.min(window.devicePixelRatio || 1, 2);
      W = Math.max(280, Math.floor(r.width));
      H = Math.max(240, Math.floor(r.height));
      cv.width = W * dpr;
      cv.height = H * dpr;
      cv.style.width = `${W}px`;
      cv.style.height = `${H}px`;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };
    const ro = new ResizeObserver(resize);
    ro.observe(host);
    resize();

    // Where everything sits. Recomputed per frame from the size, so a resize
    // needs no bookkeeping.
    const layout = (): Map<string, Pt> => {
      const m = new Map<string, Pt>();
      const { role: rl, workers: ws } = live.current;
      const cx = W / 2;
      const cy = H / 2;
      if (rl === "queen") {
        m.set(QUEEN, { x: cx, y: cy });
        const n = Math.max(ws.length, 1);
        // The bottom node's label hangs 55px under it, so the vertical radius
        // leaves that much, plus the node itself, inside the frame. Sizing it
        // as a share of the height instead clipped the last label at ten
        // workers and stacked two neighbours on top of each other.
        const rx = Math.min(W * 0.38, 480);
        const ry = Math.max(80, Math.min(H / 2 - 78, 230));
        ws.forEach((w, i) => {
          const a = -Math.PI / 2 + (Math.PI * 2 * i) / n;
          m.set(w.name, { x: cx + rx * Math.cos(a), y: cy + ry * Math.sin(a) });
        });
      } else if (rl === "worker") {
        m.set(QUEEN, { x: cx, y: H * 0.18 });
        m.set(SELF, { x: cx, y: H * 0.62 });
      } else {
        m.set(SELF, { x: cx, y: cy });
      }
      return m;
    };

    // The honeycomb behind everything, drawn once per size into its own canvas.
    let grid: HTMLCanvasElement | null = null;
    let gridKey = "";
    const drawGrid = () => {
      const key = `${W}x${H}${pal.border}${pal.light}`;
      if (grid && key === gridKey) return grid;
      gridKey = key;
      grid = document.createElement("canvas");
      grid.width = W;
      grid.height = H;
      const g = grid.getContext("2d")!;
      g.strokeStyle = pal.light ? "rgba(28,25,23,0.10)" : pal.border;
      g.globalAlpha = pal.light ? 1 : 0.55;
      g.lineWidth = 1;
      const r = 26;
      const dx = r * Math.sqrt(3);
      for (let row = -1, y = 0; y < H + r * 2; row++, y += r * 1.5) {
        for (let x = (row % 2 ? dx / 2 : 0) - dx; x < W + dx; x += dx) {
          hexPath(g, { x, y }, r - 1, 0);
          g.stroke();
        }
      }
      return grid;
    };

    const nodeState = (id: string, now: number) => {
      const t = live.current.tasks.find(
        (k) =>
          k.state === "running" &&
          (id === SELF ? live.current.role === "worker" && k.dir === "in" : k.worker === id && (k.dir === "out" || k.dir === "peer")),
      );
      return t ?? null;
    };

    const frame = (now: number) => {
      if (now - palAt > 2000) {
        pal = palette();
        palAt = now;
      }
      ctx.clearRect(0, 0, W, H);
      ctx.drawImage(drawGrid(), 0, 0);
      const pos = layout();
      const { role: rl, workers: ws, self: me } = live.current;
      const breathe = still ? 0 : Math.sin(now / 900);

      // Edges.
      const edgeTo = rl === "queen" ? ws.map((w) => w.name) : rl === "worker" ? [SELF] : [];
      for (const id of edgeTo) {
        const a = pos.get(QUEEN);
        const b = pos.get(id);
        if (!a || !b) continue;
        const busy = !!nodeState(id, now);
        const lost = rl === "queen" && ws.find((w) => w.name === id)?.state === "lost";
        ctx.save();
        ctx.beginPath();
        ctx.moveTo(a.x, a.y);
        ctx.lineTo(b.x, b.y);
        ctx.strokeStyle = busy ? pal.accent : pal.dim;
        ctx.globalAlpha = busy ? 0.75 : lost ? 0.08 : 0.22;
        ctx.lineWidth = busy ? 1.6 : 1;
        ctx.setLineDash(busy ? [6, 8] : lost ? [2, 6] : []);
        ctx.lineDashOffset = busy && !still ? -now / 25 : 0;
        ctx.stroke();
        ctx.restore();
      }

      // Workers talking to each other: an arc between the two, bowed toward the
      // centre so it does not run through the nodes between them.
      if (rl === "queen") {
        const c = pos.get(QUEEN)!;
        for (const t of live.current.tasks) {
          if (t.state !== "running" || t.dir !== "peer" || !t.from) continue;
          const a = pos.get(t.from);
          const b = pos.get(t.worker);
          if (!a || !b) continue;
          ctx.save();
          ctx.beginPath();
          ctx.moveTo(a.x, a.y);
          ctx.quadraticCurveTo((a.x + b.x) / 2 * 0.55 + c.x * 0.45, (a.y + b.y) / 2 * 0.55 + c.y * 0.45, b.x, b.y);
          ctx.strokeStyle = pal.amber;
          ctx.lineWidth = 1.8;
          ctx.globalAlpha = 0.85;
          ctx.shadowColor = pal.amber;
          ctx.shadowBlur = 10 * pal.glow;
          ctx.setLineDash([5, 7]);
          ctx.lineDashOffset = still ? 0 : -now / 22;
          ctx.stroke();
          ctx.restore();
        }
      }

      // Nodes.
      const drawNode = (id: string, label: string, r0: number, kind: "queen" | "worker", lost: boolean) => {
        const c = pos.get(id);
        if (!c) return;
        const task = nodeState(id, now);
        const r = r0 * (1 + 0.035 * breathe);
        const tone = lost ? pal.dim : kind === "queen" ? pal.accent : task ? pal.accent : pal.green;
        ctx.save();
        // Glow.
        ctx.shadowColor = tone;
        ctx.shadowBlur = (lost ? 0 : task ? 26 : 12) * pal.glow;
        hexPath(ctx, c, r);
        ctx.fillStyle = pal.light ? pal.panel : "rgba(0,0,0,0.55)";
        ctx.fill();
        if (task && pal.light) {
          // A working cell is tinted, since a halo does not show on paper.
          ctx.shadowBlur = 0;
          ctx.globalAlpha = 0.12;
          ctx.fillStyle = pal.accent;
          ctx.fill();
          ctx.globalAlpha = 1;
          ctx.shadowBlur = 8;
        }
        ctx.lineWidth = task ? 2.4 : 1.6;
        ctx.strokeStyle = tone;
        ctx.globalAlpha = lost ? 0.45 : 1;
        if (lost) ctx.setLineDash([3, 5]);
        ctx.stroke();
        ctx.restore();

        // A worker at work: a ring turning around it, one arc per tool call
        // (capped), and the phase written under it.
        if (task && !still) {
          ctx.save();
          ctx.strokeStyle = pal.accent;
          ctx.lineWidth = 2;
          ctx.globalAlpha = 0.9;
          ctx.setLineDash([10, 9]);
          ctx.lineDashOffset = -now / 35;
          hexPath(ctx, c, r + 11, now / 1600);
          ctx.stroke();
          ctx.restore();
          ctx.save();
          ctx.strokeStyle = pal.accent;
          ctx.globalAlpha = 0.55;
          ctx.lineWidth = 3;
          const arcs = Math.min(task.tools, 12);
          for (let i = 0; i < arcs; i++) {
            const a0 = (Math.PI * 2 * i) / 12 - now / 900;
            ctx.beginPath();
            ctx.arc(c.x, c.y, r + 20, a0, a0 + 0.35);
            ctx.stroke();
          }
          ctx.restore();
        }

        // A dot in the middle: the phase, as light. Thinking pulses, a tool
        // call is a sharp blink, writing is steady.
        ctx.save();
        const ph = task?.phase;
        const pulse = ph === "thinking" ? 0.5 + 0.5 * Math.sin(now / 160) : ph === "tool" ? (Math.sin(now / 70) > 0 ? 1 : 0.35) : 1;
        ctx.fillStyle = tone;
        ctx.globalAlpha = lost ? 0.3 : task ? 0.35 + 0.65 * pulse : 0.7;
        ctx.beginPath();
        ctx.arc(c.x, c.y, kind === "queen" ? 6 : 4.5, 0, Math.PI * 2);
        ctx.fill();
        ctx.restore();

        // Labels.
        ctx.save();
        ctx.textAlign = "center";
        ctx.font = "600 12px ui-sans-serif, system-ui, sans-serif";
        ctx.fillStyle = lost ? pal.dim : pal.text;
        ctx.fillText(short(label), c.x, c.y + r + (task ? 34 : 20));
        ctx.font = "11px ui-monospace, SFMono-Regular, monospace";
        ctx.fillStyle = pal.dim;
        let sub = lost ? "lost" : kind === "queen" ? "queen" : "idle";
        if (task) {
          const secs = Math.max(0, Math.round((Date.now() - Date.parse(task.started_at)) / 1000));
          sub =
            task.phase === "tool" ? `⚙ ${task.tool || "tool"}` : task.phase === "writing" ? "writing…" : "thinking…";
          sub += `  ${secs}s`;
          ctx.fillStyle = pal.accent;
        }
        ctx.fillText(sub, c.x, c.y + r + (task ? 50 : 35));
        ctx.restore();
      };

      if (rl === "queen") {
        // Smaller cells as the hive fills, so a crowd stays a honeycomb and not a
        // pile.
        const wr = ws.length > 14 ? 15 : ws.length > 8 ? 20 : 26;
        ws.forEach((w) => drawNode(w.name, w.name, wr, "worker", w.state === "lost"));
        drawNode(QUEEN, me || "queen", 36, "queen", false);
      } else if (rl === "worker") {
        drawNode(QUEEN, "queen", 30, "queen", false);
        drawNode(SELF, me || "worker", 34, "worker", false);
      } else {
        drawNode(SELF, me || "this instance", 34, "worker", false);
      }

      // Packets: an order out, a result home.
      packets.current = packets.current.filter((p) => now - p.t0 < p.dur);
      for (const p of packets.current) {
        const a = pos.get(p.from);
        const b = pos.get(p.to);
        if (!a || !b || now < p.t0) continue;
        const t = (now - p.t0) / p.dur;
        for (let k = 0; k < 6; k++) {
          const tt = Math.max(0, ease(t) - k * 0.035);
          ctx.save();
          ctx.globalAlpha = (1 - k / 6) * 0.9;
          ctx.fillStyle = p.color;
          ctx.shadowColor = p.color;
          ctx.shadowBlur = 14 * pal.glow;
          ctx.beginPath();
          ctx.arc(a.x + (b.x - a.x) * tt, a.y + (b.y - a.y) * tt, 4.5 - k * 0.55, 0, Math.PI * 2);
          ctx.fill();
          ctx.restore();
        }
      }

      // Bursts where a result lands.
      bursts.current = bursts.current.filter((b) => now - b.t0 < 900);
      for (const b of bursts.current) {
        const c = pos.get(b.at);
        if (!c || now < b.t0) continue;
        const t = (now - b.t0) / 900;
        ctx.save();
        ctx.strokeStyle = b.color;
        ctx.globalAlpha = 1 - t;
        ctx.lineWidth = 3 * (1 - t) + 1;
        ctx.shadowColor = b.color;
        ctx.shadowBlur = 18 * pal.glow;
        hexPath(ctx, c, 30 + t * 60, t);
        ctx.stroke();
        ctx.restore();
      }

      // Sparks: one per tool call, thrown outward.
      sparks.current = sparks.current.filter((s) => now - s.t0 < 600);
      for (const s of sparks.current) {
        const c = pos.get(s.at);
        if (!c || now < s.t0) continue;
        const t = (now - s.t0) / 600;
        for (let k = 0; k < 5; k++) {
          const a = s.a + (k * Math.PI * 2) / 5;
          const d = 34 + t * 40;
          ctx.save();
          ctx.globalAlpha = 1 - t;
          ctx.fillStyle = pal.amber;
          ctx.beginPath();
          ctx.arc(c.x + Math.cos(a) * d, c.y + Math.sin(a) * d, 2.2 * (1 - t) + 0.4, 0, Math.PI * 2);
          ctx.fill();
          ctx.restore();
        }
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
      // Reduced motion: no loop, one honest frame a second.
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
    <div className="hive-stage" ref={box}>
      <canvas ref={canvas} />
    </div>
  );
}
