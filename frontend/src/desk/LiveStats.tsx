import { usePulse } from "../components/Reactor";
import type { Hive } from "../canvas/data";
import type { CodingRun } from "../canvas/tiles";

const fmt = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e4 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));

/** The core's figures at a glance: what it has spent, what it has done, what
 *  it is carrying. The full reactor is a click away on Stats. */
export default function LiveStats({ hive, runs }: { hive: Hive; runs: CodingRun[] }) {
  const p = usePulse();
  const figures: [string, string, boolean?][] = [
    [fmt(p.tokens), "tokens this process", true],
    [fmt(p.cached), "cached"],
    [fmt(p.rounds), "turns"],
    [fmt(p.calls), "tool calls"],
    [fmt(p.memory), "memory calls"],
    [fmt(p.fails), "failed"],
    [`${Math.round(p.cpu)}%`, "cpu"],
    [`${Math.round(p.heap / 1048576)} MB`, "heap"],
    [String(hive.tasks.filter((t) => t.state === "running").length), "orders in flight"],
    [String(runs.filter((r) => r.state === "running").length), "coding runs going"],
  ];
  return (
    <div className="dk-stats">
      <div className="dk-stats-head"><i className={p.live ? "dk-livedot" : "dk-off"} />{p.live ? "Working now" : "Idle"}</div>
      <div className="dk-stats-grid">
        {figures.map(([v, l, big]) => (
          <div key={l} className={big ? "dk-fig big" : "dk-fig"}><b>{v}</b><span>{l}</span></div>
        ))}
      </div>
    </div>
  );
}
