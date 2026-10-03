// The desktop window as one end of the hive.
//
// Linked, every bound call this window makes goes to the queen through the
// Go side's Remote(method, args), and her events arrive under their own names
// (the Go side relays her stream). The generated bindings resolve
// window.go.app.App.<Method> at call time, so swapping that one object is the
// whole switch: nothing else in the frontend knows it is talking to the hive.
//
// The calls kept here are the ones about this machine — its window, its file
// dialogs, the link itself.

const LOCAL = new Set([
  "LinkHive",
  "UnlinkHive",
  "HiveLinkStatus",
  "Remote",
  "SetWindowTheme",
  "PickFiles",
  "OpenInBrowser",
  "TakeOver",
]);

type Bound = Record<string, (...args: unknown[]) => Promise<unknown>>;

/** Which end this window is. The served build is the web; the Wails window is
 *  the desktop unless asked to look like the other (for testing in a browser,
 *  `localStorage["superai-client"] = "desktop"` makes the web draw the desktop). */
export function clientKind(): "desktop" | "web" {
  const w = window as unknown as Record<string, unknown>;
  try {
    const forced = localStorage.getItem("superai-client");
    if (forced === "desktop" || forced === "web") return forced;
  } catch {
    /* no storage: go by the build */
  }
  return w.superaiServed ? "web" : "desktop";
}

let bridged = false;

/** Sends this window's calls to the queen from now on. */
export function installHiveBridge() {
  if (bridged) return;
  const w = window as unknown as { go?: { app?: { App?: Bound } } };
  const local = w.go?.app?.App;
  if (!local) return;
  bridged = true;
  w.go!.app!.App = new Proxy(local, {
    get: (target, prop) => {
      const name = String(prop);
      if (LOCAL.has(name)) return target[name];
      return (...args: unknown[]) => target.Remote(name, args);
    },
  });
}

export const isBridged = () => bridged;

/** Where the knowledge graph's live view is: the server's /graph/ in a
 *  browser, and in a linked desktop window the same path, which the Go side
 *  forwards to the queen. Null means "ask GraphView for a local address". */
export function graphSrc(): string | null {
  const w = window as unknown as Record<string, unknown>;
  return w.superaiServed || bridged ? "/graph/" : null;
}

export interface HiveLinkInfo {
  linked: boolean;
  url?: string;
  live: boolean;
  error?: string;
}

/** "Use this Mac on its own" — remembered so the question is asked once. */
const ALONE_KEY = "superai-desktop-alone";
export const wantsAlone = () => {
  try { return localStorage.getItem(ALONE_KEY) === "1"; } catch { return false; }
};
export const setAlone = (on: boolean) => {
  try { on ? localStorage.setItem(ALONE_KEY, "1") : localStorage.removeItem(ALONE_KEY); } catch { /* fine */ }
};
