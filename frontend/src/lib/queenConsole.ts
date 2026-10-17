// The person's one conversation with the queen, the same on every client.
//
// Each client used to keep a console of its own ("hive-console-web" here,
// "hive-console-<device>" on each phone and pad) and hide the others', so what
// was said on the iPad never showed on the iPhone. The core names the shared
// one now (QueenConsole); a core too old to say is answered with the same
// constant. The old per-device consoles stay as ordinary conversations,
// titled after the device they came from.

import { useEffect, useState } from "react";
import { PairedDevices, QueenConsole } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";

/** The shared console's id when the core does not say. */
export const QUEEN_CONSOLE_FALLBACK = "hive-console";

let current = QUEEN_CONSOLE_FALLBACK;
let asking: Promise<string> | null = null;
const listeners = new Set<(id: string) => void>();

/** The queen's conversation as last known; the fallback until the core says. */
export function queenSession(): string {
  return current;
}

/** Asks the core which conversation is the queen's, once, or again when forced
 *  (this window switched to another core). */
export function loadQueenSession(force = false): Promise<string> {
  if (asking && !force) return asking;
  // Called inside the chain: a window whose bindings predate the method
  // throws synchronously, and that too is "the core does not say".
  asking = Promise.resolve()
    .then(() => QueenConsole())
    .then((r) => {
      const id = typeof r?.session === "string" && r.session.trim() ? r.session.trim() : QUEEN_CONSOLE_FALLBACK;
      return id;
    })
    .catch(() => QUEEN_CONSOLE_FALLBACK)
    .then((id) => {
      if (id !== current) {
        current = id;
        listeners.forEach((fn) => fn(id));
      }
      return id;
    });
  return asking;
}

/** The queen's conversation, re-read when this window moves to another core. */
export function useQueenSession(): string {
  const [id, setId] = useState(current);
  useEffect(() => {
    listeners.add(setId);
    void loadQueenSession();
    setId(current);
    const off = EventsOn("hivelink:state", () => void loadQueenSession(true));
    return () => {
      listeners.delete(setId);
      if (typeof off === "function") off();
    };
  }, []);
  return id;
}

const LEGACY = "hive-console-";

/** One of the old per-device consoles, kept as an ordinary conversation. */
export function isLegacyConsole(id: string): boolean {
  return id.startsWith(LEGACY) && id.length > LEGACY.length;
}

export interface DeviceName {
  id: string;
  name: string;
}

/** "蜂群指令 · iPad": an old console, named for the device it was kept on. */
export function legacyConsoleTitle(id: string, devices: DeviceName[]): string {
  const who = id.slice(LEGACY.length);
  if (who === "web") return "蜂群指令 · 网页";
  const d = devices.find((x) => x.id === who);
  return d?.name?.trim() ? `蜂群指令 · ${d.name.trim()}` : "蜂群指令 · 旧";
}

/** Whether a conversation belongs in the person's list: not the queen's own
 *  (it has its row), not one the screens or the hive keep for themselves —
 *  but the old consoles, which are the person's. */
export function isPersonalConversation(s: { id: string; title?: string }, queen: string): boolean {
  if (s.id === queen) return false;
  if (isLegacyConsole(s.id)) return true;
  if (s.id.startsWith("tile-") || s.id.startsWith("hive-") || s.id.startsWith("hive:") || s.id.startsWith("dashboard:")) return false;
  return !(s.title || "").startsWith("You hold a standing responsibility");
}

let devicesAt = 0;
let devicesP: Promise<DeviceName[]> | null = null;

/** The paired devices, for naming old consoles; read at most once a minute. */
export function pairedDeviceNames(): Promise<DeviceName[]> {
  if (devicesP && Date.now() - devicesAt < 60_000) return devicesP;
  devicesAt = Date.now();
  devicesP = Promise.resolve()
    .then(() => PairedDevices())
    .then((l) => ((l ?? []) as DeviceName[]).map((d) => ({ id: d.id, name: d.name })))
    .catch(() => []);
  return devicesP;
}

/** The list with every old console titled after its device. */
export async function withConsoleTitles<T extends { id: string; title: string }>(list: T[]): Promise<T[]> {
  if (!list.some((s) => isLegacyConsole(s.id))) return list;
  const devices = await pairedDeviceNames();
  return list.map((s) => (isLegacyConsole(s.id) ? { ...s, title: legacyConsoleTitle(s.id, devices) } : s));
}
