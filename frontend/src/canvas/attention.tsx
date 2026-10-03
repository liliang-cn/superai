import { getLanguage, translate } from "../lib/i18n";
import { useEffect, useState } from "react";
import { AlertTriangleIcon, BellIcon, CalendarIcon, CircleSlashIcon, TerminalIcon, BotIcon } from "lucide-react";
import { Attention } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";

export interface AttentionItem {
  level: "needs" | "soon";
  kind: "approval" | "bee" | "failed" | "lost" | "run" | "event" | "reminder" | string;
  title: string;
  detail?: string;
  at?: string;
  zone?: string;
  place?: string;
  ref?: string;
  open?: string;
}

const TRIGGERS = ["hive:task", "cli:run", "agent:update", "schedule:changed", "schedule:run", "tool:approval", "tool:approval:resolved", "tool:approval:closed", "agents:changed"];

/** What needs the person, then what is coming up — the core's one answer,
 *  kept current by the events that change it and a minute's clock. */
export function useAttentionState() {
  const [items, setItems] = useState<AttentionItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let alive = true;
    let timer = 0;
    const load = () => Attention().then((list) => {
      if (alive) { setItems((list ?? []) as unknown as AttentionItem[]); setError(""); }
    }).catch(() => { if (alive) setError("Could not load items that need your attention."); })
      .finally(() => { if (alive) setLoading(false); });
    const soon = () => { window.clearTimeout(timer); timer = window.setTimeout(load, 250); };
    void load();
    const offs = TRIGGERS.map(n => EventsOn(n, soon));
    const poll = window.setInterval(load, 15000);
    return () => { alive = false; window.clearInterval(poll); window.clearTimeout(timer); offs.forEach(o => typeof o === "function" && o()); };
  }, [revision]);
  return { items, loading, error, refresh: () => setRevision(n => n + 1) };
}
export function useAttention(): AttentionItem[] { return useAttentionState().items; }

export function attentionTitle(it: AttentionItem) {
  const suffixes: Record<string,string> = {approval:" asks to run something",bee:" is waiting for you",failed:" could not finish an order",lost:" is not answering",run:" stopped with an error"};
  const suffix = suffixes[it.kind];
  return suffix && it.title.endsWith(suffix) ? translate("{name}" + suffix, {name:it.title.slice(0,-suffix.length)}) : it.title;
}

const DAY = 24 * 3600 * 1000;

/** "Tue 15:00", "Today 15:00", "Tomorrow 09:30" — and the meeting's own time
 *  beside it when it was set somewhere else: "(09:00 Vienna)". */
export function whenOf(it: AttentionItem, now = new Date()): string {
  if (!it.at) return "";
  const d = new Date(it.at);
  const hm = (x: Date) => x.toLocaleTimeString(getLanguage(), { hour: "2-digit", minute: "2-digit", hour12: false });
  const start = new Date(now); start.setHours(0, 0, 0, 0);
  const days = Math.floor((d.getTime() - start.getTime()) / DAY);
  let day: string;
  if (it.level === "needs") {
    const mins = Math.round((now.getTime() - d.getTime()) / 60000);
    return mins < 60 ? translate("{count}m ago", {count:Math.max(1,mins)}) : mins < 1440 ? translate("{count}h ago", {count:Math.round(mins/60)}) : translate("{count}d ago", {count:Math.round(mins/1440)});
  }
  if (days === 0) day = translate("Today");
  else if (days === 1) day = translate("Tomorrow");
  else day = d.toLocaleDateString(getLanguage(), { weekday: "short" });
  let out = `${day} ${hm(d)}`;
  if (it.zone) {
    const [sign, h, m] = [it.zone[0] === "-" ? -1 : 1, Number(it.zone.slice(1, 3)), Number(it.zone.slice(4, 6))];
    const theirs = sign * (h * 60 + m);
    const ours = -d.getTimezoneOffset();
    if (theirs !== ours) {
      const there = new Date(d.getTime() + (theirs - ours) * 60000);
      out += ` (${hm(there)} ${it.place || "UTC" + it.zone})`;
    }
  }
  return out;
}

const ICON: Record<string, typeof BellIcon> = {
  approval: AlertTriangleIcon, bee: BotIcon, failed: AlertTriangleIcon, lost: CircleSlashIcon,
  run: TerminalIcon, event: CalendarIcon, reminder: BellIcon,
};

/** The list itself, for any screen. */
export function AttentionList({ items, onOpen, skip = [], limit = 8 }: { items: AttentionItem[]; onOpen?: (it: AttentionItem) => void; skip?: string[]; limit?: number }) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => { const t = window.setInterval(() => setNow(new Date()), 60000); return () => window.clearInterval(t); }, []);
  const shown = items.filter((i) => !skip.includes(i.kind)).slice(0, limit);
  const needs = shown.filter((i) => i.level === "needs");
  const soon = shown.filter((i) => i.level === "soon");
  const row = (it: AttentionItem, i: number) => {
    const Icon = ICON[it.kind] ?? BellIcon;
    return (
      <button key={`${it.kind}-${it.ref}-${i}`} className={`att-row ${it.level}`} onClick={() => onOpen?.(it)}>
        <span className="att-ic"><Icon size={15} /></span>
        <span className="att-main">
          <b>{attentionTitle(it)}</b>
          {it.detail && <small>{it.detail}</small>}
        </span>
        <em>{whenOf(it, now)}</em>
      </button>
    );
  };
  return (
    <div className="att">
      {needs.length > 0 && <div className="att-h needs">Needs you</div>}
      {needs.map(row)}
      {soon.length > 0 && <div className="att-h">Coming up</div>}
      {soon.map(row)}
    </div>
  );
}
