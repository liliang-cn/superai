import { useCallback, useEffect, useState } from "react";
import QRCode from "qrcode";
import { PairPhone, PairedDevices, UnpairDevice } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";

/**
 * Pairing the SuperAI phone app with this server.
 *
 * A code is asked for here, by someone already signed in, and shown two ways:
 * as six digits to type, and as a QR code that also carries this page's
 * address — the phone's camera opens the app with both filled in. The phone
 * trades the code for a token of its own (internal/app/pairing.go), so each
 * phone is listed below and can be unpaired without touching anything else.
 *
 * Only in the browser: the desktop window has no address a phone could reach.
 */
export default function PairPhoneCard() {
  const [code, setCode] = useState<app.PairCode | null>(null);
  const [qr, setQr] = useState("");
  const [left, setLeft] = useState(0);
  const [devices, setDevices] = useState<app.PairedDevice[]>([]);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setDevices((await PairedDevices()) || []);
    } catch (e: any) {
      toast.error(String(e?.message || e));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // The countdown, and the list refreshed while a code is up: the phone that
  // claims it should appear here without a reload.
  useEffect(() => {
    if (!code) return;
    const tick = () => {
      const ms = new Date(code.expires_at as unknown as string).getTime() - Date.now();
      setLeft(Math.max(0, Math.ceil(ms / 1000)));
      if (ms <= 0) {
        setCode(null);
        setQr("");
      }
    };
    tick();
    const t = window.setInterval(tick, 1000);
    const poll = window.setInterval(() => void load(), 3000);
    return () => {
      window.clearInterval(t);
      window.clearInterval(poll);
    };
  }, [code, load]);

  const start = async () => {
    setBusy(true);
    try {
      const c = await PairPhone();
      const link = `superai://pair?server=${encodeURIComponent(window.location.origin)}&code=${c.code}`;
      setQr(await QRCode.toDataURL(link, { margin: 1, width: 360, errorCorrectionLevel: "M" }));
      setCode(c);
    } catch (e: any) {
      toast.error(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const unpair = async (d: app.PairedDevice) => {
    try {
      await UnpairDevice(d.id);
      toast.success(`已解除配对：${d.name}`);
      await load();
    } catch (e: any) {
      toast.error(String(e?.message || e));
    }
  };

  const when = (t: unknown) => {
    const d = new Date(t as string);
    return isNaN(d.getTime()) || d.getFullYear() < 2000 ? "—" : d.toLocaleString();
  };

  return (
    <div className="card">
      <div className="card-title">Phone</div>
      <div className="card-desc">
        Pair the SuperAI iPhone app with this server. Scan the code with the phone's camera, or
        type the address and the six digits into the app. Each phone gets its own key and can be
        unpaired here.
      </div>

      {code ? (
        <div style={{ display: "flex", gap: 20, alignItems: "center", flexWrap: "wrap", marginBottom: 14 }}>
          <img src={qr} alt="Pairing QR code" width={180} height={180} style={{ borderRadius: 8, background: "#fff" }} />
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            <span style={{ fontSize: 34, fontWeight: 600, letterSpacing: "0.18em", fontVariantNumeric: "tabular-nums" }}>
              {code.code.slice(0, 3)} {code.code.slice(3)}
            </span>
            <span className="card-desc" style={{ margin: 0 }}>{window.location.origin}</span>
            <span className="card-desc" style={{ margin: 0 }}>
              Expires in {Math.floor(left / 60)}:{String(left % 60).padStart(2, "0")}
            </span>
          </div>
        </div>
      ) : (
        <div className="field">
          <button className="btn" onClick={start} disabled={busy} style={{ alignSelf: "flex-start" }}>
            {busy ? <><span className="spinner" /> …</> : "Pair a phone"}
          </button>
        </div>
      )}

      {devices.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 6 }}>
          {devices.map((d) => (
            <div key={d.id} className="url-box" style={{ alignItems: "center" }}>
              <span style={{ flex: 1 }}>
                {d.name}
                <span className="card-desc" style={{ margin: "0 0 0 10px" }}>last seen {when(d.last_seen)}</span>
              </span>
              <button className="btn" onClick={() => void unpair(d)}>Unpair</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
