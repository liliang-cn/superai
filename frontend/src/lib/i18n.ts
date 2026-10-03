import { useSyncExternalStore } from "react";
export type Language = "en" | "zh-CN";
const KEY = "superai-language";
function initial(): Language {
  try { const saved = localStorage.getItem(KEY); if (saved === "en" || saved === "zh-CN") return saved; } catch { /* private browser */ }
  return navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}
let language = initial();
const listeners = new Set<() => void>();
const zh: Record<string,string> = {
  "{name} asks to run something":"{name} 请求执行操作", "{name} is waiting for you":"{name} 正在等你回复", "{name} could not finish an order":"{name} 未能完成任务", "{name} is not answering":"{name} 没有响应", "{name} stopped with an error":"{name} 因错误停止", "{count} min ago":"{count} 分钟前", "{count} h ago":"{count} 小时前", "{count} d ago":"{count} 天前", "unknown age":"更新时间未知",
  "Needs your attention":"待你处理", "Needs action":"需要处理", "Reminders":"提醒", "Reminder":"提醒", "Refresh":"刷新", "Retry":"重试",
  "Approval required":"等待审批", "Review request":"查看并审批", "Waiting for your reply":"等待你回复", "Open agent":"回复智能体", "Task failed":"任务失败", "Inspect task":"查看并处理", "Worker offline":"工作节点离线", "Inspect worker":"检查工作节点", "Coding run failed":"编程任务失败", "Inspect run":"查看运行详情", "Needs attention":"需要你处理", "Open details":"打开详情", "View reminder":"查看提醒",
  "The agent is paused until you review this request.":"智能体已暂停，正在等待你审批这项请求。", "The agent needs your input to continue.":"智能体需要你的回复才能继续。", "Open your reminders to review this item.":"打开提醒记录，查看这项提醒。", "Open the original item to investigate and take action.":"进入对应事项，查看原因并处理。",
  "Decisions, issues and reminders that need your attention appear here.":"需要你决策、回复、处理的事项和提醒会集中显示在这里。", "Checking what needs you…":"正在检查待处理事项…", "Nothing needs your attention":"暂时没有需要你处理的事项", "Approval requests, agents waiting for a reply, failed tasks, offline workers and reminders will appear here automatically.":"审批请求、等待回复的智能体、失败任务、离线工作节点和提醒会自动显示在这里。", "Could not load items that need your attention.":"无法加载待处理事项。", "{count} items need a decision, a reply or an investigation.":"有 {count} 项事项需要你决策、回复或检查。", "{count} items waiting for you":"有 {count} 项待你处理", "Nothing waiting for you":"暂无待处理事项", "Could not check pending items":"无法检查待处理事项",
  "Model":"模型", "External agents":"外部智能体", "Language":"语言", "Interface language":"界面语言", "Applies immediately and is remembered on this device.":"立即生效，并记住此设备上的选择。", "Settings":"设置", "Accounts":"账户", "Safety":"安全", "Memory":"记忆", "Notifications":"通知中心", "Agents":"智能体", "Runtime":"运行环境", "Configure providers and runtime. Saving persists and rebuilds the backend.":"配置模型服务和运行环境。保存后会更新后端配置。", "Loading settings…":"正在加载设置…",
  "Home":"首页", "Chat":"对话", "The queen":"主助手", "Hive":"蜂群", "Bees":"智能体", "Coding":"编程", "Knowledge":"知识库", "Skills":"技能", "MCP":"MCP", "Stats":"统计", "Records":"记录", "Workspace":"工作区", "System":"系统", "New chat":"新对话", "Later":"稍后", "Recents":"最近对话", "Pinned":"已固定", "Coming up":"近期安排", "Needs you":"待你处理", "Light":"浅色", "Dark":"深色", "Switch backend":"切换后端", "Show the hive":"展开蜂群面板", "Hide this pane":"收起面板", "Resize sidebar":"调整侧边栏宽度", "Drag to resize · double-click to reset":"拖拽调整宽度 · 双击复位",
  "Clear all":"清空通知", "Nothing yet.":"暂无通知。", "Loading…":"正在加载…", "just now":"刚刚", "yesterday":"昨天", "{count} unread":"{count} 条未读通知", "{count}m ago":"{count} 分钟前", "{count}h ago":"{count} 小时前", "{count}d ago":"{count} 天前", "Today":"今天", "Tomorrow":"明天",
  "Refresh settings":"刷新设置", "Refresh by re-running the saved question.":"重新查询并更新看板内容", "View original question":"查看原始提问",
  "Dashboards":"看板", "All dashboards":"全部看板", "Full screen":"全屏", "Dashboard name":"看板名称", "Manual only":"仅手动刷新", "Every hour":"每小时", "Every day at 08:00":"每天 08:00", "Weekdays at 08:00":"工作日 08:00", "Mondays at 08:00":"每周一 08:00", "Open":"打开", "Delete":"删除", "Close":"关闭", "Close (Esc)":"关闭（Esc）", "Ask again and replace the contents":"重新提问并更新内容", "No saved question to re-ask":"没有保存用于刷新的问题", "Refreshes by re-asking":"刷新时重新提问", "Nothing saved yet.":"暂无已保存的看板。", "Saved without a question, so it cannot refresh itself — it stays as it was.":"没有保存原始问题，因此无法自动刷新，内容保持原样。", "A reply that draws a chart, a wall or a panel gets a save button under it.":"包含图表、数据看板或面板的回复下方会显示保存按钮。", "Save as dashboard":"保存为看板", "Save":"保存", "Cancel":"取消", "Name":"名称", "Name this dashboard":"为看板命名", "refreshing…":"正在刷新…", "data as of":"数据更新于",
  "Send":"发送", "Stop":"停止", "Thinking…":"正在思考…", "Stopped.":"已停止。", "Ask the queen, or give the hive an order. @ for an agent or a worker":"向主助手提问或下达蜂群指令，输入 @ 选择智能体或工作节点", "This Mac only":"仅本机", "Linked":"已连接", "Reconnecting":"正在重连", "Not in a hive":"未加入蜂群", "Finding the hive…":"正在查找蜂群…", "This Mac on its own":"本机独立运行", "Conversation":"对话", "Untitled":"未命名", "Allow once":"允许一次", "Deny":"拒绝", "Open Bees":"打开智能体",
};
export function translate(key:string, values:Record<string,string|number> = {}) {
  let value = language === "zh-CN" ? zh[key] ?? key : key;
  for (const [name,replacement] of Object.entries(values)) value = value.split(`{${name}}`).join(String(replacement));
  return value;
}
function notify() { document.documentElement.lang=language; listeners.forEach(fn=>fn()); }
export function getLanguage() { return language; }
export function setLanguage(next:Language) { language=next; try {localStorage.setItem(KEY,next);} catch { /* in-memory preference still works */ } notify(); }
window.addEventListener("storage",e=>{if(e.key===KEY){language=initial();notify();}});
document.documentElement.lang=language;
export function useI18n() {
  const locale=useSyncExternalStore(fn=>{listeners.add(fn);return ()=>listeners.delete(fn);},()=>language);
  return {language:locale, setLanguage, t:translate};
}
