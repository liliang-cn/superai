// The boot screen in index.html, taken down once there is something to see:
// the 3D world's first frame, or the page itself when it has no world. It
// stays long enough for its lines to finish drawing, never longer than the
// app takes, and goes regardless after a while.

const MIN_MS = 1400;
const MAX_MS = 6000;
let done = false;

export function bootDone() {
  if (done) return;
  const el = document.getElementById("boot");
  if (!el) { done = true; return; }
  const wait = Math.max(0, MIN_MS - performance.now());
  if (wait > 0) { window.setTimeout(bootDone, wait); return; }
  done = true;
  el.classList.add("gone");
  window.setTimeout(() => el.remove(), 700);
}

/**
 * After the first render. The 3D desk mounts a little later than the page
 * (settings are read first), so the page is watched for a while: with the
 * desk in it, the engine's first frame takes the screen down; without it,
 * the page as it stands does once it has had time to show.
 */
export function bootAfterMount() {
  window.setTimeout(bootDone, MAX_MS);
  const start = performance.now();
  const look = () => {
    if (done) return;
    if (document.querySelector(".dk-world:not(.dk-flat), canvas[data-engine]")) return; // engine.ts takes it down
    if (performance.now() - start > 1500 && document.getElementById("root")?.childElementCount) { bootDone(); return; }
    window.setTimeout(look, 120);
  };
  requestAnimationFrame(look);
}
