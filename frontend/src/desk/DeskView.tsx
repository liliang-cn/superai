import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowUpIcon, HexagonIcon, 
  PlusIcon, SquareIcon, SquarePenIcon, TerminalIcon, TriangleAlertIcon, XIcon, ChevronRightIcon,
  LayoutDashboardIcon, PanelRightCloseIcon, PanelRightOpenIcon, ActivityIcon, GaugeIcon,
} from "lucide-react";
import { useChat } from "../lib/useChat";
import { AgentInfo, useAgentMentions } from "../lib/useAgentMentions";
import { AgentMenu } from "../components/AgentMenu";
import { AttentionItem, AttentionList, useAttention } from "../canvas/attention";
import { taskPath } from "../lib/routes";
import type { ToolApproval } from "../lib/useToolApprovals";
import { PATHS } from "../lib/routes";
import { withoutCallNotes } from "../lib/format";
import { Response } from "@/components/ai-elements/response";
import { Bee, QUEEN_SESSION, elapsed, short, useBees, useLinkedAgents, useCodingRuns, useHive, useUpcoming } from "../canvas/data";
import type { CodingRun, UpcomingItem } from "../canvas/tiles";
import { dashboards, Dashboard } from "../lib/dashboards";
import {
  CancelAllChats, CancelCLIRun, ChatSessions, HiveLinkStatus, TakeOver,
} from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { openSwitcher } from "../lib/hivelink";
import LiveHive from "./LiveHive";
import LiveRun, { useRun } from "./LiveRun";
import LiveStatus from "./LiveStatus";
import LiveStats from "./LiveStats";
import { useDeskTheme } from "./DeskShell";

type TabKey = string; // "hive" | "run:<id>" | "dash:<id>"

const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);

function useNow(ms = 1000) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = window.setInterval(() => setNow(new Date()), ms);
    return () => window.clearInterval(t);
  }, [ms]);
  return now;
}

interface Session { id: string; title: string; updated_at: string }

/** The conversations, newest first, without the ones the screens keep for
 *  themselves: the queen's own, the tiles', and the bees' wake-ups (whose
 *  first message is the standing-agent preamble, not something you said).
 *  The phone's calls with the queen (hive-console-<id>) are kept. */
function useRecents(sending: boolean): Session[] {
  const [list, setList] = useState<Session[]>([]);
  useEffect(() => {
    if (sending) return;
    ChatSessions()
      .then((l) => setList(((l ?? []) as unknown as Session[])
        .filter((s) => s.id !== QUEEN_SESSION && !s.id.startsWith("tile-") && !s.title.startsWith("You hold a standing responsibility"))
        .map((s) => ({ ...s, title: withoutCallNotes(s.title) }))
        .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
        .slice(0, 9)))
      .catch(() => {});
  }, [sending]);
  return list;
}

function ago(iso: string, now: Date): string {
  const s = Math.max(0, (now.getTime() - new Date(iso).getTime()) / 1000);
  if (s < 90) return "now";
  if (s < 3600) return `${Math.round(s / 60)}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return new Date(iso).toLocaleDateString([], { weekday: "short" });
}

function nextLabel(items: UpcomingItem[]): string {
  const n = items.find((i) => i.enabled && i.next);
  if (!n?.next) return "";
  const d = new Date(n.next);
  const t = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  return d.toDateString() === new Date().toDateString() ? t : `${d.toLocaleDateString([], { weekday: "short" })} ${t}`;
}

/** Where this window is looking: the hive it is linked to, or this Mac. */
function useLink() {
  const [link, setLink] = useState<{ linked: boolean; url?: string; live: boolean }>({ linked: served, url: served ? location.host : undefined, live: served });
  useEffect(() => {
    if (served) return;
    const load = () => HiveLinkStatus().then((s) => setLink(s as any)).catch(() => {});
    load();
    const off = EventsOn("hivelink:state", load);
    return () => { if (typeof off === "function") off(); };
  }, []);
  return link;
}


/** The tabs the right pane always has. */
const FIXED: TabKey[] = ["hive", "status", "stats"];
const FOLD_KEY = "superai-desk-live-folded";

const PIN_COLORS = ["dk-a-later", "dk-a-hive", "dk-a-code", "dk-a-bees", "dk-a-know", "dk-a-stats", "dk-a-rec"];

/**
 * The desktop home. Left, where things are; middle, the conversation with the
 * queen, with whatever needs you in it; right, what the hive is doing, live —
 * the comb, or a coding agent's session as it types — with a way to take
 * over; along the bottom, what you pinned.
 */
export default function DeskView({ approvals, openSession, onSessionOpened }: {
  approvals: { pending: ToolApproval[]; resolve: (id: string, allow: boolean) => void };
  /** A conversation to open on arrival — a notification's, a scheduled run's. */
  openSession?: string;
  onSessionOpened?: () => void;
}) {
  const navigate = useNavigate();
  const now = useNow();
  const hive = useHive();
  const bees = useBees();
  const upcoming = useUpcoming();
  const runs = useCodingRuns();
  const linked = useLinkedAgents();
  const chat = useChat();
  const recents = useRecents(chat.sending);
  const link = useLink();
  const attention = useAttention();
  const [draft, setDraft] = useState("");
  const composerRef = useRef<HTMLTextAreaElement>(null);
  // @ offers the agents the core knows (hermes, pi, claude.mac, …) and the
  // hive's workers, found by their short names too; a worker's message goes
  // to the queen, who hands it on.
  const workerNames = useMemo<AgentInfo[]>(() => hive.members
    .filter((m) => !/queen/.test(m.name))
    .map((m) => {
      const t = hive.tasks.find((x) => x.worker === m.name && x.state === "running");
      return { name: m.name, alias: short(m.name), about: `Hive worker ${short(m.name)}${m.state === "lost" ? ", not answering" : t ? ", busy" : ", idle"}` };
    }), [hive]);
  const mentions = useAgentMentions(draft, setDraft, workerNames);
  const { theme } = useDeskTheme();
  const [pins, setPins] = useState<Dashboard[]>([]);
  const [onQueen, setOnQueen] = useState(true);
  const [opened, setOpened] = useState<TabKey[]>([]);
  const [closed, setClosed] = useState<Set<TabKey>>(new Set());
  const [active, setActive] = useState<TabKey>("hive");
  const needsRef = useRef<HTMLDivElement>(null);
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!openSession) chat.loadSession(QUEEN_SESSION).catch(() => {});
    dashboards.list().then((l) => setPins(l ?? [])).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!openSession) return;
    setOnQueen(openSession === QUEEN_SESSION);
    chat.loadSession(openSession).catch(() => {});
    onSessionOpened?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openSession]);

  const msgs = chat.messages.filter((m) => m.kind !== "context");
  const lastText = msgs.length ? msgs[msgs.length - 1].content : "";
  useEffect(() => { endRef.current?.scrollIntoView({ block: "end" }); }, [msgs.length, lastText.length]);

  const waiting: Bee[] = bees.filter((b) => b.waitingFor && !b.paused);
  // Approvals and waiting bees are answered in the conversation (the cards);
  // the rest of what needs you, and what is coming up, sits in the sidebar.
  const needs = attention.filter((i) => i.level === "needs").length;
  const feed = attention.filter((i) => i.kind !== "approval" && i.kind !== "bee");
  const openItem = (it: AttentionItem) => {
    if (it.open === "hive" && it.kind === "failed" && it.ref) navigate(taskPath(it.ref));
    else if (it.open === "hive") navigate(PATHS.hive);
    else if (it.open === "coding") navigate(PATHS.coding);
    else if (it.open === "records") navigate(PATHS.records);
    else if (it.open === "agents") navigate(PATHS.agents);
  };

  // The tabs: the hive, its status and the figures always; every coding run
  // that is going; and whatever was opened.
  const liveRuns = runs.filter((r) => r.state === "running");
  const tabs: TabKey[] = useMemo(() => {
    const out: TabKey[] = [...FIXED];
    liveRuns.forEach((r) => { const k = "run:" + r.id; if (!closed.has(k)) out.push(k); });
    opened.forEach((k) => { if (!out.includes(k)) out.push(k); });
    return out;
  }, [liveRuns.map((r) => r.id).join(), opened, closed]);
  const current = tabs.includes(active) ? active : "hive";
  const open = (k: TabKey) => {
    setClosed((c) => { const n = new Set(c); n.delete(k); return n; });
    setOpened((o) => (o.includes(k) ? o : [...o, k]));
    setActive(k);
  };
  const close = (k: TabKey) => {
    setOpened((o) => o.filter((x) => x !== k));
    setClosed((c) => new Set(c).add(k));
    if (current === k) setActive("hive");
  };
  // The right pane folds away to a strip, and stays the way it was left.
  const [folded, setFolded] = useState(() => { try { return localStorage.getItem(FOLD_KEY) === "1"; } catch { return false; } });
  const fold = (on: boolean) => { setFolded(on); try { localStorage.setItem(FOLD_KEY, on ? "1" : "0"); } catch { /* fine */ } };

  const runOf = (k: TabKey) => runs.find((r) => "run:" + r.id === k);
  const dashOf = (k: TabKey) => pins.find((d) => "dash:" + d.id === k);
  const tabLabel = (k: TabKey) => {
    if (k === "hive") return "The hive";
    if (k === "status") return "Status";
    if (k === "stats") return "Stats";
    const r = runOf(k);
    if (r) return runName(r);
    return dashOf(k)?.name ?? "…";
  };

  const send = () => {
    const t = draft.trim();
    if (!t) return;
    setDraft("");
    chat.send(t);
  };

  const pickSession = (id: string) => {
    setOnQueen(id === QUEEN_SESSION);
    chat.loadSession(id).catch(() => {});
  };

  const busyWorkers = new Set(hive.tasks.filter((t) => t.state === "running").map((t) => t.worker)).size;
  const live = hive.members.filter((m) => m.state === "live" && !/queen/.test(m.name)).length;
  const workers = hive.members.filter((m) => !/queen/.test(m.name)).length;
  const status = !hive.loaded ? "Finding the hive…" : !hive.role ? "This Mac on its own" : `${live} of ${workers} workers up, ${busyWorkers} busy`;
  const title = onQueen ? "The queen" : recents.find((s) => s.id === chat.sessionId)?.title || "Conversation";

  return (
    <div className={folded ? "dk folded" : "dk"}>
      <aside className="cv-glass dk-side">
        <div className="dk-ws"><b>SuperAI</b></div>
        {needs > 0 && (
          <button className="dk-row need" onClick={() => needsRef.current?.scrollIntoView({ behavior: "smooth", block: "center" })}>
            <TriangleAlertIcon size={17} />Needs you<span className="dk-n">{needs}</span>
          </button>
        )}
        <button className="dk-row" onClick={() => { chat.newSession(); setOnQueen(false); }}><SquarePenIcon size={17} />New chat</button>
        <button className={onQueen ? "dk-row on" : "dk-row"} onClick={() => pickSession(QUEEN_SESSION)}><span className="dk-qhex">Q</span>The queen</button>
        {feed.length > 0 && (
          <div className="dk-foryou">
            <AttentionList items={feed} onOpen={openItem} limit={5} />
          </div>
        )}
        <button className="dk-grp" onClick={() => navigate(PATHS.records)}>Later<ChevronRightIcon size={14} /><span className="dk-more">{nextLabel(upcoming)}</span></button>
        <button className="dk-grp" onClick={() => navigate(PATHS.agents)}>Bees<ChevronRightIcon size={14} /><span className="dk-more">{bees.length || ""}</span></button>
        {pins.length > 0 && <div className="dk-grp static">Pinned</div>}
        {pins.slice(0, 6).map((d) => (
          <button key={d.id} className={current === "dash:" + d.id ? "dk-rec on" : "dk-rec"} onClick={() => open("dash:" + d.id)}>
            <LayoutDashboardIcon size={14} /><span>{d.name}</span>
          </button>
        ))}
        {recents.length > 0 && <div className="dk-grp static">Recents</div>}
        <div className="dk-recents">
          {recents.map((s) => {
            const going = s.id === chat.sessionId && chat.sending;
            return (
              <button key={s.id} className={s.id === chat.sessionId && !onQueen ? "dk-rec on" : "dk-rec"} onClick={() => pickSession(s.id)}>
                <i className={going ? "dk-dot on" : "dk-dot"} /><span>{s.title || "Untitled"}</span><em>{ago(s.updated_at, now)}</em>
              </button>
            );
          })}
        </div>
        <div className="dk-foot">
          <button className="dk-link" onClick={() => openSwitcher()} title="Switch backend">
            <span className="dk-qhex small">{link.linked ? "Q" : "M"}</span>
            <span>
              {link.linked ? (link.url || "").replace(/^https?:\/\//, "") : "This Mac only"}
              <small><i className={link.live ? "dk-ok" : "dk-off"} />{link.linked ? (link.live ? "Linked" : "Reconnecting") : "Not in a hive"}</small>
            </span>
          </button>
        </div>
      </aside>

      <section className="cv-glass dk-chat">
        <header className="dk-chead">
          <span className="dk-qhex big">Q</span>
          <div><b>{title}</b><small><i className={hive.role ? "dk-ok" : "dk-off"} />{status}</small></div>
        </header>
        <div className="dk-msgs">
          {msgs.length === 0 && (
            <div className="dk-empty">Ask the queen anything, or give the hive an order: “check disk on every worker and tell me the fullest”.</div>
          )}
          {msgs.map((m) =>
            m.role === "user" ? (
              <div key={m.id} className="dk-u">{withoutCallNotes(m.content)}</div>
            ) : (
              <div key={m.id} className={m.error ? "dk-q bad" : "dk-q"}>
                {m.content ? <Response>{withoutCallNotes(m.content)}</Response>
                  : m.streaming ? <span className="dk-thinking">{m.progress?.[m.progress.length - 1]?.text || "Thinking…"}</span>
                  : m.cancelled ? <span className="dk-thinking">Stopped.</span> : null}
                {m.error && <div className="dk-err">{m.error}</div>}
              </div>
            ),
          )}
          <div ref={needsRef} className="dk-needs">
            {approvals.pending.map((a) => (
              <div key={a.id} className="dk-card">
                <div className="dk-card-t"><TriangleAlertIcon size={15} />Needs you</div>
                <p>{a.by ? `${a.by} wants` : "The hive wants"} to run <code>{a.command || a.tool}</code></p>
                <div className="dk-card-btns">
                  <button className="dk-btn pri" onClick={() => approvals.resolve(a.id, true)}>Allow once</button>
                  <button className="dk-btn" onClick={() => approvals.resolve(a.id, false)}>Deny</button>
                  {a.expiresAt && <small>denied on its own in {elapsed(new Date().toISOString(), new Date(a.expiresAt).getTime())}</small>}
                </div>
              </div>
            ))}
            {waiting.map((b) => (
              <div key={b.id} className="dk-card">
                <div className="dk-card-t"><TriangleAlertIcon size={15} />Needs you</div>
                <p><b>{b.name}</b> is waiting: {b.waitingFor}</p>
                <div className="dk-card-btns"><button className="dk-btn pri" onClick={() => navigate(PATHS.agents)}>Open Bees</button></div>
              </div>
            ))}
          </div>
          <div ref={endRef} />
        </div>
        <form className="dk-comp" onSubmit={(e) => { e.preventDefault(); send(); }}>
          <AgentMenu
            matches={mentions.matches}
            active={mentions.active}
            onPick={(a) => {
              const next = mentions.accept(a);
              if (next) requestAnimationFrame(() => { composerRef.current?.focus(); composerRef.current?.setSelectionRange(next.caret, next.caret); });
            }}
          />
          <textarea
            ref={composerRef}
            name="message" rows={1} value={draft} placeholder="Ask the queen, or give the hive an order. @ for an agent or a worker"
            onChange={(e) => { setDraft(e.target.value); mentions.update(e.target.value, e.target.selectionStart ?? e.target.value.length); }}
            onKeyDown={(e) => {
              if (mentions.onKeyDown(e)) return;
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(); }
            }}
            onBlur={mentions.close}
          />
          {chat.sending && !draft.trim() ? (
            <button type="button" className="dk-send stop" title="Stop" onClick={() => chat.cancel()}><SquareIcon size={14} fill="currentColor" /></button>
          ) : (
            <button type="submit" className="dk-send" title="Send" disabled={!draft.trim()}><ArrowUpIcon size={18} /></button>
          )}
        </form>
      </section>

      {folded ? (
        <section className="dk-live-strip">
          <button className="cv-glass dk-unfold" title="Show the hive" onClick={() => fold(false)}>
            <PanelRightOpenIcon size={18} />
            {busyWorkers > 0 && <i className="dk-livedot" />}
          </button>
        </section>
      ) : (
      <section className="dk-live">
        <div className="dk-tabs">
          {tabs.map((k) => {
            const r = runOf(k);
            const on = k === current;
            const liveDot = k === "hive" ? busyWorkers > 0 : r?.state === "running";
            return (
              <button key={k} className={on ? "dk-tab on" : "dk-tab"} onClick={() => setActive(k)}>
                {liveDot ? <i className="dk-livedot" /> : k.startsWith("run:") ? <TerminalIcon size={14} /> : k.startsWith("dash:") ? <LayoutDashboardIcon size={14} /> : k === "status" ? <ActivityIcon size={14} /> : k === "stats" ? <GaugeIcon size={14} /> : <HexagonIcon size={14} />}
                <span>{tabLabel(k)}</span>
                {!FIXED.includes(k) && <XIcon size={13} className="dk-tab-x" onClick={(e) => { e.stopPropagation(); close(k); }} />}
              </button>
            );
          })}
          <button className="dk-icon" title="Open a coding run" onClick={() => navigate(PATHS.coding)}><PlusIcon size={17} /></button>
          <button className="dk-icon dk-fold" title="Hide this pane" onClick={() => fold(true)}><PanelRightCloseIcon size={17} /></button>
        </div>
        <div className="cv-glass dk-screen">
          <div className="dk-in">
            {current === "hive" && <LiveHive hive={hive} runs={runs} agents={linked} now={now} />}
            {current === "status" && <LiveStatus hive={hive} now={now} agents={linked} runs={runs} />}
            {current === "stats" && <LiveStats hive={hive} runs={runs} />}
            {current.startsWith("run:") && <RunTab id={current.slice(4)} now={now} />}
            {current.startsWith("dash:") && (
              <div className="dk-dash">{dashOf(current) ? <Response>{dashOf(current)!.source}</Response> : "Gone."}</div>
            )}
          </div>
        </div>
        <Controls current={current} run={runOf(current)} dash={dashOf(current)} busy={busyWorkers} onOpenHive={() => navigate(PATHS.hive)} onOpenStats={() => navigate(PATHS.stats)} />
      </section>
      )}

    </div>
  );
}

function RunTab({ id, now }: { id: string; now: Date }) {
  const run = useRun(id);
  return <LiveRun run={run} now={now} />;
}

/** Under the screen: who is driving, and how to step in. */
function Controls({ current, run, dash, busy, onOpenHive, onOpenStats }: { current: TabKey; run?: CodingRun; dash?: Dashboard; busy: number; onOpenHive: () => void; onOpenStats: () => void }) {
  const [err, setErr] = useState("");
  useEffect(() => { setErr(""); }, [current]);
  const detail = useRunSession(run?.id);
  if (current === "stats") {
    return (
      <div className="dk-ctl">
        <button className="dk-btn light" onClick={onOpenStats}>Open Stats</button>
      </div>
    );
  }
  if (current === "hive" || current === "status") {
    return (
      <div className="dk-ctl">
        <span>{busy > 0 ? "The queen has the hive" : "The hive is resting"}</span>
        {busy > 0 && <button className="dk-btn ghost" onClick={() => CancelAllChats().catch((e) => setErr(String(e)))}>Stop all</button>}
        <button className="dk-btn light" onClick={onOpenHive}>Open the hive</button>
        {err && <em className="dk-ctl-err">{err}</em>}
      </div>
    );
  }
  if (run) {
    const going = run.state === "running";
    const where = !run.remote ? "this Mac" : /mac/i.test(run.remote) ? "the Mac" : run.remote;
    return (
      <div className="dk-ctl">
        <span>{going ? `${agentName(run.agent)} is working on ${where}` : `${agentName(run.agent)} ${run.state === "done" ? "finished" : run.state}`}</span>
        {going && <button className="dk-btn ghost" onClick={() => CancelCLIRun(run.id).catch((e) => setErr(String(e)))}>Stop</button>}
        {detail?.session && (
          <button className="dk-btn light" onClick={() => TakeOver(run.agent, detail.session!, detail.cwd).catch((e) => setErr(String(e?.message ?? e)))}>Take over</button>
        )}
        {err && <em className="dk-ctl-err">{err}</em>}
      </div>
    );
  }
  if (dash) {
    return (
      <div className="dk-ctl">
        <span>{dash.name}</span>
        <button className="dk-btn light" onClick={() => dashboards.refresh(dash.id).catch((e) => setErr(String(e)))}>Refresh</button>
        {err && <em className="dk-ctl-err">{err}</em>}
      </div>
    );
  }
  return <div className="dk-ctl" />;
}

/** "claude.mac": the agent at the machine, once — the agent's own name may
 *  already carry the machine. */
export const runName = (r: { agent: string; remote?: string }) => (r.agent.includes(".") || !r.remote ? r.agent : `${r.agent}.${r.remote}`);

const agentName = (a: string) => (a.startsWith("claude") ? "Claude Code" : a.startsWith("codex") ? "Codex" : a);

function useRunSession(id?: string) {
  const run = useRun(id ?? "");
  return id ? run : null;
}
