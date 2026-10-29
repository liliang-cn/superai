import { useCallback, useEffect, useState } from "react";
import { RotateCcwIcon } from "lucide-react";
import { DesktopRequestAccess, DesktopStatus, SetDesktopBlockedApps, SetDesktopControl } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { translate } from "../lib/i18n";
import { Btn, Chips, IconBtn, Row, State, Toggle } from "./settings/kit";

/**
 * Whether the agent may operate this machine's desktop apps (agent-desktop,
 * through the accessibility API), and the apps it leaves alone. Applied at
 * once like the browser's mode: the backend rebuilds around it, and the
 * status is followed until the binary is in place.
 */
export default function DesktopControlCard({ onChanged, onBlockedChanged }: {
  onChanged?: (on: boolean) => void;
  /** The list as saved, null for the default: the page's own copy of the
   *  settings must hear it, or its next save writes the old list back. */
  onBlockedChanged?: (list: string[] | null) => void;
}) {
  const [st, setSt] = useState<backend.DesktopStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [asking, setAsking] = useState(false);
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
      onBlockedChanged?.(useDefault ? null : list);
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      void load();
      setSaving(false);
    }
  };
  const blocked = st?.blocked ?? [];
  const add = (name: string) => {
    if (blocked.some((b) => b.toLowerCase() === name.toLowerCase())) return;
    void saveBlocked([...blocked, name]);
  };

  const state = !st || !on ? null
    : st.error ? <State tone="bad" title={st.error}>Failed to start</State>
    : !st.ready ? <State tone="busy">Installing…</State>
    : needsAccess ? <State tone="bad">Needs Accessibility permission</State>
    : <State tone="ok">Allowed</State>;
  const sub = err || st?.error || (on && st?.ready && st.version ? `agent-desktop ${st.version}` : "");
  return (
    <>
      <Row label="Desktop apps" testId="desktop-control-card"
        sub={sub ? <span className={err || st?.error ? "st-note bad" : undefined}>{sub}</span> : undefined}>
        {state}
        {needsAccess && (
          <Btn disabled={asking} onClick={() => void ask()} data-testid="desktop-grant">{asking ? "Opening…" : "Grant access"}</Btn>
        )}
        <Toggle label="Desktop apps" on={on} disabled={busy || !st} onChange={(v) => void pick(v)} testId="desktop-switch" />
      </Row>
      {on && st && (
        <Row stack label={
          <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <span style={{ flex: 1 }}>{translate("Left alone")}</span>
            {!st.blockedIsDefault && <IconBtn icon={RotateCcwIcon} label="Reset to default" disabled={saving} onClick={() => void saveBlocked([], true)} />}
          </span>
        }>
          <Chips testId="desktop-blocked" items={blocked} removeLabel="Remove" addLabel="Add an app" placeholder="App name"
            disabled={saving} max={12} onRemove={(b) => void saveBlocked(blocked.filter((x) => x !== b))} onAdd={add} />
        </Row>
      )}
    </>
  );
}
