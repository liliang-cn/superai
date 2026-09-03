/**
 * A stable hue for a task id.
 *
 * Two tasks running side by side need telling apart at a glance, and a colour
 * derived from the id is one no bookkeeping can get out of step: the same task
 * is the same colour in the card, the segment strip and the trace, in this
 * window and the next one, without anything having to remember what was
 * assigned.
 */
export function hueFor(id: string): number {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return h % 360;
}
