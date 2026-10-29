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

/** After the first render: a page with no 3D world is ready as it stands. */
export function bootAfterMount() {
  window.setTimeout(bootDone, MAX_MS);
  // Two frames: the first commit has laid out; the second has painted.
  requestAnimationFrame(() => requestAnimationFrame(() => {
    // The 3D desk takes it down itself at its first frame (engine.ts).
    if (!document.querySelector(".dk-world, canvas[data-engine]")) bootDone();
  }));
}
