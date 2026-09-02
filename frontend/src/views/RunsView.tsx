import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";
import {
  GraphView as startGraphView,
  LongRunList,
  LongRunStart,
  LongRunState,
  LongRunStop,
} from "../../wailsjs/go/main/App";
import { useImeGuard } from "@/lib/ime";

/**
 * The long-run control room.
 *
 * A task that takes hours is many runs, not a long run: the supervisor calls
 * the agent, reads why it stopped, and calls it again, with the plan and the
 * workspace as the only things carried across. This page is the window onto
 * that — every model turn, tool call, lint verdict, compaction, retry and
 * segment boundary, drawn as it happens.
 *
 * Every number here comes from the backend's RunWall, which is agent-go's
 * Observer kept as state instead of written to a log. The page never derives
 * a fact of its own; it lays out what the wall knows. It refetches the whole
 * snapshot on a debounced tick rather than applying deltas, because a
 * snapshot assembled from many events shows many different moments.
 */

// --- shapes, mirroring backend/runwall.go ---
interface RoundStat {
  segment: number; round: number; tokens: number; cached: number; tools: number;
  text: number; durMs: number; compacted?: boolean; lint?: string; retried?: boolean; failed?: boolean;
}
interface SegmentStat {
  index: number; sessionId: string; startedAt: string; endedAt?: string;
  stopReason?: string; productive: boolean; costUsd: number; err?: string; rounds: number;
}
interface LogLine { at: string; kind: string; text: string }
interface PlanItem { text: string; done: boolean; note?: string }
interface TaskState {
  taskId: string; goal: string; model: string; startedAt: string; endedAt?: string;
  done: boolean; running: boolean; stop?: string; final?: string; maxSegments: number;
  segments: SegmentStat[]; rounds: RoundStat[]; plan: PlanItem[];
  toolCounts: Record<string, number>; toolCalls: number; toolErrors: number;
  lints: Record<string, number>; lintRetries: number; lintBlocks: number;
  compactions: number; retries: number; checkpoints: number; errors: number;
  totalTokens: number; totalCached: number; costUsd: number; log: LogLine[];
}
interface TaskSummary {
  taskId: string; goal: string; startedAt: string; running: boolean; done: boolean;
  stop?: string; segments: number; rounds: number;
}

const SERVED = Boolean((window as unknown as Record<string, unknown>).superaiServed);
const GRAPH_SRC = SERVED ? "/graph/" : null;

const fmtK = (n: number) =>
  n >= 1_000_000 ? (n / 1_000_000).toFixed(2) + "M" : n >= 1000 ? (n / 1000).toFixed(1) + "k" : String(n);
const pct = (a: number, b: number) => (b > 0 ? Math.round((100 * a) / b) : 0);
const hhmmss = (d: Date) => d.toTimeString().slice(0, 8);
const short = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + "…" : s);
const elapsed = (from: string, to?: string) => {
  const a = new Date(from).getTime();
  const b = to ? new Date(to).getTime() : Date.now();
  if (!a) return "—";
  const s = Math.max(0, Math.floor((b - a) / 1000));
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
  return h ? `${h}h${String(m).padStart(2, "0")}m` : m ? `${m}m${String(r).padStart(2, "0")}s` : `${r}s`;
};

export default function RunsView() {
  const [tasks, setTasks] = useState<TaskSummary[]>([]);
  const [taskId, setTaskId] = useState<string>("");
  const [state, setState] = useState<TaskState | null>(null);
  const [goal, setGoal] = useState("");
  const [segments, setSegments] = useState(8);
  const [rounds, setRounds] = useState(40);
  const [minutes, setMinutes] = useState(240);
  const [starting, setStarting] = useState(false);
  const [graph, setGraph] = useState<{ url?: string; nodes?: number; edges?: number } | null>(null);
  const [clock, setClock] = useState(new Date());
  const ime = useImeGuard();
  const logRef = useRef<HTMLDivElement | null>(null);

  const refreshList = useCallback(async () => {
    try {
      const list = ((await LongRunList()) ?? []) as TaskSummary[];
      setTasks(list);
      setTaskId((cur) => cur || (list[0]?.taskId ?? ""));
    } catch { /* the list is decoration; the state below is the substance */ }
  }, []);

  const refreshState = useCallback(async (id: string) => {
    if (!id) { setState(null); return; }
    try {
      const s = (await LongRunState(id)) as unknown as TaskState | null;
      setState(s ?? null);
    } catch { /* keep the last snapshot */ }
  }, []);

  // Debounced refetch on tick. A run emits many ticks a second; one snapshot
  // every 200ms is plenty to look live and not enough to stall the wall.
  useEffect(() => {
    let timer: number | undefined;
    const off = EventsOn("longrun:tick", (payload: { taskId?: string; kind?: string }) => {
      if (payload?.kind === "begin" || payload?.kind === "finish") refreshList();
      if (payload?.taskId && payload.taskId === taskId) {
        if (timer) window.clearTimeout(timer);
        timer = window.setTimeout(() => refreshState(taskId), 200);
      }
    });
    return () => { off(); if (timer) window.clearTimeout(timer); };
  }, [taskId, refreshList, refreshState]);

  useEffect(() => { refreshList(); }, [refreshList]);
  useEffect(() => { refreshState(taskId); }, [taskId, refreshState]);
  useEffect(() => {
    startGraphView().then((g: Record<string, unknown>) => setGraph(g as { url?: string })).catch(() => {});
    const t = window.setInterval(() => setClock(new Date()), 1000);
    return () => window.clearInterval(t);
  }, []);
  useEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [state?.log?.length]);

  const start = useCallback(async () => {
    const g = goal.trim();
    if (!g || starting) return;
    setStarting(true);
    try {
      const id = await LongRunStart(g, segments, rounds, minutes, 0, "");
      if (id) { setTaskId(id); setGoal(""); await refreshList(); }
    } finally { setStarting(false); }
  }, [goal, segments, rounds, minutes, starting, refreshList]);

  const resume = useCallback(async () => {
    if (!state || state.running) return;
    setStarting(true);
    try {
      await LongRunStart(state.goal, segments, rounds, minutes, 0, state.taskId);
      await refreshList();
    } finally { setStarting(false); }
  }, [state, segments, rounds, minutes, refreshList]);

  // --- derived, all from the snapshot ---
  const d = useMemo(() => {
    const s = state;
    if (!s) return null;
    const rounds = s.rounds ?? [];
    const last = rounds[rounds.length - 1];
    const maxTok = Math.max(1, ...rounds.map((r) => r.tokens));
    const activeSeg = s.segments.find((x) => !x.endedAt)?.index ?? (s.segments.length ? s.segments[s.segments.length - 1].index : -1);
    const planDone = s.plan.filter((p) => p.done).length;
    const nextIdx = s.plan.findIndex((p) => !p.done);
    const cacheRate = pct(s.totalCached, s.totalTokens);
    const tokPerRound = rounds.length ? Math.round(s.totalTokens / rounds.length) : 0;
    const spark = rounds.slice(-36).map((r) => r.tokens / maxTok);
    const lintRows = Object.entries(s.lints).sort((a, b) => b[1] - a[1]);
    const toolRows = Object.entries(s.toolCounts).sort((a, b) => b[1] - a[1]).slice(0, 6);
    const maxTool = Math.max(1, ...toolRows.map((x) => x[1]));
    const accepted = rounds.filter((r) => !r.lint && !r.failed).length;
    const rejected = s.lintRetries + s.lintBlocks;
    const stage = !s.running ? 5 : last && last.tools > 0 ? 2 : 1;
    return { rounds, last, maxTok, activeSeg, planDone, nextIdx, cacheRate, tokPerRound, spark, lintRows, toolRows, maxTool, accepted, rejected, stage };
  }, [state]);

  const model = state?.model || "—";

  return (
    <div className="runs">
      {/* ── header strip ── */}
      <div className="rw-top">
        <div className="rw-brand">S</div>
        <div className="rw-title">
          <h1>SuperAI <span className="acc">·</span> Long-Run Supervisor</h1>
          <div className="sub">{model} · segmented · plan is the hand-off · {state ? (state.running ? "LIVE" : state.done ? "FINISHED" : state.stop || "IDLE") : "IDLE"}</div>
        </div>
        <div className="rw-stats">
          <div className="rw-stat"><div className="k">Segments</div><div className="v">{state ? `${state.segments.length}/${state.maxSegments || "∞"}` : "—"}</div></div>
          <div className="rw-stat"><div className="k">Rounds</div><div className="v">{d ? d.rounds.length : "—"}</div></div>
          <div className="rw-stat"><div className="k">Tok/round</div><div className="v acc">{d ? fmtK(d.tokPerRound) : "—"}</div></div>
          <div className="rw-stat"><div className="k">Cache hit</div><div className="v">{d ? `${d.cacheRate}%` : "—"}</div></div>
          <div className="rw-stat"><div className="k">Cost</div><div className="v">{state ? `$${state.costUsd.toFixed(3)}` : "—"}</div></div>
          <div className="rw-spark" title="tokens per round, last 36">
            {(d?.spark ?? Array(36).fill(0.05)).map((h, i) => <i key={i} style={{ height: `${Math.max(6, h * 100)}%` }} />)}
          </div>
        </div>
        <div className="rw-clock">
          <div className="t">{hhmmss(clock)}</div>
          <div className="d">{state ? `elapsed ${elapsed(state.startedAt, state.endedAt)}` : "no task"}</div>
        </div>
      </div>

      {/* ── task bar ── */}
      <div className="rw-taskbar">
        <select value={taskId} onChange={(e) => setTaskId(e.target.value)} title="Task">
          {tasks.length === 0 && <option value="">— no runs yet —</option>}
          {tasks.map((t) => (
            <option key={t.taskId} value={t.taskId}>
              {t.running ? "● " : t.done ? "✓ " : "○ "}{short(t.goal || t.taskId, 48)} · {t.segments}s/{t.rounds}r
            </option>
          ))}
        </select>
        <input
          className="input"
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          onCompositionStart={ime.handlers.onCompositionStart}
          onCompositionEnd={ime.handlers.onCompositionEnd}
          onKeyDown={(e) => { if (e.key === "Enter" && !ime.composing(e)) { e.preventDefault(); start(); } }}
          placeholder="A goal that takes hours — the supervisor runs it segment by segment"
          autoComplete="off"
        />
        <label className="rw-knob">seg <input type="number" min={1} value={segments} onChange={(e) => setSegments(+e.target.value || 1)} /></label>
        <label className="rw-knob">rounds <input type="number" min={1} value={rounds} onChange={(e) => setRounds(+e.target.value || 1)} /></label>
        <label className="rw-knob">min <input type="number" min={0} value={minutes} onChange={(e) => setMinutes(+e.target.value || 0)} /></label>
        <button className="btn" onClick={start} disabled={starting || !goal.trim()}>{starting ? "Starting…" : "Run"}</button>
        {state?.running && <button className="btn ghost" onClick={() => LongRunStop(state.taskId)}>Stop</button>}
        {state && !state.running && !state.done && <button className="btn ghost" onClick={resume} disabled={starting} title="Same task id: the plan and checkpoints are picked back up">Resume</button>}
      </div>

      {!state && (
        <div className="rw-empty">
          Nothing on the wall yet. Give the supervisor a goal above — it runs the agent in segments,
          carrying the plan and the workspace across, and everything it does lands here as it happens.
        </div>
      )}

      {state && d && (
        <>
          {/* ── segment strip ── */}
          <div className="rw-segs">
            <span className="meta">task {state.taskId.slice(0, 8)} · ctx {fmtK(d.last?.tokens ?? 0)} · plan {d.planDone}/{state.plan.length}</span>
            {Array.from({ length: Math.max(state.maxSegments, state.segments.length) || 1 }).map((_, i) => {
              const seg = state.segments.find((x) => x.index === i);
              const cls = !seg ? "" : seg.err ? "failed" : seg.endedAt ? "done" : "active";
              return (
                <React.Fragment key={i}>
                  {i > 0 && <span className="rw-seg-gap" />}
                  <span className={`rw-seg ${cls}`} title={seg ? `${seg.rounds} rounds · ${seg.stopReason || "running"}` : "not started"}>
                    <span className="n">{String(i + 1).padStart(2, "0")}</span>
                    {seg ? (seg.endedAt ? seg.stopReason?.replace("_", " ") || "done" : "running") : "—"}
                  </span>
                </React.Fragment>
              );
            })}
          </div>

          {/* ── three-up ── */}
          <div className="rw-row3">
            <div className="rw-panel">
              <div className="rw-head"><span className="rw-label">Plan <b>·</b> the hand-off</span><span className={`rw-tag ${d.planDone === state.plan.length && state.plan.length > 0 ? "ok" : "acc"}`}>{d.planDone} / {state.plan.length}</span></div>
              {state.plan.length === 0 ? (
                <div className="rw-plan-note">No plan written yet. The agent keeps one in its scratchpad; it appears here as soon as it does.</div>
              ) : (
                <div className="rw-plan">
                  {state.plan.map((p, i) => (
                    <div key={i} className={`rw-plan-row ${p.done ? "done" : i === d.nextIdx ? "next" : ""}`} title={p.note || p.text}>
                      <span className="i">{String(i + 1).padStart(2, "0")}</span>
                      <span className="bar"><i /></span>
                      <span className="t">{short(p.text.replace(/^\d+[.\s]*/, ""), 16)}</span>
                    </div>
                  ))}
                </div>
              )}
              {d.nextIdx >= 0 && state.plan[d.nextIdx] && <div className="rw-plan-note">next → {state.plan[d.nextIdx].text}</div>}
            </div>

            <div className="rw-panel">
              <div className="rw-head"><span className="rw-label">Context window <b>·</b> per round</span><span className="rw-tag">{state.compactions} compactions</span></div>
              <div className="rw-grid">
                {d.rounds.map((r, i) => {
                  const q = r.tokens / d.maxTok;
                  const lvl = r.failed ? "fail" : q > 0.75 ? "l4" : q > 0.5 ? "l3" : q > 0.25 ? "l2" : q > 0 ? "l1" : "";
                  return <span key={i} className={`rw-cell ${lvl}${r.compacted ? " compact" : ""}${r.lint ? " lint" : ""}`} title={`r${r.round} s${r.segment + 1} · ${fmtK(r.tokens)} tok · ${fmtK(r.cached)} cached${r.compacted ? " · compacted" : ""}${r.lint ? " · " + r.lint : ""}`} />;
                })}
              </div>
              <div className="rw-legend">
                <span><i className="sw" style={{ background: "var(--accent)" }} />hot</span>
                <span><i className="sw" style={{ background: "var(--bg-3)", border: "1.5px solid #fff" }} />compacted</span>
                <span><i className="sw" style={{ outline: "1.5px solid var(--red)", outlineOffset: -1.5 }} />lint</span>
                <span>peak {fmtK(d.maxTok)}</span>
              </div>
            </div>

            <div className="rw-panel">
              <div className="rw-head"><span className="rw-label">Cache hit <b>·</b> per round</span><span className={`rw-tag ${d.cacheRate >= 80 ? "ok" : d.cacheRate >= 40 ? "acc" : "bad"}`}>{d.cacheRate}% avg</span></div>
              <div className="rw-bars">
                {d.rounds.slice(-48).map((r, i) => {
                  const h = (r.tokens / d.maxTok) * 100;
                  const c = r.tokens ? (r.cached / r.tokens) * 100 : 0;
                  return <i key={i} style={{ height: `${Math.max(3, h)}%` }} title={`r${r.round}: ${pct(r.cached, r.tokens)}% cached`}><b style={{ height: `${c}%` }} /></i>;
                })}
              </div>
              <div className="rw-axis"><span>oldest</span><span>{fmtK(state.totalCached)} / {fmtK(state.totalTokens)} cached</span><span>newest</span></div>
            </div>
          </div>

          {/* ── the loop ── */}
          <div className="rw-panel">
            <div className="rw-head"><span className="rw-label">The loop <b>·</b> <span className="acc">one state machine</span> · assemble → model → tools → lint → checkpoint → segment</span><span className="rw-tag acc">stage {String(d.stage + 1).padStart(2, "0")} / 06</span></div>
            <div className="rw-loop">
              {[
                ["01", "Context", `${fmtK(d.last?.tokens ?? 0)} tok · ${state.compactions} folds`],
                ["02", "Model", `${d.rounds.length} turns · ${state.retries} retries`],
                ["03", "Tools", `${state.toolCalls} calls · ${state.toolErrors} failed`],
                ["04", "Lint", `${d.rejected} rejected`],
                ["05", "Checkpoint", `${state.checkpoints} written`],
                ["06", "Segment", `${state.segments.length} run${state.segments.length === 1 ? "" : "s"}`],
              ].map(([n, name, v], i) => (
                <div key={n} className={`rw-stage${i === d.stage ? " on" : ""}`}>
                  <div className="n">{n}</div><div className="name">{name.toUpperCase()}</div><div className="v">{v}</div>
                </div>
              ))}
            </div>
            <div className="rw-gate">
              <div>rejected by lint<div className="big bad">{d.rejected}</div></div>
              <div className="rail" />
              <div style={{ textAlign: "right" }}>accepted turns<div className="big ok">{d.accepted}</div></div>
            </div>
          </div>

          {/* ── graph ── */}
          <div className="rw-panel rw-graph">
            <div className="rw-head">
              <span className="rw-label">Knowledge graph <b>·</b> <span className="acc">live</span> · every entity the run touches becomes a node</span>
              <span className="rw-tag">{graph?.nodes ?? "—"} nodes · {graph?.edges ?? "—"} edges</span>
            </div>
            {(GRAPH_SRC ?? graph?.url) ? <iframe src={GRAPH_SRC ?? graph?.url} title="knowledge graph" /> : <div className="rw-empty" style={{ margin: 40 }}>graph view not available</div>}
          </div>

          {/* ── activity ── */}
          <div className="rw-panel">
            <div className="rw-head"><span className="rw-label">Activity <b>·</b> what the run is doing, as it does it</span><span className="rw-tag">{state.log.length} lines</span></div>
            <div className="rw-log" ref={logRef}>
              {state.log.map((l, i) => (
                <div key={i} className={l.kind}><span className="at">{new Date(l.at).toTimeString().slice(0, 8)}</span><span className="k">{l.kind}</span>{l.text}</div>
              ))}
              {state.log.length === 0 && <div style={{ color: "var(--text-3)" }}>waiting for the first turn…</div>}
            </div>
          </div>

          {/* ── run wall + ledger ── */}
          <div className="rw-row2">
            <div className="rw-panel">
              <div className="rw-head"><span className="rw-label">The run wall <b>·</b> every model turn since the task began · <b>{d.rounds.length}</b> cells</span><span className={`rw-tag ${state.running ? "acc" : ""}`}>{state.running ? "● live" : "settled"}</span></div>
              <div className="rw-wall">
                {d.rounds.map((r, i) => {
                  const first = i === 0 || d.rounds[i - 1].segment !== r.segment;
                  return <i key={i} className={`${r.failed || r.lint ? "x" : r.compacted ? "c" : ""}${first ? " seg" : ""}`} title={`r${r.round} s${r.segment + 1}${r.lint ? " · " + r.lint : ""}${r.failed ? " · failed" : ""}`}>{r.failed || r.lint ? "×" : ""}</i>;
                })}
              </div>
              <div className="rw-wall-note">one cell = one model turn · ringed = first turn of a segment · × = rejected or failed · pale = the turn history was folded</div>
            </div>
            <div className="rw-panel">
              <div className="rw-head"><span className="rw-label">Deviation ledger</span><span className={`rw-tag ${d.rejected === 0 ? "ok" : "bad"}`}>{d.rounds.length ? `clean ${pct(d.accepted, d.rounds.length)}%` : "—"}</span></div>
              <div className="rw-label">caught by lints</div>
              <div className={`rw-ledger-big${d.rejected === 0 ? " ok" : ""}`}>{d.rejected}</div>
              {d.lintRows.map(([k, v]) => (
                <div key={k} className="rw-ledger-row"><div><div className="k">{k}</div><div className="bar"><i style={{ width: `${pct(v, Math.max(1, d.rejected))}%` }} /></div></div><div className="n">{v}</div></div>
              ))}
              <div className="rw-ledger-row soft"><div><div className="k">compactions</div><div className="bar"><i style={{ width: `${pct(state.compactions, Math.max(1, d.rounds.length))}%` }} /></div></div><div className="n">{state.compactions}</div></div>
              <div className="rw-ledger-row soft"><div><div className="k">model retries</div><div className="bar"><i style={{ width: `${pct(state.retries, Math.max(1, d.rounds.length))}%` }} /></div></div><div className="n">{state.retries}</div></div>
              <div className="rw-ledger-row info"><div><div className="k">tool errors</div><div className="bar"><i style={{ width: `${pct(state.toolErrors, Math.max(1, state.toolCalls))}%` }} /></div></div><div className="n">{state.toolErrors}</div></div>
              {d.toolRows.length > 0 && <div className="rw-label" style={{ marginTop: 12 }}>tools · top {d.toolRows.length}</div>}
              {d.toolRows.map(([k, v]) => (
                <div key={k} className="rw-ledger-row info"><div><div className="k">{k}</div><div className="bar"><i style={{ width: `${pct(v, d.maxTool)}%` }} /></div></div><div className="n">{v}</div></div>
              ))}
              {state.final && <div className="rw-final">{short(state.final, 600)}</div>}
            </div>
          </div>

          <div className="rw-foot">
            <span>a long run is many runs, not a long run <span className="acc">·</span> the plan is the hand-off <span className="acc">·</span> checkpoints are the way back</span>
            <span>superai <span className="acc">·</span> run wall</span>
          </div>
        </>
      )}
    </div>
  );
}
