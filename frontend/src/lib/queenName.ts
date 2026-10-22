import { useSyncExternalStore } from "react";
import { QueenIdentity } from "../../wailsjs/go/app/App";
import { translate } from "./i18n";

/**
 * What the person calls the queen. The core keeps it (Settings › queen_name,
 * served by QueenIdentity, HiveStatus and GetStatus); this is the one copy the
 * page reads, so every place that says "the queen" says the same name.
 *
 * Only what people see changes. Roles, session ids and the agent itself stay
 * "queen".
 */
export const DEFAULT_QUEEN = "queen";

export interface QueenName {
  name: string;
  aliases: string[];
}

let current: QueenName = { name: DEFAULT_QUEEN, aliases: [] };
let asked = false;
const listeners = new Set<() => void>();

/** Take what the core said. Polls of HiveStatus call this with their answer. */
export function setQueenName(name?: string, aliases?: string[]) {
  const next = { name: (name ?? "").trim() || DEFAULT_QUEEN, aliases: aliases ?? [] };
  if (next.name === current.name && next.aliases.join("\u0000") === current.aliases.join("\u0000")) return;
  current = next;
  listeners.forEach((fn) => fn());
}

function ask() {
  if (asked) return;
  asked = true;
  QueenIdentity()
    .then((r: any) => setQueenName(r?.name, r?.aliases))
    // An older core has no QueenIdentity; HiveStatus's poll still brings the
    // name, and asking again on every frame would not.
    .catch(() => {});
}

function subscribe(fn: () => void) {
  listeners.add(fn);
  ask();
  return () => { listeners.delete(fn); };
}

/** The name as it stands, for code outside React (the 3D world's labels). */
export function getQueenName(): QueenName {
  ask();
  return current;
}

export function useQueenName(): QueenName {
  return useSyncExternalStore(subscribe, () => current, () => current);
}

export function queenNamed(q: QueenName = current) {
  return q.name !== DEFAULT_QUEEN;
}

/** The queen as a title: "The queen" (蜂后) until she has a name. */
export function queenTitle(q: QueenName = current) {
  return queenNamed(q) ? q.name : translate("The queen");
}

/** The queen inside a sentence: "the queen" (蜂后) until she has a name. */
export function queenRef(q: QueenName = current) {
  return queenNamed(q) ? q.name : translate("the queen");
}
