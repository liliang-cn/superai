import { useI18n } from "../lib/i18n";
import { useNavigate } from "react-router-dom";
import { ArrowRightIcon, BellIcon, BotIcon, ShieldAlertIcon, TriangleAlertIcon, CheckCircleIcon } from "lucide-react";
import { AttentionItem, attentionTitle, whenOf } from "../canvas/attention";
import { ToolApproval } from "../lib/useToolApprovals";
import { PATHS, attentionPath } from "../lib/routes";
import "./tasks.css";
const TYPES: Record<string, { label: string; action: string; Icon: typeof BellIcon }> = {
  reminder: {label:"Reminder", action:"View reminder", Icon:BellIcon},
  approval: {label:"Approval required", action:"Review request", Icon:ShieldAlertIcon},
  bee: {label:"Waiting for your reply", action:"Open agent", Icon:BotIcon},
  report: {label:"From a bee", action:"Open bee", Icon:BotIcon},
  failed: {label:"Task failed", action:"Inspect task", Icon:TriangleAlertIcon},
  lost: {label:"Worker offline", action:"Inspect worker", Icon:TriangleAlertIcon},
  run: {label:"Coding run failed", action:"Inspect run", Icon:TriangleAlertIcon},
};
export default function TasksView({attention, approvals, onOpenConversation, onResolve}: {
  attention: {items: AttentionItem[]; loading: boolean; error:string; refresh:()=>void};
  approvals: ToolApproval[];
  onOpenConversation:(session:string)=>void;
  /** Answer an approval here, without going to the conversation it came from. */
  onResolve?:(id:string, allow:boolean)=>void;
}) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const items = attention.items.filter(i=>i.level==="needs" && i.kind!=="reminder");
  const reminders = attention.items.filter(i => i.kind === "reminder");
  const order = ["approval","bee","report","failed","run","lost"];
  const sorted = [...items].sort((a,b) => (order.includes(a.kind) ? order.indexOf(a.kind) : 99) - (order.includes(b.kind) ? order.indexOf(b.kind) : 99));
  const open = (it: AttentionItem) => {
    if(it.kind === "approval") {
      const approval = approvals.find(a=>a.id===it.ref);
      if(approval?.session) onOpenConversation(approval.session);
      else navigate(PATHS.home);
    } else navigate(attentionPath(it));
  };
  return <div className="attention-view">
    <header className="attention-heading"><span className={`attention-symbol${items.length ? " urgent" : ""}`}><BellIcon size={24}/></span><div><h1>{t("Needs your attention")}</h1><p>{items.length ? t("{count} items need a decision, a reply or an investigation.", {count:items.length}) : t("Decisions, issues and reminders that need your attention appear here.")}</p></div><button className="attention-refresh" onClick={attention.refresh}>{t("Refresh")}</button></header>
    {attention.error && <div className="attention-error" role="alert">{t(attention.error)} <button onClick={attention.refresh}>{t("Retry")}</button></div>}
    {!attention.error && !items.length && !reminders.length && <div className="attention-empty"><CheckCircleIcon size={36}/><h2>{t(attention.loading ? "Checking what needs you…" : "Nothing needs your attention")}</h2><p>{t("Approval requests, agents waiting for a reply, failed tasks, offline workers and reminders will appear here automatically.")}</p></div>}
    {items.length > 0 && <h2 className="attention-section-title">{t("Needs action")} <span>{items.length}</span></h2>}
    <div className="attention-cards">{[...sorted, ...reminders].map((it,index)=>{
      const type=TYPES[it.kind] ?? {label:"Needs attention",action:"Open details",Icon:BellIcon};
      const Icon=type.Icon;
      const approval=approvals.find(a=>a.id===it.ref);
      return <div key={`${it.kind}-${it.ref}-${index}`}>{it.kind === "reminder" && index === sorted.length && <h2 className="attention-section-title">{t("Reminders")} <span>{reminders.length}</span></h2>}<article className={`attention-card ${it.kind}`} key={`${it.kind}-${it.ref}-${index}`}>
        <Icon size={22} className="attention-card-icon"/><div className="attention-card-content"><span className="attention-category">{t(type.label)}</span><h2>{attentionTitle(it)}</h2>{it.kind === "report" && <small>{t("{count} updates · latest shown", {count:it.updates ?? 1})}</small>}{it.detail && !(approval?.command && approval.command === it.detail) && <p>{it.detail}</p>}{it.kind === "reminder" && <p className="attention-reminder-time">{whenOf(it)}</p>}{approval?.command && <pre>{approval.command}</pre>}{it.kind!=="report" && <small>{t(it.kind==="approval" ? "The agent is paused until you review this request." : it.kind==="bee" ? "The agent needs your input to continue." : it.kind==="reminder" ? "Open your reminders to review this item." : "Open the original item to investigate and take action.")}</small>}</div>
        {it.kind==="approval" && approval && onResolve ? (
          <div className="attention-actions">
            <button className="attention-action" onClick={()=>onResolve(approval.id, true)}>{t("Allow once")}</button>
            <button className="attention-action ghost" onClick={()=>onResolve(approval.id, false)}>{t("Deny")}</button>
          </div>
        ) : (
          <button className="attention-action" onClick={()=>open(it)}>{t(type.action)}<ArrowRightIcon size={16}/></button>
        )}
      </article></div>;
    })}</div>
  </div>;
}
