import { ReactNode, useMemo, useState } from "react";
import type { TraceRow } from "./RunTracePanel";

/**
 * One run of a trace as bars on a shared time axis: a row per model call and
 * per tool call, placed by when it started and as wide as it took.
 *
 * The row list says what happened; this says what happened at the same time.
 * agent-go runs one round's tool calls in parallel and fires its own model
 * calls (constraints, memory-pick, …) beside the turn's, and neither shows in
 * a list sorted by timestamp — two tools that overlap and two that ran back to
 * back read the same there. Here overlapping bars stack over the same stretch
 * of axis, and a row that overlaps another is tagged with how many ran at once.
 *
 * A trace file is a whole conversation, many runs long, and the gap between
 * two of them is however long the person took to answer. One axis across all
 * of that would squeeze every run to a sliver, so it is one run at a time.
 */

type Kind = "model" | "tool" | "lint";

interface Span {
  key: string;
  kind: Kind;
  label: string;
  round?: number;
  start: number;
  end: number;
  /** The start line is not in what was loaded; start is end − duration. */
  derived?: boolean;
  /** Started and not yet ended: still running, or the run was cut off. */
  open?: boolean;
  failed?: boolean;
  /** A turn whose answer a lint rejected. */
  linted?: boolean;
  /** A model call the run made on its own behalf, not the turn itself. */
  aux?: boolean;
  /** How many calls of its kind ran at once with it, itself included. */
  par: number;
  /** An auxiliary call that ran while the turn's own call did. */
  besideTurn?: boolean;
  rows: TraceRow[];
}

interface Run {
  id: string;
  rows: TraceRow[];
  start: number;
  end: number;
  calls: number;
}

const at = (r: TraceRow) => (r.ts ? Date.parse(r.ts) : NaN);
const hms = (ms: number) => (isNaN(ms) ? "--:--:--" : new Date(ms).toTimeString().slice(0, 8));
const dur = (ms: number) => (ms < 1000 ? `${Math.round(ms)}ms` : ms < 60000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.floor(ms / 60000)}m${Math.round((ms % 60000) / 1000)}s`);
const overlaps = (a: Span, b: Span) => a.start < b.end && b.start < a.end;

// Axis steps, in ms: the smallest that keeps the run to eight ticks or fewer.
const STEPS = [100, 200, 500, 1000, 2000, 5000, 10000, 15000, 30000, 60000, 120000, 300000, 600000, 1800000, 3600000];

/** Split the loaded lines into runs, newest first. A line with no run id
 *  (an older trace) falls into one run of its own. */
function runsOf(rows: TraceRow[]): Run[] {
  const by = new Map<string, Run>();
  for (const r of rows) {
    const t = at(r);
    if (isNaN(t)) continue;
    const id = r.run_id ?? "";
    let run = by.get(id);
    if (!run) by.set(id, (run = { id, rows: [], start: t, end: t, calls: 0 }));
    run.rows.push(r);
    run.start = Math.min(run.start, t - (r.event?.endsWith("_end") ? r.duration_ms ?? 0 : 0));
    run.end = Math.max(run.end, t);
    if (r.event === "model_end" || r.event === "tool_end") run.calls++;
  }
  return [...by.values()].filter((r) => r.calls > 0).sort((a, b) => b.start - a.start);
}

/** Pair each call's start and end lines into one span: model calls by
 *  span_id, tool calls by call_id. Either half may be missing — the start
 *  scrolled out of the loaded page, or the end not written yet. */
function spansOf(run: Run): Span[] {
  const models = new Map<string, { s?: TraceRow; e?: TraceRow }>();
  const tools = new Map<string, { s?: TraceRow; e?: TraceRow }>();
  const lints: TraceRow[] = [];
  for (const r of run.rows) {
    const pair = (m: Map<string, { s?: TraceRow; e?: TraceRow }>, id: string | undefined, end: boolean) => {
      if (!id) return;
      const p = m.get(id) ?? {};
      if (end) p.e = r;
      else p.s = r;
      m.set(id, p);
    };
    if (r.event === "model_start") pair(models, r.span_id, false);
    else if (r.event === "model_end") pair(models, r.span_id, true);
    else if (r.event === "tool_start") pair(tools, r.call_id, false);
    else if (r.event === "tool_end") pair(tools, r.call_id, true);
    else if (r.event === "lint") lints.push(r);
  }

  const bounds = (p: { s?: TraceRow; e?: TraceRow }) => {
    const end = p.e ? at(p.e) : run.end;
    const start = p.s ? at(p.s) : end - (p.e?.duration_ms ?? 0);
    return { start, end: Math.max(end, start), derived: !p.s, open: !p.e };
  };

  const out: Span[] = [];
  for (const [id, p] of models) {
    const r = (p.e ?? p.s)!;
    const purpose = r.purpose || "model";
    out.push({
      key: `m:${id}`,
      kind: "model",
      label: purpose,
      round: p.e?.round ?? p.s?.round,
      aux: purpose !== "turn" && purpose !== "model",
      ...bounds(p),
      par: 1,
      rows: [p.e ?? p.s!],
    });
  }
  const turns = out.filter((s) => !s.aux).sort((a, b) => a.end - b.end);

  for (const [id, p] of tools) {
    const r = (p.e ?? p.s)!;
    const b = bounds(p);
    // A tool line carries no round; it belongs to the turn that asked for
    // it, the last one to have answered by the time it started.
    const asked = turns.filter((t) => !t.open && t.end <= b.start + 50).pop();
    out.push({
      key: `t:${id}`,
      kind: "tool",
      label: r.tool || "tool",
      round: asked?.round,
      ...b,
      failed: !!p.e?.error,
      par: 1,
      rows: [p.s, p.e].filter(Boolean) as TraceRow[],
    });
  }

  const toolSpans = out.filter((s) => s.kind === "tool");
  for (const s of toolSpans) s.par = 1 + toolSpans.filter((o) => o !== s && overlaps(s, o)).length;
  for (const s of out) if (s.aux) s.besideTurn = turns.some((t) => overlaps(s, t));

  for (const [i, l] of lints.entries()) {
    const t = at(l);
    for (const turn of turns) if (turn.round === l.round) turn.linted = true;
    out.push({ key: `l:${i}`, kind: "lint", label: l.lint || "lint", round: l.round, start: t, end: t, failed: true, par: 1, rows: [l] });
  }

  // By start, and at the same instant a model call above the tools it led to.
  const order: Record<Kind, number> = { model: 0, tool: 1, lint: 2 };
  return out.sort((a, b) => a.start - b.start || order[a.kind] - order[b.kind]);
}

export default function TraceWaterfall({
  rows,
  renderDetail,
}: {
  rows: TraceRow[];
  renderDetail: (rows: TraceRow[]) => ReactNode;
}) {
  const runs = useMemo(() => runsOf(rows), [rows]);
  // Empty follows the newest run, so a live panel moves on to the next turn
  // by itself; picking one pins it.
  const [runId, setRunId] = useState<string | null>(null);
  const [sel, setSel] = useState("");
  const run = runs.find((r) => r.id === runId) ?? runs[0];
  const spans = useMemo(() => (run ? spansOf(run) : []), [run]);

  if (!run) return <div className="cr-trace-rows"><div className="cr-note">No model or tool calls in what is loaded.</div></div>;

  const t0 = Math.min(...spans.map((s) => s.start));
  const t1 = Math.max(...spans.map((s) => s.end));
  const total = Math.max(t1 - t0, 1);
  const step = STEPS.find((s) => total / s <= 8) ?? STEPS[STEPS.length - 1];
  const ticks = Array.from({ length: Math.floor(total / step) + 1 }, (_, i) => i * step);
  const pct = (ms: number) => ((ms - t0) / total) * 100;
  const picked = spans.find((s) => s.key === sel);
  const failed = spans.filter((s) => s.kind === "tool" && s.failed).length;
  const lints = spans.filter((s) => s.kind === "lint").length;
  const peak = Math.max(1, ...spans.map((s) => s.par));

  return (
    <div className="cr-wf">
      <div className="cr-trace-bar">
        <select className="input cr-wf-run" value={run.id} onChange={(e) => { setRunId(e.target.value); setSel(""); }}>
          {runs.map((r, i) => (
            <option key={r.id || i} value={r.id}>
              {hms(r.start)} · {dur(r.end - r.start)} · {r.calls} calls{i === 0 ? " · latest" : ""}
            </option>
          ))}
        </select>
        <span className="cr-wf-legend">
          <span><i className="sw model" />turn</span>
          <span><i className="sw aux" />auxiliary</span>
          <span><i className="sw tool" />tool</span>
          <span><i className="sw failed" />failed{failed ? ` ${failed}` : ""}</span>
          <span><i className="sw lint" />lint{lints ? ` ${lints}` : ""}</span>
          <span><em className="cr-wf-par">∥</em>overlapping{peak > 1 ? ` · peak ${peak}` : ""}</span>
        </span>
      </div>

      <div className="cr-wf-body" style={{ ["--wf-step" as string]: `${(step / total) * 100}%` }}>
        <div className="cr-wf-row head">
          <span className="cr-wf-label">{spans.length} spans · {dur(total)}</span>
          <span className="cr-wf-track">
            {ticks.map((t) => (
              <span key={t} className="cr-wf-tick" style={{ left: `${(t / total) * 100}%` }}>+{dur(t)}</span>
            ))}
          </span>
        </div>
        {spans.map((s) => {
          const l = pct(s.start);
          const w = pct(s.end) - l;
          const cls = [s.kind, s.aux ? "aux" : "", s.failed ? "failed" : "", s.linted ? "linted" : "", s.open ? "open" : "", s.derived ? "derived" : "", sel === s.key ? "sel" : ""].filter(Boolean).join(" ");
          const took = s.end - s.start;
          const title = [
            s.kind === "lint" ? `lint ${s.label}` : s.label,
            s.round !== undefined ? `round ${s.round}` : "",
            s.kind === "lint" ? hms(s.start) : `${hms(s.start)} · ${dur(took)}${s.open ? " · no end" : ""}`,
            s.par > 1 ? `${s.par} at once` : "",
            s.besideTurn ? "beside the turn" : "",
            s.rows.find((r) => r.error)?.error ?? (s.kind === "lint" ? s.rows[0].reason : ""),
          ].filter(Boolean).join("\n");
          return (
            <div key={s.key} className={`cr-wf-row ${cls}`} onClick={() => setSel(sel === s.key ? "" : s.key)} title={title}>
              <span className="cr-wf-label">
                <b>{s.label}</b>
                {s.round !== undefined && <i>r{s.round}</i>}
                {s.par > 1 && <em className="cr-wf-par">∥{s.par}</em>}
                {s.besideTurn && <em className="cr-wf-par">∥turn</em>}
              </span>
              <span className="cr-wf-track">
                {s.kind === "lint" ? (
                  <span className="cr-wf-mark" style={{ left: `${l}%` }} />
                ) : (
                  <span className="cr-wf-span" style={{ left: `${l}%`, width: `${w}%` }} />
                )}
                {s.kind !== "lint" && (
                  <span className="cr-wf-dur" style={l + w > 82 ? { right: `calc(${100 - l}% + 4px)` } : { left: `calc(${l + w}% + 4px)` }}>
                    {dur(took)}
                  </span>
                )}
              </span>
            </div>
          );
        })}
      </div>

      {picked && <div className="cr-wf-detail">{renderDetail(picked.rows)}</div>}
    </div>
  );
}
