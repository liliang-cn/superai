import { useState } from "react";
import { translate } from "../lib/i18n";
import { setBrowserMode, useBrowserStatus, useBrowserSwitching } from "../lib/browser";

const MODES = [
  { id: "off", label: "Off" },
  { id: "chrome", label: "My Chrome" },
  { id: "own", label: "Separate browser" },
] as const;

/**
 * Which browser the agent drives: none, the person's own Chrome (with their
 * sign-ins), or a separate one agent-browser launches. Applied at once, not
 * with the page's Save: the backend rebuilds around it, and the status line
 * follows it until the browser is up.
 */
export default function BrowserModeCard({ onChanged }: { onChanged?: (mode: string) => void }) {
  const st = useBrowserStatus();
  const switching = useBrowserSwitching();
  const [err, setErr] = useState("");
  const current = switching || (st?.enabled ? st.mode : "off");
  const pick = async (mode: "off" | "chrome" | "own") => {
    if (mode === current) return;
    setErr("");
    try {
      await setBrowserMode(mode);
      onChanged?.(mode);
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    }
  };
  const state = !st ? "" : switching || (st.enabled && !st.ready && !st.error) ? translate("Installing browser…")
    : st.error ? st.error : st.enabled && st.ready ? `${translate("Ready")}${st.version ? ` · agent-browser ${st.version}` : ""}` : "";
  return (
    <div className="card" data-testid="browser-mode-card">
      <div className="card-title">{translate("Browser")}</div>
      <div className="field">
        <label>{translate("Mode")}</label>
        <div className="seg" role="radiogroup" aria-label={translate("Browser")} style={{ flexWrap: "wrap", alignSelf: "flex-start" }}>
          {MODES.map((m) => (
            <button key={m.id} type="button" role="radio" aria-checked={current === m.id}
              className={`seg-btn${current === m.id ? " on" : ""}`} disabled={!!switching} onClick={() => void pick(m.id)}>
              {translate(m.label)}
            </button>
          ))}
        </div>
        {(state || err) && <span className="hint" style={st?.error || err ? { color: "var(--danger, #c0262f)" } : undefined}>{err || state}</span>}
      </div>
    </div>
  );
}
