// The colours the hive stages draw with.
//
// Read from the page's own theme variables, so a finish or a switch to light
// changes the stage with everything else. This file is the one thing the two
// hive stages do not share between the desktop app and the console: each has its
// own tokens.
export function palette() {
  const css = getComputedStyle(document.documentElement);
  const v = (n: string, d: string) => css.getPropertyValue(n).trim() || d;
  const light = document.documentElement.dataset.theme === "light";
  return {
    light,
    accent: v("--accent", "#d2793f"),
    green: v("--green", "#5ec48f"),
    red: v("--red", "#ef6a76"),
    amber: v("--amber", "#e0a64f"),
    dim: v("--text-2", "#8f887c"),
    text: v("--text-0", "#ece7dd"),
    border: v("--border", "#2e2925"),
    panel: v("--bg-1", "#1a1714"),
    // Halos read as light on a dark ground and as smudges on a light one.
    glow: light ? 0.35 : 1,
  };
}
export type Palette = ReturnType<typeof palette>;
