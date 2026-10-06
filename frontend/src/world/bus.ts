import { useEffect, useState } from "react";

// The page and the world point at the same things. A row clicked on a page
// selects its object in the world (and the camera goes to it); an object
// clicked in the world marks its row on the page. Both sides speak of a thing
// as kind + id: "worker"/"superai-worker-1", "bee"/<id>, "report"/<agent id>.

export type Selection = { kind: string; id: string } | null;

let current: Selection = null;
let focusFn: ((kind: string, id: string) => boolean) | null = null;
const subs = new Set<(s: Selection) => void>();

export const worldBus = {
  /** The world registers how to bring a thing into view. */
  setFocus(fn: ((kind: string, id: string) => boolean) | null) { focusFn = fn; },
  /** Whatever is selected now, from either side. */
  publish(sel: Selection) {
    current = sel;
    subs.forEach((f) => f(sel));
  },
  subscribe(f: (s: Selection) => void) { subs.add(f); return () => { subs.delete(f); }; },
};

/** Select a thing in the world, from a page. Returns whether the world has it. */
export function focusInWorld(kind: string, id: string) {
  return focusFn?.(kind, id) ?? false;
}

/** What is selected, for a page to mark its row. */
export function useWorldSelection(): Selection {
  const [sel, setSel] = useState<Selection>(current);
  useEffect(() => worldBus.subscribe(setSel), []);
  return sel;
}

export const isSelected = (sel: Selection, kind: string, id: string | undefined) => !!sel && sel.kind === kind && sel.id === (id ?? "");
