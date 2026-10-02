import { useEffect, useRef } from "react";

/** One member's light across the sky: how busy it is says how bright and how
 *  fast its trail runs. */
export interface Lane {
  key: string;
  busy: number; // 0 resting … 1 flat out
  rgb: [number, number, number];
}

// The light of the hour. Never dark: evening is lavender, night a pale violet.
const KEYS: { h: number; top: number[]; bottom: number[] }[] = [
  { h: 0, top: [204, 201, 237], bottom: [237, 227, 235] },
  { h: 6, top: [252, 214, 196], bottom: [255, 238, 214] },
  { h: 9, top: [196, 222, 246], bottom: [246, 240, 228] },
  { h: 13, top: [170, 210, 246], bottom: [232, 242, 250] },
  { h: 16, top: [186, 214, 240], bottom: [252, 236, 214] },
  { h: 19, top: [232, 196, 214], bottom: [255, 220, 190] },
  { h: 22, top: [206, 200, 236], bottom: [244, 222, 220] },
  { h: 24, top: [204, 201, 237], bottom: [237, 227, 235] },
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
 */
export default function Sky({ at, lanes }: { at: Date; lanes: Lane[] }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const atRef = useRef(at);
  const lanesRef = useRef(lanes);
  atRef.current = at;
  lanesRef.current = lanes;

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
      const s = skyAt(hour);
      const g = ctx.createLinearGradient(0, 0, 0, H);
      g.addColorStop(0, `rgb(${s.top})`);
      g.addColorStop(1, `rgb(${s.bottom})`);
      ctx.fillStyle = g;
      ctx.fillRect(0, 0, W, H);

      // The sun, from 06:00 to 20:00, highest at 13:00.
      const p = Math.min(Math.max((hour - 6) / 14, -0.1), 1.1);
      const sx = W * (-0.1 + 1.2 * p);
      const sy = H * (0.62 - Math.sin(Math.max(0, Math.min(1, p)) * Math.PI) * 0.48);
      const sun = ctx.createRadialGradient(sx, sy, 0, sx, sy, Math.max(W, H) * 0.7);
      sun.addColorStop(0, "rgba(255,247,224,.95)");
      sun.addColorStop(0.25, "rgba(255,237,204,.4)");
      sun.addColorStop(1, "rgba(255,255,255,0)");
      ctx.fillStyle = sun;
      ctx.fillRect(0, 0, W, H);

      lanesRef.current.forEach((L, i) => {
        const seed = seedOf(L.key);
        const y0 = H * (0.1 + 0.8 * ((i * 0.618 + seed) % 1));
        const amp = 40 + 90 * seed;
        const speed = 0.04 + 0.11 * L.busy;
        const head = ((t * speed + seed) % 1.25) - 0.1;
        const tail = 0.22 + 0.35 * L.busy;
        const alpha = 0.3 + 0.55 * L.busy;
        const pt = (x: number) => [x * W, y0 + Math.sin(x * 3.1 + seed * 6.2 + t * 0.12) * amp * 0.6 + Math.sin(x * 7.3 + seed * 11) * amp * 0.22];
        const n = 34;
        let prev = pt(head - tail);
        ctx.lineCap = "round";
        for (let k = 1; k <= n; k++) {
          const f = k / n;
          const q = pt(head - tail + tail * f);
          ctx.strokeStyle = `rgba(${L.rgb},${alpha * f * 0.28})`;
          ctx.lineWidth = 7 + 6 * L.busy;
          ctx.beginPath(); ctx.moveTo(prev[0], prev[1]); ctx.lineTo(q[0], q[1]); ctx.stroke();
          ctx.strokeStyle = `rgba(${L.rgb},${alpha * f})`;
          ctx.lineWidth = 1.4 + 2 * L.busy;
          ctx.beginPath(); ctx.moveTo(prev[0], prev[1]); ctx.lineTo(q[0], q[1]); ctx.stroke();
          prev = q;
        }
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
