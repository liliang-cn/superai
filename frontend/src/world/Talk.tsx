import { useEffect, useRef, useState } from "react";
import { SendIcon } from "lucide-react";
import { AskRemoteAgent, ChatHistory, FollowUpCLIRun, SendChat, StandingReports, StartCLIRun, WakeStandingAgent } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { QUEEN_SESSION } from "../canvas/data";
import { useI18n } from "../lib/i18n";
import { useImeGuard } from "../lib/ime";
import { say, sayDone } from "./bubbles";

/** Who a robot is, for talking to it. */
export type Talker =
  | { kind: "queen" }
  | { kind: "worker"; name: string }
  | { kind: "agent"; name: string } // a named agent on a linked machine (openclaw)
  | { kind: "cli"; agent: string } // a coding CLI: "claude.mac", or "codex" on the core
  | { kind: "bee"; id: string };

type Line = { who: "you" | "it"; text: string; bad?: boolean };

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
export default function Talk({ who, placeholder }: { who: Talker; placeholder: string }) {
  const { t } = useI18n();
  const ime = useImeGuard();
  const key = keyOf(who);
  const [lines, setLines] = useState<Line[]>(() => threads.get(key)?.lines ?? []);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState("");
  const live = useRef("");
  const end = useRef<HTMLDivElement>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);

  useEffect(() => { setLines(threads.get(key)?.lines ?? []); setBusy(""); }, [key]);
  // The queen's card opens on where the conversation with her is.
  useEffect(() => {
    if (who.kind !== "queen" || threads.get(key)?.lines.length) return;
    ChatHistory(QUEEN_SESSION).then((turns: any[]) => {
      const last = (turns ?? []).filter((x) => (x.role === "user" || x.role === "assistant") && x.kind !== "context" && x.content).slice(-6);
      const th = { lines: last.map((x) => ({ who: x.role === "user" ? "you" : "it", text: String(x.content).slice(0, 600) }) as Line) };
      threads.set(key, th);
      setLines(th.lines);
    }).catch(() => {});
  }, [key]);
  useEffect(() => { end.current?.scrollIntoView({ block: "end" }); }, [lines, busy]);

  const push = (l: Line) => {
    if (l.who === "it") sayDone(actorOf(who), l.text);
    const th = threads.get(key) ?? { lines: [] };
    if (!alive.current) { th.lines = [...th.lines, l].slice(-30); threads.set(key, th); return; }
    th.lines = [...th.lines, l].slice(-30);
    threads.set(key, th);
    setLines(th.lines);
  };

  // The queen's answer streams in on the chat events of this request.
  useEffect(() => {
    if (who.kind !== "queen") return;
    const offs = [
      EventsOn("chat:event", (p: any) => {
        if (!live.current || p?.requestId !== live.current) return;
        if (p.type === "tool_call" && p.tool) setBusy(t("Using {tool}…", { tool: p.tool }));
      }),
      EventsOn("chat:done", (p: any) => { if (live.current && p?.requestId === live.current) { live.current = ""; setBusy(""); push({ who: "it", text: String(p.final ?? "") }); } }),
      EventsOn("chat:error", (p: any) => { if (live.current && p?.requestId === live.current) { live.current = ""; setBusy(""); push({ who: "it", text: String(p.error ?? ""), bad: true }); } }),
    ];
    return () => offs.forEach((f) => typeof f === "function" && f());
  }, [key]);

  // A coding run answers when it ends.
  useEffect(() => {
    if (who.kind !== "cli") return;
    return EventsOn("cli:run", (r: any) => {
      const th = threads.get(key);
      if (!th?.run || r?.id !== th.run || r.state === "running") return;
      setBusy("");
      push({ who: "it", text: String(r.summary || r.error || r.state), bad: r.state !== "done" });
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
          live.current = await SendChat(QUEEN_SESSION, s, []);
          break;
        case "worker": case "agent": {
          const r: any = await AskRemoteAgent(who.name, s);
          setBusy("");
          push({ who: "it", text: r.failed ? String(r.reason || "failed") : String(r.text ?? ""), bad: !!r.failed });
          break;
        }
        case "cli": {
          const th = threads.get(key) ?? { lines: [] };
          const run: any = th.run ? await FollowUpCLIRun(th.run, s) : await StartCLIRun(who.agent, s, "", "", true);
          th.run = String(run.id);
          threads.set(key, th);
          setBusy(t("Working…"));
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
      push({ who: "it", text: String(e instanceof Error ? e.message : e), bad: true });
    }
  };

  return (
    <div className="wt">
      {(lines.length > 0 || busy) && (
        <div className="wt-lines">
          {lines.map((l, i) => <p key={i} className={`wt-${l.who}${l.bad ? " bad" : ""}`}>{l.text}</p>)}
          {busy && <p className="wt-it wt-busy">{busy}</p>}
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
