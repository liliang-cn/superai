import { useEffect, useState } from "react";
import { HivePulse } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";

/** One member's meter, as the core reports it (app.MemberPulse). */
export interface Member {
  name: string; live: boolean; tokens: number; cached: number; rounds: number; calls: number;
  fails: number; mcp: number; cpu: number; heap: number; tokPerSec: number; doing?: string; round?: number; at: string;
}

const fmtK = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));

function total(ms: Member[]): Member {
  const t: Member = { name: "hive", live: false, tokens: 0, cached: 0, rounds: 0, calls: 0, fails: 0, mcp: 0, cpu: 0, heap: 0, tokPerSec: 0, at: "" };
  for (const m of ms) {
    t.live ||= m.live; t.tokens += m.tokens; t.cached += m.cached; t.rounds += m.rounds;
    t.calls += m.calls; t.fails += m.fails; t.mcp += m.mcp; t.tokPerSec += m.tokPerSec;
  }
  return t;
}

/**
 * The whole hive's meter: the queen's process and every linked agent's, as
 * the summaries arrive (every two seconds from each), plus their sum. A full
 * read every fifteen seconds drops members that went away.
 */
export function useHiveMeter(): { members: Member[]; total: Member } {
  const [members, setMembers] = useState<Member[]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => HivePulse().then((h) => { if (alive) setMembers((h?.members ?? []) as unknown as Member[]); }).catch(() => {});
    load();
    const t = window.setInterval(load, 15000);
    const off = EventsOn("hive:meter", (p: { member?: Member }) => {
      const m = p?.member;
      if (!m) return;
      setMembers((prev) => {
        const i = prev.findIndex((x) => x.name === m.name);
        if (i < 0) return [...prev, m];
        const next = prev.slice();
        next[i] = m;
        return next;
      });
    });
    return () => { alive = false; window.clearInterval(t); if (typeof off === "function") off(); };
  }, []);
  // The queen first, then whoever is working, then by what they have used.
  const sorted = [...members].sort((a, b) =>
    a.name === "queen" ? -1 : b.name === "queen" ? 1 : a.live !== b.live ? (a.live ? -1 : 1) : b.tokens - a.tokens);
  return { members: sorted, total: total(members) };
}
