import { useCallback, useEffect, useMemo, useState } from "react";
import { ToolStats } from "../../wailsjs/go/app/App";
import { RotateCw } from "lucide-react";

/**
 * Every tool's calls over a window, read back from every trace on disk: how
 * often it ran, how often it failed, and how long it took.
 *
 * It sits beside one run's rows and waterfall because it is the same data
 * seen from the other end — a single slow call in a waterfall is a question,
 * and this table says whether that tool is always that slow. The backend does
 * the reading (a week of traces is too much to ship to the page) and bounds
 * it; when the bound cuts in, the header says the figures are partial.
 */

interface Stat {
  tool: string;
  calls: number;
  failures: number;
  failureRate: number;
  meanMs: number;
  p50Ms: number;
  p95Ms: number;
  maxMs: number;
  lastCalled: string;
}

interface Report {
  since: string;
  files: number;
  bytes: number;
  truncated: boolean;
  tools: Stat[];
}

type Col = keyof Stat;

const WINDOWS = [1, 7, 30];

const COLS: { k: Col; label: string; num?: boolean }[] = [
  { k: "tool", label: "tool" },
  { k: "calls", label: "calls", num: true },
  { k: "failures", label: "failed", num: true },
  { k: "failureRate", label: "fail %", num: true },
  { k: "meanMs", label: "mean", num: true },
  { k: "p50Ms", label: "p50", num: true },
  { k: "p95Ms", label: "p95", num: true },
  { k: "maxMs", label: "max", num: true },
  { k: "lastCalled", label: "last called" },
];

const dur = (ms: number) => (ms < 1000 ? `${ms}ms` : ms < 60000 ? `${(ms / 1000).toFixed(1)}s` : `${(ms / 60000).toFixed(1)}m`);
const when = (ts: string) => {
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};
const mb = (b: number) => (b >= 1 << 20 ? `${(b / (1 << 20)).toFixed(1)} MB` : `${Math.ceil(b / 1024)} KB`);

export default function ToolStatsPanel() {
  const [days, setDays] = useState(7);
  const [rep, setRep] = useState<Report | null>(null);
  const [busy, setBusy] = useState(false);
  const [sort, setSort] = useState<{ k: Col; desc: boolean }>({ k: "calls", desc: true });

  const load = useCallback(async () => {
    setBusy(true);
    try {
      setRep((await ToolStats(days)) as Report);
    } catch {
      /* keep what is on screen */
    } finally {
      setBusy(false);
    }
  }, [days]);

  useEffect(() => {
    load();
  }, [load]);

  const rows = useMemo(() => {
    const out = [...(rep?.tools ?? [])];
    const { k, desc } = sort;
    out.sort((a, b) => {
      const x = a[k], y = b[k];
      const c = typeof x === "string" ? String(x).localeCompare(String(y)) : (x as number) - (y as number);
      return (desc ? -c : c) || a.tool.localeCompare(b.tool);
    });
    return out;
  }, [rep, sort]);

  // A name sorts A to Z first; a number, largest first, which is the end of
  // the column anyone sorting by it is looking for.
  const by = (k: Col) => setSort((s) => (s.k === k ? { k, desc: !s.desc } : { k, desc: k !== "tool" }));

  const calls = rows.reduce((n, r) => n + r.calls, 0);
  const failures = rows.reduce((n, r) => n + r.failures, 0);

  return (
    <>
      <div className="cr-trace-bar">
        {WINDOWS.map((d) => (
          <button key={d} className={`cr-btn tiny${days === d ? " primary" : ""}`} onClick={() => setDays(d)}>
            {d}d
          </button>
        ))}
        <span className="cr-ts-sum">
          {rep && (
            <>
              <b>{rows.length}</b> tools · <b>{calls}</b> calls · <b className={failures ? "rose" : ""}>{failures}</b> failed · {rep.files} files · {mb(rep.bytes)}
            </>
          )}
        </span>
        {rep?.truncated && <span className="cr-tag amber">partial</span>}
        <button className="cr-btn tiny" onClick={load} disabled={busy}>
          <RotateCw size={10} />
          {busy ? "…" : "Reload"}
        </button>
      </div>
      <div className="cr-trace-rows cr-ts">
        <table className="cr-table">
          <thead>
            <tr>
              {COLS.map((c) => (
                <th key={c.k} className={`${c.num ? "num " : ""}${sort.k === c.k ? "on" : ""}`} onClick={() => by(c.k)} aria-sort={sort.k === c.k ? (sort.desc ? "descending" : "ascending") : undefined}>
                  {c.label}
                  {sort.k === c.k ? (sort.desc ? " ↓" : " ↑") : ""}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.tool} className={r.failures ? "bad" : ""}>
                <td className="name" title={r.tool}>{r.tool}</td>
                <td className="num">{r.calls}</td>
                <td className="num">{r.failures || ""}</td>
                <td className="num rate">{r.failures ? `${(r.failureRate * 100).toFixed(r.failureRate < 0.1 ? 1 : 0)}%` : ""}</td>
                <td className="num">{dur(r.meanMs)}</td>
                <td className="num">{dur(r.p50Ms)}</td>
                <td className="num">{dur(r.p95Ms)}</td>
                <td className="num">{dur(r.maxMs)}</td>
                <td className="when">{when(r.lastCalled)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {rep && rows.length === 0 && <div className="cr-note">No tool calls in the last {days === 1 ? "day" : `${days} days`}.</div>}
      </div>
    </>
  );
}
