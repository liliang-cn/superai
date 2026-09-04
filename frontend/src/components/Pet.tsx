import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PetStage } from "../../wailsjs/go/main/App";

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
 * It also knows where it is. Anything on the page carrying data-pet-spot is a
 * place with a name; the names are reported to the app so a turn can read them
 * back, and a "point" event walks the character to one of them and gives it a
 * line to say when it arrives. The name is resolved to a rectangle at the
 * moment it walks, so a panel that has since moved, resized or closed is found
 * or honestly missed rather than pointed at from memory.
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

/** Sent somewhere on purpose, it goes at a pace that reads as deliberate
 *  rather than at whatever the current state happens to be — being told where
 *  to stand is not the same as milling about. */
const POINT_SPEED = 150;

/** How long it rests at a waypoint before choosing the next one, in ms. */
const REST_MIN = 900;
const REST_MAX = 3600;

/** How long it stays where it was sent before wandering off again. Long
 *  enough to read the bubble and look at what it is standing next to; short
 *  enough that a forgotten instruction does not park it there for good. */
const POINT_HOLD = 20000;

/** How long a speech bubble stays up. */
const SAY_MS = 7000;

/** Kept clear of the edges so the character never half-leaves the window. */
const MARGIN = 8;

/** The stage report's heartbeat. Under the backend's 90s staleness window, so
 *  an open window never looks closed while it is sitting there. */
const STAGE_EVERY = 45000;

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

/** Somewhere the character has been told to stand, resolved each frame. */
type Aim = { spot: string; until: number };

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

/** Keeps a point inside the window, whatever the layout did. */
function inside(p: Pos): Pos {
  return {
    x: Math.max(MARGIN, Math.min(p.x, window.innerWidth - FRAME_W - MARGIN)),
    y: Math.max(MARGIN, Math.min(p.y, window.innerHeight - FRAME_H - MARGIN)),
  };
}

/** Everything on the page that has a name, in DOM order. */
function readSpots(): { name: string; label: string }[] {
  return Array.from(document.querySelectorAll<HTMLElement>("[data-pet-spot]"))
    .filter((el) => el.offsetParent !== null || el.getClientRects().length > 0)
    .map((el) => ({
      name: el.dataset.petSpot ?? "",
      label: el.dataset.petLabel ?? el.dataset.petSpot ?? "",
    }))
    .filter((s) => s.name !== "");
}

/**
 * Where the character should stand to be pointing at a named thing.
 *
 * Beside it rather than on it: the layer draws over the app, and an animal
 * sitting on the button you are describing is worse at describing it than one
 * standing next to it. Left if there is room, otherwise right, so it does not
 * end up wedged against the window edge.
 */
function beside(name: string): Pos | null {
  const el = document.querySelector<HTMLElement>(`[data-pet-spot="${CSS.escape(name)}"]`);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (r.width === 0 && r.height === 0) return null;
  const y = r.top + r.height / 2 - FRAME_H / 2;
  const left = r.left - FRAME_W - 6;
  return inside({ x: left > MARGIN ? left : r.right + 6, y });
}

/**
 * The walk itself, on one requestAnimationFrame loop.
 *
 * Position is written straight to the element's transform rather than kept in
 * state: this runs sixty times a second, and sixty React renders a second to
 * move one sprite would make the rest of the app stutter for no reason. State
 * holds only whether it is walking or standing; which way it faces is a class
 * the loop toggles, for the same reason.
 */
function useWander(
  ref: React.RefObject<HTMLDivElement | null>,
  spriteRef: React.RefObject<HTMLDivElement | null>,
  aimRef: React.RefObject<Aim | null>,
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
    // Which instruction the current target came from, so a new one is picked
    // up and the same one is not re-resolved every frame.
    let aimed: Aim | null = null;

    const place = () => {
      el.style.transform = `translate3d(${pos.x}px, ${pos.y}px, 0)`;
    };
    const face = (leftward: boolean) => {
      if (leftward === facingLeft) return;
      facingLeft = leftward;
      spriteRef.current?.classList.toggle("pet-left", facingLeft);
    };
    place();

    const step = (now: number) => {
      const dt = Math.min((now - last) / 1000, 0.1); // a backgrounded tab can
      last = now;                                    // return a huge delta

      // An instruction outranks the wander, and expires back into it.
      const aim = aimRef.current;
      const held = aim && now < aim.until;
      if (held && aim !== aimed) {
        const at = beside(aim.spot);
        aimed = aim;
        if (at) {
          target = at;
          restUntil = 0;
        }
      } else if (!held && aimed) {
        aimed = null;
        target = randomPoint();
      }

      const speed = held ? POINT_SPEED : SPEED[stateRef.current] ?? SPEED.idle;

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
          // Arrived. Standing beside something it was sent to, it stays there
          // for as long as the instruction lasts rather than drifting off
          // mid-sentence; otherwise it rests and picks somewhere else.
          if (held) {
            restUntil = aim!.until;
          } else {
            restUntil = now + REST_MIN + Math.random() * (REST_MAX - REST_MIN);
            target = randomPoint();
          }
        } else {
          if (!moving) {
            moving = true;
            onMoving(true);
          }
          const travel = Math.min(speed * dt, dist);
          pos = { x: pos.x + (dx / dist) * travel, y: pos.y + (dy / dist) * travel };
          // Only flip on real horizontal movement: a target almost straight
          // above would otherwise make it spin on the spot.
          if (Math.abs(dx) > 4) face(dx < 0);
          place();
        }
      }
      frame = requestAnimationFrame(step);
    };

    frame = requestAnimationFrame(step);

    // The window can shrink out from under it, which would otherwise leave the
    // character parked outside the visible area for good. A thing it was sent
    // to has probably moved too, so its target is re-resolved rather than
    // abandoned.
    const onResize = () => {
      pos = inside(pos);
      const aim = aimRef.current;
      const at = aim && performance.now() < aim.until ? beside(aim.spot) : null;
      target = at ?? randomPoint();
      aimed = at ? aim : null;
      place();
    };
    window.addEventListener("resize", onResize);

    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", onResize);
    };
  }, [ref, spriteRef, aimRef, onMoving]);
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
function useAvatarStream(port: number, onPoint: (spot: string, say: string) => void) {
  const [state, setState] = useState("idle");
  const [row, setRow] = useState(0);
  // Held in a ref so a new handler identity does not tear down the stream and
  // lose whatever was in flight.
  const pointRef = useRef(onPoint);
  pointRef.current = onPoint;

  useEffect(() => {
    if (!port) return;
    const es = new EventSource(`${avatarBase(port)}/avatar/events`);
    es.onmessage = (e) => {
      let ev: { type?: string; state?: string; emotion?: string; spot?: string; text?: string };
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
      } else if (ev.type === "point") {
        pointRef.current(ev.spot || "", ev.text || "");
      }
    };
    // A dropped stream is not worth showing: the character keeps whatever it
    // was doing, and EventSource reconnects on its own.
    return () => es.close();
  }, [port]);

  return { state, row };
}

/**
 * Telling the app what is on the page.
 *
 * On mount, whenever the view changes, after a resize settles, and on a slow
 * heartbeat — the last one is what makes a closed window distinguishable from
 * a quiet one, which is the whole reason pet_where can answer "nobody is
 * looking" instead of inventing a page.
 */
function useStageReport(view: string) {
  useEffect(() => {
    let alive = true;
    const send = () => {
      if (!alive) return;
      // A page that has just changed has not finished laying out; one frame is
      // enough for the panels that were about to mount to be findable.
      requestAnimationFrame(() => {
        if (alive) PetStage(view, readSpots()).catch(() => {});
      });
    };
    send();
    const beat = setInterval(send, STAGE_EVERY);
    let settle: number | undefined;
    const onResize = () => {
      window.clearTimeout(settle);
      settle = window.setTimeout(send, 400);
    };
    window.addEventListener("resize", onResize);
    return () => {
      alive = false;
      clearInterval(beat);
      window.clearTimeout(settle);
      window.removeEventListener("resize", onResize);
    };
  }, [view]);
}

export default function Pet({
  port,
  view,
  onDismiss,
}: {
  port: number;
  /** Which page is open, for the stage report. */
  view: string;
  onDismiss: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const spriteRef = useRef<HTMLDivElement>(null);
  const aimRef = useRef<Aim | null>(null);
  const [character, setCharacter] = useState(readSavedChar);
  const [moving, setMoving] = useState(true);
  const [bubble, setBubble] = useState("");

  const sayTimer = useRef<number | undefined>(undefined);
  const onPoint = useCallback((spot: string, say: string) => {
    aimRef.current = spot ? { spot, until: performance.now() + POINT_HOLD } : null;
    window.clearTimeout(sayTimer.current);
    setBubble(say);
    if (say) sayTimer.current = window.setTimeout(() => setBubble(""), SAY_MS);
  }, []);

  const { state, row } = useAvatarStream(port, onPoint);

  useWander(ref, spriteRef, aimRef, state, setMoving);
  useStageReport(view);

  useEffect(() => () => window.clearTimeout(sayTimer.current), []);

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
      {/* The actor carries the position, the sprite inside it carries the
          mirror. They were one element and the flip had to live in the same
          inline transform as the walk; splitting them is what lets a speech
          bubble hang off the character without being written backwards. */}
      <div ref={ref} className="pet-actor">
        {bubble && <div className="pet-bubble">{bubble}</div>}
        <div
          ref={spriteRef}
          className={`pet pet-${state}${moving ? " pet-walking" : ""}`}
          style={style}
          onClick={cycle}
          onDoubleClick={onDismiss}
          title={`${state} — click to change, double-click to send away`}
          role="img"
          aria-label={`SuperAI ${character}, ${state}`}
        />
      </div>
    </div>
  );
}
