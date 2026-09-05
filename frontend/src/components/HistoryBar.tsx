import React, { useCallback, useEffect, useState } from "react";
import {
  CheckIcon,
  HashIcon,
  HistoryIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react";
import { ChatSessions, DeleteChatSession } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";
import { copyText } from "../lib/format";
import { toast } from "../lib/toasts";

export interface HistoryController {
  /** Non-null while the list is open (null = transcript is showing). */
  sessions: backend.ChatSessionInfo[] | null;
  loading: boolean;
  toggle: () => void;
  close: () => void;
  /** Re-read the list in place (after a delete). */
  refresh: () => void;
  /** Drop a cached list so the next open re-reads it. */
  invalidate: () => void;
}

/**
 * Past-conversation state, shared by the Chat and Agent views. The list is
 * fetched on open rather than kept live: a finished turn changes it, and both
 * views already know when that happens.
 */
export function useHistory(): HistoryController {
  const [sessions, setSessions] = useState<backend.ChatSessionInfo[] | null>(
    null,
  );
  const [loading, setLoading] = useState(false);

  const open = useCallback(() => {
    setLoading(true);
    ChatSessions()
      .then((s) => setSessions(s || []))
      .catch(() => setSessions([]))
      .finally(() => setLoading(false));
  }, []);

  const close = useCallback(() => setSessions(null), []);
  const toggle = useCallback(() => {
    setSessions((prev) => {
      if (prev) return null;
      open();
      return prev;
    });
  }, [open]);

  return { sessions, loading, toggle, close, refresh: open, invalidate: close };
}

/** The History / New chat buttons, and which conversation this is. */
export function HistoryBar({
  history,
  onNew,
  sessionId,
}: {
  history: HistoryController;
  onNew: () => void;
  /** The conversation on screen. Shown so it can be named elsewhere. */
  sessionId?: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="chat-toolbar">
      <button
        className="btn ghost sm"
        onClick={history.toggle}
        title="Past conversations"
      >
        <HistoryIcon className="size-3.5" /> History
      </button>
      <button
        className="btn ghost sm"
        onClick={() => {
          history.close();
          onNew();
        }}
        title="Start a new conversation"
      >
        <PlusIcon className="size-3.5" /> New chat
      </button>
      {/* The id is what identifies a conversation to everything outside this
          window — a schedule appends its runs to one, the daemon logs by it —
          so it has to be readable and copyable, not only inferable from a
          screenshot of the URL. Truncated because the middle of a UUID tells
          nobody anything; the whole thing goes to the clipboard. */}
      {sessionId && (
        <button
          className="session-id"
          title={`Conversation ${sessionId} — click to copy`}
          onClick={() => {
            copyText(sessionId);
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1200);
          }}
        >
          {copied ? (
            <CheckIcon className="size-3" />
          ) : (
            <HashIcon className="size-3" />
          )}
          <span className="session-id-text">{shortId(sessionId)}</span>
        </button>
      )}
    </div>
  );
}

/** Enough of an id to recognise it by, without the unreadable middle. */
function shortId(id: string): string {
  return id.length > 13 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

/** The list of past conversations, shown in place of the transcript. */
export function HistoryList({
  history,
  currentId,
  onPick,
}: {
  history: HistoryController;
  currentId: string;
  onPick: (id: string) => void;
}) {
  const { sessions, loading } = history;
  // Deleting is permanent, so it is asked about. The row being asked about,
  // and the one whose delete is in flight — separate, because the answer to
  // "are you sure" and "is it gone yet" are two different things to draw.
  const [confirming, setConfirming] = useState("");
  const [deleting, setDeleting] = useState("");

  // Escape backs out, and so does a click anywhere that is not the popconfirm.
  // A confirmation the pointer has wandered away from should not still be
  // armed when it comes back — the next click could be meant for the row
  // underneath it.
  useEffect(() => {
    if (!confirming) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setConfirming("");
    };
    const onDown = (e: MouseEvent) => {
      const el = e.target as HTMLElement | null;
      if (!el?.closest(".history-confirm, .history-del")) setConfirming("");
    };
    window.addEventListener("keydown", onKey);
    // Capture, so a click that lands on some other handler still closes this.
    window.addEventListener("mousedown", onDown, true);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("mousedown", onDown, true);
    };
  }, [confirming]);

  const remove = (h: backend.ChatSessionInfo) => {
    setConfirming("");
    setDeleting(h.id);
    DeleteChatSession(h.id)
      .then(() => {
        history.refresh();
        // Named, because a list of near-identical rows gives no other way to
        // tell which one went.
        toast.success(`Deleted “${h.title}”`);
      })
      .catch((e: unknown) => toast.error(`Could not delete it: ${String(e)}`))
      .finally(() => setDeleting(""));
  };

  if (!sessions) return null;
  return (
    <div className="history-list">
      {loading ? (
        <div className="loading-row">
          <span
            className="spinner"
            style={{ borderTopColor: "var(--accent)" }}
          />{" "}
          Loading…
        </div>
      ) : sessions.length === 0 ? (
        <div className="trace-empty">No past conversations yet.</div>
      ) : (
        sessions.map((h) => (
          <div
            key={h.id}
            className={`history-row${h.id === currentId ? " active" : ""}`}
          >
            <button
              className="history-item"
              onClick={() => {
                onPick(h.id);
                history.close();
              }}
            >
              <span className="history-title">{h.title}</span>
              <span className="history-meta">
                {h.turns} turns · {new Date(h.updated_at).toLocaleString()}
              </span>
            </button>
            <button
              className="panel-toggle inline history-del"
              title="Delete this conversation"
              aria-haspopup="dialog"
              aria-expanded={confirming === h.id}
              disabled={deleting === h.id}
              onClick={(e) => {
                e.stopPropagation();
                setConfirming((prev) => (prev === h.id ? "" : h.id));
              }}
            >
              <Trash2Icon
                className={
                  confirming === h.id ? "size-3.5 text-red-500" : "size-3.5"
                }
              />
            </button>
            {confirming === h.id && (
              <div
                className="history-confirm"
                role="dialog"
                aria-label="Delete this conversation?"
                // The row underneath is a button that opens the conversation;
                // a click meant for Cancel must not also open what it was
                // about to delete.
                onClick={(e) => e.stopPropagation()}
              >
                <div className="history-confirm-q">Delete this conversation?</div>
                <div className="history-confirm-t">{h.title}</div>
                <div className="history-confirm-actions">
                  <button className="btn ghost sm" onClick={() => setConfirming("")}>
                    Cancel
                  </button>
                  <button className="btn danger sm" autoFocus onClick={() => remove(h)}>
                    Delete
                  </button>
                </div>
              </div>
            )}
          </div>
        ))
      )}
    </div>
  );
}

/** Keep the transcript and the history list mutually exclusive. */
export function useCloseHistoryOnDone(
  history: HistoryController,
  onDone: (cb: () => void) => void,
) {
  useEffect(() => {
    onDone(() => history.invalidate());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}
