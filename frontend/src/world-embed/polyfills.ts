// What an older WebView (Android 10's Chrome 74) lacks that the scene uses.
// Imported before anything else in the embed.

type Radii = number | DOMPointInit | (number | DOMPointInit)[];

function first(r: Radii | undefined): number {
  const v = Array.isArray(r) ? r[0] : r;
  if (typeof v === "number") return v;
  return v?.x ?? 0;
}

const proto = (typeof CanvasRenderingContext2D !== "undefined" ? CanvasRenderingContext2D.prototype : null) as
  (CanvasRenderingContext2D & { roundRect?: unknown }) | null;
if (proto && typeof proto.roundRect !== "function") {
  // One radius for all four corners: all the scene asks for.
  (proto as unknown as { roundRect: (x: number, y: number, w: number, h: number, r?: Radii) => void }).roundRect = function (
    this: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r?: Radii,
  ) {
    const k = Math.max(0, Math.min(first(r), Math.abs(w) / 2, Math.abs(h) / 2));
    this.moveTo(x + k, y);
    this.arcTo(x + w, y, x + w, y + h, k);
    this.arcTo(x + w, y + h, x, y + h, k);
    this.arcTo(x, y + h, x, y, k);
    this.arcTo(x, y, x + w, y, k);
    this.closePath();
  };
}

export {};
