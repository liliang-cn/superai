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
  "HiveLinks",
  "UseHive",
  "ForgetHive",
  "Remote",
  "SetWindowTheme",
  "PickFiles",
  "OpenInBrowser",
  "TakeOver",
  "TitleBarDoubleClick",
]);

type Bound = Record<string, (...args: unknown[]) => Promise<unknown>>;

/** Which shell this window draws. The desktop one, in the Wails window and in
 *  a browser tab alike; only a phone-width tab keeps the web layout, the one
 *  built for a screen that narrow. `localStorage["superai-client"]` set to
 *  "desktop" or "web" overrides it. */
export function clientKind(): "desktop" | "web" {
  const w = window as unknown as Record<string, unknown>;
  try {
    const forced = localStorage.getItem("superai-client");
    if (forced === "desktop" || forced === "web") return forced;
  } catch {
    /* no storage: go by the window */
  }
  if (!w.superaiServed) return "desktop";
  return window.matchMedia("(max-width: 640px)").matches ? "web" : "desktop";
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
  name?: string;
  live: boolean;
  error?: string;
}

export interface HiveLinkEntry {
  id: string;
  name: string;
  url: string;
  active: boolean;
}

/** Asks anything on screen that shows the backend switcher to open it. */
export const openSwitcher = () => window.dispatchEvent(new Event("superai:switcher"));

/** "Use this Mac on its own" — remembered so the question is asked once. */
const ALONE_KEY = "superai-desktop-alone";
export const wantsAlone = () => {
  try { return localStorage.getItem(ALONE_KEY) === "1"; } catch { return false; }
};
export const setAlone = (on: boolean) => {
  try { on ? localStorage.setItem(ALONE_KEY, "1") : localStorage.removeItem(ALONE_KEY); } catch { /* fine */ }
};

/** A double-click on any strip the window is dragged by acts as the title
 *  bar's would (zoom, minimize or nothing, as System Settings says). The
 *  strips are marked with --wails-draggable: drag, set inline or in CSS. */
export function installTitleBarDoubleClick(onDouble: () => void) {
  document.addEventListener("dblclick", (e) => {
    let el = e.target as HTMLElement | null;
    if (el?.closest("button, a, input, textarea, select, [contenteditable]")) return;
    while (el && el !== document.documentElement) {
      const v = getComputedStyle(el).getPropertyValue("--wails-draggable").trim();
      if (v === "drag") { onDouble(); return; }
      if (v === "no-drag") return;
      el = el.parentElement;
    }
  });
}
