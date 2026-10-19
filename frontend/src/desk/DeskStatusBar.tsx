import { useEffect, useState } from "react";
import { BotIcon, CpuIcon, HexagonIcon } from "lucide-react";
import { Dashboard, HiveLinkStatus } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { useHive } from "../canvas/data";
import { useI18n } from "../lib/i18n";
import { openSwitcher } from "../lib/hivelink";
import type { AppStatus } from "../lib/types";

/** Runtime status belongs to every desktop page, below its working area. */
const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);

export default function DeskStatusBar({ status, loading, codingRuns }: { status:AppStatus|null; loading:boolean; codingRuns:number }) {
  const { t } = useI18n();
  const hive = useHive();
  const [link, setLink] = useState<{linked:boolean; live:boolean; url?:string}|null>(null);
  const [runtime, setRuntime] = useState<{model:string; active:number; error:boolean}>({model:"",active:0,error:false});
  useEffect(() => {
    let alive = true;
    let timer = 0;
    const read = async () => {
      const results = await Promise.allSettled([Dashboard(), HiveLinkStatus()]);
      if (!alive) return;
      if (results[0].status === "fulfilled") {
        const data = results[0].value;
        setRuntime({model:String(data?.llm?.model ?? ""), active:Array.isArray(data?.activeRuns) ? data.activeRuns.length : 0, error:false});
      } else setRuntime(current => ({...current,error:true}));
      // A browser tab is on the server that served it; what that server says
      // about links is about its own machine, not this window.
      if (served) setLink({linked:true, live:true, url:location.host});
      else if (results[1].status === "fulfilled") setLink(results[1].value);
    };
    const refresh = () => { window.clearTimeout(timer); timer = window.setTimeout(read,250); };
    void read();
    const interval = window.setInterval(read,15000);
    const off = ["hivelink:state","chat:event","chat:done","chat:error","chat:cancelled","cli:run"].map(name=>EventsOn(name,refresh));
    return () => {alive=false;window.clearInterval(interval);window.clearTimeout(timer);off.forEach(stop=>typeof stop==="function"&&stop());};
  }, []);
  const disconnected = runtime.error || (link?.linked && !link.live);
  const state = loading ? "Connecting…" : disconnected ? "Disconnected" : status?.ready ? "Ready" : "Not ready";
  const error = disconnected ? t("Backend connection is unavailable.") : status?.error;
  const workers = hive.members.filter(member => !/queen/.test(member.name));
  const live = workers.filter(member=>member.state==="live").length;
  return <footer className="dk-statusbar" aria-label={t("Status bar")}>
    <button className="dk-status-connection" onClick={openSwitcher} title={error || t("Switch backend")}>
      <i className={`dk-status-dot${disconnected || !loading && !status?.ready ? " bad" : loading ? " pending" : ""}`}/>
      <span>{t(state)}</span><span className="dk-status-backend">{link?.linked ? link.url?.replace(/^https?:\/\//,"") : t("This Mac only")}</span>
    </button>
    {runtime.model && <span className="dk-status-model" title={runtime.model}><CpuIcon size={12}/>{runtime.model}</span>}
    <span className="dk-status-spacer"/>
    {hive.loaded && hive.role && <span className="dk-status-workers"><HexagonIcon size={12}/>{t("{live}/{total} workers online",{live,total:workers.length})}</span>}
    <span className="dk-status-runs"><BotIcon size={12}/>{runtime.active + codingRuns > 0 ? t("{count} running",{count:runtime.active+codingRuns}) : t("Idle")}</span>
    <span className="dk-status-tools" title={t("Connected MCP tools")}>MCP {status?.mcp ?? 0} · {t("{count} tools",{count:status?.mcpTools ?? 0})}</span>
  </footer>;
}
