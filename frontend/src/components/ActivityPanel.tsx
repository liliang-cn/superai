import React from "react";
import { ActivityIcon, ScrollTextIcon } from "lucide-react";
import { Counters, PulseTicker, usePulse } from "./Reactor";

/**
 * The Stats page's activity stream, beside the conversation.
 *
 * Stats answers "what is this process doing" on a page you have to go to. The
 * question comes up most while a turn is running, which is exactly when you
 * are looking at the chat and cannot leave it — so the same feed, off the same
 * pushed meter, lives in the rail too.
 *
 * It is the process's activity and not this conversation's: a pulse event
 * carries no session, and a scheduled run or a dashboard refresh burning
 * tokens in the background is a thing worth seeing rather than filtering out.
 * The header says so.
 */
export function ActivityBody() {
  const pulse = usePulse();
  return (
    <div className="cr-scope side-activity">
      <div className="side-activity-h">
        <ActivityIcon className="size-3.5" />
        <span className="cr-eyebrow">This process</span>
        <span className={`cr-tag ${pulse.live ? "cyan" : ""}`}>{pulse.live ? "burning" : "idle"}</span>
      </div>
      <Counters snap={pulse} />
      <div className="side-activity-h">
        <ScrollTextIcon className="size-3.5" />
        <span className="cr-eyebrow">Activity</span>
        <span className="cr-tag">newest first</span>
      </div>
      <PulseTicker snap={pulse} max={200} />
    </div>
  );
}

export default ActivityBody;
