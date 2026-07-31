import { useCallback, useEffect, useMemo, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";

/** One finished scheduled run, as the backend reports it. */
export interface ScheduleRun {
  prompt: string;
  session: string;
  answer: string;
  /** "" on success. */
  error: string;
  startedAt: string;
  durationMs: number;
}

export interface ScheduleRunEntry extends ScheduleRun {
  /** Local identity. The event carries no schedule id, so the log makes its own. */
  key: string;
  receivedAt: number;
  /** The user has been to the Schedules view since this arrived. */
  seen: boolean;
  /** The toast for this run has been closed (or acknowledged wholesale). */
  dismissed: boolean;
}

export interface ScheduleRunLog {
  /** Newest first. */
  runs: ScheduleRunEntry[];
  /** The runs a toast should be showing, newest first. */
  toasts: ScheduleRunEntry[];
  /** Runs that finished but have no toast on screen, because of the cap. */
  hidden: number;
  /** How many runs the user has not looked at yet — the sidebar badge. */
  unseen: number;
  dismiss: (key: string) => void;
  /** Mark everything seen and close every toast. */
  acknowledge: () => void;
  clear: () => void;
}

/** How much history to keep. A run's answer can be long; this is memory only. */
const KEEP = 20;
/** More than a few toasts at once stops being a notification and becomes a wall. */
const MAX_TOASTS = 3;

let counter = 0;

/**
 * The record of scheduled runs that finished while the app was open.
 *
 * Mounted once at the app root rather than inside the Schedules view: a run
 * fires whenever its cron says so, and the user is usually looking at a
 * conversation at the time. A listener living in the view would drop every run
 * that happened while the view was closed, which is nearly all of them.
 *
 * Runs are only ever appended, never matched back to a schedule — the event has
 * no schedule id, and a run whose schedule was deleted mid-flight still happened
 * and still needs reporting.
 */
export function useScheduleRuns(): ScheduleRunLog {
  const [runs, setRuns] = useState<ScheduleRunEntry[]>([]);

  useEffect(() => {
    // The Wails runtime only exists inside the desktop webview; guard so the SPA
    // still renders in a plain browser.
    if (typeof window === "undefined" || !(window as any).runtime) return;
    const off = EventsOn("schedule:run", (payload: Partial<ScheduleRun> | null) => {
      if (!payload) return;
      counter += 1;
      const entry: ScheduleRunEntry = {
        key: `run-${Date.now()}-${counter}`,
        prompt: String(payload.prompt ?? ""),
        session: String(payload.session ?? ""),
        answer: String(payload.answer ?? ""),
        error: String(payload.error ?? ""),
        startedAt: String(payload.startedAt ?? ""),
        durationMs: Number(payload.durationMs ?? 0),
        receivedAt: Date.now(),
        seen: false,
        dismissed: false,
      };
      setRuns((prev) => [entry, ...prev].slice(0, KEEP));
    });
    return () => off();
  }, []);

  const dismiss = useCallback((key: string) => {
    setRuns((prev) => prev.map((r) => (r.key === key ? { ...r, dismissed: true } : r)));
  }, []);

  const acknowledge = useCallback(() => {
    // Returning the previous array when there is nothing to change keeps this
    // safe to call from an effect that depends on the log.
    setRuns((prev) =>
      prev.every((r) => r.seen && r.dismissed)
        ? prev
        : prev.map((r) => ({ ...r, seen: true, dismissed: true })),
    );
  }, []);

  const clear = useCallback(() => setRuns([]), []);

  const toasts = useMemo(() => runs.filter((r) => !r.dismissed), [runs]);
  const unseen = useMemo(() => runs.filter((r) => !r.seen).length, [runs]);

  return {
    runs,
    toasts: toasts.slice(0, MAX_TOASTS),
    hidden: Math.max(0, toasts.length - MAX_TOASTS),
    unseen,
    dismiss,
    acknowledge,
    clear,
  };
}
