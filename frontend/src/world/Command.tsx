import { useEffect, useMemo, useRef, useState } from "react";
import { useStickToBottom } from "../lib/useStickToBottom";
import { useAttachments } from "../lib/useAttachments";
import AttachmentChips from "../components/AttachmentChips";
import { ArrowUpIcon, MessageSquareIcon, PlusIcon, MinusIcon, ChevronDownIcon, PaperclipIcon } from "lucide-react";
import { ChatHistory, HiveRetire, HiveSpawn, SendChat } from "../../wailsjs/go/app/App";
import { earlyError, requestSetup, useReady } from "../lib/ready";
import { engineByline, visibleAnswer } from "../lib/format";
import { Response } from "@/components/ai-elements/response";
import { onBubbleClick, say, sayDone } from "./bubbles";
import { focusInWorld } from "./bus";
import { EventsOn } from "../../wailsjs/runtime";
import { loadQueenSession, queenSession } from "../lib/queenConsole";
import { useI18n } from "../lib/i18n";
import { queenRef, queenTitle, useQueenName } from "../lib/queenName";
import { toast } from "../lib/toasts";
import { useImeGuard } from "../lib/ime";
import type { ViewKey } from "../lib/types";
import PodConfirm, { type PodChange } from "../components/PodConfirm";
import { AgentMenu } from "../components/AgentMenu";
import { useAgentMentions } from "../lib/useAgentMentions";
import type { Host } from "../canvas/data";
import { mentionsOf } from "./mentions";


/** A district's own actions: the things a person does there that are not
 *  about one object. Most start as a sentence to the queen. */
const ACTIONS: Record<string, { label: string; say?: string; run?: () => Promise<unknown>; ok?: string; icon?: "plus" | "minus"; pod?: PodChange }[]> = {
  hive: [
    { label: "Worker", icon: "plus", run: () => HiveSpawn(1), ok: "A worker is starting", pod: "spawn" },
    { label: "Worker", icon: "minus", run: () => HiveRetire(1, false), ok: "A worker is leaving", pod: "retire" },
  ],
  agents: [{ label: "New bee", icon: "plus", say: "Make a bee that " }],
  coding: [{ label: "New run", icon: "plus", say: "@claude " }],
  records: [{ label: "Add to calendar", icon: "plus", say: "Put on my calendar: " }],
  dashboards: [{ label: "New dashboard", icon: "plus", say: "Make a dashboard of " }],
  skills: [{ label: "Add skill", icon: "plus", say: "Install the skill " }, { label: "Add MCP server", icon: "plus", say: "Add an MCP server for " }],
  knowledge: [{ label: "Remember", icon: "plus", say: "Remember that " }],
  tasks: [],
};

/**
 * The bar along the bottom of the world: the district's actions, a line to
 * the queen from anywhere, and her answer above it while it is fresh.
 */
/** A turn of ours the queen is still answering. */
type Turn = { id: string; live: string; step: string; wire: string };

export default function Command({ view, onConversation, onTyping, hosts }: {
  view: ViewKey; onConversation: () => void;
  /** The world's machines and what runs on them: whom @ offers. */
  hosts?: Host[] | null;
  /** Called with the thing an @name points at, so the world can light it. */
  onTyping?: (kind: string, id: string) => void;
}) {
  const { t } = useI18n();
  const queen = useQueenName();
  const ime = useImeGuard();
  const [text, setText] = useState("");
  const ready = useReady();
  // Every turn still being answered. A new one can be sent at any time; each
  // has its own words so far, tool and wire, and lands in the thread on its own.
  const [pending, setPending] = useState<Turn[]>([]);
  const [busy, setBusy] = useState("");
  // The conversation with the queen, the same thread the home screen shows:
  // its last turns stay above the line, and what is typed continues it.
  // by: who answered for the queen, when not SuperAI's own agent alone
  // (engineByline): the engine, or SuperAI in its place.
  const [lines, setLines] = useState<{ who: "you" | "queen"; text: string; by?: string }[]>([]);
  const [open, setOpen] = useState(false);
  // A pod about to come or go, waiting for the person's yes.
  const [pod, setPod] = useState<{ change: PodChange; go: () => void } | null>(null);
  const box = useRef<HTMLInputElement>(null);
  const end = useRef<HTMLDivElement>(null);
  const district = view === "chat" ? "home" : view === "mcp" ? "skills" : view;
  // @ offers every agent the queen can hand work to: the core's own list
  // (remote agents, signed-in CLIs) and every worker the world draws.
  const workers = useMemo(() => mentionsOf(hosts), [hosts]);
  const mentions = useAgentMentions(text, setText, workers);
  const type = (v: string, caret: number) => { setText(v); mentions.update(v, caret); };
  useEffect(() => {
    loadQueenSession().then((id) => ChatHistory(id)).then((turns: any[]) => {
      setLines((turns ?? []).filter((x) => (x.role === "user" || x.role === "assistant") && x.kind !== "context" && x.content)
        .slice(-8).map((x) => ({ who: x.role === "user" ? "you" : "queen", text: String(x.content),
          by: engineByline({ engine: x.engine, standInFor: x.stand_in_for }) || undefined })));
    }).catch(() => {});
  }, []);
  // The thread stays on its newest line; sending brings it back down.
  const thread = useStickToBottom<HTMLDivElement>();
  // Files and pictures: the paperclip, or pasted (⌘V). Drops stay with the
  // chat page, which has the runtime's one drop handler.
  const attach = useAttachments({ drop: false });
  useEffect(() => {
    if (lines[lines.length - 1]?.who === "you") thread.follow();
  }, [lines]);
  // A bubble clicked: the queen's opens the conversation; anyone else's
  // selects them, and their card holds the talk.
  useEffect(() => {
    onBubbleClick((key) => {
      if (key === "queen") { setOpen(true); return; }
      const [kind, ...rest] = key.split(":");
      focusInWorld(kind === "cli" ? "cli" : kind, rest.join(":"));
    });
  }, []);
  // Typing @w1 or @codex.mac lights that one in the world.
  useEffect(() => {
    const m = /@([\w.-]+)\s*$/.exec(text) ?? /@([\w.-]+)/.exec(text);
    const name = m?.[1] ?? "";
    if (!name) { onTyping?.("", ""); return; }
    const worker = /^w\d+$/.test(name) ? "superai-worker-" + name.slice(1) : name;
    if (worker.startsWith("superai-worker-")) onTyping?.("worker", worker);
    else if (name.includes(".")) onTyping?.("machine", name.split(".").slice(1).join("."));
    else onTyping?.("machine", name);
  }, [text]);

  // A turn another device takes in the same conversation: its words when it
  // starts, its answer when it ends.
  const mineIds = useRef(new Set<string>());
  const mine = useRef("");
  useEffect(() => {
    const others = new Set<string>();
    const offs = [
      EventsOn("chat:turn", (p: any) => {
        if (!p?.requestId || p.sessionId !== queenSession() || mineIds.current.has(p.requestId)) return;
        if (mine.current && mine.current === p.message) { mine.current = ""; return; }
        others.add(p.requestId);
        setLines((l) => [...l, { who: "you" as const, text: String(p.message ?? "") }].slice(-12));
      }),
      EventsOn("chat:done", (p: any) => {
        if (others.delete(p?.requestId)) setLines((l) => [...l, { who: "queen" as const, text: String(p.final ?? ""), by: engineByline(p) || undefined }].slice(-12));
      }),
      EventsOn("chat:error", (p: any) => { others.delete(p?.requestId); }),
      EventsOn("chat:cancelled", (p: any) => { others.delete(p?.requestId); }),
    ];
    return () => offs.forEach((f) => typeof f === "function" && f());
  }, []);

  // Our own turns: their words as they stream, the tool in use, and what the
  // model call is stuck on when it is (a queue, a retry, a quiet provider).
  const raw = useRef(new Map<string, string>());
  const who = useRef(new Map<string, { engine?: string; standInFor?: string }>());
  const patch = (id: string, p: Partial<Turn>) => setPending((l) => l.map((x) => x.id === id ? { ...x, ...p } : x));
  useEffect(() => {
    const ours = (p: any) => !!p?.requestId && mineIds.current.has(p.requestId);
    const credit = (p: any) => {
      const w = who.current.get(p.requestId) ?? {};
      if (p.standInFor) who.current.set(p.requestId, { ...w, engine: undefined, standInFor: p.standInFor });
      else if (p.engine) who.current.set(p.requestId, { ...w, engine: p.engine, standInFor: undefined });
    };
    const a = EventsOn("chat:event", (p: any) => {
      if (!ours(p)) return;
      const id = p.requestId;
      credit(p);
      // Work the queen handed out is drawn on the worker doing it.
      if (p.type === "tool_call" && p.tool && p.worker) { patch(id, { step: p.tool }); say("worker:" + p.worker, p.workerTool || p.tool, "think", 60); }
      else if (p.type === "tool_call" && p.tool) { patch(id, { step: p.tool }); say("queen", p.tool, "think", 60); }
      else if (p.type === "model_wire") { const k = p.wire?.kind; if (["queued", "retry", "restart", "waiting", "error"].includes(k)) patch(id, { wire: String(p.content ?? "") }); else if (k === "first_byte" || k === "slot" || k === "request" || (k === "response" && p.wire?.status < 400)) patch(id, { wire: "" }); }
      else if (p.type === "partial" && p.content) { const r = (raw.current.get(id) ?? "") + p.content; raw.current.set(id, r); const v = visibleAnswer(r); patch(id, { live: v, step: "", wire: "" }); if (v) say("queen", v, "say", 60); }
    });
    const finish = (id: string, text: string) => {
      const by = engineByline(who.current.get(id) ?? {}) || undefined;
      mineIds.current.delete(id); raw.current.delete(id); who.current.delete(id);
      sayDone("queen", text);
      setLines((l) => [...l, { who: "queen" as const, text, by }].slice(-12));
      setPending((l) => l.filter((x) => x.id !== id));
    };
    const b = EventsOn("chat:done", (p: any) => { if (!ours(p)) return; credit(p); finish(p.requestId, String(p.final ?? visibleAnswer(raw.current.get(p.requestId) ?? ""))); });
    const c = EventsOn("chat:error", (p: any) => { if (ours(p)) finish(p.requestId, String(p.error ?? "")); });
    const d = EventsOn("chat:cancelled", (p: any) => { if (ours(p)) finish(p.requestId, visibleAnswer(raw.current.get(p.requestId) ?? "")); });
    return () => { [a, b, c, d].forEach((f) => typeof f === "function" && f()); };
  }, []);

  const send = async () => {
    const typed = text.trim();
    if (!typed && attach.paths.length === 0) return;
    const { payload: s, images } = attach.build(typed);
    // Nothing to think with: say so and open the setup, keeping the words.
    if (ready === false) { requestSetup(); return; }
    setText("");
    mentions.close();
    say("queen", "", "think", 120);
    attach.clear();
    setLines((l) => [...l, { who: "you" as const, text: typed || s }].slice(-12));
    mine.current = s;
    try {
      const id = await SendChat(queenSession(), s, images);
      mine.current = "";
      // Failed before its id came back: no event will follow.
      const failed = earlyError(id);
      if (failed !== undefined) {
        sayDone("queen", failed);
        setLines((l) => [...l, { who: "queen" as const, text: failed }].slice(-12));
        return;
      }
      mineIds.current.add(id);
      setPending((l) => [...l, { id, live: "", step: "", wire: "" }]);
    }
    catch (e) { toast.error(String(e instanceof Error ? e.message : e)); }
  };
  const run = async (key: string, fn: () => Promise<unknown>, ok: string) => {
    setBusy(key);
    try {
      const r: any = await fn();
      if (r && r.ok === false) throw new Error(r.error || "failed");
      toast.success(t(ok));
    } catch (e) { toast.error(String(e instanceof Error ? e.message : e)); }
    finally { setBusy(""); }
  };

  return (
    <div className="wc">
      {open && (lines.length > 0 || pending.length > 0) && (
        <div className="wc-thread">
          <header>
            <span className="wc-q">Q</span><b>{queenTitle(queen)}</b>
            <button title={t("Open the whole conversation")} onClick={onConversation}><MessageSquareIcon size={14} /></button>
            <button title={t("Fold")} onClick={() => setOpen(false)}><ChevronDownIcon size={15} /></button>
          </header>
          <div className="wc-lines" ref={thread.ref} onScroll={thread.onScroll}>
            {lines.map((l, i) => (
              <div key={i} className={`wc-${l.who}`}>
                {l.who === "queen" && l.by && <span className="msg-byline-engine">{l.by}</span>}
                {l.who === "queen" ? <Response>{l.text}</Response> : l.text}
              </div>
            ))}
            {pending.map((p) => (
              <div key={p.id} className="wc-queen">
                {p.live ? <Response>{p.live}</Response> : <span className="wc-busy">{p.step ? t("Using {tool}…", { tool: p.step }) : t("Thinking…")}{p.wire && <span className="wc-wire" title={p.wire}> · {p.wire}</span>}</span>}
              </div>
            ))}
            <div ref={end} />
          </div>
        </div>
      )}
      <PodConfirm change={pod?.change ?? null} onAnswer={(yes) => { const p = pod; setPod(null); if (yes) p?.go(); }} />
      <div className="wc-bar">
        {(ACTIONS[district] ?? []).map((a, i) => (
          <button key={i} className="wc-act" disabled={busy === a.label + i}
            onClick={() => {
              const go = () => { if (a.run) void run(a.label + i, a.run, a.ok ?? "Done"); };
              if (a.run && a.pod) setPod({ change: a.pod, go });
              else if (a.run) go();
              else { const v = t(a.say ?? ""); type(v, v.length); window.setTimeout(() => box.current?.focus(), 0); }
            }}>
            {a.icon === "minus" ? <MinusIcon size={14} /> : <PlusIcon size={14} />}{t(a.label)}
          </button>
        ))}
        <button className={`wc-act${open ? " on" : ""}`} title={t("The conversation")} onClick={() => setOpen((v) => !v)}><MessageSquareIcon size={14} /></button>
        <form className="wc-say" onSubmit={(e) => { e.preventDefault(); void send(); }}>
          {attach.paths.length > 0 && <div className="wc-files"><AttachmentChips paths={attach.paths} onRemove={attach.remove} /></div>}
          <AgentMenu
            matches={mentions.matches}
            active={mentions.active}
            onPick={(a) => {
              const next = mentions.accept(a);
              if (next) requestAnimationFrame(() => { box.current?.focus(); box.current?.setSelectionRange(next.caret, next.caret); });
            }}
          />
          <span className="wc-prompt">›</span>
          <input ref={box} value={text} onChange={(e) => type(e.target.value, e.target.selectionStart ?? e.target.value.length)}
            onBlur={mentions.close}
            onPaste={attach.paste}
            placeholder={t(lines.length ? "Reply to {name}… @ for a worker or an agent" : "Tell {name}, from anywhere. @ for a worker or an agent", { name: queenRef(queen) })}
            onCompositionStart={ime.handlers.onCompositionStart} onCompositionEnd={ime.handlers.onCompositionEnd}
            onKeyDown={(e) => {
              if (ime.composing(e)) { if (e.key === "Enter") e.preventDefault(); return; }
              // Picking a name with Enter must never also send.
              mentions.onKeyDown(e);
            }} />
          <button type="button" className="wc-clip" onClick={attach.pick} disabled={attach.importing} title={t("Attach")} data-testid="wc-attach"><PaperclipIcon size={15} /></button>
          <button type="submit" disabled={!text.trim() && attach.paths.length === 0} title={t("Send")}><ArrowUpIcon size={16} /></button>
        </form>
      </div>
    </div>
  );
}
