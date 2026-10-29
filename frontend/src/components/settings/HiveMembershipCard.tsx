import { useEffect, useState } from "react";
import { HexagonIcon, LogOutIcon, MonitorIcon, PlusIcon } from "lucide-react";
import { toast } from "../../lib/toasts";
import { HiveLinkStatus, JoinHive, LeaveHive, OldNodeStatus, RetireOldNode, ThisMacAbilities } from "../../../wailsjs/go/app/App";
import { useHosts } from "../../canvas/data";
import { translate } from "../../lib/i18n";
import { hiveHost, useMembership } from "../../lib/membership";
import { Avatar, Block, Btn, ConfirmBtn, Note, Row, State, Text } from "./kit";

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

/** The window follows the hive from the first call, so a join or a leave
 *  starts it over. */
const reload = () => window.setTimeout(() => window.location.reload(), 400);

/**
 * This Mac's place in the hive: a member of one (with the way out), or the
 * queen of a hive of one machine (with the way in, and a code for the next
 * machine). Rows only: the Hive group wraps them.
 */
export default function HiveMembershipCard({ onMakeCode }: { onMakeCode: () => void }) {
  const { m, recheck } = useMembership();
  const { hosts } = useHosts();
  const [address, setAddress] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [old, setOld] = useState<OldNode | null>(null);
  const [can, setCan] = useState<MacCan | null>(null);

  useEffect(() => {
    OldNodeStatus().then((o) => setOld(o as OldNode)).catch(() => setOld(null));
  }, []);
  useEffect(() => {
    ThisMacAbilities().then((c) => setCan(c as MacCan)).catch(() => setCan(null));
  }, [m?.joined, m?.connected]);
  useEffect(() => {
    if (m?.needsCode && !address) {
      // The hive this Mac was paired with before: only its code is missing.
      HiveLinkStatus().then((s) => { if (s?.url) setAddress(hiveHost(s.url)); }).catch(() => {});
    }
  }, [m?.needsCode]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!m) return null;
  const machines = hosts ? Math.max(1, hosts.filter((h) => h.state === "live").length) : 1;
  const hive = hiveHost(m.hive);

  const join = async () => {
    setBusy(true);
    setErr("");
    try {
      await JoinHive(address.trim(), code.trim());
      reload();
    } catch (e) {
      setErr(errText(e));
      setBusy(false);
    }
  };
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
              sub={translate("This Mac goes back to a hive of its own")} ok="Leave" onConfirm={leave} testId="hive-leave" />
          </Row>
          <Row lead={<Avatar><MonitorIcon size={16} strokeWidth={1.8} /></Avatar>} label="This Mac" sub={[m.name, canLine(can)].filter(Boolean).join(" · ")} testId="hive-this-mac">
            <State tone="ok">In the hive</State>
          </Row>
        </>
      ) : (
        <>
          <Row lead={<Avatar><MonitorIcon size={16} strokeWidth={1.8} /></Avatar>} label="This Mac"
            sub={[translate("Queen · {n} machines", { n: machines }), canLine(can)].filter(Boolean).join(" · ")} testId="hive-alone">
            <State tone="ok">Running</State>
          </Row>
          <Block testId="hive-join">
            <div className="st-row-t" style={{ marginBottom: 8 }}>{translate("Join a hive")}</div>
            <div className="hive-join">
              <div className="hive-join-addr">
                <Text mono value={address} onChange={setAddress} placeholder="ai.superleo.cn" label="Hive address" testId="join-address" onEnter={() => void join()} />
              </div>
              <Text mono width={110} align="right" inputMode="numeric" value={code} onChange={(v) => setCode(v.replace(/\D/g, "").slice(0, 6))}
                placeholder="Pairing code" label="Pairing code" testId="join-code" onEnter={() => void join()} />
              <Btn pri disabled={busy || !address.trim() || code.length !== 6} onClick={() => void join()} data-testid="join-go">Join</Btn>
            </div>
            {err && <Note bad testId="join-error">{err}</Note>}
          </Block>
          <Row label="Let another machine join">
            <Btn icon={PlusIcon} onClick={onMakeCode} data-testid="make-code">Make a pairing code</Btn>
          </Row>
        </>
      )}
    </>
  );
}
