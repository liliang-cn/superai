import { useCallback, useEffect, useState } from "react";
import { Trash2Icon, XIcon } from "lucide-react";
import { ForgetLife, Life } from "../../wailsjs/go/app/App";
import { fromNow, parseTime } from "../lib/format";
import { toast } from "../lib/toasts";
import "./life.css";

type Row = Record<string, unknown>;

const text = (v: unknown) => (v == null ? "" : Array.isArray(v) ? v.join(", ") : String(v));

/** A person's profile in one line: what is known, not the field names. */
function about(info: Row): string {
  return Object.entries(info)
    .filter(([k, v]) => k !== "name" && k !== "updated_at" && text(v).trim())
    .map(([, v]) => text(v))
    .join(" · ");
}

/**
 * What SuperAI has written down about you — notes and people — beside the
 * graph it reads them from. Anything wrong can be deleted; everything is added
 * by talking to it.
 */
export default function AboutYou({ onClose }: { onClose: () => void }) {
  const [notes, setNotes] = useState<Row[]>([]);
  const [people, setPeople] = useState<[string, Row][]>([]);
  const [sure, setSure] = useState("");

  const load = useCallback(async () => {
    const life = await Life();
    setNotes([...((life.records ?? []) as Row[])].reverse());
    setPeople(Object.entries((life.persons ?? {}) as Record<string, Row>));
  }, []);
  useEffect(() => { load().catch((e) => toast.error(String(e))); }, [load]);

  const forget = async (kind: "record" | "person", key: string, label: string) => {
    if (sure !== kind + key) return setSure(kind + key);
    try {
      await ForgetLife(kind, key);
      toast.success(`Forgot “${label}”`);
      setSure("");
      load();
    } catch (e) {
      toast.error(String(e));
    }
  };

  const del = (kind: "record" | "person", key: string, label: string) => (
    <button className={`ay-del${sure === kind + key ? " sure" : ""}`} onClick={() => forget(kind, key, label)} title="Delete">
      {sure === kind + key ? "Delete" : <Trash2Icon size={14} />}
    </button>
  );

  return (
    <aside className="ay" onMouseLeave={() => setSure("")}>
      <header className="ay-h">
        <b>What I know about you</b>
        <button className="btn ghost sm icon-only" onClick={onClose} aria-label="Close"><XIcon size={15} /></button>
      </header>
      <div className="ay-body">
        <h4>People <span>{people.length}</span></h4>
        {people.length === 0 && <p className="ay-none">Nobody yet.</p>}
        {people.map(([name, info]) => (
          <div key={name} className="ay-row">
            <div><b>{name}</b><small>{about(info)}</small></div>
            {del("person", name, name)}
          </div>
        ))}
        <h4>Notes <span>{notes.length}</span></h4>
        {notes.length === 0 && <p className="ay-none">Nothing written down yet.</p>}
        {notes.map((n) => {
          const id = text(n.id), title = text(n.title) || text(n.body).slice(0, 40);
          const when = parseTime(text(n.occurred_at));
          return (
            <div key={id} className="ay-row">
              <div>
                {text(n.title) && <b>{text(n.title)}</b>}
                <small className="ay-body-text">{text(n.body)}</small>
                <em>{[text(n.type), text(n.project), when ? fromNow(when) : ""].filter(Boolean).join(" · ")}</em>
              </div>
              {del("record", id, title)}
            </div>
          );
        })}
      </div>
    </aside>
  );
}
