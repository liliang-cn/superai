import React, { useEffect, useState } from "react";
import { Response } from "@/components/ai-elements/response";
import type { ToolApproval } from "../lib/useToolApprovals";
import type { Snap } from "../components/Reactor";
import type { ChatMessage } from "../lib/types";
import { Bee, Hive, elapsed, isToday, oneLine, short } from "./data";
import { withoutCallNotes } from "../lib/format";

/** A tick every second, for clocks that count up. */
function useNow(ms = 1000) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), ms);
    return () => window.clearInterval(t);
  }, [ms]);
  return now;
}

type CellState = "queen" | "working" | "done" | "resting" | "lost";

export function Cell({ label, state, size = 28, onClick }: { label: string; state: CellState; size?: number; onClick?: () => void }) {
  return (
    <button type="button" className={`cv-cell s-${state}`} style={{ width: size, height: size * 1.12, fontSize: size * (label.length > 2 ? 0.28 : 0.34) }} onClick={onClick} title={label}>
      <span>{label}</span>
    </button>
  );
}

/** What needs a person, on top of everything. One question at a time, said in
 *  full; the rest wait under it. When nothing does, a single line. */
export function NeedsYou({ pending, resolve, bees }: { pending: ToolApproval[]; resolve: (id: string, allow: boolean) => void; bees: Bee[] }) {
  const now = useNow();
  const a = pending[0];
  if (!a) {
    const out = bees.find((b) => b.running);
    return (
      <div className="cv-glass cv-clear" data-testid="nothing-needs-you">
        <i />
        <b>Nothing needs you</b>
        {out && <span>{out.name} is out</span>}
      </div>
    );
  }
  const left = a.expiresAt ? Math.max(0, Math.round((new Date(a.expiresAt).getTime() - now) / 1000)) : null;
  return (
    <div className="cv-glass cv-needs" key={a.id}>
      <div className="cv-needs-by">
        <Cell label={(a.by || "S").slice(0, 1).toUpperCase()} state="working" size={16} />
        {a.by ? `${a.by} asks you` : "SuperAI asks you"}
        {left !== null && <span className="cv-needs-left">{Math.floor(left / 60)}:{String(left % 60).padStart(2, "0")}</span>}
      </div>
      <div className="cv-needs-q">{question(a)}</div>
      {(a.command || Object.keys(a.args || {}).length > 0) && (
        <pre className="cv-needs-cmd">{a.command || JSON.stringify(a.args, null, 2)}</pre>
      )}
      <div className="cv-needs-acts">
        <button className="cv-pill" onClick={() => resolve(a.id, false)}>Deny</button>
        <button className="cv-pill ink" onClick={() => resolve(a.id, true)}>Allow</button>
      </div>
      {pending.length > 1 && <div className="cv-needs-more">{pending.length - 1} more waiting: {question(pending[1])}</div>}
    </div>
  );
}

function question(a: ToolApproval): string {
  switch (a.tool) {
    case "bash":
    case "shell":
    case "run_command":
      return "Run this command?";
    case "write_file":
    case "edit_file":
      return "Change this file?";
    case "delete_file":
      return "Delete this file?";
    default:
      return `Allow ${a.tool}?`;
  }
}

export function InFlight({ hive, open }: { hive: Hive; open: (worker: string) => void }) {
  const now = useNow();
  const running = hive.tasks.filter((t) => t.state === "running");
  if (running.length === 0) {
    const last = hive.tasks.find((t) => t.state !== "running");
    return <p className="cv-quiet clamp">{last ? `Nothing out right now. Last: ${oneLine(last.prompt)}` : "Nothing out right now."}</p>;
  }
  return (
    <div className="cv-rows">
      {running.slice(0, 6).map((t) => (
        <button key={t.id} className="cv-row" onClick={() => open(t.worker)}>
          <Cell label={short(t.worker)} state="working" size={24} />
          <span className="cv-row-main">
            <b>{oneLine(t.prompt)}</b>
            <small>{t.tool ? `Using ${t.tool}` : "Thinking"}</small>
          </span>
          <span className="cv-row-time">{elapsed(t.started_at, now)}</span>
        </button>
      ))}
      {running.length > 6 && <p className="cv-quiet">and {running.length - 6} more</p>}
    </div>
  );
}

export function Machines({ hive, open }: { hive: Hive; open: (name: string) => void }) {
  if (hive.loaded && !hive.role) return <p className="cv-quiet">This SuperAI is not in a hive.</p>;
  const busy = new Set(hive.tasks.filter((t) => t.state === "running").map((t) => t.worker));
  const recent = new Set(hive.tasks.slice(0, 30).filter((t) => t.state === "done").map((t) => t.worker));
  const center = hive.role === "queen" ? hive.name : "queen";
  return (
    <div className="cv-comb">
      <Cell label="Q" state="queen" onClick={() => open(center)} />
      {hive.members.filter((m) => m.name !== center).map((m) => (
        <Cell key={m.name} label={short(m.name)} onClick={() => open(m.name)}
          state={m.state !== "live" ? "lost" : busy.has(m.name) ? "working" : recent.has(m.name) ? "done" : "resting"} />
      ))}
    </div>
  );
}

/** What the whole hive did today, counted by what it changed. */
export function Today({ hive, pulse }: { hive: Hive; pulse: Snap }) {
  const orders = hive.tasks.filter((t) => isToday(t.started_at));
  const calls = pulse.calls + orders.reduce((n, t) => n + (t.tools || 0), 0);
  const failed = pulse.fails + orders.filter((t) => t.state === "failed").length;
  const fig = (n: number, label: string, bad = false) => (
    <div className="cv-fig"><b className={bad ? "bad" : ""}>{n}</b><small>{label}</small></div>
  );
  return (
    <div className="cv-figs">
      {fig(orders.length, "orders")}
      {fig(calls, "tool calls")}
      {fig(pulse.writes, "files written")}
      {fig(failed, "failed", failed > 0)}
    </div>
  );
}

export function Bees({ bees, open }: { bees: Bee[]; open: () => void }) {
  if (bees.length === 0) return <p className="cv-quiet">No bees yet. Ask for one below: “watch the hive and tell me when a worker drops”.</p>;
  return (
    <div className="cv-rows">
      {bees.map((b) => (
        <button key={b.id} className="cv-row" onClick={open}>
          <Cell label={b.glyph || b.name.slice(0, 1)} state={b.waitingFor || b.running ? "working" : b.paused ? "resting" : "done"} size={24} />
          <span className="cv-row-main"><b>{b.name}</b><small>{b.goal}</small></span>
          <span className={`cv-row-time${b.waitingFor ? " hot" : ""}`}>
            {b.waitingFor ? "Needs you" : b.running ? "Out now" : b.paused ? "Paused" : b.nextDue ? `Next ${new Date(b.nextDue).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })}` : "Resting"}
          </span>
        </button>
      ))}
    </div>
  );
}

/** The last order given here and what the queen said. */
export function Queen({ messages }: { messages: ChatMessage[] }) {
  const shown = messages.filter((m) => m.kind !== "context");
  const you = [...shown].reverse().find((m) => m.role === "user");
  const her = shown.length && shown[shown.length - 1].role === "assistant" ? shown[shown.length - 1] : undefined;
  if (!you) return <p className="cv-quiet">Orders you give below go to the queen; her answer shows here.</p>;
  return (
    <div className="cv-queen">
      <div className="cv-queen-you"><b>You</b>{withoutCallNotes(you.content)}</div>
      {her && (
        <div className="cv-queen-her">
          <b>Queen</b>
          {her.content ? <Response>{withoutCallNotes(her.content)}</Response> : <span className="cv-quiet">…</span>}
        </div>
      )}
      {!her && <div className="cv-queen-her"><b>Queen</b><span className="cv-quiet">Working on it…</span></div>}
    </div>
  );
}

export interface UpcomingItem {
  kind: "schedule" | "bee";
  id: string;
  what: string;
  when?: string;
  next?: string;
  enabled: boolean;
  running: boolean;
  bee?: string;
  waitingFor?: string;
}

function whenLabel(iso: string | undefined, now: number): string {
  if (!iso) return "On an event";
  const d = new Date(iso);
  const mins = Math.round((d.getTime() - now) / 60000);
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  if (mins < 1) return "Now";
  if (mins < 60) return `In ${mins} min`;
  const today = new Date(now).toDateString() === d.toDateString();
  const tomorrow = new Date(now + 86400000).toDateString() === d.toDateString();
  return today ? hm : tomorrow ? `Tomorrow ${hm}` : d.toLocaleDateString([], { month: "short", day: "numeric" }) + ` ${hm}`;
}

/** Everything the hive will do without being asked, scheduled prompts and
 *  bees alike, soonest first: one list, because they are one thing to whoever
 *  is waiting on them. */
export function Later({ items, open }: { items: UpcomingItem[]; open: (i: UpcomingItem) => void }) {
  const now = useNow(30000);
  if (items.length === 0) return <p className="cv-quiet">Nothing is scheduled. Ask below: “every weekday at 8, check the hive and tell me if anything is down”.</p>;
  return (
    <div className="cv-rows">
      {items.slice(0, 6).map((i) => (
        <button key={i.kind + i.id} className={`cv-row${i.enabled ? "" : " off"}`} onClick={() => open(i)}>
          <Cell label={i.kind === "bee" ? (i.bee || "B").slice(0, 1).toUpperCase() : "⏱"} state={i.running ? "working" : i.enabled ? "done" : "resting"} size={24} />
          <span className="cv-row-main">
            <b>{i.kind === "bee" ? i.bee : oneLine(i.what)}</b>
            <small>{i.kind === "bee" ? oneLine(i.what) : i.when}</small>
          </span>
          <span className={`cv-row-time${i.waitingFor ? " hot" : ""}`}>
            {i.waitingFor ? "Needs you" : i.running ? "Running" : !i.enabled ? "Paused" : whenLabel(i.next, now)}
          </span>
        </button>
      ))}
      {items.length > 6 && <p className="cv-quiet">and {items.length - 6} more</p>}
    </div>
  );
}

export interface CodingRun {
  id: string;
  agent: string;
  prompt: string;
  state: string;
  remote?: string;
  started: string;
  summary?: string;
}

/** The coding agents' sessions — Claude Code, Codex — here and on linked
 *  machines, newest first. */
export function Coding({ runs, open }: { runs: CodingRun[]; open: () => void }) {
  const now = useNow();
  if (runs.length === 0) return <p className="cv-quiet">No coding sessions yet. Write @claude or @claude.mac below to start one.</p>;
  return (
    <div className="cv-rows">
      {runs.slice(0, 5).map((r) => (
        <button key={r.id} className="cv-row" onClick={open}>
          <Cell label={r.remote ? r.remote.slice(0, 3) : r.agent.slice(0, 1).toUpperCase()} state={r.state === "running" ? "working" : r.state === "failed" ? "lost" : "done"} size={24} />
          <span className="cv-row-main">
            <b>{oneLine(r.prompt)}</b>
            <small>@{r.agent.includes(".") || !r.remote ? r.agent : `${r.agent}.${r.remote}`}{r.summary ? ` — ${oneLine(r.summary)}` : ""}</small>
          </span>
          <span className="cv-row-time">{r.state === "running" ? elapsed(r.started, now) : new Date(r.started).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })}</span>
        </button>
      ))}
    </div>
  );
}
