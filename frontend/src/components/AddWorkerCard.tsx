import { useEffect, useState } from "react";
import { PlusIcon } from "lucide-react";
import { LocalWorkerCandidates } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { translate } from "../lib/i18n";
import { Avatar, Block, Btn, IconBtn, Note, Row, Text } from "./settings/kit";

type Candidate = { name: string; about: string; command: string[]; added: boolean };

/** A command line as argv: words split on spaces, quotes kept together. */
export function splitCommand(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote = "";
  let has = false;
  for (const ch of line) {
    if (quote) {
      if (ch === quote) quote = "";
      else cur += ch;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
      has = true;
    } else if (/\s/.test(ch)) {
      if (has || cur) out.push(cur);
      cur = "";
      has = false;
    } else {
      cur += ch;
    }
  }
  if (has || cur) out.push(cur);
  return out;
}

/**
 * Workers you add yourself: pi, hermes and openclaw found on this machine,
 * one click each, or any agent by the command that asks it one thing — here,
 * or on another machine over ssh. Each is a remote agent (host "local" for
 * this machine), saved at once. The ones already added are listed with the
 * rest of the agents, not here.
 */
export default function AddWorkerCard({ ra, onSave, open, onClose }: {
  ra: backend.RemoteAgents;
  /** Saves the settings with these agents, switched on. */
  onSave: (agents: Record<string, backend.RemoteAgent>) => Promise<void>;
  open: boolean;
  onClose: () => void;
}) {
  const [found, setFound] = useState<Candidate[]>([]);
  const [name, setName] = useState("");
  const [command, setCommand] = useState("");
  const [host, setHost] = useState("");
  const [about, setAbout] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const agents = ra.agents ?? {};
  useEffect(() => {
    LocalWorkerCandidates().then((c) => setFound((c ?? []) as Candidate[])).catch(() => setFound([]));
  }, [ra]);

  const put = async (n: string, a: backend.RemoteAgent) => {
    setBusy(true);
    setError("");
    try {
      await onSave({ ...agents, [n]: a });
      return true;
    } catch (e) {
      setError(String(e instanceof Error ? e.message : e));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const addOwn = async () => {
    const n = name.trim().replace(/^@/, "");
    let argv = splitCommand(command.trim());
    if (!/^[\w.-]+$/.test(n)) return setError(translate("A name is letters, digits, - and . only"));
    if (agents[n]) return setError(translate("That name is taken"));
    if (argv.length === 0) return setError(translate("Give the command"));
    if (!argv.includes("{prompt}")) argv = [...argv, "{prompt}"];
    const ok = await put(n, new backend.RemoteAgent({ about: about.trim(), hosts: [host.trim() || "local"], command: argv }));
    if (ok) {
      setName("");
      setCommand("");
      setHost("");
      setAbout("");
      onClose();
    }
  };

  const fresh = found.filter((c) => !c.added && !agents[c.name]);

  return (
    <>
      {fresh.map((c) => (
        <Row key={c.name} dim lead={<Avatar tone="muted">@</Avatar>} label={c.name} sub={c.about || translate("On this machine")}>
          <IconBtn icon={PlusIcon} label="Add" disabled={busy} onClick={() => void put(c.name, new backend.RemoteAgent({ about: c.about, hosts: ["local"], command: c.command }))}
            data-testid={`add-worker-${c.name}`} />
        </Row>
      ))}
      {open && (
        <Block testId="worker-form">
          <div className="st-form">
            <label>{translate("Name")}</label>
            <Text mono label="Name" placeholder="kimi" value={name} onChange={setName} testId="worker-name" />
            <label>{translate("Command")}</label>
            <Text mono label="Command" placeholder="kimi -p {prompt}" value={command} onChange={setCommand} testId="worker-command" />
            <label>{translate("Host")}</label>
            <Text mono label="Host" placeholder="local" value={host} onChange={setHost} />
            <label>{translate("What it is")}</label>
            <Text label="What it is" value={about} onChange={setAbout} onEnter={() => void addOwn()} />
            <div className="st-form-b">
              {error && <Note bad>{error}</Note>}
              <Btn onClick={onClose}>Cancel</Btn>
              <Btn pri disabled={busy || !name.trim() || !command.trim()} onClick={() => void addOwn()} data-testid="worker-add">Add</Btn>
            </div>
          </div>
        </Block>
      )}
    </>
  );
}
