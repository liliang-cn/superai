import { useAttention } from "../canvas/attention";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { ChatSessions, HiveRetire, HiveSpawn, HiveStatus, StandingAgents } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { Link, Route, Routes, useNavigate, useParams } from "react-router-dom";
import HiveTaskPage, { FullTask } from "./HiveTaskPage";
import { taskPath } from "../lib/routes";
import { focusInWorld, isSelected, useWorldSelection } from "../world/bus";
import type { StageHandle, StagePulse, StageTask } from "../components/HiveStage";
import { QUEEN, SELF } from "../components/hiveFx";
import { useHiveMeter } from "../components/HiveMeter";

const short = (n: string) => n.replace(/^superai-/, "");
const fmtK = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));

/** The conversations' titles by id, read now and every half minute. A bee's
 *  wake is named after the bee, not after the brief it was woken with. */
function useSessionTitles(): Record<string, string> {
  const [titles, setTitles] = useState<Record<string, string>>({});
  useEffect(() => {
    const load = () => Promise.all([ChatSessions(), StandingAgents().catch(() => [])]).then(([l, bees]) => {
      const out: Record<string, string> = {};
      for (const s of (l ?? []) as { id: string; title: string }[]) {
        out[s.id] = s.title.startsWith("You hold a standing responsibility") ? "A bee's check" : s.title;
      }
      for (const b of (bees ?? []) as { name: string; lastWake?: { session_id?: string }; running?: { session_id?: string } }[]) {
        for (const w of [b.lastWake, b.running]) if (w?.session_id) out[w.session_id] = `${b.name} (bee)`;
      }
      setTitles(out);
    }).catch(() => {});
    load();
    const t = window.setInterval(load, 30000);
    return () => window.clearInterval(t);
  }, []);
  return titles;
}

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
  name?: string;
  joined: boolean;
  last_ok: string;
  error: string;
}

type Task = StageTask & FullTask;

/** A note between two members. dir is this instance's side of it: sent,
 *  received, or — on a queen — seen passing between two workers. */
interface Mail {
  id: string;
  from: string;
  to: string;
  text: string;
  reply_to?: string;
  at: string;
  dir?: "in" | "out" | "peer";
}

interface Status {
  tasks?: Task[];
  spawner?: { enabled: boolean; max: number };
  role: "" | "queen" | "worker";
  name: string;
  protocol: string;
  interval_ms?: number;
  members: Member[];
  queen?: QueenLink;
  messages?: Mail[];
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
  const sel = useWorldSelection();
  const navigate = useNavigate();
  // The swarm view folds away and stays the way it was left.
  const [st, setSt] = useState<Status | null>(null);
  const [err, setErr] = useState("");
  const [now, setNow] = useState(Date.now());
  const [tasks, setTasks] = useState<Task[]>([]);
  const [ready, setReady] = useState(false);
  const stage = useRef<StageHandle>(null);
  const [mail, setMail] = useState<Mail[]>([]);
  // The latest status, for the event handlers below, which are set up once.
  const stRef = useRef<Status | null>(null);
  stRef.current = st;
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
      setMail((cur) => (cur.length === 0 && !ready ? (next.messages ?? []).map((m) => ({ ...m, dir: "in" as const })) : cur));
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

  // Messages go to the stage as a beam between their two ends, and into the
  // feed. A name the stage does not draw (a worker's peers, on a worker) has
  // no beam, only its line in the feed.
  useEffect(() => {
    const stageId = (name: string): string | undefined => {
      const s = stRef.current;
      if (!s) return undefined;
      if (name === s.name) return s.role === "worker" ? SELF : QUEEN;
      if (s.role === "worker") return name === (s.queen?.name || "queen") || name === "queen" ? QUEEN : undefined;
      return s.members.some((m) => m.name === name) ? name : undefined;
    };
    const off = EventsOn("hive:message", (m: Mail) => {
      if (!m?.id) return;
      setMail((cur) => (cur.some((x) => x.id === m.id && x.dir === m.dir) ? cur : [...cur, m].slice(-60)));
      const src = stageId(m.from);
      const dst = stageId(m.to);
      if (src && dst) {
        stage.current?.pulse({ task: m.id, worker: m.from, dir: "peer", kind: "message", src, dst, text: m.text });
      }
    });
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
  const waiting = useAttention().filter((i) => i.kind === "approval" || i.kind === "bee").length;
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

  // The workers' meters (tokens, what each is doing) and the conversations'
  // titles, for the table and for naming the requests orders came from.
  const meter = useHiveMeter().members;
  const titles = useSessionTitles();
  const tokensOf = (name: string) => meter.find((m) => m.name === name)?.tokens ?? 0;
  const doingOf = (name: string) => tasks.find((t) => t.worker === name && t.state === "running");
  // Orders grouped by the conversation that gave them, newest request first.
  const groups = (() => {
    const by = new Map<string, Task[]>();
    for (const t of ordered) {
      const k = (t as Task & { session?: string }).session || "";
      by.set(k, [...(by.get(k) ?? []), t]);
    }
    return [...by.entries()].sort(([, x], [, y]) =>
      Math.max(...y.map((t) => Date.parse(t.started_at))) - Math.max(...x.map((t) => Date.parse(t.started_at))));
  })();
  const who = (t: Task) => t.dir === "peer"
    ? `${short(t.from ?? "")} ⇄ ${short(t.worker)}`
    : t.dir === "out" ? short(t.worker) : "queen";
  const phase = (t: Task) => (t.state === "running"
    ? t.phase === "tool" ? `⚙ ${t.tool || "tool"}` : t.phase === "writing" ? "writing…" : "thinking…"
    : t.state) + (t.tools > 0 ? ` · ${t.tools} tool${t.tools === 1 ? "" : "s"}` : "");

  const overview = (
    <div className="view hive-page">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Hive</div>
          {st && st.role !== "" && (
            <div className="view-desc">
              {st.role === "queen" ? (st.name && st.name !== "queen" ? `${st.name} · queen` : "queen") : `${st.name} · worker of ${st.queen?.name || "the queen"}`} · {st.protocol}
            </div>
          )}
        </div>
        {st?.role === "queen" && st.spawner?.enabled && (
          <div className="vh-actions hive-spawn">
            <button className="btn ghost sm" disabled={making !== ""} onClick={() => resize("retire")}>
              {making === "retire" ? "Retiring…" : "− Worker"}
            </button>
            <button className="btn sm" disabled={making !== ""} onClick={() => resize("spawn")}>
              {making === "spawn" ? "Starting…" : "+ Worker"}
            </button>
          </div>
        )}
      </div>

      <div className="panel-scroll">
        {err && <div className="hint err">{err}</div>}
        {note && <div className="hint err">{note}</div>}
        {st && (
          <>
            {st.role !== "" && (
              <div className="hive-sum">
                <div><b>{live}<small>/{members.length}</small></b><span>online</span></div>
                <div className={running > 0 ? "busy" : ""}><b>{new Set(tasks.filter((t) => t.state === "running").map((t) => t.worker)).size}</b><span>busy</span></div>
                <div className={members.length - live > 0 ? "bad" : ""}><b>{members.length - live}</b><span>not answering</span></div>
                <div className={waiting > 0 ? "wait" : ""}><b>{waiting}</b><span>waiting for you</span></div>
              </div>
            )}

            {/* Who is in the hive, and what each is doing now. */}
            {st.role === "queen" && (
              <div className="card">
                <div className="card-title">Workers</div>
                {members.length === 0 ? (
                  <div className="hive-dim">No workers yet. + Worker starts one; a SuperAI set to join this queen joins by itself.</div>
                ) : (
                  <div className="hive-roster" role="table">
                    {members.map((m) => {
                      const t = doingOf(m.name);
                      return (
                        <div className={`hive-roster-row${m.state === "lost" ? " lost" : ""}${t ? " busy" : ""}${isSelected(sel, "worker", m.name) ? " on" : ""}`} role="row" key={m.name}
                          onClick={() => focusInWorld("worker", m.name)}>
                          <span className={`hive-live-dot ${t ? "running" : m.state === "live" ? "done" : "failed"}`} title={m.state} />
                          <span className="hive-roster-name">
                            <b>{short(m.name)}</b>
                            {m.engine && <span className="chip">{m.engine}</span>}
                          </span>
                          <span className="hive-roster-doing" title={t?.prompt}>
                            {t ? <><em>{phase(t)}</em> {t.prompt}</> : m.state === "lost" ? "not answering" : <span className="hive-dim">idle</span>}
                          </span>
                          <span className="hive-roster-num">{fmtK(tokensOf(m.name))} <small>tokens</small></span>
                          <span className="hive-dim hive-roster-seen">{m.state === "lost" ? `last heard ${ago(m.last_seen, now)}` : `up ${ago(m.joined_at, now).replace(/ ago$/, "")}`}</span>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            )}

            {st.role === "worker" && st.queen && (
              <div className="card hive-link">
                <span className={`status-dot ${st.queen.joined ? "ok" : "bad"}`} />
                <div className="hive-self-body">
                  <div className="hive-self-name">{st.queen.joined ? "Joined the queen" : "Not joined yet"}</div>
                  <div className="hive-dim hive-mono">{st.queen.url}</div>
                  <div className="hive-dim">{st.queen.joined ? `last heard ${ago(st.queen.last_ok, now)}` : st.queen.error || "trying…"}</div>
                </div>
              </div>
            )}

            {/* Every order, grouped by the request it came from. */}
            {st.role !== "" && (
              <div className="card">
                <div className="card-title">Missions{running > 0 ? ` · ${running} running` : ""}</div>
                {groups.length === 0 ? (
                  <div className="hive-dim">
                    {st.role === "queen" ? "No orders yet. Ask the queen for something big and the parts she hands out appear here." : "No orders received yet."}
                  </div>
                ) : (
                  <div className="hive-groups">
                    {groups.map(([session, list]) => {
                      const live = list.filter((t) => t.state === "running").length;
                      return (
                        <div className="hive-group" key={session || "none"}>
                          <div className="hive-group-h">
                            <b>{session ? titles[session] || "A conversation" : "Orders"}</b>
                            <span className="hive-dim">{list.length} part{list.length === 1 ? "" : "s"}{live ? ` · ${live} running` : ""} · {ago(list[list.length - 1].started_at, now)}</span>
                          </div>
                          {list.map((t) => (
                            <button className="hive-group-row" key={t.id} onClick={() => navigate(taskPath(t.id))}>
                              <span className={`hive-live-dot ${t.state}`} title={t.state} />
                              <span className="hive-group-who">{who(t)}</span>
                              <span className="hive-group-p" title={t.prompt}>{t.prompt}</span>
                              <span className="hive-group-ph">{phase(t)}</span>
                              <span className="hive-dim hive-group-t">{elapsed(t)}</span>
                            </button>
                          ))}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            )}

            {st.role !== "" && mail.length > 0 && (
              <div className="card">
                <div className="card-title">Messages · {mail.length}</div>
                <div className="hive-mail">
                  {[...mail].reverse().map((m) => (
                    <div className={`hive-mail-row ${m.dir || "in"}`} key={`${m.id}-${m.dir}`}>
                      <span className="hive-mail-who">
                        {short(m.from)} → {short(m.to)}
                        {m.reply_to && <span className="chip">reply</span>}
                      </span>
                      <span className="hive-mail-text">{m.text}</span>
                      <span className="hive-dim">{ago(m.at, now)}</span>
                    </div>
                  ))}
                </div>
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
