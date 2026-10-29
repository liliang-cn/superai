import { useCallback, useEffect, useRef, useState } from "react";
import { GetLocalHive, GetSettings, SetLocalHive } from "../../wailsjs/go/app/App";
import { app, backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";
import { translate } from "../lib/i18n";
import { Row, State, Stepper, Toggle } from "./settings/kit";

/**
 * Workers on this machine: more SuperLeo processes the queen hands work to, so
 * one machine can do several things at once. Off until turned on, because
 * each worker is a whole process with its own memory and its own MCP servers.
 * While on, a changed count is applied a moment after the last click.
 *
 * onChanged hands back the saved settings, so the page's own copy does not
 * undo the switch the next time someone presses Save.
 */
export default function LocalHiveCard({ onChanged }: { onChanged: (s: backend.Settings) => void }) {
  const [hive, setHive] = useState<app.LocalHive | null>(null);
  const [keep, setKeep] = useState(2);
  const [most, setMost] = useState(4);
  const [busy, setBusy] = useState(false);
  const timer = useRef(0);

  const load = useCallback(async () => {
    const h = await GetLocalHive();
    setHive(h);
    return h;
  }, []);

  useEffect(() => {
    void load().then((h) => {
      setKeep(h.min_workers || 1);
      setMost(h.max_workers || 1);
    }).catch(() => {});
  }, [load]);

  // While it is on, the count follows the workers starting and joining.
  useEffect(() => {
    if (!hive?.enabled) return;
    const id = window.setInterval(() => void load().catch(() => {}), 3000);
    return () => window.clearInterval(id);
  }, [hive?.enabled, load]);

  const apply = async (enabled: boolean, k = keep, m = most) => {
    setBusy(true);
    try {
      await SetLocalHive(enabled, k, m);
      onChanged(await GetSettings());
      await load();
    } catch (e: any) {
      toast.error(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const count = (k: number, m: number) => {
    setKeep(k);
    setMost(m);
    if (!hive?.enabled) return;
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => void apply(true, k, m), 700);
  };
  useEffect(() => () => window.clearTimeout(timer.current), []);

  if (!hive) return null;
  if (!hive.available) return <Row label="Workers on this machine" sub={hive.note} dim />;

  return (
    <Row label="Workers on this machine" testId="local-hive"
      sub={hive.enabled ? translate("{live} of {running} ready", { live: hive.live, running: hive.running }) : undefined}>
      <span className="st-lab">{translate("Always running")}</span>
      <Stepper label="Always running" value={keep} min={1} max={most} disabled={busy} onChange={(v) => count(v, most)} />
      <span className="st-lab" style={{ marginLeft: 4 }}>{translate("At most")}</span>
      <Stepper label="At most" value={most} min={keep} max={16} disabled={busy} onChange={(v) => count(keep, v)} />
      {busy && <State tone="busy">{""}</State>}
      <Toggle label="Workers on this machine" on={hive.enabled} disabled={busy} onChange={(v) => void apply(v)} />
    </Row>
  );
}
