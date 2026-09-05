import { useEffect, useState } from "react";

/**
 * How much room the window has, in the three shapes the layout has answers for.
 *
 * The stylesheet already handled phones — one breakpoint at 640, where the
 * sidebar becomes a drawer. What it never handled was the range in between,
 * which on a phone does not exist and on a desktop is wherever somebody drags
 * the corner to. Measured at 860px: the sidebar kept its 232px and the panel
 * its 330px, leaving the conversation about 300 — "New chat" wrapped onto two
 * lines and the message box was narrower than the placeholder in it.
 *
 * It is a hook rather than a media query because the two things that have to
 * give way are React state, not CSS. The sidebar and the side panel each
 * render a different tree when collapsed; a stylesheet can only hide what is
 * already there.
 *
 * What it must not do is overwrite what the person chose. A window dragged
 * narrow and back again has to come back the way they left it, so nothing here
 * writes to the saved preference — the narrow layout is applied over it and
 * lifted when the room returns.
 */

export type Room = "wide" | "narrow" | "phone";

/** Below this the sidebar is a drawer; the stylesheet's own breakpoint. */
const PHONE = 640;

/**
 * Below this the two chrome columns cannot both keep their full width.
 *
 * 1100 rather than something tighter: 232 of sidebar plus 330 of panel is 562,
 * and a conversation wants at least the same again to read as one. The number
 * is where the composer stops being wider than its own placeholder.
 */
const NARROW = 1100;

function roomFor(width: number): Room {
  if (width <= PHONE) return "phone";
  if (width < NARROW) return "narrow";
  return "wide";
}

export function useRoom(): Room {
  const [room, setRoom] = useState<Room>(() =>
    roomFor(typeof window === "undefined" ? NARROW + 1 : window.innerWidth),
  );
  useEffect(() => {
    // The resize of a desktop window is continuous — a drag fires this on
    // every frame — but the state only changes at two thresholds, so setting
    // it to what it already is costs nothing and React drops the render.
    const onResize = () => setRoom(roomFor(window.innerWidth));
    window.addEventListener("resize", onResize);
    onResize();
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return room;
}
