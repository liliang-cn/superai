import { Hive, elapsed, oneLine, short } from "../canvas/data";

const ago = (iso: string | undefined, now: Date) => (iso ? `${elapsed(iso, now.getTime())} ago` : "");

/** Every member of the hive, one row each: up or not, what it is doing, when
 *  it was last heard from and how long it has been running. */
export default function LiveStatus({ hive, now }: { hive: Hive; now: Date }) {
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
