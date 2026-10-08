import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronDownIcon, HomeIcon, LocateFixedIcon, MinusIcon, PlusIcon, RotateCcwIcon, RotateCwIcon } from "lucide-react";
import { Dashboards, MCP, Skills } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { useBees, useCodingRuns, useHive, useLinkedAgents } from "../canvas/data";
import { useAttentionState } from "../canvas/attention";
import { useHiveMeter } from "../components/HiveMeter";
import { useI18n } from "../lib/i18n";
import type { ViewKey } from "../lib/types";
import { WorldEngine, type Picked } from "./engine";
import { cluster, lensOf, type WorldData } from "./cluster";
import { worldBus } from "./bus";
import Inspector from "./Inspector";
import Command from "./Command";
import { dashboards as dashApi, type Dashboard } from "../lib/dashboards";
import { Response } from "@/components/ai-elements/response";
import { RefreshCwIcon, XIcon } from "lucide-react";
import "./world.css";

/** The app's slower lists, refreshed now and then and on the events that
 *  change them. */
function useSlow() {
  const [dashboards, setDashboards] = useState<WorldData["dashboards"]>([]);
  const [skills, setSkills] = useState<string[]>([]);
  const [mcp, setMcp] = useState<WorldData["mcp"]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => {
      Dashboards().then((l: any) => { if (alive) setDashboards(((l ?? []) as any[]).map((d) => ({ id: String(d.id), name: String(d.name ?? d.title ?? "Dashboard"), refreshed_at: d.refreshed_at }))); }).catch(() => {});
      Skills().then((l: any) => { if (alive) setSkills(((l ?? []) as any[]).map((s) => String(s.name))); }).catch(() => {});
      MCP().then((l: any) => { if (alive) setMcp(((l ?? []) as any[]).map((s) => ({ name: String(s.name), ok: !!s.running, tools: Number(s.tool_count ?? 0) }))); }).catch(() => {});
    };
    load();
    const t = window.setInterval(load, 60000);
    const offs = ["dashboard:changed", "skills:changed", "mcp:changed"].map((e) => EventsOn(e, load));
    return () => { alive = false; window.clearInterval(t); offs.forEach((o) => typeof o === "function" && o()); };
  }, []);
  return { dashboards, skills, mcp };
}

/** Whether a conversation turn is going, anywhere in the app. */
function useBusy() {
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let timer = 0;
    const on = () => { setBusy(true); window.clearTimeout(timer); timer = window.setTimeout(() => setBusy(false), 20000); };
    const off = () => { window.clearTimeout(timer); setBusy(false); };
    const a = EventsOn("chat:event", on);
    const b = ["chat:done", "chat:error", "chat:cancelled"].map((e) => EventsOn(e, off));
    return () => { window.clearTimeout(timer); [a, ...b].forEach((x) => typeof x === "function" && x()); };
  }, []);
  return busy;
}

/**
 * The cluster behind the whole desktop app: one scene, a lens per page.
 * What is selected gets a card tied to it by a line, with everything that can
 * be done to it; the bar along the bottom talks to the queen from anywhere.
 */
export default function World({ view }: { view: ViewKey }) {
  const host = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const line = useRef<SVGLineElement>(null);
  const engine = useRef<WorldEngine<WorldData> | null>(null);
  const navigate = useNavigate();
  const [picked, setPicked] = useState<Picked | null>(null);
  const [inset, setInsetState] = useState(0);
  const [dash, setDash] = useState("");
  const home = view === "home" || view === "chat";

  const hive = useHive();
  const runs = useCodingRuns();
  const bees = useBees();
  const linked = useLinkedAgents();
  const { items: attention } = useAttentionState();
  const meter = useHiveMeter();
  const slow = useSlow();
  const busy = useBusy();

  useEffect(() => {
    if (!host.current) return;
    const e = new WorldEngine<WorldData>(host.current);
    e.add(cluster);
    e.pickHandler((p) => {
      setPicked(p);
      worldBus.publish(p ? { kind: p.kind, id: p.id } : null);
    });
    const l = lensOf(view);
    e.look(l.x, l.z, l.zoom, "cluster", true);
    engine.current = e;
    if (import.meta.env.DEV) (window as unknown as { __world: unknown }).__world = e;
    worldBus.setFocus((kind, id) => e.focusOn(kind, id));
    return () => { worldBus.setFocus(null); e.dispose(); engine.current = null; };
  }, []);

  const data: WorldData = useMemo(() => ({
    hive, runs, bees, linked, attention,
    dashboards: slow.dashboards, skills: slow.skills, mcp: slow.mcp,
    knowledge: 0, meter: { total: meter.total, members: meter.members }, busy,
  }), [hive, runs, bees, linked, attention, slow, meter, busy]);
  useEffect(() => { engine.current?.update(data); }, [data]);

  // A new page: a new lens, and nothing selected.
  useEffect(() => {
    const l = lensOf(view);
    engine.current?.look(l.x, l.z, l.zoom, "cluster");
    engine.current?.select(null);
    setPicked(null);
    worldBus.publish(null);
  }, [view]);

  // The card follows what it describes; the engine moves it every frame.
  useEffect(() => { engine.current?.setCallout(picked ? card.current : null, picked ? line.current : null); }, [picked]);

  // Keep what is looked at centred in the part of the window left open.
  useEffect(() => {
    const measure = () => {
      const root = host.current?.getBoundingClientRect();
      const cover = document.querySelector(home ? ".dk-live" : ".dk-page") as HTMLElement | null;
      if (!root || !cover || cover.offsetParent === null) { engine.current?.setInset(0); setInsetState(0); return; }
      const r = cover.getBoundingClientRect();
      const inset = home ? r.left - root.left - (root.right - r.right) : r.right - root.left;
      engine.current?.setInset(Math.max(0, inset));
      setInsetState(home ? 0 : Math.max(0, r.right - root.left));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(document.body);
    const t = window.setTimeout(measure, 300);
    return () => { ro.disconnect(); window.clearTimeout(t); };
  }, [view]);

  const close = () => { setPicked(null); engine.current?.select(null); worldBus.publish(null); };
  return (
    <div className="wl-root">
      <div ref={host} className="wl-host" />
      <svg className="wl-lead"><line ref={line} /></svg>
      <div className="wl-cam">
        <button title="Zoom in" onClick={() => engine.current?.zoomBy(1.3)}><PlusIcon size={16} /></button>
        <button title="Zoom out" onClick={() => engine.current?.zoomBy(1 / 1.3)}><MinusIcon size={16} /></button>
        <button title="Turn left" onClick={() => engine.current?.rotateBy(-Math.PI / 8)}><RotateCcwIcon size={15} /></button>
        <button title="Turn right" onClick={() => engine.current?.rotateBy(Math.PI / 8)}><RotateCwIcon size={15} /></button>
        <button title="Back to this view" onClick={() => engine.current?.home()}><HomeIcon size={15} /></button>
        <span className="wl-cam-sep" />
        <button title="Reset to the default view" onClick={() => {
          // The whole cluster, from the usual corner, nothing selected.
          const l = lensOf("home");
          engine.current?.select(null);
          setPicked(null);
          worldBus.publish(null);
          engine.current?.look(l.x, l.z, l.zoom, "cluster");
        }}><LocateFixedIcon size={15} /></button>
      </div>
      <Hud view={view} data={data} left={24} />
      <div ref={card} className="wl-callout" style={{ display: picked ? "" : "none" }}>
        {picked && <Inspector key={picked.kind + ":" + picked.id} picked={picked} data={data} onClose={close} openDash={setDash} open={(to, full) => { if (full) window.dispatchEvent(new CustomEvent("superai:window", { detail: to })); else navigate(to); }} />}
      </div>
      {dash && <DashScreen id={dash} onClose={() => setDash("")} />}
      <Command view={view} onConversation={() => window.dispatchEvent(new CustomEvent("superai:window", { detail: "/" }))} onTyping={(kind, id) => engine.current?.hint(kind, id)} />
    </div>
  );
}

/** The hive's figures, on one flat screen that stays on the page: the
 *  totals, how fast tokens are going lately, and each member's share. Folds
 *  to its top line. */
function Hud({ data, left }: { view: ViewKey; data: WorldData; left: number }) {
  const { t } = useI18n();
  // Folded to one pill unless opened; it remembers.
  const [open, setOpen] = useState(() => { try { return localStorage.getItem("superai-world-stats") === "1"; } catch { return false; } });
  const toggle = () => setOpen((v) => { try { localStorage.setItem("superai-world-stats", v ? "0" : "1"); } catch { /* fine */ } return !v; });
  // Tokens spent between updates, for the line.
  const hist = useRef<number[]>([]);
  const last = useRef(-1);
  const tt = data.meter.total;
  useEffect(() => {
    if (last.current > 0) { hist.current.push(Math.max(0, tt.tokens - last.current)); if (hist.current.length > 90) hist.current.shift(); }
    last.current = tt.tokens;
  }, [tt.tokens]);
  const run = data.hive.tasks.filter((x) => x.state === "running");
  const fmt = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));
  const workers = data.hive.members.filter((m) => !/queen/.test(m.name));
  const live = workers.filter((m) => m.state === "live").length;
  const needs = data.attention.filter((a) => a.level === "needs").length;
  const pts = hist.current;
  const max = Math.max(1, ...pts);
  const W = 260, H = 44;
  const line = pts.map((v, i) => `${(i / Math.max(1, pts.length - 1)) * W},${H - 2 - (v / max) * (H - 6)}`).join(" ");
  const members = [...data.meter.members].sort((a, b) => b.tokens - a.tokens).slice(0, 6);
  const topTokens = Math.max(1, ...members.map((m) => m.tokens));
  const short = (n: string) => n.replace(/^superai-worker-/, "w").replace(/^superai-/, "");
  return (
    <div className={`wl-stats${open ? "" : " folded"}`} style={{ left }}>
      <button className="wl-stats-top" onClick={toggle} title={t(open ? "Fold" : "Show the figures")}>
        <span className={`wl-stats-dot${tt.tokPerSec > 0.5 ? " on" : ""}`} />
        <b>{fmt(tt.tokens)}</b><em>{t("tokens")}</em>
        <b>{Math.round(tt.tokPerSec)}</b><em>tok/s</em>
        <b className={live < workers.length ? "bad" : ""}>{live}/{workers.length}</b><em>{t("workers")}</em>
        {needs > 0 && <><b className="warn">{needs}</b><em>{t("need you")}</em></>}
        <ChevronDownIcon size={15} className="wl-stats-chev" />
      </button>
      {open && (
        <div className="wl-stats-body">
          <div className="wl-stats-grid">
            <div><b>{tt.calls}</b><span>{t("tool calls")}</span></div>
            <div><b>{tt.rounds}</b><span>{t("rounds")}</span></div>
            <div><b>{run.length}</b><span>{t("orders out")}</span></div>
            <div><b className={tt.fails ? "bad" : ""}>{tt.fails}</b><span>{t("failures")}</span></div>
          </div>
          <svg className="wl-spark" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none">
            {pts.length > 1 && <polyline points={`0,${H} ${line} ${W},${H}`} className="fill" />}
            {pts.length > 1 && <polyline points={line} className="stroke" />}
          </svg>
          <div className="wl-stats-cap"><span>{pts.length > 1 ? t("tokens, lately") : t("tokens, lately — the line starts with the next work")}</span>{pts.length > 1 && <span>{t("peak")} {fmt(max)}</span>}</div>
          <ul className="wl-stats-who">
            {members.map((m) => (
              <li key={m.name}>
                <i className={m.live ? "on" : ""} />
                <span>{short(m.name)}</span>
                <span className={`bar${m.live ? " live" : ""}`}><span style={{ width: `${Math.max(2, (m.tokens / topTokens) * 100)}%` }} /></span>
                <b>{fmt(m.tokens)}</b>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

/** A dashboard full size, over the world, as the screen it stands for would
 *  show it up close. */
function DashScreen({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useI18n();
  const [d, setD] = useState<Dashboard | null>(null);
  const [busy, setBusy] = useState(false);
  const load = () => dashApi.list().then((l) => setD((l ?? []).find((x) => String(x.id) === id) ?? null)).catch(() => {});
  useEffect(() => { void load(); }, [id]);
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", k);
    return () => window.removeEventListener("keydown", k);
  }, []);
  return (
    <div className="wl-screen" onClick={onClose}>
      <div className="wl-screen-in" onClick={(e) => e.stopPropagation()}>
        <header>
          <b>{d?.name ?? "…"}</b>
          <button disabled={busy} title={t("Refresh")} onClick={async () => { setBusy(true); try { await dashApi.refresh(id); await load(); } finally { setBusy(false); } }}><RefreshCwIcon size={15} /></button>
          <button title={t("Close")} onClick={onClose}><XIcon size={16} /></button>
        </header>
        <div className="wl-screen-body">{d ? <Response>{(d as any).source ?? ""}</Response> : t("Loading…")}</div>
      </div>
    </div>
  );
}
