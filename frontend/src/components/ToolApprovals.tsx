import React, { useEffect, useState } from "react";
import { ShieldAlertIcon } from "lucide-react";
import { ToolApproval } from "../lib/useToolApprovals";
import { StartYoloMode } from "../../wailsjs/go/app/App";

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
            <ShieldAlertIcon className="size-4 approval-icon" /> {req.by || "SuperAI"} wants to run{" "}
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
              the only way to stop that is a switch buried elsewhere, that is the
              switch they will flip. It stays on until it is turned off — the
              banner is what keeps saying so. */}
          <button
            className="btn ghost approval-yolo"
            title="Approve everything from now on, until you switch it off"
            onClick={() => { void StartYoloMode(); }}
          >
            Allow all
          </button>
        </div>
      </div>
    </div>
  );
}

/**
 * A standing agent's question, docked in the corner instead of over the app.
 * The person is doing something else — that is the point of an agent that
 * runs on its own — and a modal would take the whole window away from them
 * for a call they may answer in a minute. The command is still shown whole.
 */
function DockedApproval({ req, more, onResolve }: { req: ToolApproval; more: number; onResolve: (id: string, allow: boolean) => void }) {
  const left = useCountdown(req.expiresAt);
  return (
    <div className="approval-dock" role="alertdialog" aria-label={`${req.by} wants to run ${req.tool}`}>
      <div className="ad-head">
        <span className="ad-who">{req.by}</span>
        <span className="ad-what">wants to run <b>{req.tool}</b></span>
        {left >= 0 && <span className="ad-left">{left > 0 ? fmtLeft(left) : "expired"}</span>}
      </div>
      {req.command ? <pre className="approval-cmd">{req.command}</pre> : <ArgsBlock args={req.args} />}
      <div className="ad-actions">
        {more > 0 && <span className="ad-more">{more} more waiting</span>}
        <button className="btn ghost sm" onClick={() => onResolve(req.id, false)}>Deny</button>
        <button className="btn sm" onClick={() => onResolve(req.id, true)}>Allow once</button>
      </div>
    </div>
  );
}

function fmtLeft(s: number) {
  return s >= 60 ? `${Math.floor(s / 60)} min left` : `${s}s left`;
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
  // Your own turn's questions stop you; a standing agent's wait in the corner.
  const mine = pending.filter((p) => !p.by);
  const theirs = pending.filter((p) => p.by);
  const head = mine[0];
  const dock = theirs[0] ? <DockedApproval req={theirs[0]} more={theirs.length - 1} onResolve={onResolve} /> : null;
  if (!head && dock) return dock;
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
      {mine.length > 1 && (
        <div className="approval-more">{mine.length - 1} more waiting</div>
      )}
      {dock}
    </>
  );
}
