import type { Host, HostAgent } from "../canvas/data";
import type { AgentInfo } from "../lib/useAgentMentions";

/**
 * What the command bar's @ offers from the world: every agent that can take
 * work, by the rule HiveWorkers counts them (the status bar's "n/m workers"):
 * a SuperLeo only when it is a worker, every coding CLI, every named agent.
 *
 * A CLI on this machine is offered by its bare name (@claude), the name the
 * core routes to its own CLI; one on another machine keeps its machine
 * (@codex.sds-a). A hive worker is found by its short name too (w3).
 */
export function mentionsOf(hosts: Host[] | null | undefined): AgentInfo[] {
  const list = hosts ?? [];
  // The SuperLeo drawn as this core: its CLIs are the local ones.
  const self = new Set<string>();
  for (const h of list) for (const a of h.agents ?? []) {
    if (a.kind === "superleo" && (a.role === "standalone" || a.role === "queen")) self.add(a.name);
  }
  const out = new Map<string, AgentInfo & { standby: boolean }>();
  for (const h of list) for (const a of h.agents ?? []) {
    if (a.kind === "superleo" && a.role !== "worker") continue;
    const info = infoOf(a, h, self);
    const standby = a.state.startsWith("standby");
    const had = out.get(info.name);
    // An HA agent is drawn on every host it can run on; the one it is on wins.
    if (had && !(had.standby && !standby)) continue;
    out.set(info.name, { ...info, standby });
  }
  return [...out.values()].map(({ standby: _s, ...a }) => a);
}

function infoOf(a: HostAgent, h: Host, self: Set<string>): AgentInfo {
  const where = h.name || h.hostname || h.id;
  if (a.kind === "cli") {
    const bare = a.via && a.name.endsWith("." + a.via) ? a.name.slice(0, -(a.via.length + 1)) : a.name.split(".")[0];
    const local = !!a.via && self.has(a.via);
    return { name: local ? bare : a.name, alias: local ? undefined : bare, about: `coding agent on ${where} · ${a.state}` };
  }
  if (a.kind === "superleo") {
    const n = /^superleo-worker-(\d+)$/.exec(a.name);
    return { name: a.name, alias: n ? "w" + n[1] : undefined, about: `hive worker on ${where} · ${a.state}` };
  }
  return { name: a.name, about: `${a.about || "agent"} · ${where} · ${a.state}` };
}
