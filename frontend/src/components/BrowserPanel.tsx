import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { GlobeIcon, RotateCwIcon, XIcon } from "lucide-react";
import { EventsOn } from "../../wailsjs/runtime";
import {
  BrowserStream, closeBrowserPanel, noteBrowserEvent, refreshBrowserStatus, setBrowserMode, settleBrowserTurn, useBrowserPanelOpen, useBrowserStream,
} from "../lib/browser";
import { PATHS } from "../lib/routes";
import { useI18n } from "../lib/i18n";
import "./browser.css";

/**
 * What stands in for the page when there is none to show, in a word or two;
 * "" when the stream is up and showing it. Shared by the card and the panel so
 * both say the same thing about the same browser.
 */
export function browserBlocker(s: BrowserStream): { label: string; kind: "off" | "wait" | "bad" | "" ; error?: string } {
  const st = s.status;
  if (s.switching === "off") return { label: "Browser off", kind: "off" };
  if (s.switching) return { label: "Installing browser…", kind: "wait" };
  if (!st) return { label: "Connecting…", kind: "wait" };
  if (!st.enabled) return { label: "Browser off", kind: "off" };
  if (st.error) return { label: "Browser failed", kind: "bad", error: st.error };
  if (!st.ready) return { label: "Installing browser…", kind: "wait" };
  if (st.mode === "chrome" && (s.connected === false || s.socket === "retrying")) return { label: "Chrome not connected", kind: "off" };
  if (s.connected === false) return { label: "Not connected", kind: "off" };
  if (s.socket === "retrying") return { label: "Reconnecting…", kind: "wait" };
  if (s.socket !== "open" && !s.frame) return { label: "Connecting…", kind: "wait" };
  return { label: "", kind: "" };
}

// The keys whose meaning is not the character they type, as agent-browser's
// own dashboard sends them: Enter and Tab carry their control character so
// the page sees them typed, the rest only a key code.
const KEYS: Record<string, { text?: string; code: number }> = {
  Enter: { text: "\r", code: 13 }, Tab: { text: "\t", code: 9 }, Backspace: { text: "\b", code: 8 }, Escape: { code: 27 },
  ArrowLeft: { code: 37 }, ArrowUp: { code: 38 }, ArrowRight: { code: 39 }, ArrowDown: { code: 40 },
  Delete: { code: 46 }, Home: { code: 36 }, End: { code: 35 }, PageUp: { code: 33 }, PageDown: { code: 34 },
};
const modifiers = (e: { altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean }) =>
  (e.altKey ? 1 : 0) | (e.ctrlKey ? 2 : 0) | (e.metaKey ? 4 : 0) | (e.shiftKey ? 8 : 0);
const buttonName = (b: number) => (b === 0 ? "left" : b === 1 ? "middle" : b === 2 ? "right" : "none");

/**
 * The page, live, and taken over by whoever clicks into it: while the view
 * has focus, pointer, wheel and keys go to the page through the socket. Out
 * of focus it is only a picture, and every key belongs to the app again.
 */
function BrowserScreen({ s }: { s: BrowserStream }) {
  const { t } = useI18n();
  const box = useRef<HTMLDivElement>(null);
  const img = useRef<HTMLImageElement>(null);
  const [focused, setFocused] = useState(false);
  const down = useRef<number | null>(null);
  const lastClick = useRef({ at: 0, x: 0, y: 0, n: 0 });
  const moveQueued = useRef<{ x: number; y: number } | null>(null);
  const raf = useRef(0);
  const sendRef = useRef(s.send);
  sendRef.current = s.send;

  const page = s.meta ?? (s.viewport ? { deviceWidth: s.viewport.width, deviceHeight: s.viewport.height } : null);
  const pageRef = useRef(page);
  pageRef.current = page;

  /** A point on the screen, in the page's CSS pixels, or null when it is in the letterbox. */
  const toPage = (clientX: number, clientY: number) => {
    const el = img.current, p = pageRef.current;
    if (!el || !p || !el.naturalWidth) return null;
    // The picture is fitted inside its box keeping its shape, so the part of
    // the box it covers is worked out the way object-fit: contain lays it out.
    const r = el.getBoundingClientRect();
    const scale = Math.min(r.width / el.naturalWidth, r.height / el.naturalHeight);
    const w = el.naturalWidth * scale, h = el.naturalHeight * scale;
    const left = r.left + (r.width - w) / 2, top = r.top + (r.height - h) / 2;
    const fx = (clientX - left) / w, fy = (clientY - top) / h;
    if (fx < 0 || fy < 0 || fx > 1 || fy > 1) return null;
    return { x: Math.round(fx * p.deviceWidth), y: Math.round(fy * p.deviceHeight) };
  };

  // Keys go to the page only while the view has focus, caught before the
  // app's own shortcuts see them; with the focus elsewhere nothing changes.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (document.activeElement !== box.current || e.isComposing) return;
      e.preventDefault();
      e.stopPropagation();
      const type = e.type === "keydown" ? "keyDown" : "keyUp";
      const k = KEYS[e.key];
      const printable = e.key.length === 1;
      sendRef.current({
        type: "input_keyboard", eventType: type, key: e.key, code: e.code,
        text: type === "keyDown" ? k?.text ?? (printable && !e.metaKey && !e.ctrlKey ? e.key : undefined) : undefined,
        windowsVirtualKeyCode: k?.code ?? (printable ? e.key.toUpperCase().charCodeAt(0) : 0),
        modifiers: modifiers(e),
      });
    };
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("keyup", onKey, true);
    return () => { window.removeEventListener("keydown", onKey, true); window.removeEventListener("keyup", onKey, true); };
  }, []);

  // The wheel scrolls the page, never the panel; React's own wheel listener
  // is passive and could not stop the panel from scrolling, so this one is not.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      const p = toPage(e.clientX, e.clientY);
      if (!p) return;
      e.preventDefault();
      // Line and page deltas (a mouse wheel on some systems) in pixels.
      const k = e.deltaMode === 1 ? 40 : e.deltaMode === 2 ? 800 : 1;
      sendRef.current({ type: "input_mouse", eventType: "mouseWheel", ...p, button: "none", clickCount: 0, deltaX: e.deltaX * k, deltaY: e.deltaY * k, modifiers: modifiers(e) });
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  useEffect(() => () => cancelAnimationFrame(raf.current), []);

  const onPointerDown = (e: React.PointerEvent) => {
    box.current?.focus();
    const p = toPage(e.clientX, e.clientY);
    if (!p) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    // A second press close by and soon after is a double click to the page.
    const c = lastClick.current, now = performance.now();
    const n = now - c.at < 450 && Math.abs(c.x - p.x) < 6 && Math.abs(c.y - p.y) < 6 ? c.n + 1 : 1;
    lastClick.current = { at: now, x: p.x, y: p.y, n };
    down.current = e.button;
    s.send({ type: "input_mouse", eventType: "mousePressed", ...p, button: buttonName(e.button), clickCount: n, modifiers: modifiers(e) });
  };
  const onPointerMove = (e: React.PointerEvent) => {
    if (!focused && down.current === null) return;
    const p = toPage(e.clientX, e.clientY);
    if (!p) return;
    // At most one move a frame: a pointer reports far more often than the
    // page can redraw, and every move is a message over the network.
    moveQueued.current = p;
    if (raf.current) return;
    raf.current = requestAnimationFrame(() => {
      raf.current = 0;
      const q = moveQueued.current;
      if (!q) return;
      sendRef.current({ type: "input_mouse", eventType: "mouseMoved", ...q, button: down.current === null ? "none" : buttonName(down.current), clickCount: 0, modifiers: 0 });
    });
  };
  const onPointerUp = (e: React.PointerEvent) => {
    if (down.current === null) return;
    const p = toPage(e.clientX, e.clientY) ?? moveQueued.current;
    const button = buttonName(down.current);
    down.current = null;
    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
    if (p) s.send({ type: "input_mouse", eventType: "mouseReleased", ...p, button, clickCount: lastClick.current.n, modifiers: modifiers(e) });
  };

  const block = browserBlocker(s);
  return (
    <div ref={box} className={`bw-screen${focused ? " taken" : ""}`} tabIndex={0} data-testid="browser-screen"
      onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
      onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={onPointerUp}
      onContextMenu={(e) => e.preventDefault()}>
      {s.frame ? <img ref={img} src={s.frame} alt="" draggable={false} /> : <div className="bw-blank"><GlobeIcon size={28} strokeWidth={1.4} /></div>}
      {block.label && <span className={`bw-flag ${block.kind}`}>{t(block.label)}</span>}
    </div>
  );
}

const dotOf = (s: BrowserStream) => (s.socket === "open" && s.connected !== false ? "on" : s.socket === "connecting" || s.socket === "retrying" ? "wait" : "off");

/** The large live view: tabs, address, the page, and a line of status. */
export function BrowserPanel({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const s = useBrowserStream(15);
  const block = browserBlocker(s);
  const ready = !s.switching && !!s.status?.enabled && !!s.status.ready && !s.status.error;
  const off = !s.switching && !!s.status && !s.status.enabled;
  const [err, setErr] = useState("");
  const turnOn = (mode: "chrome" | "own") => { setErr(""); setBrowserMode(mode).catch((e) => setErr(String(e instanceof Error ? e.message : e))); };

  // Escape closes the panel unless the page has the keys.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || (document.activeElement as HTMLElement | null)?.classList.contains("bw-screen")) return;
      onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="bw-overlay cr-scope" onClick={onClose}>
      <div className="bw-panel" role="dialog" aria-label={t("Browser")} onClick={(e) => e.stopPropagation()} data-testid="browser-panel">
        <div className="bw-tabs">
          <GlobeIcon size={15} className="bw-tabs-ic" />
          {s.tabs.length > 0 ? s.tabs.map((tab) => (
            <span key={tab.tabId} className={`bw-tab${tab.active ? " on" : ""}`} title={tab.url}>{tab.title || tab.url || t("New tab")}</span>
          )) : <span className="bw-tab on">{s.title || t("Browser")}</span>}
          <button className="cr-trace-x bw-x" onClick={onClose} aria-label={t("Close")}><XIcon size={16} /></button>
        </div>
        <div className="bw-addr">
          <span className={`bw-dot ${dotOf(s)}`} />
          <span className="bw-url" data-testid="browser-url">{s.url || "—"}</span>
        </div>
        {ready || s.frame ? <BrowserScreen s={s} /> : (
          <div className="bw-screen bw-empty">
            <div className={`bw-state ${block.kind}`}>
              {block.kind === "wait" && <i className="bw-spin big" />}
              <b>{t(block.label)}</b>
              {(block.error || err) && <code>{err || block.error}</code>}
              <div className="bw-btns">
                {/* Off is not a dead end: either browser can be turned on from here. */}
                {off && <button className="cr-btn primary" onClick={() => turnOn("chrome")} data-testid="browser-on-chrome">{t("My Chrome")}</button>}
                {off && <button className="cr-btn" onClick={() => turnOn("own")} data-testid="browser-on-own">{t("Separate browser")}</button>}
                {block.kind === "bad" && <button className="cr-btn tiny" onClick={refreshBrowserStatus}><RotateCwIcon size={12} />{t("Retry")}</button>}
                {(block.kind === "bad" || (block.kind === "off" && !off)) && <button className="cr-btn tiny" onClick={() => { onClose(); navigate(PATHS.settings); }}>{t("Settings")}</button>}
              </div>
            </div>
          </div>
        )}
        <div className="bw-foot">
          <span>{s.socket === "open" && s.connected !== false ? t("Connected") : t(block.label || "Not connected")}</span>
          {s.status?.mode && s.status.mode !== "off" && <span>{s.status.mode === "chrome" ? "Chrome" : t("Own browser")}</span>}
          {s.meta && <span>{s.meta.deviceWidth}×{s.meta.deviceHeight}</span>}
          {s.status?.version && <span className="bw-ver">agent-browser {s.status.version}</span>}
        </div>
      </div>
    </div>
  );
}

/**
 * Mounted once per window: the panel when it is open, and the listener that
 * files each turn's browser calls for the screens that follow chat events
 * without useChat (the world's queen card and command line).
 */
export function BrowserPanelHost() {
  const open = useBrowserPanelOpen();
  useEffect(() => {
    if (typeof window === "undefined" || !(window as any).runtime) return;
    const offs = [
      EventsOn("chat:event", (p: any) => noteBrowserEvent(p)),
      EventsOn("chat:done", (p: any) => p?.requestId && settleBrowserTurn(p.requestId)),
      EventsOn("chat:error", (p: any) => p?.requestId && settleBrowserTurn(p.requestId, true)),
      EventsOn("chat:cancelled", (p: any) => p?.requestId && settleBrowserTurn(p.requestId)),
    ];
    return () => offs.forEach((f) => typeof f === "function" && f());
  }, []);
  return open ? <BrowserPanel onClose={closeBrowserPanel} /> : null;
}
