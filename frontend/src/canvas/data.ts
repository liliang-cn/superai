import { useEffect, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";
import { CLIRuns, HiveStatus, HiveTopology, Hosts, LinkedAgents, StandingAgents, Upcoming } from "../../wailsjs/go/app/App";
import type { CodingRun, UpcomingItem } from "./tiles";
import { setQueenName } from "../lib/queenName";

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
  /** "out" for an order this core gave — to a worker, or to an agent outside the hive. */
  dir?: "out" | "in" | "peer";
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
  /** Who thinks for the queen in a person's chat ("claude", "claude.mac"); empty is SuperAI's own agent. */
  engine?: string;
  /** The relay agents (agent_link.relay_only): not machines, but the named
   *  agents they serve are drawn as their own group. */
  relays?: RelayAgent[];
}

/** A relay agent: what it serves (openclaw, hermes, pi …) and what is
 *  running on it, by CLI. */
export interface RelayAgent {
  name: string;
  host: string;
  agents: { name: string; about: string }[];
  cliActivity?: Record<string, CLIActivity>;
}

/** Patches one CLI's snapshot into a by-CLI map; null removes it. */
export function withCLI(m: Record<string, CLIActivity> | undefined, cli: string, act: CLIActivity | null | undefined): Record<string, CLIActivity> {
  const next = { ...(m ?? {}) };
  if (act && act.sessions?.length) next[cli] = act;
  else delete next[cli];
  return next;
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
        setQueenName(s.queen_name, s.queen_aliases);
        setHive((cur) => ({
          loaded: true,
          role: s.role ?? "",
          name: s.name ?? "",
          engine: s.queen_engine ?? "",
          relays: Array.isArray(s.relays) ? s.relays : [],
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
    // What runs on a relay moved: patched in place.
    const offCli = EventsOn("cli:activity", (p: { agent?: string; cli?: string; activity?: CLIActivity | null }) => {
      if (!p?.agent || !p.cli) return;
      setHive((cur) => (cur.relays?.some((r) => r.name === p.agent)
        ? { ...cur, relays: cur.relays.map((r) => (r.name === p.agent ? { ...r, cliActivity: withCLI(r.cliActivity, p.cli!, p.activity) } : r)) }
        : cur));
    });
    return () => {
      alive = false;
      window.clearInterval(t);
      [off, offCli].forEach((o) => typeof o === "function" && o());
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

// The conversation with the queen the home screens share is the person's one
// conversation with her on every device: lib/queenConsole.ts.

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
  /** Shown and looked after as a machine. */
  machine?: boolean;
  /** machine, worker (a hive worker's pod) or relay (kept for its named agents). */
  role?: string;
  /** What the interactive Claude Code sessions in its terminals are doing —
   *  not SuperAI's own runs. Absent when it has none. */
  activity?: CCActivity | null;
  /** What every coding CLI running on it is doing, by CLI (claude too):
   *  from the CLI's plugin or hooks, or from its processes. A CLI not
   *  running is not in it. */
  cliActivity?: Record<string, CLIActivity>;
}

/** One terminal Claude Code session, as its machine reports it: no prompt,
 *  no tool arguments, only where it is, how it is and what tool it is on. */
export interface CCActivitySession {
  id: string;
  /** The working directory's last element. */
  project?: string;
  state: "working" | "waiting" | "idle";
  /** Why a waiting one waits. */
  wait?: "permission" | "input";
  tool?: string;
  /** Tool calls this turn. */
  tools: number;
  since: string;
  started: string;
  last: string;
}

export interface CCActivity {
  working: number;
  waiting: number;
  idle: number;
  sessions: CCActivitySession[];
  at: string;
}

/** One CLI's sessions on a machine: "plugin" when its own plugin or hooks
 *  said so, "process" when only its processes did (working while they use
 *  the CPU, else idle). */
export interface CLIActivity extends CCActivity {
  source: "plugin" | "process";
}

/** The machines connected to core over the agent link — the ones it can run
 *  things on and look after. Core leaves out the hive's own workers, listed
 *  as such, and the relays kept only for their named agents; the filter here
 *  is for a core from before it did. */
export function useLinkedAgents(): LinkedAgent[] {
  const [list, setList] = useState<LinkedAgent[]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => LinkedAgents().then((l) => {
      if (alive) setList(((l ?? []) as unknown as LinkedAgent[]).filter((a) => a.machine ?? !a.name.startsWith("superai-worker-")));
    }).catch(() => {});
    load();
    const off = EventsOn("agents:changed", load);
    // A machine's terminal sessions moved: patched in place, no reload.
    const offAct = EventsOn("cc:activity", (p: { agent?: string; activity?: CCActivity | null }) => {
      if (!alive || !p?.agent) return;
      setList((cur) => cur.map((a) => (a.name === p.agent ? { ...a, activity: p.activity ?? null } : a)));
    });
    // Any coding CLI's: the same, by CLI.
    const offCli = EventsOn("cli:activity", (p: { agent?: string; cli?: string; activity?: CLIActivity | null }) => {
      if (!alive || !p?.agent || !p.cli) return;
      setList((cur) => cur.map((a) => (a.name === p.agent ? { ...a, cliActivity: withCLI(a.cliActivity, p.cli!, p.activity) } : a)));
    });
    const t = window.setInterval(load, 30000);
    return () => { alive = false; window.clearInterval(t); [off, offAct, offCli].forEach((o) => typeof o === "function" && o()); };
  }, []);
  return list;
}

/** What a host is: a pod, a container, a Mac, a VM or a bare machine; empty
 *  when nothing on it has said. "service" is an HA service's IP, standing as
 *  a host with the agents reached at it — which node runs it is not shown. */
export type HostKind = "pod" | "container" | "mac" | "vm" | "metal" | "service" | "";

/** One agent on a host: a SuperAI (queen, worker, machine agent, relay), a
 *  coding CLI ("claude.mac"), or a named agent a SuperAI serves (openclaw). */
export interface HostAgent {
  name: string;
  kind: "superai" | "cli" | "agent";
  role?: "queen" | "worker" | "machine" | "relay" | "standalone" | string;
  about?: string;
  /** live or lost for a SuperAI; working, waiting, idle or installed for a
   *  CLI; configured for an agent only the settings know; standby (or
   *  standby-unready) for an HA service's agent on a host it is not on now. */
  state: string;
  /** For a named agent, what the coding CLI of the same name on this host
   *  is doing (working, waiting, idle): one agent, seen both ways. */
  activity?: string;
  version?: string;
  /** The SuperAI on this host that runs it for core. */
  via?: string;
  mcp?: string[];
  /** The HA service it belongs to, when one keeps it up. */
  service?: string;
}

/** A box agents run on, with what runs on it. Parents come before the hosts
 *  inside them. */
export interface Host {
  id: string;
  name: string;
  hostname?: string;
  kind: HostKind;
  os?: string;
  arch?: string;
  /** The id of the host this one runs inside: a pod's k3s node. */
  parent?: string;
  state: "live" | "lost" | "configured";
  lastSeen?: string;
  /** Its IP addresses, where something has said them (haify's node list). */
  addresses?: string[];
  agents: HostAgent[];
}

/** A service kept up by HA (haify's drbd-reactor promoters): it runs on one
 *  host at a time, reached at its service IP wherever that is. */
export interface HAService {
  name: string;
  provider: string;
  vip?: string;
  /** The hostname it runs on now; empty when it runs nowhere. */
  active?: string;
  standbys: { host: string; ready: boolean }[];
  units: string[];
  state: "active" | "degraded" | "down" | string;
}

export interface HostsView {
  /** Null on a core from before hosts: draw the hive the old way. */
  hosts: Host[] | null;
  services: HAService[];
}

/** The hosts and the HA services, kept current: on hosts:changed and the
 *  events that move an agent, and every half minute. */
export function useHosts(): HostsView {
  const [view, setView] = useState<HostsView>({ hosts: null, services: [] });
  useEffect(() => {
    let alive = true;
    let timer = 0;
    // HiveTopology where the core has it, Hosts where it has only that,
    // nothing on a core from before either.
    const read = async (): Promise<HostsView | null> => {
      try {
        const t = (await HiveTopology()) as unknown as { hosts?: Host[]; services?: HAService[] };
        if (t && Array.isArray(t.hosts)) return { hosts: t.hosts, services: Array.isArray(t.services) ? t.services : [] };
      } catch { /* an older core */ }
      try {
        const h = (await Hosts()) as unknown as Host[];
        if (Array.isArray(h)) return { hosts: h, services: [] };
      } catch { /* older still */ }
      return null;
    };
    const load = () => read().then((v) => {
      if (!alive) return;
      // A failed read after a good one keeps what was there.
      setView((cur) => (v ? { hosts: v.hosts?.map((h) => ({ ...h, agents: h.agents ?? [] })) ?? null, services: v.services } : cur));
    });
    const soon = () => { if (!timer) timer = window.setTimeout(() => { timer = 0; load(); }, 800); };
    load();
    const t = window.setInterval(load, 30000);
    const offs = ["hosts:changed", "agents:changed", "cli:activity"].map((n) => EventsOn(n, soon));
    return () => { alive = false; window.clearInterval(t); window.clearTimeout(timer); offs.forEach((o) => typeof o === "function" && o()); };
  }, []);
  return view;
}
