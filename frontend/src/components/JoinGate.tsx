// The desktop window before it is in the hive. There is one hive and the
// window is a client of it, so there is nothing to show until it has joined:
// the hive's address and a pairing code, and nothing else.

import { useEffect, useRef, useState } from "react";
import Sky from "../canvas/Sky";
import "../canvas/canvas.css";
import { loadTheme, themeVars } from "../canvas/theme";
import { HiveLinkStatus, JoinHive } from "../../wailsjs/go/app/App";
import { hiveHost } from "../lib/membership";
import { translate } from "../lib/i18n";

const IDLE = [
  { key: "a", busy: 0.2, rgb: [242, 165, 22] as [number, number, number] },
  { key: "b", busy: 0.15, rgb: [255, 120, 90] as [number, number, number] },
  { key: "c", busy: 0.15, rgb: [70, 180, 170] as [number, number, number] },
  { key: "d", busy: 0.15, rgb: [60, 170, 100] as [number, number, number] },
];

export default function JoinGate() {
  const [address, setAddress] = useState("ai.superleo.cn");
  const [code, setCode] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const theme = loadTheme();

  useEffect(() => {
    // The hive this Mac was paired with the old way: only its code is missing.
    HiveLinkStatus().then((s) => { if (s?.url) setAddress(hiveHost(s.url)); }).catch(() => {});
    field.current?.focus();
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (busy || !address.trim() || code.length !== 6) return;
    setBusy(true);
    setErr("");
    try {
      await JoinHive(address.trim(), code);
      window.location.reload();
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
      setBusy(false);
    }
  };

  return (
    <div className="cv-root gate2" style={themeVars(theme)} data-testid="join-gate">
      <Sky at={new Date()} lanes={IDLE} base={theme.base} mode={theme.mode} />
      <form className="cv-glass gate2-card" onSubmit={submit}>
        <h1>SuperLeo</h1>
        <div className="gate2-field">
          <input value={address} spellCheck={false} placeholder={translate("Hive address")}
            aria-label={translate("Hive address")} data-testid="join-address"
            onChange={(e) => setAddress(e.target.value)} />
        </div>
        <div className="gate2-field">
          <input ref={field} value={code} inputMode="numeric" autoComplete="one-time-code"
            placeholder={translate("Pairing code")} aria-label={translate("Pairing code")} data-testid="join-code"
            onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))} />
        </div>
        <button className="cv-pill ink gate2-go" type="submit" disabled={busy || !address.trim() || code.length !== 6} data-testid="join-go">
          {busy ? translate("Joining…") : translate("Join")}
        </button>
        {err && <div className="gate2-err" data-testid="join-error">{err}</div>}
      </form>
    </div>
  );
}
