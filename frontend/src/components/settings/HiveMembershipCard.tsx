import { useEffect, useState } from "react";
import { HexagonIcon, LogOutIcon, MonitorIcon } from "lucide-react";
import { toast } from "../../lib/toasts";
import { LeaveHive, OldNodeStatus, RetireOldNode, ThisMacAbilities } from "../../../wailsjs/go/app/App";
import { useHosts } from "../../canvas/data";
import { translate } from "../../lib/i18n";
import { hiveHost, useMembership } from "../../lib/membership";
import { Avatar, ConfirmBtn, Row, State } from "./kit";

type MacCan = { abilities: { name: string }[]; clis: number };

/** "Browser · Desktop apps · 10 coding tools": what this Mac's own SuperLeo can do. */
function canLine(c: MacCan | null): string {
  if (!c) return "";
  const names: Record<string, string> = { browser: "Browser", desktop: "Desktop apps", files: "Files", exec: "Commands" };
  const order = ["browser", "desktop", "files", "exec"];
  const have = order.filter((n) => c.abilities.some((a) => a.name === n)).map((n) => translate(names[n]));
  if (c.clis > 0) have.push(translate("{n} coding tools", { n: c.clis }));
  return have.join(" · ");
}

type OldNode = { present: boolean; running: boolean; retired: boolean; name?: string };

const errText = (e: unknown) => String(e instanceof Error ? e.message : e);

/** The window follows the hive from the first call, so a leave starts it
 *  over, at the join form. */
const reload = () => window.setTimeout(() => window.location.reload(), 400);

/**
 * This Mac's place in the one hive, with the way out. Joining is the window's
 * first screen (JoinGate), not a row here. Rows only: the Hive group wraps them.
 */
export default function HiveMembershipCard() {
  const { m, recheck } = useMembership();
  const { hosts } = useHosts();
  const [old, setOld] = useState<OldNode | null>(null);
  const [can, setCan] = useState<MacCan | null>(null);

  useEffect(() => {
    OldNodeStatus().then((o) => setOld(o as OldNode)).catch(() => setOld(null));
  }, []);
  useEffect(() => {
    ThisMacAbilities().then((c) => setCan(c as MacCan)).catch(() => setCan(null));
  }, [m?.joined, m?.connected]);

  if (!m) return null;
  const machines = hosts ? Math.max(1, hosts.filter((h) => h.state === "live").length) : 1;
  const hive = hiveHost(m.hive);

  const leave = async () => {
    try {
      await LeaveHive();
      reload();
    } catch (e) {
      toast.error(errText(e));
    }
  };
  const retire = async () => {
    try {
      setOld((await RetireOldNode()) as OldNode);
      void recheck();
    } catch (e) {
      toast.error(errText(e));
    }
  };

  return (
    <>
      {old?.running && (
        <Row label="The old node program is still running" testId="old-node">
          <ConfirmBtn icon={LogOutIcon} label="Stop the old node program" title="Stop the old node program?" ok="Stop" onConfirm={retire} testId="retire-old-node" />
        </Row>
      )}
      {m.joined ? (
        <>
          <Row lead={<Avatar><HexagonIcon size={16} strokeWidth={1.8} /></Avatar>} label={hive} testId="hive-joined"
            sub={translate("{n} machines online", { n: machines })}>
            <State tone={m.connected ? "ok" : m.unreachable ? "bad" : "busy"}>
              {m.connected ? "Connected" : m.unreachable ? "Unreachable" : "Connecting…"}
            </State>
            <ConfirmBtn icon={LogOutIcon} label="Leave the hive" title={translate("Leave {hive}?", { hive })}
              ok="Leave" onConfirm={leave} testId="hive-leave" />
          </Row>
          <Row lead={<Avatar><MonitorIcon size={16} strokeWidth={1.8} /></Avatar>} label="This Mac" sub={[m.name, canLine(can)].filter(Boolean).join(" · ")} testId="hive-this-mac">
            <State tone="ok">In the hive</State>
          </Row>
        </>
      ) : null}
    </>
  );
}
