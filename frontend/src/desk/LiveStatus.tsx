import { Hive, LinkedAgent, elapsed, oneLine, short } from "../canvas/data";
import type { CodingRun } from "../canvas/tiles";

const ago = (iso: string | undefined, now: Date) => (iso ? `${elapsed(iso, now.getTime())} ago` : "");

/** Every member of the hive, one row each: up or not, what it is doing, when
 *  it was last heard from and how long it has been running. */
export default function LiveStatus({ hive, now, agents, runs }: { hive: Hive; now: Date; agents: LinkedAgent[]; runs: CodingRun[] }) {
  if (!hive.role) return <div className="dk-live-empty"><b>No hive here</b><span>This Mac is on its own.</span></div>;
  const running = new Map(hive.tasks.filter((t) => t.state === "running").map((t) => [t.worker, t]));
  const workers = hive.members.filter((m) => !/queen/.test(m.name));
  const live = workers.filter((m) => m.state === "live").length;
  return (
    <div className="dk-status">
      <div className="dk-status-sum">
        <div><b>{live}</b><span>of {workers.length} up</span></div>
        <div><b>{running.size}</b><span>working</span></div>
        <div><b>{hive.tasks.filter((t) => t.state === "failed").length}</b><span>failed lately</span></div>
      </div>
      <div className="dk-status-rows">
        {agents.length > 0 && <div className="dk-st-h">Agents</div>}
        {agents.map((a) => {
          const going = runs.filter((r) => r.remote === a.name && r.state === "running").length;
          const can = [...a.clis, ...(a.agents ?? []).map((x) => x.name)];
          return (
            <div key={a.name} className={going ? "dk-st busy" : "dk-st"} title={`${a.host} · ${a.os}/${a.arch}${a.version ? " · " + a.version : ""}`}>
              <i />
              <b>{a.name}</b>
              <span className="dk-st-what">{going ? `${going} coding run${going > 1 ? "s" : ""}` : can.length ? can.join(", ") : `${a.os}/${a.arch}, nothing to run yet`}</span>
              <em>{elapsed(a.connected, now.getTime())}</em>
            </div>
          );
        })}
        {agents.length > 0 && <div className="dk-st-h">Workers</div>}
        {workers.map((m) => {
          const t = running.get(m.name);
          return (
            <div key={m.name} className={m.state === "lost" ? "dk-st lost" : t ? "dk-st busy" : "dk-st"}>
              <i />
              <b>{short(m.name)}</b>
              <span className="dk-st-what">{m.state === "lost" ? "Not answering" : t ? oneLine(t.prompt) : "Idle"}</span>
              <em>{t ? elapsed(t.started_at, now.getTime()) : ago(m.last_seen, now)}</em>
            </div>
          );
        })}
      </div>
    </div>
  );
}
