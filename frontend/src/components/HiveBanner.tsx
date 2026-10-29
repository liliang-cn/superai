import { useEffect } from "react";
import { TriangleAlertIcon } from "lucide-react";
import { useI18n } from "../lib/i18n";
import { isCutOff, useMembership, useRetryCountdown } from "../lib/membership";

/** True while this Mac is in a hive that does not answer: history stays
 *  readable, nothing can be sent. */
export function useHiveCutOff() {
  const { m } = useMembership();
  return isCutOff(m);
}

/** The line over a conversation when the hive cannot be reached. */
export default function HiveBanner({ className }: { className?: string }) {
  const { t } = useI18n();
  const { m, recheck } = useMembership();
  const off = isCutOff(m);
  const { left, reset } = useRetryCountdown(off, recheck);
  useEffect(() => { if (!off) reset(); }, [off]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!off) return null;
  return (
    <div className={`hive-banner${className ? ` ${className}` : ""}`} role="alert" data-testid="hive-banner">
      <TriangleAlertIcon size={16} strokeWidth={1.9} aria-hidden />
      <span>{t("The hive is out of reach · retrying in {n}s", { n: left })}</span>
      <button type="button" onClick={() => { reset(); void recheck(); }} data-testid="hive-retry">{t("Retry")}</button>
    </div>
  );
}
