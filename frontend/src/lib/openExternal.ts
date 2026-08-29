import { BrowserOpenURL } from "../../wailsjs/runtime";

// Opening a link the model wrote.
//
// Inside the desktop window a plain <a href> is a trap: the WKWebView follows
// it, SuperAI's own UI is replaced by whatever the page was, and there is no
// back button to return from it. So links are opened outside instead — the
// system browser in the desktop app, a new tab in the served one, which is what
// each surface's BrowserOpenURL already does.
//
// The scheme is checked because the href is untrusted input. It comes from
// model output, and BrowserOpenURL hands it to the operating system: on macOS
// that is `open`, which will launch whatever application has registered for the
// scheme. So this is an allowlist of the three that mean "a place on the web or
// a person to mail" — a blocklist would have to guess at every custom scheme
// some other installed application claims.
const OPENABLE_SCHEMES = new Set(["http:", "https:", "mailto:"]);

/** isExternallyOpenable reports whether a href should leave the app. */
export function isExternallyOpenable(href: string | undefined | null): boolean {
  if (!href) return false;
  let parsed: URL;
  try {
    // Deliberately parsed without a base, so a relative href or a bare "#top"
    // throws rather than resolving against the app's own origin — an in-page
    // anchor is not something to open a browser for.
    parsed = new URL(href);
  } catch {
    return false;
  }
  return OPENABLE_SCHEMES.has(parsed.protocol);
}

/** openExternal sends a link outside the app, or does nothing if it should not leave. */
export function openExternal(href: string | undefined | null): void {
  if (!isExternallyOpenable(href)) return;
  BrowserOpenURL(href as string);
}
