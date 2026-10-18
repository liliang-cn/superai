import { AlarmClockIcon, BellIcon, BellOffIcon, BellRingIcon, type LucideIcon } from "lucide-react";
import { Notify, NOTIFY_MODES, notifyLabel } from "../lib/schedule";
import { useI18n } from "../lib/i18n";
import "../views/schedule.css";

export const MODE_ICONS: Record<Notify, LucideIcon> = {
  alarm: AlarmClockIcon,
  reminder: BellRingIcon,
  notification: BellIcon,
  none: BellOffIcon,
};

/** How an item reaches the person: 闹钟 / 提醒 / 通知 / 不提醒, the same mark everywhere. */
export function ModeChip({ mode, compact }: { mode: Notify; compact?: boolean }) {
  useI18n();
  const Icon = MODE_ICONS[mode];
  const label = notifyLabel(mode);
  return (
    <span className={`mode-chip m-${mode}${compact ? " compact" : ""}`} title={label}>
      <Icon size={12} strokeWidth={2} aria-hidden />
      {!compact && <span>{label}</span>}
      {compact && <span className="sr-only">{label}</span>}
    </span>
  );
}

/** The four modes as one segmented control. */
export function ModePicker({ value, onChange, disabled }: { value: Notify; onChange: (n: Notify) => void; disabled?: boolean }) {
  const { t } = useI18n();
  return (
    <div className="sch-seg modes" role="radiogroup" aria-label={t("How it reaches you")}>
      {NOTIFY_MODES.map((m) => {
        const Icon = MODE_ICONS[m];
        return (
          <button key={m} type="button" role="radio" aria-checked={value === m} disabled={disabled}
            className={`${value === m ? "on" : ""} m-${m}`} onClick={() => onChange(m)}>
            <Icon size={13} strokeWidth={2} aria-hidden />{notifyLabel(m)}
          </button>
        );
      })}
    </div>
  );
}
