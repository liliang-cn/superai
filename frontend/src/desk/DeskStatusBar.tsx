import { useEffect, useState } from "react";
import { BotIcon, CpuIcon, HexagonIcon, MonitorIcon } from "lucide-react";
import { Dashboard, StartYoloMode, StopYoloMode, ToolApprovalInfo } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { useHive, useWorkers } from "../canvas/data";
import { useI18n } from "../lib/i18n";
import { hiveHost, isCutOff, useMembership } from "../lib/membership";
import { useHosts } from "../canvas/data";
import type { AppStatus } from "../lib/types";
import { machine } from "../lib/machine";
import { queenEngineNote, queenThinker } from "../lib/format";

/** Runtime status belongs to every desktop page, below its working area. */
const served = Boolean((window as unknown as Record<string, unknown>).superleoServed);

export default function DeskStatusBar({ status, loading, codingRuns }: { status:AppStatus|null; loading:boolean; codingRuns:number }) {
  const { t } = useI18n();
  const hive = useHive();
  const { m } = useMembership();
  const { hosts } = useHosts();
  const [runtime, setRuntime] = useState<{model:string; active:number; error:boolean}>({model:"",active:0,error:false});
  useEffect(() => {
    let alive = true;
    let timer = 0;
    const read = async () => {
      const results = await Promise.allSettled([Dashboard()]);
      if (!alive) return;
      if (results[0].status === "fulfilled") {
        const data = results[0].value;
        setRuntime({model:String(data?.llm?.model ?? ""), active:Array.isArray(data?.activeRuns) ? data.activeRuns.length : 0, error:false});
      } else setRuntime(current => ({...current,error:true}));
    };
    const refresh = () => { window.clearTimeout(timer); timer = window.setTimeout(read,250); };
    void read();
    const interval = window.setInterval(read,15000);
    const off = ["hive:membership","chat:event","chat:done","chat:error","chat:cancelled","cli:run"].map(name=>EventsOn(name,refresh));
    return () => {alive=false;window.clearInterval(interval);window.clearTimeout(timer);off.forEach(stop=>typeof stop==="function"&&stop());};
  }, []);
  // A browser tab's live updates: a stream that is down means answers and
  // events stop arriving while everything else still looks fine.
  const [stream, setStream] = useState<string>(() => String((window as unknown as { superleoStream?: { state: string } }).superleoStream?.state ?? "open"));
  useEffect(() => {
    if (!served) return;
    const off = EventsOn("stream:status", (h: { state?: string }) => setStream(String(h?.state ?? "")));
    // It may have changed between the first render and this subscription.
    setStream(String((window as unknown as { superleoStream?: { state: string } }).superleoStream?.state ?? "open"));
    return () => { if (typeof off === "function") off(); };
  }, []);
  const queenNote = queenEngineNote(hive.engine, hive.engineNow);
  // The model shown is the one thinking for the queen: her engine's, when one
  // takes her turns, not SuperLeo's own model sitting idle beside it.
  const thinker = queenThinker(hive.engine, hive.engineNow);
  // YOLO: every approval says yes until it is switched off (app_permission.go
  // StartYoloMode). Here, so it is one click from any screen.
  const [yolo, setYolo] = useState(false);
  useEffect(() => {
    ToolApprovalInfo(1).then((info: Record<string, unknown>) => setYolo(Boolean(info?.yolo))).catch(() => {});
    const off = EventsOn("tool:yolo", (e: Record<string, unknown>) => setYolo(Boolean(e?.active)));
    return () => { if (typeof off === "function") off(); };
  }, []);
  const flipYolo = () => {
    const next = !yolo;
    setYolo(next);
    (next ? StartYoloMode() : StopYoloMode())
      .then((r: Record<string, unknown>) => { if (r?.error) setYolo(!next); })
      .catch(() => setYolo(!next));
  };
  const cut = isCutOff(m);
  const disconnected = runtime.error || cut;
  const state = loading ? "Connecting…" : disconnected ? "Disconnected" : status?.ready ? "Ready" : "Not ready";
  const error = cut ? (m?.error || t("The hive is out of reach")) : runtime.error ? t("Backend connection is unavailable.") : status?.error;
  // Which hive this window is on: the one this Mac joined, the address a
  // browser tab is served from, else this Mac's own.
  const hiveName = m?.joined ? hiveHost(m.hive) : served ? location.host : t("This {machine}", { machine: t(machine) });
  const workers = useWorkers();
  const machines = hosts ? hosts.filter((h) => h.state === "live").length : 0;
  return <footer className="dk-statusbar" aria-label={t("Status bar")}>
    <span className="dk-status-connection" title={error || undefined} data-testid="status-hive">
      <i className={`dk-status-dot${disconnected || !loading && !status?.ready ? " bad" : loading ? " pending" : ""}`}/>
      <span>{t(state)}</span><span className="dk-status-backend">{hiveName}</span>
    </span>
    {thinker ? <span className="dk-status-model" title={thinker} data-testid="status-model"><CpuIcon size={12}/>{thinker}</span>
      : runtime.model && <span className="dk-status-model" title={runtime.model} data-testid="status-model"><CpuIcon size={12}/>{runtime.model}</span>}
    {served && stream === "down" && <span className="dk-status-stream" title={t("Answers and live updates are not arriving; reconnecting.")}><i className="dk-status-dot bad"/>{t("Live updates off")}</span>}
    {queenNote && <span className="dk-status-queen" title={t("SuperLeo answered in the engine's place")}>{t("Queen")}: {queenNote}</span>}
    <button type="button" className={`dk-status-yolo${yolo ? " on" : ""}`} onClick={flipYolo} aria-pressed={yolo}
      title={yolo ? t("YOLO on: tools and coding agents run without asking. Click to ask again.") : t("YOLO off: risky tools ask first. Click to stop asking.")} data-testid="yolo">
      <i className="dk-yolo-dot"/>YOLO
    </button>
    <span className="dk-status-spacer"/>
    {machines > 0 && <span className="dk-status-workers" data-testid="status-machines"><HexagonIcon size={12}/>{t("{count} machines online",{count:machines})}</span>}
    {workers.list.length > 0 && <span className="dk-status-workers" title={workers.list.map((w) => `${w.name} · ${w.host}`).join("\n")}><HexagonIcon size={12}/>{t("{live}/{total} workers online",{live:workers.up,total:workers.list.length})}</span>}
    <span className="dk-status-runs"><BotIcon size={12}/>{runtime.active + codingRuns > 0 ? t("{count} running",{count:runtime.active+codingRuns}) : t("Idle")}</span>
    <span className="dk-status-tools" title={t("Connected MCP tools")}>MCP {status?.mcp ?? 0} · {t("{count} tools",{count:status?.mcpTools ?? 0})}</span>
  </footer>;
}
