import React, { useCallback, useEffect, useState } from "react";
import { HistoryIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { ChatSessions, DeleteChatSession } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";

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
  const [sessions, setSessions] = useState<backend.ChatSessionInfo[] | null>(null);
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

/** The History / New chat buttons. */
export function HistoryBar({
  history,
  onNew,
  busy,
}: {
  history: HistoryController;
  onNew: () => void;
  busy?: boolean;
}) {
  return (
    <div className="chat-toolbar">
      <button className="btn ghost sm" onClick={history.toggle} title="Past conversations">
        <HistoryIcon className="size-3.5" /> History
      </button>
      <button
        className="btn ghost sm"
        onClick={() => {
          history.close();
          onNew();
        }}
        disabled={busy}
        title="Start a new conversation"
      >
        <PlusIcon className="size-3.5" /> New chat
      </button>
    </div>
  );
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
  // Deleting is permanent, so it takes a second click on the same row.
  const [confirming, setConfirming] = useState("");
  if (!sessions) return null;
  return (
    <div className="history-list">
      {loading ? (
        <div className="loading-row">
          <span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading…
        </div>
      ) : sessions.length === 0 ? (
        <div className="trace-empty">No past conversations yet.</div>
      ) : (
        sessions.map((h) => (
          <div key={h.id} className={`history-row${h.id === currentId ? " active" : ""}`}>
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
              title={confirming === h.id ? "Click again to delete" : "Delete this conversation"}
              onClick={(e) => {
                e.stopPropagation();
                if (confirming !== h.id) {
                  setConfirming(h.id);
                  return;
                }
                setConfirming("");
                DeleteChatSession(h.id).then(() => history.refresh());
              }}
            >
              <Trash2Icon className={confirming === h.id ? "size-3.5 text-red-500" : "size-3.5"} />
            </button>
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
