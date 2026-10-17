// "Open the conversation with the queen, ready to type": asked for by the
// round chat button from any page, answered by the home's conversation —
// now if it is on screen, or when it next mounts if the page is still
// changing to it.

let wanted = false;

export function askToCompose() {
  wanted = true;
  window.dispatchEvent(new Event("superai:compose"));
}

/** Whether a request is waiting; taking it clears it. */
export function takeCompose(): boolean {
  const w = wanted;
  wanted = false;
  return w;
}
