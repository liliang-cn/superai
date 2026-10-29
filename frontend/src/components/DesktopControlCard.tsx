import { useCallback, useEffect, useState } from "react";
import { DesktopStatus, SetDesktopControl } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { translate } from "../lib/i18n";

/**
 * Whether the agent may operate this machine's desktop apps (agent-desktop,
 * through the accessibility API). Applied at once like the browser's mode:
 * the backend rebuilds around it, and the status is followed until the
 * binary is in place.
 */
export default function DesktopControlCard({ onChanged }: { onChanged?: (on: boolean) => void }) {
  const [st, setSt] = useState<backend.DesktopStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try {
      const next = await DesktopStatus();
      setSt(next);
      return next;
    } catch {
      return null;
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  // Coming up: asked again until it is ready or says why not.
  useEffect(() => {
    if (!st?.enabled || st.ready || st.error) return;
    const t = window.setTimeout(() => void load(), 1500);
    return () => window.clearTimeout(t);
  }, [st, load]);

  const on = !!st?.enabled;
  const pick = async (next: boolean) => {
    if (next === on || busy) return;
    setBusy(true);
    setErr("");
    try {
      await SetDesktopControl(next);
      onChanged?.(next);
      await load();
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };

  const state = !st || !on ? ""
    : st.error ? st.error
    : !st.ready ? translate("Installing…")
    : st.accessibility && st.accessibility !== "granted" ? translate("Needs Accessibility permission")
    : `${translate("Ready")} · agent-desktop ${st.version}`;
  const bad = !!err || !!st?.error || (on && st?.ready && st.accessibility !== undefined && st.accessibility !== "" && st.accessibility !== "granted");
  return (
    <div className="card" data-testid="desktop-control-card">
      <div className="card-title">{translate("Desktop apps")}</div>
      <div className="field">
        <label>{translate("Control")}</label>
        <div className="seg" role="radiogroup" aria-label={translate("Desktop apps")} style={{ alignSelf: "flex-start" }}>
          {[false, true].map((v) => (
            <button key={String(v)} type="button" role="radio" aria-checked={on === v} data-testid={v ? "desktop-on" : "desktop-off"}
              className={`seg-btn${on === v ? " on" : ""}`} disabled={busy} onClick={() => void pick(v)}>
              {translate(v ? "On" : "Off")}
            </button>
          ))}
        </div>
        {(state || err) && <span className="hint" style={bad ? { color: "var(--danger, #c0262f)" } : undefined}>{err || state}</span>}
      </div>
      {on && !!st?.blocked?.length && (
        <div className="field">
          <label>{translate("Left alone")}</label>
          <span className="hint" data-testid="desktop-blocked">{st.blocked.join(" · ")}</span>
        </div>
      )}
    </div>
  );
}
