import { useCallback, useEffect, useMemo, useState } from "react";
import { TraceLines } from "../../wailsjs/go/main/App";
import { useImeGuard } from "@/lib/ime";
import { RotateCw, ScrollText, X } from "lucide-react";

/**
 * One task's JSONL trace, a row per event.
 *
 * The run wall narrates a task in prose — enough to see it is alive and going
 * well. This is the other half: what exactly happened in round 34, which tool
 * took eleven seconds, where the cache stopped hitting. agent-go's TraceWriter
 * emits it as one JSON object per line and the backend keeps one file per
 * task, so the panel's whole job is to page the tail and let it be filtered.
 *
 * Parsing happens here rather than in Go on purpose: the framework adds fields
 * to a trace line as it grows new seams, and a panel that reads the JSON it is
 * given picks them up without a backend release.
 */

interface TraceRow {
  ts?: string;
  event?: string;
  round?: number;
  tool?: string;
  subagent?: string;
  lint?: string;
  verdict?: string;
  duration_ms?: number;
  tokens?: { total?: number; prompt?: number; completion?: number; cached?: number };
  tool_calls?: number;
  text_len?: number;
  reason?: string;
  message?: string;
  marker?: string;
  error?: string;
  result?: string;
  kind?: string;
  attempt?: number;
  trigger?: string;
  stop_reason?: string;
  checkpoint_reason?: string;
  segment_index?: number;
  segment_total?: number;
}

const PAGES = [200, 600, 2000];

const hms = (ts?: string) => {
  if (!ts) return "--:--:--";
  const d = new Date(ts);
  return isNaN(d.getTime()) ? "--:--:--" : d.toTimeString().slice(0, 8);
};
const dur = (ms?: number) => (ms === undefined ? "" : ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`);
const tok = (n?: number) => (n === undefined ? "" : n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n));

// what names the row: whichever of tool / sub-agent / lint / marker this event
// is about. One column, because no event has two of them.
const subject = (r: TraceRow) =>
  r.tool ?? r.subagent ?? r.lint ?? r.marker ?? r.trigger ?? r.kind ?? r.checkpoint_reason ?? r.stop_reason ?? "";

// what the row says, if anything: an error beats a reason beats a result.
const detail = (r: TraceRow) => r.error ?? r.reason ?? r.message ?? r.result ?? "";

export default function RunTracePanel({
  taskId,
  live,
  onClose,
}: {
  taskId: string;
  live?: boolean;
  onClose: () => void;
}) {
  const [lines, setLines] = useState<string[]>([]);
  const [limit, setLimit] = useState(PAGES[0]);
  const [filter, setFilter] = useState("");
  const [busy, setBusy] = useState(false);
  const ime = useImeGuard();

  const load = useCallback(async () => {
    setBusy(true);
    try {
      setLines((await TraceLines(taskId, limit)) ?? []);
    } catch {
      /* keep what is on screen */
    } finally {
      setBusy(false);
    }
  }, [taskId, limit]);

  useEffect(() => {
    load();
  }, [load]);

  // A trace reaches the file as the run writes it, so a running task's panel
  // catches up on a timer rather than waiting for the run to end.
  useEffect(() => {
    if (!live) return;
    const t = window.setInterval(load, 2000);
    return () => window.clearInterval(t);
  }, [live, load]);

  const rows = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const out: TraceRow[] = [];
    for (const l of lines) {
      if (q && !l.toLowerCase().includes(q)) continue;
      try {
        out.push(JSON.parse(l) as TraceRow);
      } catch {
        out.push({ event: "unparsed", message: l });
      }
    }
    return out;
  }, [lines, filter]);

  return (
    <div className="cr-trace-overlay" onClick={onClose}>
      <div className="cr-trace" onClick={(e) => e.stopPropagation()}>
        <div className="cr-panel-h">
          <ScrollText size={13} />
          <span className="cr-eyebrow">Trace · {taskId}</span>
          <span className="cr-tag">{rows.length} / {lines.length}</span>
          <button className="cr-trace-x" onClick={onClose} aria-label="Close trace">
            <X size={14} />
          </button>
        </div>

        <div className="cr-trace-bar">
          <input
            className="input"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            onCompositionStart={ime.handlers.onCompositionStart}
            onCompositionEnd={ime.handlers.onCompositionEnd}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !ime.composing(e)) {
                e.preventDefault();
                load();
              }
            }}
            placeholder="filter — fs_read, lint, error, model_end…"
            autoComplete="off"
          />
          {PAGES.map((n) => (
            <button
              key={n}
              className={`cr-btn tiny${limit === n ? " primary" : ""}`}
              onClick={() => setLimit(n)}
            >
              {n}
            </button>
          ))}
          <button className="cr-btn tiny" onClick={load} disabled={busy}>
            <RotateCw size={10} />
            {busy ? "…" : "Reload"}
          </button>
        </div>

        <div className="cr-trace-rows">
          <div className="cr-trace-row head">
            <span>time</span>
            <span>event</span>
            <span>r</span>
            <span>subject</span>
            <span>took</span>
            <span>tokens</span>
            <span>detail</span>
          </div>
          {rows.length === 0 && (
            <div className="cr-note">
              {lines.length === 0
                ? "No trace for this task. Traces are kept for tasks launched from here; a chat turn writes none."
                : "Nothing matches that filter."}
            </div>
          )}
          {rows.map((r, i) => (
            <div key={i} className={`cr-trace-row ${r.event ?? ""}${r.error ? " bad" : ""}`}>
              <span className="t">{hms(r.ts)}</span>
              <span className="e">{r.event ?? "?"}</span>
              <span className="r">{r.round ?? ""}</span>
              <span className="s" title={subject(r)}>{subject(r)}</span>
              <span className="d">{dur(r.duration_ms)}</span>
              <span className="k" title={r.tokens?.cached ? `${r.tokens.cached} cached` : undefined}>
                {tok(r.tokens?.total)}
                {r.tokens?.cached ? <i> ·{tok(r.tokens.cached)}c</i> : null}
              </span>
              <span className="x" title={detail(r)}>{detail(r)}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
