import React, { useEffect, useState } from "react";
import { ZapIcon } from "lucide-react";
import { StartYoloMode, StopYoloMode, ToolApprovalInfo } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime";

/**
 * Whether approvals are switched off right now, and the switch itself.
 *
 * This used to be a banner across the top of the window. It was there because a
 * mode that turns every approval prompt into a silent yes must not be
 * forgettable, and prompts stopping is exactly the sort of absence nobody
 * notices — but it read as a toast, which is a thing you wait out rather than a
 * thing you operate, and it sat over the conversation for as long as it was on.
 *
 * A lit icon in the strip that is on screen in every view says the same thing
 * continuously, in the place where the other controls of its kind live, and it
 * is also the way out: one click. The state is loud when it is on (amber, and
 * it is the only lit thing up there) and quiet when it is not.
 */
export default function YoloToggle() {
  const [gated, setGated] = useState(false);
  const [on, setOn] = useState(false);

  // Asked on mount as well as listened for: a window that opens while it is on
  // would otherwise show nothing, which is the failure this exists to prevent.
  useEffect(() => {
    let alive = true;
    ToolApprovalInfo(1)
      .then((info: Record<string, unknown>) => {
        if (!alive) return;
        setGated(Boolean(info?.enabled));
        setOn(Boolean(info?.yolo));
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    return EventsOn("tool:yolo", (e: Record<string, unknown>) => setOn(Boolean(e?.active)));
  }, []);

  // Nothing to relax when the gate is off in Settings: there are no prompts for
  // this to stop, and a switch that does nothing is worse than no switch.
  if (!gated) return null;

  return (
    <button
      type="button"
      className={`pill-btn icon-only yolo-toggle${on ? " on" : ""}`}
      onClick={() => {
        // Optimistic, then corrected by the event the backend sends back.
        setOn(!on);
        void (on ? StopYoloMode() : StartYoloMode());
      }}
      title={
        on
          ? "Approving everything — click to go back to asking"
          : "Asking before shell commands — click to approve everything"
      }
      aria-label="YOLO mode"
      aria-pressed={on}
    >
      <ZapIcon className="size-4" />
    </button>
  );
}
