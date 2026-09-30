// What happens in a hive, as things to draw.
//
// Every look the hive can be shown in — the orbits, the mycelium, flat or in
// three dimensions — tells the same story from the same inputs: who is in the
// hive, which orders are running, and the flicker of what each is doing. This is
// that story, kept apart from any way of drawing it. A renderer asks where two
// things are and draws what this file says is travelling between them.
//
// Nothing here is React state. A renderer's frame loop reads these arrays, and an
// event costs one push onto one of them.

export interface StageTask {
  id: string;
  worker: string;
  dir: "out" | "in" | "peer";
  /** Who asked, when it was a worker and not the queen. */
  from?: string;
  state: "running" | "done" | "failed" | "cancelled";
  phase?: string;
  tool?: string;
  tools: number;
  started_at: string;
}

/** One flicker while an order is carried out: a tool called, a result back, the
 *  model thinking, some text written — or a message between two members, which
 *  names its two ends itself (src, dst: stage ids, QUEEN and SELF included). */
export interface StagePulse {
  task: string;
  worker: string;
  from?: string;
  dir: "out" | "in" | "peer";
  kind: "thinking" | "tool" | "result" | "text" | "message";
  tool?: string;
  bytes?: number;
  src?: string;
  dst?: string;
  text?: string;
}

/** A message's first words, for the tag beside whoever sent it. */
export function mailTag(text?: string): string {
  const t = (text || "").replace(/\s+/g, " ").trim();
  return `✉ ${t.length > 20 ? t.slice(0, 19) + "…" : t}`;
}

export interface StageHandle {
  pulse: (p: StagePulse) => void;
}

export interface StageWorker {
  name: string;
  state: "live" | "lost";
  /** What is behind it when it is not a SuperAI, e.g. "cli · claude". */
  engine?: string;
}

export interface StageProps {
  role: "" | "queen" | "worker";
  self: string;
  workers: StageWorker[];
  tasks: StageTask[];
  /** False until the first batch has loaded. That batch is history, and is not
   *  replayed as if every old order had just been given. */
  ready: boolean;
}

/** The queen, and this instance when it is a worker looking at itself. */
export const QUEEN = "\u0000queen";
export const SELF = "\u0000self";

export const short = (n: string) => n.replace(/^superai-/, "");
export const ease = (t: number) => (t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2);

/** Something travelling between two nodes. `size` is relative: 1 is an order. */
export type Flight = { from: string; to: string; t0: number; dur: number; color: string; size: number; alpha: number };
/** Light arriving somewhere. `big` is an order finishing, the rest are pulses. */
export type Landing = { at: string; t0: number; color: string; big: boolean };
/** A tool's name, shown briefly beside whoever called it. */
export type Tag = { at: string; text: string; t0: number; color: string };
/** A tool called: a small burst. */
export type Spark = { at: string; t0: number; a: number };

type Colors = { accent: string; green: string; red: string; amber: string; mail?: string };

/** The running order on a node, if there is one. */
export function runningOn(id: string, role: string, tasks: StageTask[]): StageTask | undefined {
  return tasks.find(
    (k) =>
      k.state === "running" &&
      (id === SELF ? role === "worker" && k.dir === "in" : k.worker === id && (k.dir === "out" || k.dir === "peer")),
  );
}

/** What a node is doing, in a few words: "thinking 12s", "⚙ bash 30s". */
export function doing(task: StageTask, withTime = true): string {
  const what = task.phase === "tool" ? `⚙ ${task.tool || "tool"}` : task.phase === "writing" ? "writing" : "thinking";
  if (!withTime) return what;
  const secs = Math.max(0, Math.round((Date.now() - Date.parse(task.started_at)) / 1000));
  return `${what} ${secs}s`;
}

export class HiveFx {
  flights: Flight[] = [];
  landings: Landing[] = [];
  tags: Tag[] = [];
  sparks: Spark[] = [];
  // What has been animated already, so a re-render of the same task is not a
  // second flight. `null` until the first batch, which is history.
  private seen: Map<string, { state: string; tools: number }> | null = null;

  /** A pulse off the stream: one bolt from whoever is doing the work to whoever
   *  is waiting on it — the queen, or for a peer order the worker that asked. */
  pulse(p: StagePulse, role: string, c: Colors, now = performance.now()) {
    if (p.kind === "message") {
      // A message is not work: slower, and in a colour of its own, so a note
      // passed between two members reads differently from the traffic of an order.
      if (!p.src || !p.dst) return;
      const color = c.mail || c.accent;
      this.flights.push({ from: p.src, to: p.dst, t0: now, dur: 1100, color, size: 1.1, alpha: 1 });
      this.tags.push({ at: p.src, text: mailTag(p.text), t0: now, color });
      this.landings.push({ at: p.dst, t0: now + 1100, color, big: true });
      return;
    }
    let from: string;
    let to: string;
    if (role === "worker") {
      if (p.dir !== "in") return;
      from = SELF;
      to = QUEEN;
    } else if (role === "queen") {
      from = p.worker;
      to = p.dir === "peer" && p.from ? p.from : QUEEN;
    } else {
      return;
    }
    const grow = Math.min(3, Math.log10(1 + (p.bytes ?? 0)));
    let f: Flight;
    switch (p.kind) {
      case "tool":
        f = { from, to, t0: now, dur: 620, color: c.amber, size: 0.9, alpha: 1 };
        this.tags.push({ at: from, text: p.tool || "tool", t0: now, color: c.amber });
        this.sparks.push({ at: from, t0: now, a: Math.random() * Math.PI * 2 });
        break;
      case "result":
        f = { from, to, t0: now, dur: 560, color: c.green, size: 0.6 + grow * 0.12, alpha: 1 };
        break;
      case "text":
        f = { from, to, t0: now, dur: 460, color: c.accent, size: 0.4 + grow * 0.06, alpha: 0.8 };
        break;
      default:
        f = { from, to, t0: now, dur: 800, color: c.accent, size: 0.35, alpha: 0.4 };
    }
    this.flights.push(f);
    if (this.flights.length > 120) this.flights.splice(0, this.flights.length - 120);
    if (p.kind !== "thinking") this.landings.push({ at: to, t0: now + f.dur, color: f.color, big: false });
  }

  /** The task list changed: new orders fly out, finished ones fly home. */
  tasks(tasks: StageTask[], role: string, c: Colors & { red: string }, now = performance.now()) {
    const target = (t: StageTask) => (role === "worker" ? SELF : t.worker);
    const origin = (t: StageTask) => (t.dir === "peer" && t.from ? t.from : QUEEN);
    const known = this.seen;
    if (known === null) {
      this.seen = new Map(tasks.map((t) => [t.id, { state: t.state, tools: t.tools }]));
      return;
    }
    for (const t of tasks) {
      // A worker's own view has no peers on it to draw.
      if (role === "worker" && t.dir === "peer") {
        known.set(t.id, { state: t.state, tools: t.tools });
        continue;
      }
      const before = known.get(t.id);
      if (!before) {
        this.flights.push({ from: origin(t), to: target(t), t0: now, dur: 1000, color: c.accent, size: 1, alpha: 1 });
        this.landings.push({ at: target(t), t0: now + 1000, color: c.accent, big: false });
      } else if (before.state === "running" && t.state !== "running") {
        const color = t.state === "done" ? c.green : t.state === "failed" ? c.red : c.amber;
        this.flights.push({ from: target(t), to: origin(t), t0: now, dur: 1000, color, size: 1, alpha: 1 });
        this.landings.push({ at: origin(t), t0: now + 950, color, big: true });
      }
      known.set(t.id, { state: t.state, tools: t.tools });
    }
  }

  /** Drops what has finished playing. Call once a frame. */
  prune(now: number) {
    this.flights = this.flights.filter((f) => now - f.t0 < f.dur);
    this.landings = this.landings.filter((l) => now - l.t0 < (l.big ? 1100 : 600));
    this.tags = this.tags.filter((g) => now - g.t0 < 1600);
    this.sparks = this.sparks.filter((s) => now - s.t0 < 700);
  }
}

/** A stable number in [0, 1) from a name, so jitter is the same every frame and
 *  on every screen. */
export function hash01(s: string, salt = 0): number {
  let h = 2166136261 ^ salt;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return ((h >>> 0) % 100000) / 100000;
}
