import React, { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { HiveTaskDetail } from "../../wailsjs/go/app/App";

/** A step in a task's life, as the backend records it. */
export interface TaskStep {
  at: string;
  phase: string;
  tool?: string;
}

export interface FullTask {
  id: string;
  worker: string;
  from?: string;
  dir: "out" | "in" | "peer";
  prompt: string;
  state: "running" | "done" | "failed" | "cancelled";
  phase?: string;
  tool?: string;
  tools: number;
  started_at: string;
  ended_at?: string;
  result?: string;
  error?: string;
  events?: TaskStep[];
}

const short = (n: string) => n.replace(/^superai-/, "");

const stepLabel = (e: TaskStep) => {
  switch (e.phase) {
    case "tool":
      return `⚙ ${e.tool || "tool"}`;
    case "sent":
      return "order sent";
    case "thinking":
      return "thinking";
    case "writing":
      return "writing the answer";
    case "done":
      return "done";
    case "failed":
      return "failed";
    case "cancelled":
      return "cancelled";
    default:
      return e.phase;
  }
};

function span(ms: number): string {
  const s = Math.max(0, ms / 1000);
  if (s < 10) return `${s.toFixed(1)}s`;
  if (s < 60) return `${Math.round(s)}s`;
  return `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
}

/**
 * One order, whole.
 *
 * It reads the live list first and asks the backend only when the id is not on
 * it — a page reloaded, or a link opened from somewhere else. The live copy is
 * preferred because it keeps moving: the timeline grows and the clock runs
 * while the task does, without this page polling for it.
 */
export default function HiveTaskPage({
  id,
  live,
  now,
}: {
  id: string;
  live?: FullTask;
  now: number;
}) {
  const [fetched, setFetched] = useState<FullTask | null>(null);
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (live) return;
    let alive = true;
    HiveTaskDetail(id)
      .then((r: any) => {
        if (!alive) return;
        if (r?.ok) setFetched(r.task as FullTask);
        else setErr(String(r?.error || "not found"));
      })
      .catch((e: any) => alive && setErr(String(e?.message || e)));
    return () => {
      alive = false;
    };
  }, [id, live]);

  const t = live ?? fetched;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(id);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      /* clipboard is not always there; the id is on screen to select */
    }
  };

  return (
    <div className="hive-task">
      <Link to="/hive" className="hive-back">← Hive</Link>

      <div className="hive-task-id">
        <code>{id}</code>
        <button className="btn ghost sm" onClick={copy}>{copied ? "Copied" : "Copy"}</button>
      </div>

      {!t && (
        <div className="card">
          <div className="hive-dim">{err || "Looking for it…"}</div>
        </div>
      )}

      {t && (
        <>
          <div className="card hive-task-head">
            <span className={`hive-live-dot ${t.state}`} />
            <div className="hive-self-body">
              <div className="hive-self-name">{t.state === "running" ? "Running" : t.state}</div>
              <div className="hive-dim">
                {t.dir === "peer"
                  ? `${short(t.from ?? "?")} asked ${short(t.worker)}`
                  : t.dir === "out"
                    ? `the queen ordered ${short(t.worker)}`
                    : `${short(t.worker)} received it from the queen`}
              </div>
            </div>
            <div className="hive-task-clock">
              {span((t.ended_at && !t.ended_at.startsWith("0001") ? Date.parse(t.ended_at) : now) - Date.parse(t.started_at))}
            </div>
          </div>

          <div className="card">
            <div className="card-title">Timeline</div>
            <ol className="hive-steps">
              {(t.events ?? []).map((e, i, all) => {
                const last = i === all.length - 1;
                const end = last && t.state === "running";
                const at = Date.parse(e.at);
                const next = all[i + 1] ? Date.parse(all[i + 1].at) : end ? now : at;
                return (
                  <li key={i} className={`hive-step ${e.phase}${end ? " now" : ""}`}>
                    <span className="hive-step-dot" />
                    <span className="hive-step-what">{stepLabel(e)}</span>
                    <span className="hive-dim hive-step-at">+{span(at - Date.parse(t.started_at))}</span>
                    {(e.phase === "thinking" || e.phase === "tool" || e.phase === "writing") && (
                      <span className="hive-dim hive-step-for">{span(next - at)}</span>
                    )}
                  </li>
                );
              })}
            </ol>
            <div className="hive-dim">
              {t.tools} tool call{t.tools === 1 ? "" : "s"} · session <code>hive:{t.id}</code>
            </div>
          </div>

          <div className="card">
            <div className="card-title">Order</div>
            <pre className="hive-pre">{t.prompt}</pre>
          </div>

          {(t.result || t.error) && (
            <div className="card">
              <div className="card-title">{t.error ? "Failed" : "Answer"}</div>
              <pre className={`hive-pre${t.error ? " err" : ""}`}>{t.error || t.result}</pre>
              {t.error && t.result && <pre className="hive-pre">{t.result}</pre>}
            </div>
          )}
        </>
      )}
    </div>
  );
}
