import { useCallback, useEffect, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";
import { PendingToolApprovals, ResolveToolApproval } from "../../wailsjs/go/main/App";

/** One tool call waiting for a human, as the backend describes it. */
export interface ToolApproval {
  id: string;
  tool: string;
  /** The shell command, for the shell-family tools. Never abbreviated. */
  command: string;
  /** Everything else the tool was called with, for tools that are not shells. */
  args: Record<string, unknown>;
  session: string;
  /** RFC3339. When the gate stops waiting and denies on its own. */
  expiresAt: string;
}

function toApproval(payload: any): ToolApproval | null {
  const id = String(payload?.id ?? "");
  if (id === "") return null;
  return {
    id,
    tool: String(payload?.tool ?? "unknown tool"),
    command: String(payload?.command ?? ""),
    args: (payload?.args as Record<string, unknown>) ?? {},
    session: String(payload?.session ?? ""),
    expiresAt: String(payload?.expiresAt ?? ""),
  };
}

/**
 * The prompts the agent is currently blocked on.
 *
 * Mounted once at the app root, like the scheduled-run log and for a stronger
 * version of the same reason: the agent's turn is stopped dead until one of
 * these is answered, so a listener that only exists while the chat view is open
 * would leave the user staring at a spinner with the question on a screen they
 * are not looking at.
 *
 * Two sources, on purpose. The event carries new requests to a live UI; the
 * PendingToolApprovals call on mount recovers the ones that were asked before
 * this UI existed. Only the second covers the serve-mode reload — an SSE stream
 * has no replay, so a browser tab that refreshes mid-prompt would otherwise
 * never learn that something is waiting, and the call would sit there until it
 * timed out.
 */
export function useToolApprovals() {
  const [pending, setPending] = useState<ToolApproval[]>([]);
  const [note, setNote] = useState("");

  const add = useCallback((next: ToolApproval) => {
    setPending((prev) => (prev.some((p) => p.id === next.id) ? prev : [...prev, next]));
  }, []);

  const drop = useCallback((id: string) => {
    setPending((prev) => prev.filter((p) => p.id !== id));
  }, []);

  useEffect(() => {
    if (typeof window === "undefined" || !(window as any).runtime) return;

    PendingToolApprovals()
      .then((list) => {
        (list || []).forEach((raw) => {
          const a = toApproval(raw);
          if (a) add(a);
        });
      })
      .catch(() => {});

    const offAsk = EventsOn("tool:approval", (payload: any) => {
      const a = toApproval(payload);
      if (a) add(a);
    });
    // The backend retires a card when the call is over however it ended:
    // answered here, answered in another window, timed out, or the run was
    // stopped. Without this a dead prompt would sit on screen with buttons
    // that do nothing.
    const offClose = EventsOn("tool:approval:closed", (payload: any) => {
      drop(String(payload?.id ?? ""));
    });
    return () => {
      offAsk();
      offClose();
    };
  }, [add, drop]);

  const resolve = useCallback(
    (id: string, allow: boolean) => {
      // Removed optimistically: the agent goroutine is blocked on this answer,
      // and leaving the card up while the round trip completes invites a second
      // click on a decision that has already been made.
      drop(id);
      ResolveToolApproval(id, allow)
        .then((msg) => setNote(msg === "ok" ? "" : msg))
        .catch((e) => setNote(String(e?.message || e)));
    },
    [drop],
  );

  return { pending, note, resolve, dismissNote: () => setNote("") };
}
