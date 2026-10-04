import { calendarEvents } from "../lib/calendarEvents";
import { getLanguage, translate, useI18n } from "../lib/i18n";
import { useSearchParams } from "react-router-dom";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { Life } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { AppStatus } from "../lib/types";
import { ScheduleRunLog } from "../lib/useScheduleRuns";
import SchedulesView from "./SchedulesView";
import { AlarmClockIcon, CalendarClockIcon, NotebookPenIcon, UserIcon, type LucideIcon } from "lucide-react";

// The tab keyed "records" is labelled Notes, and used to be keyed that way
// too — inside a page that is itself called Records. Two different things with
// one name, one nested in the other. The key follows the label now; the backend
// field it reads is still `records`, which is where the name came from and the
// one place it is not ambiguous.
type TabKey = "schedules" | "notes" | "persons" | "reminders";

const TABS: { key: TabKey; label: string; icon: LucideIcon }[] = [
  { key: "schedules", label: "Schedules", icon: CalendarClockIcon },
  { key: "notes", label: "Notes", icon: NotebookPenIcon },
  { key: "persons", label: "People", icon: UserIcon },
  { key: "reminders", label: "Reminders", icon: AlarmClockIcon },
];

const EMPTY_HINT: Record<TabKey, string> = {
  schedules: "No schedules yet — ask SuperAI in Chat to add one.",
  notes: "No notes yet — ask SuperAI in Chat to keep notes for you.",
  persons: "No people yet — mention someone in Chat and SuperAI will remember them.",
  reminders: "No reminders yet — ask SuperAI in Chat to remind you of something.",
};

// Known display fields, in priority order, for loose map[string]any objects.
const TITLE_KEYS = ["subject", "title", "name", "summary"];
const FIELD_ORDER = ["time", "at", "when", "date", "type", "content", "text", "note", "notes", "description"];

function asString(v: any): string {
  if (v == null) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

function RecordCard({ obj, fallbackTitle }: { obj: any; fallbackTitle?: string }) {
  if (obj == null || typeof obj !== "object") {
    return (
      <div className="record-card">
        <div className="rc-title">{fallbackTitle ?? "Item"}</div>
        <div className="rc-row">
          <span className="rc-val">{asString(obj)}</span>
        </div>
      </div>
    );
  }

  const entries = Object.entries(obj as Record<string, any>);
  const titleKey = TITLE_KEYS.find((k) => obj[k] != null && asString(obj[k]).trim() !== "");
  const title = titleKey ? asString(obj[titleKey]) : fallbackTitle ?? "Item";

  // Build ordered set of "known" fields to show as labelled rows (excluding the title field).
  const shown = new Set<string>(titleKey ? [titleKey] : []);
  const rows: { key: string; val: string }[] = [];
  for (const k of FIELD_ORDER) {
    if (shown.has(k)) continue;
    if (obj[k] != null && asString(obj[k]).trim() !== "") {
      rows.push({ key: k, val: asString(obj[k]) });
      shown.add(k);
    }
  }

  // Remaining unknown fields → dump as JSON so nothing is silently dropped.
  const rest: Record<string, any> = {};
  for (const [k, v] of entries) {
    if (!shown.has(k) && !(titleKey && k === titleKey)) rest[k] = v;
  }
  const hasRest = Object.keys(rest).length > 0;

  return (
    <div className="record-card">
      <div className="rc-title">{title}</div>
      {rows.map((r) => (
        <div className="rc-row" key={r.key}>
          <span className="rc-key">{r.key}</span>
          <span className="rc-val">{r.val}</span>
        </div>
      ))}
      {hasRest && <div className="rc-json">{JSON.stringify(rest, null, 2)}</div>}
    </div>
  );
}

function InlineEmpty({ icon: Icon, hint }: { icon: LucideIcon; hint: string }) {
  return (
    <div className="inline-empty">
      <Icon className="ie-icon" size={22} strokeWidth={1.6} />
      <div>Nothing here yet.</div>
      <div className="ie-hint">{hint}</div>
    </div>
  );
}

export default function RecordsView({
  status,
  log,
  onOpenConversation,
}: {
  status: AppStatus | null;
  log: ScheduleRunLog;
  onOpenConversation: (session: string) => void;
}) {
  const { t } = useI18n();
  const [data, setData] = useState<backend.LifeData | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [params] = useSearchParams();
  const [tab, setTab] = useState<TabKey>(() => params.get("tab") === "reminders" ? "reminders" : "schedules");
  useEffect(() => { if (params.get("tab") === "reminders") setTab("reminders"); }, [params]);
  // The scheduler's own count, reported by the view that owns it. Life() has a
  // schedules field too, but it is a second answer to the same question and the
  // two drift.
  const [scheduleCount, setScheduleCount] = useState(0);
  const stripRef = useRef<HTMLDivElement | null>(null);
  // Whether the strip is scrolled to its right end, which is what takes the
  // edge fade off. Settings' strip answers the same question the same way; two
  // strips that scroll should not disagree about how they say so.
  const [stripAtEnd, setStripAtEnd] = useState(false);

  const syncStripFade = useCallback(() => {
    const el = stripRef.current;
    if (!el) return;
    // Sub-pixel widths make the exact equality unreachable at some zoom levels.
    setStripAtEnd(el.scrollLeft + el.clientWidth >= el.scrollWidth - 1);
  }, []);

  useEffect(() => {
    syncStripFade();
    window.addEventListener("resize", syncStripFade);
    return () => window.removeEventListener("resize", syncStripFade);
  }, [syncStripFade]);

  const load = useCallback(async () => {
    setLoading(true);
    setErr("");
    try {
      const res = await Life();
      setData(res);
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // Four tabs carrying count badges measure about 570px; a phone window is 390.
  // The strip scrolls sideways rather than wrapping (see styles.css), which
  // means the active tab can be sitting off the right edge — on first paint, or
  // after anything that changes the tab without a tap. Scrolled by hand rather
  // than with scrollIntoView: that walks up to *every* scrollable ancestor, so
  // bringing a tab into view would also drag the run list underneath it.
  useEffect(() => {
    const strip = stripRef.current;
    const active = strip?.querySelector<HTMLElement>(".tab.active");
    if (!strip || !active) return;
    const box = strip.getBoundingClientRect();
    const it = active.getBoundingClientRect();
    // 24px so the tab lands clear of the strip's own inset rather than flush
    // against it, which reads as clipped.
    if (it.left < box.left) strip.scrollLeft -= box.left - it.left + 24;
    else if (it.right > box.right) strip.scrollLeft += it.right - box.right + 24;
    syncStripFade();
  }, [tab, syncStripFade]);

  const schedules = calendarEvents(Array.isArray(data?.schedules) ? data!.schedules : []);
  const records = Array.isArray(data?.records) ? data!.records : [];
  const reminders = Array.isArray(data?.reminders) ? data!.reminders : [];
  const persons = data?.persons && typeof data.persons === "object" ? data.persons : {};
  const personEntries = Object.entries(persons);

  const count: Record<TabKey, number> = {
    schedules: scheduleCount + schedules.length,
    notes: records.length,
    persons: personEntries.length,
    reminders: reminders.length,
  };

  const renderArrayTab = (items: any[], key: TabKey, icon: LucideIcon) => {
    if (items.length === 0) return <InlineEmpty icon={icon} hint={EMPTY_HINT[key]} />;
    return (
      <div className="record-list">
        {items.map((it, i) => (
          <RecordCard key={i} obj={it} fallbackTitle={`${TABS.find((t) => t.key === key)?.label.replace(/s$/, "")} ${i + 1}`} />
        ))}
      </div>
    );
  };

  return (
    <div className="view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">{t("Records")}</div>
          <div className="view-desc">{t("Schedules, notes, people and reminders SuperAI keeps for you. Edits happen via Chat.")}</div>
        </div>
        {/* The Schedules tab reloads itself from the scheduler and has its own
            button. Showing this one too would stack two Refreshes that refresh
            different things. */}
        {tab !== "schedules" && (
          <div className="vh-actions">
            <button className="btn ghost sm" onClick={load} disabled={loading}>
              {loading ? <><span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…</> : "↻ Refresh"}
            </button>
          </div>
        )}
      </div>

      <div
        className={`tabs${stripAtEnd ? " at-end" : ""}`}
        ref={stripRef}
        role="tablist"
        onScroll={syncStripFade}
      >
        {TABS.map((t) => (
          <button
            key={t.key}
            role="tab"
            aria-selected={tab === t.key}
            className={`tab${tab === t.key ? " active" : ""}`}
            onClick={() => setTab(t.key)}
          >
            <t.icon size={14} strokeWidth={1.8} /> {translate(t.label)}
            <span className="tab-count">{count[t.key]}</span>
          </button>
        ))}
      </div>

      <div className="panel-scroll">
        {err && <div className="report-error">⚠ {err}</div>}
        {!err && loading && !data && (
          <div className="loading-row">
            <span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading life data…
          </div>
        )}
        {!err && data && (
          <>
            {tab === "schedules" && (
              <>
              <CalendarAgenda items={schedules}/>
              <SchedulesView
                embedded
                status={status}
                log={log}
                onOpenConversation={onOpenConversation}
                onCount={setScheduleCount}
              />
              </>
            )}
            {tab === "notes" && renderArrayTab(records, "notes", NotebookPenIcon)}
            {tab === "reminders" && renderArrayTab(reminders, "reminders", AlarmClockIcon)}
            {tab === "persons" &&
              (personEntries.length === 0 ? (
                <InlineEmpty icon={UserIcon} hint={EMPTY_HINT.persons} />
              ) : (
                <div className="record-list">
                  {personEntries.map(([name, info]) => (
                    <RecordCard key={name} obj={info} fallbackTitle={name} />
                  ))}
                </div>
              ))}
          </>
        )}
      </div>
    </div>
  );
}

/** Personal calendar entries are distinct from the scheduler's automated routines. */
export function CalendarAgenda({ items }: { items: Record<string, any>[] }) {
  const { t } = useI18n();
  const events = calendarEvents(items);
  if (!events.length) return null;
  const when = (value: unknown) => {
    const raw = typeof value === "string" ? value : "";
    const date = new Date(raw);
    return Number.isFinite(date.getTime()) ? date.toLocaleString(getLanguage(), {year:"numeric",month:"short",day:"numeric",weekday:"short",hour:"2-digit",minute:"2-digit"}) : raw;
  };
  return <section className="calendar-agenda" aria-label={t("Calendar")}>
    <h2>{t("Calendar")} <span>{events.length}</span></h2>
    <div className="record-list">{events.map((event,index) => <article className="record-card" key={String(event.id ?? index)}>
      <div className="rc-title">{String(event.title ?? event.subject ?? t("Event"))}</div>
      {event.start_at && <div className="rc-row"><span className="rc-key">{t("Time")}</span><time className="rc-val" dateTime={String(event.start_at)} title={String(event.start_at)}>{when(event.start_at)}</time></div>}
      {event.location && <div className="rc-row"><span className="rc-key">{t("Location")}</span><span className="rc-val">{String(event.location)}</span></div>}
      {Array.isArray(event.participants) && event.participants.length > 0 && <div className="rc-row"><span className="rc-key">{t("Participants")}</span><span className="rc-val">{event.participants.join(", ")}</span></div>}
    </article>)}</div>
  </section>;
}
