import { useCallback, useRef } from "react";

/**
 * Keeps a scrolling thread on its newest line while it grows — a sent
 * message, an answer streaming in, markdown laying itself out after the
 * render — unless the person has scrolled up to read. `follow()` brings it
 * back down (on sending). Put the ref and onScroll on the element that
 * scrolls; it may mount later than the component.
 */
export function useStickToBottom<T extends HTMLElement>() {
  const el = useRef<T | null>(null);
  const stick = useRef(true);
  const stop = useRef<() => void>(() => {});
  const toEnd = () => { const e = el.current; if (e && stick.current) e.scrollTop = e.scrollHeight; };
  const ref = useCallback((node: T | null) => {
    stop.current();
    stop.current = () => {};
    el.current = node;
    if (!node) return;
    stick.current = true;
    toEnd();
    const mo = new MutationObserver(toEnd);
    mo.observe(node, { childList: true, subtree: true, characterData: true });
    const ro = new ResizeObserver(toEnd);
    ro.observe(node);
    stop.current = () => { mo.disconnect(); ro.disconnect(); };
  }, []);
  const onScroll = (e: React.UIEvent<T>) => {
    const n = e.currentTarget;
    stick.current = n.scrollHeight - n.scrollTop - n.clientHeight < 48;
  };
  const follow = () => { stick.current = true; toEnd(); };
  return { ref, onScroll, follow };
}
