import { useEffect, useState } from "react";
import { LocalWorkerCandidates } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { translate } from "../lib/i18n";

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
 * this machine), saved at once.
 */
export default function AddWorkerCard({
  ra,
  onSave,
}: {
  ra: backend.RemoteAgents;
  /** Saves the settings with these agents, switched on. */
  onSave: (agents: Record<string, backend.RemoteAgent>) => Promise<void>;
}) {
  const [found, setFound] = useState<Candidate[]>([]);
  const [name, setName] = useState("");
  const [command, setCommand] = useState("");
  const [host, setHost] = useState("");
  const [about, setAbout] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const agents = ra.agents ?? {};
  const refresh = () => LocalWorkerCandidates().then((c) => setFound((c ?? []) as Candidate[])).catch(() => setFound([]));
  useEffect(() => {
    refresh();
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

  const addFound = (c: Candidate) =>
    put(c.name, new backend.RemoteAgent({ about: c.about, hosts: ["local"], command: c.command }));

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
    }
  };

  const remove = async (n: string) => {
    const rest = { ...agents };
    delete rest[n];
    setBusy(true);
    try {
      await onSave(rest);
    } finally {
      setBusy(false);
    }
  };

  const mine = Object.entries(agents).filter(([, a]) => (a.hosts ?? []).every((h) => h === "local") && (a.hosts ?? []).length > 0);

  return (
    <div className="card">
      <div className="card-title">{translate("Add a worker")}</div>

      {found.length > 0 && (
        <div className="field">
          <label>{translate("On this machine")}</label>
          <div className="ea-agents">
            {found.map((c) => (
              <div key={c.name} className="ea-agent" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span className="ea-agent-name" style={{ flex: 1 }}>@{c.name}</span>
                {c.added ? (
                  <span className="hint">{translate("Added")}</span>
                ) : (
                  <button type="button" className="btn sm" disabled={busy} onClick={() => addFound(c)} data-testid={`add-worker-${c.name}`}>
                    {translate("Add")}
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {mine.length > 0 && (
        <div className="field">
          <label>{translate("Workers here")}</label>
          <div className="ea-agents">
            {mine.map(([n, a]) => (
              <div key={n} className="ea-agent" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span className="ea-agent-name">@{n}</span>
                <code style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 11 }}>
                  {(a.command ?? []).join(" ")}
                </code>
                <button type="button" className="btn ghost sm" disabled={busy} onClick={() => remove(n)}>
                  {translate("Remove")}
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="field">
        <label>{translate("Your own")}</label>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 2fr", gap: 8 }}>
          <input className="input" placeholder={translate("Name")} value={name} onChange={(e) => setName(e.target.value)} data-testid="worker-name" />
          <input className="input" placeholder="kimi -p {prompt}" value={command} onChange={(e) => setCommand(e.target.value)} data-testid="worker-command" />
          <input className="input" placeholder={translate("Host (empty: this machine)")} value={host} onChange={(e) => setHost(e.target.value)} />
          <input className="input" placeholder={translate("What it is")} value={about} onChange={(e) => setAbout(e.target.value)} />
        </div>
        <button type="button" className="btn" style={{ alignSelf: "flex-start", marginTop: 8 }} disabled={busy || !name.trim() || !command.trim()} onClick={addOwn} data-testid="worker-add">
          {translate("Add")}
        </button>
        {error && <span className="hint" style={{ color: "var(--danger, #c0392b)" }}>{error}</span>}
      </div>
    </div>
  );
}
