import { useEffect, useState } from "react";
import { AgentJoinCode } from "../../wailsjs/go/app/App";
import { copyText } from "../lib/format";
import { translate } from "../lib/i18n";

type Join = { code: string; expiresAt: string; commands?: string[]; error?: string };

/**
 * Another machine joins this one as an agent: a pairing code, and the
 * command to run there, once per address this machine has (AgentJoinCode,
 * app_agentdoor.go).
 */
export default function AddAgentCard({ queen, onQueen }: {
  /** Whether this SuperLeo is the queen of the machines that joined it. */
  queen?: boolean;
  /** Absent where it cannot be one (a worker). */
  onQueen?: (on: boolean) => void;
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
  const left = join?.code ? Math.max(0, Math.round((new Date(join.expiresAt).getTime() - now) / 1000)) : 0;
  const expired = !!join?.code && left === 0;

  return (
    <div className="card">
      <div className="card-title">{translate("Add an agent")}</div>
      {onQueen && (
        <div className="field">
          <label>{translate("Act as queen")}</label>
          <div className="toggle" onClick={() => onQueen(!queen)} style={{ cursor: "pointer", marginTop: 2 }} data-testid="act-as-queen">
            <div className={`switch${queen ? " on" : ""}`}><div className="knob" /></div>
          </div>
        </div>
      )}
      <div className="field">
        <button type="button" className="btn" onClick={make} disabled={busy} style={{ alignSelf: "flex-start" }}>
          {join?.code ? translate("New code") : translate("Make a code")}
        </button>
        {join?.error && <span className="hint" style={{ color: "var(--danger, #c0392b)" }}>{join.error}</span>}
      </div>
      {join?.code && (
        <div className="field">
          <label>
            {expired
              ? translate("Expired")
              : translate("Run on the other machine · {m}:{s}", { m: Math.floor(left / 60), s: String(left % 60).padStart(2, "0") })}
          </label>
          {(join.commands ?? []).map((cmd) => (
            <div key={cmd} style={{ display: "flex", gap: 8, alignItems: "center", opacity: expired ? 0.45 : 1 }}>
              <code className="input" style={{ flex: 1, minWidth: 0, overflowX: "auto", whiteSpace: "nowrap", fontFamily: "var(--font-mono)", fontSize: 12 }} data-testid="agent-join-cmd">
                {cmd}
              </code>
              <button
                type="button"
                className="btn ghost sm"
                disabled={expired}
                onClick={async () => {
                  if (await copyText(cmd)) {
                    setCopied(cmd);
                    window.setTimeout(() => setCopied(""), 1500);
                  }
                }}
              >
                {copied === cmd ? translate("Copied") : translate("Copy")}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
