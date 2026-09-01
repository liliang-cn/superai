import React, { useCallback, useEffect, useState } from "react";
import { Dashboard, GraphView as startGraphView } from "../../wailsjs/go/main/App";
import { openExternal } from "../lib/openExternal";
import { formatDuration, fromNow, parseTime } from "../lib/format";

/**
 * One glance at what SuperAI is: the brain it runs on, what is in flight,
 * what it has spent, what its task memory holds — and the memory graph, live.
 *
 * Everything except the graph comes from a single polled RPC (App.Dashboard),
 * because a dashboard assembled from five calls shows five different moments.
 * The graph rides in the same proxied frame the Knowledge page uses; starting
 * it here has the same lifetime semantics — once up, it stays up.
 */

interface ActiveRun {
  run_id: string;
  session_id?: string;
  task_id?: string;
  started_at: string;
}

interface TaskRow {
  id: string;
  goal: string;
  status: string;
  brief: string;
  updatedAt: string;
  runs: number;
}

interface UsageInfo {
  totalTokens: number;
  cachedTokens: number;
  modelTurns: number;
  today: number;
  days: { day: string; tokens: number }[];
}

interface DashData {
  ready: boolean;
  llm?: { model: string; baseURL: string; embedModel: string; maxRounds: number; workspace: string };
  memoryMode?: string;
  activeRuns?: ActiveRun[];
  usage?: UsageInfo;
  tasks?: TaskRow[];
}

// Same reasoning as KnowledgeView: served over HTTP the loopback-bound graph
// is unreachable, so the app proxies it under its own origin.
const SERVED = Boolean((window as unknown as Record<string, unknown>).superaiServed);
const GRAPH_SRC = SERVED ? "/graph/" : null;

function fmtTokens(n: number | undefined): string {
  const v = n ?? 0;
  if (v >= 1_000_000) return (v / 1_000_000).toFixed(2) + "M";
  if (v >= 1_000) return (v / 1_000).toFixed(1) + "k";
  return String(v);
}

const STATUS_TONE: Record<string, string> = {
  running: "ok",
  completed: "ok",
  pending: "unknown",
  blocked: "bad",
  failed: "bad",
  cancelled: "unknown",
};

function endpointHost(u: string): string {
  try {
    return new URL(u).host;
  } catch {
    return u;
  }
}

/** Tiny inline bar chart for the last two weeks of tokens. */
function UsageBars({ days }: { days: { day: string; tokens: number }[] }) {
  if (!days?.length) return null;
  const max = Math.max(...days.map((d) => d.tokens), 1);
  return (
    <div className="dash-bars" aria-hidden>
      {days.map((d) => (
        <div
          key={d.day}
          className="dash-bar"
          style={{ height: `${Math.max(6, Math.round((d.tokens / max) * 100))}%` }}
          title={`${d.day}: ${d.tokens.toLocaleString()} tokens`}
        />
      ))}
    </div>
  );
}

export default function DashboardView() {
  const [data, setData] = useState<DashData | null>(null);
  const [graphURL, setGraphURL] = useState("");
  const [graphErr, setGraphErr] = useState("");
  const [now, setNow] = useState(Date.now());

  const load = useCallback(async () => {
    try {
      setData((await Dashboard()) as DashData);
    } catch {
      // A failed poll keeps the last picture; the next one repaints it.
    }
    setNow(Date.now());
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [load]);

  // The graph view starts on first visit of this page and stays up.
  useEffect(() => {
    (async () => {
      try {
        const st = (await startGraphView()) as Record<string, any>;
        setGraphURL(String(st?.url ?? ""));
        setGraphErr(String(st?.error ?? ""));
      } catch (e: any) {
        setGraphErr(String(e?.message || e));
      }
    })();
  }, []);

  const runs = data?.activeRuns ?? [];
  const tasks = data?.tasks ?? [];
  const usage = data?.usage;
  const frameSrc = GRAPH_SRC ?? graphURL;

  return (
    <div className="dash">
      <div className="dash-grid">
        <div className="card dash-card">
          <div className="card-title">🧠 Brain</div>
          {data?.llm ? (
            <div className="dash-kv">
              <div><span className="dash-k">Model</span><span className="dash-v">{data.llm.model || "—"}</span></div>
              <div><span className="dash-k">Endpoint</span><span className="dash-v">{endpointHost(data.llm.baseURL) || "—"}</span></div>
              <div><span className="dash-k">Embeddings</span><span className="dash-v">{data.llm.embedModel || "off"}</span></div>
              <div><span className="dash-k">Memory</span><span className="dash-v">{data.memoryMode || "—"}</span></div>
              <div><span className="dash-k">Max rounds</span><span className="dash-v">{data.llm.maxRounds}</span></div>
            </div>
          ) : (
            <div className="card-desc">{data ? "No account configured yet." : "Loading…"}</div>
          )}
        </div>

        <div className="card dash-card">
          <div className="card-title">🔢 Tokens</div>
          <div className="dash-stats">
            <div className="dash-stat">
              <div className="dash-stat-n">{fmtTokens(usage?.today)}</div>
              <div className="dash-stat-l">today</div>
            </div>
            <div className="dash-stat">
              <div className="dash-stat-n">{fmtTokens(usage?.totalTokens)}</div>
              <div className="dash-stat-l">total</div>
            </div>
            <div className="dash-stat">
              <div className="dash-stat-n">{fmtTokens(usage?.cachedTokens)}</div>
              <div className="dash-stat-l">cache hits</div>
            </div>
            <div className="dash-stat">
              <div className="dash-stat-n">{(usage?.modelTurns ?? 0).toLocaleString()}</div>
              <div className="dash-stat-l">model turns</div>
            </div>
          </div>
          <UsageBars days={usage?.days ?? []} />
          {(usage?.modelTurns ?? 0) > 0 && (usage?.totalTokens ?? 0) === 0 && (
            <div className="card-desc" style={{ marginTop: 8, marginBottom: 0 }}>
              The provider is not reporting token usage — turns are counted, tokens cannot be.
            </div>
          )}
        </div>

        <div className="card dash-card">
          <div className="card-title">⚡ Running now</div>
          {runs.length === 0 ? (
            <div className="card-desc">Idle — nothing in flight.</div>
          ) : (
            <div className="dash-list">
              {runs.map((r) => {
                const started = parseTime(r.started_at);
                return (
                  <div key={r.run_id} className="dash-row">
                    <span className="status-dot ok" />
                    <span className="dash-row-main">
                      {r.task_id ? `task ${r.task_id.slice(0, 8)}` : `session ${String(r.session_id || "").slice(0, 8)}`}
                    </span>
                    <span className="dash-row-side">
                      {started ? formatDuration(now - started.getTime()) : ""}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="card dash-card dash-tasks">
          <div className="card-title">🗒️ Task memory</div>
          {tasks.length === 0 ? (
            <div className="card-desc">No long tasks recorded yet — they appear here once a segmented run starts.</div>
          ) : (
            <div className="dash-list">
              {tasks.map((t) => {
                const upd = parseTime(t.updatedAt);
                return (
                  <div key={t.id} className="dash-task" title={t.brief || t.goal}>
                    <div className="dash-row">
                      <span className={`status-dot ${STATUS_TONE[t.status] ?? "unknown"}`} />
                      <span className="dash-row-main">{t.goal}</span>
                      <span className="dash-row-side">
                        {t.runs} run{t.runs === 1 ? "" : "s"}{upd ? ` · ${fromNow(upd, now)}` : ""}
                      </span>
                    </div>
                    {t.brief && <div className="dash-brief">{t.brief}</div>}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>

      <div className="card dash-card dash-graph">
        <div className="card-title">🕸️ Memory graph</div>
        {frameSrc ? (
          <iframe className="dash-frame" src={frameSrc} title="Memory graph" />
        ) : graphErr ? (
          <div className="card-desc">{graphErr}</div>
        ) : graphURL ? (
          <div className="card-desc">
            <a href={graphURL} onClick={(e) => { e.preventDefault(); openExternal(graphURL); }}>
              Open the graph view
            </a>
          </div>
        ) : (
          <div className="card-desc">Starting the graph view…</div>
        )}
      </div>
    </div>
  );
}
