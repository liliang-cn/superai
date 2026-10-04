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
  "Calendar":"日历", "Event":"日程", "Time":"时间", "Location":"地点", "Participants":"参会人", "Schedules":"日程与自动任务", "Notes":"笔记", "People":"联系人", "Schedules, notes, people and reminders SuperAI keeps for you. Edits happen via Chat.":"查看日程、笔记、联系人和提醒，通过对话修改记录。",
  "Task instructions":"任务指令",
  "Status bar": "状态栏",
  "Connecting…": "正在连接…",
  "Disconnected": "连接断开",
  "Ready": "就绪",
  "Not ready": "未就绪",
  "Backend connection is unavailable.": "后端连接不可用。",
  "{live}/{total} workers online": "{live}/{total} 个工作节点在线",
  "{count} running": "{count} 项运行中",
  "Idle": "空闲",
  "Connected MCP tools": "已连接的 MCP 工具",
  "{count} tools": "{count} 个工具",
  "Theme": "主题",
  "Color": "颜色",
  "Background": "背景",
  "Glass": "玻璃效果",
  "Radius": "圆角",
  "Mode": "模式",
  "Reset": "恢复默认",
  "Done": "完成",
  "Honey": "蜂蜜金",
  "Orange": "橙色",
  "Red": "红色",
  "Rose": "玫瑰色",
  "Green": "绿色",
  "Teal": "青色",
  "Blue": "蓝色",
  "Yellow": "黄色",
  "Neutral": "中性",
  "Time-lapse": "随时间变化",
  "Stone": "石色",
  "Slate": "蓝灰",
  "Sand": "沙色",
  "Clear": "透明",
  "Frosted": "磨砂",
  "Solid": "实色",
  "From a bee": "智能体报告",
  "Open bee": "查看智能体",
  "{count} updates · latest shown": "共 {count} 条更新 · 显示最新一条",
  "Could not load notifications.": "无法加载通知。",
  "Could not mark notifications read.": "无法标记通知为已读。",
  "Could not clear notifications.": "无法清空通知。",
  "Installed skills SuperAI can activate during a turn.": "SuperAI 在执行任务时可以使用的已安装技能。",
  "+ Add skill": "+ 添加技能",
  "↻ Refresh": "刷新",
  "Add a skill": "添加技能",
  "No skills installed.": "尚未安装技能。",
  "Filter by name or what it does…": "按名称或用途筛选…",
  "Nothing found on this machine.": "当前后端没有找到可安装的技能。",
  "Use “+ Add skill” to install one already on this machine.": "点击“添加技能”，选择当前后端已有的技能。",
  "Skills already on this machine, including the ones Claude Code uses (~/.claude/skills).": "当前后端已有的技能，包括 Claude Code 使用的技能（~/.claude/skills）。",
  "Model Context Protocol servers SuperAI connects to for extra tools.": "SuperAI 通过 MCP 服务器连接扩展工具。",
  "+ Add server": "+ 添加服务器",
  "Add an MCP server": "添加 MCP 服务器",
  "Search": "搜索",
  "Searching…": "正在搜索…",
  "No servers matched.": "没有找到匹配的服务器。",
  "Install": "安装",
  "Installed": "已安装",
  "Installing…": "正在安装…",
  "Remove": "移除",
  "What should it be able to do? e.g. postgres, slack, filesystem": "需要什么能力？例如 postgres、slack、filesystem",
  "Search the official registry. The launch command comes from the registry — no need to know the package name.": "搜索官方注册表，启动命令由注册表提供。",
  "Advanced": "高级配置",
  "API Base URL": "API 地址",
  "API Key": "API 密钥",
  "Local Port": "本机端口",
  "show": "显示",
  "hide": "隐藏",
  "Use my accounts": "使用已登录账户",
  "Save settings": "保存设置",
  "OpenAI-compatible chat completions endpoint.": "兼容 OpenAI 的对话 API 服务地址。",
  "Only change this if something else already uses it.": "仅在端口被其他程序占用时修改。",
  "Off — SuperAI uses the API endpoint set under Advanced": "关闭时，SuperAI 使用“高级配置”中的 API 地址",
  "Sign in with the AI accounts you already pay for — no API key needed. Everything stays on this machine.": "登录已有的 AI 账户，无需填写 API 密钥。登录信息保存在当前后端。",
  "Memory store": "记忆存储",
  "Webhook URL": "Webhook 地址",
  "Webhook Secret": "Webhook 密钥",
  "Bot Token": "机器人令牌",
  "Allowed Chat IDs": "允许的聊天 ID",
  "Send test": "发送测试",
  "About you": "关于你",
  "Recall from memory": "搜索记忆",
  "Import a file into this graph": "导入文件到知识图谱",
  "Reload": "重新加载",
  "Everything SuperAI knows, as one graph it reads on every turn.": "以知识图谱查看 SuperAI 每次对话使用的知识。",
  "New run": "新建编程任务",
  "Ask before tools run": "执行工具前询问",
  "workspace": "工作目录",
  "The directory it works in, on that machine": "所选机器上的工作目录",
  "Select a coding agent": "选择编程智能体",
  "Extensions":"扩展",
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
  const locale=useSyncExternalStore(fn=>{listeners.add(fn);return ()=>listeners.delete(fn);},()=>language,()=>language);
  return {language:locale, setLanguage, t:translate};
}
