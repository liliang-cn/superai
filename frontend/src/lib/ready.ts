import { useSyncExternalStore } from "react";
import { EventsOn } from "../../wailsjs/runtime";

// Whether this SuperLeo has something to think with: a model, or an engine
// such as Claude Code. App's status poll sets it; the bars read it so a
// message is not sent into a backend that can only fail, and the setup
// banner and the first start's model step open from anywhere.

let ready: boolean | null = null;
const subs = new Set<() => void>();

export function setReady(v: boolean) {
  if (v === ready) return;
  ready = v;
  subs.forEach((f) => f());
}

export function useReady(): boolean | null {
  return useSyncExternalStore(
    (f) => {
      subs.add(f);
      return () => subs.delete(f);
    },
    () => ready,
  );
}

export const SETUP_EVENT = "superleo:setup";

/** Opens the first start's "choose how SuperLeo thinks" step. */
export function requestSetup() {
  window.dispatchEvent(new Event(SETUP_EVENT));
}

// chat:error can arrive before SendChat has handed its id back (a backend
// with no model fails at once), so the last few are kept for whoever binds
// the id a moment later.
const early = new Map<string, string>();
let listening = false;
function listen() {
  if (listening) return;
  listening = true;
  EventsOn("chat:error", (p: any) => {
    if (!p?.requestId) return;
    early.set(p.requestId, String(p.error ?? ""));
    if (early.size > 20) early.delete(early.keys().next().value as string);
  });
}
listen();

/** The error a request already failed with, if it did before its id was bound. */
export function earlyError(requestId: string): string | undefined {
  const e = early.get(requestId);
  if (e !== undefined) early.delete(requestId);
  return e;
}
