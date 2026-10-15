import { translate } from "../lib/i18n";

// noted's day for the world's board: get_overview (horizon today) read
// through NotedToday, cut down to what is still to come.

export interface AgendaEntry {
  id: string;
  kind: "event" | "reminder" | "todo" | "timer";
  title: string;
  /** ISO time it starts; none for a to-do with no time. */
  at?: string;
  allDay?: boolean;
  link?: string;
}

const text = (v: unknown) => (typeof v === "string" ? v.trim() : typeof v === "number" ? String(v) : "");
const pick = (o: Record<string, unknown>, ...keys: string[]) => {
  for (const k of keys) { const v = text(o[k]); if (v) return v; }
  return "";
};

/** Today's still-open noted items in time order: the running timer first,
 *  timed things, then to-dos with no time. What is over is left out. */
export function agendaOf(raw: unknown, now = new Date()): AgendaEntry[] {
  if (!raw || typeof raw !== "object") return [];
  const r = raw as Record<string, unknown>;
  const timed: AgendaEntry[] = [], untimed: AgendaEntry[] = [];
  const lists: [string, AgendaEntry["kind"] | ""][] = [["items", ""], ["events", "event"], ["tasks", "todo"], ["todos", "todo"], ["reminders", "reminder"]];
  for (const [key, fallback] of lists) {
    const arr = Array.isArray(r[key]) ? (r[key] as Record<string, unknown>[]) : [];
    arr.forEach((x, i) => {
      if (!x || typeof x !== "object") return;
      const title = pick(x, "title", "name", "summary", "content");
      if (!title || x.completed === true || x.done === true || x.isDone === true) return;
      const k = pick(x, "kind", "type") || fallback || "event";
      const kind: AgendaEntry["kind"] = k === "task" || k === "todo" ? "todo" : k === "reminder" ? "reminder" : "event";
      const start = pick(x, "startTime", "start", "dueTime", "due", "fireTime", "time", "at");
      const end = pick(x, "endTime", "end");
      const e: AgendaEntry = { id: pick(x, "id") || `${key}-${i}`, kind, title, allDay: x.allDay === true || x.all_day === true, link: pick(x, "link") || undefined };
      if (e.allDay) { timed.push(e); return; }
      if (!start || isNaN(+new Date(start))) { if (kind === "todo") untimed.push(e); return; }
      if (+new Date(end || start) < +now) return;
      timed.push({ ...e, at: start });
    });
  }
  timed.sort((a, b) => (a.allDay !== b.allDay ? (a.allDay ? -1 : 1) : +new Date(a.at ?? 0) - +new Date(b.at ?? 0)));
  const t = (r.runningTimer ?? r.running_timer ?? r.timer) as Record<string, unknown> | undefined;
  const timer: AgendaEntry[] = t && typeof t === "object" && pick(t, "title", "activity", "name")
    ? [{ id: "timer", kind: "timer", title: pick(t, "title", "activity", "name"), at: pick(t, "startTime", "start", "startedAt") || undefined }]
    : [];
  return [...timer, ...timed, ...untimed];
}

const clock = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });

/** When, for a row: "09:30", "All day", or nothing. */
export function agendaWhen(e: AgendaEntry): string {
  if (e.kind === "timer") return translate("Timing");
  if (e.allDay) return translate("All day");
  return e.at ? clock(e.at) : "";
}

/** One line of the board: "09:30 Stand-up", "To-do · Groceries". */
export function boardLine(e: AgendaEntry): string {
  const title = e.title.replace(/\s+/g, " ").slice(0, 22);
  const tag = e.kind === "timer" ? translate("Timing") : e.kind === "reminder" ? translate("Reminder") : e.kind === "todo" && !e.at ? translate("To-do") : "";
  const when = e.kind === "timer" || !(e.at || e.allDay) ? "" : agendaWhen(e);
  const body = tag ? `${tag} · ${title}` : title;
  return when ? `${when} ${body}` : body;
}

/** Whether a member's current step is a noted tool call. */
export const touchesNoted = (doing?: string) => !!doing && /noted/i.test(doing);
