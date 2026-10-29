import { useEffect, useRef, useState } from "react";
import { useStickToBottom } from "../lib/useStickToBottom";
import { SendIcon } from "lucide-react";
import { AskRemoteAgent, ChatHistory, CLIRunDetail, FollowUpCLIRun, HiveTaskDetail, SendChat, StandingReports, StartCLIRun, WakeStandingAgent } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { loadQueenSession, queenSession } from "../lib/queenConsole";
import { useI18n } from "../lib/i18n";
import { useImeGuard } from "../lib/ime";
import { visibleAnswer } from "../lib/format";
import { stepLabel } from "../lib/trace";
import { Response } from "@/components/ai-elements/response";
import { say, sayDone } from "./bubbles";
import { useRequestBrowser } from "../components/BrowserCard";
import { SentMessage } from "../components/SentFiles";

/** Who a robot is, for talking to it. */
export type Talker =
  | { kind: "queen" }
  | { kind: "worker"; name: string }
  | { kind: "agent"; name: string } // a named agent on a linked machine (openclaw)
  | { kind: "cli"; agent: string } // a coding CLI: "claude.mac", or "codex" on the core
  | { kind: "bee"; id: string };

/** `req` is the queen's request that wrote an answer, for the browser card under it. */
type Line = { who: "you" | "it"; text: string; bad?: boolean; req?: string; tools?: string[] };

// Each robot's conversation lasts as long as the app does, so closing its
// card and opening it again picks up where it was.
const threads = new Map<string, { lines: Line[]; run?: string }>();
/** Which robot speaks for a talker, in the world. */
const actorOf = (t: Talker) => t.kind === "queen" ? "queen" : t.kind === "worker" ? "worker:" + t.name : t.kind === "agent" ? "agent:" + t.name : t.kind === "cli" ? "cli:" + t.agent : "bee:" + t.id;
const keyOf = (t: Talker) => t.kind + ":" + ("name" in t ? t.name : "agent" in t ? t.agent : "id" in t ? t.id : "");

/**
 * A conversation with one robot, in its card. The queen answers in her own
 * thread (the same one the home screen shows); a worker or a named agent is
 * asked directly; a coding CLI gets a run, and what is said next follows it
 * up; a bee is woken with the message and its next report is its answer.
 */
/** How long the answer has been coming, once it has been a while: an agent on
 *  another machine runs its own model and tools, and can take minutes over a
 *  question the queen answers in seconds. Mounted with the busy line, so it
 *  counts from when the question went. */
function Waited({ remote }: { remote: boolean }) {
  const { t } = useI18n();
  const [from] = useState(() => Date.now());
  const [, tick] = useState(0);
  useEffect(() => {
    const i = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(i);
  }, []);
  const s = Math.floor((Date.now() - from) / 1000);
  if (s < 5) return null;
  const took = s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
  return (
    <span className="wt-waited">
      {took}
      {remote && s >= 30 && <span> — {t("an agent on another machine can take a few minutes")}</span>}
    </span>
  );
}

export default function Talk({ who, placeholder }: { who: Talker; placeholder: string }) {
  const { t } = useI18n();
  const ime = useImeGuard();
  const key = keyOf(who);
  const [lines, setLines] = useState<Line[]>(() => threads.get(key)?.lines ?? []);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState("");
  // The queen's answer so far, as it streams.
  const [streamed, setStreamed] = useState("");
  const raw = useRef("");
  const live = useRef("");
  // The same request, as state: the card for the turn in progress follows it.
  const [liveReq, setLiveReq] = useState("");
  // What this card just sent, until SendChat names its request.
  const mine = useRef("");
  const end = useRef<HTMLDivElement>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);

  useEffect(() => { setLines(threads.get(key)?.lines ?? []); setBusy(""); setStreamed(""); raw.current = ""; }, [key]);
  // The queen's card opens on where the conversation with her is.
  useEffect(() => {
    if (who.kind !== "queen" || threads.get(key)?.lines.length) return;
    loadQueenSession().then((id) => ChatHistory(id)).then((turns: any[]) => {
      const last = (turns ?? []).filter((x) => (x.role === "user" || x.role === "assistant") && x.kind !== "context" && x.content).slice(-6);
      const th = { lines: last.map((x) => ({ who: x.role === "user" ? "you" : "it", text: String(x.content).slice(0, 4000) }) as Line) };
      threads.set(key, th);
      setLines(th.lines);
    }).catch(() => {});
  }, [key]);
  // The thread stays on its newest line; sending brings it back down.
  const thread = useStickToBottom<HTMLDivElement>();
  useEffect(() => {
    if (lines[lines.length - 1]?.who === "you") thread.follow();
  }, [lines]);

  const push = (l: Line) => {
    if (l.who === "it") sayDone(actorOf(who), l.text);
    const th = threads.get(key) ?? { lines: [] };
    if (!alive.current) { th.lines = [...th.lines, l]; threads.set(key, th); return; }
    th.lines = [...th.lines, l];
    threads.set(key, th);
    setLines(th.lines);
  };

  // The queen's answer streams in on the chat events of this request, word by
  // word. A turn another device takes in the same conversation shows here
  // too: its words when it starts, its answer when it ends.
  useEffect(() => {
    if (who.kind !== "queen") return;
    const others = new Set<string>();
    const settle = () => { live.current = ""; raw.current = ""; setStreamed(""); setBusy(""); setLiveReq(""); };
    const offs = [
      EventsOn("chat:turn", (p: any) => {
        if (!p?.requestId || p.sessionId !== queenSession() || p.requestId === live.current) return;
        // Ours, before SendChat answered with its id: its words stream from now.
        if (mine.current && mine.current === p.message) { mine.current = ""; live.current = p.requestId; setLiveReq(p.requestId); return; }
        others.add(p.requestId);
        push({ who: "you", text: String(p.message ?? "") });
      }),
      EventsOn("chat:done", (p: any) => { if (others.delete(p?.requestId)) push({ who: "it", text: String(p.final ?? ""), req: p.requestId }); }),
      EventsOn("chat:error", (p: any) => { if (others.delete(p?.requestId)) push({ who: "it", text: String(p.error ?? ""), bad: true }); }),
      EventsOn("chat:cancelled", (p: any) => { others.delete(p?.requestId); }),
      EventsOn("chat:event", (p: any) => {
        if (!live.current || p?.requestId !== live.current) return;
        if (p.type === "tool_call" && p.tool) setBusy(t("Using {tool}…", { tool: stepLabel(p.tool, p.args) }));
        else if (p.type === "tool_result") setBusy(t("Thinking…"));
        else if (p.type === "partial" && p.content) {
          raw.current += p.content;
          const v = visibleAnswer(raw.current);
          setStreamed(v);
          setBusy(t("Thinking…"));
          if (v) say("queen", v, "say", 60);
        }
        // A draft the output lint turned down, or a retried attempt: the next
        // answer is written from scratch.
        else if (p.type === "tombstone" && p.debugType === "retry") { raw.current = ""; setStreamed(""); }
      }),
      EventsOn("chat:done", (p: any) => {
        if (!live.current || p?.requestId !== live.current) return;
        const text = String(p.final ?? visibleAnswer(raw.current));
        settle();
        push({ who: "it", text, req: p.requestId });
      }),
      EventsOn("chat:error", (p: any) => { if (live.current && p?.requestId === live.current) { settle(); push({ who: "it", text: String(p.error ?? ""), bad: true, req: p.requestId }); } }),
      EventsOn("chat:cancelled", (p: any) => { if (live.current && p?.requestId === live.current) settle(); }),
    ];
    return () => offs.forEach((f) => typeof f === "function" && f());
  }, [key]);

  // A coding run answers when it ends. The end can arrive before StartCLIRun
  // or FollowUpCLIRun has returned the run's id — a short answer often does —
  // so ends that match no run yet are kept until the id is known.
  const ended = useRef(new Map<string, any>());
  const answered = useRef(new Set<string>());
  const finish = (r: any) => {
    if (answered.current.has(r.id)) return;
    answered.current.add(r.id);
    setBusy("");
    push({ who: "it", text: String(r.summary || r.error || r.state), bad: r.state !== "done" });
  };
  useEffect(() => {
    if (who.kind !== "cli") return;
    return EventsOn("cli:run", (r: any) => {
      if (!r?.id || r.state === "running") return;
      const th = threads.get(key);
      if (th?.run === r.id) finish(r);
      else ended.current.set(r.id, r);
    }) as unknown as () => void;
  }, [key]);

  const send = async () => {
    const s = text.trim();
    if (!s || busy) return;
    setText("");
    push({ who: "you", text: s });
    setBusy(t("Thinking…"));
    say(actorOf(who), "", "think", 600);
    try {
      switch (who.kind) {
        case "queen":
          mine.current = s;
          raw.current = "";
          setStreamed("");
          live.current = await SendChat(queenSession(), s, []);
          setLiveReq(live.current);
          mine.current = "";
          break;
        case "worker": case "agent": {
          // AskRemoteAgent starts an independent remote request each time. Carry
          // this thread with it so a follow-up has the same conversational context.
          const history = lines.filter((l) => !l.bad).map((l) => `${l.who === "you" ? "User" : "Assistant"}: ${l.text}`).join("\n\n");
          const prompt = history ? `Previous conversation:\n${history}\n\nUser: ${s}\n\nContinue the conversation and answer the latest user message.` : s;
          const r: any = await AskRemoteAgent(who.name, prompt);
          const detail: any = r.task_id ? await HiveTaskDetail(r.task_id).catch(() => null) : null;
          const tools = (detail?.task?.events ?? []).filter((e: any) => e.phase === "tool").map((e: any) => String(e.tool || "tool"));
          setBusy("");
          push({ who: "it", text: r.failed ? String(r.reason || "failed") : String(r.text ?? ""), bad: !!r.failed, tools });
          break;
        }
        case "cli": {
          const th = threads.get(key) ?? { lines: [] };
          const run: any = th.run ? await FollowUpCLIRun(th.run, s) : await StartCLIRun(who.agent, s, "", "", true);
          th.run = String(run.id);
          threads.set(key, th);
          setBusy(t("Working…"));
          const early = ended.current.get(th.run);
          if (early) { ended.current.delete(th.run); finish(early); break; }
          // Or it ended with no event this view saw: ask once.
          if (run.state && run.state !== "running") { finish(run); break; }
          CLIRunDetail(th.run).then((d: any) => { if (d && d.state && d.state !== "running") finish(d); }).catch(() => {});
          break;
        }
        case "bee": {
          const since = Date.now();
          await WakeStandingAgent(who.id, s);
          setBusy(t("Woken; waiting for its report…"));
          // Its answer is its next report.
          for (let i = 0; i < 40; i++) {
            await new Promise((r) => setTimeout(r, 3000));
            const list: any[] = await StandingReports(who.id).catch(() => []);
            const fresh = list?.find((x) => new Date(x.at).getTime() >= since - 1000);
            if (fresh) { setBusy(""); push({ who: "it", text: String(fresh.message ?? "") }); return; }
          }
          setBusy("");
          push({ who: "it", text: t("It woke and had nothing to report."), bad: false });
          break;
        }
      }
    } catch (e) {
      setBusy("");
      live.current = "";
      setLiveReq("");
      push({ who: "it", text: String(e instanceof Error ? e.message : e), bad: true });
    }
  };

  // The inspector is a narrow column: the compact card, as under a bubble.
  const browser = useRequestBrowser([...lines.map((l) => l.req), liveReq || undefined], true);
  return (
    <div className="wt">
      {(lines.length > 0 || busy) && (
        <div className="wt-lines" ref={thread.ref} onScroll={thread.onScroll}>
          {lines.map((l, i) => [
            browser(l.req, false),
            l.who === "it" && !l.bad
              ? <div key={i} className="wt-line wt-it">{l.tools && l.tools.length > 0 && <div className="wt-tools">{l.tools.map((tool, n) => <div key={n}>⚙ {tool}</div>)}</div>}<Response>{l.text}</Response></div>
              : <div key={i} className={`wt-line wt-plain wt-${l.who}${l.bad ? " bad" : ""}`}>{l.who === "you" ? <SentMessage content={l.text} /> : l.text}</div>,
          ])}
          {liveReq && browser(liveReq, true)}
          {streamed && <div className="wt-line wt-it"><Response>{streamed}</Response></div>}
          {busy && (!streamed || busy !== t("Thinking…")) && <p className="wt-line wt-it wt-busy">{busy} <Waited remote={who.kind === "agent" || who.kind === "worker"} /></p>}
          <div ref={end} />
        </div>
      )}
      <form className="wi-say" onSubmit={(e) => { e.preventDefault(); void send(); }}>
        <input value={text} onChange={(e) => setText(e.target.value)} placeholder={t(placeholder)}
          onCompositionStart={ime.handlers.onCompositionStart} onCompositionEnd={ime.handlers.onCompositionEnd}
          onKeyDown={(e) => { if (e.key === "Enter" && ime.composing(e)) e.preventDefault(); }} autoFocus />
        <button type="submit" disabled={!text.trim() || !!busy} title={t("Send")}><SendIcon size={14} /></button>
      </form>
    </div>
  );
}
