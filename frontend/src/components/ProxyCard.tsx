import { useEffect, useState } from "react";
import { PlugZapIcon } from "lucide-react";
import { ProxyStatus, TestProxy } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { translate } from "../lib/i18n";
import { IconBtn, Row, Seg, Text } from "./settings/kit";

const MODES = [
  { value: "", label: "Environment" },
  { value: "system", label: "Follow system" },
  { value: "manual", label: "Manual" },
  { value: "off", label: "Off" },
];

type Now = { mode: string; using: string; system?: string; error?: string };

/**
 * The proxy SuperLeo's own requests and the CLIs it starts go through: the
 * mode, and in manual mode the address (http, https or socks5). Edits the
 * page's copy of the settings; the page's Save applies it at once (the
 * backend sets the proxy as it saves). The line under it is the proxy in use
 * and the last test, which tries the one on screen without saving it.
 */
export default function ProxyCard({ proxy, saved, onChange }: {
  proxy: backend.ProxySettings;
  /** The proxy as last saved: "in use" is asked again when it changes. */
  saved?: unknown;
  onChange: (p: backend.ProxySettings) => void;
}) {
  const [now, setNow] = useState<Now | null>(null);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const mode = proxy.mode ?? "";
  const url = proxy.url ?? "";

  // Asked again whenever the saved proxy changes, so "in use" follows Save.
  useEffect(() => {
    ProxyStatus().then((s: any) => setNow(s as Now)).catch(() => setNow(null));
  }, [JSON.stringify(saved ?? null)]);
  useEffect(() => setTest(null), [mode, url]);

  const set = (next: Partial<backend.ProxySettings>) => onChange(new backend.ProxySettings({ mode, url, ...next }));
  const run = async () => {
    setBusy(true);
    setTest(null);
    try {
      const r: any = await TestProxy(new backend.ProxySettings({ mode, url }));
      const via = r?.state?.using || translate("direct");
      setTest(r?.ok ? { ok: true, text: `${translate("Passed")} ${r.result} · ${via}` } : { ok: false, text: String(r?.error ?? "failed") });
    } catch (e) {
      setTest({ ok: false, text: String(e instanceof Error ? e.message : e) });
    } finally {
      setBusy(false);
    }
  };

  const using = now ? `${translate("In use: {engine}", { engine: now.using || translate("direct") })}${now.error ? ` · ${now.error}` : ""}` : "";
  const line = (
    <span data-testid="proxy-now">
      {using}
      {busy && <> · {translate("Testing…")}</>}
      {test && <> · <span className={`st-note ${test.ok ? "ok" : "bad"}`} style={{ display: "inline" }} data-testid="proxy-test">{test.text}</span></>}
    </span>
  );
  const tester = <IconBtn icon={PlugZapIcon} label="Test the proxy" disabled={busy || (mode === "manual" && !url.trim())} onClick={() => void run()} data-testid="proxy-test-btn" />;
  const manual = mode === "manual";

  return (
    <>
      <Row label="Proxy" sub={manual ? undefined : line}>
        <Seg label="Proxy" value={mode} options={MODES} onChange={(m) => set({ mode: m })} testId="proxy-mode" />
        {!manual && tester}
      </Row>
      {manual && (
        <Row label="Proxy address" sub={line}>
          <Text mono label="Proxy address" value={url} placeholder="http://127.0.0.1:7890" onChange={(v) => set({ url: v })} testId="proxy-url" />
          {tester}
        </Row>
      )}
    </>
  );
}
