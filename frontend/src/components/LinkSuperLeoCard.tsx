import { useCallback, useEffect, useState } from "react";
import { MonitorIcon, Unlink2Icon } from "lucide-react";
import { GetSettings, LinkSuperLeo, LinkedSuperLeos, UnlinkSuperLeo } from "../../wailsjs/go/app/App";
import { app, backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";
import { translate } from "../lib/i18n";
import { Avatar, Block, Btn, ConfirmBtn, Row, State, Text } from "./settings/kit";

/**
 * Other SuperLeos this one drives: their Claude Code and Codex become
 * "@claude.<name>". Linking is pairing — the other side shows six digits
 * under Pair a device, this side claims a key of its own with them.
 *
 * onChanged hands back the saved settings, so the page's own copy does not
 * overwrite the new link the next time someone presses Save.
 */
export default function LinkSuperLeoCard({ onChanged, open, onClose }: {
  onChanged: (s: backend.Settings) => void;
  open: boolean;
  onClose: () => void;
}) {
  const [linked, setLinked] = useState<app.LinkedSuperLeo[]>([]);
  const [address, setAddress] = useState("");
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLinked((await LinkedSuperLeos().catch(() => [])) || []);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  const link = async () => {
    setBusy(true);
    try {
      const n = await LinkSuperLeo(address, code, name);
      toast.success(translate("Linked {name}", { name: n }));
      setAddress("");
      setCode("");
      setName("");
      onChanged(await GetSettings());
      await load();
      onClose();
    } catch (e: any) {
      toast.error(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  const unlink = async (n: string) => {
    try {
      await UnlinkSuperLeo(n);
      onChanged(await GetSettings());
      await load();
    } catch (e: any) {
      toast.error(String(e?.message || e));
    }
  };

  const ready = address.trim() !== "" && code.replace(/\D/g, "").length === 6;

  return (
    <>
      {linked.map((l) => (
        <Row key={l.name} lead={<Avatar><MonitorIcon size={15} strokeWidth={1.8} /></Avatar>}
          label={`${l.name} · SuperLeo`}
          sub={<span className="mono">{l.url}{l.reachable && (l.clis ?? []).length > 0 ? ` · ${(l.clis ?? []).map((c) => `@${c}.${l.name}`).join(" ")}` : ""}</span>}>
          {l.reachable ? <State tone="ok">Online</State> : <State tone="bad">Not answering</State>}
          <ConfirmBtn icon={Unlink2Icon} label="Unlink" title={translate("Unlink {name}?", { name: l.name })} onConfirm={() => unlink(l.name)} />
        </Row>
      ))}
      {open && (
        <Block testId="link-form">
          <div className="st-form">
            <label>{translate("Address")}</label>
            <Text mono label="Address" placeholder="192.168.1.20:43117" value={address} onChange={setAddress} />
            <label>{translate("Code")}</label>
            <Text mono label="Code" placeholder="000000" inputMode="numeric" value={code} onChange={setCode} />
            <label>{translate("Name")}</label>
            <Text label="Name" placeholder="Optional" value={name} onChange={setName} onEnter={() => ready && void link()} />
            <div className="st-form-b">
              <Btn onClick={onClose}>Cancel</Btn>
              <Btn pri disabled={!ready || busy} onClick={() => void link()}>{busy ? "Linking…" : "Link"}</Btn>
            </div>
          </div>
        </Block>
      )}
    </>
  );
}
