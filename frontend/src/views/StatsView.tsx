import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";
import {
  Dashboard, GraphView as startGraphView, LongRunList, LongRunStart, LongRunState, LongRunStop,
} from "../../wailsjs/go/main/App";
import {
  Activity, AlertTriangle, Brain, Clock, Coins, Cpu, Database, Gauge, GitBranch, Grid3x3,
  ListChecks, Play, Radio, RotateCcw, ScrollText, Shield, Sparkles, Square, Terminal, Wrench, Zap,
} from "lucide-react";
import Reactor, { PulseTicker, ToolTable, usePulse } from "../components/Reactor";
import { hueFor } from "../lib/hues";
import HealthCard from "../components/HealthCard";
import RunTracePanel from "../components/RunTracePanel";
import { useTween } from "../lib/useTween";
import { useImeGuard } from "@/lib/ime";

/**
 * The control room.
 *
 * One page that answers, at a glance: is the agent alive, what is it running
 * on, what is it doing right now, and is it going well. The top half is the
 * system — model, brain, memory, spend. The bottom half is the task in flight,
 * drawn from agent-go's Observer through the backend's RunWall: every model
 * turn, tool call, lint verdict, compaction, retry and segment boundary.
 *
 * The orbital is the one thing here that moves on its own; it is the loop —
 * agent-go has exactly one — with the station in flight lit and traffic in
 * proportion to what the run is doing. Everything else animates only when its
 * number changes, so motion means information.
 */

// ---- shapes ----
interface RoundStat { segment: number; round: number; tokens: number; cached: number; tools: number; text: number; durMs: number; compacted?: boolean; lint?: string; retried?: boolean; failed?: boolean }
interface SegmentStat { index: number; sessionId: string; startedAt: string; endedAt?: string; stopReason?: string; productive: boolean; costUsd: number; err?: string; rounds: number }
interface LogLine { at: string; kind: string; text: string }
interface PlanItem { text: string; done: boolean; note?: string }
interface TaskState {
  taskId: string; goal: string; model: string; startedAt: string; endedAt?: string; done: boolean; running: boolean; stop?: string; final?: string; maxSegments: number;
  segments: SegmentStat[]; rounds: RoundStat[]; plan: PlanItem[]; toolCounts: Record<string, number>; toolCalls: number; toolErrors: number;
  lints: Record<string, number>; lintRetries: number; lintBlocks: number; compactions: number; retries: number; checkpoints: number; errors: number;
  totalTokens: number; totalCached: number; costUsd: number; unpriced?: boolean; log: LogLine[];
}
interface TaskSummary {
  taskId: string; goal: string; model: string; startedAt: string; endedAt?: string; running: boolean; done: boolean; stop?: string;
  segments: number; maxSegments: number; segmentOpen: boolean; rounds: number; lastTokens: number; lastTools: number;
  totalTokens: number; totalCached: number; costUsd: number; unpriced?: boolean; rejected: number; errors: number; planDone: number; planTotal: number; spark: number[];
}
// Where a task is on the loop, from its summary alone — the same rule the
// detail view uses, so a card and the orbital never disagree.
const stageOf = (t: TaskSummary) => (!t.running ? 5 : !t.segmentOpen ? 5 : t.lastTools > 0 ? 2 : 1);
interface DashData {
  ready: boolean;
  llm?: { model: string; baseURL: string; embedModel: string; maxRounds: number; workspace: string };
  memoryMode?: string;
  activeRuns?: { run_id: string; task_id?: string; started_at: string }[];
  usage?: { totalTokens: number; cachedTokens: number; modelTurns: number; today: number; days: { day: string; tokens: number }[] };
  tasks?: { id: string; goal: string; status: string; brief: string; updatedAt: string; runs: number }[];
}

const SERVED = Boolean((window as unknown as Record<string, unknown>).superaiServed);
const GRAPH_SRC = SERVED ? "/graph/" : null;

const fmtK = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));
const pct = (a: number, b: number) => (b > 0 ? Math.round((100 * a) / b) : 0);
const short = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + "…" : s);
const hhmmss = (d: Date) => d.toTimeString().slice(0, 8);
const hms = (at: string) => new Date(at).toTimeString().slice(0, 8);
const elapsed = (from: string, to?: string) => {
  const a = new Date(from).getTime(); if (!a) return "—";
  const s = Math.max(0, Math.floor(((to ? new Date(to).getTime() : Date.now()) - a) / 1000));
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
  return h ? `${h}h ${String(m).padStart(2, "0")}m` : m ? `${m}m ${String(r).padStart(2, "0")}s` : `${r}s`;
};

// Numbers only, below the reactor. Every picture this page had — the
// sparkline in a card, the heat grid of turns, the cache bars, the fleet
// cards' little polylines — moved into the wheel or became a figure. One
// place draws; everything under it can be read.
function Stat({ icon, label, value, sub, tone }: { icon: React.ReactNode; label: string; value: React.ReactNode; sub?: React.ReactNode; tone?: "cyan" | "amber" | "rose" | "lime" }) {
  return (
    <div className={`cr-stat ${tone ?? ""}`}>
      <div className="cr-stat-h"><span className="cr-ico">{icon}</span><span className="cr-eyebrow">{label}</span></div>
      <div className="cr-stat-v">{value}</div>
      {sub && <div className="cr-stat-s">{sub}</div>}
    </div>
  );
}

function Num({ v, fmt = fmtK }: { v: number; fmt?: (n: number) => string }) {
  const t = useTween(v);
  return <>{fmt(t)}</>;
}

export default function StatsView() {
  const [dash, setDash] = useState<DashData | null>(null);
  const [graph, setGraph] = useState<{ url?: string; nodes?: number; edges?: number } | null>(null);
  const [tasks, setTasks] = useState<TaskSummary[]>([]);
  const [taskId, setTaskId] = useState("");
  const [st, setSt] = useState<TaskState | null>(null);
  const [goal, setGoal] = useState("");
  const [segs, setSegs] = useState(8);
  const [rounds, setRounds] = useState(40);
  const [minutes, setMinutes] = useState(240);
  // A task that runs for hours is one nobody is sitting beside. Default on;
  // the audit log still records every call it lets through.
  const [unattended, setUnattended] = useState(true);
  const [busy, setBusy] = useState(false);
  // The task whose JSONL trace is open, if any. Its own state rather than a
  // mode of taskId: reading one task's trace must not move the focus of every
  // other panel on the page.
  const [traceId, setTraceId] = useState("");
  const [clock, setClock] = useState(new Date());
  // The live meter, polled once here and shared by the reactor, the columns
  // and the ticker below it.
  const pulse = usePulse();
  const ime = useImeGuard();
  const logRef = useRef<HTMLDivElement | null>(null);

  const loadDash = useCallback(async () => { try { setDash((await Dashboard()) as DashData); } catch { /* keep last */ } }, []);
  const loadList = useCallback(async () => {
    try { const l = ((await LongRunList()) ?? []) as TaskSummary[]; setTasks(l); setTaskId((c) => c || (l[0]?.taskId ?? "")); } catch { /* keep last */ }
  }, []);
  const loadState = useCallback(async (id: string) => {
    if (!id) { setSt(null); return; }
    try { setSt(((await LongRunState(id)) as unknown as TaskState) ?? null); } catch { /* keep last */ }
  }, []);

  useEffect(() => { loadDash(); loadList(); startGraphView().then((g: Record<string, unknown>) => setGraph(g as { url?: string })).catch(() => {}); }, [loadDash, loadList]);
  useEffect(() => { loadState(taskId); }, [taskId, loadState]);
  useEffect(() => {
    const t1 = window.setInterval(() => setClock(new Date()), 1000);
    const t2 = window.setInterval(loadDash, 8000);
    return () => { window.clearInterval(t1); window.clearInterval(t2); };
  }, [loadDash]);
  useEffect(() => {
    let timer: number | undefined;
    const off = EventsOn("longrun:tick", (p: { taskId?: string; kind?: string }) => {
      if (p?.kind === "begin" || p?.kind === "finish") loadDash();
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(() => { loadList(); if (p?.taskId && p.taskId === taskId) loadState(taskId); }, 180);
    });
    return () => { off(); if (timer) window.clearTimeout(timer); };
  }, [taskId, loadList, loadState, loadDash]);
  useEffect(() => { const el = logRef.current; if (el) el.scrollTop = el.scrollHeight; }, [st?.log?.length]);

  const start = useCallback(async () => {
    const g = goal.trim(); if (!g || busy) return;
    setBusy(true);
    try { const id = await LongRunStart(g, segs, rounds, minutes, 0, "", unattended); if (id) { setTaskId((cur) => cur || id); setGoal(""); await loadList(); } } finally { setBusy(false); }
  }, [goal, segs, rounds, minutes, busy, loadList]);

  // ---- derived ----
  const d = useMemo(() => {
    if (!st) return null;
    const rs = st.rounds ?? [];
    const last = rs[rs.length - 1];
    const maxTok = Math.max(1, ...rs.map((r) => r.tokens));
    const planDone = st.plan.filter((p) => p.done).length;
    const nextIdx = st.plan.findIndex((p) => !p.done);
    const rejected = st.lintRetries + st.lintBlocks;
    const accepted = rs.filter((r) => !r.lint && !r.failed).length;
    const cacheRate = pct(st.totalCached, st.totalTokens);
    const recent = rs.slice(-8);
    const activity = Math.min(1, recent.reduce((a, r) => a + r.tools, 0) / Math.max(1, recent.length * 2));
    const stage = !st.running ? 5 : st.segments.length && !st.segments[st.segments.length - 1].endedAt ? (last && last.tools > 0 ? 2 : 1) : 5;
    const lintRows = Object.entries(st.lints).sort((a, b) => b[1] - a[1]);
    const toolRows = Object.entries(st.toolCounts).sort((a, b) => b[1] - a[1]).slice(0, 5);
    const maxTool = Math.max(1, ...toolRows.map((x) => x[1]));
    return { rs, last, maxTok, planDone, nextIdx, rejected, accepted, cacheRate, activity, stage, lintRows, toolRows, maxTool };
  }, [st]);

  const days = dash?.usage?.days ?? [];
  const live = Boolean(st?.running) || (dash?.activeRuns?.length ?? 0) > 0;
  const brainLabel = dash?.memoryMode?.startsWith("shared") ? "shared brain" : dash?.memoryMode || "local brain";

  return (
    <div className="cr">
      {/* ── header ── */}
      <header className="cr-head">
        <div className="cr-brand"><span className={`cr-led ${live ? "on" : ""}`} /><b>SUPERAI</b><span className="cr-eyebrow">control room</span></div>
        <div className="cr-chips">
          <span className="cr-chip"><Cpu size={12} />{dash?.llm?.model || "—"}</span>
          <span className="cr-chip"><Brain size={12} />{brainLabel}</span>
          <span className="cr-chip"><Database size={12} />{graph?.nodes ?? "—"} nodes · {graph?.edges ?? "—"} edges</span>
          <span className="cr-chip"><Radio size={12} />{dash?.activeRuns?.length ?? 0} in flight</span>
        </div>
        <div className="cr-clock"><Clock size={13} />{hhmmss(clock)}<span className={`cr-tag ${live ? "cyan" : ""}`}>{live ? "LIVE" : "IDLE"}</span></div>
      </header>

      {/* ── the reactor: the whole width, the brain in its hub ── */}
      <section className="cr-panel cr-reactor">
        {/* The live view's own switches, set from the URL: no control panels
            (inside a disc this size they would cover the graph), orbiting from
            the start, and the reactor's own black behind it. */}
        <Reactor snap={pulse} brain={(GRAPH_SRC ?? graph?.url) ? `${GRAPH_SRC ?? graph?.url}?panels=0&spin=1&bg=05070f` : null} />
      </section>

      {/* ── the system ── */}
      <section className="cr-stats six">
        <Stat icon={<Cpu size={14} />} label="Model" value={<span className="cr-stat-txt">{dash?.llm?.model || "—"}</span>} sub={<>{dash?.llm?.maxRounds ?? "—"} rounds/turn · {short(dash?.llm?.baseURL || "", 34)}</>} />
        <Stat icon={<Gauge size={14} />} label="Tokens today" value={<Num v={dash?.usage?.today ?? 0} />} sub={<>{fmtK(dash?.usage?.totalTokens ?? 0)} all time · {pct(dash?.usage?.cachedTokens ?? 0, dash?.usage?.totalTokens ?? 0)}% cached · 7d {fmtK(days.reduce((a, x) => a + x.tokens, 0))}</>} tone="cyan" />
        <Stat icon={<Sparkles size={14} />} label="Cache hit" value={d ? <Num v={d.cacheRate} fmt={(n) => Math.round(n) + "%"} /> : <span className="cr-dim">—</span>} sub={d ? `${fmtK(st!.totalCached)} / ${fmtK(st!.totalTokens)} this task` : "select or start a task"} tone={!d ? undefined : d.cacheRate >= 80 ? "lime" : "amber"} />
        <Stat icon={<Coins size={14} />} label="Spend" value={!st ? <span className="cr-dim">—</span> : st.unpriced ? <span className="cr-dim">unpriced</span> : <Num v={st.costUsd} fmt={(n) => "$" + n.toFixed(3)} />} sub={!st ? "select or start a task" : st.unpriced ? "no rates for this model · set llm_price_* in settings" : `this task · ${st.segments.length} segments`} tone={!st ? undefined : st.unpriced ? "rose" : "amber"} />
        <Stat icon={<Activity size={14} />} label="Turns" value={<Num v={dash?.usage?.modelTurns ?? 0} />} sub={<>{dash?.tasks?.length ?? 0} tasks on record · {dash?.activeRuns?.length ?? 0} active</>} />
        <Stat icon={<Shield size={14} />} label="Lint gate" value={d ? <span className={d.rejected ? "rose" : "lime"}><Num v={d.rejected} /></span> : <span className="cr-dim">—</span>} sub={d ? `${pct(d.accepted, Math.max(1, d.rs.length))}% clean turns` : "select or start a task"} tone={!d ? undefined : d.rejected ? "rose" : "lime"} />
      </section>

      {/* ── the traffic the reactor is drawing ── */}
      <section className="cr-live">
        <div className="cr-panel rx-tools">
          <div className="cr-panel-h">
            <Wrench size={13} />
            <span className="cr-eyebrow">Tools</span>
            <span className="cr-tag">{pulse.tools.length}</span>
          </div>
          <ToolTable snap={pulse} />
        </div>
        <div className="cr-panel rx-ticker">
          <div className="cr-panel-h">
            <ScrollText size={13} />
            <span className="cr-eyebrow">Activity</span>
            <span className="cr-tag">newest first</span>
          </div>
          <PulseTicker snap={pulse} />
        </div>
      </section>

      {/* ── health: is the install itself sound ── */}
      <HealthCard />

      {/* ── task bar ── */}
      <section className="cr-taskbar">
        <Play size={14} className="cr-dim" />
        <input className="input" value={goal} onChange={(e) => setGoal(e.target.value)}
          onCompositionStart={ime.handlers.onCompositionStart} onCompositionEnd={ime.handlers.onCompositionEnd}
          onKeyDown={(e) => { if (e.key === "Enter" && !ime.composing(e)) { e.preventDefault(); start(); } }}
          placeholder="Launch a task — press Enter, then launch another; they run side by side" autoComplete="off" />
        <label className="cr-knob">seg<input type="number" min={1} value={segs} onChange={(e) => setSegs(+e.target.value || 1)} /></label>
        <label className="cr-knob">rounds<input type="number" min={1} value={rounds} onChange={(e) => setRounds(+e.target.value || 1)} /></label>
        <label className="cr-knob">min<input type="number" min={0} value={minutes} onChange={(e) => setMinutes(+e.target.value || 0)} /></label>
        <label className="cr-knob cr-check" title="Tool calls go through the approval gate without asking; every one is audited"><input type="checkbox" checked={unattended} onChange={(e) => setUnattended(e.target.checked)} />unattended</label>
        <button className="cr-btn primary" onClick={start} disabled={busy || !goal.trim()}><Play size={13} />{busy ? "Launching…" : "Launch"}</button>
      </section>

      {/* ── the fleet: every task, side by side ── */}
      {tasks.length > 0 && (
        <section className="cr-fleet">
          {tasks.map((t) => {
            const hue = hueFor(t.taskId);
            const cache = pct(t.totalCached, t.totalTokens);
            const stage = stageOf(t);
            return (
              <button key={t.taskId} className={`cr-task${t.taskId === taskId ? " focus" : ""}${t.running ? " live" : ""}`} style={{ ["--hue" as string]: hue }} onClick={() => setTaskId(t.taskId)}>
                <div className="cr-task-h">
                  <span className={`cr-task-led${t.running ? " on" : t.done ? " done" : t.stop ? " off" : ""}`} />
                  <span className="cr-task-goal" title={t.goal}>{short(t.goal || t.taskId, 64)}</span>
                  <span className="cr-tag">{t.running ? ["context", "model", "tools", "lint", "checkpoint", "segment"][stage] : t.done ? "finished" : t.stop || "idle"}</span>
                </div>
                <div className="cr-task-segs">
                  {Array.from({ length: Math.max(1, t.maxSegments, t.segments) }).map((_, i) => <i key={i} className={i < t.segments - (t.segmentOpen ? 1 : 0) ? "done" : i === t.segments - 1 && t.segmentOpen ? "on" : ""} />)}
                </div>
                <div className="cr-task-row">
                  <span><b>{t.planDone}</b>/{t.planTotal || "?"} plan</span>
                  <span><b>{t.rounds}</b> turns</span>
                  <span><b>{cache}%</b> cache</span>
                  <span>{t.unpriced ? <b className="cr-dim" title="no rates for this model">unpriced</b> : <b>${t.costUsd.toFixed(2)}</b>}</span>
                  <span className={t.rejected ? "rose" : ""}><b>{t.rejected}</b> rej</span>
                  <span className="cr-task-time">{elapsed(t.startedAt, t.endedAt)}</span>
                </div>
                <div className="cr-task-row dim">
                  <span><b>{fmtK(t.totalTokens)}</b> tok</span>
                  <span><b>{fmtK(t.lastTokens)}</b> last turn</span>
                  <span><b>{t.lastTools}</b> tools last turn</span>
                  <span><b>{t.segments}</b>/{t.maxSegments} seg</span>
                </div>
                <div className="cr-task-actions" onClick={(e) => e.stopPropagation()}>
                  <span role="button" className="cr-btn tiny" onClick={() => setTraceId(t.taskId)} title="Every event this task emitted, one per row"><Terminal size={10} />Trace</span>
                  {t.running && <span role="button" className="cr-btn tiny" onClick={() => LongRunStop(t.taskId)}><Square size={10} />Stop</span>}
                  {!t.running && !t.done && <span role="button" className="cr-btn tiny" onClick={() => LongRunStart(t.goal, segs, rounds, minutes, 0, t.taskId, unattended).then(loadList)}><RotateCcw size={10} />Resume</span>}
                </div>
              </button>
            );
          })}
        </section>
      )}

      {!st && (
        <div className="cr-empty"><Zap size={16} /> Nothing on the wall yet. Give the supervisor a goal above: it runs the agent segment by segment, carrying the plan and workspace across, and everything it does lands here as it happens.</div>
      )}

      {st && d && (
        <>
          {/* ── segment strip (focused task) ── */}
          <section className="cr-segs" style={{ ["--hue" as string]: hueFor(st.taskId) }}>
            <span className="cr-eyebrow cr-segs-meta"><span className="cr-task-led on hue" /><GitBranch size={12} />{short(st.goal, 40)} · {elapsed(st.startedAt, st.endedAt)} · ctx {fmtK(d.last?.tokens ?? 0)}</span>
            {Array.from({ length: Math.max(st.maxSegments, st.segments.length) || 1 }).map((_, i) => {
              const s = st.segments.find((x) => x.index === i);
              const cls = !s ? "" : s.err ? "failed" : s.endedAt ? "done" : "active";
              return (<React.Fragment key={i}>{i > 0 && <i className="cr-seg-gap" />}
                <span className={`cr-seg ${cls}`} title={s ? `${s.rounds} rounds · ${s.stopReason || "running"}` : "not started"}><b>{String(i + 1).padStart(2, "0")}</b>{s ? (s.endedAt ? (s.stopReason || "done").replace("_", " ") : "running") : "—"}</span>
              </React.Fragment>);
            })}
          </section>

          {/* ── plan · context · cache ── */}
          <section className="cr-row3">
            <div className="cr-panel">
              <div className="cr-panel-h"><ListChecks size={13} /><span className="cr-eyebrow">Plan · the hand-off</span><span className={`cr-tag ${d.planDone === st.plan.length && st.plan.length ? "lime" : "cyan"}`}>{d.planDone}/{st.plan.length}</span></div>
              {st.plan.length === 0 ? <div className="cr-note">No plan yet. The agent writes one into its scratchpad; it appears here the moment it does.</div> : (
                <ol className="cr-plan">
                  {st.plan.map((p, i) => (
                    <li key={i} className={p.done ? "done" : i === d.nextIdx ? "next" : ""} style={{ animationDelay: `${i * 30}ms` }} title={p.note || p.text}>
                      <span className="cr-plan-i">{p.done ? "✓" : i === d.nextIdx ? "▶" : "·"}</span><span className="cr-plan-t">{p.text.replace(/^\d+[.\s]*/, "")}</span>
                    </li>))}
                </ol>)}
            </div>
            <div className="cr-panel span2">
              <div className="cr-panel-h"><Grid3x3 size={13} /><span className="cr-eyebrow">Turns · newest first</span><span className="cr-tag amber">{st.compactions} folds</span><span className={`cr-tag ${d.cacheRate >= 80 ? "lime" : d.cacheRate >= 40 ? "amber" : "rose"}`}>{d.cacheRate}% cached</span><span className="cr-tag">peak {fmtK(d.maxTok)}</span></div>
              <div className="cr-table-wrap">
                <table className="cr-table">
                  <thead><tr><th>turn</th><th>seg</th><th className="num">tokens</th><th className="num">cached</th><th className="num">%</th><th className="num">tools</th><th className="num">text</th><th className="num">ms</th><th>flags</th></tr></thead>
                  <tbody>
                    {d.rs.slice(-60).reverse().map((r, i) => (
                      <tr key={i} className={r.failed ? "bad" : ""}>
                        <td>r{r.round}</td><td>{r.segment + 1}</td>
                        <td className="num">{fmtK(r.tokens)}</td><td className="num">{fmtK(r.cached)}</td><td className="num">{pct(r.cached, r.tokens)}</td>
                        <td className="num">{r.tools}</td><td className="num">{fmtK(r.text)}</td><td className="num">{r.durMs}</td>
                        <td className="flags">{r.compacted ? "folded " : ""}{r.lint ? r.lint + " " : ""}{r.retried ? "retried " : ""}{r.failed ? "failed" : ""}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </section>

          {/* ── wall · activity · ledger ── */}
          <section className="cr-row3 wide">
            <div className="cr-panel">
              <div className="cr-panel-h"><Grid3x3 size={13} /><span className="cr-eyebrow">The run wall · <b>{d.rs.length}</b> turns since the task began</span><span className={`cr-tag ${st.running ? "cyan" : ""}`}>{st.running ? "● live" : "settled"}</span></div>
              <div className="cr-wall">
                {d.rs.map((r, i) => { const first = i === 0 || d.rs[i - 1].segment !== r.segment;
                  return <i key={i} className={`${r.failed || r.lint ? "x" : r.compacted ? "c" : ""}${first ? " seg" : ""}`} style={{ animationDelay: `${Math.min(i, 80) * 8}ms` }} title={`r${r.round} s${r.segment + 1}${r.lint ? " · " + r.lint : ""}`}>{r.failed || r.lint ? "×" : ""}</i>; })}
              </div>
              <div className="cr-note small">one cell = one turn · ringed = first of a segment · × = rejected · pale = history folded</div>
            </div>
            <div className="cr-panel">
              <div className="cr-panel-h"><ScrollText size={13} /><span className="cr-eyebrow">Activity</span><span className="cr-tag">{st.log.length}</span></div>
              <div className="cr-log" ref={logRef}>
                {st.log.length === 0 && <div className="cr-dim">waiting for the first turn…</div>}
                {st.log.slice(-160).map((l, i) => <div key={i} className={`cr-line ${l.kind}`}><span className="at">{hms(l.at)}</span><span className="k">{l.kind}</span>{l.text}</div>)}
              </div>
            </div>
            <div className="cr-panel">
              <div className="cr-panel-h"><AlertTriangle size={13} /><span className="cr-eyebrow">Deviation ledger</span><span className={`cr-tag ${d.rejected ? "rose" : "lime"}`}>{d.rs.length ? `clean ${pct(d.accepted, d.rs.length)}%` : "—"}</span></div>
              <div className={`cr-big ${d.rejected ? "rose" : "lime"}`}><Num v={d.rejected} fmt={(n) => String(Math.round(n))} /></div>
              <div className="cr-eyebrow">caught by lints</div>
              {d.lintRows.map(([k, v]) => <div key={k} className="cr-ledger rose"><span className="k">{k}</span><span className="bar"><i style={{ width: `${pct(v, Math.max(1, d.rejected))}%` }} /></span><span className="n">{v}</span></div>)}
              <div className="cr-ledger amber"><span className="k">compactions</span><span className="bar"><i style={{ width: `${pct(st.compactions, Math.max(1, d.rs.length))}%` }} /></span><span className="n">{st.compactions}</span></div>
              <div className="cr-ledger amber"><span className="k">model retries</span><span className="bar"><i style={{ width: `${pct(st.retries, Math.max(1, d.rs.length))}%` }} /></span><span className="n">{st.retries}</span></div>
              <div className="cr-ledger"><span className="k">tool errors</span><span className="bar"><i style={{ width: `${pct(st.toolErrors, Math.max(1, st.toolCalls))}%` }} /></span><span className="n">{st.toolErrors}</span></div>
              {d.toolRows.length > 0 && <div className="cr-eyebrow" style={{ marginTop: 10 }}><Wrench size={11} /> tools</div>}
              {d.toolRows.map(([k, v]) => <div key={k} className="cr-ledger"><span className="k">{k}</span><span className="bar"><i style={{ width: `${pct(v, d.maxTool)}%` }} /></span><span className="n">{v}</span></div>)}
              {st.final && <div className="cr-final">{short(st.final, 500)}</div>}
            </div>
          </section>
        </>
      )}

      {traceId && (
        <RunTracePanel
          taskId={traceId}
          live={tasks.some((t) => t.taskId === traceId && t.running)}
          onClose={() => setTraceId("")}
        />
      )}

      <footer className="cr-foot"><span>a long run is many runs, not a long run · the plan is the hand-off · checkpoints are the way back</span><span>superai · control room</span></footer>
    </div>
  );
}
