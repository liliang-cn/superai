import "./polyfills";
import "../world/world.css";
import "./embed.css";
import { WorldEngine, type EngineOptions, type Picked } from "../world/engine";
import { cluster, lensOf, type WorldData } from "../world/cluster";
import { setQueenName } from "../lib/queenName";
import { LANGUAGES, Language, setLanguage } from "../lib/i18n";
import { PROTOCOL, normalize, parse, type EmbedMessage, type HostMessage } from "./protocol";

// The 3D world alone, for a native host: the same scene the desktop draws,
// with no core, no sign-in and none of the app around it. The host sends
// what to show and camera moves; taps go back to it. See protocol.ts.

type HostPort = { postMessage(message: string): void; addEventListener?(type: "message", fn: (e: MessageEvent) => void): void };

declare global {
  interface Window {
    /** Put there by the Android host (WebViewCompat.addWebMessageListener). */
    SuperAIHost?: HostPort;
    /** For the host's tests and a desktop browser: the same doors by hand. */
    __world?: { receive(message: unknown): void; firePick(kind: string, id: string, title?: string): void; ready: boolean };
  }
}

const query = new URLSearchParams(location.search);
const lang = query.get("lang");
if (LANGUAGES.some((l) => l.id === lang)) setLanguage(lang as Language);

/** How hard to work: the host knows the device; the query says. */
function options(): EngineOptions {
  const low = query.get("tier") === "low";
  const dpr = Number(query.get("dpr"));
  const fps = Number(query.get("fps"));
  return {
    maxPixelRatio: dpr > 0 ? dpr : 1.5,
    shadows: !low,
    antialias: !low,
    idleFps: fps >= 0 && query.has("fps") ? fps : 30,
  };
}

function send(m: EmbedMessage) {
  const s = JSON.stringify(m);
  try {
    if (window.SuperAIHost) window.SuperAIHost.postMessage(s);
    else if (window.parent !== window) window.parent.postMessage(m, "*");
  } catch { /* a host that went away */ }
  window.dispatchEvent(new CustomEvent("superai:out", { detail: m }));
}

/** The whole cluster from the usual corner, wide enough for a tall phone. */
function homeLens() {
  const l = lensOf("home");
  const aspect = window.innerWidth / Math.max(1, window.innerHeight);
  const fit = Math.min(1, Math.max(0.7, aspect / 1.3));
  return { x: l.x, z: l.z, zoom: l.zoom * fit };
}

let engine: WorldEngine<WorldData> | null = null;
let quiet = false;
let lens = homeLens();

function receive(raw: unknown) {
  const m = parse(raw) as HostMessage | null;
  if (!m || !engine) return;
  const e = engine;
  switch (m.type) {
    case "data":
      if (m.queen?.name) setQueenName(m.queen.name, m.queen.aliases ?? []);
      e.update(normalize(m.data));
      break;
    case "camera":
      switch (m.op) {
        case "zoomIn": e.zoomBy(1.3); break;
        case "zoomOut": e.zoomBy(1 / 1.3); break;
        case "rotateLeft": e.rotateBy(-Math.PI / 8); break;
        case "rotateRight": e.rotateBy(Math.PI / 8); break;
        case "home": e.home(); break;
        case "reset":
          lens = homeLens();
          e.select(null);
          e.look(lens.x, lens.z, lens.zoom, "cluster");
          break;
      }
      break;
    case "focus":
      quiet = true;
      try { e.focusOn(m.kind, m.id); } finally { quiet = false; }
      break;
    case "select":
      e.select(null);
      break;
    case "visible":
      e.setPaused(!m.on);
      break;
    case "lens":
      if ([m.x, m.z, m.zoom].every((n) => typeof n === "number" && isFinite(n))) { lens = { x: m.x, z: m.z, zoom: m.zoom }; e.look(m.x, m.z, m.zoom, "cluster"); }
      break;
  }
}

function start() {
  const host = document.getElementById("world")!;
  try {
    engine = new WorldEngine<WorldData>(host, options());
  } catch (err) {
    send({ type: "ready", version: PROTOCOL, webgl: false, error: String((err as Error)?.message ?? err) });
    return;
  }
  const e = engine;
  try {
    e.add(cluster);
    e.update(normalize(undefined));
  } catch (err) {
    // A scene that cannot build says so rather than leaving the host waiting.
    e.setPaused(true);
    send({ type: "ready", version: PROTOCOL, webgl: false, error: String((err as Error)?.message ?? err) });
    return;
  }
  e.pickHandler((p: Picked | null) => {
    if (quiet) return;
    send(p ? { type: "pick", kind: p.kind, id: p.id, title: p.title } : { type: "pick", id: null });
  });
  e.look(lens.x, lens.z, lens.zoom, "cluster", true);
  // The host's messages: posted to the window (WebViewCompat.postWebMessage),
  // or answered on the injected port.
  window.addEventListener("message", (ev) => receive(ev.data));
  window.SuperAIHost?.addEventListener?.("message", (ev) => receive(ev.data));
  // Out of sight in the host (another app in front): nothing to draw.
  document.addEventListener("visibilitychange", () => { if (document.hidden) e.setPaused(true); else e.setPaused(false); });
  window.__world = {
    receive,
    firePick: (kind, id, title = id) => send({ type: "pick", kind, id, title }),
    ready: true,
  };
  // Ready once the first frame is on screen.
  requestAnimationFrame(() => requestAnimationFrame(() => send({ type: "ready", version: PROTOCOL, webgl: true })));
}

start();
