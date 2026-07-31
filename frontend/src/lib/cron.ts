/**
 * Turning a schedule into words, and words into a schedule.
 *
 * The backend speaks cron; a person thinking "every morning at eight" does not.
 * These helpers let the UI name a schedule in plain language and show when it
 * would actually fire *before* the user commits to it — after creation the
 * backend's own next_run is the truth.
 *
 * This deliberately understands a smaller dialect than the backend
 * (robfig/cron, with an optional leading seconds field and @descriptors).
 * Anything it cannot evaluate is reported as "unknown", never as invalid:
 * refusing an expression the backend would have accepted is the one failure
 * mode worth designing out.
 */

export type WhenMode =
  | "daily"
  | "weekdays"
  | "weekly"
  | "monthly"
  | "hours"
  | "minutes"
  | "cron";

/** The schedule builder's state — everything needed to compose an expression. */
export interface When {
  mode: WhenMode;
  /** "HH:MM", straight out of <input type="time">. */
  time: string;
  /** Cron weekday numbers (0 = Sunday) for the "certain days" mode. */
  days: number[];
  /** Day of month for the monthly mode. */
  dom: number;
  everyHours: number;
  everyMinutes: number;
  /** Which minute of the hour the "every few hours" mode fires on. */
  minute: number;
  /** Raw expression, for the mode that lets people type one. */
  expr: string;
}

export const DEFAULT_WHEN: When = {
  mode: "daily",
  time: "08:00",
  days: [1],
  dom: 1,
  everyHours: 4,
  everyMinutes: 30,
  minute: 0,
  expr: "0 8 * * *",
};

export type CronPreview =
  | { kind: "ok"; runs: Date[] }
  | { kind: "invalid"; why: string }
  | { kind: "unknown" };

/** Sunday-first, matching cron's own weekday numbering. */
export const WEEKDAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
/** Monday-first for display, because that is how a week reads to most people. */
export const WEEKDAY_ORDER = [1, 2, 3, 4, 5, 6, 0];

const DESCRIPTORS: Record<string, string> = {
  "@yearly": "0 0 1 1 *",
  "@annually": "0 0 1 1 *",
  "@monthly": "0 0 1 * *",
  "@weekly": "0 0 * * 0",
  "@daily": "0 0 * * *",
  "@midnight": "0 0 * * *",
  "@hourly": "0 * * * *",
};

const MONTH_NAMES = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
const DOW_NAMES = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];

interface Fields {
  minute: number[];
  hour: number[];
  dom: number[];
  month: number[];
  dow: number[];
  /** Cron's odd rule: day-of-month and weekday are OR'd only when both are set. */
  domAny: boolean;
  dowAny: boolean;
}

type Parsed =
  | { kind: "ok"; fields: Fields }
  | { kind: "invalid"; why: string }
  | { kind: "unknown" };

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** Resolve one token of a field to a number, accepting the usual name forms. */
function tokenValue(tok: string, names: string[] | null): number | null {
  const t = tok.trim().toLowerCase();
  if (t === "") return null;
  if (/^\d+$/.test(t)) return Number(t);
  if (names) {
    const i = names.indexOf(t.slice(0, 3));
    if (i >= 0) return i + (names === MONTH_NAMES ? 1 : 0);
  }
  return null;
}

function parseField(spec: string, min: number, max: number, names: string[] | null): number[] | null {
  const out = new Set<number>();
  for (const part of spec.split(",")) {
    const slash = part.split("/");
    if (slash.length > 2) return null;
    let step = 1;
    if (slash.length === 2) {
      if (!/^\d+$/.test(slash[1])) return null;
      step = Number(slash[1]);
      if (step < 1) return null;
    }
    const range = slash[0];
    let lo: number;
    let hi: number;
    if (range === "*") {
      lo = min;
      hi = max;
    } else {
      const bounds = range.split("-");
      if (bounds.length > 2) return null;
      const a = tokenValue(bounds[0], names);
      if (a === null) return null;
      lo = a;
      if (bounds.length === 2) {
        const b = tokenValue(bounds[1], names);
        if (b === null) return null;
        hi = b;
      } else {
        // "5/10" means "from 5 to the end of the field", while a bare "5" is
        // just the one value.
        hi = slash.length === 2 ? max : a;
      }
    }
    if (lo < min || hi > max || lo > hi) return null;
    for (let v = lo; v <= hi; v += step) out.add(v);
  }
  return out.size > 0 ? [...out].sort((a, b) => a - b) : null;
}

/** Normalise to a single-space 5-field expression, resolving named shorthands. */
function normalize(raw: string): string {
  return (raw || "").trim().replace(/\s+/g, " ");
}

function expandDescriptor(expr: string): string | null {
  return DESCRIPTORS[expr.toLowerCase()] ?? null;
}

function parse(raw: string): Parsed {
  const expr = normalize(raw);
  if (expr === "") return { kind: "invalid", why: "Pick when this should run." };
  if (expr.startsWith("@")) {
    const alias = expandDescriptor(expr);
    if (alias) return parse(alias);
    // "@every 30m" and friends count from when the scheduler started, which is
    // not something the UI can know. Left to the backend.
    return { kind: "unknown" };
  }
  const parts = expr.split(" ");
  // Six fields is the seconds dialect the backend also accepts. Sub-minute
  // firing is not modelled here, so hand it over rather than call it wrong.
  if (parts.length === 6 || parts.length === 7) return { kind: "unknown" };
  if (parts.length !== 5) {
    return {
      kind: "invalid",
      why: "A cron expression has 5 parts: minute hour day month weekday.",
    };
  }
  const [mi, ho, dm, mo, dw] = parts;
  const minute = parseField(mi, 0, 59, null);
  if (!minute) return { kind: "invalid", why: `Not a valid minute: "${mi}".` };
  const hour = parseField(ho, 0, 23, null);
  if (!hour) return { kind: "invalid", why: `Not a valid hour: "${ho}".` };
  const dom = parseField(dm, 1, 31, null);
  if (!dom) return { kind: "invalid", why: `Not a valid day of month: "${dm}".` };
  const month = parseField(mo, 1, 12, MONTH_NAMES);
  if (!month) return { kind: "invalid", why: `Not a valid month: "${mo}".` };
  const dowRaw = parseField(dw, 0, 7, DOW_NAMES);
  if (!dowRaw) return { kind: "invalid", why: `Not a valid weekday: "${dw}".` };
  // Cron allows both 0 and 7 for Sunday.
  const dow = [...new Set(dowRaw.map((d) => (d === 7 ? 0 : d)))].sort((a, b) => a - b);
  return {
    kind: "ok",
    fields: { minute, hour, dom, month, dow, domAny: dm === "*", dowAny: dw === "*" },
  };
}

function dayMatches(f: Fields, d: Date): boolean {
  if (!f.month.includes(d.getMonth() + 1)) return false;
  if (f.domAny && f.dowAny) return true;
  if (f.domAny) return f.dow.includes(d.getDay());
  if (f.dowAny) return f.dom.includes(d.getDate());
  return f.dom.includes(d.getDate()) || f.dow.includes(d.getDay());
}

/**
 * The next few times an expression fires, in local time. Walks days and only
 * expands hours/minutes on a day that matches, so even a once-a-year schedule
 * resolves in well under a millisecond.
 */
export function previewCron(expr: string, count = 3, from: Date = new Date()): CronPreview {
  const parsed = parse(expr);
  if (parsed.kind !== "ok") return parsed;
  const f = parsed.fields;
  const runs: Date[] = [];
  const start = from.getTime();
  // Four hundred days covers every expression this parser accepts, including
  // "29 February" in a leap-year gap of one.
  for (let i = 0; i < 400 && runs.length < count; i++) {
    const day = new Date(from.getFullYear(), from.getMonth(), from.getDate() + i);
    if (!dayMatches(f, day)) continue;
    for (const h of f.hour) {
      for (const m of f.minute) {
        const t = new Date(day.getFullYear(), day.getMonth(), day.getDate(), h, m, 0, 0);
        if (t.getTime() <= start) continue;
        runs.push(t);
        if (runs.length >= count) break;
      }
      if (runs.length >= count) break;
    }
  }
  if (runs.length === 0) return { kind: "unknown" };
  return { kind: "ok", runs };
}

function listNames(dows: number[]): string {
  const names = dows.map((d) => WEEKDAY_LABELS[d] ?? String(d));
  if (names.length === 1) return names[0];
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

/**
 * A schedule in words, or "" when the expression has no short plain-English
 * form. Reads the raw fields rather than the expanded sets: a step of 15 is
 * "every 15 minutes", while the same four minutes written out as a list is not.
 */
export function describeCron(raw: string): string {
  let expr = normalize(raw);
  if (expr === "") return "";
  if (expr.startsWith("@")) {
    const alias = expandDescriptor(expr);
    if (alias) return describeCron(alias);
    const every = /^@every\s+(.+)$/i.exec(expr);
    return every ? `Every ${every[1]}` : "";
  }
  const parts = expr.split(" ");
  if (parts.length !== 5) return "";
  const [mi, ho, dm, mo, dw] = parts;
  const anyDate = dm === "*" && mo === "*" && dw === "*";
  const num = (s: string) => (/^\d{1,2}$/.test(s) ? Number(s) : null);
  const stepOf = (s: string) => {
    const m = /^\*\/(\d{1,2})$/.exec(s);
    return m ? Number(m[1]) : null;
  };

  const miNum = num(mi);
  const hoNum = num(ho);

  if (anyDate) {
    if (mi === "*" && ho === "*") return "Every minute";
    const miStep = stepOf(mi);
    // "*/1" is a step of one, so it needs the singular — "Every 1 minutes" reads
    // as a bug even though the expression is right.
    if (miStep && ho === "*") return miStep === 1 ? "Every minute" : `Every ${miStep} minutes`;
    if (miNum !== null && ho === "*") return `Every hour, at :${pad(miNum)}`;
    const hoStep = stepOf(ho);
    if (miNum !== null && hoStep) {
      return hoStep === 1
        ? `Every hour, at :${pad(miNum)}`
        : `Every ${hoStep} hours, at :${pad(miNum)}`;
    }
  }
  if (miNum === null || hoNum === null) return "";
  const at = `at ${pad(hoNum)}:${pad(miNum)}`;
  if (mo !== "*") return "";
  if (dm === "*" && dw === "*") return `Every day ${at}`;
  if (dm === "*" && dw === "1-5") return `Every weekday ${at}`;
  if (dm === "*") {
    const dows = parseField(dw, 0, 7, DOW_NAMES);
    if (!dows) return "";
    const uniq = [...new Set(dows.map((d) => (d === 7 ? 0 : d)))];
    return `Every ${listNames(uniq)} ${at}`;
  }
  const dmNum = num(dm);
  if (dmNum !== null && dw === "*") return `Day ${dmNum} of every month ${at}`;
  return "";
}

function parseHM(time: string): { h: number; m: number } | null {
  const m = /^(\d{1,2}):(\d{2})/.exec((time || "").trim());
  if (!m) return null;
  const h = Number(m[1]);
  const mm = Number(m[2]);
  if (h > 23 || mm > 59) return null;
  return { h, m: mm };
}

/** True when this mode needs a time of day to be meaningful. */
function needsTime(mode: WhenMode): boolean {
  return mode === "daily" || mode === "weekdays" || mode === "weekly" || mode === "monthly";
}

/** Why this builder state cannot produce an expression yet, or "" when it can. */
export function whenProblem(w: When): string {
  if (needsTime(w.mode) && !parseHM(w.time)) return "Pick a time of day.";
  if (w.mode === "weekly" && w.days.length === 0) return "Pick at least one day.";
  if (w.mode === "cron" && normalize(w.expr) === "") return "Type a cron expression.";
  return "";
}

/** The cron expression a builder state means, or "" while it is incomplete. */
export function whenToCron(w: When): string {
  if (whenProblem(w) !== "") return "";
  if (w.mode === "cron") return normalize(w.expr);
  if (w.mode === "minutes") return `*/${clampInt(w.everyMinutes, 1, 59)} * * * *`;
  if (w.mode === "hours") {
    const every = clampInt(w.everyHours, 1, 23);
    const minute = clampInt(w.minute, 0, 59);
    // "*/1" fires every hour, but so does "*", and the plain form is the one a
    // reader recognises.
    return every === 1 ? `${minute} * * * *` : `${minute} */${every} * * *`;
  }
  const hm = parseHM(w.time)!;
  const at = `${hm.m} ${hm.h}`;
  switch (w.mode) {
    case "daily":
      return `${at} * * *`;
    case "weekdays":
      return `${at} * * 1-5`;
    case "weekly":
      return `${at} * * ${[...w.days].sort((a, b) => a - b).join(",")}`;
    case "monthly":
      return `${at} ${clampInt(w.dom, 1, 31)} * *`;
  }
  return "";
}

export function clampInt(v: number, min: number, max: number): number {
  if (!Number.isFinite(v)) return min;
  return Math.min(max, Math.max(min, Math.round(v)));
}
