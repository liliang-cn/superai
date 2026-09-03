import React, { useEffect, useMemo, useRef, useState } from "react";

/**
 * The pixel character, loose in the window.
 *
 * The avatar has existed for a while as a page on its own port, which is the
 * right shape for an external renderer and the wrong shape for looking at: it
 * means opening a second window to see whether anything is happening. This is
 * the same character in the app itself — it wanders the whole surface, and it
 * is driven by the same event stream, so what it is doing is what the agent is
 * doing.
 *
 * It never takes a click except on the character itself. The overlay is
 * pointer-events: none, so everything underneath stays usable — an animal you
 * cannot type through would be a toy you turn off after a minute.
 */

/** The sheets that ship, in the order clicking the character cycles them. */
const CHARS = ["cat", "bunny", "robot"];

/** The sheet's rows. An unknown mood leaves the face alone rather than
 *  snapping it to neutral — the tag is optional and models invent their own. */
const ROWS: Record<string, number> = {
  neutral: 0, happy: 1, sad: 2, thinking: 3, excited: 4,
  sleepy: 5, confused: 6, love: 7, angry: 8, surprised: 9,
};

/** One frame is 20x24. Drawn at 3x here — big enough to read the face, small
 *  enough that it reads as something wandering across a page rather than a
 *  character standing on top of it. */
const ZOOM = 3;
const FRAME_W = 20 * ZOOM;
const FRAME_H = 24 * ZOOM;
const SHEET_W = 20 * 8 * ZOOM;  // four standing frames, then four walking
const SHEET_H = 24 * 10 * ZOOM; // ten emotions

/** How fast it crosses the window, in pixels per second, by state. Working is
 *  the state with somewhere to be; waiting and error do not move at all. */
const SPEED: Record<string, number> = {
  idle: 38,
  thinking: 30,
  speaking: 34,
  working: 90,
  waiting: 0,
  error: 0,
};

/** How long it rests at a waypoint before choosing the next one, in ms. */
const REST_MIN = 900;
const REST_MAX = 3600;

/** Kept clear of the edges so the character never half-leaves the window. */
const MARGIN = 8;

const STORAGE_KEY = "superai-pet-char";

/**
 * Where the avatar bridge is, from this page.
 *
 * In the desktop window the app is not served over HTTP at all, so the bridge
 * is reached on loopback by port. Served — a browser tab, a phone — loopback is
 * the *viewer's* machine: on a phone 127.0.0.1:47615 is the phone, which is why
 * letting the pet out over the network did nothing at all. There the app
 * proxies /avatar itself, same origin, behind the same session.
 */
const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);
const avatarBase = (port: number) => (served ? "" : `http://127.0.0.1:${port}`);

type Pos = { x: number; y: number };

function readSavedChar(): string {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved && CHARS.includes(saved)) return saved;
  } catch {
    // Private window, or site data switched off. The default is fine.
  }
  return CHARS[0];
}

function randomPoint(): Pos {
  return {
    x: MARGIN + Math.random() * Math.max(0, window.innerWidth - FRAME_W - MARGIN * 2),
    y: MARGIN + Math.random() * Math.max(0, window.innerHeight - FRAME_H - MARGIN * 2),
  };
}

/**
 * The walk itself, on one requestAnimationFrame loop.
 *
 * Position is written straight to the element's transform rather than kept in
 * state: this runs sixty times a second, and sixty React renders a second to
 * move one sprite would make the rest of the app stutter for no reason. State
 * holds only what actually changes rarely — which way it faces, and whether it
 * is walking or standing.
 */
function useWander(
  ref: React.RefObject<HTMLDivElement | null>,
  state: string,
  onMoving: (moving: boolean) => void,
) {
  // Read inside the loop so a state change takes effect on the next frame
  // rather than restarting the animation.
  const stateRef = useRef(state);
  stateRef.current = state;

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    let pos = randomPoint();
    let target = randomPoint();
    let restUntil = 0;
    let facingLeft = false;
    let moving = true;
    let frame = 0;
    let last = performance.now();

    // Facing is part of this transform rather than a class, because an inline
    // transform overrides the stylesheet's entirely — a `.pet-left { scaleX(-1) }`
    // rule would simply never apply while the loop is writing a position here.
    const place = () => {
      el.style.transform =
        `translate3d(${pos.x}px, ${pos.y}px, 0) scaleX(${facingLeft ? -1 : 1})`;
    };
    place();

    const step = (now: number) => {
      const dt = Math.min((now - last) / 1000, 0.1); // a backgrounded tab can
      last = now;                                    // return a huge delta

      const speed = SPEED[stateRef.current] ?? SPEED.idle;

      if (now < restUntil || speed === 0) {
        if (moving) {
          moving = false;
          onMoving(false);
        }
      } else {
        const dx = target.x - pos.x;
        const dy = target.y - pos.y;
        const dist = Math.hypot(dx, dy);
        if (dist < 2) {
          // Arrived. Stand about for a moment, then pick somewhere else.
          restUntil = now + REST_MIN + Math.random() * (REST_MAX - REST_MIN);
          target = randomPoint();
        } else {
          if (!moving) {
            moving = true;
            onMoving(true);
          }
          const travel = Math.min(speed * dt, dist);
          pos = { x: pos.x + (dx / dist) * travel, y: pos.y + (dy / dist) * travel };
          // Only flip on real horizontal movement: a target almost straight
          // above would otherwise make it spin on the spot.
          if (Math.abs(dx) > 4) {
            facingLeft = dx < 0;
          }
          place();
        }
      }
      frame = requestAnimationFrame(step);
    };

    frame = requestAnimationFrame(step);

    // The window can shrink out from under it, which would otherwise leave the
    // character parked outside the visible area for good.
    const onResize = () => {
      pos = {
        x: Math.min(pos.x, Math.max(MARGIN, window.innerWidth - FRAME_W - MARGIN)),
        y: Math.min(pos.y, Math.max(MARGIN, window.innerHeight - FRAME_H - MARGIN)),
      };
      target = randomPoint();
      place();
    };
    window.addEventListener("resize", onResize);

    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", onResize);
    };
  }, [ref, onMoving]);
}

/**
 * The avatar's own event stream, read from the app.
 *
 * Cross-origin on purpose: the avatar server is a separate listener on its own
 * port, and its /avatar/events handler already answers with
 * Access-Control-Allow-Origin: *. Reading it rather than the app's chat events
 * means the character reacts to exactly what an external renderer would react
 * to — one protocol, and no second definition of "working" to keep in step.
 */
function useAvatarStream(port: number) {
  const [state, setState] = useState("idle");
  const [row, setRow] = useState(0);

  useEffect(() => {
    if (!port) return;
    const es = new EventSource(`${avatarBase(port)}/avatar/events`);
    es.onmessage = (e) => {
      let ev: { type?: string; state?: string; emotion?: string };
      try {
        ev = JSON.parse(e.data);
      } catch {
        return;
      }
      if (ev.type === "state") {
        setState(ev.state || "idle");
      } else if (ev.type === "emotion") {
        const r = ROWS[ev.emotion || ""];
        if (r !== undefined) setRow(r);
      }
    };
    // A dropped stream is not worth showing: the character keeps whatever it
    // was doing, and EventSource reconnects on its own.
    return () => es.close();
  }, [port]);

  return { state, row };
}

export default function Pet({ port, onDismiss }: { port: number; onDismiss: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [character, setCharacter] = useState(readSavedChar);
  const [moving, setMoving] = useState(true);
  const { state, row } = useAvatarStream(port);

  useWander(ref, state, setMoving);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, character);
    } catch {
      // The choice simply will not persist. Not worth saying so.
    }
  }, [character]);

  const style = useMemo(
    () => ({
      width: FRAME_W,
      height: FRAME_H,
      backgroundImage: `url("${avatarBase(port)}/avatar/sprites/${character}.png")`,
      backgroundSize: `${SHEET_W}px ${SHEET_H}px`,
      backgroundPositionY: `${-row * FRAME_H}px`,
      // The frame width the walk keyframes step by, so the stylesheet and the
      // constants above cannot disagree about how wide a frame is.
      "--pet-frame": `${FRAME_W}px`,
    } as React.CSSProperties),
    [port, character, row],
  );

  const cycle = () => setCharacter(CHARS[(CHARS.indexOf(character) + 1) % CHARS.length]);

  return (
    <div className="pet-layer">
      <div
        ref={ref}
        className={`pet pet-${state}${moving ? " pet-walking" : ""}`}
        style={style}
        onClick={cycle}
        onDoubleClick={onDismiss}
        title={`${state} — click to change, double-click to send away`}
        role="img"
        aria-label={`SuperAI ${character}, ${state}`}
      />
    </div>
  );
}
