import { useEffect, useRef } from "react";
import { useI18n } from "../lib/i18n";
import "./podconfirm.css";

export type PodChange = "spawn" | "retire";

/**
 * Asked before a worker pod is started or taken away. Everything else in the
 * hive goes through on YOLO; how many pods run is the person's to say each
 * time, so it gets a question of its own rather than a card in the stream.
 */
export default function PodConfirm({ change, onAnswer }: { change: PodChange | null; onAnswer: (yes: boolean) => void }) {
  const { t } = useI18n();
  const yes = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!change) return;
    yes.current?.focus();
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") onAnswer(false); };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [change, onAnswer]);
  if (!change) return null;
  const spawn = change === "spawn";
  return (
    <div className="pc-scrim" role="presentation" onClick={() => onAnswer(false)}>
      <div className="pc-box" role="alertdialog" aria-modal="true" aria-labelledby="pc-title" onClick={(e) => e.stopPropagation()} data-testid="pod-confirm">
        <h3 id="pc-title">{spawn ? t("Start a new worker pod?") : t("Take a worker pod away?")}</h3>
        <p>{spawn
          ? t("One more pod joins the hive on the cluster. It takes about half a minute to start.")
          : t("The idlest worker leaves the hive and its pod is deleted. Work it is doing now is handed back.")}</p>
        <div className="pc-acts">
          <button type="button" className="pc-no" onClick={() => onAnswer(false)}>{t("Cancel")}</button>
          <button type="button" ref={yes} className={spawn ? "pc-yes" : "pc-yes danger"} onClick={() => onAnswer(true)}>
            {spawn ? t("Start a pod") : t("Delete a pod")}
          </button>
        </div>
      </div>
    </div>
  );
}
