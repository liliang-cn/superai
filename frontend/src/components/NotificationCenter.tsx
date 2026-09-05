import React, { useEffect, useRef, useState } from "react";
import {
  AlertTriangleIcon,
  BellIcon,
  CheckIcon,
  InfoIcon,
  MessageSquareIcon,
  XCircleIcon,
} from "lucide-react";
import {
  Notification,
  useNotificationList,
  useUnreadNotifications,
} from "../lib/notifications";

/**
 * What SuperAI said while you were not looking.
 *
 * The bell lives in the status bar because that is the one strip on screen in
 * every view, and a notification centre you have to navigate to is one you
 * check after you already found out the hard way. The badge is the whole
 * feature: the panel is only what you open once the badge has told you there is
 * something to open.
 *
 * Opening marks everything read, and the rows that were unread keep their mark
 * until the panel closes — otherwise opening the panel erases the very thing
 * you opened it to find.
 */

const ICONS: Record<Notification["level"], typeof InfoIcon> = {
  info: InfoIcon,
  success: CheckIcon,
  warn: AlertTriangleIcon,
  error: XCircleIcon,
};

/** "3m", "2h", "yesterday" — a centre is read by recency, not by timestamp. */
function ago(at: string): string {
  const t = new Date(at).getTime();
  if (!Number.isFinite(t)) return "";
  const secs = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (secs < 60) return "just now";
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  if (secs < 172800) return "yesterday";
  return `${Math.floor(secs / 86400)}d ago`;
}

export default function NotificationCenter({
  onOpenConversation,
}: {
  onOpenConversation?: (session: string) => void;
}) {
  const unread = useUnreadNotifications();
  const [open, setOpen] = useState(false);
  const { items, loading, markAllRead, clear } = useNotificationList(open);
  const wrap = useRef<HTMLDivElement | null>(null);

  // Where the panel hangs. Measured rather than written in CSS: the status bar
  // wraps to a second row when the pills do not fit, and the bell is not the
  // rightmost thing in it — a panel anchored to the bell's right edge runs off
  // the left of a phone screen.
  const [pos, setPos] = useState({ top: 0, left: 0, width: 340 });
  useEffect(() => {
    if (!open) return;
    const place = () => {
      const bell = wrap.current?.querySelector(".notif-bell");
      if (!bell) return;
      const r = bell.getBoundingClientRect();
      const width = Math.min(340, window.innerWidth - 16);
      const left = Math.min(Math.max(8, r.right - width), window.innerWidth - width - 8);
      setPos({ top: r.bottom + 8, left, width });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [open]);

  // Which rows to keep showing as new for as long as this panel is open. Taken
  // once per opening, before the read-marking lands.
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  useEffect(() => {
    if (!open) {
      setFresh(new Set());
      return;
    }
    if (loading) return;
    setFresh((current) => {
      if (current.size > 0) return current;
      const next = new Set(items.filter((n) => !n.read).map((n) => n.id));
      if (next.size > 0) void markAllRead();
      return next;
    });
  }, [open, loading, items, markAllRead]);

  // Click-away and Escape. A panel that hangs over the app until you find its
  // button again is a panel people stop opening.
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  return (
    <div className="notif" ref={wrap}>
      <button
        type="button"
        className={`pill-btn icon-only notif-bell${open ? " on" : ""}`}
        onClick={() => setOpen((v) => !v)}
        title={unread > 0 ? `${unread} unread` : "Notifications"}
        aria-label="Notifications"
        aria-expanded={open}
      >
        <BellIcon className="size-4" />
        {unread > 0 && <span className="notif-badge">{unread > 99 ? "99+" : unread}</span>}
      </button>

      {open && (
        <div
          className="notif-panel"
          role="dialog"
          aria-label="Notifications"
          style={{ top: pos.top, left: pos.left, width: pos.width }}
        >
          <div className="notif-head">
            <span className="notif-title">Notifications</span>
            {items.length > 0 && (
              <button className="notif-action" onClick={() => void clear()}>
                Clear all
              </button>
            )}
          </div>
          <div className="notif-list">
            {items.length === 0 && (
              <div className="notif-empty">{loading ? "Loading…" : "Nothing yet."}</div>
            )}
            {items.map((n) => {
              const Icon = ICONS[n.level] ?? InfoIcon;
              const clickable = Boolean(n.session && onOpenConversation);
              return (
                <div
                  key={n.id}
                  className={
                    `notif-row notif-${n.level}` +
                    (fresh.has(n.id) ? " notif-new" : "") +
                    (clickable ? " notif-clickable" : "")
                  }
                  onClick={
                    clickable
                      ? () => {
                          onOpenConversation!(n.session!);
                          setOpen(false);
                        }
                      : undefined
                  }
                >
                  <Icon size={14} className="notif-icon" />
                  <div className="notif-text">
                    {n.title && <div className="notif-row-title">{n.title}</div>}
                    <div className="notif-message">{n.message}</div>
                    <div className="notif-meta">
                      {n.source && (
                        <span className="notif-source">
                          <MessageSquareIcon size={10} />
                          {n.source}
                        </span>
                      )}
                      <span className="notif-time">{ago(n.at)}</span>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
