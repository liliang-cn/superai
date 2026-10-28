import type React from "react";
import { useEffect, useRef, useState } from "react";
import { ArrowDownIcon, ArrowUpIcon, SlidersHorizontalIcon } from "lucide-react";
import { Dashboard } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { fmtK, pillarReach, usePulse, type Snap } from "./Reactor";
import type { Hive } from "../canvas/data";
import type { CodingRun } from "../canvas/tiles";
import { useI18n } from "../lib/i18n";
import "./statsblock.css";

/** What a figure is read from. usage is the core's token ledger, read when
 *  the block opens and again whenever a turn ends. */
interface Sources { pulse: Snap; hive: Hive; runs: CodingRun[]; usage: { today: number; total: number } | null }

interface Figure {
  key: string;
  /** The name in the chooser and on hover. */
  label: string;
  /** The name under the pillar, where there is room for a word. */
  short: string;
  value: (s: Sources) => number;
  text?: (s: Sources) => string;
  /** Goes up and down on its own (cpu, heap): no flash when it rises. */
  gauge?: boolean;
  /** Red when above zero. */
  bad?: boolean;
  usage?: boolean;
}

/** Every figure the home can stand up, in the order the chooser lists them. */
export const FIGURES: Figure[] = [
  { key: "today", label: "tokens today", short: "today", value: (s) => s.usage?.today ?? 0, usage: true },
  { key: "total", label: "tokens all time", short: "all time", value: (s) => s.usage?.total ?? 0, usage: true },
  { key: "tokens", label: "tokens this process", short: "tokens", value: (s) => s.pulse.tokens },
  { key: "cached", label: "cached", short: "cached", value: (s) => s.pulse.cached },
  { key: "turns", label: "turns", short: "turns", value: (s) => s.pulse.rounds },
  { key: "calls", label: "tool calls", short: "calls", value: (s) => s.pulse.calls },
  { key: "reads", label: "reads", short: "reads", value: (s) => s.pulse.reads },
  { key: "writes", label: "writes", short: "writes", value: (s) => s.pulse.writes },
  { key: "shells", label: "shells", short: "shells", value: (s) => s.pulse.shells },
  { key: "mcp", label: "mcp calls", short: "mcp", value: (s) => s.pulse.mcp },
  { key: "memory", label: "memory calls", short: "memory", value: (s) => s.pulse.memory },
  { key: "fails", label: "failed", short: "failed", value: (s) => s.pulse.fails, bad: true },
  { key: "cpu", label: "cpu", short: "cpu", value: (s) => s.pulse.cpu, text: (s) => `${Math.round(s.pulse.cpu)}%`, gauge: true },
  { key: "heap", label: "heap", short: "heap", value: (s) => s.pulse.heap / 1048576, text: (s) => `${Math.round(s.pulse.heap / 1048576)} MB`, gauge: true },
  { key: "goroutines", label: "goroutines", short: "goroutines", value: (s) => s.pulse.goroutines, gauge: true },
  { key: "orders", label: "orders in flight", short: "orders", value: (s) => s.hive.tasks.filter((t) => t.state === "running").length },
  { key: "coding", label: "coding runs going", short: "coding", value: (s) => s.runs.filter((r) => r.state === "running").length },
];
const BY_KEY = new Map(FIGURES.map((f) => [f.key, f]));
export const DEFAULT_FIGURES = ["tokens", "cached", "turns", "calls", "memory", "fails", "cpu", "heap", "orders", "coding"];

// Which figures the home shows, in order. Kept in this browser, like the
// home's other choices (its mode, its theme, its tiles); every block on the
// page reads the same list and hears when it changes.
const KEY = "superleo-home-stats";
const CHANGED = "superleo:home-stats";

function readChoice(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || "null");
    if (Array.isArray(v)) return v.filter((k): k is string => typeof k === "string" && BY_KEY.has(k));
  } catch { /* the defaults */ }
  return DEFAULT_FIGURES;
}

export function useStatChoice(): [string[], (next: string[]) => void] {
  const [keys, setKeys] = useState<string[]>(readChoice);
  useEffect(() => {
    const again = () => setKeys(readChoice());
    const other = (e: StorageEvent) => { if (e.key === KEY) again(); };
    window.addEventListener(CHANGED, again);
    window.addEventListener("storage", other);
    return () => { window.removeEventListener(CHANGED, again); window.removeEventListener("storage", other); };
  }, []);
  const set = (next: string[]) => {
    setKeys(next);
    try { localStorage.setItem(KEY, JSON.stringify(next)); } catch { /* this visit only */ }
    window.dispatchEvent(new Event(CHANGED));
  };
  return [keys, set];
}

/** The ledger's totals, when a figure needs them: once now, then after each
 *  turn the core finishes. */
function useUsage(wanted: boolean) {
  const [usage, setUsage] = useState<Sources["usage"]>(null);
  useEffect(() => {
    if (!wanted) return;
    let alive = true;
    const load = () => {
      Dashboard()
        .then((d: any) => { if (alive && d?.usage) setUsage({ today: Number(d.usage.today ?? 0), total: Number(d.usage.totalTokens ?? 0) }); })
        .catch(() => {});
    };
    load();
    const offs = ["chat:done", "chat:error"].map((n) => EventsOn(n, load));
    return () => { alive = false; offs.forEach((off) => typeof off === "function" && off()); };
  }, [wanted]);
  return usage;
}

/**
 * The core's figures as the reactor stands them: one pillar per figure, its
 * number at the tip, full even when nothing runs. A pillar whose figure just
 * rose lights in the signal colour. Which figures, and in what order, is the
 * person's choice.
 */
export default function StatsBlock({ hive, runs, compact, className }: { hive: Hive; runs: CodingRun[]; compact?: boolean; className?: string }) {
  const { t } = useI18n();
  const pulse = usePulse();
  const [keys, setKeys] = useStatChoice();
  const [choosing, setChoosing] = useState(false);
  const figures = keys.map((k) => BY_KEY.get(k)!).filter(Boolean);
  const usage = useUsage(figures.some((f) => f.usage));
  const src: Sources = { pulse, hive, runs, usage };
  const values = figures.map((f) => Math.max(0, f.value(src)));
  const reach = pillarReach(values);
  // Short names once the pillars stand closer than a long name is wide.
  const rowRef = useRef<HTMLDivElement>(null);
  const [tight, setTight] = useState(false);
  useEffect(() => {
    const el = rowRef.current;
    if (!el) return;
    const fit = () => setTight(el.clientWidth / Math.max(1, figures.length) < 60);
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    fit();
    return () => ro.disconnect();
  }, [figures.length]);
  const brief = compact || tight;

  // A figure that rose since the last frame gets its flash restarted; the
  // first reading is where it stands, not a rise.
  const last = useRef(new Map<string, number>());
  const flash = useRef(new Map<string, number>());
  figures.forEach((f, i) => {
    if (!pulse.now || (f.usage && !usage)) return;
    const before = last.current.get(f.key);
    if (!f.gauge && before !== undefined && values[i] > before) flash.current.set(f.key, (flash.current.get(f.key) ?? 0) + 1);
    last.current.set(f.key, values[i]);
  });

  return (
    <div className={`sb${compact ? " compact" : ""}${className ? " " + className : ""}`}>
      <div className="sb-head">
        <i className={pulse.live ? "sb-dot on" : "sb-dot"} />
        <span>{t(pulse.live ? "Working now" : "Idle")}</span>
        <button type="button" className={choosing ? "sb-pick on" : "sb-pick"} aria-expanded={choosing} aria-label={t("Choose figures")} title={t("Choose figures")}
          onClick={() => setChoosing((v) => !v)}>
          <SlidersHorizontalIcon size={14} />
        </button>
      </div>
      {figures.length === 0 ? (
        <button type="button" className="sb-none" onClick={() => setChoosing(true)}>{t("Choose figures")}</button>
      ) : (
        <div className={brief ? "sb-row brief" : "sb-row"} role="list" ref={rowRef}>
          {figures.map((f, i) => {
            const v = values[i];
            const full = f.text ? f.text(src) : fmtK(v);
            const text = brief ? full.replace(" ", "") : full;
            const n = flash.current.get(f.key) ?? 0;
            return (
              <div key={f.key} className={`sb-col${f.bad && v > 0 ? " bad" : ""}`} role="listitem" title={`${full} ${t(f.label)}`}
                style={{ "--r": reach[i] } as React.CSSProperties}>
                <div className="sb-track">
                  <i key={n} className={n ? "sb-pillar lit" : "sb-pillar"} />
                  <b>{text}</b>
                </div>
                <span>{t(brief ? f.short : f.label)}</span>
              </div>
            );
          })}
        </div>
      )}
      {choosing && <Chooser keys={keys} onChange={setKeys} onClose={() => setChoosing(false)} />}
    </div>
  );
}

/** Which figures stand, and in what order: the chosen ones first, as they
 *  stand, then the rest. */
function Chooser({ keys, onChange, onClose }: { keys: string[]; onChange: (k: string[]) => void; onClose: () => void }) {
  const { t } = useI18n();
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const away = (e: PointerEvent) => {
      const el = e.target as HTMLElement;
      if (!box.current?.contains(el) && !el.closest?.(".sb-pick")) onClose();
    };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") { e.stopPropagation(); onClose(); } };
    window.addEventListener("pointerdown", away);
    window.addEventListener("keydown", esc, true);
    box.current?.querySelector("input")?.focus();
    return () => { window.removeEventListener("pointerdown", away); window.removeEventListener("keydown", esc, true); };
  }, [onClose]);
  const rest = FIGURES.filter((f) => !keys.includes(f.key));
  const move = (i: number, by: number) => {
    const j = i + by;
    if (j < 0 || j >= keys.length) return;
    const next = keys.slice();
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };
  return (
    <div className="sb-chooser" ref={box} role="dialog" aria-label={t("Choose figures")}>
      <ul>
        {keys.map((k, i) => (
          <li key={k}>
            <label><input type="checkbox" checked onChange={() => onChange(keys.filter((x) => x !== k))} />{t(BY_KEY.get(k)!.label)}</label>
            <button type="button" aria-label={t("Move up")} title={t("Move up")} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUpIcon size={13} /></button>
            <button type="button" aria-label={t("Move down")} title={t("Move down")} disabled={i === keys.length - 1} onClick={() => move(i, 1)}><ArrowDownIcon size={13} /></button>
          </li>
        ))}
        {rest.map((f, i) => (
          <li key={f.key} className={i === 0 && keys.length > 0 ? "off split" : "off"}>
            <label><input type="checkbox" checked={false} onChange={() => onChange([...keys, f.key])} />{t(f.label)}</label>
          </li>
        ))}
      </ul>
      <footer>
        <button type="button" onClick={() => onChange(DEFAULT_FIGURES)}>{t("Reset")}</button>
        <button type="button" className="pri" onClick={onClose}>{t("Done")}</button>
      </footer>
    </div>
  );
}
