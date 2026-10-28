import { useEffect } from "react";
import { CheckIcon, GlobeIcon, XIcon } from "lucide-react";
import { BrowserCall, browserCallsOfSteps, browserCallsOfTrace, browserLabel, keepTurnFrame, openBrowserPanel, browserTurnOver, turnFrame, useBrowserStream, useBrowserTurns } from "../lib/browser";
import { browserBlocker } from "./BrowserPanel";
import { useI18n } from "../lib/i18n";
import "./browser.css";

const shortUrl = (u: string) => u.replace(/^https?:\/\/(www\.)?/, "").replace(/\/$/, "");

/**
 * A turn's work in the browser, as one card in the conversation: the page as
 * it is now (or as the turn left it), its address, and what the turn did
 * there, step by step. A click opens the large live view.
 *
 * `live` is whether this card should be watching the page right now: the
 * turn is running, or it is the newest browser turn on screen. Every live
 * card shares one small socket; an older card keeps the frame its turn ended on.
 */
export default function BrowserCard({ turnKey, calls, running, live, compact }: { turnKey: string; calls: BrowserCall[]; running: boolean; live: boolean; compact?: boolean }) {
  const { t } = useI18n();
  const s = useBrowserStream(4, live);
  // While live, remember what the page looked like, so the card still shows
  // where this turn left it once a later turn has moved the browser on.
  useEffect(() => { if (live) keepTurnFrame(turnKey, s); }, [live, turnKey, s.frame, s.url, s.title]);
  const kept = live ? null : turnFrame(turnKey);
  const frame = live ? s.frame : kept?.frame ?? "";
  const url = live ? s.url : kept?.url ?? "";
  const title = live ? s.title : kept?.title ?? "";
  const block = live ? browserBlocker(s) : { label: "", kind: "" };
  const failed = calls.filter((c) => c.status === "fail").length;
  const streaming = live && s.socket === "open" && !!s.frame;

  if (calls.length === 0) return null;
  return (
    <div className={`bw-card cr-scope${running ? " running" : ""}${compact ? " compact" : ""}`} role="button" tabIndex={0} data-testid="browser-card"
      // Its own click: a card inside something clickable (a speech bubble
      // opens the conversation) opens the browser instead.
      onClick={(e) => { e.stopPropagation(); openBrowserPanel(); }}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopPropagation(); openBrowserPanel(); } }}>
      <div className="bw-thumb">
        {frame ? <img src={frame} alt="" draggable={false} /> : <GlobeIcon size={22} strokeWidth={1.4} />}
        {streaming && <span className="bw-live">{t("Live")}</span>}
        {!frame && block.label && <span className={`bw-flag ${block.kind}`}>{t(block.label)}</span>}
      </div>
      <div className="bw-body">
        <div className="bw-head">
          <GlobeIcon size={13} />
          <b>{title || t("Browser")}</b>
          {failed > 0 && <span className="bw-bad">{failed} {t("failed")}</span>}
        </div>
        {url && shortUrl(url) !== title && <div className="bw-addr-line" title={url}>{shortUrl(url)}</div>}
        <ol className="bw-steps">
          {(compact ? calls.slice(-2) : calls).map((c) => (
            <li key={c.id} className={c.status}>
              <span className="bw-mark">{c.status === "running" ? <i className="bw-spin" /> : c.status === "ok" ? <CheckIcon size={11} strokeWidth={3} /> : <XIcon size={11} strokeWidth={3} />}</span>
              {browserLabel(c)}
            </li>
          ))}
        </ol>
      </div>
    </div>
  );
}

type Msg = { id: string; role: string; streaming?: boolean; progress?: { id: string; kind: string; text: string; tool?: string }[] };
type Traced = Parameters<typeof browserCallsOfTrace>[0];

// The calls as the trace had them, with their arguments and outcomes. useChat
// drops a finished turn's trace when the next one starts, and its saved steps
// only name the operations, so the richer list is kept here for the card.
const traced = new Map<string, BrowserCall[]>();

/** A message's browser calls: from the live trace when this window streamed it, else from its saved steps. */
export function browserCallsOfMessage(m: Msg, trace: Traced): BrowserCall[] {
  const live = browserCallsOfTrace(trace, m.id);
  if (live.length > 0) {
    traced.set(m.id, live);
    if (traced.size > 200) traced.delete(traced.keys().next().value as string);
    return live;
  }
  return traced.get(m.id) ?? browserCallsOfSteps(m.progress);
}

/** The newest message in a conversation that used the browser, whose card stays live. */
export function newestBrowserTurn(messages: Msg[], trace: Traced): string {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.role === "assistant" && browserCallsOfMessage(m, trace).length > 0) return m.id;
  }
  return "";
}

/** The card for one answer of a useChat conversation; nothing when it did not use the browser. */
export function ChatTurnBrowser({ m, trace, newest }: { m: Msg; trace: Traced; newest: string }) {
  const calls = browserCallsOfMessage(m, trace);
  if (calls.length === 0) return null;
  return <BrowserCard turnKey={m.id} calls={calls} running={!!m.streaming} live={!!m.streaming || m.id === newest} />;
}

/**
 * The cards for a thread kept as plain lines (the world's queen card and its
 * command line), whose turns are known by request id. Returns the card for a
 * line, given which request on screen is the newest that used the browser.
 */
export function useRequestBrowser(requests: (string | undefined)[], compact = false) {
  const callsOf = useBrowserTurns();
  let newest = "";
  for (let i = requests.length - 1; i >= 0 && !newest; i--) if (callsOf(requests[i]).length > 0) newest = requests[i]!;
  return (req: string | undefined, running: boolean) => {
    const calls = callsOf(req);
    if (!req || calls.length === 0) return null;
    return <BrowserCard key={`bw-${req}`} turnKey={req} calls={calls} running={running} live={running || req === newest} compact={compact} />;
  };
}

/**
 * The compact card under the queen's speech bubble in the world, for one
 * request: live while that turn runs, the frame it ended on after.
 */
export function BubbleBrowser({ req }: { req: string }) {
  const calls = useBrowserTurns()(req);
  const running = !browserTurnOver(req);
  if (calls.length === 0) return null;
  return <BrowserCard turnKey={req} calls={calls} running={running} live={running} compact />;
}
