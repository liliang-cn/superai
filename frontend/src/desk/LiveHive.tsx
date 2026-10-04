import { useMemo } from "react";
import type { CanvasTheme } from "../canvas/theme";
import type { CodingRun } from "../canvas/tiles";
import { Hive, HiveTask, LinkedAgent, oneLine, short } from "../canvas/data";

const R = 34; // a cell's radius
const SQ3 = Math.sqrt(3);

/** Cell centres spiralling out from the queen: ring one, then ring two. */
function spiral(n: number): [number, number][] {
  const dirs = [[1, 0], [1, -1], [0, -1], [-1, 0], [-1, 1], [0, 1]];
  const out: [number, number][] = [];
  for (let k = 1; out.length < n; k++) {
    let q = -k, r = k; // start k steps along dir 4
    for (let i = 0; i < 6 && out.length < n; i++) {
      for (let j = 0; j < k && out.length < n; j++) {
        out.push([R * SQ3 * (q + r / 2), R * 1.5 * r]);
        q += dirs[i][0];
        r += dirs[i][1];
      }
    }
  }
  return out;
}

const hexPath = (r: number) => {
  const p = Array.from({ length: 6 }, (_, i) => {
    const a = (Math.PI / 3) * i - Math.PI / 2;
    return `${(r * Math.cos(a)).toFixed(1)} ${(r * Math.sin(a)).toFixed(1)}`;
  });
  return `M${p.join(" L")} Z`;
};
const CELL = hexPath(R - 1);
const QUEEN = hexPath(R + 3);

const clock = (iso?: string) => (iso ? new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }) : "");

/** What a worker is doing, said short enough for a label. */
function doing(t: HiveTask): string {
  const s = oneLine(t.prompt);
  return s.length > 34 ? s.slice(0, 33) + "…" : s;
}

/**
 * The hive, live: the queen in the middle of her comb, every worker a cell,
 * the busy ones lit with what they are doing; the machines that run coding
 * agents sit outside the comb, linked to her. Under it, the last few things
 * that happened.
 */
export default function LiveHive({ hive, runs, agents, now }: { hive: Hive; runs: CodingRun[]; agents: LinkedAgent[]; theme?: CanvasTheme; now: Date }) {
  const workers = hive.members.filter((m) => !/queen/.test(m.name));
  const running = hive.tasks.filter((t) => t.state === "running");
  const busyBy = new Map<string, HiveTask>();
  running.forEach((t) => { if (!busyBy.has(t.worker)) busyBy.set(t.worker, t); });
  const cells = useMemo(() => spiral(workers.length), [workers.length]);
  // The machines core can run things on: its linked agents (or, before any
  // linked, wherever coding runs have run).
  const machines = useMemo(() => {
    if (agents.length) return agents.map((a) => a.name).slice(0, 8);
    const names = new Set<string>();
    runs.forEach((r) => names.add(r.remote || "this Mac"));
    return [...names].slice(0, 3);
  }, [runs, agents]);
  // Busy: a coding run there, or an order out to one of its agents —
  // "codex.mac" for a CLI on it, "openclaw" for a named agent it serves.
  // What a busy machine is doing, in a few words under its name: the agent
  // and how long it has been at it — "codex · 3m".
  const machineDoing = (m: string): string => {
    const r = runs.find((x) => (x.remote || "this Mac") === m && x.state === "running");
    const t = running.find((x) => x.dir === "out" && (x.worker.endsWith("." + m) ||
      agents.some((a) => a.name === m && (a.agents ?? []).some((n) => n.name === x.worker))));
    const what = r ? r.agent.split(".")[0] : t ? t.worker.split(".")[0] : "";
    const since = r?.started ?? t?.started_at;
    if (!what) return "";
    const mins = since ? Math.max(0, Math.floor((now.getTime() - new Date(since).getTime()) / 60000)) : 0;
    return `${what} · ${mins < 1 ? "working" : `${mins}m`}`;
  };
  const machineBusy = (m: string) =>
    runs.some((r) => (r.remote || "this Mac") === m && r.state === "running") ||
    running.some((t) => t.dir === "out" && (t.worker.endsWith("." + m) ||
      agents.some((a) => a.name === m && (a.agents ?? []).some((n) => n.name === t.worker))));


  const recent = hive.tasks.filter((t) => t.state !== "running").slice(0, 3);
  const span = Math.max(130, ...cells.map(([x, y]) => Math.max(Math.abs(x), Math.abs(y)) + R + 6));
  // Room for the labels either side, and for the machines under the comb.
  const reach = machines.length ? 1.5 * 104 + 50 : 0;
  const w = Math.max(span + (busyBy.size ? 200 : 20), reach);
  const top = -span - 16, height = span * 2 + 16 + (machines.length ? 120 + Math.floor((machines.length - 1) / 4) * 92 : 30);

  if (!hive.loaded) return <div className="dk-live-empty">Finding the hive…</div>;
  if (!hive.role) {
    return (
      <div className="dk-live-empty">
        <b>No hive here</b>
        <span>This Mac is on its own. Link it to your hive in Settings to watch the queen and her workers here.</span>
      </div>
    );
  }

  return (
    <div className="dk-hive">
      <div className="dk-hud">
        <b>The hive</b>
        <span>{workers.length + 1} members, {busyBy.size + machines.filter(machineBusy).length} working</span>
        <span className="dk-clock">{clock(now.toISOString())}</span>
      </div>
      <svg className="dk-comb" viewBox={`${-w} ${top} ${w * 2} ${height}`} preserveAspectRatio="xMidYMid meet">
        <defs>
          <filter id="dk-glow" x="-60%" y="-60%" width="220%" height="220%"><feGaussianBlur stdDeviation="8" /></filter>
        </defs>
        {/* orders out: queen to each busy worker */}
        {workers.map((m, i) => busyBy.has(m.name) && (
          <line key={"o" + m.name} x1={0} y1={0} x2={cells[i][0]} y2={cells[i][1]} className="dk-order" />
        ))}
        {machines.map((m, i) => {
          const [x, y] = machineAt(i, span);
          return <path key={"l" + m} d={`M0 0 C ${x * 0.4} ${y * 0.9}, ${x * 0.8} ${y * 0.9}, ${x} ${y - 22}`} className={machineBusy(m) ? "dk-wire on" : "dk-wire"} />;
        })}
        {workers.map((m, i) => busyBy.has(m.name) && (
          <circle key={"g" + m.name} cx={cells[i][0]} cy={cells[i][1]} r={R} className="dk-glow" filter="url(#dk-glow)" />
        ))}
        {workers.map((m, i) => {
          const [x, y] = cells[i];
          const busy = busyBy.has(m.name);
          return (
            <g key={m.name} transform={`translate(${x} ${y})`} className={busy ? "dk-cell busy" : m.state === "lost" ? "dk-cell lost" : "dk-cell"}>
              <title>{m.name}{busy ? `: ${doing(busyBy.get(m.name)!)}` : ""}</title>
              <path d={CELL} />
              <text y={4}>{short(m.name)}</text>
            </g>
          );
        })}
        <g className="dk-queen">
          <path d={QUEEN} />
          <text y={5}>Q</text>
        </g>
        {machines.map((m, i) => {
          const [x, y] = machineAt(i, span);
          const doingHere = machineDoing(m);
          return (
            <g key={"m" + m} transform={`translate(${x} ${y})`} className={doingHere || machineBusy(m) ? "dk-mac on" : "dk-mac"}>
              {doingHere && <rect className="dk-mac-ring" x={-34} y={-22} width={68} height={44} rx={11} />}
              <rect x={-34} y={-22} width={68} height={44} rx={11} />
              <path d="M-12 -9 h24 v13 h-24 z M-16 8 h32" />
              {doingHere && <path className="dk-mac-type" d="M-8 -4 h6 M0 -4 h8 M-8 0 h10" />}
              <text y={38}>{m}</text>
              {doingHere && <text y={54} className="dk-mac-doing">{doingHere}</text>}
            </g>
          );
        })}
        {/* what the busy ones are doing, beside them, outward */}
        {workers.map((m, i) => {
          const t = busyBy.get(m.name);
          if (!t) return null;
          const [x, y] = cells[i];
          const right = x >= 0;
          return (
            <foreignObject key={"c" + m.name} x={right ? x + R + 4 : x - R - 4 - 220} y={y - 15} width={220} height={30}>
              <div className={right ? "dk-chipbox" : "dk-chipbox left"}><div className="dk-chip"><b>{short(m.name)}</b><span>{doing(t)}</span></div></div>
            </foreignObject>
          );
        })}
      </svg>
      {recent.length > 0 && (
        <div className="dk-ticker">
          {recent.map((t) => (
            <div key={t.id}>
              <em>{clock(t.started_at)}</em>
              <span><strong>{short(t.worker)}</strong> {t.state === "failed" ? "failed: " : ""}{oneLine(t.result || t.prompt).slice(0, 90)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** Where the n-th machine sits: below the comb, spread to the right. */
function machineAt(i: number, span: number): [number, number] {
  const row = Math.floor(i / 4), col = i % 4;
  return [(col - 1.5) * 104, span + 52 + row * 92];
}
