import * as THREE from "three";
import { CSS2DObject } from "three/examples/jsm/renderers/CSS2DRenderer.js";
import { EventsOn } from "../../wailsjs/runtime";
import { actorOf } from "./bubbles";

// A holographic pane over a robot while it works: the tool in use with its
// arguments, how the call came out, and the words it is writing, scrolling as
// they arrive. It is there while the work is, fades when it ends, and is not
// there at all while the robot is idle.

const MAX = 400;
type Pane = { el: HTMLElement; body: HTMLElement; obj: CSS2DObject; actor: THREE.Object3D; text: string; timer: number; slot: number };
const panes = new Map<string, Pane>();

function open(key: string): Pane | null {
  // A worker is drawn as a hive member or, on a machine of its own, as an agent.
  const actor = actorOf(key) ?? (key.startsWith("worker:") ? actorOf("agent:" + key.slice(7)) : undefined);
  if (!actor) return null;
  let p = panes.get(key);
  if (p && p.actor !== actor) { p.obj.removeFromParent(); p.el.remove(); panes.delete(key); p = undefined; }
  if (p) return p;
  // Robots stand close together: each pane takes the lowest free place, right then left of the robot, then higher so they do not cover one another.
  const taken = new Set([...panes.values()].map((q) => q.slot));
  let slot = 0;
  while (taken.has(slot)) slot++;
  const el = document.createElement("div");
  el.className = "wl-hw";
  const inner = document.createElement("div");
  inner.className = "wl-hw-in";
  inner.style.bottom = 8 + Math.floor(slot / 2) * 126 + "px";
  if (slot % 2) inner.classList.add("l");
  const body = document.createElement("div");
  body.className = "wl-hw-body";
  inner.appendChild(body);
  el.appendChild(inner);
  const obj = new CSS2DObject(el);
  const box = new THREE.Box3().setFromObject(actor);
  const top = (box.max.y - actor.getWorldPosition(new THREE.Vector3()).y) / actor.scale.y;
  obj.position.set(0, top + 0.3, 0);
  actor.add(obj);
  p = { el, body, obj, actor, text: "", timer: 0, slot };
  panes.set(key, p);
  return p;
}

function draw(p: Pane) {
  const lines = p.text.split("\n");
  p.body.replaceChildren(...lines.filter((l, i) => l.trim() || i === lines.length - 1).map((l) => {
    const d = document.createElement("div");
    d.className = l.startsWith("▸") ? "tool" : l.startsWith("◂") ? (l.startsWith("◂ ✗") ? "res bad" : "res") : "txt";
    d.textContent = l;
    return d;
  }));
  const c = document.createElement("i");
  c.className = "wl-hw-caret";
  p.body.lastElementChild?.appendChild(c);
  p.body.scrollTo({ top: p.body.scrollHeight, behavior: "smooth" });
}

/** Shows `text` as the pane's content (the tail of what is going on). */
export function holoSet(key: string, text: string) {
  const p = open(key);
  if (!p) return;
  window.clearTimeout(p.timer);
  p.el.classList.remove("gone");
  p.text = text.length > MAX ? text.slice(-MAX) : text;
  draw(p);
}

/** Adds to what the pane shows. */
export function holoAdd(key: string, more: string) {
  const p = open(key);
  holoSet(key, (p?.text ?? "") + more);
}

/** The work is over: the pane fades and goes. */
export function holoEnd(key: string) {
  const p = panes.get(key);
  if (!p) return;
  window.clearTimeout(p.timer);
  p.el.classList.add("gone");
  p.timer = window.setTimeout(() => { p.obj.removeFromParent(); p.el.remove(); if (panes.get(key) === p) panes.delete(key); }, 700);
}

let off: (() => void) | null = null;
/** Follows the hive's orders: a worker's pane is its running task's live text. */
export function startHolo() {
  if (off) return;
  const o = EventsOn("hive:task", (t: any) => {
    if (!t?.worker) return;
    const key = "worker:" + t.worker;
    if (t.state === "running") {
      const s = String(t.live ?? "");
      if (s || t.tool) holoSet(key, s || "▸ " + t.tool);
      else holoSet(key, "");
    } else holoEnd(key);
  });
  off = () => { if (typeof o === "function") o(); off = null; };
}
