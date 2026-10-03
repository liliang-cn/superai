// CortexDB's live view, made to sit in this app. Since CortexDB 2.117 the view
// has its own light and dark modes (light turns its bloom off, which on a
// light ground only greys everything), an Ember family close to the app's
// copper, and no violet in its palette. So the look is asked for through its
// URL parameters, and nothing reaches into the frame's document or globals:
// the CSS that used to be injected and the node recolouring were written for
// panels and functions the view no longer has.

const LIGHT_BG = "f6f4ef";
const DARK_BG = "05070f";

/** The live view's address with the look for the theme: the app's ground,
 * Ember, light by day and dark by night. */
export function graphLook(src: string, day: boolean, extra = ""): string {
  const q = `theme=ember&mode=${day ? "light" : "dark"}&bg=${day ? LIGHT_BG : DARK_BG}`;
  return `${src}${src.includes("?") ? "&" : "?"}${q}${extra ? "&" + extra : ""}`;
}
