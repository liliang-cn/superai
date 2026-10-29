import { useCallback, useEffect, useState } from "react";
import { XIcon } from "lucide-react";
import { DesktopRequestAccess, DesktopStatus, SetDesktopBlockedApps, SetDesktopControl } from "../../wailsjs/go/app/App";
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
  const [asking, setAsking] = useState(false);
  const [adding, setAdding] = useState("");
  const [saving, setSaving] = useState(false);
  // Turned on here: the permission is asked for once, as soon as it is up.
  const [askOnReady, setAskOnReady] = useState(false);

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
  const needsAccess = !!st?.enabled && !!st.ready && !!st.accessibility && st.accessibility !== "granted";
  // Coming up, or waiting for the person in System Settings: asked again
  // until it is ready and allowed, or says why not.
  useEffect(() => {
    if (!st?.enabled || st.error || (st.ready && !needsAccess)) return;
    const t = window.setTimeout(() => void load(), needsAccess ? 2000 : 1500);
    return () => window.clearTimeout(t);
  }, [st, load, needsAccess]);

  const ask = useCallback(async () => {
    setAsking(true);
    setErr("");
    try {
      await DesktopRequestAccess();
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setAsking(false);
      void load();
    }
  }, [load]);
  useEffect(() => {
    if (!askOnReady || !st?.ready) return;
    setAskOnReady(false);
    if (needsAccess) void ask();
  }, [askOnReady, st, needsAccess, ask]);

  const on = !!st?.enabled;
  const pick = async (next: boolean) => {
    if (next === on || busy) return;
    setBusy(true);
    setErr("");
    try {
      await SetDesktopControl(next);
      onChanged?.(next);
      if (next) setAskOnReady(true);
      await load();
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };

  // The list is saved as it changes; the backend rebuilds around it.
  const saveBlocked = async (list: string[], useDefault = false) => {
    setSaving(true);
    setErr("");
    // Shown at once: the save rebuilds the backend and takes seconds.
    if (!useDefault) setSt((cur) => (cur ? backend.DesktopStatus.createFrom({ ...cur, blocked: list, blockedIsDefault: false }) : cur));
    else setSt((cur) => (cur ? backend.DesktopStatus.createFrom({ ...cur, blockedIsDefault: true }) : cur));
    try {
      await SetDesktopBlockedApps(list, useDefault);
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      void load();
      setSaving(false);
    }
  };
  const blocked = st?.blocked ?? [];
  const add = () => {
    const name = adding.trim();
    if (!name) return;
    setAdding("");
    if (blocked.some((b) => b.toLowerCase() === name.toLowerCase())) return;
    void saveBlocked([...blocked, name]);
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
        {needsAccess && (
          <button type="button" className="btn sm" style={{ alignSelf: "flex-start" }} disabled={asking} onClick={() => void ask()} data-testid="desktop-grant">
            {translate(asking ? "Opening…" : "Grant access")}
          </button>
        )}
      </div>
      {on && st && (
        <div className="field">
          <label>{translate("Left alone")}</label>
          <div className="dc-apps" data-testid="desktop-blocked">
            {blocked.map((b) => (
              <span key={b} className="dc-app">
                {b}
                <button type="button" aria-label={`${translate("Allow")} ${b}`} title={translate("Allow")} disabled={saving}
                  onClick={() => void saveBlocked(blocked.filter((x) => x !== b))}>
                  <XIcon size={12} />
                </button>
              </span>
            ))}
            {blocked.length === 0 && <span className="hint">{translate("None")}</span>}
          </div>
          <div className="dc-add">
            <input className="input" value={adding} placeholder={translate("App name")} disabled={saving}
              onChange={(e) => setAdding(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && !e.nativeEvent.isComposing) { e.preventDefault(); add(); } }}
              data-testid="desktop-block-input" />
            <button type="button" className="btn ghost sm" disabled={saving || !adding.trim()} onClick={add}>{translate("Add")}</button>
            {!st.blockedIsDefault && (
              <button type="button" className="btn ghost sm" disabled={saving} onClick={() => void saveBlocked([], true)}>{translate("Reset to default")}</button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
