import React, { useCallback, useEffect, useMemo, useState } from "react";
import { PauseIcon, PlayIcon, ZapIcon, PencilIcon, Trash2Icon, CopyIcon, XIcon } from "lucide-react";
import {
  DeleteStandingAgent,
  MCP,
  PauseStandingAgent,
  ResumeStandingAgent,
  SaveStandingAgent,
  StandingAgents,
  StandingReports,
  WakeStandingAgent,
} from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { fromNow, parseTime } from "../lib/format";
import { useImeGuard } from "@/lib/ime";
import { toast } from "../lib/toasts";

type Spec = Omit<app.AgentSpec, "convertValues" | "report"> & { report: { push: boolean; telegram: boolean } };
type View = Spec & {
  paused: boolean;
  pausedReason?: string;
  notes?: string;
  running?: Wake;
  lastWake?: Wake;
  nextDue?: string;
  nextDueKind?: string;
  wakesToday: number;
  costTodayUsd: number;
  hookPath?: string;
  waitingFor?: string;
};
interface Wake {
  id: string;
  kind: string;
  reason?: string;
  started_at: string;
  ended_at?: string;
  cost_usd: number;
  tool_calls: number;
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

/** The six finishes an agent can wear, as hues. */
const HUES: Record<string, number> = { cyan: 190, blue: 220, violet: 265, rose: 335, orange: 25, green: 145 };
const hueOf = (h?: string) => HUES[h ?? ""] ?? HUES.cyan;

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
    everyMinutes: 0,
    scanEveryMinutes: 0,
    report: { push: true, telegram: false },
    maxWakesPerDay: 0,
    maxCostPerDayUsd: 0,
  }) as unknown as Spec;

/**
 * Standing agents: agents that are never finished. Each is told what should
 * stay true; it wakes on its schedule, on an event, or when it asked to, does
 * one turn, keeps notes, reports what is worth knowing, and sleeps.
 */
export default function StandingView() {
  const [agents, setAgents] = useState<View[]>([]);
  const [reports, setReports] = useState<Report[]>([]);
  const [open, setOpen] = useState<string>("");
  const [editing, setEditing] = useState<Spec | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const [list, rs] = await Promise.all([StandingAgents(), StandingReports("")]);
      setAgents((list ?? []) as unknown as View[]);
      setReports(((rs ?? []) as unknown as Report[]).filter((r) => r.message));
      setError("");
    } catch (e) {
      setError(String(e));
    }
  }, []);

  useEffect(() => {
    load();
    const offU = EventsOn("agent:update", () => load());
    const offR = EventsOn("agent:report", (r: Report) => r.message && setReports((prev) => [r, ...prev].slice(0, 300)));
    // Running wakes have a clock; keep "awake for 12s" honest.
    const tick = window.setInterval(() => setAgents((a) => [...a]), 5000);
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

  const selected = agents.find((a) => a.id === open) ?? null;

  return (
    <div className="view standing-view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Agents</div>
          <div className="view-desc">
            Agents that keep something true: they wake on a schedule, on an event, or when they asked to, and tell you
            what is worth knowing.
          </div>
        </div>
        <div className="vh-actions">
          <button className="btn sm" onClick={() => setEditing(blank())}>
            New agent
          </button>
        </div>
      </div>
      {error && <div className="report-error" style={{ marginBottom: 12 }}>{error}</div>}

      {agents.length === 0 && !error ? (
        <div className="inline-empty">
          <div className="ie-hint">
            No agents yet. Give one something to keep true — “main stays green”, “tell me when a worker is down”.
          </div>
        </div>
      ) : (
        <div className="sa-grid">
          {agents.map((a) => (
            <AgentCard
              key={a.id}
              agent={a}
              last={reports.find((r) => r.agent === a.id)}
              open={a.id === open}
              onOpen={() => setOpen(a.id === open ? "" : a.id)}
              onWake={() => act(() => WakeStandingAgent(a.id, ""))}
              onPause={() => act(() => (a.paused ? ResumeStandingAgent(a.id) : PauseStandingAgent(a.id)))}
            />
          ))}
        </div>
      )}

      {selected && (
        <AgentDetail
          agent={selected}
          reports={reports.filter((r) => r.agent === selected.id)}
          onClose={() => setOpen("")}
          onEdit={() => setEditing({ ...selected })}
          onDelete={() => {
            if (window.confirm(`Delete ${selected.name}? Its notes go with it.`))
              act(() => DeleteStandingAgent(selected.id), `${selected.name} deleted`).then(() => setOpen(""));
          }}
          onTell={(msg) => act(() => WakeStandingAgent(selected.id, msg), `Sent to ${selected.name}`)}
        />
      )}

      {editing && (
        <AgentEditor
          spec={editing}
          onCancel={() => setEditing(null)}
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

function Disc({ agent, size = 44 }: { agent: Pick<Spec, "glyph" | "hue" | "name"> & { running?: unknown; paused?: boolean }; size?: number }) {
  const h = hueOf(agent.hue);
  const glyph = agent.glyph || (agent.name || "?").slice(0, 1).toUpperCase();
  return (
    <span
      className={`sa-disc${agent.running ? " live" : ""}${agent.paused ? " paused" : ""}`}
      style={{ "--hue": h, width: size, height: size, fontSize: size * 0.46 } as React.CSSProperties}
    >
      {glyph}
    </span>
  );
}

function status(a: View): string {
  if (a.waitingFor) return `waiting for you · ${a.waitingFor}`;
  if (a.running) return `awake · ${secsSince(a.running.started_at)}`;
  if (a.paused) return a.pausedReason ? `paused · ${a.pausedReason}` : "paused";
  if (a.nextDue) return `next ${fromNow(parseTime(a.nextDue) ?? new Date())}`;
  if (a.cron) return `on ${a.cron}`;
  return "waits for an event";
}

function secsSince(t: string) {
  const s = Math.max(0, Math.round((Date.now() - (parseTime(t)?.getTime() ?? Date.now())) / 1000));
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

function AgentCard({
  agent,
  last,
  open,
  onOpen,
  onWake,
  onPause,
}: {
  agent: View;
  last?: Report;
  open: boolean;
  onOpen: () => void;
  onWake: () => void;
  onPause: () => void;
}) {
  return (
    <div
      className={`sa-card${open ? " open" : ""}${agent.running ? " live" : ""}${agent.waitingFor ? " needs" : ""}`}
      style={{ "--hue": hueOf(agent.hue) } as React.CSSProperties}
      onClick={onOpen}
    >
      <div className="sa-card-top">
        <Disc agent={agent} />
        <div className="sa-card-id">
          <div className="sa-name">{agent.name}</div>
          <div className="sa-status">{status(agent)}</div>
        </div>
        <div className="sa-card-actions" onClick={(e) => e.stopPropagation()}>
          <button className="btn ghost sm" title="Wake it now" onClick={onWake} disabled={!!agent.running}>
            <ZapIcon size={13} />
          </button>
          <button className="btn ghost sm" title={agent.paused ? "Resume" : "Pause"} onClick={onPause}>
            {agent.paused ? <PlayIcon size={13} /> : <PauseIcon size={13} />}
          </button>
        </div>
      </div>
      <div className="sa-goal">{agent.goal}</div>
      {last && (
        <div className={`sa-last ${last.kind}`}>
          <span>{last.message}</span>
          <span className="sa-when">{fromNow(parseTime(last.at) ?? new Date())}</span>
        </div>
      )}
      <div className="sa-meta">
        {agent.wakesToday} wakes today
        {agent.costTodayUsd > 0 && ` · $${agent.costTodayUsd.toFixed(3)}`}
      </div>
    </div>
  );
}

function AgentDetail({
  agent,
  reports,
  onClose,
  onEdit,
  onDelete,
  onTell,
}: {
  agent: View;
  reports: Report[];
  onClose: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onTell: (msg: string) => void;
}) {
  const [msg, setMsg] = useState("");
  const ime = useImeGuard();
  const hook = agent.hookPath ? window.location.origin + agent.hookPath : "";
  return (
    <div className="sa-detail" style={{ "--hue": hueOf(agent.hue) } as React.CSSProperties}>
      <div className="sa-detail-head">
        <Disc agent={agent} size={56} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="sa-name big">{agent.name}</div>
          <div className="sa-status">{status(agent)}</div>
        </div>
        <button className="btn ghost sm" onClick={onEdit}>
          <PencilIcon size={13} /> Edit
        </button>
        <button className="btn ghost sm" onClick={onDelete} title="Delete">
          <Trash2Icon size={13} />
        </button>
        <button className="btn ghost sm" onClick={onClose} title="Close">
          <XIcon size={13} />
        </button>
      </div>

      <div className="sa-cols">
        <div className="sa-col">
          <div className="sa-label">Reports</div>
          {reports.length === 0 ? (
            <div className="card-desc">Nothing yet. It reports only what is worth your attention.</div>
          ) : (
            <div className="sa-feed">
              {reports.map((r, i) => (
                <div key={i} className={`sa-report ${r.kind}`}>
                  <div className="sa-when">{new Date(r.at).toLocaleString()}</div>
                  <div className="sa-report-text">{r.message}</div>
                </div>
              ))}
            </div>
          )}
          <div className="sa-tell">
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
        </div>
        <div className="sa-col">
          <div className="sa-label">Its notes</div>
          <div className="sa-notes">{agent.notes || "No notes yet — it writes down what the next wake must know."}</div>
          {agent.lastWake && (
            <>
              <div className="sa-label">Last wake</div>
              <div className="sa-kv">
                {agent.lastWake.kind} · {agent.lastWake.tool_calls} tool calls
                {agent.lastWake.cost_usd > 0 && ` · $${agent.lastWake.cost_usd.toFixed(3)}`}
                {agent.lastWake.error && <div className="sa-err">{agent.lastWake.error}</div>}
              </div>
            </>
          )}
          <div className="sa-label">Permissions</div>
          <div className="sa-perm">
            <Tier name="On its own" items={agent.auto} cls="auto" />
            <Tier name="Asks first" items={agent.ask} cls="ask" />
            <Tier name="Never" items={agent.forbid} cls="never" />
            <div className="sa-kv">
              Connectors: {agent.allConnectors ? "all" : (agent.connectors ?? []).join(", ") || "none"}
            </div>
          </div>
          {hook && (
            <>
              <div className="sa-label">Webhook</div>
              <div className="sa-hook">
                <code>{hook}</code>
                <button
                  className="btn ghost sm"
                  onClick={() => navigator.clipboard.writeText(hook).then(() => toast.success("Webhook copied"))}
                >
                  <CopyIcon size={12} />
                </button>
              </div>
              <div className="card-desc">POST any text here and it wakes with it.</div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function Tier({ name, items, cls }: { name: string; items?: string[]; cls: string }) {
  return (
    <div className="sa-tier">
      <span className={`sa-tier-name ${cls}`}>{name}</span>
      {(items ?? []).length ? (items ?? []).map((t) => <span key={t} className="chip">{t}</span>) : <span className="sa-none">—</span>}
    </div>
  );
}

const lines = (s: string) =>
  s
    .split(/[\n,，]/)
    .map((x) => x.trim())
    .filter(Boolean);

function AgentEditor({ spec, onCancel, onSaved }: { spec: Spec; onCancel: () => void; onSaved: (v: View) => void }) {
  const [s, setS] = useState<Spec>(spec);
  const [servers, setServers] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    MCP()
      .then((l) => setServers((l ?? []).map((x) => x.name)))
      .catch(() => {});
  }, []);
  const set = <K extends keyof Spec>(k: K, v: Spec[K]) => setS((p) => ({ ...p, [k]: v }));
  const list = (k: "watch" | "never" | "auto" | "ask" | "forbid") => ((s[k] as string[]) ?? []).join(", ");

  const save = async () => {
    setSaving(true);
    try {
      // A new agent has no creation time yet; an empty string is not a time
      // the server can read.
      const { createdAt, ...rest } = s as Spec & { createdAt?: string };
      const v = await SaveStandingAgent(app.AgentSpec.createFrom(createdAt ? s : rest));
      toast.success(`${s.name} saved`);
      onSaved(v as unknown as View);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const preview = useMemo(() => ({ glyph: s.glyph, hue: s.hue, name: s.name }), [s.glyph, s.hue, s.name]);

  return (
    <div className="sa-modal" onClick={onCancel}>
      <div className="sa-editor" onClick={(e) => e.stopPropagation()}>
        <div className="sa-editor-head">
          <Disc agent={preview} size={48} />
          <input className="input sa-title" placeholder="Name" value={s.name} onChange={(e) => set("name", e.target.value)} />
          <input
            className="input sa-glyph"
            placeholder="🙂"
            maxLength={2}
            value={s.glyph ?? ""}
            onChange={(e) => set("glyph", e.target.value)}
            title="One character or emoji on its disc"
          />
          <div className="sa-hues">
            {Object.keys(HUES).map((h) => (
              <button
                key={h}
                className={`sa-hue${s.hue === h ? " on" : ""}`}
                style={{ "--hue": HUES[h] } as React.CSSProperties}
                onClick={() => set("hue", h)}
                aria-label={h}
              />
            ))}
          </div>
        </div>

        <div className="sa-form">
          <Field label="What should stay true">
            <textarea className="input" rows={2} value={s.goal} onChange={(e) => set("goal", e.target.value)} placeholder="main stays green; every worker in the hive is live" />
          </Field>
          <Field label="What to watch" hint="comma separated">
            <input className="input" value={list("watch")} onChange={(e) => set("watch", lines(e.target.value))} placeholder="CI, the hive's workers" />
          </Field>
          <Field label="Worth telling you about">
            <input className="input" value={s.attention ?? ""} onChange={(e) => set("attention", e.target.value)} placeholder="only when something is broken or needs a decision" />
          </Field>
          <Field label="Must never" hint="in words, comma separated">
            <input className="input" value={list("never")} onChange={(e) => set("never", lines(e.target.value))} placeholder="push to main, spend money" />
          </Field>

          <div className="sa-section">Permissions — tool names, * as a wildcard, mcp:server for a whole connector</div>
          <Field label="On its own">
            <input className="input" value={list("auto")} onChange={(e) => set("auto", lines(e.target.value))} placeholder="fs_read*, web_search" />
          </Field>
          <Field label="Asks first">
            <input className="input" value={list("ask")} onChange={(e) => set("ask", lines(e.target.value))} placeholder="*bash*, mcp:github" />
          </Field>
          <Field label="Never">
            <input className="input" value={list("forbid")} onChange={(e) => set("forbid", lines(e.target.value))} placeholder="*delete*, *payment*" />
          </Field>
          <Field label="Connectors">
            <div className="sa-checks">
              <label>
                <input type="checkbox" checked={s.allConnectors} onChange={(e) => set("allConnectors", e.target.checked)} /> all
              </label>
              {!s.allConnectors &&
                servers.map((n) => (
                  <label key={n}>
                    <input
                      type="checkbox"
                      checked={(s.connectors ?? []).includes(n)}
                      onChange={(e) =>
                        set("connectors", e.target.checked ? [...(s.connectors ?? []), n] : (s.connectors ?? []).filter((x) => x !== n))
                      }
                    />{" "}
                    {n}
                  </label>
                ))}
              {!s.allConnectors && servers.length === 0 && <span className="sa-none">no MCP servers configured</span>}
            </div>
          </Field>

          <div className="sa-section">When it wakes — also whenever it asks to, and on every event</div>
          <div className="sa-row">
            <Field label="Schedule (cron)">
              <input className="input" value={s.cron ?? ""} onChange={(e) => set("cron", e.target.value)} placeholder="0 9 * * 1-5" />
            </Field>
            <Field label="Every (minutes)">
              <input className="input" type="number" min={0} value={s.everyMinutes || ""} onChange={(e) => set("everyMinutes", Number(e.target.value) || 0)} />
            </Field>
            <Field label="Look around every (minutes)" hint="read-only">
              <input className="input" type="number" min={0} value={s.scanEveryMinutes || ""} onChange={(e) => set("scanEveryMinutes", Number(e.target.value) || 0)} />
            </Field>
          </div>

          <div className="sa-section">Reports — always here, and</div>
          <div className="sa-checks">
            <label>
              <input type="checkbox" checked={s.report.push} onChange={(e) => set("report", { ...s.report, push: e.target.checked })} /> push (notification webhook)
            </label>
            <label>
              <input type="checkbox" checked={s.report.telegram} onChange={(e) => set("report", { ...s.report, telegram: e.target.checked })} /> Telegram
            </label>
          </div>
          <div className="sa-row">
            <Field label="Wakes per day at most" hint="0: 24">
              <input className="input" type="number" min={0} value={s.maxWakesPerDay || ""} onChange={(e) => set("maxWakesPerDay", Number(e.target.value) || 0)} />
            </Field>
            <Field label="Spend per day at most (USD)" hint="0: no ceiling">
              <input className="input" type="number" min={0} step="0.1" value={s.maxCostPerDayUsd || ""} onChange={(e) => set("maxCostPerDayUsd", Number(e.target.value) || 0)} />
            </Field>
          </div>
        </div>

        <div className="sa-editor-foot">
          <button className="btn ghost" onClick={onCancel}>
            Cancel
          </button>
          <button className="btn" onClick={save} disabled={saving || !s.name.trim() || !s.goal.trim()}>
            {saving ? "Saving…" : s.id ? "Save" : "Create agent"}
          </button>
        </div>
      </div>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="sa-field">
      <span className="sa-field-label">
        {label}
        {hint && <span className="sa-hint"> {hint}</span>}
      </span>
      {children}
    </label>
  );
}
