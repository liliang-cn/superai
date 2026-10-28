import * as THREE from "three";
import { CSS2DObject } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import { createElement, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { Response } from "@/components/ai-elements/response";

// Speech, as a comic draws it: what a robot says comes out of its head in a
// bubble with a tail pointing at it, and while it is thinking a cloud of
// dots floats there instead. The robots are registered by who they are, so
// anything in the app can make one of them speak. What is said is markdown,
// drawn the way the chat draws it, in a bubble of a few lines: while it is
// coming the latest of it shows, once it is done its start.

const actors = new Map<string, THREE.Object3D>();

/** "queen", "worker:<name>", "cli:<agent>", "agent:<name>", "bee:<id>". */
export function registerActor(key: string, obj: THREE.Object3D) { actors.set(key, obj); }
export function forgetActor(key: string) { actors.delete(key); speaking.get(key)?.remove(); }

type Bubble = { el: HTMLElement; body: HTMLElement; obj: CSS2DObject; timer: number; md?: { box: HTMLElement; root: Root }; extra?: Root; remove: () => void };
// Something drawn under what a robot says (the browser card of the turn), by
// robot. It outlives the switch between dots and words, and goes with the bubble.
const attached = new Map<string, ReactNode>();
const speaking = new Map<string, Bubble>();
let onOpen: ((key: string) => void) | null = null;
/** What clicking a bubble does (open the whole conversation). */
export function onBubbleClick(fn: (key: string) => void) { onOpen = fn; }

// The most of an answer a bubble holds; it shows a few lines of that.
const MAX = 2000;

/**
 * Makes `key` say `text` ("think" shows the cloud of dots). The bubble stays
 * while speech keeps coming and fades `ttl` seconds after the last of it.
 * `from` is which end of a long answer shows: its latest words as it streams,
 * its start once it is done.
 */
export function say(key: string, text: string, mode: "say" | "think" = "say", ttl = 12, from: "end" | "start" = "end") {
  const actor = actors.get(key);
  if (!actor) return;
  let b = speaking.get(key);
  if (!b) {
    const el = document.createElement("div");
    el.className = "wl-say";
    const inner = document.createElement("div");
    inner.className = "wl-say-in";
    el.appendChild(inner);
    // The words or the dots go in `body`; `extra` keeps an attachment while
    // they change.
    const body = document.createElement("div");
    const extra = document.createElement("div");
    extra.className = "wl-say-extra";
    inner.append(body, extra);
    const obj = new CSS2DObject(el);
    // Above the head, and above its name label, whatever the robot's size.
    const box = new THREE.Box3().setFromObject(actor);
    const top = (box.max.y - actor.getWorldPosition(new THREE.Vector3()).y) / actor.scale.y;
    const pill = actor.children.find((c) => c instanceof CSS2DObject && c.element.classList.contains("wl-pill"));
    obj.position.set(0, Math.max(top + 0.4, (pill?.position.y ?? 0) + 0.7), 0);
    actor.add(obj);
    el.addEventListener("click", () => onOpen?.(key));
    const remove = () => {
      unmount(b!);
      const x = b!.extra;
      if (x) setTimeout(() => x.unmount(), 0);
      attached.delete(key);
      obj.removeFromParent(); el.remove(); speaking.delete(key);
    };
    b = { el, body, obj, timer: 0, remove };
    speaking.set(key, b);
    b.extra = createRoot(extra);
    const a = attached.get(key);
    if (a) { b.extra.render(a); el.classList.add("has-extra"); }
  }
  window.clearTimeout(b.timer);
  b.el.dataset.mode = mode;
  const inner = b.body;
  if (mode === "think") {
    unmount(b);
    inner.innerHTML = '<span class="wl-dots"><i></i><i></i><i></i></span>' + (text ? `<small>${escape(text)}</small>` : "");
    b.el.classList.remove("more");
  } else {
    const t = text.trim();
    if (!b.md) {
      inner.innerHTML = "";
      const box = document.createElement("div");
      box.className = "wl-say-md";
      inner.appendChild(box);
      b.md = { box, root: createRoot(box) };
    }
    b.md.root.render(createElement(Response, null, from === "start" ? t.slice(0, MAX) : t.slice(-MAX)));
    const { box } = b.md;
    const el = b.el;
    requestAnimationFrame(() => {
      box.scrollTop = from === "start" ? 0 : box.scrollHeight;
      el.classList.toggle("more", from === "start" && (box.scrollHeight > box.clientHeight + 1 || t.length > MAX));
    });
  }
  b.el.classList.remove("gone");
  const bubble = b;
  bubble.timer = window.setTimeout(() => {
    bubble.el.classList.add("gone");
    bubble.timer = window.setTimeout(bubble.remove, 400);
  }, ttl * 1000);
}

/** Says it in full once it is finished: the start of a long answer, not
 *  its last words. */
export function sayDone(key: string, text: string, ttl = 14) {
  say(key, text, "say", ttl, "start");
}

// The markdown's React root goes when the bubble goes back to dots or away;
// later, so a bubble removed while React renders does not unmount mid-render.
function unmount(b: Bubble) {
  const md = b.md;
  if (!md) return;
  b.md = undefined;
  setTimeout(() => md.root.unmount(), 0);
}

/**
 * Draws `node` under what `key` says, from now until its bubble goes; null
 * takes it away. Before the robot has a bubble it waits for the next one.
 */
export function attachToBubble(key: string, node: ReactNode | null) {
  if (node) attached.set(key, node); else attached.delete(key);
  const b = speaking.get(key);
  b?.extra?.render(node ?? null);
  b?.el.classList.toggle("has-extra", !!node);
}

export function hush(key: string) { speaking.get(key)?.remove(); }

const escape = (s: string) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
