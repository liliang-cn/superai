import { useEffect, useState } from "react";
import { CheckIcon, CopyIcon, RefreshCwIcon, XIcon } from "lucide-react";
import { AgentJoinCode } from "../../wailsjs/go/app/App";
import { copyText } from "../lib/format";
import { translate } from "../lib/i18n";
import { Block, IconBtn, Note, Row, Toggle } from "./settings/kit";

type Join = { code: string; expiresAt: string; commands?: string[]; error?: string };

/**
 * Another machine joins this one as an agent: a pairing code, and the
 * command to run there, once per address this machine has (AgentJoinCode,
 * app_agentdoor.go). The code is made when the block is opened.
 */
export default function AddAgentCard({ queen, onQueen, open, onClose }: {
  /** Whether this SuperLeo is the queen of the machines that joined it. */
  queen?: boolean;
  /** Absent where it cannot be one (a worker). */
  onQueen?: (on: boolean) => void;
  open: boolean;
  onClose: () => void;
}) {
  const [join, setJoin] = useState<Join | null>(null);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState("");
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    if (!join?.code) return;
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, [join?.code]);

  const make = async () => {
    setBusy(true);
    try {
      setJoin((await AgentJoinCode()) as Join);
    } catch (e) {
      setJoin({ code: "", expiresAt: "", error: String(e instanceof Error ? e.message : e) });
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    if (open && !join?.code) void make();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const left = join?.code ? Math.max(0, Math.round((new Date(join.expiresAt).getTime() - now) / 1000)) : 0;
  const expired = !!join?.code && left === 0;

  return (
    <>
      {onQueen && (
        <Row label="Act as queen">
          <Toggle label="Act as queen" on={!!queen} onChange={onQueen} testId="act-as-queen" />
        </Row>
      )}
      {open && (
        <Block testId="agent-join">
          <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 8 }}>
            <span className="st-row-t" style={{ flex: 1 }}>
              {expired
                ? translate("Expired")
                : join?.code
                  ? translate("Run on the other machine · {m}:{s}", { m: Math.floor(left / 60), s: String(left % 60).padStart(2, "0") })
                  : translate("Add a machine")}
            </span>
            <IconBtn icon={RefreshCwIcon} label="New code" disabled={busy} spin={busy} onClick={() => void make()} />
            <IconBtn icon={XIcon} label="Close" onClick={onClose} />
          </div>
          {join?.error && <Note bad>{join.error}</Note>}
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            {(join?.commands ?? []).map((cmd) => (
              <div key={cmd} className={`st-cmd${expired ? " spent" : ""}`}>
                <code data-testid="agent-join-cmd">{cmd}</code>
                <IconBtn icon={copied === cmd ? CheckIcon : CopyIcon} label={copied === cmd ? "Copied" : "Copy"} disabled={expired}
                  onClick={async () => {
                    if (await copyText(cmd)) {
                      setCopied(cmd);
                      window.setTimeout(() => setCopied(""), 1500);
                    }
                  }} />
              </div>
            ))}
          </div>
        </Block>
      )}
    </>
  );
}
