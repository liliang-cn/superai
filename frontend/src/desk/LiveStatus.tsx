import { Hive, LinkedAgent, elapsed, oneLine, short } from "../canvas/data";
import type { CodingRun } from "../canvas/tiles";
import { machine } from "../lib/machine";
import { useEffect, useState } from "react";
import { HiveWorkers } from "../../wailsjs/go/app/App";
import WorkerKitCard from "../world/WorkerKit";

type Worker = { name: string; kind: string; host: string; state: string; up: boolean };

/** A SuperLeo on its own: no hive members, but the agents that work for it
 *  here (signed-in CLIs, named agents), the same count the status bar has. */
function OwnWorkers({ runs, now }: { runs: CodingRun[]; now: Date }) {
  const [list, setList] = useState<Worker[] | null>(null);
  useEffect(() => {
    let alive = true;
    const pull = () => HiveWorkers().then((w) => { if (alive) setList((w ?? []) as Worker[]); }).catch(() => { if (alive) setList([]); });
    pull();
    const t = window.setInterval(pull, 10000);
    return () => { alive = false; window.clearInterval(t); };
  }, []);
  if (list === null) return null;
  if (!list.length) return <div className="dk-live-empty"><b>No workers yet</b><span>This {machine} is on its own.</span></div>;
  const busy = (w: Worker) => runs.filter((r) => r.state === "running" && (r.agent === w.name || r.agent === w.name.split(".")[0])).length;
  return (
    <div className="dk-status">
      <div className="dk-status-sum">
        <div><b>{list.filter((w) => w.up).length}</b><span>of {list.length} up</span></div>
        <div><b>{runs.filter((r) => r.state === "running").length}</b><span>working</span></div>
      </div>
      <div className="dk-status-rows">
        {list.map((w) => {
          const n = busy(w);
          return (
            <div key={w.name} className={!w.up ? "dk-st lost" : n ? "dk-st busy" : "dk-st"}>
              <i />
              <b>{w.name}</b>
              <span className="dk-st-what">{!w.up ? "Not answering" : n ? `${n} run${n > 1 ? "s" : ""} going` : `${w.kind} on ${w.host}`}</span>
              <em>{w.state}</em>
            </div>
          );
        })}
      </div>
    </div>
  );
}

const ago = (iso: string | undefined, now: Date) => (iso ? `${elapsed(iso, now.getTime())} ago` : "");

/** Every member of the hive, one row each: up or not, what it is doing, when
 *  it was last heard from and how long it has been running. */
export default function LiveStatus({ hive, now, agents, runs }: { hive: Hive; now: Date; agents: LinkedAgent[]; runs: CodingRun[] }) {
  // A worker's row opens to its card: its own MCP servers and skills.
  const [open, setOpen] = useState("");
  if (!hive.role) return <OwnWorkers runs={runs} now={now} />;
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
          // Its terminal Claude Code sessions, beside SuperLeo's own runs.
          const cc = a.activity;
          const term = cc?.working ? `claude: ${cc.working} session${cc.working > 1 ? "s" : ""} working` : cc?.waiting ? "claude: waiting for you" : "";
          const what = [going ? `${going} coding run${going > 1 ? "s" : ""}` : "", term].filter(Boolean).join(" · ");
          return (
            <div key={a.name} className={going || cc?.working ? "dk-st busy" : "dk-st"} title={`${a.host} · ${a.os}/${a.arch}${a.version ? " · " + a.version : ""}`}>
              <i />
              <b>{a.name}</b>
              <span className="dk-st-what">{what || (can.length ? can.join(", ") : `${a.os}/${a.arch}, nothing to run yet`)}</span>
              <em>{elapsed(a.connected, now.getTime())}</em>
            </div>
          );
        })}
        {agents.length > 0 && <div className="dk-st-h">Workers</div>}
        {workers.map((m) => {
          const t = running.get(m.name);
          return (
            <div key={m.name}>
              <div className={(m.state === "lost" ? "dk-st lost" : t ? "dk-st busy" : "dk-st") + (open === m.name ? " on" : "")}
                role="button" data-testid={`worker-row-${m.name}`} onClick={() => setOpen(open === m.name ? "" : m.name)}>
                <i />
                <b>{short(m.name)}</b>
                <span className="dk-st-what">{m.state === "lost" ? "Not answering" : t ? oneLine(t.prompt) : "Idle"}</span>
                <em>{t ? elapsed(t.started_at, now.getTime()) : ago(m.last_seen, now)}</em>
              </div>
              {open === m.name && m.state !== "lost" && <div className="dk-st-kit"><WorkerKitCard worker={m.name} /></div>}
            </div>
          );
        })}
      </div>
    </div>
  );
}
