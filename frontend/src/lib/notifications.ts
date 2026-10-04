import { useCallback, useEffect, useState } from "react";
import {
  ClearNotifications,
  MarkNotificationsRead,
  Notifications,
  UnreadNotifications,
} from "../../wailsjs/go/app/App";

/**
 * The notification centre, from the frontend's side.
 *
 * A toast answers "what just happened"; this answers "what happened while I was
 * not here", which is a different question with a different failure. The toast
 * is gone in four seconds, the banner is replaced by the next one, and a
 * schedule that ran at three in the morning was announced to an empty room. The
 * backend files everything it publishes (backend/inbox.go); this reads it back.
 *
 * The count is kept in a module store rather than in the bell component so that
 * one arriving notice updates it wherever the bell happens to be mounted, and
 * so the "notice" event keeps exactly one listener: Wails' EventsOff removes
 * every handler for a name, so a second subscriber here would silently take the
 * toasts down with it on unmount. lib/toasts.ts owns that subscription and
 * calls noticeArrived() from inside it.
 */

export interface Notification {
  id: string;
  level: "info" | "success" | "warn" | "error";
  title?: string;
  message: string;
  source?: string;
  session?: string;
  at: string;
  read: boolean;
}

/** How often the count is re-read from disk when nothing has arrived. */
//
// The app is not the only writer: the scheduler daemon raises notices in its
// own process, into the same file, and this window is told nothing about it. So
// there is a poll, slow enough to be free and fast enough that a badge is not
// stale by the time somebody looks up.
const POLL_MS = 30_000;

type Listener = (unread: number) => void;

let unread = 0;
const listeners = new Set<Listener>();
const listListeners = new Set<() => void>();

function publish(next: number) {
  if (next === unread) return;
  unread = next;
  for (const listener of listeners) listener(unread);
}

/** Re-reads the count from the backend. */
export async function refreshUnread(): Promise<void> {
  try {
    publish(await UnreadNotifications());
  } catch {
    // The backend is not up yet, or the window is closing. The next poll or the
    // next notice will put it right; a badge is not worth a toast about itself.
  }
}

/** Called by the one "notice" listener, for every notice the backend raises. */
export function noticeArrived(): void {
  void refreshUnread();
  for (const listener of listListeners) listener();
}

/** The badge. */
export function useUnreadNotifications(): number {
  const [count, setCount] = useState(unread);
  useEffect(() => {
    listeners.add(setCount);
    setCount(unread);
    void refreshUnread();
    const id = setInterval(() => void refreshUnread(), POLL_MS);
    return () => {
      listeners.delete(setCount);
      clearInterval(id);
    };
  }, []);
  return count;
}

/** The list, loaded when the panel opens. */
export function useNotificationList(open: boolean) {
  const [items, setItems] = useState<Notification[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(((await Notifications()) as unknown as Notification[]) ?? []);
      setError("");
    } catch {
      setError("Could not load notifications.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    void load();
    const refresh = () => { void load(); };
    listListeners.add(refresh);
    const timer = setInterval(refresh, POLL_MS);
    return () => { listListeners.delete(refresh); clearInterval(timer); };
  }, [open, load]);

  const markAllRead = useCallback(async (ids: string[]) => {
    if (!ids.length) return;
    try {
      const result = await MarkNotificationsRead(ids);
      if (!result.ok) throw new Error(result.error ?? "Could not mark notifications read.");
      setItems(current => current.map(n => ids.includes(n.id) ? { ...n, read: true } : n));
    } catch {
      setError("Could not mark notifications read.");
    } finally { void refreshUnread(); }
  }, []);

  const clear = useCallback(async () => {
    try {
      const result = await ClearNotifications();
      if (!result.ok) throw new Error(result.error ?? "Could not clear notifications.");
      setItems([]);
      setError("");
    } catch { setError("Could not clear notifications."); }
    finally { void refreshUnread(); }
  }, []);

  return { items, loading, error, reload: load, markAllRead, clear };
}
