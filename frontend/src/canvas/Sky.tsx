import { useEffect, useRef } from "react";
import { BASES, BaseName } from "./theme";

/** One member's light across the sky: how busy it is says how bright and how
 *  fast its trail runs. */
export interface Lane {
  key: string;
  busy: number; // 0 resting … 1 flat out
  rgb: [number, number, number];
}

// The light of the hour. Never dark and never purple: the night is a cool
// moonlit grey-blue, the evening a warm peach.
const KEYS: { h: number; top: number[]; bottom: number[] }[] = [
  { h: 0, top: [212, 222, 230], bottom: [236, 239, 238] },
  { h: 5, top: [216, 226, 234], bottom: [240, 238, 232] },
  { h: 7, top: [252, 220, 196], bottom: [255, 240, 218] },
  { h: 10, top: [196, 222, 246], bottom: [246, 242, 230] },
  { h: 13, top: [170, 210, 246], bottom: [232, 242, 250] },
  { h: 16, top: [186, 214, 240], bottom: [252, 238, 216] },
  { h: 19, top: [250, 214, 186], bottom: [255, 234, 206] },
  { h: 21, top: [222, 226, 228], bottom: [246, 236, 222] },
  { h: 24, top: [212, 222, 230], bottom: [236, 239, 238] },
];

function skyAt(hour: number) {
  let i = KEYS.length - 2;
  while (i > 0 && KEYS[i].h > hour) i--;
  const a = KEYS[i], b = KEYS[i + 1];
  const f = b.h > a.h ? (hour - a.h) / (b.h - a.h) : 0;
  const mix = (x: number[], y: number[]) => x.map((v, k) => Math.round(v + (y[k] - v) * f));
  return { top: mix(a.top, b.top), bottom: mix(a.bottom, b.bottom) };
}

const seedOf = (s: string) => ([...s].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 997, 7)) / 997;

/**
 * The canvas's ground: the hive's day as a time-lapse. The light follows the
 * hour being shown, and every member draws a trail that lingers like a long
 * exposure — bright and quick while it works, faint and slow at rest.
 * (Drawn as drifting patches of light: the line trails read as caterpillars.)
 */
export default function Sky({ at, lanes, base = "timelapse", mode = "light" }: { at: Date; lanes: Lane[]; base?: BaseName; mode?: "light" | "dark" }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const atRef = useRef(at);
  const lanesRef = useRef(lanes);
  const lookRef = useRef({ base, mode });
  atRef.current = at;
  lanesRef.current = lanes;
  lookRef.current = { base, mode };

  useEffect(() => {
    const cv = ref.current!;
    const ctx = cv.getContext("2d")!;
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    let raf = 0;
    let W = 0, H = 0;
    const size = () => {
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      W = cv.clientWidth; H = cv.clientHeight;
      cv.width = W * dpr; cv.height = H * dpr;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };
    size();
    const ro = new ResizeObserver(size);
    ro.observe(cv);
    const t0 = performance.now();

    const frame = (now: number) => {
      const t = (now - t0) / 1000;
      const d = atRef.current;
      const hour = d.getHours() + d.getMinutes() / 60;
      const { base, mode } = lookRef.current;
      const dark = mode === "dark";
      let s = skyAt(hour);
      if (base !== "timelapse") {
        const [top, bottom] = BASES[base][mode];
        s = { top, bottom };
      } else if (dark) {
        // The same hours, deep: the hue of the moment kept, the light taken out.
        const deep = (c: number[]) => c.map((v) => Math.round(10 + v * 0.13));
        s = { top: deep(s.top), bottom: deep(s.bottom) };
      }
      const g = ctx.createLinearGradient(0, 0, 0, H);
      g.addColorStop(0, `rgb(${s.top})`);
      g.addColorStop(1, `rgb(${s.bottom})`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, W, H);

      // The sun, from 06:00 to 20:00, highest at 13:00.
      const p = Math.min(Math.max((hour - 6) / 14, -0.1), 1.1);
      const sx = W * (-0.1 + 1.2 * p);
      const sy = H * (0.62 - Math.sin(Math.max(0, Math.min(1, p)) * Math.PI) * 0.48);
      if (base === "timelapse") {
        const sun = ctx.createRadialGradient(sx, sy, 0, sx, sy, Math.max(W, H) * 0.7);
        sun.addColorStop(0, dark ? "rgba(255,214,160,.16)" : "rgba(255,247,224,.95)");
        sun.addColorStop(0.25, dark ? "rgba(255,200,140,.06)" : "rgba(255,237,204,.4)");
        sun.addColorStop(1, "rgba(255,255,255,0)");
        ctx.fillStyle = sun;
        ctx.fillRect(0, 0, W, H);
      }

      // The members as light, not lines: each a soft patch drifting across
      // the sky, larger, brighter and quicker while it works, faint and slow
      // at rest — the way light moves over a room in a time-lapse.
      const span = Math.max(W, H);
      lanesRef.current.slice(0, 8).forEach((L, i) => {
        const seed = seedOf(L.key);
        const pace = 0.018 + 0.05 * L.busy;
        const ph = t * pace + seed * 6.283;
        const x = W * (0.5 + 0.42 * Math.sin(ph + i * 1.7) * Math.cos(ph * 0.37 + seed * 3));
        const y = H * (0.5 + 0.36 * Math.sin(ph * 0.71 + i * 2.3 + seed * 5));
        const breathe = 1 + 0.06 * Math.sin(t * (0.6 + L.busy) + seed * 9);
        const r = span * (0.16 + 0.12 * L.busy) * breathe;
        const a = (dark ? 0.08 : 0.13) + (dark ? 0.12 : 0.2) * L.busy;
        const glow = ctx.createRadialGradient(x, y, 0, x, y, r);
        glow.addColorStop(0, `rgba(${L.rgb},${a})`);
        glow.addColorStop(0.45, `rgba(${L.rgb},${a * 0.45})`);
        glow.addColorStop(1, `rgba(${L.rgb},0)`);
        ctx.fillStyle = glow;
        ctx.fillRect(x - r, y - r, r * 2, r * 2);
      });
      if (!still) raf = requestAnimationFrame(frame);
    };
    raf = requestAnimationFrame(frame);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
    };
  }, []);

  return <canvas ref={ref} className="cv-sky" aria-hidden="true" />;
}
