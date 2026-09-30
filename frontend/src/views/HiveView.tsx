import React, { useCallback, useEffect, useRef, useState } from "react";
import { HiveRetire, HiveSpawn, HiveStatus } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { Link, Route, Routes, useNavigate, useParams } from "react-router-dom";
import HiveTaskPage, { FullTask } from "./HiveTaskPage";
import { taskPath } from "../lib/routes";
import HiveStage, { StageHandle, StagePulse, StageTask } from "../components/HiveStage";

/** One worker as HiveStatus reports it. */
interface Member {
  name: string;
  engine?: string;
  url: string;
  version?: string;
  state: "live" | "lost";
  joined_at: string;
  last_seen: string;
}

interface QueenLink {
  url: string;
  joined: boolean;
  last_ok: string;
  error: string;
}

type Task = StageTask & FullTask;

interface Status {
  tasks?: Task[];
  spawner?: { enabled: boolean; max: number };
  role: "" | "queen" | "worker";
  name: string;
  protocol: string;
  interval_ms?: number;
  members: Member[];
  queen?: QueenLink;
}

/** How long ago, in the units a heartbeat is read in. A minute-granularity
 *  "less than a minute" (see lib/format.ts) would hide the one thing this panel
 *  is for: whether a worker spoke a moment ago or has gone quiet. */
function ago(iso: string, now: number): string {
  const t = Date.parse(iso);
  if (!t || t < 0 || iso.startsWith("0001")) return "never";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export default function HiveView() {
  const navigate = useNavigate();
  const [st, setSt] = useState<Status | null>(null);
  const [err, setErr] = useState("");
  const [now, setNow] = useState(Date.now());
  const [tasks, setTasks] = useState<Task[]>([]);
  const [ready, setReady] = useState(false);
  const stage = useRef<StageHandle>(null);
  // What the add/retire buttons are doing, so a spawn that takes half a minute
  // reads as working and not as a dead button.
  const [making, setMaking] = useState<"" | "spawn" | "retire">("");
  const [note, setNote] = useState("");

  const resize = async (kind: "spawn" | "retire") => {
    setMaking(kind);
    setNote("");
    try {
      const r: any = kind === "spawn" ? await HiveSpawn(1) : await HiveRetire(1, false);
      if (!r?.ok) setNote(String(r?.error || "did not work"));
      else if (kind === "spawn" && !r.complete) setNote(`Asked for ${r.replicas}, but only ${r.live} are live yet — the rest are still starting.`);
    } catch (e: any) {
      setNote(String(e?.message || e));
    } finally {
      setMaking("");
      load();
    }
  };

  const load = useCallback(async () => {
    try {
      const next = (await HiveStatus()) as unknown as Status;
      setSt(next);
      // Only the first load seeds the tasks. After that the event stream is the
      // source, and replacing the list from a poll would drop a change that
      // arrived a moment before the poll's answer did.
      setTasks((cur) => (cur.length === 0 && !ready ? next.tasks ?? [] : cur));
      setReady(true);
      setErr("");
    } catch (e: any) {
      setErr(String(e?.message || e));
    }
  }, []);

  // The roster is polled, not pushed: a worker going quiet is the absence of an
  // event, and there is nothing to push for that. Three seconds is under the
  // default heartbeat, so a change shows on the next look rather than the one
  // after.
  // Tasks are pushed. Each event is the whole task as it now stands, so applying
  // one is a replace-or-append and a missed or repeated event costs nothing.
  useEffect(() => {
    const off = EventsOn("hive:task", (t: Task) => {
      if (!t?.id) return;
      setTasks((cur) => {
        const i = cur.findIndex((k) => k.id === t.id);
        if (i < 0) return [...cur, t].slice(-80);
        const next = cur.slice();
        next[i] = t;
        return next;
      });
    });
    return () => {
      if (typeof off === "function") off();
    };
  }, []);

  // The flicker goes straight to the stage and nowhere else. It is not state:
  // a re-render per pulse would be a re-render per token.
  useEffect(() => {
    const off = EventsOn("hive:pulse", (p: StagePulse) => stage.current?.pulse(p));
    return () => {
      if (typeof off === "function") off();
    };
  }, []);

  useEffect(() => {
    load();
    const poll = window.setInterval(load, 3000);
    const tick = window.setInterval(() => setNow(Date.now()), 1000);
    return () => {
      window.clearInterval(poll);
      window.clearInterval(tick);
    };
  }, [load]);

  const members = st?.members ?? [];
  const running = tasks.filter((t) => t.state === "running").length;
  const ordered = [...tasks].sort(
    (a, b) =>
      Number(b.state === "running") - Number(a.state === "running") ||
      Date.parse(b.started_at) - Date.parse(a.started_at),
  );
  const elapsed = (t: Task) => {
    const end = t.ended_at && !t.ended_at.startsWith("0001") ? Date.parse(t.ended_at) : now;
    const s = Math.max(0, Math.round((end - Date.parse(t.started_at)) / 1000));
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
  };
  const live = members.filter((m) => m.state === "live").length;

  const overview = (
    <div className="view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Hive</div>
          <div className="view-desc">
            SuperAI instances sharing one memory. The queen gives the orders; workers announce themselves and carry them out.
          </div>
        </div>
      </div>

      <div className="panel-scroll">
        {err && <div className="hint err">{err}</div>}
        {st && (
          <>
            <HiveStage ref={stage} role={st.role} self={st.name} workers={members} tasks={tasks} ready={ready} />

            <div className="card hive-self">
              <span className={`hive-role ${st.role || "alone"}`}>{st.role || "standalone"}</span>
              <div className="hive-self-body">
                <div className="hive-self-name">{st.name || "this instance"}</div>
                <div className="hive-dim">
                  {st.role === "queen" && `Accepting workers · ${live} live of ${members.length}`}
                  {st.role === "worker" && "Announces itself to the queen and obeys her"}
                  {st.role === "" && "Working on its own. Nothing here is required."}
                </div>
              </div>
              <span className="chip">{st.protocol}</span>
            </div>

            {st.role === "worker" && st.queen && (
              <div className="card hive-link">
                <span className={`status-dot ${st.queen.joined ? "ok" : "bad"}`} />
                <div className="hive-self-body">
                  <div className="hive-self-name">
                    {st.queen.joined ? "Joined the queen" : "Not joined yet"}
                  </div>
                  <div className="hive-dim hive-mono">{st.queen.url}</div>
                  {st.queen.joined ? (
                    <div className="hive-dim">last heard {ago(st.queen.last_ok, now)}</div>
                  ) : (
                    <div className="hive-dim">
                      {st.queen.error || "trying…"} — keeps trying, in any start order.
                    </div>
                  )}
                </div>
              </div>
            )}

            {st.role === "queen" && (
              <div className="card">
                <div className="hive-head-row">
                  <div className="card-title">Workers ({members.length})</div>
                  {st.spawner?.enabled && (
                    <div className="hive-spawn">
                      <button className="btn ghost sm" disabled={making !== ""} onClick={() => resize("retire")}>
                        {making === "retire" ? "Retiring…" : "− Worker"}
                      </button>
                      <button className="btn ghost sm" disabled={making !== ""} onClick={() => resize("spawn")}>
                        {making === "spawn" ? "Starting…" : "+ Worker"}
                      </button>
                    </div>
                  )}
                </div>
                {note && <div className="hint err" style={{ margin: "6px 0" }}>{note}</div>}
                {members.length === 0 ? (
                  <div className="hive-empty">
                    <p>No workers have joined yet.</p>
                    <p className="hive-dim">
                      A worker joins by itself: give it <code>hive.role: "worker"</code> and{" "}
                      <code>hive.join_url</code> pointing here, plus a reachable{" "}
                      <code>hive.advertise_url</code>. This roster is whoever is actually there.
                    </p>
                  </div>
                ) : (
                  <div className="hive-table" role="table">
                    <div className="hive-row hive-head" role="row">
                      <span />
                      <span>Worker</span>
                      <span>Address</span>
                      <span>Joined</span>
                      <span>Last heard</span>
                    </div>
                    {members.map((m) => (
                      <div className={`hive-row${m.state === "lost" ? " lost" : ""}`} role="row" key={m.name}>
                        <span
                          className={`status-dot ${m.state === "live" ? "ok" : "unknown"}`}
                          title={m.state}
                        />
                        <span className="hive-name">
                          {m.name}
                          {m.engine && <span className="chip" title="Not a SuperAI: an agent behind an adapter">{m.engine}</span>}
                          {m.state === "lost" && <span className="chip">lost</span>}
                        </span>
                        <span className="hive-mono hive-dim">{m.url}</span>
                        <span className="hive-dim">{ago(m.joined_at, now)}</span>
                        <span className={m.state === "lost" ? "hive-warn" : "hive-dim"}>{ago(m.last_seen, now)}</span>
                      </div>
                    ))}
                  </div>
                )}
                <div className="hive-foot hive-dim">
                  A worker that stays quiet for three heartbeats
                  {st.interval_ms ? ` (${Math.round((st.interval_ms * 3) / 1000)}s)` : ""} is marked lost and is not
                  given orders; it returns to live by speaking again.
                </div>
              </div>
            )}

            {st.role !== "" && (
              <div className="card">
                <div className="card-title">
                  Missions{running > 0 ? ` · ${running} running` : ""}
                </div>
                {ordered.length === 0 ? (
                  <div className="hive-dim">
                    {st.role === "queen"
                      ? "Nothing has been ordered yet. Ask this queen to have the workers do something and it appears here as it happens."
                      : "No orders received yet. When the queen sends this worker something, it appears here as it happens."}
                  </div>
                ) : (
                  <div className="hive-missions">
                    {ordered.map((t) => (
                      <div className="hive-mission" key={t.id}>
                        <div className="hive-m-head" onClick={() => navigate(taskPath(t.id))}>
                          <span className={`hive-live-dot ${t.state}`} title={t.state} />
                          <span className="hive-m-who">{t.dir === "peer"
                              ? `${(t.from ?? "").replace(/^superai-/, "")} ⇄ ${t.worker.replace(/^superai-/, "")}`
                              : t.dir === "out"
                                ? `→ ${t.worker.replace(/^superai-/, "")}`
                                : "← queen"}</span>
                          <span className="hive-m-prompt" title={t.prompt}>{t.prompt}</span>
                          <span className="hive-m-phase">
                            {t.state === "running"
                              ? t.phase === "tool"
                                ? `⚙ ${t.tool || "tool"}`
                                : t.phase === "writing"
                                  ? "writing…"
                                  : "thinking…"
                              : t.state}
                            {t.tools > 0 ? ` · ${t.tools} tool${t.tools === 1 ? "" : "s"}` : ""}
                          </span>
                          <span className="hive-m-meta">
                            <Link to={taskPath(t.id)} className="hive-uuid" onClick={(e) => e.stopPropagation()} title={t.id}>
                              {t.id.slice(0, 8)}
                            </Link>{" "}
                            {elapsed(t)}
                          </span>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}

            {st.role === "" && (
              <div className="card">
                <div className="card-title">Joining a hive</div>
                <div className="hive-dim">
                  This instance is complete by itself. To make it a queen, set <code>hive.role: "queen"</code>. To make
                  it a worker, set <code>hive.role: "worker"</code>, <code>hive.join_url</code> and{" "}
                  <code>hive.advertise_url</code>. Either way it keeps working alone if the hive is gone.
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );

  // The overview, and a page per task at /hive/tasks/<uuid>. The live list
  // stays up here so a task page keeps moving while it is open.
  return (
    <Routes>
      <Route path="tasks/:id" element={<TaskRoute tasks={tasks} now={now} />} />
      <Route path="*" element={overview} />
    </Routes>
  );
}

function TaskRoute({ tasks, now }: { tasks: Task[]; now: number }) {
  const { id = "" } = useParams();
  return (
    <div className="view">
      <div className="panel-scroll">
        <HiveTaskPage id={id} live={tasks.find((t) => t.id === id)} now={now} />
      </div>
    </div>
  );
}
