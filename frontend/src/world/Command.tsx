import { useEffect, useRef, useState } from "react";
import { ArrowUpIcon, ListIcon, MessageSquareIcon, PlusIcon, MinusIcon, ChevronDownIcon } from "lucide-react";
import { ChatHistory, HiveRetire, HiveSpawn, SendChat } from "../../wailsjs/go/app/App";
import { visibleAnswer } from "../lib/format";
import { Response } from "@/components/ai-elements/response";
import { onBubbleClick, say, sayDone } from "./bubbles";
import { focusInWorld } from "./bus";
import { EventsOn } from "../../wailsjs/runtime";
import { QUEEN_SESSION } from "../canvas/data";
import { useI18n } from "../lib/i18n";
import { toast } from "../lib/toasts";
import { useImeGuard } from "../lib/ime";
import type { ViewKey } from "../lib/types";


/** A district's own actions: the things a person does there that are not
 *  about one object. Most start as a sentence to the queen. */
const ACTIONS: Record<string, { label: string; say?: string; run?: () => Promise<unknown>; ok?: string; icon?: "plus" | "minus" }[]> = {
  hive: [
    { label: "Worker", icon: "plus", run: () => HiveSpawn(1), ok: "A worker is starting" },
    { label: "Worker", icon: "minus", run: () => HiveRetire(1, false), ok: "A worker is leaving" },
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
export default function Command({ view, panel, onPanel, onConversation, onTyping }: {
  view: ViewKey; panel: boolean; onPanel: () => void; onConversation: () => void;
  /** Called with the thing an @name points at, so the world can light it. */
  onTyping?: (kind: string, id: string) => void;
}) {
  const { t } = useI18n();
  const ime = useImeGuard();
  const [text, setText] = useState("");
  const [req, setReq] = useState("");
  const [step, setStep] = useState("");
  const [busy, setBusy] = useState("");
  // The conversation with the queen, the same thread the home screen shows:
  // its last turns stay above the line, and what is typed continues it.
  const [lines, setLines] = useState<{ who: "you" | "queen"; text: string }[]>([]);
  const [live, setLive] = useState("");
  const [open, setOpen] = useState(false);
  const raw = useRef("");
  const box = useRef<HTMLInputElement>(null);
  const end = useRef<HTMLDivElement>(null);
  const district = view === "chat" ? "home" : view === "mcp" ? "skills" : view;
  useEffect(() => {
    ChatHistory(QUEEN_SESSION).then((turns: any[]) => {
      setLines((turns ?? []).filter((x) => (x.role === "user" || x.role === "assistant") && x.kind !== "context" && x.content)
        .slice(-8).map((x) => ({ who: x.role === "user" ? "you" : "queen", text: String(x.content) })));
    }).catch(() => {});
  }, []);
  useEffect(() => { end.current?.scrollIntoView({ block: "end" }); }, [lines, live, step, open]);
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

  useEffect(() => {
    if (!req) return;
    const a = EventsOn("chat:event", (p: any) => {
      if (p?.requestId !== req) return;
      if (p.type === "tool_call" && p.tool) { setStep(p.tool); say("queen", p.tool, "think", 60); }
      else if (p.type === "partial" && p.content) { raw.current += p.content; const v = visibleAnswer(raw.current); setLive(v); setStep(""); if (v) say("queen", v, "say", 60); }
    });
    const finish = (text: string) => { sayDone("queen", text); setLines((l) => [...l, { who: "queen" as const, text }].slice(-12)); setLive(""); setStep(""); setReq(""); raw.current = ""; };
    const b = EventsOn("chat:done", (p: any) => { if (p?.requestId === req) finish(String(p.final ?? visibleAnswer(raw.current))); });
    const c = EventsOn("chat:error", (p: any) => { if (p?.requestId === req) finish(String(p.error ?? "")); });
    return () => { [a, b, c].forEach((f) => typeof f === "function" && f()); };
  }, [req]);

  const send = async () => {
    const s = text.trim();
    if (!s || req) return;
    setText("");
    say("queen", "", "think", 120);
    setLines((l) => [...l, { who: "you" as const, text: s }].slice(-12));
    setStep("");
    raw.current = "";
    try { setReq(await SendChat(QUEEN_SESSION, s, [])); }
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
      {open && (lines.length > 0 || req) && (
        <div className="wc-thread">
          <header>
            <span className="wc-q">Q</span><b>{t("The queen")}</b>
            <button title={t("Open the whole conversation")} onClick={onConversation}><MessageSquareIcon size={14} /></button>
            <button title={t("Fold")} onClick={() => setOpen(false)}><ChevronDownIcon size={15} /></button>
          </header>
          <div className="wc-lines">
            {lines.map((l, i) => (
              <div key={i} className={`wc-${l.who}`}>{l.who === "queen" ? <Response>{l.text}</Response> : l.text}</div>
            ))}
            {req && (
              <div className="wc-queen">
                {live ? <Response>{live}</Response> : <span className="wc-busy">{step ? t("Using {tool}…", { tool: step }) : t("Thinking…")}</span>}
              </div>
            )}
            <div ref={end} />
          </div>
        </div>
      )}
      <div className="wc-bar">
        <button className={`wc-act${panel ? " on" : ""}`} onClick={onPanel} title={t("Menus and lists, the way it was")}><ListIcon size={15} />{t("Menu")}</button>
        <span className="wc-sep" />
        {(ACTIONS[district] ?? []).map((a, i) => (
          <button key={i} className="wc-act" disabled={busy === a.label + i}
            onClick={() => {
              if (a.run) void run(a.label + i, a.run, a.ok ?? "Done");
              else { setText(t(a.say ?? "")); window.setTimeout(() => box.current?.focus(), 0); }
            }}>
            {a.icon === "minus" ? <MinusIcon size={14} /> : <PlusIcon size={14} />}{t(a.label)}
          </button>
        ))}
        <button className={`wc-act${open ? " on" : ""}`} title={t("The conversation")} onClick={() => setOpen((v) => !v)}><MessageSquareIcon size={14} /></button>
        <form className="wc-say" onSubmit={(e) => { e.preventDefault(); void send(); }}>
          <span className="wc-prompt">›</span>
          <input ref={box} value={text} onChange={(e) => setText(e.target.value)} 
            placeholder={t(lines.length ? "Reply to the queen… @ for a worker or an agent" : "Tell the queen, from anywhere. @ for a worker or an agent")}
            onCompositionStart={ime.handlers.onCompositionStart} onCompositionEnd={ime.handlers.onCompositionEnd}
            onKeyDown={(e) => { if (e.key === "Enter" && ime.composing(e)) e.preventDefault(); }} />
          <button type="submit" disabled={!text.trim() || !!req} title={t("Send")}><ArrowUpIcon size={16} /></button>
        </form>
      </div>
    </div>
  );
}
