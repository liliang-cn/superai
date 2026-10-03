import { useEffect, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";
import { CLIRuns, HiveStatus, LinkedAgents, StandingAgents, Upcoming } from "../../wailsjs/go/app/App";
import type { CodingRun, UpcomingItem } from "./tiles";

export interface HiveMember {
  name: string;
  state: "live" | "lost";
  engine?: string;
  version?: string;
  last_seen?: string;
  started_at?: string;
}

export interface HiveTask {
  id: string;
  worker: string;
  prompt: string;
  state: "running" | "done" | "failed" | "cancelled";
  tool?: string;
  tools: number;
  started_at: string;
  result?: string;
}

export interface Hive {
  loaded: boolean;
  role: "" | "queen" | "worker";
  name: string;
  members: HiveMember[];
  tasks: HiveTask[];
}

/** The hive as this SuperAI sees it: polled for the roster, kept current by
 *  hive:task for the orders. */
export function useHive(): Hive {
  const [hive, setHive] = useState<Hive>({ loaded: false, role: "", name: "", members: [], tasks: [] });
  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const s = (await HiveStatus()) as any;
        if (!alive) return;
        setHive((cur) => ({
          loaded: true,
          role: s.role ?? "",
          name: s.name ?? "",
          members: [...(s.members ?? [])].sort((a: HiveMember, b: HiveMember) => a.name.localeCompare(b.name, undefined, { numeric: true })),
          // The first load seeds the orders; after that the stream is the source.
          tasks: cur.loaded ? cur.tasks : [...(s.tasks ?? [])].slice(-80).reverse(),
        }));
      } catch {
        if (alive) setHive((cur) => ({ ...cur, loaded: true }));
      }
    };
    load();
    const t = window.setInterval(load, 10000);
    const off = EventsOn("hive:task", (t: HiveTask) => {
      if (!t?.id) return;
      setHive((cur) => {
        const i = cur.tasks.findIndex((x) => x.id === t.id);
        const tasks = i >= 0 ? cur.tasks.map((x, j) => (j === i ? { ...x, ...t } : x)) : [t, ...cur.tasks].slice(0, 80);
        return { ...cur, tasks };
      });
    });
    return () => {
      alive = false;
      window.clearInterval(t);
      if (typeof off === "function") off();
    };
  }, []);
  return hive;
}

export interface Bee {
  id: string;
  name: string;
  glyph?: string;
  goal: string;
  paused: boolean;
  running?: unknown;
  nextDue?: string;
  waitingFor?: string;
}

/** The standing agents, reloaded whenever one of them changes. */
export function useBees(): Bee[] {
  const [bees, setBees] = useState<Bee[]>([]);
  useEffect(() => {
    let alive = true;
    let timer = 0;
    const load = async () => {
      try {
        const list = (await StandingAgents()) as unknown as Bee[];
        if (alive) setBees(list ?? []);
      } catch {
        /* not a standing-agent build, or not ready: the tile says so */
      }
    };
    load();
    const off = EventsOn("agent:update", () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(load, 250);
    });
    return () => {
      alive = false;
      window.clearTimeout(timer);
      if (typeof off === "function") off();
    };
  }, []);
  return bees;
}

/** The one conversation with the queen that the home screens share. */
export const QUEEN_SESSION = "hive-console-web";

export const isToday = (iso?: string) => {
  if (!iso) return false;
  const d = new Date(iso);
  const s = new Date();
  s.setHours(0, 0, 0, 0);
  return d >= s;
};

export function short(name: string): string {
  if (name.startsWith("superai-worker-")) return "w" + name.slice("superai-worker-".length);
  if (name === "queen" || name.startsWith("superai-queen")) return "Q";
  if (name.toLowerCase().includes("mac")) return "Mac";
  return name.slice(0, 3);
}

export const oneLine = (s: string) => s.replace(/[#*`>|]+/g, "").replace(/\s+/g, " ").trim();

export function elapsed(fromIso: string, now = Date.now()): string {
  const n = Math.max(0, Math.round((now - new Date(fromIso).getTime()) / 1000));
  return n < 60 ? `${n}s` : n < 3600 ? `${Math.floor(n / 60)}m` : `${Math.floor(n / 3600)}h ${Math.floor((n % 3600) / 60)}m`;
}

/** Everything the hive will do on its own, kept current. */
export function useUpcoming(): UpcomingItem[] {
  const [items, setItems] = useState<UpcomingItem[]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => Upcoming().then((l) => { if (alive) setItems((l ?? []) as unknown as UpcomingItem[]); }).catch(() => {});
    load();
    const t = window.setInterval(load, 60000);
    const offs = ["agent:update", "schedule:changed", "schedule:run"].map((n) => EventsOn(n, load));
    return () => { alive = false; window.clearInterval(t); offs.forEach((o) => typeof o === "function" && o()); };
  }, []);
  return items;
}

/** The coding agents' runs, newest first, kept current. */
export function useCodingRuns(): CodingRun[] {
  const [runs, setRuns] = useState<CodingRun[]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => CLIRuns().then((l) => {
      if (!alive) return;
      const list = ((l ?? []) as unknown as CodingRun[]).slice().sort((a, b) => new Date(b.started).getTime() - new Date(a.started).getTime());
      setRuns(list);
    }).catch(() => {});
    load();
    const off = EventsOn("cli:run", load);
    const t = window.setInterval(load, 30000);
    return () => { alive = false; window.clearInterval(t); if (typeof off === "function") off(); };
  }, []);
  return runs;
}


export interface LinkedAgent {
  name: string;
  host: string;
  os: string;
  arch: string;
  version: string;
  clis: string[];
  agents?: { name: string; about: string }[];
  connected: string;
  lastSeen: string;
}

/** The agents connected to core over the agent link — the machines it can
 *  run things on — without the hive's own workers, which are listed as such. */
export function useLinkedAgents(): LinkedAgent[] {
  const [list, setList] = useState<LinkedAgent[]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => LinkedAgents().then((l) => {
      if (alive) setList(((l ?? []) as unknown as LinkedAgent[]).filter((a) => !a.name.startsWith("superai-worker-")));
    }).catch(() => {});
    load();
    const off = EventsOn("agents:changed", load);
    const t = window.setInterval(load, 30000);
    return () => { alive = false; window.clearInterval(t); if (typeof off === "function") off(); };
  }, []);
  return list;
}
