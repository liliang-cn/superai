// The desktop window's first question: which hive is this Mac a window onto.
//
// Pairing is the phone's: the queen shows a code (Settings → Pair a device on
// her web page), this window trades it for a token once, and from then on
// opens straight onto the hive. "On its own" keeps the old behaviour — the
// engine inside this app, no hive — for a Mac that is not near one.

import { useEffect, useRef, useState } from "react";
import Sky from "../canvas/Sky";
import "../canvas/canvas.css";
import { loadTheme, themeVars } from "../canvas/theme";
import { LinkHive as Link } from "../../wailsjs/go/app/App";

const IDLE = [
  { key: "a", busy: 0.2, rgb: [242, 165, 22] as [number, number, number] },
  { key: "b", busy: 0.15, rgb: [255, 120, 90] as [number, number, number] },
  { key: "c", busy: 0.15, rgb: [70, 180, 170] as [number, number, number] },
  { key: "d", busy: 0.15, rgb: [60, 170, 100] as [number, number, number] },
];

/** The pairing form. First run: with a way to stay on this Mac. From the
 *  backend switcher: with a way back. */
export default function LinkHive({ onLinked, onAlone, onCancel }: { onLinked: () => void; onAlone?: () => void; onCancel?: () => void }) {
  const [address, setAddress] = useState("");
  const [code, setCode] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const theme = loadTheme();

  useEffect(() => field.current?.focus(), []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!address.trim() || !code.trim() || busy) return;
    setBusy(true);
    setErr("");
    try {
      await Link(address, code);
      onLinked();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className={onCancel ? "cv-root gate2 gate2-over" : "cv-root gate2"} style={themeVars(theme)}>
      <Sky at={new Date()} lanes={IDLE} base={theme.base} mode={theme.mode} />
      <form className="cv-glass gate2-card" onSubmit={submit}>
        <h1>{onCancel ? "Add a hive" : "Link this Mac to your hive"}</h1>
        <p>On your hive's web page, open Settings and pair a device. Enter its address and the code it shows.</p>
        <div className="gate2-field">
          <input ref={field} value={address} placeholder="Address, like ai.example.com" autoCapitalize="off" spellCheck={false}
            onChange={(e) => setAddress(e.target.value)} />
        </div>
        <div className="gate2-field">
          <input value={code} placeholder="Pairing code" inputMode="numeric" autoComplete="one-time-code"
            onChange={(e) => setCode(e.target.value)} />
        </div>
        <button className="cv-pill ink gate2-go" type="submit" disabled={busy || !address.trim() || !code.trim()}>
          {busy ? "Linking…" : "Link"}
        </button>
        {err && <div className="gate2-err">{err}</div>}
        {onCancel ? (
          <button type="button" className="gate2-alt" onClick={onCancel}>Cancel</button>
        ) : onAlone && (
          <button type="button" className="gate2-alt" onClick={onAlone}>Use this Mac on its own</button>
        )}
      </form>
    </div>
  );
}
