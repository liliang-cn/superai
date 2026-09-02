import { useEffect, useRef, useState } from "react";

/**
 * A number that rolls to its new value instead of jumping. Dashboards are
 * read at a glance, and a figure that changes by sliding is one whose change
 * you noticed; one that snaps is one you may have missed. Respects
 * prefers-reduced-motion by snapping.
 */
export function useTween(target: number, ms = 600): number {
  const [v, setV] = useState(target);
  const from = useRef(target);
  const start = useRef(0);
  useEffect(() => {
    const reduce = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    if (reduce || !Number.isFinite(target)) { setV(target); from.current = target; return; }
    from.current = v;
    start.current = performance.now();
    let raf = 0;
    const step = (now: number) => {
      const p = Math.min(1, (now - start.current) / ms);
      const e = 1 - Math.pow(1 - p, 3);
      setV(from.current + (target - from.current) * e);
      if (p < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, ms]);
  return v;
}
