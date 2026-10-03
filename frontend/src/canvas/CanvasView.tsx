import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowUpIcon, MenuIcon, MinusIcon, Maximize2Icon, Minimize2Icon, PlusIcon, SquareIcon, RefreshCwIcon } from "lucide-react";
import { useChat } from "../lib/useChat";
import { usePulse } from "../components/Reactor";
import type { ToolApproval } from "../lib/useToolApprovals";
import { PATHS } from "../lib/routes";
import Sky, { Lane } from "./Sky";
import { Bees, Cell, Coding, CodingRun, InFlight, Later, Machines, NeedsYou, Queen, Today, UpcomingItem } from "./tiles";
import { CLIRuns, Upcoming } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { Hive, elapsed, isToday, oneLine, short, useBees, useHive } from "./data";
import { GenSpec, loadSpecs, makeTile, saveSpecs } from "./generated";
import { BASES, BaseName, COLORS, CanvasTheme, ColorName, DEFAULT_THEME, loadTheme, saveTheme, themeVars } from "./theme";
import "./canvas.css";

type Kind = "inFlight" | "machines" | "today" | "bees" | "later" | "coding" | "queen" | "generated";
interface Tile { id: string; kind: Kind; span: 2 | 3 | 4 | 6 }

const TITLES: Record<Kind, string> = {
  inFlight: "In flight", machines: "Machines", today: "Today", bees: "Bees", later: "Later", coding: "Coding runs", queen: "The queen", generated: "Made for you",
};

const LAYOUT_KEY = "superai-canvas-layout";
const DEFAULT: Tile[] = [
  { id: "t-inflight", kind: "inFlight", span: 4 },
  { id: "t-today", kind: "today", span: 2 },
  { id: "t-machines", kind: "machines", span: 2 },
  { id: "t-bees", kind: "bees", span: 4 },
  { id: "t-later", kind: "later", span: 3 },
  { id: "t-coding", kind: "coding", span: 3 },
  { id: "t-queen", kind: "queen", span: 6 },
];

function loadLayout(): Tile[] {
  try {
    const t = JSON.parse(localStorage.getItem(LAYOUT_KEY) || "null") as Tile[] | null;
    if (!Array.isArray(t) || !t.length) return DEFAULT;
    // Later and Coding runs came after the first layouts were saved: they are
    // offered once, before the queen, and stay gone if removed after that.
    if (!localStorage.getItem("superai-canvas-v2")) {
      localStorage.setItem("superai-canvas-v2", "1");
      const add = DEFAULT.filter((d) => (d.kind === "later" || d.kind === "coding") && !t.some((x) => x.kind === d.kind));
      const q = t.findIndex((x) => x.kind === "queen");
      return q < 0 ? [...t, ...add] : [...t.slice(0, q), ...add, ...t.slice(q)];
    }
    return t;
  } catch {
    return DEFAULT;
  }
}

/** Everything the hive will do on its own, kept current. */
function useUpcoming(): UpcomingItem[] {
  const [items, setItems] = useState<UpcomingItem[]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => Upcoming().then((l) => { if (alive) setItems((l ?? []) as unknown as UpcomingItem[]); }).catch(() => {});
    load();
    const t = window.setInterval(load, 60000);
    const offs = ["agent:update", "schedule:changed", "schedule:run"].map((n) => EventsOn(n, load));
    return () => { alive = false; window.clearInterval(t); offs.forEach((o) => typeof o === "function" && o()); };
  }, []);
  return items;
}

/** The coding agents' runs, newest first, kept current. */
function useCodingRuns(): CodingRun[] {
  const [runs, setRuns] = useState<CodingRun[]>([]);
  useEffect(() => {
    let alive = true;
    const load = () => CLIRuns().then((l) => {
      if (!alive) return;
      const list = ((l ?? []) as unknown as CodingRun[]).slice().sort((a, b) => new Date(b.started).getTime() - new Date(a.started).getTime());
      setRuns(list);
    }).catch(() => {});
    load();
    const off = EventsOn("cli:run", load);
    const t = window.setInterval(load, 30000);
    return () => { alive = false; window.clearInterval(t); if (typeof off === "function") off(); };
  }, []);
  return runs;
}

const QUEEN_SESSION = "hive-console-web";

/**
 * Home: one full screen, the AI's. What needs a person sits on top in honey
 * glass; underneath, tiles the person arranged and the hive keeps live; behind
 * it all, the hive's day as a time-lapse.
 */
export default function CanvasView({ approvals }: { approvals: { pending: ToolApproval[]; resolve: (id: string, allow: boolean) => void } }) {
  const navigate = useNavigate();
  const hive = useHive();
  const bees = useBees();
  const pulse = usePulse();
  const upcoming = useUpcoming();
  const codingRuns = useCodingRuns();
  const chat = useChat();
  const [tiles, setTiles] = useState<Tile[]>(loadLayout);
  const [specs, setSpecs] = useState<Record<string, GenSpec>>(loadSpecs);
  const [making, setMaking] = useState<Set<string>>(new Set());
  const [editing, setEditing] = useState(false);
  const [adding, setAdding] = useState(false);
  const [menu, setMenu] = useState(false);
  const [attention, setAttention] = useState(false);
  const [theming, setTheming] = useState(false);
  const [theme, setThemeState] = useState<CanvasTheme>(loadTheme);
  const setTheme = (t: CanvasTheme) => { setThemeState(t); saveTheme(t); };
  const [machine, setMachine] = useState<string | null>(null);
  const [replayAt, setReplayAt] = useState<Date | null>(null);
  const [order, setOrder] = useState("");
  const dragged = useRef<string | null>(null);
  const queenRef = useRef<HTMLDivElement>(null);

  // The orders given from this screen go to one conversation with the queen.
  useEffect(() => {
    chat.loadSession(QUEEN_SESSION).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    try { localStorage.setItem(LAYOUT_KEY, JSON.stringify(tiles)); } catch { /* fine */ }
  }, [tiles]);
  useEffect(() => saveSpecs(specs), [specs]);

  const snapshot = useCallback(() => hiveSnapshot(hive), [hive]);

  const refresh = useCallback(async (id: string) => {
    const spec = loadSpecs()[id] ?? specs[id];
    if (!spec) return;
    setMaking((m) => new Set(m).add(id));
    const made = await makeTile(spec.prompt, snapshot());
    setSpecs((s) => ({ ...s, [id]: { prompt: spec.prompt, ...made } }));
    setMaking((m) => { const n = new Set(m); n.delete(id); return n; });
  }, [specs, snapshot]);

  // Tiles cut short — the page closed while the hive was making them — are
  // asked for again.
  useEffect(() => {
    if (!hive.loaded) return;
    Object.entries(specs).forEach(([id, s]) => { if (!s.updated && !s.error && !making.has(id)) refresh(id); });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hive.loaded]);

  // When the queen answers, bring the answer into view.
  const wasSending = useRef(false);
  useEffect(() => {
    if (wasSending.current && !chat.sending) queenRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
    wasSending.current = chat.sending;
  }, [chat.sending]);

  const send = () => {
    const t = order.trim();
    if (!t) return;
    setOrder("");
    chat.send(t);
  };

  const lanes = useMemo<Lane[]>(() => {
    // The theme's colour leads; the others keep neighbouring trails apart.
    const palette: [number, number, number][] = [COLORS[theme.color].rgb, [255, 120, 90], [70, 180, 170], [60, 170, 100], [80, 150, 240]];
    if (!hive.role) return Array.from({ length: 6 }, (_, i) => ({ key: `idle${i}`, busy: 0.15, rgb: palette[i % 5] }));
    const busy = new Set(hive.tasks.filter((t) => t.state === "running").map((t) => t.worker));
    const out = hive.members.filter((m) => m.state === "live").map((m, i) => {
      let level = busy.has(m.name) ? 1 : 0.12;
      if (replayAt) level = Math.max(0.1, levelAt(replayAt, hive));
      return { key: m.name, busy: level, rgb: palette[i % 5] };
    });
    out.push({ key: "queen", busy: pulse.live ? 0.9 : 0.3, rgb: [60, 52, 40] });
    return out;
  }, [hive, pulse.live, replayAt, theme.color]);

  const running = hive.tasks.filter((t) => t.state === "running");
  const live = hive.members.filter((m) => m.state === "live").length;
  const status = !hive.loaded ? "Linking…" : !hive.role ? "Not in a hive" : `${live} of ${hive.members.length} up, ${new Set(running.map((t) => t.worker)).size} busy`;

  const move = (from: string, to: string) => {
    if (from === to) return;
    setTiles((ts) => {
      const a = ts.findIndex((t) => t.id === from), b = ts.findIndex((t) => t.id === to);
      if (a < 0 || b < 0) return ts;
      const next = [...ts];
      const [t] = next.splice(a, 1);
      next.splice(b, 0, t);
      return next;
    });
  };
  const nextSpan = (s: Tile["span"]): Tile["span"] => (s === 2 ? 3 : s === 3 ? 4 : s === 4 ? 6 : 2);

  const body = (t: Tile) => {
    switch (t.kind) {
      case "inFlight": return <InFlight hive={hive} open={setMachine} />;
      case "machines": return <Machines hive={hive} open={setMachine} />;
      case "today": return <Today hive={hive} pulse={pulse} />;
      case "bees": return <Bees bees={bees} open={() => navigate(PATHS.agents)} />;
      case "later": return <Later items={upcoming} open={(i) => navigate(i.kind === "bee" ? PATHS.agents : PATHS.records)} />;
      case "coding": return <Coding runs={codingRuns} open={() => navigate(PATHS.coding)} />;
      case "queen": return <div ref={queenRef}><Queen messages={chat.messages} /></div>;
      case "generated": return <Generated spec={specs[t.id]} busy={making.has(t.id)} onRefresh={() => refresh(t.id)} />;
    }
  };

  return (
    <div className="cv-root" style={themeVars(theme)}>
      <Sky at={replayAt ?? new Date()} lanes={lanes} base={theme.base} mode={theme.mode} />
      <div className="cv-scroll">
        <header className="cv-top">
          <div>
            <h1>Hive</h1>
            <p>{status}</p>
          </div>
          <button className={`cv-pill glassy${editing ? " on" : ""}`} onClick={() => setEditing((e) => !e)} data-testid="canvas-edit">{editing ? "Done" : "Edit"}</button>
          <div className="cv-menu-wrap">
            <button className="cv-pill glassy round" onClick={() => setMenu((m) => !m)} aria-label="Menu" data-testid="canvas-menu"><MenuIcon size={17} /></button>
            {menu && (
              <div className="cv-menu cv-glass" onMouseLeave={() => setMenu(false)}>
                {([["Conversations", PATHS.chat], ["Bees", PATHS.agents], ["Hive", PATHS.hive], ["Coding", PATHS.coding], ["Stats", PATHS.stats], ["Knowledge", PATHS.knowledge], ["Skills", PATHS.skills], ["MCP", PATHS.mcp], ["Records", PATHS.records], ["Settings", PATHS.settings]] as const).map(([label, path]) => (
                  <button key={label} onClick={() => navigate(path)}>{label}</button>
                ))}
                <button onClick={() => { setMenu(false); setAttention(true); }}>Attention</button>
                <button onClick={() => { setMenu(false); setTheming(true); }} data-testid="menu-theme">Theme</button>
              </div>
            )}
          </div>
        </header>

        <NeedsYou pending={approvals.pending} resolve={approvals.resolve} bees={bees} />

        <div className="cv-grid">
          {tiles.map((t) => (
            <section
              key={t.id}
              className={`cv-glass cv-tile span-${t.span}${editing ? " editing" : ""}`}
              draggable={editing}
              onDragStart={() => { dragged.current = t.id; }}
              onDragOver={(e) => { if (editing) e.preventDefault(); }}
              onDrop={() => { if (dragged.current) move(dragged.current, t.id); dragged.current = null; }}
            >
              {t.kind !== "generated" && <h2>{TITLES[t.kind]}</h2>}
              {editing && (
                <div className="cv-handles">
                  <button onClick={() => setTiles((ts) => ts.map((x) => (x.id === t.id ? { ...x, span: nextSpan(x.span) } : x)))} aria-label="Resize">
                    {t.span === 6 ? <Minimize2Icon size={12} /> : <Maximize2Icon size={12} />}
                  </button>
                  <button className="ink" onClick={() => setTiles((ts) => ts.filter((x) => x.id !== t.id))} aria-label="Remove"><MinusIcon size={13} /></button>
                </div>
              )}
              {body(t)}
            </section>
          ))}
          {editing && (
            <button className="cv-glass cv-add span-6" onClick={() => setAdding(true)} data-testid="add-tile"><PlusIcon size={16} /> Add a tile</button>
          )}
        </div>
      </div>

      <footer className="cv-bottom">
        <Replay hive={hive} at={replayAt} onScrub={setReplayAt} />
        <div className="cv-glass cv-say">
          <input
            value={order}
            onChange={(e) => setOrder(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing) { e.preventDefault(); send(); } }}
            placeholder="Ask, order, or change this screen"
            data-testid="canvas-say"
          />
          <button className="cv-go" onClick={() => (chat.sending ? chat.cancel() : send())} disabled={!chat.sending && !order.trim()} aria-label={chat.sending ? "Stop" : "Send"} data-testid="canvas-send">
            {chat.sending ? <SquareIcon size={14} fill="currentColor" /> : <ArrowUpIcon size={18} />}
          </button>
        </div>
      </footer>

      {adding && (
        <AddTile
          present={new Set(tiles.map((t) => t.kind))}
          onPick={(kind) => { setTiles((ts) => [...ts, { id: `t-${kind}-${Date.now()}`, kind, span: kind === "queen" ? 6 : kind === "today" || kind === "machines" ? 2 : kind === "later" || kind === "coding" ? 3 : 4 }]); setAdding(false); }}
          onDescribe={(prompt) => {
            const id = `g-${Date.now().toString(36)}`;
            setSpecs((s) => ({ ...s, [id]: { prompt } }));
            setTiles((ts) => [...ts, { id, kind: "generated", span: 3 }]);
            setAdding(false);
            setMaking((m) => new Set(m).add(id));
            makeTile(prompt, snapshot()).then((made) => {
              setSpecs((s) => ({ ...s, [id]: { prompt, ...made } }));
              setMaking((m) => { const n = new Set(m); n.delete(id); return n; });
            });
          }}
          onClose={() => setAdding(false)}
        />
      )}
      {machine && <MachineDrawer name={machine} hive={hive} onClose={() => setMachine(null)} />}
      {attention && <Attention onClose={() => setAttention(false)} />}
      {theming && <ThemePanel theme={theme} onChange={setTheme} onClose={() => setTheming(false)} />}
    </div>
  );
}

function Generated({ spec, busy, onRefresh }: { spec?: GenSpec; busy: boolean; onRefresh: () => void }) {
  const items = spec?.items ?? [];
  const top = Math.max(1, ...items.map((i) => Math.abs(i.value ?? 0)));
  return (
    <div className="cv-gen" data-testid="generated-tile">
      <h2>
        {spec?.title || spec?.prompt}
        <button onClick={onRefresh} aria-label="Refresh" className={busy ? "spin" : ""}><RefreshCwIcon size={12} /></button>
      </h2>
      {!spec?.updated && !spec?.error && <p className="cv-quiet">The hive is making this…</p>}
      {spec?.error && <p className="cv-quiet">{spec.error}</p>}
      {spec?.updated && spec.kind === "number" && (
        <div className="cv-fig big"><b>{spec.value ?? "—"}</b>{spec.caption && <small>{spec.caption}</small>}</div>
      )}
      {spec?.updated && spec.kind === "bars" && (
        <>
          <div className="cv-bars">
            {items.slice(0, 6).map((it, i) => (
              <div key={i}><b>{it.text ?? it.value}</b><i style={{ height: `${Math.max(4, (70 * Math.abs(it.value ?? 0)) / top)}px` }} /><small>{it.label}</small></div>
            ))}
          </div>
          {spec.caption && <p className="cv-quiet">{spec.caption}</p>}
        </>
      )}
      {spec?.updated && (spec.kind === "list" || !spec.kind) && (
        <div className="cv-list">
          {items.slice(0, 5).map((it, i) => <div key={i}><b>{it.label}</b>{it.text && <span>{it.text}</span>}</div>)}
          {spec.caption && <p className="cv-quiet">{spec.caption}</p>}
        </div>
      )}
      {spec?.updated && <p className="cv-made">Made by the hive, {new Date(spec.updated).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })}</p>}
    </div>
  );
}

function AddTile({ present, onPick, onDescribe, onClose }: { present: Set<Kind>; onPick: (k: Kind) => void; onDescribe: (p: string) => void; onClose: () => void }) {
  const [text, setText] = useState("");
  const kinds = (Object.keys(TITLES) as Kind[]).filter((k) => k !== "generated" && !present.has(k));
  return (
    <div className="cv-scrim" onClick={onClose}>
      <div className="cv-glass cv-sheet" onClick={(e) => e.stopPropagation()}>
        <h3>Add a tile</h3>
        <label>Describe one</label>
        <div className="cv-describe">
          <input autoFocus value={text} onChange={(e) => setText(e.target.value)} placeholder="Which workers were busiest this week"
            onKeyDown={(e) => { if (e.key === "Enter" && text.trim() && !e.nativeEvent.isComposing) onDescribe(text.trim()); }} data-testid="describe-tile" />
          <button className="cv-pill ink" disabled={!text.trim()} onClick={() => onDescribe(text.trim())} data-testid="make-tile">Make</button>
        </div>
        <label>Or pick one</label>
        <div className="cv-chips">
          {kinds.length === 0 && <p className="cv-quiet">Every built-in tile is already on your screen.</p>}
          {kinds.map((k) => <button key={k} className="cv-pill glassy" onClick={() => onPick(k)}>{TITLES[k]}</button>)}
        </div>
      </div>
    </div>
  );
}

function MachineDrawer({ name, hive, onClose }: { name: string; hive: Hive; onClose: () => void }) {
  const mine = hive.tasks.filter((t) => t.worker === name);
  const now = mine.find((t) => t.state === "running");
  const member = hive.members.find((m) => m.name === name);
  return (
    <div className="cv-scrim" onClick={onClose}>
      <aside className="cv-glass cv-drawer" onClick={(e) => e.stopPropagation()}>
        <div className="cv-drawer-head">
          <Cell label={short(name)} state={member?.state === "lost" ? "lost" : now ? "working" : "resting"} size={40} />
          <div><b>{name}</b><small>{member?.state === "lost" ? "Not answering" : now ? "Working" : "Resting"}</small></div>
        </div>
        {now && (
          <div className="cv-glass cv-now">
            <small>Now</small>
            <b>{oneLine(now.prompt)}</b>
            <span>{now.tool ? `Using ${now.tool}, ` : ""}{now.tools} tool calls, {elapsed(now.started_at)}</span>
          </div>
        )}
        <h4>Earlier</h4>
        {mine.filter((t) => t.state !== "running").slice(0, 15).map((t) => (
          <div key={t.id} className="cv-earlier">
            <span>{new Date(t.started_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} <em className={t.state}>{t.state}</em></span>
            <p>{oneLine(t.prompt)}</p>
            {t.result && <p className="cv-quiet">{oneLine(t.result).slice(0, 240)}</p>}
          </div>
        ))}
        {mine.length === 0 && <p className="cv-quiet">No orders on this machine yet.</p>}
      </aside>
    </div>
  );
}

/** The look, chosen like shadcn's theme panel: every pick applies at once,
 *  and the canvas behind the panel is the preview. */
function ThemePanel({ theme, onChange, onClose }: { theme: CanvasTheme; onChange: (t: CanvasTheme) => void; onClose: () => void }) {
  const set = <K extends keyof CanvasTheme>(k: K, v: CanvasTheme[K]) => onChange({ ...theme, [k]: v });
  return (
    <div className="cv-scrim" style={{ background: "transparent" }} onClick={onClose}>
      <div className="cv-glass cv-sheet" onClick={(e) => e.stopPropagation()} data-testid="theme-panel">
        <h3>Theme</h3>
        <div className="cv-theme-row">
          <label>Color</label>
          <div className="cv-theme-opts">
            {(Object.keys(COLORS) as ColorName[]).map((c) => (
              <button key={c} className={`cv-opt${theme.color === c ? " on" : ""}`} onClick={() => set("color", c)}>
                <i style={{ background: `rgb(${COLORS[c].rgb.join(",")})` }} />{COLORS[c].label}
              </button>
            ))}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>Background</label>
          <div className="cv-theme-opts">
            <button className={`cv-opt${theme.base === "timelapse" ? " on" : ""}`} onClick={() => set("base", "timelapse")}><i className="sky" />Time-lapse</button>
            {(Object.keys(BASES) as Exclude<BaseName, "timelapse">[]).map((b) => {
              const [top, bottom] = BASES[b][theme.mode];
              return (
                <button key={b} className={`cv-opt${theme.base === b ? " on" : ""}`} onClick={() => set("base", b)}>
                  <i style={{ background: `linear-gradient(rgb(${top}), rgb(${bottom}))`, boxShadow: "inset 0 0 0 1px rgba(0,0,0,.12)" }} />{BASES[b].label}
                </button>
              );
            })}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>Glass</label>
          <div className="cv-theme-opts">
            {(["clear", "frosted", "solid"] as const).map((g) => (
              <button key={g} className={`cv-opt${theme.glass === g ? " on" : ""}`} onClick={() => set("glass", g)}>{g[0].toUpperCase() + g.slice(1)}</button>
            ))}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>Radius</label>
          <div className="cv-theme-opts">
            {([0, 0.5, 0.75, 1, 1.25] as const).map((r) => (
              <button key={r} className={`cv-opt${theme.radius === r ? " on" : ""}`} onClick={() => set("radius", r)}>{r}</button>
            ))}
          </div>
        </div>
        <div className="cv-theme-row">
          <label>Mode</label>
          <div className="cv-theme-opts">
            {(["light", "dark"] as const).map((m) => (
              <button key={m} className={`cv-opt${theme.mode === m ? " on" : ""}`} onClick={() => set("mode", m)}>{m === "light" ? "Light" : "Dark"}</button>
            ))}
          </div>
        </div>
        <div className="cv-theme-foot">
          <button className="cv-pill" onClick={() => onChange(DEFAULT_THEME)}>Reset</button>
          <button className="cv-pill ink" onClick={onClose}>Done</button>
        </div>
      </div>
    </div>
  );
}

const RULES: [string, string][] = [
  ["attn.destructive", "Before anything is deleted, sent to someone or paid for"],
  ["attn.restart", "Before a machine is restarted"],
  ["attn.spend", "When a day's spend passes $5"],
  ["attn.failures", "When the same thing fails three times"],
  ["attn.pr", "Before code leaves your machines as a pull request"],
  ["attn.quiet", "Between 23:00 and 08:00, only if it can't wait"],
];

/** What is worth interrupting you for. For now kept in this browser only; the
 *  hive's approvals still follow the server's own settings. */
function Attention({ onClose }: { onClose: () => void }) {
  // On unless turned off, except quiet hours, which are off unless turned on.
  const read = (k: string) => {
    try {
      const v = localStorage.getItem(k);
      return v === null ? k !== "attn.quiet" : v === "1";
    } catch {
      return k !== "attn.quiet";
    }
  };
  const [on, setOn] = useState<Record<string, boolean>>(() => Object.fromEntries(RULES.map(([k]) => [k, read(k)])));
  const flip = (k: string) => setOn((o) => {
    const v = !o[k];
    try { localStorage.setItem(k, v ? "1" : "0"); } catch { /* fine */ }
    return { ...o, [k]: v };
  });
  return (
    <div className="cv-scrim" onClick={onClose}>
      <div className="cv-glass cv-sheet" onClick={(e) => e.stopPropagation()}>
        <h3>Attention</h3>
        <p className="cv-quiet">Everything else the hive decides by itself, and you can watch it on the screen.</p>
        {RULES.map(([k, label]) => (
          <div key={k} className="cv-rule">
            <span>{label}</span>
            <button className={`cv-toggle${on[k] ? " on" : ""}`} onClick={() => flip(k)} aria-pressed={on[k]} aria-label={label} />
          </div>
        ))}
        <div className="cv-sheet-foot"><button className="cv-pill ink" onClick={onClose}>Done</button></div>
      </div>
    </div>
  );
}

/** Today, as a strip to drag through: how much the hive did in each half hour,
 *  and the sky behind follows the hour under the pointer. */
function Replay({ hive, at, onScrub }: { hive: Hive; at: Date | null; onScrub: (d: Date | null) => void }) {
  const slots = 36;
  // The last eighteen hours: a strip that started at six this morning was
  // empty, and pointed into the future, from midnight until six.
  const now = new Date();
  const start = useMemo(() => new Date(Date.now() - 18 * 3600 * 1000), [Math.floor(now.getTime() / 60000)]);
  const span = Math.max(1, now.getTime() - start.getTime());
  const bins = useMemo(() => {
    const c = Array(slots).fill(0);
    hive.tasks.forEach((t) => {
      const i = Math.floor(((new Date(t.started_at).getTime() - start.getTime()) / span) * slots);
      if (i >= 0 && i < slots) c[i]++;
    });
    const top = Math.max(1, ...c);
    return c.map((v) => v / top);
  }, [hive.tasks, start, span]);
  const pos = Math.min(1, Math.max(0, ((at ?? now).getTime() - start.getTime()) / span));
  const scrub = (e: React.PointerEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const f = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
    onScrub(new Date(start.getTime() + span * f));
  };
  const release = () => window.setTimeout(() => onScrub(null), 2500);
  return (
    <div className="cv-glass cv-replay" data-testid="replay">
      <b>{(at ?? now).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })}</b>
      <div className="cv-track" onPointerDown={(e) => { e.currentTarget.setPointerCapture(e.pointerId); scrub(e); }} onPointerMove={(e) => { if (e.buttons) scrub(e); }} onPointerUp={release}>
        {bins.map((v, i) => <i key={i} className={i / slots <= pos ? "past" : ""} style={{ height: 4 + 18 * v }} />)}
        <span className="cv-knob" style={{ left: `calc(${pos * 100}% - 2px)` }} />
      </div>
      <small>{at ? "Replay" : "Last 18 h"}</small>
    </div>
  );
}

function levelAt(d: Date, hive: Hive): number {
  const n = hive.tasks.filter((t) => Math.abs(new Date(t.started_at).getTime() - d.getTime()) < 30 * 60 * 1000).length;
  return Math.min(1, n / 4);
}

/** The hive as a few lines of text, for the queen to fill a tile from. */
function hiveSnapshot(h: Hive): string {
  if (!h.role) return "Not in a hive.";
  const busy = new Set(h.tasks.filter((t) => t.state === "running").map((t) => t.worker));
  const lines = [`Now: ${new Date().toISOString()}`, "Members:"];
  h.members.forEach((m) => lines.push(`- ${m.name}: ${m.state === "live" ? (busy.has(m.name) ? "working" : "idle") : "not answering"}`));
  const today = h.tasks.filter((t) => isToday(t.started_at));
  lines.push(`Orders today (${today.length} known here, newest first):`);
  today.slice(0, 40).forEach((t) => lines.push(`- ${new Date(t.started_at).toTimeString().slice(0, 5)} ${t.worker} ${t.state}, ${t.tools} tool calls: ${oneLine(t.prompt).slice(0, 80)}`));
  return lines.join("\n");
}
