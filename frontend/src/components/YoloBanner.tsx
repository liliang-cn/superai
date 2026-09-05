import React, { useEffect, useState } from "react";
import { StopYoloMode, ToolApprovalInfo } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime";

/**
 * The banner that says approvals are switched off right now.
 *
 * This is the half of YOLO mode that makes it safe to have at all. The mode
 * turns every approval prompt into a silent yes and does not end on its own, so
 * the one thing that must not happen is forgetting it is on — and prompts
 * stopping is exactly the sort of absence nobody notices. So it is stated,
 * continuously, with how long it has been on and a way out, in the middle of
 * the screen where it cannot be mistaken for decoration.
 *
 * The elapsed time is recomputed from the start rather than counted up from a
 * number, so a tab that was asleep comes back with the truth.
 */
export default function YoloBanner() {
  const [since, setSince] = useState<Date | null>(null);
  const [, tick] = useState(0);

  // Asked on mount as well as listened for: a page that loads while it is on
  // would otherwise show nothing, which is the failure this component exists to
  // prevent.
  useEffect(() => {
    let alive = true;
    ToolApprovalInfo(1)
      .then((info: Record<string, unknown>) => {
        if (!alive) return;
        if (info?.yolo) setSince(typeof info.yoloSince === "string" ? new Date(info.yoloSince) : new Date());
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    return EventsOn("tool:yolo", (e: Record<string, unknown>) => {
      if (e?.active) setSince(typeof e.since === "string" ? new Date(e.since) : new Date());
      else setSince(null);
    });
  }, []);

  // One second is enough to keep the number honest and cheap enough to ignore.
  useEffect(() => {
    if (!since) return;
    const id = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [since]);

  if (!since) return null;
  const on = Math.max(0, Math.round((Date.now() - since.getTime()) / 1000));
  const hrs = Math.floor(on / 3600);
  const mins = Math.floor((on % 3600) / 60);
  const secs = on % 60;
  const elapsed =
    hrs > 0
      ? `${hrs}h ${String(mins).padStart(2, "0")}m`
      : mins > 0
        ? `${mins}m ${String(secs).padStart(2, "0")}s`
        : `${secs}s`;

  return (
    <div className="yolo-banner" role="status">
      <span>⚡ Approving everything — on for {elapsed}</span>
      <button
        onClick={() => {
          void StopYoloMode();
          setSince(null);
        }}
      >
        Stop now
      </button>
    </div>
  );
}
