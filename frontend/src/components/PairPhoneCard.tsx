import { useCallback, useEffect, useState } from "react";
import QRCode from "qrcode";
import { QrCodeIcon, SmartphoneIcon, Unlink2Icon, XIcon } from "lucide-react";
import { PairPhone, PairedDevices, UnpairDevice } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";
import { translate } from "../lib/i18n";
import { Avatar, Block, Btn, ConfirmBtn, Group, IconBtn, Row, State } from "./settings/kit";

/**
 * Pairing the SuperLeo phone app (and other SuperLeos) with this server.
 *
 * A code is asked for here, by someone already signed in, and shown two ways:
 * as six digits to type, and as a QR code that also carries this page's
 * address — the phone's camera opens the app with both filled in. The phone
 * trades the code for a token of its own (internal/app/pairing.go), so each
 * device is listed and can be unpaired without touching anything else.
 *
 * In the desktop app the link comes from the backend: the window has no
 * address a phone could reach, and the app opens one for phones, on the local
 * network and through the relay (app/phonelink.go).
 */
export default function PairPhoneCard() {
  const [code, setCode] = useState<app.PairCode | null>(null);
  const [qr, setQr] = useState("");
  const [left, setLeft] = useState(0);
  const [devices, setDevices] = useState<app.PairedDevice[]>([]);
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(Date.now());

  const load = useCallback(async () => {
    try {
      setDevices((await PairedDevices()) || []);
      setNow(Date.now());
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
      const link = c.link || `superleo://pair?server=${encodeURIComponent(window.location.origin)}&code=${c.code}`;
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
      toast.success(translate("Unpaired {name}", { name: d.name }));
      await load();
    } catch (e: any) {
      toast.error(String(e?.message || e));
    }
  };

  const seen = (t: unknown) => {
    const d = new Date(t as string);
    return isNaN(d.getTime()) || d.getFullYear() < 2000 ? null : d;
  };

  return (
    <Group icon={SmartphoneIcon} title="Devices" testId="devices"
      action={<Btn icon={QrCodeIcon} disabled={busy || !!code} onClick={() => void start()} data-testid="pair-device">Pair a device</Btn>}>
      {code && (
        <Block testId="pair-code">
          <div className="st-qr">
            <img src={qr} alt={translate("Pairing QR code")} width={150} height={150} />
            <div style={{ display: "flex", flexDirection: "column", gap: 6, flex: 1, minWidth: 0 }}>
              <span className="st-qr-code">{code.code.slice(0, 3)} {code.code.slice(3)}</span>
              {!code.link && <span className="st-note mono">{window.location.origin}</span>}
              <State tone="busy">{translate("Expires in {time}", { time: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` })}</State>
            </div>
            <IconBtn icon={XIcon} label="Close" onClick={() => { setCode(null); setQr(""); }} />
          </div>
        </Block>
      )}
      {devices.map((d) => {
        const last = seen(d.last_seen);
        const recent = !!last && now - last.getTime() < 10 * 60 * 1000;
        return (
          <Row key={d.id} lead={<Avatar><SmartphoneIcon size={15} strokeWidth={1.8} /></Avatar>} label={d.name}
            sub={last ? translate("Last seen {time}", { time: last.toLocaleString() }) : undefined}>
            {recent ? <State tone="ok">Online</State> : <State tone="off">Offline</State>}
            <ConfirmBtn icon={Unlink2Icon} label="Unpair" title={translate("Unpair {name}?", { name: d.name })} onConfirm={() => unpair(d)} testId="unpair" />
          </Row>
        );
      })}
      {!code && devices.length === 0 && <Row label="No devices" dim />}
    </Group>
  );
}
