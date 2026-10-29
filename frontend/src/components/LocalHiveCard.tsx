import { useCallback, useEffect, useState } from "react";
import { GetLocalHive, GetSettings, SetLocalHive } from "../../wailsjs/go/app/App";
import { app, backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";

/**
 * Workers on this machine: more SuperLeo processes the queen hands work to, so
 * one machine can do several things at once. Off until turned on, because
 * each worker is a whole process with its own memory and its own MCP servers.
 *
 * onChanged hands back the saved settings, so the page's own copy does not
 * undo the switch the next time someone presses Save.
 */
export default function LocalHiveCard({ onChanged }: { onChanged: (s: backend.Settings) => void }) {
  const [hive, setHive] = useState<app.LocalHive | null>(null);
  const [keep, setKeep] = useState(2);
  const [most, setMost] = useState(4);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const h = await GetLocalHive();
    setHive(h);
    return h;
  }, []);

  useEffect(() => {
    void load().then((h) => {
      setKeep(h.min_workers);
      setMost(h.max_workers);
    });
  }, [load]);

  // While it is on, the count follows the workers starting and joining.
  useEffect(() => {
    if (!hive?.enabled) return;
    const id = window.setInterval(() => void load(), 3000);
    return () => window.clearInterval(id);
  }, [hive?.enabled, load]);

  const apply = async (enabled: boolean) => {
    setBusy(true);
    try {
      await SetLocalHive(enabled, keep, most);
      onChanged(await GetSettings());
      await load();
      toast.success(enabled ? "Workers on this machine are starting." : "Workers on this machine stopped.");
    } catch (e: any) {
      toast.error(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  if (!hive) return null;

  return (
    <div className="card">
      <div className="card-title">Workers on this machine</div>

      {!hive.available ? (
        <span className="hint">Not available here: {hive.note}.</span>
      ) : (
        <>
          <div className="field">
            <label>Run workers</label>
            <div className="toggle" onClick={() => !busy && apply(!hive.enabled)} style={{ cursor: busy ? "default" : "pointer", marginTop: 2 }}>
              <div className={`switch${hive.enabled ? " on" : ""}`}><div className="knob" /></div>
              <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                {hive.enabled
                  ? `On: ${hive.live} of ${hive.running} worker${hive.running === 1 ? "" : "s"} ready`
                  : "Off: SuperLeo works alone"}
              </span>
            </div>
          </div>

          <div className="row2">
            <div className="field">
              <label>Always running</label>
              <input className="input" type="number" min={1} max={most} value={keep}
                onChange={(e) => setKeep(Math.max(1, Number(e.target.value) || 1))} />
            </div>
            <div className="field">
              <label>At most</label>
              <input className="input" type="number" min={keep} max={16} value={most}
                onChange={(e) => setMost(Math.max(1, Number(e.target.value) || 1))} />
            </div>
          </div>

          {hive.enabled && (keep !== hive.min_workers || most !== hive.max_workers) && (
            <div className="row" style={{ marginTop: 8 }}>
              <button className="btn ghost sm" onClick={() => apply(true)} disabled={busy || keep > most}>
                {busy ? <><span className="spinner" /> Applying…</> : "Apply"}
              </button>
              {keep > most && <span className="hint ea-bad" style={{ marginLeft: 10 }}>Always running cannot be more than at most.</span>}
            </div>
          )}
        </>
      )}
    </div>
  );
}
