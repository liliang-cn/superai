import React, { useEffect, useState } from "react";
import { StopYoloMode, ToolApprovalInfo } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime";

/**
 * The banner that says approvals are switched off right now.
 *
 * This is the half of YOLO mode that makes it safe to have at all. The mode
 * turns every approval prompt into a silent yes, so the one thing that must not
 * happen is forgetting it is on — and prompts stopping is exactly the sort of
 * absence nobody notices. So it is stated, continuously, with the time left and
 * a way out, in the middle of the screen where it cannot be mistaken for
 * decoration.
 *
 * The remaining time is recomputed from the deadline rather than counted down
 * from a number, so a tab that was asleep comes back with the truth.
 */
export default function YoloBanner() {
  const [until, setUntil] = useState<Date | null>(null);
  const [, tick] = useState(0);

  // Asked on mount as well as listened for: a page that loads mid-window would
  // otherwise show nothing, which is the failure this component exists to
  // prevent.
  useEffect(() => {
    let alive = true;
    ToolApprovalInfo(1)
      .then((info: Record<string, unknown>) => {
        if (!alive) return;
        if (info?.yolo && typeof info.yoloUntil === "string") setUntil(new Date(info.yoloUntil));
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    return EventsOn("tool:yolo", (e: Record<string, unknown>) => {
      if (e?.active && typeof e.until === "string") setUntil(new Date(e.until));
      else setUntil(null);
    });
  }, []);

  // One second is enough to keep the number honest and cheap enough to ignore.
  useEffect(() => {
    if (!until) return;
    const id = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [until]);

  if (!until) return null;
  const left = Math.max(0, Math.round((until.getTime() - Date.now()) / 1000));
  if (left === 0) return null;

  const mins = Math.floor(left / 60);
  const secs = left % 60;
  const remaining = mins > 0 ? `${mins}m ${String(secs).padStart(2, "0")}s` : `${secs}s`;

  return (
    <div className="yolo-banner" role="status">
      <span>⚡ Approving everything — {remaining} left</span>
      <button
        onClick={() => {
          void StopYoloMode();
          setUntil(null);
        }}
      >
        Stop now
      </button>
    </div>
  );
}
