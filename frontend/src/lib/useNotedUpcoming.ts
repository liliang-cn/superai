import { useEffect, useState } from "react";
import { NotedUpcoming } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { readSchedule, ScheduleItem } from "./schedule";

/** The home's 接下来的安排: noted's next few items (NotedUpcoming), read
 *  again when the schedule changes, when the window comes back, and each
 *  minute. ok is false while noted cannot be reached. */
export function useNotedUpcoming(limit = 5) {
  const [state, setState] = useState<{ items: ScheduleItem[]; more: boolean; ok: boolean }>({ items: [], more: false, ok: false });
  useEffect(() => {
    let alive = true;
    const load = () => {
      NotedUpcoming(limit)
        .then((r: any) => { if (alive) setState({ items: readSchedule(r).items, more: !!r?.more, ok: true }); })
        .catch(() => { if (alive) setState({ items: [], more: false, ok: false }); });
    };
    load();
    const off = EventsOn("noted:alarms", load);
    const poll = window.setInterval(load, 60000);
    window.addEventListener("focus", load);
    return () => {
      alive = false;
      if (typeof off === "function") off();
      window.clearInterval(poll);
      window.removeEventListener("focus", load);
    };
  }, [limit]);
  return state;
}
