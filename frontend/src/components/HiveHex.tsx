import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { palette } from "./hivePalette";
import type { StageHandle, StagePulse, StageProps, StageTask } from "./hiveFx";

// The hive, drawn while it works — the hexagon look.
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

type Props = StageProps;

type Pt = { x: number; y: number };
type Packet = { from: string; to: string; t0: number; dur: number; color: string; size?: number; alpha?: number };
type Label = { at: string; text: string; t0: number; color: string };
type Ripple = { at: string; t0: number; color: string };
type Burst = { at: string; t0: number; color: string };
type Spark = { at: string; t0: number; a: number };

const QUEEN = "\u0000queen";

/** How much to shrink the picture for a short stage. A stage the height of a
 *  desktop panel is drawn at full size; the corner of a console is a third of
 *  that, and drawing the same nodes and the same 76px of label under each in it
 *  stacked the bottom worker on the queen. Everything that has a size in pixels
 *  goes through this, so the picture is the same picture, smaller. */
const wrOf = (n: number) => (n > 14 ? 12 : n > 8 ? 16 : 20);
const scaleFor = (h: number) => Math.max(0.5, Math.min(1, h / 460));
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

const HiveHex = forwardRef<StageHandle, Props>(function HiveHex({ role, self, workers, tasks, ready }, ref) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const box = useRef<HTMLDivElement>(null);

  // What the frame loop reads. Updated by the effects below, never by render.
  const live = useRef({ role, self, workers, tasks });
  live.current = { role, self, workers, tasks };
  const packets = useRef<Packet[]>([]);
  const bursts = useRef<Burst[]>([]);
  const sparks = useRef<Spark[]>([]);
  const labels = useRef<Label[]>([]);
  const ripples = useRef<Ripple[]>([]);
  // What has been animated already, so a re-render of the same task is not a
  // second packet. `null` until the first batch, which is history and is not
  // replayed.
  const seen = useRef<Map<string, { state: string; tools: number }> | null>(null);

  // The live stream. Each pulse is one beam, with a look decided by what it is:
  // a tool call is a bright amber bolt with the tool's name beside it, a result
  // a green one as large as the result is, thinking a faint slow dot, and text a
  // thin quick streak. They run from the worker toward whoever is waiting on it —
  // the queen, or for a peer order the worker that asked.
  useImperativeHandle(
    ref,
    () => ({
      pulse: (p: StagePulse) => {
        const now = performance.now();
        const rl = live.current.role;
        const pal = palette();
        // Where the light travels: from the one doing the work to the one waiting.
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
        } else {
          return;
        }
        const grow = Math.min(3, Math.log10(1 + (p.bytes ?? 0)));
        let beam: Packet;
        switch (p.kind) {
          case "tool":
            beam = { from, to, t0: now, dur: 460, color: pal.amber, size: 5.2 };
            labels.current.push({ at: from, text: p.tool || "tool", t0: now, color: pal.amber });
            break;
          case "result":
            beam = { from, to, t0: now, dur: 420, color: pal.green, size: 3.6 + grow };
            break;
          case "text":
            beam = { from, to, t0: now, dur: 340, color: pal.accent, size: 2.2 + grow * 0.5, alpha: 0.85 };
            break;
          default:
            beam = { from, to, t0: now, dur: 620, color: pal.accent, size: 2.4, alpha: 0.4 };
        }
        packets.current.push(beam);
        if (packets.current.length > 90) packets.current.splice(0, packets.current.length - 90);
        if (p.kind !== "thinking") ripples.current.push({ at: to, t0: now + beam.dur, color: beam.color });
      },
    }),
    [],
  );

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
        // The bottom node's label hangs up to 76px under it while it works (the
        // status line is two rows lower than when it is idle), so the vertical
        // radius leaves that much, plus the node itself, inside the frame. Sizing it
        // as a share of the height instead clipped the last label at ten
        // workers and stacked two neighbours on top of each other.
        const rx = Math.min(W * 0.38, 480);
        const k = scaleFor(H);
        // The room the bottom node's label needs below it, at this scale.
        const ry = Math.max(50, Math.min(H / 2 - (wrOf(ws.length) * k + Math.max(50 * k, 38) + 12), 230));
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

    const nodeState = (id: string) => {
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
        const busy = !!nodeState(id);
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
      const drawNode = (id: string, label: string, r0: number, kind: "queen" | "worker", lost: boolean, engine?: string) => {
        const c = pos.get(id);
        if (!c) return;
        const task = nodeState(id);
        const k = scaleFor(H);
        const r = r0 * k * (1 + 0.035 * breathe);
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
          hexPath(ctx, c, r + 11 * k, now / 1600);
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
            ctx.arc(c.x, c.y, r + 20 * k, a0, a0 + 0.35);
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
        ctx.font = `600 ${Math.max(10, 12 * k)}px ui-sans-serif, system-ui, sans-serif`;
        ctx.fillStyle = lost ? pal.dim : pal.text;
        // Labels point away from the queen. A worker in the upper half has the queen
        // below it, and a label hung there ran into her; above is always clear.
        const nameOff = Math.max((task ? 34 : 20) * k, task ? 24 : 14);
        const subOff = Math.max((task ? 50 : 35) * k, task ? 38 : 26);
        const above = rl === "queen" && kind === "worker" && c.y < H / 2 - 10;
        const subY = above ? c.y - r - Math.max(8 * k, 7) : c.y + r + subOff;
        const nameY = above ? subY - (subOff - nameOff) : c.y + r + nameOff;
        ctx.fillText(short(label), c.x, nameY);
        ctx.font = `${Math.max(9, 11 * k)}px ui-monospace, SFMono-Regular, monospace`;
        ctx.fillStyle = pal.dim;
        let sub = lost ? "lost" : kind === "queen" ? (label === "queen" ? "" : "queen") : engine ? `idle · ${engine}` : "idle";
        if (task) {
          const secs = Math.max(0, Math.round((Date.now() - Date.parse(task.started_at)) / 1000));
          sub =
            task.phase === "tool" ? `⚙ ${task.tool || "tool"}` : task.phase === "writing" ? "writing…" : "thinking…";
          sub += `  ${secs}s`;
          ctx.fillStyle = pal.accent;
        }
        ctx.fillText(sub, c.x, subY);
        ctx.restore();
      };

      if (rl === "queen") {
        // Smaller cells as the hive fills, so a crowd stays a honeycomb and not a
        // pile.
        const wr = wrOf(ws.length);
        ws.forEach((w) => drawNode(w.name, w.name, wr, "worker", w.state === "lost", w.engine));
        drawNode(QUEEN, me || "queen", 28, "queen", false);
      } else if (rl === "worker") {
        drawNode(QUEEN, "queen", 24, "queen", false);
        drawNode(SELF, me || "worker", 27, "worker", false);
      } else {
        drawNode(SELF, me || "this instance", 27, "worker", false);
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
          ctx.globalAlpha = (1 - k / 6) * (p.alpha ?? 0.9);
          ctx.fillStyle = p.color;
          ctx.shadowColor = p.color;
          ctx.shadowBlur = 14 * pal.glow;
          ctx.beginPath();
          ctx.arc(a.x + (b.x - a.x) * tt, a.y + (b.y - a.y) * tt, Math.max(0.6, (p.size ?? 4.5) - k * ((p.size ?? 4.5) / 8)), 0, Math.PI * 2);
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
        hexPath(ctx, c, (30 + t * 60) * scaleFor(H), t);
        ctx.stroke();
        ctx.restore();
      }

      // Where a beam lands: a small ring that opens and fades.
      ripples.current = ripples.current.filter((r) => now - r.t0 < 420);
      for (const r of ripples.current) {
        const c = pos.get(r.at);
        if (!c || now < r.t0) continue;
        const t = (now - r.t0) / 420;
        ctx.save();
        ctx.strokeStyle = r.color;
        ctx.globalAlpha = 0.6 * (1 - t);
        ctx.lineWidth = 1.6;
        ctx.beginPath();
        ctx.arc(c.x, c.y, (16 + t * 20) * scaleFor(H), 0, Math.PI * 2);
        ctx.stroke();
        ctx.restore();
      }

      // The name of the tool, to the side of the worker that called it and drifting
      // up as it fades. To the side, because above and below are where the node's
      // own labels are.
      labels.current = labels.current.filter((l) => now - l.t0 < 1400);
      for (const l of labels.current) {
        const c = pos.get(l.at);
        if (!c) continue;
        const t = (now - l.t0) / 1400;
        const k = scaleFor(H);
        ctx.save();
        ctx.globalAlpha = 1 - t * t;
        ctx.fillStyle = l.color;
        ctx.font = `600 ${Math.max(9, 11 * k)}px ui-monospace, SFMono-Regular, monospace`;
        ctx.textAlign = "left";
        ctx.fillText(`⚙ ${l.text}`, c.x + (wrOf(live.current.workers.length) + 12) * k, c.y - 2 - t * 16 * k);
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
          const d = (34 + t * 40) * scaleFor(H);
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
    <div className="relative h-full min-h-[180px] w-full overflow-hidden" ref={box}>
      <canvas ref={canvas} className="absolute left-0 top-0" />
    </div>
  );
});

export default HiveHex;
