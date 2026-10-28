import { useCallback, useRef } from "react";
import type { CompositionEvent, KeyboardEvent } from "react";

// Enter while an IME is composing means "accept this candidate", not "send".
//
// React exposes nativeEvent.isComposing for exactly this, and on Chromium it is
// enough. It is not enough here. Wails renders in WKWebView, where the keydown
// that accepts a candidate can arrive *after* compositionend has already fired
// — isComposing is false by then, the guard passes, and a half-typed Chinese
// sentence is sent while the user was only choosing a word.
//
// So this tracks composition itself and keeps the guard up for a moment past
// compositionend. The window is short enough to be invisible when someone
// genuinely types Enter after finishing a word, and long enough to cover the
// event ordering WKWebView actually produces.

// imeSettleMs is how long after compositionend an Enter is still treated as
// belonging to the composition.
const imeSettleMs = 80;

// legacyImeKeyCode is what browsers report for a keystroke consumed by an IME.
// Predates isComposing and is still emitted; free to check, and it catches the
// engines that report nothing else.
const legacyImeKeyCode = 229;

export type ImeGuard = {
  /** True when this key event belongs to an IME composition. */
  composing: (e: KeyboardEvent<HTMLElement>) => boolean;
  /** Spread onto the input: onCompositionStart / onCompositionEnd. */
  handlers: {
    onCompositionStart: (e: CompositionEvent<HTMLElement>) => void;
    onCompositionEnd: (e: CompositionEvent<HTMLElement>) => void;
  };
};

/**
 * useImeGuard reports whether a key event landed mid-composition.
 *
 * Spread `handlers` onto the input and call `composing(e)` before acting on
 * Enter. An input that forwards its own composition handlers still works:
 * pass them through alongside.
 */
export function useImeGuard(): ImeGuard {
  const active = useRef(false);
  const endedAt = useRef(0);

  const onCompositionStart = useCallback(() => {
    active.current = true;
  }, []);

  const onCompositionEnd = useCallback(() => {
    active.current = false;
    endedAt.current = Date.now();
  }, []);

  const composing = useCallback((e: KeyboardEvent<HTMLElement>) => {
    if (active.current) return true;
    if (e.nativeEvent.isComposing) return true;
    if (e.keyCode === legacyImeKeyCode) return true;
    return Date.now() - endedAt.current < imeSettleMs;
  }, []);

  return { composing, handlers: { onCompositionStart, onCompositionEnd } };
}

/**
 * installImeEnterGuard keeps every Enter that belongs to an IME composition
 * away from the page, for every input at once.
 *
 * useImeGuard only protects the boxes that remember to use it, and a plain
 * <form> submits on Enter with no handler at all. Listening on window in the
 * capture phase runs before React's own listener, so stopping the event there
 * means no onKeyDown sees it, and preventDefault stops the form's implicit
 * submission. Composition is tracked the same way as useImeGuard, with the
 * same settle window for WKWebView's late keydown.
 */
export function installImeEnterGuard(): void {
  let active = false;
  let endedAt = 0;
  window.addEventListener("compositionstart", () => { active = true; }, true);
  window.addEventListener("compositionend", () => { active = false; endedAt = Date.now(); }, true);
  window.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    if (!(active || e.isComposing || e.keyCode === legacyImeKeyCode || Date.now() - endedAt < imeSettleMs)) return;
    e.preventDefault();
    e.stopPropagation();
  }, true);
}
