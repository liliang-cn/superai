import React, { useEffect, useState } from "react";
import { Response } from "@/components/ai-elements/response";
import type { ToolApproval } from "../lib/useToolApprovals";
import type { Snap } from "../components/Reactor";
import type { ChatMessage } from "../lib/types";
import { Bee, Hive, elapsed, isToday, oneLine, short } from "./data";

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
      <div className="cv-queen-you"><b>You</b>{you.content}</div>
      {her && (
        <div className="cv-queen-her">
          <b>Queen</b>
          {her.content ? <Response>{her.content}</Response> : <span className="cv-quiet">…</span>}
        </div>
      )}
      {!her && <div className="cv-queen-her"><b>Queen</b><span className="cv-quiet">Working on it…</span></div>}
    </div>
  );
}
