import React, { useEffect, useState } from "react";
import { ShieldAlertIcon } from "lucide-react";
import { ToolApproval } from "../lib/useToolApprovals";
import { StartYoloMode } from "../../wailsjs/go/main/App";

// How long "Allow all" lasts. Deliberately a fixed, visible, short number
// rather than a picker: the decision worth making here is "stop asking while I
// watch this run", and offering to choose the length invites choosing a long
// one. Settings has the permanent switch for anyone who means that instead.
const YOLO_MINUTES = 15;

/**
 * Asking before the agent runs a shell command.
 *
 * Modal, unlike the scheduled-run toasts, and the difference is not a style
 * choice: a finished run is news, this is a question the turn is stopped on.
 * Nothing else the user could do with the app right now matters more than
 * answering it, and a card in the corner would be dismissed by reflex.
 *
 * The command is shown verbatim, in full, in a monospace block with the tool
 * name above it — no truncation, no summary, no "…". A person cannot approve
 * what they cannot see, and a shortened command is worse than none because it
 * still invites a yes.
 */

/** Seconds until the gate stops waiting and denies on its own. */
function useCountdown(expiresAt: string): number {
  const [left, setLeft] = useState(() => secondsLeft(expiresAt));
  useEffect(() => {
    setLeft(secondsLeft(expiresAt));
    const t = window.setInterval(() => setLeft(secondsLeft(expiresAt)), 1000);
    return () => window.clearInterval(t);
  }, [expiresAt]);
  return left;
}

function secondsLeft(expiresAt: string): number {
  const at = Date.parse(expiresAt);
  if (Number.isNaN(at)) return -1;
  return Math.max(0, Math.round((at - Date.now()) / 1000));
}

/** The arguments of a non-shell tool, so the prompt is never a bare name. */
function ArgsBlock({ args }: { args: Record<string, unknown> }) {
  const keys = Object.keys(args || {});
  if (keys.length === 0) return null;
  return (
    <pre className="approval-cmd">{JSON.stringify(args, null, 2)}</pre>
  );
}

function ApprovalCard({
  req,
  onResolve,
}: {
  req: ToolApproval;
  onResolve: (id: string, allow: boolean) => void;
}) {
  const left = useCountdown(req.expiresAt);
  return (
    <div className="modal-overlay approval-overlay">
      <div className="modal approval-modal">
        <div className="modal-head">
          <span className="modal-title">
            <ShieldAlertIcon className="size-4 approval-icon" /> SuperAI wants to run{" "}
            <b>{req.tool}</b>
          </span>
          {left >= 0 && (
            <span className="run-meta">
              {left > 0 ? `denied automatically in ${left}s` : "no longer waiting"}
            </span>
          )}
        </div>
        <div className="modal-body">
          <div className="approval-desc">
            {req.command
              ? "This runs as a shell command on your machine, with your permissions. It is not confined to the agent workspace."
              : "This tool changes something outside the agent's workspace, or cannot be undone."}
          </div>
          {req.command ? (
            <pre className="approval-cmd">{req.command}</pre>
          ) : (
            <ArgsBlock args={req.args} />
          )}
          {req.session !== "" && (
            <div className="run-meta">conversation {req.session}</div>
          )}
        </div>
        <div className="approval-actions">
          {/* Deny is the plain button and comes first: the safe answer should
              be the easy one to hit, and the one a stray Enter lands on. */}
          <button className="btn" onClick={() => onResolve(req.id, false)} autoFocus>
            Deny
          </button>
          <button className="btn ghost approval-allow" onClick={() => onResolve(req.id, true)}>
            Allow once
          </button>
          {/* The pressure valve. It belongs here rather than in Settings
              because here is where it is wanted: this is the prompt someone is
              about to click through for the twentieth time in one run, and if
              the only way to stop that is a permanent switch buried elsewhere,
              the permanent switch is what they will flip. The window is short
              and shown on the button, so choosing it is choosing an end. */}
          <button
            className="btn ghost approval-yolo"
            title={"Approve everything for " + YOLO_MINUTES + " minutes, then go back to asking"}
            onClick={() => { void StartYoloMode(YOLO_MINUTES); }}
          >
            Allow all for {YOLO_MINUTES}m
          </button>
        </div>
      </div>
    </div>
  );
}

/**
 * The stack. Only the oldest prompt is shown: two modals on top of each other
 * is how a user ends up approving the one they did not read.
 */
export default function ToolApprovals({
  pending,
  note,
  onResolve,
  onDismissNote,
}: {
  pending: ToolApproval[];
  note: string;
  onResolve: (id: string, allow: boolean) => void;
  onDismissNote: () => void;
}) {
  const head = pending[0];
  if (!head) {
    if (note === "") return null;
    return (
      <div className="run-toasts">
        <div className="run-toast">
          <div className="rt-head">
            <span className="status-dot unknown" /> Tool approval
            <button
              className="panel-toggle inline"
              style={{ marginLeft: "auto" }}
              onClick={onDismissNote}
            >
              ×
            </button>
          </div>
          <div className="rt-body">{note}</div>
        </div>
      </div>
    );
  }
  return (
    <>
      <ApprovalCard req={head} onResolve={onResolve} />
      {pending.length > 1 && (
        <div className="approval-more">{pending.length - 1} more waiting</div>
      )}
    </>
  );
}
