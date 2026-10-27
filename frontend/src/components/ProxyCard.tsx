import { useEffect, useState } from "react";
import { ProxyStatus, TestProxy } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { translate } from "../lib/i18n";

const MODES: { id: string; label: string }[] = [
  { id: "", label: "Environment" },
  { id: "system", label: "Follow system" },
  { id: "manual", label: "Manual" },
  { id: "off", label: "Off" },
];

type State = { mode: string; using: string; system?: string; error?: string };

/**
 * The proxy SuperAI's own requests and the CLIs it starts go through. Edits
 * the page's copy of the settings, saved with the rest; the line under it is
 * the setting in use, and Test tries the one on screen without saving it.
 */
export default function ProxyCard({ hidden, proxy, onChange }: {
  hidden: boolean;
  proxy: backend.ProxySettings;
  onChange: (p: backend.ProxySettings) => void;
}) {
  const [now, setNow] = useState<State | null>(null);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const mode = proxy.mode ?? "";
  const url = proxy.url ?? "";

  useEffect(() => {
    if (hidden) return;
    ProxyStatus().then((s: any) => setNow(s as State)).catch(() => setNow(null));
  }, [hidden]);
  useEffect(() => setTest(null), [mode, url]);

  const set = (next: Partial<backend.ProxySettings>) => onChange(new backend.ProxySettings({ mode, url, ...next }));
  const run = async () => {
    setBusy(true);
    try {
      const r: any = await TestProxy(new backend.ProxySettings({ mode, url }));
      const via = r?.state?.using ? ` · ${r.state.using}` : ` · ${translate("direct")}`;
      setTest(r?.ok ? { ok: true, text: `${r.result}${via}` } : { ok: false, text: String(r?.error ?? "failed") });
    } catch (e) {
      setTest({ ok: false, text: String(e instanceof Error ? e.message : e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card" hidden={hidden}>
      <div className="card-title">{translate("Proxy")}</div>
      <div className="field">
        <label>{translate("Mode")}</label>
        <div className="seg" role="radiogroup" aria-label={translate("Proxy")} style={{ flexWrap: "wrap", alignSelf: "flex-start" }}>
          {MODES.map((m) => (
            <button
              key={m.id || "env"}
              type="button"
              role="radio"
              aria-checked={mode === m.id}
              className={`seg-btn${mode === m.id ? " on" : ""}`}
              onClick={() => set({ mode: m.id })}
            >
              {translate(m.label)}
            </button>
          ))}
        </div>
        {now && (
          <span className="hint" data-testid="proxy-now">
            {translate("In use: {engine}", { engine: now.using || translate("direct") })}
            {now.system ? ` · ${translate("Follow system")}: ${now.system}` : ""}
            {now.error ? ` · ${now.error}` : ""}
          </span>
        )}
      </div>
      {mode === "manual" && (
        <div className="field">
          <label>{translate("Address")}</label>
          <input
            className="input"
            aria-label={translate("Address")}
            value={url}
            placeholder="http://127.0.0.1:7897"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => set({ url: e.target.value })}
          />
        </div>
      )}
      <div className="field">
        <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
          <button type="button" className="btn" onClick={run} disabled={busy}>
            {busy ? translate("Testing…") : translate("Test")}
          </button>
          {test && (
            <span className="hint" style={{ color: test.ok ? "var(--ok, #12805c)" : "var(--danger, #c0392b)" }} data-testid="proxy-test">
              {test.text}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
