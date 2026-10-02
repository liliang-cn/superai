import { useCallback, useEffect, useState } from "react";
import { GetSettings, LinkSuperAI, LinkedSuperAIs, UnlinkSuperAI } from "../../wailsjs/go/app/App";
import { app, backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";

/**
 * Other SuperAIs this one drives: their Claude Code and Codex become
 * "@claude.<name>". Linking is pairing — the other side shows six digits
 * under Pair a device, this side claims a key of its own with them.
 *
 * onChanged hands back the saved settings, so the page's own copy does not
 * overwrite the new link the next time someone presses Save.
 */
export default function LinkSuperAICard({ onChanged }: { onChanged: (s: backend.Settings) => void }) {
  const [linked, setLinked] = useState<app.LinkedSuperAI[]>([]);
  const [address, setAddress] = useState("");
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLinked((await LinkedSuperAIs()) || []);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  const link = async () => {
    setBusy(true);
    try {
      const n = await LinkSuperAI(address, code, name);
      toast.success(`Linked ${n}. Its agents are @claude.${n}, @codex.${n}.`);
      setAddress("");
      setCode("");
      setName("");
      onChanged(await GetSettings());
      await load();
    } catch (e: any) {
      toast.error(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const unlink = async (n: string) => {
    try {
      await UnlinkSuperAI(n);
      onChanged(await GetSettings());
      await load();
    } catch (e: any) {
      toast.error(String(e?.message || e));
    }
  };

  const ready = address.trim() !== "" && code.replace(/\D/g, "").length === 6;

  return (
    <div className="card">
      <div className="card-title">Other SuperAIs</div>
      <div className="card-desc">
        Use Claude Code or Codex on another machine. On that machine, open Settings › Runtime › Pair a
        device; enter its address and the six digits here.
      </div>
      <div className="link-form">
        <input className="input" placeholder="Address, e.g. 192.168.1.20:43117" value={address} onChange={(e) => setAddress(e.target.value)} />
        <input
          className="input link-code"
          placeholder="Code"
          inputMode="numeric"
          value={code}
          onChange={(e) => setCode(e.target.value)}
        />
        <input className="input link-name" placeholder="Name (optional)" value={name} onChange={(e) => setName(e.target.value)} />
        <button className="btn" onClick={link} disabled={!ready || busy}>
          {busy ? <><span className="spinner" /> Linking…</> : "Link"}
        </button>
      </div>
      {linked.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 12 }}>
          {linked.map((l) => (
            <div key={l.name} className="url-box" style={{ alignItems: "center" }}>
              <span className={`status-dot ${l.reachable ? "ok" : "bad"}`} style={{ marginRight: 8 }} />
              <span style={{ flex: 1, minWidth: 0 }}>
                <b>{l.name}</b>
                <span className="card-desc" style={{ margin: "0 0 0 10px" }}>
                  {l.url}
                  {" · "}
                  {!l.reachable
                    ? "not answering"
                    : (l.clis ?? []).length > 0
                      ? (l.clis ?? []).map((c) => `@${c}.${l.name}`).join("  ")
                      : "no agent CLI there, or External agents is off"}
                </span>
              </span>
              <button className="btn" onClick={() => void unlink(l.name)}>Unlink</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
