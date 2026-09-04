import React, { useEffect, useState } from "react";
import { AtSignIcon, CircleSlashIcon, RadioIcon, ServerIcon } from "lucide-react";
import { RemoteAgentNames } from "../../wailsjs/go/main/App";
import { AgentInfo } from "../lib/useAgentMentions";

/**
 * Who else you can ask, in the rail.
 *
 * The @ menu in the composer is for someone who already knows the name they
 * want; this is for finding out there is anyone to name at all. A feature whose
 * only entrance is a character you have to know to type is a feature most
 * people never learn exists.
 *
 * Clicking one puts the name in the composer rather than sending anything. The
 * choice of who to ask and the choice of what to ask them are two decisions,
 * and a panel that made both at once would be a send button disguised as a
 * list.
 */
export function AgentsBody({ onMention }: { onMention?: (name: string) => void }) {
  const [agents, setAgents] = useState<AgentInfo[] | null>(null);

  useEffect(() => {
    let alive = true;
    RemoteAgentNames()
      .then((list) => {
        if (!alive) return;
        setAgents(
          (list ?? []).map((a) => ({ name: a.name ?? "", about: a.about ?? "" })).filter((a) => a.name),
        );
      })
      .catch(() => alive && setAgents([]));
    return () => {
      alive = false;
    };
  }, []);

  if (agents === null) {
    return <div className="agents-panel"><div className="agents-empty">looking…</div></div>;
  }

  if (agents.length === 0) {
    return (
      <div className="agents-panel">
        <div className="agents-empty">
          <CircleSlashIcon className="size-4" />
          <p>No other agents are reachable.</p>
          {/* Named exactly, because the two switches are in different places
              and turning on the wrong one looks like the feature is broken. */}
          <p className="agents-dim">
            Turn on <b>external_agents</b> for the CLIs on this machine, or{" "}
            <b>remote_agents</b> for the ones on the cluster, in
            <code> ~/.superai-desktop/settings.json</code>.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="agents-panel">
      <div className="agents-lede">
        Ask one directly by starting a message with its name. Mention it
        mid-sentence instead and SuperAI decides whether to call it.
      </div>
      {agents.map((a) => {
        // The about line already begins with the name and a dash; showing it
        // again under a heading that is the name would read as a stutter.
        const about = a.about.replace(new RegExp(`^${a.name}\\s*—\\s*`), "");
        // "this machine" is the only phrase the backend uses for a local CLI,
        // so it is the honest way to tell the two apart without a second call.
        const local = /installed on this machine/.test(a.about);
        return (
          <button
            key={a.name}
            type="button"
            className="agents-row"
            onClick={() => onMention?.(a.name)}
            title={`Put @${a.name} in the message box`}
          >
            <span className="agents-head">
              {local ? <ServerIcon className="size-3.5" /> : <RadioIcon className="size-3.5" />}
              <span className="agents-name">@{a.name}</span>
              <span className="agents-where">{local ? "this machine" : "cluster"}</span>
            </span>
            <span className="agents-about">{about}</span>
          </button>
        );
      })}
      <div className="agents-foot">
        <AtSignIcon className="size-3" />
        typing @ in the message box opens the same list
      </div>
    </div>
  );
}

export default AgentsBody;
