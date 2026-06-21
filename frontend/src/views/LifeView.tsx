import React, { useCallback, useEffect, useState } from "react";
import { Life } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";

type TabKey = "schedules" | "records" | "persons" | "reminders";

const TABS: { key: TabKey; label: string; icon: string }[] = [
  { key: "schedules", label: "Schedules", icon: "📅" },
  { key: "records", label: "Records", icon: "📝" },
  { key: "persons", label: "Persons", icon: "👤" },
  { key: "reminders", label: "Reminders", icon: "⏰" },
];

const EMPTY_HINT: Record<TabKey, string> = {
  schedules: "No schedules yet — ask SuperAI in Chat to add one.",
  records: "No records yet — ask SuperAI in Chat to keep notes for you.",
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

function InlineEmpty({ icon, hint }: { icon: string; hint: string }) {
  return (
    <div className="inline-empty">
      <div className="ie-icon">{icon}</div>
      <div>Nothing here yet.</div>
      <div className="ie-hint">{hint}</div>
    </div>
  );
}

export default function LifeView() {
  const [data, setData] = useState<backend.LifeData | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string>("");
  const [tab, setTab] = useState<TabKey>("schedules");

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

  const schedules = Array.isArray(data?.schedules) ? data!.schedules : [];
  const records = Array.isArray(data?.records) ? data!.records : [];
  const reminders = Array.isArray(data?.reminders) ? data!.reminders : [];
  const persons = data?.persons && typeof data.persons === "object" ? data.persons : {};
  const personEntries = Object.entries(persons);

  const count: Record<TabKey, number> = {
    schedules: schedules.length,
    records: records.length,
    persons: personEntries.length,
    reminders: reminders.length,
  };

  const renderArrayTab = (items: any[], key: TabKey, icon: string) => {
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
          <div className="view-title">Life</div>
          <div className="view-desc">Schedules, records, people and reminders SuperAI keeps for you. Edits happen via Chat.</div>
        </div>
        <div className="vh-actions">
          <button className="btn ghost sm" onClick={load} disabled={loading}>
            {loading ? <><span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…</> : "↻ Refresh"}
          </button>
        </div>
      </div>

      <div className="tabs">
        {TABS.map((t) => (
          <button key={t.key} className={`tab${tab === t.key ? " active" : ""}`} onClick={() => setTab(t.key)}>
            {t.icon} {t.label}
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
            {tab === "schedules" && renderArrayTab(schedules, "schedules", "📅")}
            {tab === "records" && renderArrayTab(records, "records", "📝")}
            {tab === "reminders" && renderArrayTab(reminders, "reminders", "⏰")}
            {tab === "persons" &&
              (personEntries.length === 0 ? (
                <InlineEmpty icon="👤" hint={EMPTY_HINT.persons} />
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
