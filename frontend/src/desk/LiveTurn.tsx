import React, { useEffect, useRef, useState } from "react";
import { BookmarkCheckIcon, CheckIcon, ChevronDownIcon, XIcon } from "lucide-react";
import { ChatMessage, TraceItem } from "../lib/types";
import { fmtK, Snap } from "../components/Reactor";
import "./liveturn.css";

/**
 * What a turn is doing while it does it: how long, how many tokens so far,
 * every tool it called as it calls it, and its latest thought. When the answer
 * is in, the same card folds to one line above it and opens on a click.
 *
 * Tokens come from the core's meter (pulse), which counts the whole process:
 * the turn's share is what the meter gained since the turn started. They land
 * a round at a time, so the figure climbs in steps and the count-up smooths it.
 */

// The meter at the moment each turn was first seen, and what it gained by the
// time the turn ended. Module-level so a re-render or a remount keeps them.
const startTokens = new Map<string, number>();
const turnTokens = new Map<string, number>();

function useClock(running: boolean) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!running) return;
    const t = window.setInterval(() => setNow(Date.now()), 250);
    return () => window.clearInterval(t);
  }, [running]);
  return now;
}

/** A number that runs up to where it is going instead of jumping. */
function useCountUp(target: number) {
  const [shown, setShown] = useState(target);
  const from = useRef(target);
  useEffect(() => {
    const start = performance.now(), a = from.current, d = target - a;
    if (d === 0) return;
    let raf = 0;
    const step = (t: number) => {
      const k = Math.min(1, (t - start) / 700);
      const v = Math.round(a + d * (1 - Math.pow(1 - k, 3)));
      from.current = v;
      setShown(v);
      if (k < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [target]);
  return shown;
}

const clock = (ms: number) => {
  const s = Math.max(0, Math.floor(ms / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
};

/** The arguments in a few words: the values, not the keys. */
function argsLine(args: Record<string, unknown>): string {
  const parts: string[] = [];
  for (const v of Object.values(args ?? {})) {
    if (v == null || v === "") continue;
    if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") parts.push(String(v));
    else if (Array.isArray(v)) parts.push(v.slice(0, 3).map(String).join(", "));
    if (parts.join(" · ").length > 90) break;
  }
  const s = parts.join(" · ").replace(/\s+/g, " ");
  return s.length > 90 ? s.slice(0, 89) + "…" : s;
}

const when = (v: unknown) => {
  const d = typeof v === "string" ? new Date(v) : null;
  if (!d || isNaN(+d)) return typeof v === "string" ? v : "";
  return d.toLocaleString(undefined, { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
};

/** What a call wrote down, in a line the person can check: the calendar entry,
 *  note, person or reminder it saved. Other tools return "". */
function savedLine(t: TraceItem): string {
  if (t.status !== "ok" || t.inner) return "";
  const a = (t.args ?? {}) as Record<string, unknown>;
  const s = (k: string) => (typeof a[k] === "string" ? (a[k] as string).trim() : "");
  switch (t.tool) {
    case "add_schedule": return `Calendar: ${s("title")} · ${when(a.start_at)}${s("location") ? ` · ${s("location")}` : ""}`;
    case "set_reminder": return `Reminder: ${s("title")} · ${/^\d\d:\d\d$/.test(s("remind_at")) ? `every day ${s("remind_at")}` : when(a.remind_at)}`;
    case "add_record": return `Noted: ${s("title") || s("body").slice(0, 80)}`;
    case "upsert_person": return `Person: ${s("name")}${s("relation") ? ` · ${s("relation")}` : ""}`;
    default: return "";
  }
}

export default function LiveTurn({ m, trace, pulse, session }: { m: ChatMessage; trace: TraceItem[]; pulse: Snap; session: string }) {
  const running = !!m.streaming;
  const now = useClock(running);
  // Open while it runs and folded once it is done, unless the person has
  // clicked it one way or the other: then it stays how they left it.
  const [pref, setPref] = useState<boolean | null>(null);
  const open = pref ?? running;

  const calls = trace.filter((t) => t.askId === m.id);
  const top = calls.filter((t) => !t.inner);
  const inner = calls.length - top.length;
  const saved = top.map((t) => ({ id: t.id, line: savedLine(t) })).filter((x) => x.line);
  const savedList = saved.length > 0 && (
    <ul className="lt-saved">
      {saved.map((x) => <li key={x.id}><BookmarkCheckIcon size={13} />{x.line}</li>)}
    </ul>
  );

  // Tokens: the meter's gain since this turn began.
  const loaded = pulse.now !== "";
  if (running && loaded && !startTokens.has(m.id)) startTokens.set(m.id, pulse.tokens);
  const base = startTokens.get(m.id);
  if (base !== undefined && loaded) {
    if (running || !turnTokens.has(m.id)) turnTokens.set(m.id, Math.max(0, pulse.tokens - base));
  }
  const tokens = useCountUp(turnTokens.get(m.id) ?? 0);

  const run = pulse.runs.find((r) => r.session === session);
  const thought = [...(m.progress ?? [])].reverse().find((p) => p.kind !== "tool")?.text || run?.think || run?.doing;
  const elapsed = m.startedAt ? (running ? now : m.finishedAt ?? now) - m.startedAt : 0;

  // Nothing was streamed here (an old turn loaded from history): nothing to show.
  if (!running && !m.startedAt) return null;
  if (!running && top.length === 0 && !turnTokens.get(m.id)) return null;

  const stats = (
    <>
      <span className="lt-n">{clock(elapsed)}</span>
      {tokens > 0 && <span className="lt-n"><b>{fmtK(tokens)}</b> tokens</span>}
      {calls.length > 0 && <span className="lt-n"><b>{calls.length}</b> {calls.length === 1 ? "tool call" : "tool calls"}</span>}
      {running && run && run.round > 0 && <span className="lt-n">round {run.round}</span>}
    </>
  );

  if (!open) {
    return (
      <>
        <button className={`lt-fold${running ? " on" : ""}`} onClick={() => setPref(true)}>
          {running && <i className="lt-pulse" />}
          <span>{running ? "Working" : "Worked"}</span>{stats}<ChevronDownIcon size={13} />
        </button>
        {savedList}
      </>
    );
  }

  return (
    <div className={`lt${running ? " on" : ""}`}>
      <button className="lt-head" onClick={() => setPref(false)}>
        <i className="lt-pulse" />
        <span className="lt-what">{running ? "Working" : "Worked"}</span>
        {stats}
        <ChevronDownIcon size={13} className="lt-up" />
      </button>
      {(top.length > 0 || (running && thought)) && (
        <ol className="lt-steps">
          {top.map((t) => (
            <li key={t.id} className={`lt-step ${t.status}`}>
              <span className="lt-mark">{t.status === "running" ? <i className="lt-spin" /> : t.status === "ok" ? <CheckIcon size={12} strokeWidth={3} /> : <XIcon size={12} strokeWidth={3} />}</span>
              <code>{t.tool}</code>
              <span className="lt-args">{argsLine(t.args)}</span>
            </li>
          ))}
          {inner > 0 && <li className="lt-step inner"><span className="lt-mark">+</span><span className="lt-args">{inner} more inside its own code</span></li>}
          {running && thought && <li className="lt-step thought"><span className="lt-mark">✦</span><span className="lt-shimmer">{thought}</span></li>}
        </ol>
      )}
      {savedList}
    </div>
  );
}
