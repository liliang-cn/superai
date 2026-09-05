import React from "react";
import { AtSignIcon, SendHorizontalIcon } from "lucide-react";
import { AgentInfo } from "../lib/useAgentMentions";

/**
 * The @ menu, and the line that says what the @ is about to do.
 *
 * Two pieces because they answer two different questions. The menu answers
 * "who can I ask"; the banner answers "where is this message going", which is
 * the one worth being told before pressing Enter rather than after — a message
 * addressed to another agent never reaches SuperAI at all, and finding that
 * out from the reply is finding out too late.
 */

export function AgentMenu({
  matches,
  active,
  onPick,
}: {
  matches: AgentInfo[];
  active: number;
  onPick: (a: AgentInfo) => void;
}) {
  if (matches.length === 0) return null;
  return (
    <div className="agent-menu" role="listbox" aria-label="Agents">
      {matches.map((a, i) => (
        <button
          key={a.name}
          type="button"
          role="option"
          aria-selected={i === active}
          className={`agent-opt${i === active ? " active" : ""}`}
          // Mouse down rather than click: the textarea loses focus on blur and
          // a click that fires after it would insert into a field nobody is in.
          onMouseDown={(e) => {
            e.preventDefault();
            onPick(a);
          }}
        >
          <span className="agent-name">@{a.name}</span>
          <span className="agent-about">{a.about}</span>
        </button>
      ))}
      <div className="agent-hint">
        <AtSignIcon className="size-3" />
        at the start sends the whole message there · anywhere else SuperAI decides
      </div>
    </div>
  );
}

/**
 * Shown when the message would be routed rather than answered here.
 *
 * It used to say SuperAI would never see the exchange, which was true and is
 * not any more: the question and the answer are filed in this conversation, so
 * a later turn can refer to what the other agent said. What is still true — and
 * the only thing worth warning about before Enter — is that this turn skips
 * SuperAI's own model entirely.
 */
export function AddressedBanner({ agent }: { agent: string }) {
  if (!agent) return null;
  return (
    <div className="agent-addressed">
      <SendHorizontalIcon className="size-3.5" />
      <span>
        going straight to <b>@{agent}</b> — SuperAI does not answer this one, but keeps it
      </span>
    </div>
  );
}
