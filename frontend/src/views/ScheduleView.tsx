import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ArrowUpIcon, CalendarDaysIcon, CheckIcon, ChevronDownIcon, MapPinIcon, PlusIcon, RepeatIcon, XIcon } from "lucide-react";
import { CallTool, NotedSchedule, SendChat } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { useI18n } from "../lib/i18n";
import { useImeGuard } from "../lib/ime";
import { loadQueenSession } from "../lib/queenConsole";
import { toast } from "../lib/toasts";
import {
  addDays, clock, completeCall, createCall, dayHeading, deleteCall, Draft, draftOf, editCalls, Freq, groupByDay, HOLIDAY_POLICIES,
  holidayLabel, isAlarm, isPaused, isTask, leadLabel, localDay, NEW_KINDS, newDraft, NewKind, newKindLabel, notifyOf, readSchedule,
  repeatLabel, repeats, ringSummary, ScheduleItem, sourceOf, taskEditCalls, timeEditable, timeLabel, ToolCall, UnscheduledTask,
  weekdayLabel, WEEKDAYS,
} from "../lib/schedule";
import { ModeChip, ModePicker } from "../components/ModeChip";
import "./schedule.css";

const STEP = 7;
const MAX_DAYS = 62;
const LEADS = [0, 5, 10, 15, 30, 60, 120, 1440];

/** One noted write through CallTool; its error, if any, thrown. */
export async function runCall(c: ToolCall) {
  const res = await CallTool("noted", c.tool, c.args, app.ActionOptions.createFrom({ label: c.label, confirmed: !!c.confirmed, sessionId: "" }));
  if (!res?.ok) throw new Error(res?.error || res?.code || "failed");
  return res;
}

function useToday() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = window.setInterval(() => setNow(new Date()), 30000);
    return () => window.clearInterval(t);
  }, []);
  return now;
}

type Picked =
  | { type: "item"; item: ScheduleItem }
  | { type: "task"; task: UnscheduledTask }
  | { type: "new"; kind: NewKind; day: string };

/**
 * 日程安排: the one page for events, alarms, reminders and to-dos. Day by
 * day from today; each row says how it reaches you (闹钟 / 提醒 / 通知 /
 * 不提醒), and opens to be changed.
 */
export default function ScheduleView() {
  const { t } = useI18n();
  const now = useToday();
  const today = localDay(now);
  const [params, setParams] = useSearchParams();
  const [from, setFrom] = useState(() => (/^\d{4}-\d\d-\d\d$/.test(params.get("date") ?? "") ? params.get("date")! : today));
  const [days, setDays] = useState(STEP);
  const [data, setData] = useState<{ items: ScheduleItem[]; unscheduled: UnscheduledTask[] } | null>(null);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(true);
  const [picked, setPicked] = useState<Picked | null>(null);
  const [adding, setAdding] = useState(false);
  const want = useRef(params.get("id") ?? "");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const raw = await NotedSchedule(from, days);
      setData(readSchedule(raw));
      setErr("");
    } catch (e: any) {
      const msg = String(e?.message ?? e);
      setErr(/503|not configured/i.test(msg) ? t("noted is not connected, so there is no schedule to show.") : msg);
    } finally {
      setLoading(false);
    }
  }, [from, days, t]);

  useEffect(() => { void load(); }, [load]);
  // noted's version moves on any schedule change, the queen's included.
  useEffect(() => {
    const off = EventsOn("noted:alarms", () => void load());
    const poll = window.setInterval(() => void load(), 60000);
    window.addEventListener("focus", load);
    return () => { if (typeof off === "function") off(); window.clearInterval(poll); window.removeEventListener("focus", load); };
  }, [load]);

  // Arrived from the home's 接下来的安排 with an item: open it.
  useEffect(() => {
    if (!data || !want.current) return;
    const it = data.items.find((x) => x.id === want.current);
    want.current = "";
    if (it) setPicked({ type: "item", item: it });
    if (params.get("id") || params.get("date")) setParams({}, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const grouped = useMemo(() => groupByDay(data?.items ?? [], from, days), [data, from, days]);
  const total = data?.items.length ?? 0;

  const done = (msg?: string) => {
    if (msg) toast.success(msg);
    setPicked(null);
    void load();
  };

  const jump = (day: string) => {
    if (!/^\d{4}-\d\d-\d\d$/.test(day)) return;
    setFrom(day);
    setDays(STEP);
  };

  const pickedKey = picked?.type === "item" ? `${picked.item.id}@${picked.item.startTime}` : picked?.type === "task" ? "task:" + picked.task.id : "";

  return (
    <div className="view sch-view">
      <div className="view-header with-action sch-head">
        <div className="view-title">{t("Schedule")}</div>
        <AskQueen onAnswered={() => void load()} />
        <div className="vh-actions sch-actions">
          {from !== today && <button className="sch-btn" onClick={() => jump(today)}>{t("Today")}</button>}
          <label className="sch-datepick" title={t("Go to a date")}>
            <CalendarDaysIcon size={15} />
            <input type="date" value={from} onChange={(e) => jump(e.target.value)} aria-label={t("Go to a date")} />
          </label>
          <div className="sch-new">
            <button className="sch-btn pri" aria-expanded={adding} onClick={() => setAdding((v) => !v)}>
              <PlusIcon size={16} />{t("New")}<ChevronDownIcon size={14} />
            </button>
            {adding && (
              <div className="sch-menu" role="menu" onMouseLeave={() => setAdding(false)}>
                {NEW_KINDS.map((k) => (
                  <button key={k} role="menuitem" onClick={() => { setAdding(false); setPicked({ type: "new", kind: k, day: from < today ? today : from }); }}>
                    {newKindLabel(k)}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>

      <div className={`sch-body${picked ? " with-panel" : ""}`}>
        <div className="sch-scroll">
          {err && <div className="sch-error" role="alert">{err} <button className="sch-link" onClick={() => void load()}>{t("Retry")}</button></div>}
          {!err && !data && loading && <div className="sch-loading">{t("Loading…")}</div>}
          {data && (
            <>
              {grouped.map((day) => (
                <DaySection key={day.date} date={day.date} items={day.items} today={today} now={now} pickedKey={pickedKey}
                  onPick={(it) => setPicked({ type: "item", item: it })}
                  onAdd={() => setPicked({ type: "new", kind: "event", day: day.date })} />
              ))}
              <div className="sch-more">
                {days < MAX_DAYS
                  ? <button className="sch-btn" onClick={() => setDays((d) => Math.min(MAX_DAYS, d + STEP))}>{t("{n} more days", { n: STEP })}</button>
                  : <button className="sch-btn" onClick={() => jump(addDays(from, days))}>{t("Further on")}</button>}
                <span>{t("{count} items through {date}", { count: total, date: dayHeading(addDays(from, days - 1), today).date })}</span>
              </div>
              {data.unscheduled.length > 0 && (
                <section className="sch-day sch-unsched">
                  <header className="sch-dayhead"><b>{t("To-dos with no time")}</b><em>{data.unscheduled.length}</em></header>
                  <ul className="sch-rows">
                    {data.unscheduled.map((task) => (
                      <li key={task.id} className={`sch-row task${pickedKey === "task:" + task.id ? " on" : ""}`} onClick={() => setPicked({ type: "task", task })}>
                        <button className="sch-check" title={t("Mark done")} aria-label={t("Mark done")}
                          onClick={(e) => { e.stopPropagation(); runCall(completeCall(task.id, task.title)).then(() => done(t("Done: {title}", { title: task.title }))).catch((x) => toast.error(String(x?.message ?? x))); }}>
                          <CheckIcon size={12} />
                        </button>
                        <span className="sch-tick"><i className="m-none" /></span>
                        <span className="sch-main"><b>{task.title}</b>{task.notes && <small>{task.notes}</small>}</span>
                        <ModeChip mode={notifyOf(task)} />
                      </li>
                    ))}
                  </ul>
                </section>
              )}
            </>
          )}
        </div>
        {picked && (
          <aside className="sch-panel" key={pickedKey || (picked.type === "new" ? "new:" + picked.kind + picked.day : "")}>
            {picked.type === "item" && <ItemEditor item={picked.item} onClose={() => setPicked(null)} onDone={done} />}
            {picked.type === "task" && <TaskEditor task={picked.task} today={today} onClose={() => setPicked(null)} onDone={done} />}
            {picked.type === "new" && <NewEditor kind={picked.kind} day={picked.day} onKind={(k) => setPicked({ ...picked, kind: k })} onClose={() => setPicked(null)} onDone={done} />}
          </aside>
        )}
      </div>
    </div>
  );
}

function DaySection({ date, items, today, now, pickedKey, onPick, onAdd }: {
  date: string; items: ScheduleItem[]; today: string; now: Date; pickedKey: string;
  onPick: (it: ScheduleItem) => void; onAdd: () => void;
}) {
  const { t } = useI18n();
  const head = dayHeading(date, today);
  const isToday = date === today;
  // Where now falls in today's list: before the first timed item still to come.
  const nowAt = isToday ? items.findIndex((it) => !it.allDay && +new Date(it.startTime) >= +now) : -2;
  const rows: ReactNode[] = [];
  items.forEach((it, i) => {
    if (i === nowAt) rows.push(<NowLine key="now" now={now} />);
    const past = isToday && !it.allDay && +new Date(it.endTime || it.startTime) < +now;
    rows.push(<Row key={`${it.id}@${it.startTime}`} it={it} past={past || date < today} on={pickedKey === `${it.id}@${it.startTime}`} onPick={() => onPick(it)} />);
  });
  if (isToday && items.length > 0 && nowAt === -1) rows.push(<NowLine key="now" now={now} />);
  return (
    <section className={`sch-day${isToday ? " today" : ""}`}>
      <header className="sch-dayhead">
        {head.rel && <b>{head.rel}</b>}
        <span className={head.rel ? "" : "plain"}>{head.date}</span>
        <em>{items.length || ""}</em>
        <button className="sch-dayadd" title={t("New on this day")} aria-label={t("New on this day")} onClick={onAdd}><PlusIcon size={14} /></button>
      </header>
      {items.length === 0
        ? <div className="sch-empty">{t("Nothing planned")}</div>
        : <ol className="sch-rows">{rows}</ol>}
    </section>
  );
}

function NowLine({ now }: { now: Date }) {
  return <li className="sch-now" aria-hidden><span>{clock(now.toISOString())}</span><i /><b /></li>;
}

function Row({ it, past, on, onPick }: { it: ScheduleItem; past: boolean; on: boolean; onPick: () => void }) {
  const { t } = useI18n();
  const mode = notifyOf(it);
  const rings = ringSummary(it.ringTimes);
  const meta = [rings, repeatLabel(it), leadLabel(it), it.location].filter(Boolean);
  const cls = ["sch-row", past && "past", isPaused(it) && "paused", it.completed && "done", on && "on", it.allDay && "allday"].filter(Boolean).join(" ");
  return (
    <li className={cls} onClick={onPick} tabIndex={0} onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onPick(); } }}
      title={it.ringTimes && it.ringTimes.length > 1 ? it.ringTimes.join(" ") : undefined}>
      <span className={`sch-time${it.allDay ? " allday" : ""}`}>{timeLabel(it)}</span>
      <span className="sch-tick"><i className={`m-${mode}${isAlarm(it) ? " alarm" : isTask(it) ? " task" : ""}`} /></span>
      <span className="sch-main">
        <b>{it.title || t("Untitled")}</b>
        {(meta.length > 0 || isPaused(it)) && (
          <small>
            {isPaused(it) && <span className="sch-off">{t("Off")}</span>}
            {meta.join(" · ")}
          </small>
        )}
      </span>
      <ModeChip mode={mode} />
    </li>
  );
}

// ── The panel ────────────────────────────────────────────────────────────

function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return <label className="sch-field"><span>{label}</span>{children}{hint && <small>{hint}</small>}</label>;
}

function PanelHead({ kind, title, onClose }: { kind: string; title?: string; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <header className="sch-phead">
      <div><em>{kind}</em>{title && <b>{title}</b>}</div>
      <button className="sch-x" onClick={onClose} title={t("Close")} aria-label={t("Close")}><XIcon size={16} /></button>
    </header>
  );
}

function useRunner(onDone: (msg?: string) => void) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const run = async (calls: ToolCall[], msg = t("Saved")) => {
    if (!calls.length) { onDone(); return; }
    setBusy(true); setErr("");
    try {
      for (const c of calls) await runCall(c);
      onDone(msg);
    } catch (e: any) {
      setErr(String(e?.message ?? e));
    } finally {
      setBusy(false);
    }
  };
  return { busy, err, run };
}

const leadText = (n: number, t: (k: string, v?: Record<string, string | number>) => string) =>
  n === 0 ? t("At the time") : n % 1440 === 0 ? t("{n} d before", { n: n / 1440 }) : n % 60 === 0 ? t("{n} h before", { n: n / 60 }) : t("{n} min before", { n });

function LeadPicker({ value, onChange }: { value: number; onChange: (n: number) => void }) {
  const { t } = useI18n();
  const opts = LEADS.includes(value) ? LEADS : [...LEADS, value].sort((a, b) => a - b);
  return (
    <select className="sch-input" value={value} onChange={(e) => onChange(Number(e.target.value))}>
      {opts.map((n) => <option key={n} value={n}>{leadText(n, t)}</option>)}
    </select>
  );
}

const FREQS: { key: Freq; label: string }[] = [
  { key: "once", label: "Once" }, { key: "daily", label: "Daily" }, { key: "weekly", label: "Weekly" }, { key: "monthly", label: "Monthly" },
];
const UNITS: Record<string, string> = { daily: "days", weekly: "weeks", monthly: "months", hourly: "hours", minutely: "minutes" };

/** 重复 and 节假日: a repeat rule the way a person says it. */
function RepeatFields({ d, set, alarm, extra }: { d: Draft; set: (p: Partial<Draft>) => void; alarm: boolean; extra?: Freq }) {
  const { t, language } = useI18n();
  const freqs = extra && !FREQS.some((f) => f.key === extra) ? [...FREQS, { key: extra, label: extra === "hourly" ? "Hourly" : "Every few minutes" }] : FREQS;
  return (
    <>
      <Field label={t("Repeat")}>
        <div className="sch-seg">
          {freqs.map((f) => (
            <button key={f.key} type="button" className={d.freq === f.key ? "on" : ""} onClick={() => set({ freq: f.key })}>{t(f.label)}</button>
          ))}
        </div>
      </Field>
      {d.freq !== "once" && (
        <div className="sch-inline">
          <span>{t("Every")}</span>
          <input className="sch-input num" type="number" min={1} max={99} value={d.interval} onChange={(e) => set({ interval: Math.max(1, Number(e.target.value) || 1) })} />
          <span>{t(UNITS[d.freq] ?? "days")}</span>
          {d.freq === "monthly" && (
            <>
              {language !== "zh-CN" && <span>on day</span>}
              <input className="sch-input num" type="number" min={1} max={31} value={d.byMonthDay} onChange={(e) => set({ byMonthDay: Math.min(31, Math.max(1, Number(e.target.value) || 1)) })} />
              {language === "zh-CN" && <span>号</span>}
            </>
          )}
        </div>
      )}
      {d.freq === "weekly" && (
        <div className="sch-days" role="group" aria-label={t("Days of the week")}>
          {WEEKDAYS.map((w) => {
            const on = d.byDay.includes(w);
            return <button key={w} type="button" className={on ? "on" : ""} aria-pressed={on}
              onClick={() => set({ byDay: on ? d.byDay.filter((x) => x !== w) : WEEKDAYS.filter((x) => x === w || d.byDay.includes(x)) })}>{weekdayLabel(w)}</button>;
          })}
        </div>
      )}
      {d.freq !== "once" && (alarm || d.freq === "daily" || d.freq === "weekly") && (
        <Field label={t("Holidays")}>
          <select className="sch-input" value={d.holidayPolicy} onChange={(e) => set({ holidayPolicy: e.target.value })}>
            {HOLIDAY_POLICIES.map((p) => <option key={p} value={p}>{holidayLabel(p)}</option>)}
          </select>
        </Field>
      )}
    </>
  );
}

function DeleteButton({ onDelete, busy, note }: { onDelete: () => void; busy: boolean; note?: string }) {
  const { t } = useI18n();
  const [sure, setSure] = useState(false);
  if (!sure) return <button type="button" className="sch-btn ghost danger" onClick={() => setSure(true)} disabled={busy}>{t("Delete")}</button>;
  return (
    <span className="sch-sure">
      <span>{note ?? t("Delete it for good?")}</span>
      <button type="button" className="sch-btn danger" onClick={onDelete} disabled={busy}>{t("Delete it")}</button>
      <button type="button" className="sch-btn ghost" onClick={() => setSure(false)}>{t("Keep")}</button>
    </span>
  );
}

const KIND_NAMES: Record<string, string> = { event: "Event", task: "To-do", alarm: "Alarm" };

function ItemEditor({ item, onClose, onDone }: { item: ScheduleItem; onClose: () => void; onDone: (msg?: string) => void }) {
  const { t } = useI18n();
  const [d, setD] = useState<Draft>(() => draftOf(item));
  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }));
  const { busy, err, run } = useRunner(onDone);
  const alarm = isAlarm(item);
  const task = isTask(item);
  const calls = editCalls(item, d);
  const extraFreq = draftOf(item).freq;
  const rings = ringSummary(item.ringTimes);
  return (
    <form className="sch-form" onSubmit={(e) => { e.preventDefault(); void run(calls); }}>
      <PanelHead kind={t(KIND_NAMES[sourceOf(item)] ?? "Event")} onClose={onClose} />
      <Field label={t("Title")}>
        <input className="sch-input" value={d.title} onChange={(e) => set({ title: e.target.value })} autoFocus />
      </Field>
      {timeEditable(item) ? (
        <div className="sch-two">
          <Field label={alarm && repeats(item) ? t("Starts") : t("Date")}>
            <input className="sch-input" type="date" value={d.date} onChange={(e) => set({ date: e.target.value })} />
          </Field>
          {!d.allDay && !(alarm && item.schedule?.recurrence?.byTime?.length) && (
            <Field label={t("Time")}>
              <input className="sch-input" type="time" value={d.time} onChange={(e) => set({ time: e.target.value })} />
            </Field>
          )}
        </div>
      ) : (
        <Field label={t("Time")} hint={t("A repeating event is moved by telling the queen.")}>
          <div className="sch-static"><RepeatIcon size={14} />{timeLabel(item)} · {repeatLabel(item)}</div>
        </Field>
      )}
      {rings && <div className="sch-note">{t("Rings today")}: {item.ringTimes!.join("  ")}</div>}
      <Field label={t("How it reaches you")}>
        <ModePicker value={d.notify} onChange={(n) => set({ notify: n })} />
      </Field>
      {d.notify !== "none" && !(alarm && d.notify === "alarm") && (
        <Field label={t("Lead")}>
          <LeadPicker value={d.lead} onChange={(n) => set({ lead: n })} />
        </Field>
      )}
      {alarm && (
        <label className="sch-switch">
          <input type="checkbox" checked={d.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
          <i aria-hidden />
          <span>{d.enabled ? t("On") : t("Off")}</span>
          <small>{t("An alarm that is off keeps its settings and does not ring.")}</small>
        </label>
      )}
      {alarm && <RepeatFields d={d} set={set} alarm extra={extraFreq} />}
      {sourceOf(item) === "event" && (
        <Field label={t("Location")}>
          <div className="sch-withicon"><MapPinIcon size={14} /><input className="sch-input" value={d.location} onChange={(e) => set({ location: e.target.value })} /></div>
        </Field>
      )}
      {item.description && <p className="sch-desc">{item.description}</p>}
      {err && <div className="sch-error" role="alert">{err}</div>}
      <footer className="sch-pfoot">
        <DeleteButton busy={busy} note={repeats(item) ? t("Every repeat goes with it. Delete?") : undefined}
          onDelete={() => void run([deleteCall(item)], t("Deleted {title}", { title: item.title }))} />
        <span className="sch-sp" />
        {task && !item.completed && (
          <button type="button" className="sch-btn ghost" disabled={busy} onClick={() => void run([completeCall(item.id, item.title)], t("Done: {title}", { title: item.title }))}>
            <CheckIcon size={14} />{t("Mark done")}
          </button>
        )}
        <button type="submit" className="sch-btn pri" disabled={busy || !calls.length || !d.title.trim()}>{busy ? t("Saving…") : t("Save")}</button>
      </footer>
    </form>
  );
}

function TaskEditor({ task, today, onClose, onDone }: { task: UnscheduledTask; today: string; onClose: () => void; onDone: (msg?: string) => void }) {
  const { t } = useI18n();
  const [d, setD] = useState({ title: task.title, notify: notifyOf(task), date: today, time: "" });
  const { busy, err, run } = useRunner(onDone);
  const calls = taskEditCalls(task, d);
  return (
    <form className="sch-form" onSubmit={(e) => { e.preventDefault(); void run(calls); }}>
      <PanelHead kind={t("To-do")} onClose={onClose} />
      <Field label={t("Title")}><input className="sch-input" value={d.title} onChange={(e) => setD({ ...d, title: e.target.value })} autoFocus /></Field>
      <div className="sch-two">
        <Field label={t("Date")}><input className="sch-input" type="date" value={d.date} onChange={(e) => setD({ ...d, date: e.target.value })} /></Field>
        <Field label={t("Time")}><input className="sch-input" type="time" value={d.time} onChange={(e) => setD({ ...d, time: e.target.value })} /></Field>
      </div>
      <div className="sch-note">{t("Give it a time to put it on the schedule.")}</div>
      <Field label={t("How it reaches you")}><ModePicker value={d.notify} onChange={(n) => setD({ ...d, notify: n })} /></Field>
      {err && <div className="sch-error" role="alert">{err}</div>}
      <footer className="sch-pfoot">
        <span className="sch-sp" />
        <button type="button" className="sch-btn ghost" disabled={busy} onClick={() => void run([completeCall(task.id, task.title)], t("Done: {title}", { title: task.title }))}><CheckIcon size={14} />{t("Mark done")}</button>
        <button type="submit" className="sch-btn pri" disabled={busy || !calls.length || !d.title.trim()}>{busy ? t("Saving…") : t("Save")}</button>
      </footer>
    </form>
  );
}

function NewEditor({ kind, day, onKind, onClose, onDone }: { kind: NewKind; day: string; onKind: (k: NewKind) => void; onClose: () => void; onDone: (msg?: string) => void }) {
  const { t } = useI18n();
  const [d, setD] = useState<Draft>(() => newDraft(kind, day));
  const [timed, setTimed] = useState(kind !== "todo");
  useEffect(() => { setD((x) => ({ ...newDraft(kind, day), title: x.title, date: x.date })); setTimed(kind !== "todo"); }, [kind, day]);
  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }));
  const { busy, err, run } = useRunner(onDone);
  const alarmLike = kind === "alarm" || kind === "reminder";
  const draft = kind === "todo" && !timed ? { ...d, time: "" } : d;
  const placeholder: Record<NewKind, string> = { event: "e.g. Dentist", alarm: "e.g. Get up", reminder: "e.g. Take the medicine", todo: "e.g. Buy milk" };
  return (
    <form className="sch-form" onSubmit={(e) => { e.preventDefault(); if (d.title.trim()) void run([createCall(kind, draft)], t("Added {title}", { title: d.title.trim() })); }}>
      <PanelHead kind={t("New")} onClose={onClose} />
      <div className="sch-seg kinds" role="radiogroup" aria-label={t("Kind")}>
        {NEW_KINDS.map((k) => <button key={k} type="button" role="radio" aria-checked={k === kind} className={k === kind ? "on" : ""} onClick={() => onKind(k)}>{newKindLabel(k)}</button>)}
      </div>
      <Field label={t("Title")}>
        <input className="sch-input" value={d.title} placeholder={t(placeholder[kind])} onChange={(e) => set({ title: e.target.value })} autoFocus />
      </Field>
      {kind === "todo" && (
        <label className="sch-switch">
          <input type="checkbox" checked={timed} onChange={(e) => setTimed(e.target.checked)} />
          <i aria-hidden /><span>{t("At a time")}</span>
          <small>{t("Without a time it waits under To-dos with no time.")}</small>
        </label>
      )}
      {(kind !== "todo" || timed) && (
        <div className="sch-two">
          <Field label={alarmLike && d.freq !== "once" ? t("Starts") : t("Date")}>
            <input className="sch-input" type="date" value={d.date} onChange={(e) => set({ date: e.target.value })} />
          </Field>
          {!d.allDay && (
            <Field label={t("Time")}>
              <input className="sch-input" type="time" value={d.time} onChange={(e) => set({ time: e.target.value })} />
            </Field>
          )}
        </div>
      )}
      {kind === "event" && (
        <label className="sch-check-row"><input type="checkbox" checked={d.allDay} onChange={(e) => set({ allDay: e.target.checked, notify: e.target.checked ? "none" : "reminder" })} />{t("All day")}</label>
      )}
      {(kind === "event" || (kind === "todo" && timed)) && (
        <Field label={t("How it reaches you")}>
          <ModePicker value={d.notify} onChange={(n) => set({ notify: n })} />
        </Field>
      )}
      {(kind === "event" || (kind === "todo" && timed)) && d.notify !== "none" && (
        <Field label={t("Lead")}><LeadPicker value={d.lead} onChange={(n) => set({ lead: n })} /></Field>
      )}
      {alarmLike && <div className="sch-note">{kind === "alarm" ? t("An alarm rings on the phone like an alarm clock, through silent mode, until stopped.") : t("A reminder is a notification on the phone at the time, with snooze.")}</div>}
      {(alarmLike || kind === "event") && <RepeatFields d={d} set={set} alarm={alarmLike} />}
      {kind === "event" && (
        <Field label={t("Location")}>
          <div className="sch-withicon"><MapPinIcon size={14} /><input className="sch-input" value={d.location} onChange={(e) => set({ location: e.target.value })} /></div>
        </Field>
      )}
      {err && <div className="sch-error" role="alert">{err}</div>}
      <footer className="sch-pfoot">
        <span className="sch-sp" />
        <button type="button" className="sch-btn ghost" onClick={onClose}>{t("Cancel")}</button>
        <button type="submit" className="sch-btn pri" disabled={busy || !d.title.trim()}>{busy ? t("Saving…") : t("Add")}</button>
      </footer>
    </form>
  );
}

/** 说一句来新建: tell the queen, in your own words; she writes it into noted. */
function AskQueen({ onAnswered }: { onAnswered: () => void }) {
  const { t } = useI18n();
  const ime = useImeGuard();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [answer, setAnswer] = useState<{ text: string; bad?: boolean } | null>(null);
  const req = useRef("");
  const answered = useRef(onAnswered);
  answered.current = onAnswered;
  useEffect(() => {
    const end = (p: any, bad: boolean) => {
      if (!req.current || p?.requestId !== req.current) return;
      req.current = "";
      setBusy(false);
      setAnswer({ text: String((bad ? p.error : p.final) ?? "").trim(), bad });
      answered.current();
    };
    const offs = [EventsOn("chat:done", (p: any) => end(p, false)), EventsOn("chat:error", (p: any) => end(p, true))];
    return () => offs.forEach((f) => typeof f === "function" && f());
  }, []);
  const send = async () => {
    const s = text.trim();
    if (!s || busy) return;
    setBusy(true); setAnswer(null); setText("");
    try {
      const session = await loadQueenSession();
      req.current = await SendChat(session, s, []);
    } catch (e: any) {
      setBusy(false);
      setAnswer({ text: String(e?.message ?? e), bad: true });
    }
  };
  return (
    <div className="sch-ask">
      <form onSubmit={(e) => { e.preventDefault(); void send(); }}>
        <input value={text} onChange={(e) => setText(e.target.value)} {...ime.handlers}
          onKeyDown={(e) => { if (e.key === "Enter" && ime.composing(e)) e.preventDefault(); }}
          placeholder={t("Add by saying it…")} aria-label={t("Tell the queen")} />
        <button type="submit" disabled={!text.trim() || busy} title={t("Send")} aria-label={t("Send")}>
          {busy ? <span className="sch-spin" /> : <ArrowUpIcon size={16} />}
        </button>
      </form>
      {busy && <div className="sch-said">{t("The queen is on it…")}</div>}
      {answer && answer.text && (
        <div className={`sch-said${answer.bad ? " bad" : ""}`}>
          <span>{answer.text}</span>
          <button onClick={() => setAnswer(null)} aria-label={t("Close")}><XIcon size={13} /></button>
        </div>
      )}
    </div>
  );
}
