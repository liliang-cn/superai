import { useCallback, useEffect, useState } from "react";
import { CheckIcon, LaptopIcon, PlusIcon, ServerIcon, XIcon } from "lucide-react";
import { ForgetHive, HiveLinkStatus, HiveLinks, UseHive } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { HiveLinkEntry, HiveLinkInfo, setAlone } from "../lib/hivelink";
import LinkHive from "../components/LinkHive";
import { noDrag } from "./DeskShell";

const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);

/** The window starts over on the backend it now points at: every screen,
 *  every stream, the bridge itself are set up once, at load. */
const restart = () => window.location.reload();

/**
 * Which backend this window is: one of the hives it has been paired with, or
 * this Mac on its own. At the foot of the rail on every page; the sidebar's
 * footer opens it too.
 */
export default function BackendSwitcher() {
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [status, setStatus] = useState<HiveLinkInfo>({ linked: false, live: false });
  const [saved, setSaved] = useState<HiveLinkEntry[]>([]);
  const [forgetting, setForgetting] = useState("");
  const [err, setErr] = useState("");

  const load = useCallback(() => {
    HiveLinkStatus().then((s) => setStatus(s as HiveLinkInfo)).catch(() => {});
    HiveLinks().then((l) => setSaved((l ?? []) as HiveLinkEntry[])).catch(() => {});
  }, []);

  useEffect(() => {
    if (served) return;
    load();
    const off = EventsOn("hivelink:state", load);
    const show = () => { load(); setOpen(true); };
    window.addEventListener("superai:switcher", show);
    return () => {
      if (typeof off === "function") off();
      window.removeEventListener("superai:switcher", show);
    };
  }, [load]);

  if (served) return null;

  const use = async (id: string) => {
    setErr("");
    try {
      await UseHive(id);
      setAlone(id === "");
      restart();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    }
  };

  const forget = async (id: string) => {
    setErr("");
    try {
      await ForgetHive(id);
      setForgetting("");
      if (status.linked && saved.find((h) => h.id === id)?.active) restart();
      else load();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    }
  };

  const onMac = !status.linked;

  return (
    <>
      <button title={onMac ? "This Mac" : `Backend: ${status.name || status.url}`} style={noDrag} className="dk-app dk-a-backend"
        onClick={() => { load(); setOpen((o) => !o); }}>
        {onMac ? <LaptopIcon size={19} /> : <ServerIcon size={19} />}
        <i className={onMac ? "dk-be-dot" : status.live ? "dk-be-dot on" : "dk-be-dot off"} />
      </button>
      {open && (
        <>
          <div className="dk-be-scrim" style={noDrag} onClick={() => { setOpen(false); setForgetting(""); }} />
          <div className="cv-glass dk-be" style={noDrag} role="dialog" aria-label="Backend">
            <b className="dk-be-h">Backend</b>
            {saved.map((h) => (
              <div key={h.id} className={h.active ? "dk-be-row on" : "dk-be-row"}>
                <button className="dk-be-pick" onClick={() => !h.active && use(h.id)}>
                  <ServerIcon size={16} />
                  <span>
                    {h.name}
                    <small>{h.active ? (status.live ? "In use, connected" : "In use, reconnecting") : h.url}</small>
                  </span>
                  {h.active && <CheckIcon size={16} className="dk-be-check" />}
                </button>
                {forgetting === h.id ? (
                  <span className="dk-be-confirm">
                    <button className="dk-be-forget" onClick={() => forget(h.id)}>Forget</button>
                    <button onClick={() => setForgetting("")}>Keep</button>
                  </span>
                ) : (
                  <button className="dk-be-x" title="Forget this hive" onClick={() => setForgetting(h.id)}><XIcon size={14} /></button>
                )}
              </div>
            ))}
            <div className={onMac ? "dk-be-row on" : "dk-be-row"}>
              <button className="dk-be-pick" onClick={() => !onMac && use("")}>
                <LaptopIcon size={16} />
                <span>This Mac<small>On its own, no hive</small></span>
                {onMac && <CheckIcon size={16} className="dk-be-check" />}
              </button>
            </div>
            <button className="dk-be-add" onClick={() => { setOpen(false); setAdding(true); }}><PlusIcon size={15} />Add a hive…</button>
            {forgetting && <p className="dk-be-note">Forgetting also unpairs this Mac on that hive.</p>}
            {err && <p className="dk-be-err">{err}</p>}
          </div>
        </>
      )}
      {adding && <LinkHive onLinked={restart} onCancel={() => setAdding(false)} />}
    </>
  );
}
