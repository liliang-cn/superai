// An agent that changes host between two plans (an HA service failing over:
// openclaw going from sds-a to sds-b) walks from where it stood to where it
// stands now, rather than vanishing and appearing. Pure: no three.js here, so
// the tests can load it.

/** Where a robot stands, in world coordinates, and for whom. `key` is what
 *  the agent is (its kind and name); `host` is the plot it stands on. */
export interface Stand { key: string; host: string; x: number; y: number; z: number }
export interface Move { key: string; from: Stand; to: Stand }

/**
 * The robots that changed host from one plan to the next: the same agent,
 * once in each, on another host. An agent seen on more than one host in
 * either plan (a coding CLI installed everywhere) is not one robot, so it
 * never travels.
 */
export function movesOf(before: Stand[], after: Stand[]): Move[] {
  const once = (xs: Stand[]) => {
    const m = new Map<string, Stand | null>();
    for (const s of xs) m.set(s.key, m.has(s.key) ? null : s);
    return m;
  };
  const was = once(before);
  const out: Move[] = [];
  for (const [key, to] of once(after)) {
    const from = was.get(key);
    if (from && to && from.host !== to.host) out.push({ key, from, to });
  }
  return out;
}

/** How long a trip takes: two seconds across a neighbour, four at most. */
export function tripTime(from: Stand, to: Stand) {
  const d = Math.hypot(to.x - from.x, to.z - from.z);
  return Math.min(4, Math.max(2, 1.6 + d / 16));
}

/** Gently off, steady across, gently in. */
export const easeTrip = (t: number) => (1 - Math.cos(Math.PI * Math.min(1, Math.max(0, t)))) / 2;

const HOP = 0.32;

/**
 * Where a traveller is a fraction `t` of the way: eased along the ground, in
 * short hops of about two units each, landing on the last.
 */
export function along(from: { x: number; y: number; z: number }, to: { x: number; y: number; z: number }, t: number) {
  const e = easeTrip(t);
  const d = Math.hypot(to.x - from.x, to.z - from.z);
  const hops = Math.max(3, Math.round(d / 2.2));
  const lift = t >= 1 ? 0 : Math.abs(Math.sin(e * hops * Math.PI)) * HOP;
  return {
    x: from.x + (to.x - from.x) * e,
    y: from.y + (to.y - from.y) * e + lift,
    z: from.z + (to.z - from.z) * e,
  };
}

/** The turn from `a` to `b` a fraction `k` of the way, the short way round. */
export function turn(a: number, b: number, k: number) {
  let d = (b - a) % (Math.PI * 2);
  if (d > Math.PI) d -= Math.PI * 2;
  if (d < -Math.PI) d += Math.PI * 2;
  return a + d * Math.min(1, Math.max(0, k));
}
