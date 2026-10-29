import { useEffect, useState } from "react";
import { ChevronLeftIcon } from "lucide-react";
import { Response } from "@/components/ai-elements/response";
import { EarlierChat, EarlierChats } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { SentMessage } from "../components/SentFiles";
import { useI18n } from "../lib/i18n";
import { withoutCallNotes } from "../lib/format";
import { withConsoleTitles } from "../lib/queenConsole";

/** How many conversations this Mac kept before it joined a hive; the window
 *  asks its own process, never the hive. */
export function useEarlierCount(joined: boolean): number {
  const [n, setN] = useState(0);
  useEffect(() => {
    if (!joined) { setN(0); return; }
    EarlierChats().then((l) => setN((l ?? []).length)).catch(() => setN(0));
  }, [joined]);
  return n;
}

/** This Mac's conversations from before it joined the hive, read-only: a
 *  list, then one conversation with no composer. */
export default function EarlierChatsPane({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const [list, setList] = useState<backend.ChatSessionInfo[] | null>(null);
  const [open, setOpen] = useState<backend.ChatSessionInfo | null>(null);
  const [turns, setTurns] = useState<backend.ChatTurn[] | null>(null);

  useEffect(() => {
    EarlierChats()
      .then((l) => withConsoleTitles(l ?? []))
      .then(setList)
      .catch(() => setList([]));
  }, []);
  useEffect(() => {
    if (!open) { setTurns(null); return; }
    EarlierChat(open.id).then((x) => setTurns(x ?? [])).catch(() => setTurns([]));
  }, [open]);

  const back = () => (open ? setOpen(null) : onClose());
  return (
    <section className="cv-glass dk-chat dk-earlier" data-testid="earlier-chats">
      <header className="dk-chead">
        <button className="dk-icon" title={t("Back")} aria-label={t("Back")} onClick={back} data-testid="earlier-back"><ChevronLeftIcon size={17} /></button>
        <div><b>{open ? open.title || t("Untitled") : t("Earlier chats on this Mac")}</b>
          <small>{open ? new Date(open.updated_at).toLocaleString() : t("Read only")}</small></div>
      </header>
      <div className="dk-msgs">
        {!open && list && list.length === 0 && <div className="dk-empty">{t("No earlier chats")}</div>}
        {!open && list?.map((s) => (
          <button key={s.id} className="dk-rec dk-hist dk-earlier-row" onClick={() => setOpen(s)} data-testid="earlier-row">
            <span>{s.title || t("Untitled")}</span>
            <em>{new Date(s.updated_at).toLocaleDateString()}</em>
          </button>
        ))}
        {open && turns && turns.length === 0 && <div className="dk-empty">{t("No earlier chats")}</div>}
        {open && turns?.filter((m) => m.kind !== "context").map((m, i) =>
          m.role === "user" ? (
            <div key={i} className="dk-u"><SentMessage content={withoutCallNotes(m.content)} /></div>
          ) : (
            <div key={i} className="dk-q"><Response>{withoutCallNotes(m.content)}</Response></div>
          ),
        )}
      </div>
      {/* No composer: nothing is sent from here. */}
      <div />
    </section>
  );
}
