// Said across the top while SuperLeo has nothing to think with, so a first
// start that skipped the questions, or a key that stopped working, is never
// a silent bubble.

import "../canvas/canvas.css";
import { requestSetup } from "../lib/ready";
import { useI18n } from "../lib/i18n";
import type { AppStatus } from "../lib/types";

export default function SetupBanner({ status, loading }: { status: AppStatus | null; loading: boolean }) {
  const { t } = useI18n();
  if (loading || !status || status.ready) return null;
  return (
    <div className="setup-banner" role="status">
      <span className="setup-banner-dot" />
      <span>{t("SuperLeo has no model to think with yet.")}</span>
      <button className="setup-banner-go" onClick={requestSetup}>{t("Set up")}</button>
    </div>
  );
}
