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

/**
 * Post a banner right now, whatever the page is doing.
 *
 * The only caller is the "try it" button. Everything automatic goes through
 * notifyRun, which stays quiet while the page is visible — but a test fired
 * from a click is always fired at a visible page, so testing through that path
 * would show nothing and read as broken.
 *
 * Returns what happened, because a test that fails silently is worse than no
 * test: macOS Focus, a Do Not Disturb schedule, or a browser in full screen
 * will swallow the banner after the API has happily returned.
 */
export function notifyNow(title: string, body: string): "posted" | "denied" | "unsupported" | "failed" {
  if (!notifySupported()) return "unsupported";
  if (Notification.permission !== "granted") return "denied";
  try {
    const banner = new Notification(title, { body, tag: "superai-test", icon: "/favicon.ico" });
    banner.onclick = () => {
      window.focus();
      banner.close();
    };
    return "posted";
  } catch {
    return "failed";
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
