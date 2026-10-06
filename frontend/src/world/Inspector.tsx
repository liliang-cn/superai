import { useEffect, useState } from "react";
import { ArrowRightIcon, XIcon, Maximize2Icon, PlayIcon, PauseIcon, Trash2Icon, SquareIcon, SendIcon, RefreshCwIcon, SearchIcon, ZapIcon } from "lucide-react";
import {
  CLIRunDetail, InstallMCPServer, InstallSkill, SearchMCPServers, SearchSkills,
  CancelCLIRun, DeleteDashboard, DeleteStandingAgent, ForgetLife, HiveTaskDetail, MemoryRecall, PauseStandingAgent,
  RefreshDashboard, RemoveMCPServer, RemoveSkill, ResolveToolApproval, ResumeStandingAgent, SendChat, StandingReports,
  StartCLIRun, WakeStandingAgent,
} from "../../wailsjs/go/app/App";
import { QUEEN_SESSION } from "../canvas/data";
import { useI18n } from "../lib/i18n";
import { toast } from "../lib/toasts";
import { PATHS, attentionPath, taskPath } from "../lib/routes";
import { focusInWorld } from "./bus";
import { openSwitcher } from "../lib/hivelink";
import Talk from "./Talk";
import type { Picked } from "./engine";
import type { WorldData } from "./cluster";

const fmt = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));
const when = (s?: string) => {
  const d = s ? new Date(s) : null;
  return d && !isNaN(+d) ? d.toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit", hour12: false }) : "";
};

/**
 * What is selected in the world, and everything that can be done with it.
 * Each kind of thing brings its own actions: a bee is woken, paused or told
 * something; a machine starts a coding run; an approval is answered here.
 * `open(path, panel)` goes to a page, optionally with its list showing.
 */
export default function Inspector({ picked, data, onClose, open, openDash }: {
  picked: Picked; data: WorldData; onClose: () => void; open: (path: string, panel?: boolean) => void;
  /** Shows a dashboard full size over the world. */
  openDash?: (id: string) => void;
}) {
  const { t, language, setLanguage } = useI18n();
  const [busy, setBusy] = useState("");
  const [text, setText] = useState("");
  const [confirm, setConfirm] = useState("");
  const [extra, setExtra] = useState<string>("");
  useEffect(() => { setText(""); setConfirm(""); setExtra(""); }, [picked.kind, picked.id]);

  /** Runs an action, says how it went, and keeps the button busy meanwhile. */
  const act = async (key: string, fn: () => Promise<unknown>, ok: string) => {
    setBusy(key);
    try { await fn(); toast.success(t(ok)); }
    catch (e) { toast.error(String(e instanceof Error ? e.message : e)); }
    finally { setBusy(""); }
  };
  /** A destructive action asks once more, on the same button. */
  const twice = (key: string, fn: () => void) => () => { if (confirm === key) { setConfirm(""); fn(); } else setConfirm(key); };

  const rows: [string, string][] = [];
  let kind = picked.kind;
  let body: React.ReactNode = null;
  const say = (placeholder: string, send: (s: string) => Promise<unknown>, ok: string) => (
    <form className="wi-say" onSubmit={(e) => { e.preventDefault(); const s = text.trim(); if (!s) return; void act("say", () => send(s), ok).then(() => setText("")); }}>
      <input value={text} onChange={(e) => setText(e.target.value)} placeholder={t(placeholder)} />
      <button type="submit" disabled={!text.trim() || busy === "say"} title={t("Send")}><SendIcon size={14} /></button>
    </form>
  );

  switch (picked.kind) {
    case "hall": {
      const tt = data.meter.total;
      kind = "Queen";
      rows.push(["State", data.busy || tt.live ? "Working" : "Idle"], ["Hive tokens", fmt(tt.tokens)], ["Tool calls", String(tt.calls)],
        ["Orders out", String(data.hive.tasks.filter((x) => x.state === "running").length)]);
      body = <Talk who={{ kind: "queen" }} placeholder="Talk to the queen…" />;
      break;
    }
    case "worker": {
      const m = data.hive.members.find((x) => x.name === picked.id);
      const p = data.meter.members.find((x) => x.name === picked.id);
      const mine = data.hive.tasks.filter((x) => x.worker === picked.id);
      const now = mine.find((x) => x.state === "running");
      kind = "Worker";
      rows.push(["State", m?.state === "lost" ? "Not answering" : now ? "Working" : "Idle"]);
      if (now) rows.push(["Doing", now.prompt]);
      if (p) rows.push(["Tokens", fmt(p.tokens)], ["Tool calls", String(p.calls)]);
      body = (
        <>
          <Talk who={{ kind: "worker", name: picked.id }} placeholder={`Talk to ${picked.id.replace(/^superai-/, "")}…`} />
          {mine.length > 0 && (
            <ul className="wi-list">
              {mine.slice(0, 4).map((x) => (
                <li key={x.id} onClick={() => open(taskPath(x.id))}><i className={`wi-dot ${x.state}`} />{x.prompt}<em>{t(x.state)}</em></li>
              ))}
            </ul>
          )}
        </>
      );
      break;
    }
    case "order": {
      const x = data.hive.tasks.find((y) => y.id === picked.id);
      kind = "Order";
      if (x) rows.push(["To", x.worker], ["State", x.state], ["Tool", x.tool ?? "—"], ["Tool calls", String(x.tools ?? 0)]);
      body = <button className="wl-go" onClick={() => open(taskPath(picked.id))}>{t("Open full log")}<ArrowRightIcon size={14} /></button>;
      break;
    }
    case "bee": {
      const b = data.bees.find((x) => x.id === picked.id);
      kind = "Bee";
      if (b) {
        rows.push(["Watches", b.goal], ["State", b.waitingFor ? "Needs you" : b.running ? "Out now" : b.paused ? "Paused" : "Resting"]);
        if (b.nextDue) rows.push(["Next", when(b.nextDue)]);
        if (b.waitingFor) rows.push(["Asks", b.waitingFor]);
      }
      body = b && (
        <>
          <Latest id={b.id} />
          <Talk who={{ kind: "bee", id: b.id }} placeholder={b.waitingFor ? "Answer it…" : "Tell it something — it wakes to read it"} />
          <div className="wi-acts">
            <button disabled={!!b.running || busy === "wake"} onClick={() => act("wake", () => WakeStandingAgent(b.id, ""), "Woken")}><ZapIcon size={14} />{t("Wake now")}</button>
            {b.paused
              ? <button disabled={busy === "pause"} onClick={() => act("pause", () => ResumeStandingAgent(b.id), "Resumed")}><PlayIcon size={14} />{t("Resume")}</button>
              : <button disabled={busy === "pause"} onClick={() => act("pause", () => PauseStandingAgent(b.id), "Paused")}><PauseIcon size={14} />{t("Pause")}</button>}
            <button onClick={() => open(PATHS.agents + "?agent=" + encodeURIComponent(b.id), true)}>{t("Edit")}</button>
            <button className="danger" onClick={twice("del", () => act("del", () => DeleteStandingAgent(b.id), "Deleted").then(onClose))}>
              <Trash2Icon size={14} />{t(confirm === "del" ? "Delete it?" : "Delete")}
            </button>
          </div>
        </>
      );
      break;
    }
    case "machine": {
      const a = data.linked.find((x) => x.name === picked.id);
      const clis = a?.clis?.length ? a.clis : ["claude", "codex"];
      const host = picked.id;
      const mine = data.runs.filter((r) => (r.remote || "core") === host);
      const live = mine.filter((r) => r.state === "running");
      kind = "Machine";
      if (a) rows.push(["System", `${a.os} ${a.arch}`]);
      rows.push(["Running", String(live.length)], ["Sessions", String(mine.length)]);
      body = (
        <>
          <NewRun clis={clis} host={host} onStart={(agent, prompt) => act("run", () => StartCLIRun(agent, prompt, "", "", true), "Run started")} busy={busy === "run"} />
          <ul className="wi-list">
            {mine.slice(0, 5).map((r) => <RunLine key={r.id} run={r} onStop={() => act("stop" + r.id, () => CancelCLIRun(r.id), "Stopped")} />)}
          </ul>
          <button className="wl-go ghost" onClick={() => open(PATHS.coding, true)}>{t("All coding runs")}<ArrowRightIcon size={14} /></button>
        </>
      );
      break;
    }
    case "cli": case "agent": {
      // A coding CLI or a named agent on a linked machine: talk to it.
      const [name, ...rest] = picked.id.split(".");
      const host = rest.join(".") || "core";
      const mine = data.runs.filter((r) => (r.remote || "core") === host && (r.agent === picked.id || r.agent === name));
      const live = mine.filter((r) => r.state === "running");
      kind = picked.kind === "cli" ? "Coding agent" : "Agent";
      rows.push(["On", host], ["Running", String(live.length)]);
      body = (
        <>
          <Talk who={picked.kind === "cli" ? { kind: "cli", agent: picked.id } : { kind: "agent", name }} placeholder={picked.kind === "cli" ? `What should ${name} do?` : `Talk to ${name}…`} />
          {live.length > 0 && (
            <ul className="wi-list">
              {live.map((r) => (
                <li key={r.id}><i className="wi-dot running" />{r.prompt}
                  <button className="wi-x" title={t("Stop")} onClick={() => act("stop" + r.id, () => CancelCLIRun(r.id), "Stopped")}><SquareIcon size={11} /></button>
                </li>
              ))}
            </ul>
          )}
        </>
      );
      break;
    }
    case "brain": {
      kind = "Shared brain";
      rows.push(["Store", "CortexDB, shared by every worker"]);
      body = (
        <>
          <form className="wi-say" onSubmit={(e) => { e.preventDefault(); const q = text.trim(); if (!q) return; setBusy("recall"); MemoryRecall(q).then(setExtra).catch((er) => setExtra(String(er))).finally(() => setBusy("")); }}>
            <input value={text} onChange={(e) => setText(e.target.value)} placeholder={t("Search what the hive knows…")} />
            <button type="submit" disabled={!text.trim() || busy === "recall"}><SearchIcon size={14} /></button>
          </form>
          {busy === "recall" && <p className="wi-note">{t("Searching…")}</p>}
          {extra && <pre className="wi-text">{extra}</pre>}
          <button className="wl-go ghost" onClick={() => open(PATHS.knowledge, true)}>{t("Open the graph")}<ArrowRightIcon size={14} /></button>
        </>
      );
      break;
    }
    case "dashboard": {
      kind = "Dashboard";
      const d = data.dashboards.find((x) => x.id === picked.id);
      if (d?.refreshed_at) rows.push(["Updated", when(d.refreshed_at)]);
      body = (
        <div className="wi-acts">
          <button onClick={() => (openDash ? openDash(picked.id) : open(PATHS.dashboards, true))}>{t("Open")}</button>
          <button disabled={busy === "refresh"} onClick={() => act("refresh", () => RefreshDashboard(picked.id), "Refreshing")}><RefreshCwIcon size={14} />{t("Refresh")}</button>
          <button className="danger" onClick={twice("del", () => act("del", () => DeleteDashboard(picked.id), "Deleted").then(onClose))}><Trash2Icon size={14} />{t(confirm === "del" ? "Delete it?" : "Delete")}</button>
        </div>
      );
      break;
    }
    case "skills":
      kind = "Skills";
      rows.push(["Installed", String(data.skills.length)]);
      body = <SkillShop installed={data.skills} />;
      break;
    case "mcphub":
      kind = "MCP servers";
      rows.push(["Connected", `${data.mcp.filter((m) => m.ok).length} of ${data.mcp.length}`]);
      body = <McpShop servers={data.mcp} />;
      break;
    case "skill":
      kind = "Skill";
      body = (
        <div className="wi-acts">
          <button className="danger" onClick={twice("del", () => act("del", () => RemoveSkill(picked.id), "Removed").then(onClose))}><Trash2Icon size={14} />{t(confirm === "del" ? "Remove it?" : "Remove")}</button>
        </div>
      );
      break;
    case "mcp": {
      const m = data.mcp.find((x) => x.name === picked.id);
      kind = "MCP server";
      if (m) rows.push(["State", m.ok ? "Connected" : "Not connected"], ["Tools", String(m.tools)]);
      body = (
        <div className="wi-acts">
          <button className="danger" onClick={twice("del", () => act("del", () => RemoveMCPServer(picked.id), "Removed").then(onClose))}><Trash2Icon size={14} />{t(confirm === "del" ? "Remove it?" : "Remove")}</button>
        </div>
      );
      break;
    }
    case "event": case "reminder": {
      const it = data.attention.find((a) => a.kind === picked.kind && (a.ref ?? a.title) === picked.id);
      kind = picked.kind === "event" ? "Meeting" : "Reminder";
      if (it?.at) rows.push(["When", when(it.at)]);
      if (it?.place) rows.push(["Where", it.place]);
      if (it?.detail) rows.push(["Detail", it.detail]);
      body = (
        <div className="wi-acts">
          <button onClick={() => open(PATHS.records, true)}>{t("Open Records")}</button>
          {picked.kind === "event" && (
            <button className="danger" onClick={twice("del", () => act("del", () => ForgetLife("schedule", picked.id), "Deleted").then(onClose))}><Trash2Icon size={14} />{t(confirm === "del" ? "Delete it?" : "Delete")}</button>
          )}
        </div>
      );
      break;
    }
    case "figure": {
      kind = "Live figure";
      const [who, fig] = picked.id.includes(":") ? picked.id.split(":") : ["", picked.id];
      const m = who ? data.meter.members.find((x) => x.name === who) : data.meter.total;
      const f = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));
      if (m) rows.push(["Tokens", f(m.tokens)], ["Tool calls", String(m.calls)], ["Rounds", String(m.rounds)], ["tok/s", String(Math.round(m.tokPerSec))], ["Failures", String(m.fails)]);
      void fig;
      body = <button className="wl-go ghost" onClick={() => open(PATHS.stats, true)}>{t("Open Stats")}<ArrowRightIcon size={14} /></button>;
      break;
    }
    case "settings":
      kind = "This core";
      body = (
        <>
        <div className="wi-seg" role="radiogroup" aria-label="Language">
          <span>{t("Language")}</span>
          {([["en", "English"], ["zh-CN", "中文"]] as const).map(([v, label]) => (
            <button key={v} role="radio" aria-checked={language === v} className={language === v ? "on" : ""} onClick={() => setLanguage(v)}>{label}</button>
          ))}
        </div>
        <div className="wi-acts">
          <button onClick={() => open(PATHS.settings, true)}>{t("Settings")}</button>
          <button onClick={() => window.dispatchEvent(new Event("superai:theme"))}>{t("Theme")}</button>
          <button onClick={() => openSwitcher()}>{t("Switch backend")}</button>
          <button onClick={() => open(PATHS.stats, true)}>{t("Stats")}</button>
        </div>
        </>
      );
      break;
    case "node": {
      kind = "Node";
      const on = data.hive.members.filter((m) => (m as any).node === picked.id);
      rows.push(["Workers", on.map((m) => m.name.replace(/^superai-/, "")).join(", ") || "—"]);
      body = (
        <ul className="wi-list">
          {on.map((m) => <li key={m.name} onClick={() => focusInWorld("worker", m.name)}><i className={`wi-dot ${m.state === "live" ? "done" : "failed"}`} />{m.name}<em>{m.state}</em></li>)}
        </ul>
      );
      break;
    }
    case "records": {
      kind = "Calendar";
      const soon = data.attention.filter((a) => a.level === "soon" && a.at).sort((a, b) => +new Date(a.at!) - +new Date(b.at!));
      body = (
        <>
          <ul className="wi-list wi-shop">
            {soon.map((a) => (
              <li key={a.kind + (a.ref ?? a.title)}>
                <span className="wi-shop-n"><b>{a.title}</b><small>{when(a.at)}{a.place ? " · " + a.place : ""}</small></span>
                {a.kind === "event" && a.ref && (
                  <button className="wi-x" title={t("Delete")} onClick={twice("del" + a.ref, () => act("del", () => ForgetLife("schedule", a.ref!), "Deleted"))}>
                    {confirm === "del" + a.ref ? "?" : <Trash2Icon size={11} />}
                  </button>
                )}
              </li>
            ))}
            {soon.length === 0 && <li className="wi-none">{t("Nothing on the calendar this week.")}</li>}
          </ul>
          <Talk who={{ kind: "queen" }} placeholder="Put something on the calendar…" />
        </>
      );
      break;
    }
    default: {
      const it = data.attention.find((a) => (a.ref ?? "") === picked.id && a.kind === picked.kind);
      const label: Record<string, string> = { approval: "Approval required", bee: "Waiting for your reply", report: "From a bee", failed: "Task failed", lost: "Worker offline", run: "Coding run failed" };
      kind = label[picked.kind] ?? "Needs you";
      if (it?.kind === "report") rows.push(["Reports", String(it.updates ?? 1)]);
      if (it?.detail) rows.push([it.kind === "approval" ? "Command" : "Latest", it.detail]);
      if (it?.kind === "approval") {
        body = (
          <div className="wl-two">
            <button className="wl-go" disabled={!!busy} onClick={() => act("y", () => ResolveToolApproval(it.ref ?? "", true), "Allowed").then(onClose)}>{t("Allow once")}</button>
            <button className="wl-go ghost" disabled={!!busy} onClick={() => act("n", () => ResolveToolApproval(it.ref ?? "", false), "Denied").then(onClose)}>{t("Deny")}</button>
          </div>
        );
      } else if (it?.kind === "report" || it?.kind === "bee") {
        body = (
          <>
            {it.kind === "bee" && say("Answer it…", (s) => WakeStandingAgent(it.ref ?? "", s), "Sent")}
            <button className="wl-go" onClick={() => focusInWorld("bee", it.ref ?? "")}>{t("Go to the bee")}<ArrowRightIcon size={14} /></button>
          </>
        );
      } else if (it?.kind === "lost") {
        body = <button className="wl-go" onClick={() => focusInWorld("worker", it.ref ?? "")}>{t("Go to the worker")}<ArrowRightIcon size={14} /></button>;
      } else if (it?.kind === "failed") {
        body = <FailedOrder id={it.ref ?? ""} open={() => open(taskPath(it.ref ?? ""))} />;
      } else if (it) {
        body = <button className="wl-go" onClick={() => open(attentionPath(it))}>{t("Open details")}<ArrowRightIcon size={14} /></button>;
      }
    }
  }

  // Every thing has a page with all it can do; it opens full size, as its
  // screen, over the world.
  const full: Record<string, string> = {
    hall: PATHS.home, worker: PATHS.hive, node: PATHS.hive, order: taskPath(picked.id),
    bee: PATHS.agents + "?agent=" + encodeURIComponent(picked.id), machine: PATHS.coding, cli: PATHS.coding, agent: PATHS.coding,
    brain: PATHS.knowledge, skills: PATHS.skills, skill: PATHS.skills, mcphub: PATHS.mcp, mcp: PATHS.mcp,
    dashboard: PATHS.dashboards, records: PATHS.records, event: PATHS.records, reminder: PATHS.records,
    figure: PATHS.stats, settings: PATHS.settings,
  };
  const screen = full[picked.kind] ?? PATHS.tasks;
  return (
    <aside className="wl-detail">
      <header>
        <div><em>{t(kind)}</em><b>{picked.title}</b></div>
        <button title={t("Open full screen")} onClick={() => open(screen, true)}><Maximize2Icon size={14} /></button>
        <button title={t("Close")} onClick={onClose}><XIcon size={15} /></button>
      </header>
      {rows.length > 0 && <dl>{rows.map(([k, v]) => <div key={k}><dt>{t(k)}</dt><dd>{t(v)}</dd></div>)}</dl>}
      <div className="wi-body">{body}</div>
    </aside>
  );
}

/** A bee's newest report, read when the bee is selected. */
function Latest({ id }: { id: string }) {
  const { t } = useI18n();
  const [r, setR] = useState<{ at: string; message: string } | null>(null);
  useEffect(() => {
    let alive = true;
    StandingReports(id).then((l: any[]) => { if (alive) setR(l?.[0] ? { at: String(l[0].at ?? ""), message: String(l[0].message ?? "") } : null); }).catch(() => {});
    return () => { alive = false; };
  }, [id]);
  if (!r) return null;
  return <div className="wi-latest"><em>{t("Last said")} · {when(r.at)}</em><p>{r.message}</p></div>;
}

/** What a failed order said, and a way to the whole log. */
function FailedOrder({ id, open }: { id: string; open: () => void }) {
  const { t } = useI18n();
  const [why, setWhy] = useState("");
  useEffect(() => {
    HiveTaskDetail(id).then((d: any) => setWhy(String(d?.error || d?.result || ""))).catch(() => {});
  }, [id]);
  return (
    <>
      {why && <pre className="wi-text">{why}</pre>}
      <button className="wl-go" onClick={open}>{t("Open full log")}<ArrowRightIcon size={14} /></button>
    </>
  );
}

/** Start a coding run on this machine: which CLI, and what to do. */
function NewRun({ clis, host, onStart, busy }: { clis: string[]; host: string; onStart: (agent: string, prompt: string) => Promise<unknown>; busy: boolean }) {
  const { t } = useI18n();
  const [cli, setCli] = useState(clis[0]);
  const [prompt, setPrompt] = useState("");
  const agent = host === "core" ? cli : `${cli}.${host}`;
  return (
    <form className="wi-run" onSubmit={(e) => { e.preventDefault(); if (prompt.trim()) void onStart(agent, prompt.trim()).then(() => setPrompt("")); }}>
      <div className="wi-clis">{clis.slice(0, 6).map((c) => <button type="button" key={c} className={c === cli ? "on" : ""} onClick={() => setCli(c)}>{c}</button>)}</div>
      <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={2} placeholder={t("What should {cli} do here?", { cli })} />
      <button className="wl-go" type="submit" disabled={busy || !prompt.trim()}>{t("Start run")}<PlayIcon size={13} /></button>
    </form>
  );
}

/** Skills on this machine that can be installed, found by what they do; an
 *  install is one click and lands on the rack (and, through the hive, on
 *  every worker). */
function SkillShop({ installed }: { installed: string[] }) {
  const { t } = useI18n();
  const [q, setQ] = useState("");
  const [found, setFound] = useState<{ name: string; description: string; path: string; installed: boolean }[] | null>(null);
  const [busy, setBusy] = useState("");
  const search = async (query: string) => {
    setBusy("search");
    try { setFound(((await SearchSkills(query)) ?? []) as any); } catch (e) { toast.error(String(e)); } finally { setBusy(""); }
  };
  useEffect(() => { void search(""); }, []);
  return (
    <>
      <form className="wi-say" onSubmit={(e) => { e.preventDefault(); void search(q.trim()); }}>
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("Find a skill by what it does…")} />
        <button type="submit" disabled={busy === "search"}><SearchIcon size={14} /></button>
      </form>
      <ul className="wi-list wi-shop">
        {(found ?? []).slice(0, 8).map((c) => {
          const have = installed.includes(c.name) || c.installed;
          return (
            <li key={c.path || c.name} title={c.description}>
              <span className="wi-shop-n"><b>{c.name}</b><small>{c.description}</small></span>
              <button className="wi-get" disabled={have || busy === c.name} onClick={async () => {
                setBusy(c.name);
                try {
                  const r = await InstallSkill(c.name, c.path);
                  if (r && r !== "ok") throw new Error(r);
                  toast.success(t("Installed {name}", { name: c.name }));
                  setFound((cur) => cur?.map((x) => (x.name === c.name ? { ...x, installed: true } : x)) ?? null);
                } catch (e) { toast.error(String(e instanceof Error ? e.message : e)); } finally { setBusy(""); }
              }}>{have ? t("Installed") : busy === c.name ? "…" : t("Install")}</button>
            </li>
          );
        })}
        {found && found.length === 0 && <li className="wi-none">{t("Nothing found on this machine.")}</li>}
      </ul>
    </>
  );
}

/** MCP servers from the official registry: search by what they should do,
 *  install with one click; each one becomes a box cabled to the hub. */
function McpShop({ servers }: { servers: WorldData["mcp"] }) {
  const { t } = useI18n();
  const [q, setQ] = useState("");
  const [found, setFound] = useState<{ name: string; title?: string; description: string; command?: string; args?: string[] }[] | null>(null);
  const [busy, setBusy] = useState("");
  const short = (full: string) => (full.split("/").pop() || full).replace(/[^A-Za-z0-9._-]/g, "-");
  const search = async () => {
    if (!q.trim()) return;
    setBusy("search");
    try {
      const r: any = await SearchMCPServers(q.trim());
      if (r?.error) toast.error(r.error);
      setFound(Array.isArray(r?.servers) ? r.servers : []);
    } catch (e) { toast.error(String(e)); } finally { setBusy(""); }
  };
  return (
    <>
      {servers.length > 0 && (
        <ul className="wi-list">
          {servers.map((m) => (
            <li key={m.name} onClick={() => focusInWorld("mcp", m.name)}><i className={`wi-dot ${m.ok ? "done" : "failed"}`} />{m.name}<em>{m.ok ? `${m.tools} tools` : t("not connected")}</em></li>
          ))}
        </ul>
      )}
      <form className="wi-say" onSubmit={(e) => { e.preventDefault(); void search(); }}>
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("What should it be able to do? e.g. postgres, slack")} />
        <button type="submit" disabled={!q.trim() || busy === "search"}><SearchIcon size={14} /></button>
      </form>
      {busy === "search" && <p className="wi-note">{t("Searching…")}</p>}
      {found && (
        <ul className="wi-list wi-shop">
          {found.slice(0, 8).map((c) => {
            const name = short(c.name);
            const have = servers.some((x) => x.name === name);
            return (
              <li key={c.name} title={c.description}>
                <span className="wi-shop-n"><b>{c.title || name}</b><small>{c.description}</small></span>
                <button className="wi-get" disabled={have || !c.command || busy === c.name} title={c.command ? "" : t("Hosted remotely; add it by URL instead.")} onClick={async () => {
                  setBusy(c.name);
                  try {
                    const r = await InstallMCPServer(name, c.command || "", c.args || [], {});
                    if (r !== "ok") throw new Error(r);
                    toast.success(t("Installed {name}", { name }));
                  } catch (e) { toast.error(String(e instanceof Error ? e.message : e)); } finally { setBusy(""); }
                }}>{have ? t("Installed") : busy === c.name ? "…" : t("Install")}</button>
              </li>
            );
          })}
          {found.length === 0 && <li className="wi-none">{t("No servers matched.")}</li>}
        </ul>
      )}
    </>
  );
}

/** One coding run in a machine's card: its prompt, and what it came to when
 *  opened. */
function RunLine({ run, onStop }: { run: { id: string; prompt: string; state: string }; onStop: () => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [out, setOut] = useState("");
  useEffect(() => {
    if (!open || out) return;
    CLIRunDetail(run.id).then((r: any) => setOut(String(r?.summary || r?.error || (run.state === "running" ? t("Still running…") : "")))).catch((e) => setOut(String(e)));
  }, [open]);
  return (
    <>
      <li onClick={() => setOpen((v) => !v)}><i className={`wi-dot ${run.state}`} />{run.prompt}
        {run.state === "running" && <button className="wi-x" title={t("Stop")} onClick={(e) => { e.stopPropagation(); onStop(); }}><SquareIcon size={11} /></button>}
      </li>
      {open && out && <pre className="wi-text">{out}</pre>}
    </>
  );
}
