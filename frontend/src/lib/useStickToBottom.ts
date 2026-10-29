import { useCallback, useRef } from "react";

/**
 * Keeps a scrolling thread on its newest line while it grows — a sent
 * message, an answer streaming in, markdown laying itself out after the
 * render — unless the person has scrolled up to read. `follow()` brings it
 * back down (on sending). Put the ref and onScroll on the element that
 * scrolls; it may mount later than the component.
 *
 * Only the person lets go of the bottom. A layout change — a pane folded or
 * opened beside the thread, the window resized — reflows the text and moves
 * scrollTop on its own, and that scroll event used to read as "scrolled up":
 * the answer still streaming then grew out of sight. So a scroll away from the
 * bottom counts only shortly after a wheel, touch, pointer press or key.
 */
export function useStickToBottom<T extends HTMLElement>() {
  const el = useRef<T | null>(null);
  const stick = useRef(true);
  const userAt = useRef(0);
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
    const touched = () => { userAt.current = Date.now(); };
    const events = ["wheel", "touchmove", "pointerdown", "keydown"] as const;
    events.forEach((ev) => node.addEventListener(ev, touched, { passive: true }));
    stop.current = () => {
      mo.disconnect(); ro.disconnect();
      events.forEach((ev) => node.removeEventListener(ev, touched));
    };
  }, []);
  const onScroll = (e: React.UIEvent<T>) => {
    const n = e.currentTarget;
    if (n.scrollHeight - n.scrollTop - n.clientHeight < 48) { stick.current = true; return; }
    if (Date.now() - userAt.current < 1000) { stick.current = false; return; }
    // Not the person: the layout moved it. Stay where we were asked to be.
    toEnd();
  };
  const follow = () => { stick.current = true; toEnd(); };
  return { ref, onScroll, follow };
}
