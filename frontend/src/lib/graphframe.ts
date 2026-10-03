// CortexDB's live view, made to sit in this app: the app's ground, the app's
// colours. It draws its glow with an additive bloom pass, which on a light
// ground lifts everything to white — so by day the glow is off and the ground
// is the page's own; by night it keeps both. Its node colours include violet;
// the frame is same-origin (/graph/ in a browser, the linked queen's /graph/
// in the desktop window), so they are replaced from here.

const LIGHT_BG = "f6f4ef";
const DARK_BG = "05070f";

/** The live view's address with the look for the theme. */
export function graphLook(src: string, day: boolean, extra = ""): string {
  const q = day ? `bg=${LIGHT_BG}&glow=0` : `bg=${DARK_BG}`;
  return `${src}${src.includes("?") ? "&" : "?"}${q}${extra ? "&" + extra : ""}`;
}

const PALETTE: Record<string, string> = {
  entity: "#2f7fa3", concept: "#c98208", memory: "#12805c", knowledge: "#e0a106",
  document: "#d4521d", person: "#cf3655", project: "#2563d9", organization: "#0f8a8a",
  location: "#b06a00", event: "#d0313f", chunk: "#8a96a3",
};

/** Gives the live view the app's node colours: no violet, darker by day. */
export function recolorGraph(frame: HTMLIFrameElement | null, day: boolean) {
  try {
    const w = frame?.contentWindow as any;
    if (!w || !w.NAMED) return;
    Object.assign(w.NAMED, PALETTE);
    w.colorOf = (t: string) => {
      if (!t) return "#7c8ba1";
      if (w.NAMED[t]) return w.NAMED[t];
      let h = 0;
      for (let i = 0; i < t.length; i++) h = (h * 31 + t.charCodeAt(i)) % 360;
      if (h >= 250 && h < 330) h = (h + 110) % 360; // violet and magenta skipped
      return `hsl(${h},62%,${day ? 44 : 62}%)`;
    };
    if (w.G && w.nodeColor) w.G.nodeColor(w.nodeColor);
    if (w.G && typeof w.rebuildLegend === "function") w.rebuildLegend(w.G.graphData().nodes);
  } catch {
    // Another origin: its look is its own.
  }
}
