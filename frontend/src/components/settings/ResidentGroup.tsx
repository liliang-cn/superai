import { useEffect, useState } from "react";
import { PowerIcon } from "lucide-react";
import { DesktopResidency, SetKeepRunning, SetStartAtLogin } from "../../../wailsjs/go/app/App";
import { Group, Row, Toggle } from "./kit";

type Resident = { startAtLogin: boolean; keepRunning: boolean; restartToApply: boolean; supported: boolean };

/** How the desktop app stays running: opened at login, and kept running with
 *  its window closed, so this Mac stays in its hive. */
export default function ResidentGroup() {
  const [r, setR] = useState<Resident | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    DesktopResidency().then((v) => setR(v as Resident)).catch(() => setR(null));
  }, []);
  if (!r || !r.supported) return null;
  const change = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      setR((await f()) as Resident);
    } catch {
      /* the toggle snaps back to what is saved */
    } finally {
      setBusy(false);
    }
  };
  return (
    <Group icon={PowerIcon} title="Background">
      <Row label="Open at login">
        <Toggle label="Open at login" on={r.startAtLogin} disabled={busy} testId="start-at-login"
          onChange={(on) => void change(() => SetStartAtLogin(on))} />
      </Row>
      <Row label="Keep running when the window is closed" sub={r.restartToApply ? "On next start" : undefined}>
        <Toggle label="Keep running when the window is closed" on={r.keepRunning} disabled={busy} testId="keep-running"
          onChange={(on) => void change(() => SetKeepRunning(on))} />
      </Row>
    </Group>
  );
}
