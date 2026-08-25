// Telling you a scheduled run happened when you are not looking at the page.
//
// The in-page toast is only a reminder if the page is what you are looking at.
// A 15:00 Tuesday reminder is precisely the case where it is not: the tab is
// behind an editor, or on another desktop, or minimised. The toast fires into
// an empty room.
//
// The browser's own Notification is the cheap fix. It draws a real system
// banner from a background tab, so a hidden page can still reach you.
//
// Its one limit is worth being plain about: something has to still be open.
// Close the tab (or quit the browser) and nothing here fires — the run still
// happens on the server and the answer is still waiting in the conversation,
// but no banner appears. Delivery to a device that is not running this page at
// all is a different mechanism (a push subscription, or a message through a
// channel like Telegram), not this one.

/** Whether this browser has the API at all. */
export function notifySupported(): boolean {
  return typeof window !== "undefined" && "Notification" in window;
}

export function notifyPermission(): NotificationPermission | "unsupported" {
  return notifySupported() ? Notification.permission : "unsupported";
}

/**
 * Ask for permission. Call this from a click.
 *
 * Browsers refuse (or silently deny) a permission prompt that no one asked
 * for, and a prompt that appears on page load is the reason people click
 * "block" — after which there is no second chance from script at all.
 */
export async function requestNotifyPermission(): Promise<NotificationPermission | "unsupported"> {
  if (!notifySupported()) return "unsupported";
  try {
    return await Notification.requestPermission();
  } catch {
    return Notification.permission;
  }
}

export interface RunNotice {
  /** Dedupe key: the same run must not stack two banners. */
  key: string;
  title: string;
  body: string;
  /** Conversation to open when the banner is clicked, if there is one. */
  session?: string;
}

/**
 * Post a banner for a finished run, and hand back what to do on a click.
 *
 * Nothing is posted while the page is visible: the toast has already said it,
 * and a banner for something already on screen is noise. Anything else that
 * stops a banner — no permission, no API, a browser that throws — is silent
 * here on purpose, because a scheduled run must not fail over its own
 * notification.
 */
export function notifyRun(notice: RunNotice, onOpen?: (session: string) => void): void {
  if (!notifySupported() || Notification.permission !== "granted") return;
  if (typeof document !== "undefined" && !document.hidden) return;
  try {
    const banner = new Notification(notice.title, {
      body: notice.body,
      tag: notice.key,
      icon: "/favicon.ico",
    });
    banner.onclick = () => {
      window.focus();
      if (notice.session && onOpen) onOpen(notice.session);
      banner.close();
    };
  } catch {
    /* a banner that cannot be drawn is not worth an error in the console */
  }
}
