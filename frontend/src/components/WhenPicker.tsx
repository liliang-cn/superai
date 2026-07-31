import React from "react";
import {
  DEFAULT_WHEN,
  WEEKDAY_LABELS,
  WEEKDAY_ORDER,
  When,
  WhenMode,
  clampInt,
  describeCron,
  previewCron,
  whenProblem,
  whenToCron,
} from "../lib/cron";
import { formatStamp, fromNow } from "../lib/format";

const MODES: { key: WhenMode; label: string }[] = [
  { key: "daily", label: "Every day" },
  { key: "weekdays", label: "Weekdays" },
  { key: "weekly", label: "Certain days" },
  { key: "monthly", label: "Monthly" },
  { key: "hours", label: "Every few hours" },
  { key: "minutes", label: "Every few minutes" },
  { key: "cron", label: "Cron expression" },
];

/**
 * The wordings people actually arrive with. A preset is a whole state, so
 * picking one and then nudging the time still behaves.
 */
const PRESETS: { label: string; when: Partial<When> }[] = [
  { label: "Every morning at 8:00", when: { mode: "daily", time: "08:00" } },
  { label: "Every weekday at 9:00", when: { mode: "weekdays", time: "09:00" } },
  { label: "Every evening at 21:00", when: { mode: "daily", time: "21:00" } },
  { label: "Every Monday at 9:00", when: { mode: "weekly", days: [1], time: "09:00" } },
  { label: "Every hour", when: { mode: "hours", everyHours: 1, minute: 0 } },
  { label: "1st of the month at 9:00", when: { mode: "monthly", dom: 1, time: "09:00" } },
];

/**
 * Choosing when a prompt should run, without knowing cron.
 *
 * The builder is the primary input and a raw expression is one of its modes,
 * rather than the other way round: "every morning at eight" is the request being
 * made, and "0 8 * * *" is an implementation detail the user is shown (so they
 * can learn it, edit it, or paste one they already had) but never has to write.
 */
export default function WhenPicker({
  when,
  onChange,
}: {
  when: When;
  onChange: (w: When) => void;
}) {
  const patch = (p: Partial<When>) => onChange({ ...when, ...p });
  const cron = whenToCron(when);
  const problem = whenProblem(when);
  // The expression is only previewed once it is complete, so a half-filled
  // builder does not read as a broken schedule.
  const preview = cron === "" ? null : previewCron(cron, 3);
  // No plain-English gloss on an expression that will be refused: "every day at
  // 08:70" reads as if it were fine.
  const words = preview !== null && preview.kind !== "invalid" ? describeCron(cron) : "";

  const toggleDay = (d: number) => {
    const days = when.days.includes(d) ? when.days.filter((x) => x !== d) : [...when.days, d];
    patch({ days });
  };

  return (
    <div className="field">
      <label>When should it run?</label>

      <div className="when-presets">
        {PRESETS.map((p) => {
          const target = { ...DEFAULT_WHEN, ...p.when };
          const active = whenToCron(target) === cron && cron !== "";
          return (
            <button
              key={p.label}
              type="button"
              className={`btn sm${active ? "" : " ghost"}`}
              onClick={() => patch(p.when)}
            >
              {p.label}
            </button>
          );
        })}
      </div>

      <div className="when-modes">
        {MODES.map((m) => (
          <button
            key={m.key}
            type="button"
            className={`btn sm${when.mode === m.key ? "" : " ghost"}`}
            onClick={() => patch({ mode: m.key })}
          >
            {m.label}
          </button>
        ))}
      </div>

      <div className="when-detail">
        {(when.mode === "daily" || when.mode === "weekdays") && (
          <div className="when-inline">
            at
            <input
              className="input when-time"
              type="time"
              aria-label="Time of day"
              value={when.time}
              onChange={(e) => patch({ time: e.target.value })}
            />
          </div>
        )}

        {when.mode === "weekly" && (
          <>
            <div className="when-days">
              {WEEKDAY_ORDER.map((d) => (
                <button
                  key={d}
                  type="button"
                  className={`btn sm${when.days.includes(d) ? "" : " ghost"}`}
                  onClick={() => toggleDay(d)}
                >
                  {WEEKDAY_LABELS[d]}
                </button>
              ))}
            </div>
            <div className="when-inline">
              at
              <input
                className="input when-time"
                type="time"
                aria-label="Time of day"
                value={when.time}
                onChange={(e) => patch({ time: e.target.value })}
              />
            </div>
          </>
        )}

        {when.mode === "monthly" && (
          <div className="when-inline">
            on day
            <input
              className="input when-num"
              type="number"
              min={1}
              max={31}
              aria-label="Day of the month"
              value={when.dom}
              onChange={(e) => patch({ dom: clampInt(Number(e.target.value), 1, 31) })}
            />
            at
            <input
              className="input when-time"
              type="time"
              value={when.time}
              onChange={(e) => patch({ time: e.target.value })}
            />
          </div>
        )}

        {when.mode === "hours" && (
          <div className="when-inline">
            every
            <input
              className="input when-num"
              type="number"
              min={1}
              max={23}
              aria-label="Hours between runs"
              value={when.everyHours}
              onChange={(e) => patch({ everyHours: clampInt(Number(e.target.value), 1, 23) })}
            />
            hours, at minute
            <input
              className="input when-num"
              type="number"
              min={0}
              max={59}
              aria-label="Minute of the hour"
              value={when.minute}
              onChange={(e) => patch({ minute: clampInt(Number(e.target.value), 0, 59) })}
            />
          </div>
        )}

        {when.mode === "minutes" && (
          <div className="when-inline">
            every
            <input
              className="input when-num"
              type="number"
              min={1}
              max={59}
              aria-label="Minutes between runs"
              value={when.everyMinutes}
              onChange={(e) => patch({ everyMinutes: clampInt(Number(e.target.value), 1, 59) })}
            />
            minutes
          </div>
        )}

        {when.mode === "cron" && (
          <>
            <input
              className="input mono"
              aria-label="Cron expression"
              value={when.expr}
              spellCheck={false}
              autoComplete="off"
              placeholder="0 8 * * *"
              onChange={(e) => patch({ expr: e.target.value })}
            />
            <span className="hint">
              minute hour day month weekday. @daily, @hourly and @every 30m work too.
            </span>
          </>
        )}
      </div>

      <div className="when-preview">
        {problem !== "" || preview === null ? (
          <span className="hint err">{problem || "Pick when this should run."}</span>
        ) : preview.kind === "invalid" ? (
          <span className="hint err">{preview.why}</span>
        ) : preview.kind === "unknown" ? (
          <span className="wp-next">SuperAI works out the next run when you save this.</span>
        ) : (
          <>
            <span className="wp-next">
              Next run {formatStamp(preview.runs[0])} · {fromNow(preview.runs[0])}
            </span>
            {preview.runs.length > 1 && (
              <span className="wp-then">
                then {preview.runs.slice(1).map((d) => formatStamp(d)).join(" · ")}
              </span>
            )}
          </>
        )}
        {cron !== "" && (
          <span className="wp-expr">
            {words !== "" ? `${words} · ` : ""}
            <span className="mono">{cron}</span>
          </span>
        )}
      </div>
    </div>
  );
}
