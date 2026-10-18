import { getLanguage, translate } from "./i18n";

// noted's schedule (日程安排): calendar events, to-dos with a time and alarms,
// each with a notify mode. The pure part of the page: grouping, labels and
// the CallTool calls an edit turns into. See NotedSchedule / NotedUpcoming.

export type Notify = "alarm" | "reminder" | "notification" | "none";
export const NOTIFY_MODES: Notify[] = ["alarm", "reminder", "notification", "none"];

export interface AlarmRecurrence {
  freq: string;
  interval?: number;
  byDay?: string[];
  byMonthDay?: number;
  byTime?: string[];
  until?: string;
  count?: number;
  endTime?: string;
}

export interface AlarmSchedule {
  kind: "oneShot" | "recurring" | string;
  at?: string;
  anchor?: string;
  time?: string;
  timeZone?: string;
  recurrence?: AlarmRecurrence;
}

export interface ScheduleItem {
  kind: "event" | "task" | string;
  source?: "event" | "task" | "alarm" | string;
  id: string;
  link?: string;
  title: string;
  description?: string;
  location?: string;
  startTime: string;
  endTime?: string;
  allDay?: boolean;
  timeZone?: string;
  rrule?: string;
  schedule?: AlarmSchedule;
  holidayPolicy?: string;
  region?: string;
  notify?: string;
  remindBeforeMinutes?: number;
  enabled?: boolean;
  completed?: boolean;
  taskId?: string;
  ringTimes?: string[];
}

export interface UnscheduledTask {
  id: string;
  title: string;
  notes?: string;
  priority?: number;
  tags?: string[];
  notify?: string;
  remindBeforeMinutes?: number;
}

export interface ToolCall {
  tool: string;
  args: Record<string, unknown>;
  label: string;
  confirmed?: boolean;
}

const zh = () => getLanguage() === "zh-CN";

const LABELS: Record<Notify, string> = { alarm: "Alarm", reminder: "Reminder", notification: "Notification", none: "No alert" };

/** How an item reaches the person. An older noted sends no notify: a lead
 *  then means a reminder. Anything unknown is a reminder, never an alarm. */
export function notifyOf(it: { notify?: string; remindBeforeMinutes?: number | null }): Notify {
  const n = (it.notify ?? "").trim();
  if ((NOTIFY_MODES as string[]).includes(n)) return n as Notify;
  if (n) return "reminder";
  return it.remindBeforeMinutes != null ? "reminder" : "none";
}

/** 闹钟 / 提醒 / 通知 / 不提醒. */
export const notifyLabel = (n: Notify) => translate(LABELS[n]);

export const sourceOf = (it: ScheduleItem) => it.source || (it.kind === "task" ? "task" : "event");
export const isAlarm = (it: ScheduleItem) => sourceOf(it) === "alarm";
export const isTask = (it: ScheduleItem) => sourceOf(it) === "task" || it.kind === "task";
export const isPaused = (it: ScheduleItem) => it.enabled === false;
export const repeats = (it: ScheduleItem) => !!it.rrule || it.schedule?.kind === "recurring";

const pad = (n: number) => String(n).padStart(2, "0");

/** A local date as YYYY-MM-DD. */
export const localDay = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

/** Local midnight of a YYYY-MM-DD. */
export function dateOf(day: string): Date {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(y, m - 1, d);
}

export function addDays(day: string, n: number): string {
  const d = dateOf(day);
  d.setDate(d.getDate() + n);
  return localDay(d);
}

/** The day an item is on. An all-day item is on its own date, whatever the
 *  zone this window is in. */
export function dayOf(it: Pick<ScheduleItem, "startTime" | "allDay">): string {
  if (it.allDay && /^\d{4}-\d\d-\d\d/.test(it.startTime)) return it.startTime.slice(0, 10);
  const d = new Date(it.startTime);
  return Number.isFinite(d.getTime()) ? localDay(d) : "";
}

/** HH:MM, local, 24-hour. */
export function clock(iso: string): string {
  const d = new Date(iso);
  return Number.isFinite(d.getTime()) ? `${pad(d.getHours())}:${pad(d.getMinutes())}` : "";
}

const byTime = (a: ScheduleItem, b: ScheduleItem) =>
  a.allDay !== b.allDay ? (a.allDay ? -1 : 1) : +new Date(a.startTime) - +new Date(b.startTime);

export interface ScheduleDay { date: string; items: ScheduleItem[] }

/** Every day from from for days days, each with its items: all-day first,
 *  then by time. Something that began before from (an event under way) is on
 *  the first day; anything after the window is left out. */
export function groupByDay(items: ScheduleItem[], from: string, days: number): ScheduleDay[] {
  const out: ScheduleDay[] = [];
  const at = new Map<string, ScheduleDay>();
  for (let i = 0; i < days; i++) {
    const day = { date: addDays(from, i), items: [] as ScheduleItem[] };
    out.push(day);
    at.set(day.date, day);
  }
  const last = addDays(from, days);
  for (const it of items) {
    let key = dayOf(it);
    if (!key) continue;
    if (key < from) key = from;
    if (key >= last) continue;
    at.get(key)?.items.push(it);
  }
  out.forEach((d) => d.items.sort(byTime));
  return out;
}

const minutes = (hm: string) => {
  const m = /^(\d{1,2}):(\d\d)$/.exec(hm.trim());
  return m ? Number(m[1]) * 60 + Number(m[2]) : NaN;
};

/** A repeating alarm's rings on one day, in a few words: "07:00 起 每10分钟"
 *  when they are evenly spaced, else "07:00 等 3 次". One ring says nothing. */
export function ringSummary(times?: string[]): string {
  const list = (times ?? []).filter((t) => Number.isFinite(minutes(t)));
  if (list.length < 2) return "";
  const m = list.map(minutes);
  const step = m[1] - m[0];
  const even = step > 0 && m.every((v, i) => i === 0 || v - m[i - 1] === step);
  if (even) {
    return step % 60 === 0
      ? translate("{start} on, every {n} h", { start: list[0], n: step / 60 })
      : translate("{start} on, every {n} min", { start: list[0], n: step });
  }
  return translate("{start}, {count} times", { start: list[0], count: list.length });
}

/** The time column of a row: 全天, or HH:MM. */
export const timeLabel = (it: ScheduleItem) => (it.allDay ? translate("All day") : clock(it.startTime));

const ZH_DAYS: Record<string, string> = { MON: "一", TUE: "二", WED: "三", THU: "四", FRI: "五", SAT: "六", SUN: "日" };
const EN_DAYS: Record<string, string> = { MON: "Mon", TUE: "Tue", WED: "Wed", THU: "Thu", FRI: "Fri", SAT: "Sat", SUN: "Sun" };
export const WEEKDAYS = ["MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"];
const RRULE_DAYS: Record<string, string> = { MO: "MON", TU: "TUE", WE: "WED", TH: "THU", FR: "FRI", SA: "SAT", SU: "SUN" };

export const weekdayLabel = (d: string) => (zh() ? "周" + (ZH_DAYS[d] ?? d) : EN_DAYS[d] ?? d);

/** "每天", "每周一、三", "每2周 周一", "每月15日", "每10分钟", in the page's language. */
export function ruleLabel(r: { freq: string; interval?: number; byDay?: string[]; byMonthDay?: number }): string {
  const n = r.interval && r.interval > 1 ? r.interval : 1;
  const freq = r.freq.toLowerCase();
  const days = (r.byDay ?? []).filter((d) => d in ZH_DAYS);
  if (zh()) {
    const every = (unit: string) => (n > 1 ? `每${n}${unit}` : `每${unit}`);
    switch (freq) {
      case "minutely": return every("分钟");
      case "hourly": return every("小时");
      case "daily": return n > 1 ? `每${n}天` : "每天";
      case "weekly": {
        const ds = days.map((d) => ZH_DAYS[d]).join("、");
        return n > 1 ? `每${n}周${ds ? " 周" + ds : ""}` : `每周${ds}`;
      }
      case "monthly": return (n > 1 ? `每${n}个月` : "每月") + (r.byMonthDay ? `${r.byMonthDay}日` : "");
      case "yearly": return n > 1 ? `每${n}年` : "每年";
    }
    return "";
  }
  const every = (unit: string) => (n > 1 ? `Every ${n} ${unit}s` : `Every ${unit}`);
  switch (freq) {
    case "minutely": return every("minute");
    case "hourly": return every("hour");
    case "daily": return n > 1 ? `Every ${n} days` : "Every day";
    case "weekly": {
      const ds = days.map((d) => EN_DAYS[d]).join(", ");
      return (n > 1 ? `Every ${n} weeks` : "Every week") + (ds ? ` on ${ds}` : "");
    }
    case "monthly": return (n > 1 ? `Every ${n} months` : "Every month") + (r.byMonthDay ? ` on the ${r.byMonthDay}th` : "");
    case "yearly": return n > 1 ? `Every ${n} years` : "Every year";
  }
  return "";
}

/** An RRULE's FREQ, INTERVAL and BYDAY. */
export function parseRRule(rrule: string): { freq: string; interval?: number; byDay?: string[]; byMonthDay?: number } | null {
  const parts = Object.fromEntries(rrule.replace(/^RRULE:/i, "").split(";").map((p) => p.split("=") as [string, string]).filter((p) => p.length === 2).map(([k, v]) => [k.toUpperCase(), v]));
  if (!parts.FREQ) return null;
  return {
    freq: parts.FREQ.toLowerCase(),
    interval: parts.INTERVAL ? Number(parts.INTERVAL) : undefined,
    byDay: parts.BYDAY ? parts.BYDAY.split(",").map((d: string) => RRULE_DAYS[d.slice(-2).toUpperCase()]).filter(Boolean) : undefined,
    byMonthDay: parts.BYMONTHDAY ? Number(parts.BYMONTHDAY) : undefined,
  };
}

export const HOLIDAY_POLICIES = ["none", "skipHoliday", "workdayOnly", "followStatutory"];
const HOLIDAY_LABELS: Record<string, string> = { none: "Every time", skipHoliday: "Skip holidays", workdayOnly: "Workdays only", followStatutory: "Follow make-up workdays" };
export const holidayLabel = (p: string) => translate(HOLIDAY_LABELS[p] ?? HOLIDAY_LABELS.none);

/** How an item repeats, or "" for a one-off: "每天 · 仅工作日". */
export function repeatLabel(it: ScheduleItem): string {
  const r = it.schedule?.kind === "recurring" && it.schedule.recurrence ? it.schedule.recurrence : it.rrule ? parseRRule(it.rrule) : null;
  if (!r) return "";
  const rule = ruleLabel(r);
  const policy = it.holidayPolicy && it.holidayPolicy !== "none" ? holidayLabel(it.holidayPolicy) : "";
  return [rule, policy].filter(Boolean).join(" · ");
}

/** The lead, for a row: "提前10分钟", nothing when it is at the time. */
export function leadLabel(it: { remindBeforeMinutes?: number; notify?: string }): string {
  const n = it.remindBeforeMinutes ?? 0;
  if (n <= 0 || notifyOf(it) === "none") return "";
  if (n % 1440 === 0) return translate("{n} d before", { n: n / 1440 });
  if (n % 60 === 0) return translate("{n} h before", { n: n / 60 });
  return translate("{n} min before", { n });
}

/** A day's heading: 今天 / 明天 / 后天 or nothing, and the date itself. */
export function dayHeading(day: string, today: string): { rel: string; date: string } {
  const date = dateOf(day);
  const diff = Math.round((+date - +dateOf(today)) / 86400000);
  const rel = diff === 0 ? translate("Today") : diff === 1 ? translate("Tomorrow") : diff === 2 ? translate("The day after") : diff === -1 ? translate("Yesterday") : "";
  const label = date.toLocaleDateString(getLanguage(), { month: "long", day: "numeric", weekday: "short" });
  return { rel, date: label };
}

/** When an upcoming item is, in one short line: "今天 15:00", "明天 全天", "周二 09:30". */
export function upcomingWhen(it: ScheduleItem, now = new Date()): string {
  const day = dayOf(it);
  const today = localDay(now);
  const rel = day === today ? translate("Today") : day === addDays(today, 1) ? translate("Tomorrow")
    : dateOf(day).toLocaleDateString(getLanguage(), { weekday: "short" });
  return `${rel} ${timeLabel(it)}`;
}

/** An RFC 3339 time with this window's offset, from a date and HH:MM. */
export function localISO(day: string, hm: string): string {
  const [y, m, d] = day.split("-").map(Number);
  const [h, mi] = (hm || "00:00").split(":").map(Number);
  const t = new Date(y, m - 1, d, h || 0, mi || 0, 0);
  const off = -t.getTimezoneOffset();
  const sign = off >= 0 ? "+" : "-";
  return `${localDay(t)}T${pad(t.getHours())}:${pad(t.getMinutes())}:00${sign}${pad(Math.floor(Math.abs(off) / 60))}:${pad(Math.abs(off) % 60)}`;
}

// ── Editing ──────────────────────────────────────────────────────────────

export type Freq = "once" | "minutely" | "hourly" | "daily" | "weekly" | "monthly";

/** What the edit panel shows and changes. */
export interface Draft {
  title: string;
  date: string;
  time: string;
  allDay: boolean;
  location: string;
  notify: Notify;
  lead: number;
  enabled: boolean;
  freq: Freq;
  interval: number;
  byDay: string[];
  byMonthDay: number;
  holidayPolicy: string;
}

const freqOf = (it: ScheduleItem): Freq => {
  if (it.schedule?.kind === "recurring") return (it.schedule.recurrence?.freq?.toLowerCase() as Freq) || "daily";
  if (it.rrule) return (parseRRule(it.rrule)?.freq as Freq) || "daily";
  return "once";
};

export function draftOf(it: ScheduleItem): Draft {
  const rec = it.schedule?.kind === "recurring" ? it.schedule.recurrence : it.rrule ? parseRRule(it.rrule) ?? undefined : undefined;
  const day = dayOf(it) || localDay(new Date());
  return {
    title: it.title,
    date: it.schedule?.kind === "recurring" && it.schedule.anchor ? it.schedule.anchor : day,
    time: it.allDay ? "" : it.schedule?.kind === "recurring" && it.schedule.time ? it.schedule.time : clock(it.startTime),
    allDay: !!it.allDay,
    location: it.location ?? "",
    notify: notifyOf(it),
    lead: it.remindBeforeMinutes ?? 0,
    enabled: it.enabled !== false,
    freq: freqOf(it),
    interval: rec?.interval && rec.interval > 1 ? rec.interval : 1,
    byDay: rec?.byDay ?? [],
    byMonthDay: rec?.byMonthDay ?? Number(day.slice(8, 10)),
    holidayPolicy: it.holidayPolicy || "none",
  };
}

/** A weekday of a date, as MON…SUN. */
const weekdayOf = (day: string) => WEEKDAYS[(dateOf(day).getDay() + 6) % 7];

/** An alarm-style schedule from a draft. What the panel does not show of
 *  the old one (byTime, until, count, endTime, zone) is kept while the
 *  repeat stays the same. */
export function scheduleOf(d: Draft, prev?: AlarmSchedule): AlarmSchedule {
  if (d.freq === "once") return { kind: "oneShot", at: localISO(d.date, d.time || "09:00") };
  const old = prev?.kind === "recurring" ? prev.recurrence : undefined;
  const same = old && old.freq.toLowerCase() === d.freq;
  const rec: AlarmRecurrence = { ...(same ? old : {}), freq: d.freq };
  if (d.interval > 1) rec.interval = d.interval; else delete rec.interval;
  if (d.freq === "weekly") rec.byDay = d.byDay.length ? d.byDay : [weekdayOf(d.date)]; else delete rec.byDay;
  if (d.freq === "monthly") rec.byMonthDay = d.byMonthDay || Number(d.date.slice(8, 10)); else delete rec.byMonthDay;
  const out: AlarmSchedule = { kind: "recurring", anchor: d.date };
  if (!(same && old?.byTime?.length)) out.time = d.time || "09:00";
  if (prev?.timeZone) out.timeZone = prev.timeZone;
  out.recurrence = rec;
  return out;
}

/** An RRULE from a draft, for a repeating event. */
export function rruleOf(d: Draft): string {
  if (d.freq === "once") return "";
  const freq = d.freq === "minutely" || d.freq === "hourly" ? "DAILY" : d.freq.toUpperCase();
  const parts = [`FREQ=${freq}`];
  if (d.interval > 1) parts.push(`INTERVAL=${d.interval}`);
  if (d.freq === "weekly" && d.byDay.length) parts.push(`BYDAY=${d.byDay.map((x) => x.slice(0, 2)).join(",")}`);
  if (d.freq === "monthly" && d.byMonthDay) parts.push(`BYMONTHDAY=${d.byMonthDay}`);
  return parts.join(";");
}

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

/** Whether the panel may move an item's time: not a repeating calendar
 *  event (its start is the series'), which the queen moves instead. */
export const timeEditable = (it: ScheduleItem) => !it.rrule;

/** The CallTool calls that turn an item into the draft: update_event for
 *  title, time, mode and lead (it forwards to a to-do), update_alarm for an
 *  alarm's schedule, holidays and on/off. Nothing changed, no calls. */
export function editCalls(it: ScheduleItem, d: Draft): ToolCall[] {
  const base = draftOf(it);
  const ev: Record<string, unknown> = {};
  const al: Record<string, unknown> = {};
  const alarm = isAlarm(it);
  const title = d.title.trim();
  if (title && title !== base.title) {
    if (alarm) al.label = title; else ev.title = title;
  }
  if (d.notify !== base.notify) ev.notify = d.notify;
  if (d.notify !== "none" && d.lead !== base.lead) ev.remind_before_minutes = Math.max(0, Math.round(d.lead));
  if (alarm) {
    const moved = d.date !== base.date || d.time !== base.time || d.freq !== base.freq || d.interval !== base.interval ||
      (d.freq === "weekly" && !sameList(d.byDay, base.byDay)) || (d.freq === "monthly" && d.byMonthDay !== base.byMonthDay);
    if (moved) al.schedule = scheduleOf(d, it.schedule);
    if (d.freq !== "once" && d.holidayPolicy !== base.holidayPolicy) al.holiday_policy = d.holidayPolicy;
    if (d.enabled !== base.enabled) al.enabled = d.enabled;
  } else if (timeEditable(it) && (d.date !== base.date || (!d.allDay && d.time !== base.time))) {
    ev.start = localISO(d.date, d.allDay ? "00:00" : d.time);
  }
  if (!alarm && d.location.trim() !== base.location.trim() && sourceOf(it) === "event") ev.location = d.location.trim();
  const out: ToolCall[] = [];
  const label = translate("Change {title}", { title: title || base.title });
  if (Object.keys(ev).length) out.push({ tool: "update_event", args: { id: it.id, ...ev }, label });
  if (Object.keys(al).length) out.push({ tool: "update_alarm", args: { id: it.id, ...al }, label });
  return out;
}

/** Switching only the mode, from a row's chip. */
export function modeCall(it: ScheduleItem, notify: Notify): ToolCall {
  return { tool: "update_event", args: { id: it.id, notify }, label: translate("{title}: {mode}", { title: it.title, mode: notifyLabel(notify) }) };
}

/** The same for a to-do with no time, which is not on the schedule. */
export function taskModeCall(t: UnscheduledTask, notify: Notify): ToolCall {
  return { tool: "update_task", args: { id: t.id, notify }, label: translate("{title}: {mode}", { title: t.title, mode: notifyLabel(notify) }) };
}

/** Editing a to-do with no time: rename, its mode, or a time that puts it
 *  on the schedule. */
export function taskEditCalls(t: UnscheduledTask, d: { title: string; notify: Notify; date: string; time: string }): ToolCall[] {
  const args: Record<string, unknown> = {};
  const title = d.title.trim();
  if (title && title !== t.title) args.title = title;
  if (d.notify !== notifyOf(t)) args.notify = d.notify;
  if (d.date && d.time) args.due = localISO(d.date, d.time);
  if (!Object.keys(args).length) return [];
  return [{ tool: "update_task", args: { id: t.id, ...args }, label: translate("Change {title}", { title: title || t.title }) }];
}

export function deleteCall(it: ScheduleItem): ToolCall {
  return { tool: isAlarm(it) ? "delete_alarm" : "delete_event", args: { id: it.id }, label: translate("Delete {title}", { title: it.title }), confirmed: true };
}

export function completeCall(id: string, title: string): ToolCall {
  return { tool: "complete_task", args: { id }, label: translate("Done: {title}", { title }) };
}

// ── New ──────────────────────────────────────────────────────────────────

export type NewKind = "event" | "alarm" | "reminder" | "todo";
export const NEW_KINDS: NewKind[] = ["event", "alarm", "reminder", "todo"];
const NEW_LABELS: Record<NewKind, string> = { event: "Event", alarm: "Alarm", reminder: "Reminder", todo: "To-do" };
export const newKindLabel = (k: NewKind) => translate(NEW_LABELS[k]);

/** A fresh draft of a kind, on a day, at the next round hour. */
export function newDraft(kind: NewKind, day: string, now = new Date()): Draft {
  const next = new Date(now.getTime() + 60 * 60000);
  const sameDay = day === localDay(now);
  return {
    title: "", date: day, time: sameDay ? `${pad(next.getHours())}:00` : kind === "alarm" ? "07:00" : "09:00", allDay: false, location: "",
    notify: kind === "alarm" ? "alarm" : kind === "reminder" ? "reminder" : kind === "todo" ? "none" : "reminder",
    lead: kind === "event" ? 10 : 0, enabled: true, freq: "once", interval: 1, byDay: [], byMonthDay: Number(day.slice(8, 10)), holidayPolicy: "none",
  };
}

/** The CallTool call that makes a new item. A to-do with no time (time
 *  "") stays unscheduled. */
export function createCall(kind: NewKind, d: Draft): ToolCall {
  const title = d.title.trim();
  const label = translate("New {kind}: {title}", { kind: newKindLabel(kind), title });
  if (kind === "alarm" || kind === "reminder") {
    const args: Record<string, unknown> = { label: title, notify: kind, schedule: scheduleOf(d) };
    if (d.freq !== "once" && d.holidayPolicy !== "none") args.holiday_policy = d.holidayPolicy;
    return { tool: "create_alarm", args, label };
  }
  if (kind === "todo") {
    const args: Record<string, unknown> = { title };
    if (d.time) {
      const due = localISO(d.date, d.time);
      args.due = due;
      if (d.notify !== "none") {
        args.notify = d.notify;
        if (d.lead > 0) args.remind_at = localISO(d.date, minusMinutes(d.time, d.lead));
      }
    }
    return { tool: "create_task", args, label };
  }
  const args: Record<string, unknown> = { title, start: localISO(d.date, d.allDay ? "00:00" : d.time || "09:00"), notify: d.notify };
  if (d.allDay) args.all_day = true;
  if (d.location.trim()) args.location = d.location.trim();
  if (d.notify !== "none") args.remind_before_minutes = Math.max(0, Math.round(d.lead));
  const rrule = rruleOf(d);
  if (rrule) {
    args.rrule = rrule;
    if (d.holidayPolicy !== "none") args.holiday_policy = d.holidayPolicy;
  }
  return { tool: "create_event", args, label };
}

/** HH:MM less some minutes, on the same day (clamped at 00:00). */
function minusMinutes(hm: string, n: number): string {
  const m = Math.max(0, minutes(hm) - n);
  return `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;
}

/** Read NotedSchedule's answer, tolerating an older noted's. */
export function readSchedule(raw: unknown): { items: ScheduleItem[]; unscheduled: UnscheduledTask[] } {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const items = (Array.isArray(r.items) ? r.items : Array.isArray(r.events) ? r.events : []) as ScheduleItem[];
  const unscheduled = (Array.isArray(r.unscheduled) ? r.unscheduled : []) as UnscheduledTask[];
  return {
    items: items.filter((x) => x && typeof x === "object" && typeof x.id === "string" && typeof x.startTime === "string").map((x) => ({ ...x, title: x.title || "" })),
    unscheduled: unscheduled.filter((x) => x && typeof x === "object" && typeof x.id === "string"),
  };
}
