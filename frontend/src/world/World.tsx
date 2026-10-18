import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronDownIcon, GaugeIcon, HomeIcon, LocateFixedIcon, MinusIcon, PlusIcon, RotateCcwIcon, RotateCwIcon } from "lucide-react";
import { Dashboards, MCP, NotedToday, Skills } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { useBees, useCodingRuns, useHive, useHosts, useLinkedAgents } from "../canvas/data";
import { useAttentionState } from "../canvas/attention";
import { useHiveMeter } from "../components/HiveMeter";
import { useI18n } from "../lib/i18n";
import { useDaylight } from "../lib/useDaylight";
import type { ViewKey } from "../lib/types";
import { WorldEngine, type Picked } from "./engine";
import { cluster, lensOf, type WorldData } from "./cluster";
import { worldBus } from "./bus";
import Inspector from "./Inspector";
import Command from "./Command";
import StatsHud from "./StatsHud";
import { agendaOf, touchesNoted, type AgendaEntry } from "./agenda";
import { dashboards as dashApi, type Dashboard } from "../lib/dashboards";
import { Response } from "@/components/ai-elements/response";
import { RefreshCwIcon, XIcon } from "lucide-react";
import "./world.css";

const HOLO_KEY = "superai-world-holo";

/** The app's slower lists, refreshed now and then and on the events that
 *  change them. */
function useSlow() {
  const [dashboards, setDashboards] = useState<WorldData["dashboards"]>([]);
  const [skills, setSkills] = useState<string[]>([]);
  const [mcp, setMcp] = useState<WorldData["mcp"]>([]);
  const [agenda, setAgenda] = useState<AgendaEntry[]>([]);
  const [agendaTick, setAgendaTick] = useState(0);
  // noted's day: a minute old at most, and read again after a noted call.
  useEffect(() => {
    let alive = true;
    const load = () => { NotedToday().then((r: unknown) => { if (alive) setAgenda(agendaOf(r)); }).catch(() => { if (alive) setAgenda([]); }); };
    load();
    const t = window.setInterval(load, 60000);
    return () => { alive = false; window.clearInterval(t); };
  }, [agendaTick]);
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
  return { dashboards, skills, mcp, agenda, reloadAgenda: () => setAgendaTick((n) => n + 1) };
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
  const { t } = useI18n();
  // The figures in the corners: on unless turned off, and remembered.
  const [holo, setHolo] = useState(() => { try { return localStorage.getItem(HOLO_KEY) !== "0"; } catch { return true; } });
  const flipHolo = () => setHolo((v) => { try { localStorage.setItem(HOLO_KEY, v ? "0" : "1"); } catch { /* this visit only */ } return !v; });
  // S, when nothing is being typed into.
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "s" || e.metaKey || e.ctrlKey || e.altKey || e.repeat) return;
      if (e.isComposing || e.keyCode === 229) return;
      if ((e.target as HTMLElement)?.closest?.("input,textarea,select,[contenteditable=''],[contenteditable=true]")) return;
      e.preventDefault();
      flipHolo();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);

  const hive = useHive();
  const runs = useCodingRuns();
  const bees = useBees();
  const linked = useLinkedAgents();
  const topo = useHosts();
  const byHost = !!topo.hosts?.length;
  const { items: attention } = useAttentionState();
  const meter = useHiveMeter();
  const slow = useSlow();
  const busy = useBusy();
  // A noted tool call that just ended may have changed today.
  const notedBusy = meter.members.some((m) => m.live && touchesNoted(m.doing));
  const wasNoted = useRef(false);
  useEffect(() => {
    if (wasNoted.current && !notedBusy) slow.reloadAgenda();
    wasNoted.current = notedBusy;
  }, [notedBusy]);

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
    hive, runs, bees, linked, attention, agenda: slow.agenda,
    dashboards: slow.dashboards, skills: slow.skills, mcp: slow.mcp,
    knowledge: 0, meter: { total: meter.total, members: meter.members }, busy,
    hosts: topo.hosts, services: topo.services,
  }), [hive, runs, bees, linked, topo, attention, slow, meter, busy]);
  useEffect(() => { engine.current?.update(data); }, [data]);
  const day = useDaylight();
  useEffect(() => { engine.current?.setNight(!day); }, [day]);

  // A new page: a new lens, and nothing selected.
  useEffect(() => {
    const l = lensOf(view, byHost);
    engine.current?.look(l.x, l.z, l.zoom, "cluster");
    engine.current?.select(null);
    setPicked(null);
    worldBus.publish(null);
  }, [view]);
  // The hosts arrive after the first look: stand back for them, once.
  useEffect(() => {
    if (!byHost) return;
    const l = lensOf(view, true);
    engine.current?.look(l.x, l.z, l.zoom, "cluster");
  }, [byHost]);

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
          const l = lensOf("home", byHost);
          engine.current?.select(null);
          setPicked(null);
          worldBus.publish(null);
          engine.current?.look(l.x, l.z, l.zoom, "cluster");
        }}><LocateFixedIcon size={15} /></button>
        <span className="wl-cam-sep" />
        <button className={holo ? "on" : ""} aria-pressed={holo} aria-label={t("Stats")} title={`${t("Stats")} (S)`} onClick={flipHolo}><GaugeIcon size={15} /></button>
      </div>
      <div className="wl-corner">
        <Hud view={view} data={data} />
      </div>
      <StatsHud hive={hive} runs={runs} on={holo} />
      <div ref={card} className="wl-callout" style={{ display: picked ? "" : "none" }}>
        {picked && <Inspector key={picked.kind + ":" + picked.id} picked={picked} data={data} onClose={close} openDash={setDash} open={(to, full) => { if (full) window.dispatchEvent(new CustomEvent("superai:window", { detail: to })); else navigate(to); }} />}
      </div>
      {dash && <DashScreen id={dash} onClose={() => setDash("")} />}
      <Command view={view} onConversation={() => window.dispatchEvent(new CustomEvent("superai:window", { detail: "/" }))} onTyping={(kind, id) => engine.current?.hint(kind, id)} />
    </div>
  );
}

/** The hive's status, as one pane of light in the top-left corner: what the
 *  cluster spends and how many of its workers answer on the top line, and
 *  opened, its work, how fast tokens have gone lately, and each member's
 *  share. It stays up whether the other corners are shown or not. */
function Hud({ data }: { view: ViewKey; data: WorldData }) {
  const { t } = useI18n();
  // Folded to its top line unless opened; it remembers.
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
  const W = 260, H = 40;
  const line = pts.map((v, i) => `${(i / Math.max(1, pts.length - 1)) * W},${H - 2 - (v / max) * (H - 6)}`).join(" ");
  const members = [...data.meter.members].sort((a, b) => b.tokens - a.tokens).slice(0, 6);
  const topTokens = Math.max(1, ...members.map((m) => m.tokens));
  const short = (n: string) => n.replace(/^superai-worker-/, "w").replace(/^superai-/, "");
  return (
    <div className={`wl-holo tl wl-hive${open ? " open" : ""}`}>
      <button className="wh-hive-top" onClick={toggle} aria-expanded={open} title={t(open ? "Fold" : "Show the figures")}>
        <div className="wh-fig big main">
          <b><i className={tt.tokPerSec > 0.5 ? "wh-dot on" : "wh-dot"} />{fmt(tt.tokens)}</b>
          <span>{t("hive tokens")}</span>
        </div>
        <ChevronDownIcon size={14} className="wh-hive-chev" />
      </button>
      <div className="wh-row">
        <div className="wh-fig"><b>{Math.round(tt.tokPerSec)}</b><span>tok/s</span></div>
        <div className={`wh-fig${live < workers.length ? " bad" : ""}`}><b>{live}/{workers.length}</b><span>{t("workers")}</span></div>
        {needs > 0 && <div className="wh-fig bad"><b>{needs}</b><span>{t("need you")}</span></div>}
      </div>
      {open && (
        <>
          <div className="wh-grid">
            <div className="wh-fig"><b>{fmt(tt.calls)}</b><span>{t("tool calls")}</span></div>
            <div className="wh-fig"><b>{tt.rounds}</b><span>{t("rounds")}</span></div>
            <div className="wh-fig"><b>{run.length}</b><span>{t("orders out")}</span></div>
            <div className={`wh-fig${tt.fails ? " bad" : ""}`}><b>{tt.fails}</b><span>{t("failures")}</span></div>
          </div>
          <svg className="wh-spark" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none">
            {pts.length > 1 && <polyline points={`0,${H} ${line} ${W},${H}`} className="fill" />}
            {pts.length > 1 && <polyline points={line} className="stroke" />}
          </svg>
          <div className="wh-cap"><span>{pts.length > 1 ? t("tokens, lately") : t("tokens, lately — the line starts with the next work")}</span>{pts.length > 1 && <span>{t("peak")} {fmt(max)}</span>}</div>
          <ul className="wh-who">
            {members.map((m) => (
              <li key={m.name}>
                <i className={m.live ? "on" : ""} />
                <span>{short(m.name)}</span>
                <span className="bar"><span style={{ width: `${Math.max(2, (m.tokens / topTokens) * 100)}%` }} /></span>
                <b>{fmt(m.tokens)}</b>
              </li>
            ))}
          </ul>
        </>
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
