import { useSearchParams } from "react-router-dom";
import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PauseIcon, PlayIcon, ZapIcon, CopyIcon, XIcon, PlusIcon } from "lucide-react";
import {
  DeleteStandingAgent,
  MCP,
  PauseStandingAgent,
  ResumeStandingAgent,
  SaveStandingAgent,
  StandingAgents,
  StandingReports,
  StandingWakes,
  WakeStandingAgent,
} from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { parseTime } from "../lib/format";
import { useImeGuard } from "@/lib/ime";
import { toast } from "../lib/toasts";

// ---------------------------------------------------------------------------
// Shapes. Plain objects: what arrives over events is JSON, not the classes.
// ---------------------------------------------------------------------------

type Spec = Omit<app.AgentSpec, "convertValues" | "report" | "createdAt"> & {
  report: { push: boolean; telegram: boolean };
  createdAt?: string;
};
interface Wake {
  kind: string;
  reason?: string;
  started_at: string;
  ended_at?: string;
  tool_calls: number;
  cost_usd: number;
  error?: string;
}
type View = Spec & {
  paused: boolean;
  pausedReason?: string;
  notes?: string;
  running?: Wake;
  lastWake?: Wake;
  nextDue?: string;
  wakesToday: number;
  costTodayUsd: number;
  hookPath?: string;
  waitingFor?: string;
};
interface Mark {
  agent: string;
  kind: string;
  started: string;
  ended?: string;
  toolCalls: number;
  error?: string;
  notified: number;
}
interface Report {
  agent: string;
  name: string;
  kind: string;
  message: string;
  at: string;
}

/** Identity hues. Deliberately not the signal blue, and never purple: a bee
 *  saved as "violet" before that rule is drawn in amber. */
const HUES: Record<string, string> = {
  cyan: "#0e7c92",
  blue: "#2f7fa3",
  violet: "#c2410c",
  rose: "#b8265f",
  orange: "#b35a00",
  green: "#12805c",
};
const hue = (h?: string) => HUES[h ?? ""] ?? HUES.cyan;

/** The track covers the last day and the next three hours. */
const PAST_H = 24;
const AHEAD_H = 3;
const SPAN = (PAST_H + AHEAD_H) * 3600_000;

const blank = (): Spec =>
  ({
    id: "",
    name: "",
    glyph: "",
    hue: "cyan",
    goal: "",
    watch: [],
    attention: "",
    never: [],
    auto: [],
    ask: ["*bash*", "*shell*"],
    forbid: [],
    allConnectors: false,
    connectors: [],
    cron: "",
    everyMinutes: 60,
    scanEveryMinutes: 0,
    report: { push: true, telegram: false },
    maxWakesPerDay: 0,
    maxCostPerDayUsd: 0,
  }) as unknown as Spec;

const t = (s?: string) => parseTime(s)?.getTime() ?? 0;
const hhmm = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });

function until(ms: number) {
  const m = Math.round((ms - Date.now()) / 60000);
  if (m <= 0) return "now";
  if (m < 60) return `in ${m} min`;
  const h = Math.floor(m / 60);
  return m % 60 ? `in ${h} h ${m % 60} min` : `in ${h} h`;
}

// ---------------------------------------------------------------------------
// The page
// ---------------------------------------------------------------------------

/**
 * Standing agents as a watch bill: one row per agent on a shared 24-hour
 * track, so it reads at a glance who is on watch, how often each wakes, when
 * it spoke, and who is waiting for you. The track is the page; everything
 * else is quiet.
 */
/** `children` is what else runs on its own: the recurring routines, drawn by
 *  their own view and passed in by the app, which holds what they need. */
export default function StandingView({ children }: { children?: React.ReactNode }) {
  const [agents, setAgents] = useState<View[]>([]);
  const [wakes, setWakes] = useState<Mark[]>([]);
  const [reports, setReports] = useState<Report[]>([]);
  const [params] = useSearchParams();
  const agentId = params.get("agent") ?? "";
  const [open, setOpen] = useState(agentId);
  useEffect(() => { setOpen(agentId); }, [agentId]);
  const [editing, setEditing] = useState<Spec | null>(null);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());

  const load = useCallback(async () => {
    try {
      const [list, ws, rs] = await Promise.all([StandingAgents(), StandingWakes(PAST_H), StandingReports("")]);
      setAgents((list ?? []) as unknown as View[]);
      setWakes((ws ?? []) as unknown as Mark[]);
      setReports(((rs ?? []) as unknown as Report[]).filter((r) => r.message));
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }, []);

  useEffect(() => {
    load();
    const offU = EventsOn("agent:update", () => load());
    const offR = EventsOn("agent:report", (r: Report) => r.message && setReports((p) => [r, ...p].slice(0, 300)));
    const tick = window.setInterval(() => setNow(Date.now()), 15_000);
    return () => {
      offU();
      offR();
      window.clearInterval(tick);
    };
  }, [load]);

  const act = async (f: () => Promise<unknown>, done?: string) => {
    try {
      await f();
      if (done) toast.success(done);
      await load();
    } catch (e) {
      toast.error(String(e));
    }
  };

  const waiting = agents.filter((a) => a.waitingFor).length;
  const onWatch = agents.filter((a) => !a.paused).length;
  const from = now - PAST_H * 3600_000;

  return (
    <div className="view wt-view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Bees</div>
          <div className="view-desc">
            {agents.length === 0
              ? "Agents that keep something true, and tell you only what is worth knowing."
              : `${onWatch} on watch${waiting ? `, ${waiting} waiting for you` : ""}.`}
          </div>
        </div>
        <div className="vh-actions">
          <button className="btn" onClick={() => setEditing(blank())}>
            <PlusIcon size={14} /> New agent
          </button>
        </div>
      </div>
      {error && <div className="report-error wt-error">{error}</div>}

      {agents.length === 0 && !error ? (
        <div className="wt-empty">
          <div className="wt-empty-title">Nothing on watch yet</div>
          <p>
            Give an agent one thing to keep true — “every worker in the hive is live”, “main stays green”, “no empty files in
            the workspace”. It wakes on its own, looks, and tells you only when something needs you.
          </p>
          <button className="btn" onClick={() => setEditing(blank())}>
            <PlusIcon size={14} /> New agent
          </button>
        </div>
      ) : (
        <div className="wt-bill">
          <Axis from={from} />
          {agents.map((a) => (
            <React.Fragment key={a.id}>
              <Row
                agent={a}
                marks={wakes.filter((w) => w.agent === a.id)}
                said={reports.filter((r) => r.agent === a.id)}
                from={from}
                now={now}
                open={open === a.id}
                onOpen={() => setOpen(open === a.id ? "" : a.id)}
              />
              {open === a.id && (
                <Detail
                  agent={a}
                  said={reports.filter((r) => r.agent === a.id)}
                  onWake={() => act(() => WakeStandingAgent(a.id, ""))}
                  onPause={() => act(() => (a.paused ? ResumeStandingAgent(a.id) : PauseStandingAgent(a.id)))}
                  onEdit={() => setEditing({ ...a })}
                  onDelete={() => {
                    if (window.confirm(`Delete ${a.name}? Its notes and history go with it.`))
                      act(() => DeleteStandingAgent(a.id), `Deleted ${a.name}`).then(() => setOpen(""));
                  }}
                  onTell={(m) => act(() => WakeStandingAgent(a.id, m), `Told ${a.name}`)}
                />
              )}
            </React.Fragment>
          ))}
        </div>
      )}

      {children && (
        <section className="wt-routines">
          <h2>Routines</h2>
          {children}
        </section>
      )}

      {editing && (
        <Editor
          spec={editing}
          onClose={() => setEditing(null)}
          onSaved={(v) => {
            setEditing(null);
            setOpen(v.id);
            load();
          }}
        />
      )}
    </div>
  );
}

/** Position on the track, 0..100. */
const pos = (ms: number, from: number) => Math.max(0, Math.min(100, ((ms - from) / SPAN) * 100));

function Axis({ from }: { from: number }) {
  // A label every four hours, on the hour.
  const first = new Date(from);
  first.setMinutes(0, 0, 0);
  first.setHours(first.getHours() + (4 - (first.getHours() % 4)));
  const ticks: number[] = [];
  for (let ms = first.getTime(); ms < from + PAST_H * 3600_000 - 2 * 3600_000; ms += 4 * 3600_000) ticks.push(ms);
  const nowAt = pos(from + PAST_H * 3600_000, from);
  return (
    <div className="wt-row wt-axis" aria-hidden="true">
      <div className="wt-id" />
      <div className="wt-track">
        {ticks.map((ms) => (
          <span key={ms} className="wt-hour" style={{ left: `${pos(ms, from)}%` }}>
            {hhmm(ms)}
          </span>
        ))}
        <span className="wt-hour now" style={{ left: `${nowAt}%` }}>now</span>
      </div>
      <div className="wt-next">Next</div>
    </div>
  );
}

function Glyph({ agent, size = 34 }: { agent: Pick<View, "glyph" | "name" | "hue" | "paused" | "running" | "waitingFor">; size?: number }) {
  const g = agent.glyph || (agent.name ? agent.name.slice(0, 1).toUpperCase() : "◎");
  return (
    <span
      className={`wt-glyph${agent.running ? " live" : ""}${agent.paused ? " paused" : ""}${agent.waitingFor ? " needs" : ""}`}
      style={{ "--h": hue(agent.hue), width: size, height: size, fontSize: size * 0.48 } as React.CSSProperties}
    >
      {g}
    </span>
  );
}

const KIND_LABEL: Record<string, string> = {
  schedule: "on schedule",
  self: "it asked to",
  event: "an event",
  scan: "looked around",
  host: "you woke it",
};

function Row({
  agent,
  marks,
  said,
  from,
  now,
  open,
  onOpen,
}: {
  agent: View;
  marks: Mark[];
  said: Report[];
  from: number;
  now: number;
  open: boolean;
  onOpen: () => void;
}) {
  const nowAt = pos(now, from);
  const next = t(agent.nextDue);
  const runStart = agent.running ? t(agent.running.started_at) : 0;
  return (
    <button
      className={`wt-row wt-agent${open ? " open" : ""}${agent.waitingFor ? " needs" : ""}${agent.paused ? " paused" : ""}`}
      style={{ "--h": hue(agent.hue) } as React.CSSProperties}
      onClick={onOpen}
      aria-expanded={open}
    >
      <div className="wt-id">
        <Glyph agent={agent} />
        <div className="wt-names">
          <span className="wt-name">{agent.name}</span>
          <span className="wt-goal">{agent.goal}</span>
        </div>
      </div>
      <div className="wt-track">
        <span className="wt-base" />
        <span className="wt-future" style={{ left: `${nowAt}%` }} />
        <span className="wt-nowline" style={{ left: `${nowAt}%` }} />
        {marks.map((m, i) => (
          <span
            key={i}
            className={`wt-mark k-${m.kind}${m.error ? " bad" : ""}`}
            style={{ left: `${pos(t(m.started), from)}%` }}
            title={`${hhmm(t(m.started))}, ${KIND_LABEL[m.kind] ?? m.kind}, ${m.toolCalls} tool calls${m.error ? `, failed: ${m.error}` : ""}`}
          />
        ))}
        {said
          .filter((r) => t(r.at) >= from)
          .map((r, i) => (
            <span key={`r${i}`} className={`wt-said${r.kind === "error" ? " bad" : ""}`} style={{ left: `${pos(t(r.at), from)}%` }} title={`${hhmm(t(r.at))}: ${r.message}`} />
          ))}
        {agent.running && (
          <span className="wt-awake" style={{ left: `${pos(runStart, from)}%`, width: `${Math.max(0.6, nowAt - pos(runStart, from))}%` }} />
        )}
        {next > now && next < from + SPAN && <span className="wt-due" style={{ left: `${pos(next, from)}%` }} title={`next ${hhmm(next)}`} />}
      </div>
      <div className="wt-next">
        {agent.waitingFor ? (
          <span className="wt-state needs">Waiting for you</span>
        ) : agent.running ? (
          <span className="wt-state live">Awake</span>
        ) : agent.paused ? (
          <span className="wt-state">Paused</span>
        ) : next ? (
          <span className="wt-state">{until(next)}</span>
        ) : (
          <span className="wt-state quiet">On an event</span>
        )}
      </div>
    </button>
  );
}

function Detail({
  agent,
  said,
  onWake,
  onPause,
  onEdit,
  onDelete,
  onTell,
}: {
  agent: View;
  said: Report[];
  onWake: () => void;
  onPause: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onTell: (m: string) => void;
}) {
  const [msg, setMsg] = useState("");
  const ime = useImeGuard();
  const hook = agent.hookPath ? window.location.origin + agent.hookPath : "";
  const when: string[] = [];
  if (agent.everyMinutes) when.push(`every ${agent.everyMinutes} min`);
  if (agent.cron) when.push(`on the schedule ${agent.cron}`);
  if (agent.scanEveryMinutes) when.push(`looks around every ${agent.scanEveryMinutes} min`);
  when.push("when it asks to, and when an event arrives");

  return (
    <div className="wt-detail" style={{ "--h": hue(agent.hue) } as React.CSSProperties}>
      <div className="wt-said-col">
        <div className="wt-tell">
          <input
            className="input"
            placeholder={`Tell ${agent.name} something — it wakes with it`}
            value={msg}
            onChange={(e) => setMsg(e.target.value)}
            {...ime.handlers}
            onKeyDown={(e) => {
              if (e.key === "Enter" && msg.trim() && !ime.composing(e)) {
                onTell(msg);
                setMsg("");
              }
            }}
          />
        </div>
        {said.length === 0 ? (
          <p className="wt-quiet">It has said nothing yet. It speaks only when something is worth your attention.</p>
        ) : (
          <ol className="wt-log">
            {said.map((r, i) => (
              <li key={i} className={r.kind === "error" ? "bad" : ""}>
                <time>{new Date(r.at).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false })}</time>
                <span>{r.message}</span>
              </li>
            ))}
          </ol>
        )}
      </div>
      <div className="wt-order-col">
        <div className="wt-actions">
          <button className="btn sm" onClick={onWake} disabled={!!agent.running}>
            <ZapIcon size={13} /> Wake now
          </button>
          <button className="btn ghost sm" onClick={onPause}>
            {agent.paused ? <PlayIcon size={13} /> : <PauseIcon size={13} />} {agent.paused ? "Resume" : "Pause"}
          </button>
          <button className="btn ghost sm" onClick={onEdit}>
            Edit
          </button>
          <button className="btn ghost sm wt-del" onClick={onDelete}>
            Delete
          </button>
        </div>
        <dl className="wt-order">
          {(agent.watch ?? []).length > 0 && (
            <>
              <dt>Watches</dt>
              <dd>{(agent.watch ?? []).join(", ")}</dd>
            </>
          )}
          {agent.attention && (
            <>
              <dt>Tells you</dt>
              <dd>{agent.attention}</dd>
            </>
          )}
          {(agent.never ?? []).length > 0 && (
            <>
              <dt>Never</dt>
              <dd>{(agent.never ?? []).join(", ")}</dd>
            </>
          )}
          <dt>Wakes</dt>
          <dd>{when.join("; ")}</dd>
        </dl>
        <div className="wt-tiers">
          <Tier name="On its own" items={agent.auto} cls="auto" />
          <Tier name="Asks first" items={agent.ask} cls="ask" />
          <Tier name="Never" items={agent.forbid} cls="never" />
        </div>
        <div className="wt-conn">
          Connectors: {agent.allConnectors ? "all of them" : (agent.connectors ?? []).join(", ") || "none"}
        </div>
        {agent.notes && (
          <details className="wt-notes">
            <summary>Its notes for next time</summary>
            <p>{agent.notes}</p>
          </details>
        )}
        {hook && (
          <div className="wt-hook">
            <code title="POST any text here and it wakes with it">{hook}</code>
            <button
              className="btn ghost sm"
              aria-label="Copy webhook"
              onClick={() => navigator.clipboard.writeText(hook).then(() => toast.success("Webhook copied"))}
            >
              <CopyIcon size={12} />
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function Tier({ name, items, cls }: { name: string; items?: string[]; cls: string }) {
  return (
    <div className={`wt-tier ${cls}`}>
      <span className="wt-tier-name">{name}</span>
      <div className="wt-chips">
        {(items ?? []).length ? (items ?? []).map((x) => <code key={x}>{x}</code>) : <span className="wt-none">nothing</span>}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// The editor: a standing order, written out.
// ---------------------------------------------------------------------------

const split = (s: string) =>
  s
    .split(/[\n,，]/)
    .map((x) => x.trim())
    .filter(Boolean);

function Chips({ value, onChange, placeholder, cls }: { value: string[]; onChange: (v: string[]) => void; placeholder: string; cls: string }) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const more = split(draft).filter((x) => !value.includes(x));
    if (more.length) onChange([...value, ...more]);
    setDraft("");
  };
  return (
    <div className={`ed-chips ${cls}`}>
      {value.map((x) => (
        <code key={x}>
          {x}
          <button aria-label={`Remove ${x}`} onClick={() => onChange(value.filter((y) => y !== x))}>
            <XIcon size={10} />
          </button>
        </code>
      ))}
      <input
        value={draft}
        placeholder={value.length ? "" : placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={add}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          } else if (e.key === "Backspace" && !draft && value.length) onChange(value.slice(0, -1));
        }}
      />
    </div>
  );
}

function Editor({ spec, onClose, onSaved }: { spec: Spec; onClose: () => void; onSaved: (v: View) => void }) {
  const [s, setS] = useState<Spec>(spec);
  const [servers, setServers] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const first = useRef<HTMLInputElement>(null);
  useEffect(() => {
    first.current?.focus();
    MCP()
      .then((l) => setServers((l ?? []).map((x) => x.name)))
      .catch(() => {});
    const esc = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", esc);
    return () => window.removeEventListener("keydown", esc);
  }, [onClose]);
  const set = <K extends keyof Spec>(k: K, v: Spec[K]) => setS((p) => ({ ...p, [k]: v }));

  const save = async () => {
    setSaving(true);
    try {
      const { createdAt, ...rest } = s;
      const v = await SaveStandingAgent(app.AgentSpec.createFrom(createdAt ? s : rest));
      toast.success(s.id ? `Saved ${s.name}` : `${s.name} is on watch`);
      onSaved(v as unknown as View);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const preview = useMemo(() => ({ glyph: s.glyph, name: s.name, hue: s.hue, paused: false }), [s.glyph, s.name, s.hue]);

  return (
    <div className="ed-scrim" onClick={onClose}>
      <aside className="ed-drawer" onClick={(e) => e.stopPropagation()} aria-label={s.id ? `Edit ${s.name}` : "New agent"}>
        <header className="ed-head">
          <Glyph agent={preview} size={44} />
          <div className="ed-ident">
            <input ref={first} className="ed-name" placeholder="Name it" value={s.name} onChange={(e) => set("name", e.target.value)} />
            <div className="ed-look">
              <input className="ed-glyph" placeholder="◎" maxLength={2} value={s.glyph ?? ""} onChange={(e) => set("glyph", e.target.value)} aria-label="Glyph" />
              {Object.keys(HUES).map((h) => (
                <button
                  key={h}
                  className={`ed-hue${s.hue === h ? " on" : ""}`}
                  style={{ "--h": HUES[h] } as React.CSSProperties}
                  onClick={() => set("hue", h)}
                  aria-label={h}
                />
              ))}
            </div>
          </div>
          <button className="btn ghost sm" onClick={onClose} aria-label="Close">
            <XIcon size={14} />
          </button>
        </header>

        <div className="ed-body">
          <section className="ed-order">
            <label>
              <span>Keeps this true</span>
              <textarea rows={2} value={s.goal} onChange={(e) => set("goal", e.target.value)} placeholder="Every worker in the hive is live" />
            </label>
            <label>
              <span>Watches</span>
              <input value={(s.watch ?? []).join(", ")} onChange={(e) => set("watch", split(e.target.value))} placeholder="hive members, running tasks" />
            </label>
            <label>
              <span>Tells you when</span>
              <input value={s.attention ?? ""} onChange={(e) => set("attention", e.target.value)} placeholder="a worker goes down, or a task is stuck" />
            </label>
            <label>
              <span>Never</span>
              <input value={(s.never ?? []).join(", ")} onChange={(e) => set("never", split(e.target.value))} placeholder="dispatch work, retire a worker" />
            </label>
          </section>

          <section>
            <h3>Tools</h3>
            <p className="ed-hint">Tool names; * matches anything, mcp:github means every tool of that connector.</p>
            <div className="ed-tiers">
              <div>
                <span className="ed-tier auto">On its own</span>
                <Chips cls="auto" value={s.auto ?? []} onChange={(v) => set("auto", v)} placeholder="fs_read*" />
              </div>
              <div>
                <span className="ed-tier ask">Asks first</span>
                <Chips cls="ask" value={s.ask ?? []} onChange={(v) => set("ask", v)} placeholder="*bash*" />
              </div>
              <div>
                <span className="ed-tier never">Never</span>
                <Chips cls="never" value={s.forbid ?? []} onChange={(v) => set("forbid", v)} placeholder="*delete*" />
              </div>
            </div>
            <div className="ed-conn">
              <span>Connectors</span>
              <label className="ed-check">
                <input type="checkbox" checked={s.allConnectors} onChange={(e) => set("allConnectors", e.target.checked)} /> All
              </label>
              {!s.allConnectors &&
                servers.map((n) => (
                  <label key={n} className="ed-check">
                    <input
                      type="checkbox"
                      checked={(s.connectors ?? []).includes(n)}
                      onChange={(e) => set("connectors", e.target.checked ? [...(s.connectors ?? []), n] : (s.connectors ?? []).filter((x) => x !== n))}
                    />{" "}
                    {n}
                  </label>
                ))}
              {!s.allConnectors && servers.length === 0 && <span className="ed-hint">No MCP servers yet.</span>}
            </div>
          </section>

          <section>
            <h3>Wakes</h3>
            <div className="ed-grid">
              <span>Every</span>
              <div>
                <input type="number" min={0} value={s.everyMinutes || ""} onChange={(e) => set("everyMinutes", Number(e.target.value) || 0)} placeholder="—" /> min
                <em>or on</em>
                <input className="ed-cron" value={s.cron ?? ""} onChange={(e) => set("cron", e.target.value)} placeholder="0 9 * * 1-5" />
              </div>
              <span>Glances every</span>
              <div>
                <input type="number" min={0} value={s.scanEveryMinutes || ""} onChange={(e) => set("scanEveryMinutes", Number(e.target.value) || 0)} placeholder="—" /> min
                <em>reading only, between wakes</em>
              </div>
              <span>At most</span>
              <div>
                <input type="number" min={0} value={s.maxWakesPerDay || ""} onChange={(e) => set("maxWakesPerDay", Number(e.target.value) || 0)} placeholder="24" /> wakes
                <em>and</em>
                $<input type="number" min={0} step="0.1" value={s.maxCostPerDayUsd || ""} onChange={(e) => set("maxCostPerDayUsd", Number(e.target.value) || 0)} placeholder="—" /> a day
              </div>
            </div>
            <p className="ed-hint">Also wakes when it asks to, when you tell it something, and when its webhook is called.</p>
          </section>

          <section>
            <h3>Reports</h3>
            <div className="ed-reports">
              <label className="ed-check">
                <input type="checkbox" checked={s.report.push} onChange={(e) => set("report", { ...s.report, push: e.target.checked })} /> Push notification
              </label>
              <label className="ed-check">
                <input type="checkbox" checked={s.report.telegram} onChange={(e) => set("report", { ...s.report, telegram: e.target.checked })} /> Telegram
              </label>
            </div>
          </section>
        </div>

        <footer className="ed-foot">
          <button className="btn ghost" onClick={onClose}>
            Cancel
          </button>
          <button className="btn" onClick={save} disabled={saving || !s.name.trim() || !s.goal.trim()}>
            {saving ? "Saving…" : s.id ? "Save" : "Put on watch"}
          </button>
        </footer>
      </aside>
    </div>
  );
}
