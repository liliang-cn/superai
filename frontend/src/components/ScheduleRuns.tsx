import React, { useEffect, useState } from "react";
import { CopyIcon, MessageSquareIcon, XIcon } from "lucide-react";
import { Response } from "@/components/ai-elements/response";
import { ScheduleRun, ScheduleRunLog } from "../lib/useScheduleRuns";
import {
  copyText,
  firstLine,
  formatDuration,
  formatStamp,
  parseTime,
} from "../lib/format";

/**
 * Reporting a scheduled run that finished while the app was open.
 *
 * The backend already sent a system banner for the user who is away; this is for
 * the one who is here. A toast in the corner rather than anything modal: a run
 * fires on its own schedule, so it must never interrupt whatever the user was
 * doing — but a failure has to be impossible to miss, and the answer has to be
 * reachable in full, which a banner cannot do.
 */

function runLabel(run: ScheduleRun): string {
  if (run.cancelled) return "Scheduled run stopped";
  return run.error !== "" ? "Scheduled run failed" : "Scheduled run finished";
}

/**
 * The dot beside a run. A stop is deliberately neutral rather than red: the
 * user asked for it, and marking their own decision as a fault is the thing
 * this whole feature exists to avoid.
 */
function runDot(run: ScheduleRun): string {
  if (run.cancelled) return "unknown";
  return run.error !== "" ? "bad" : "ok";
}

/** What a run has to show for itself once it is over. */
function runBody(run: ScheduleRun): string {
  if (run.error !== "") return run.error;
  if (run.answer !== "") return run.answer;
  return run.cancelled ? "Stopped before it finished." : "";
}

/** The meta line every surface shows: when it started and how long it took. */
function RunMeta({ run }: { run: ScheduleRun }) {
  const started = parseTime(run.startedAt);
  return (
    <span className="run-meta">
      {started ? formatStamp(started) : run.startedAt || "just now"}
      {run.durationMs > 0 ? ` · took ${formatDuration(run.durationMs)}` : ""}
      {run.session !== "" ? ` · ${run.session}` : ""}
    </span>
  );
}

/**
 * The whole answer. A scheduled prompt tends to produce a report rather than a
 * sentence, so it renders through the same pipeline as a chat answer — tables
 * and charts included — instead of being flattened into a preview string.
 */
function RunModal({
  run,
  onClose,
  onOpenConversation,
}: {
  run: ScheduleRun;
  onClose: () => void;
  onOpenConversation?: (session: string) => void;
}) {
  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="modal-title">
            {firstLine(run.prompt, 90) || runLabel(run)}
          </span>
          <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
            {run.answer !== "" && (
              <button
                className="btn ghost sm"
                onClick={() => copyText(run.answer)}
              >
                <CopyIcon className="size-3.5" /> Copy
              </button>
            )}
            {run.session !== "" && onOpenConversation && (
              <button
                className="btn ghost sm"
                onClick={() => {
                  onOpenConversation(run.session);
                  onClose();
                }}
              >
                <MessageSquareIcon className="size-3.5" /> Open conversation
              </button>
            )}
            <button className="modal-close" onClick={onClose}>
              ×
            </button>
          </span>
        </div>
        <div className="modal-body">
          <div style={{ marginBottom: 10 }}>
            <RunMeta run={run} />
          </div>
          {run.error !== "" && (
            <div className="report-error">⚠ {run.error}</div>
          )}
          {run.answer !== "" ? (
            <Response>{run.answer}</Response>
          ) : (
            run.error === "" && (
              <div className="trace-empty">The run produced no answer.</div>
            )
          )}
        </div>
      </div>
    </div>
  );
}

/** The toast stack. Renders nothing at all when no run is waiting to be read. */
export function ScheduleRunToasts({
  log,
  onOpenConversation,
}: {
  log: ScheduleRunLog;
  onOpenConversation: (session: string) => void;
}) {
  const [detail, setDetail] = useState<ScheduleRun | null>(null);

  // A success has been dealt with by being read, and a card parked over the
  // composer for the rest of the session is its own kind of interruption — so it
  // retires itself, leaving the sidebar count and the Schedules list as the
  // record. A failure stays until it is closed.
  const { dismiss } = log;
  const expiring = log.toasts
    .filter((r) => r.error === "")
    .map((r) => r.key)
    .join(",");
  useEffect(() => {
    if (expiring === "") return;
    const timers = expiring
      .split(",")
      .map((key) => window.setTimeout(() => dismiss(key), 14000));
    return () => timers.forEach((t) => window.clearTimeout(t));
  }, [expiring, dismiss]);

  // Deliberately not `if (toasts.length === 0) return null` up here: the modal
  // below is opened *from* a toast, and a success retires after 14s, so an early
  // return would close the full answer out from under someone reading it.
  if (log.toasts.length === 0 && detail === null) return null;
  return (
    <>
      {log.toasts.length > 0 && (
        <div className="run-toasts">
          {log.toasts.map((run) => (
            <div
              key={run.key}
              className={`run-toast${run.error !== "" ? " failed" : ""}`}
            >
              <div className="rt-head">
                <span className={`status-dot ${runDot(run)}`} />
                <span>{runLabel(run)}</span>
                <button
                  className="panel-toggle inline"
                  style={{ marginLeft: "auto" }}
                  title="Dismiss"
                  aria-label="Dismiss"
                  onClick={() => log.dismiss(run.key)}
                >
                  <XIcon className="size-3.5" />
                </button>
              </div>
              <div className="rt-prompt">{firstLine(run.prompt, 80)}</div>
              <div
                className={`rt-body${run.error !== "" ? " run-failed" : ""}`}
              >
                {runBody(run)}
              </div>
              <div className="rt-actions">
                {run.session !== "" && (
                  <button
                    className="btn ghost sm"
                    onClick={() => onOpenConversation(run.session)}
                  >
                    Open conversation
                  </button>
                )}
                <button className="btn ghost sm" onClick={() => setDetail(run)}>
                  {run.error !== "" ? "Details" : "Full answer"}
                </button>
                <RunMeta run={run} />
              </div>
            </div>
          ))}
          {log.hidden > 0 && (
            <div className="run-toast-more">
              {log.hidden} more finished — see Schedules.
            </div>
          )}
        </div>
      )}
      {detail && (
        <RunModal
          run={detail}
          onClose={() => setDetail(null)}
          onOpenConversation={onOpenConversation}
        />
      )}
    </>
  );
}

/**
 * The same runs as a list, so closing a toast is not the same as losing the
 * result — particularly a failure, which is the one a user wants to come back to.
 */
export function ScheduleRunList({
  log,
  onOpenConversation,
}: {
  log: ScheduleRunLog;
  onOpenConversation: (session: string) => void;
}) {
  const [detail, setDetail] = useState<ScheduleRun | null>(null);
  if (log.runs.length === 0) return null;
  return (
    <>
      <div className="view-subhead">
        Runs this session
        <button className="btn ghost sm" onClick={log.clear}>
          Clear
        </button>
      </div>
      <div className="record-list">
        {log.runs.map((run) => (
          <div key={run.key} className="record-card">
            <div
              className="rc-title"
              style={{ display: "flex", alignItems: "center", gap: 8 }}
            >
              <span className={`status-dot ${runDot(run)}`} />
              <span className="rt-prompt">{firstLine(run.prompt, 80)}</span>
              <span className="sched-actions">
                {run.session !== "" && (
                  <button
                    className="btn ghost sm"
                    onClick={() => onOpenConversation(run.session)}
                  >
                    Open conversation
                  </button>
                )}
                <button className="btn ghost sm" onClick={() => setDetail(run)}>
                  {run.error !== "" ? "Details" : "Full answer"}
                </button>
              </span>
            </div>
            <div className="rc-row">
              <RunMeta run={run} />
            </div>
            <div className="rc-row">
              <span
                className={`rt-body${run.error !== "" ? " run-failed" : ""}`}
              >
                {runBody(run) || "No answer."}
              </span>
            </div>
          </div>
        ))}
      </div>
      {detail && (
        <RunModal
          run={detail}
          onClose={() => setDetail(null)}
          onOpenConversation={onOpenConversation}
        />
      )}
    </>
  );
}
