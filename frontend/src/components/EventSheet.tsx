import { useState } from "react";
import { AlarmClockIcon, CalendarIcon, MapPinIcon, UsersIcon } from "lucide-react";
import { ForgetLife } from "../../wailsjs/go/app/App";
import { AttentionItem } from "../canvas/attention";
import { toast } from "../lib/toasts";
import "./life.css";

/** A meeting or reminder from "Coming up", opened: when (yours and its own
 *  zone), who, where. A meeting that was saved wrong can be deleted here; any
 *  other change is said to the queen. */
export default function EventSheet({ item, onClose, onGone }: { item: AttentionItem; onClose: () => void; onGone?: () => void }) {
  const [sure, setSure] = useState(false);
  const at = item.at ? new Date(item.at) : null;
  const yours = at?.toLocaleString(undefined, { weekday: "long", month: "long", day: "numeric", hour: "2-digit", minute: "2-digit" });
  // The time where it was set, when that is not here.
  const here = at ? -at.getTimezoneOffset() : 0;
  const theirs = (() => {
    if (!at || !item.zone) return "";
    const m = /^([+-])(\d\d):(\d\d)$/.exec(item.zone);
    if (!m) return "";
    const off = (m[1] === "-" ? -1 : 1) * (Number(m[2]) * 60 + Number(m[3]));
    if (off === here) return "";
    const t = new Date(at.getTime() + off * 60000);
    return `${String(t.getUTCHours()).padStart(2, "0")}:${String(t.getUTCMinutes()).padStart(2, "0")} there (UTC${item.zone})`;
  })();
  const people = item.detail?.replace(/^With /, "");

  const forget = async () => {
    try {
      await ForgetLife("schedule", item.ref ?? "");
      toast.success(`Deleted “${item.title}”`);
      onGone?.();
      onClose();
    } catch (e) {
      toast.error(String(e));
    }
  };

  return (
    <div className="cv-scrim ev-scrim" onClick={onClose}>
      <div className="cv-glass cv-sheet ev-sheet" onClick={(e) => e.stopPropagation()}>
        <h3>{item.title}</h3>
        <dl className="ev-rows">
          <dt>{item.kind === "reminder" ? <AlarmClockIcon size={15} /> : <CalendarIcon size={15} />}</dt>
          <dd>{yours ?? "No time set"}{theirs && <small>{theirs}</small>}</dd>
          {people && item.kind === "event" && <><dt><UsersIcon size={15} /></dt><dd>{people}</dd></>}
          {item.place && <><dt><MapPinIcon size={15} /></dt><dd>{item.place}</dd></>}
          {item.kind === "reminder" && item.detail && <><dt /><dd>{item.detail}</dd></>}
        </dl>
        <div className="ev-foot">
          <span>To change it, tell the queen.</span>
          {item.kind === "event" && item.ref && (
            sure
              ? <button className="cv-pill ev-del" onClick={forget}>Delete it</button>
              : <button className="cv-pill" onClick={() => setSure(true)}>Delete</button>
          )}
          <button className="cv-pill ink" onClick={onClose}>Done</button>
        </div>
      </div>
    </div>
  );
}
