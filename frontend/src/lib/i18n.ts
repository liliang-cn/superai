import { useSyncExternalStore } from "react";
import de from "./locales/de.json";
import es from "./locales/es.json";
import fr from "./locales/fr.json";
import ja from "./locales/ja.json";
import ko from "./locales/ko.json";
export type Language = "en" | "zh-CN" | "de" | "fr" | "es" | "ja" | "ko";
/** Every interface language, in its own words, for the picker. */
export const LANGUAGES: { id: Language; name: string }[] = [
  { id: "en", name: "English" },
  { id: "zh-CN", name: "简体中文" },
  { id: "de", name: "Deutsch" },
  { id: "fr", name: "Français" },
  { id: "es", name: "Español" },
  { id: "ja", name: "\u65e5\u672c\u8a9e" },
  { id: "ko", name: "\ud55c\uad6d\uc5b4" },
];
const KEY = "superai-language";
// English is the default; Chinese is a choice in Settings (the core's case
// in the world, or the Settings page). A choice made before English became
// the default is reset once.
function initial(): Language {
  try {
    if (!localStorage.getItem(KEY + "-v2")) { localStorage.setItem(KEY + "-v2", "1"); localStorage.setItem(KEY, "en"); }
    const saved = localStorage.getItem(KEY); if (LANGUAGES.some((l) => l.id === saved)) return saved as Language;
  } catch { /* private browser */ }
  return "en";
}
let language = initial();
const listeners = new Set<() => void>();
const zh: Record<string,string> = { "Sign in with your AI account":"用你的 AI 账号登录", "Sign in":"登录", "Use an API key":"用 API Key",
  "Find agents":"查找智能体",
  "No AI agents found on {where}.":"{where}上没有找到 AI 智能体。", "Use my own model":"用我自己的模型", "Also here: {names}":"这里还有：{names}", "Back to the agents":"回到智能体", "Your own model":"自己的模型",
  "the machine SuperAI runs on":"运行 SuperAI 的那台机器", "this {machine}":"这台{machine}", "SuperAI can work with Claude Code, Codex and the other AI agents you already use. May it look for them on {where}? It only checks what is installed there.":"SuperAI 可以和你已经在用的 Claude Code、Codex 等 AI 智能体一起工作。要在{where}上找找它们吗？只检查那里装了什么。", "Look for AI agents on {where} instead":"改为在{where}上查找 AI 智能体",
  "{count} models available; {model} picked.":"有 {count} 个模型可用，先选了 {model}。",
  "SuperAI has no model to think with yet.":"SuperAI 还没有能用的模型。", "Set up":"去设置", "First start":"第一次启动", "The password was printed once, when this SuperAI started: in the terminal window you started it from, or in its service log on a server.":"密码只在这个 SuperAI 第一次启动时打印过一次：在你启动它的那个终端窗口里；如果它是服务器上的服务，就在它的服务日志里。", "Lost it? Delete {file} and start SuperAI again; it prints a new one.":"找不到了？删掉 {file} 再启动一次 SuperAI，它会打印一个新密码。", "Use a model of my own instead":"改用我自己的模型", "Sign in to {name} and SuperAI can think with it, or give it a model of its own now.":"登录 {name} 后 SuperAI 就能用它思考；也可以现在给它配一个模型。", "SuperAI needs a model to think with. It takes a minute.":"SuperAI 需要一个模型来思考，一分钟就能配好。", "Choose a model":"选择模型", "How should SuperAI think?":"SuperAI 用什么思考？", "Pick one; you can change it any time in Settings.":"选一种，之后随时可以在设置里改。", "An API key":"API Key", "My AI account":"我的 AI 账号", "Service":"服务", "API key":"API Key", "Test to list the models":"点「测试」列出可用的模型", "Testing…":"测试中…", "Test":"测试", "{model} answered. Ready to save.":"{model} 有回应，可以保存了。", "{count} models available; pick one.":"有 {count} 个模型可用，选一个。", "Pick a model first.":"先选一个模型。", "That did not work.":"没成功。", "Save and start":"保存并开始", "Sign in with your Claude, ChatGPT or Gemini account in the browser, and SuperAI thinks with that subscription: no key to paste. Settings opens on the sign-in buttons.":"在浏览器里登录你的 Claude、ChatGPT 或 Gemini 账号，SuperAI 就用这份订阅思考，不用粘贴 Key。接下来会打开设置里的登录按钮。", "The sign-in runs on the machine this SuperAI runs on.":"登录会在运行这个 SuperAI 的那台机器上进行。", "Sign in with an account":"用账号登录", "Look for AI agents on this {machine} instead":"改为在这台{machine}上查找 AI 智能体", "Other":"其他",
  "Calendar":"日历", "Event":"日程", "Time":"时间", "Location":"地点", "Participants":"参会人", "Schedules":"日程与自动任务", "Notes":"笔记", "People":"联系人", "Schedules, notes, people and reminders SuperAI keeps for you. Edits happen via Chat.":"查看日程、笔记、联系人和提醒，通过对话修改记录。",
  "Task instructions":"任务指令",
  "Find your AI agents":"找到你的 AI 助手",
  "SuperAI can work with Claude Code, Codex and the other AI agents you already use. May it look for them on this {machine}? It only checks what is installed here.":"SuperAI 可以和你已经在用的 Claude Code、Codex 等 AI 助手一起工作。要在本机上找找看吗？只检查本机装了什么。",
  "Look for agents":"查找",
  "Not now":"暂不",
  "Looking…":"正在查找…",
  "Checking which AI agents are installed and signed in.":"正在检查装了哪些 AI 助手、是否已登录。",
  "Signed in":"已登录",
  "Not signed in":"未登录",
  "Use {name}?":"用 {name} 吗？",
  "SuperAI will think with {name}, on your own subscription: no key to paste, no model to pick. You can change this in Settings.":"SuperAI 会用 {name} 来思考，走你自己的订阅：不用填 key，也不用选模型。之后可以在设置里改。",
  "Use {name}":"使用 {name}",
  "Setting up…":"正在设置…",
  "Almost there":"差一步",
  "No AI agents found":"没有找到 AI 助手",
  "Sign in to {name} and SuperAI can think with it. Or choose a model in Settings.":"登录 {name} 后，SuperAI 就能用它思考。也可以在设置里选一个模型。",
  "To start, choose a model in Settings: sign in with your Claude, Codex or Gemini account, or add an API key.":"开始之前，请在设置里选一个模型：登录 Claude、Codex 或 Gemini 账号，或者填一个 API key。",
  "Continue":"继续",
  "+{count} more: {names}":"另有 {count} 个：{names}",
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
  "Home":"首页", "Chat":"对话", "The queen":"蜂后", "Hive":"蜂群", "Bees":"智能体", "Coding":"编程", "Knowledge":"知识库", "Skills":"技能", "MCP":"MCP", "Stats":"统计", "Records":"记录", "Workspace":"工作区", "System":"系统", "New chat":"新对话", "Later":"稍后", "Recents":"最近对话", "Pinned":"已固定", "Coming up":"近期安排", "Needs you":"待你处理", "Light":"浅色", "Dark":"深色", "Start a new worker pod?":"新建一个 worker pod？", "Take a worker pod away?":"删除一个 worker pod？", "One more pod joins the hive on the cluster. It takes about half a minute to start.":"集群上会多一个 pod 加入蜂群，大约半分钟启动好。", "The idlest worker leaves the hive and its pod is deleted. Work it is doing now is handed back.":"最空闲的 worker 离开蜂群，它的 pod 会被删除；它手上的活会交回来。", "Start a pod":"新建 pod", "Delete a pod":"删除 pod", "Switch backend":"切换后端", "Show the hive":"展开蜂群面板", "Hide this pane":"收起面板", "Resize sidebar":"调整侧边栏宽度", "Drag to resize · double-click to reset":"拖拽调整宽度 · 双击复位",
  "Clear all":"清空通知", "Nothing yet.":"暂无通知。", "Loading…":"正在加载…", "just now":"刚刚", "yesterday":"昨天", "{count} unread":"{count} 条未读通知", "{count}m ago":"{count} 分钟前", "{count}h ago":"{count} 小时前", "{count}d ago":"{count} 天前", "Today":"今天", "Tomorrow":"明天",
  "Refresh settings":"刷新设置", "Refresh by re-running the saved question.":"重新查询并更新看板内容", "View original question":"查看原始提问",
  "Dashboards":"看板", "All dashboards":"全部看板", "Full screen":"全屏", "Dashboard name":"看板名称", "Manual only":"仅手动刷新", "Every hour":"每小时", "Every day at 08:00":"每天 08:00", "Weekdays at 08:00":"工作日 08:00", "Mondays at 08:00":"每周一 08:00", "Open":"打开", "Delete":"删除", "Close":"关闭", "Close (Esc)":"关闭（Esc）", "Ask again and replace the contents":"重新提问并更新内容", "No saved question to re-ask":"没有保存用于刷新的问题", "Refreshes by re-asking":"刷新时重新提问", "Nothing saved yet.":"暂无已保存的看板。", "Saved without a question, so it cannot refresh itself — it stays as it was.":"没有保存原始问题，因此无法自动刷新，内容保持原样。", "A reply that draws a chart, a wall or a panel gets a save button under it.":"包含图表、数据看板或面板的回复下方会显示保存按钮。", "Save as dashboard":"保存为看板", "Save":"保存", "Cancel":"取消", "Name":"名称", "Name this dashboard":"为看板命名", "refreshing…":"正在刷新…", "data as of":"数据更新于",
  "the queen":"蜂后", "Ask {name}, or give the hive an order. @ for an agent or a worker":"向{name}提问或下达蜂群指令，输入 @ 选择智能体或工作节点", "Ask {name} anything, or give the hive an order: “check disk on every worker and tell me the fullest”.":"向{name}提任何问题，或给蜂群下指令：“检查每个工作节点的磁盘，告诉我哪个最满”。", "Talk to {name}…":"跟{name}说…", "Reply to {name}… @ for a worker or an agent":"回复{name}… 输入 @ 选择工作节点或智能体", "Tell {name}, from anywhere. @ for a worker or an agent":"随时告诉{name}，输入 @ 选择工作节点或智能体", "Orders you give below go to {name}; her answers show here.":"你在下方下达的指令交给{name}，回答显示在这里。",
  "Queen's name":"蜂后的名字", "Also answers to":"别名", "What the hive's main agent is called here, on the phone and in its own replies. @ with the name or an alias reaches it, never a worker.":"蜂后的名字：界面、手机和它自己的回答里都用这个名字。用 @名字 或 @别名 找的都是它，不会转给工作节点。", "Comma-separated, e.g. bee, 小蜂":"用逗号分隔，例如 bee, 小蜂",
  "Queen engine":"蜂后引擎", "Who answers when you talk to the queen. Calls, schedules and dashboards stay with SuperAI.":"你跟蜂后说话时由谁来回答。通话、定时任务和看板仍由 SuperAI 负责。", "Engine":"引擎", "Not reachable now":"现在连不上", "When it cannot answer":"引擎答不了时", "Proxy":"代理", "Environment":"环境变量", "Follow system":"跟随系统", "Add an agent":"添加 agent", "Act as queen":"作为蜂后", "New code":"换一个码", "Make a code":"生成配对码", "Expired":"已过期", "Run on the other machine · {m}:{s}":"在另一台机器上运行 · {m}:{s}", "Copied":"已复制", "Copy":"复制", "Manual":"手动", "direct":"直连", "Answers and live updates are not arriving; reconnecting.":"回答和实时更新收不到了，正在重连。", "Live updates off":"实时更新已断开", "SuperAI answered in the engine's place":"引擎答不了，SuperAI 回答的", "Queen":"蜂后", "In use: {engine}":"正在用：{engine}", "SuperAI answers, and says so":"由 SuperAI 回答并注明", "The turn fails":"这一条直接报错", "Public address":"公网地址", "Where the other machine reaches this SuperAI's /mcp.":"另一台机器访问本 SuperAI 的 /mcp 用的地址。",
  "Send":"发送", "Stop":"停止", "Thinking…":"正在思考…", "an agent on another machine can take a few minutes":"别的机器上的智能体可能要几分钟", "The hive in 3D":"3D 蜂群视图", "Menu":"菜单", "Menus and lists, the way it was":"菜单和列表，不显示 3D", "View":"视图", "Stopped.":"已停止。", "Trace":"调用记录", "Ask the queen, or give the hive an order. @ for an agent or a worker":"向蜂后提问或下达蜂群指令，输入 @ 选择智能体或工作节点", "This {machine} only":"仅本机", "Linked":"已连接", "Reconnecting":"正在重连", "Not in a hive":"未加入蜂群", "Finding the hive…":"正在查找蜂群…", "This {machine} on its own":"本机独立运行", "Conversation":"对话", "Untitled":"未命名", "Allow once":"允许一次", "Deny":"拒绝", "Open Bees":"打开智能体",
  // 日程安排 (views/ScheduleView.tsx, lib/schedule.ts)
  "Schedule":"日程安排", "Up next":"接下来的安排", "All":"全部", "Nothing planned this week":"这周没有安排", "Alarm":"闹钟", "Notification":"通知", "No alert":"不提醒", "How it reaches you":"提醒方式", "{start} on, every {n} h":"{start} 起 每{n}小时", "{start} on, every {n} min":"{start} 起 每{n}分钟", "{start}, {count} times":"{start} 等 {count} 次", "{n} d before":"提前{n}天", "{n} h before":"提前{n}小时", "{n} min before":"提前{n}分钟", "At the time":"准时", "The day after":"后天", "Yesterday":"昨天", "Change {title}":"修改：{title}", "{title}: {mode}":"{title}：{mode}", "Delete {title}":"删除：{title}", "Done: {title}":"已完成：{title}", "New {kind}: {title}":"新建{kind}：{title}", "Deleted {title}":"已删除：{title}", "Added {title}":"已添加：{title}", "Saved":"已保存", "Saving…":"正在保存…", "noted is not connected, so there is no schedule to show.":"还没有连上 noted，暂时没有日程可显示。", "Events, alarms, reminders and to-dos in one place. Each one can ring like an alarm, remind you, send a notification, or just sit here.":"日程、闹钟、提醒和待办都在这里。每一项都可以选：闹钟、提醒、通知，或者不提醒。", "Go to a date":"跳到某天", "New":"新建", "{n} more days":"再看 {n} 天", "Further on":"往后看", "{count} items through {date}":"到 {date} 共 {count} 项", "To-dos with no time":"没定时间的待办", "Mark done":"完成", "New on this day":"在这天新建", "Nothing planned":"没有安排", "On":"开", "Off":"已关闭", "Repeat":"重复", "Every":"每", "Days of the week":"星期", "Holidays":"节假日", "Delete it for good?":"确定删除？", "Delete it":"删除", "Keep":"保留", "Title":"标题", "Starts":"开始日期", "Date":"日期", "A repeating event is moved by telling the queen.":"重复日程要改时间，跟蜂后说一声。", "Rings today":"今天响铃", "Lead":"提前", "An alarm that is off keeps its settings and does not ring.":"关闭的闹钟会保留设置，但不会响。", "Every repeat goes with it. Delete?":"所有重复都会一起删除，确定？", "Give it a time to put it on the schedule.":"定个时间，它就会出现在日程里。", "Kind":"类型", "At a time":"定个时间", "Without a time it waits under To-dos with no time.":"不定时间的话，会放在“没定时间的待办”里。", "An alarm rings on the phone like an alarm clock, through silent mode, until stopped.":"闹钟会在手机上像闹钟一样响，静音也响，直到你关掉。", "A reminder is a notification on the phone at the time, with snooze.":"提醒会到点在手机上弹出通知，可以稍后再提醒。", "Add":"添加", "Or just say it: “wake me at 7 on workdays”, “remind me about the dentist on Friday at 3”":"或者直接说一句：“工作日早上7点叫我”“周五下午3点提醒我看牙”", "Tell the queen":"告诉蜂后", "The queen is on it…":"蜂后正在安排…", "Once":"仅一次", "Daily":"每天", "Weekly":"每周", "Monthly":"每月", "Hourly":"每小时", "Every few minutes":"每几分钟", "days":"天", "weeks":"周", "months":"个月", "hours":"小时", "minutes":"分钟", "Every time":"照常", "Skip holidays":"节假日不响", "Workdays only":"仅工作日", "Follow make-up workdays":"按调休", "e.g. Dentist":"比如：看牙", "e.g. Get up":"比如：起床", "e.g. Take the medicine":"比如：吃药", "e.g. Buy milk":"比如：买牛奶",
  "Timing":"计时中", "All day":"全天", "To-do":"待办", "{count} left today":"今天还有 {count} 项", "Nothing else today":"今天没有别的安排了", "Nothing else planned today.":"今天没有别的安排了。", "To-dos":"待办", "Alarms":"闹钟", "Investments":"投资", "Ask the queen about it":"问问蜂后", "Tell noted something…":"让 noted 记点什么…", "Still to come":"还有",
  "Load":"负载", "{n} CPUs":"{n} 核", "RAM":"内存", "Disk":"磁盘", "free":"可用", "Up {time}":"已运行 {time}", "Services":"服务", "Command":"命令", "Reading the machine…":"正在读取机器状态…", "Reading services…":"正在读取服务…", "No services.":"没有服务。", "Log":"日志", "Restart":"重启", "Restart it?":"确定重启？", "A shell command on {name}":"在 {name} 上执行的命令", "Running…":"正在执行…", "exit {code}":"退出码 {code}", "The log is empty.":"日志为空。", "This machine's owner turned off commands and service actions.":"这台机器的主人关闭了命令和服务操作。",
  "Talk to {name}":"和{name}对话", "Choose figures":"选择指标", "Working now":"运行中", "Move up":"上移", "Move down":"下移",
  "tokens today":"今日 tokens", "tokens all time":"累计 tokens", "tokens this process":"本进程 tokens", "cached":"缓存", "turns":"轮次", "tool calls":"工具调用", "reads":"读取", "writes":"写入", "shells":"命令", "mcp calls":"MCP 调用", "memory calls":"记忆调用", "failed":"失败", "cpu":"CPU", "heap":"堆内存", "goroutines":"协程", "orders in flight":"进行中的指令", "coding runs going":"进行中的编程",
  "{count} sessions working":"{count} 个会话在干活", "1 session working":"1 个会话在干活", "1 agent ready":"1 个智能体就绪", "HA node":"HA 节点", "runs {names}":"运行 {names}", "standby for {names}":"{names} 的备用", "standby {names}":"备用 {names}", "reached over ssh":"经 ssh 访问", "not ready":"未就绪", "Known from":"来源", "haify (HA node)":"haify（HA 节点）", "Address":"地址", "Addresses":"地址", "Doing":"在做", "waiting for you":"等你", "waiting for your OK":"等你批准", "thinking":"思考中", "idle":"空闲",
  "Terminal sessions":"终端会话", "by its processes":"按进程判断", "working":"干活中", "waiting":"等你", "since {time}":"自 {time}", "{count} tool calls":"{count} 次工具调用",
  "VM":"虚拟机", "Pod":"Pod", "Container":"容器", "Mac":"Mac", "PC":"电脑", "computer":"电脑", "Bare metal":"物理机", "Host":"主机", "Hosts":"主机", "on {name}":"在 {name} 上", "this core":"本核心", "needs your OK":"等你批准", "not answering":"没有响应", "not running":"没在运行", "configured":"已配置", "{count} agents ready":"{count} 个智能体就绪", "not linked":"未连接", "1 pod":"1 个 Pod", "{count} pods":"{count} 个 Pod", "1 agent":"1 个智能体", "{count} agents":"{count} 个智能体",
  "shared brain":"共享大脑", "node":"节点", "a worker is not answering":"有工作节点没回应", "{count} orders running":"{count} 条指令在执行", "1 order running":"1 条指令在执行", "ready":"就绪", "out now":"外出中", "paused":"已暂停", "next {at}":"下次 {at}", "resting":"休息中", "hive tokens":"蜂群 Token", "thinking…":"思考中…", "say something below":"在下面说点什么", "orders out":"派出的指令", "{count} installed":"已装 {count} 个", "none yet — click to add":"还没有，点这里添加", "not connected":"没连上", "{ok}/{all} connected":"已连 {ok}/{all}", "workers":"工作节点", "need you":"等你处理", "rounds":"轮次", "failures":"失败", "peak":"峰值", "tokens, lately":"最近的 Token", "tokens, lately — the line starts with the next work":"最近的 Token，下次干活时开始画线",
  "Asks":"请求", "Connected":"已连接", "Detail":"详情", "Failures":"失败", "Hive tokens":"蜂群 Token", "Next":"下次", "Reports":"报告", "Rounds":"轮次", "Sessions":"会话", "Store":"存储", "To":"发给", "Tokens":"Token", "Tool":"工具", "Tool calls":"工具调用", "Tools":"工具", "Updated":"更新于", "Watches":"盯着", "When":"时间", "Where":"位置", "Workers":"工作节点", "Orders out":"派出的指令", "What should {name} do?":"让 {name} 做什么？",
  "standby":"备用", "standby · not ready":"备用 · 未就绪", "active":"运行中", "degraded":"降级", "down":"已停止", "live":"在线", "lost":"失联", "installed":"已安装", "worker":"工作节点", "machine agent":"机器智能体", "relay":"中继", "on its own":"独立运行",
  "This host is no longer listed.":"这台主机已不在列表中。", "Hostname":"主机名", "Inside":"所在", "State":"状态", "Last seen":"最后在线", "HA services":"高可用服务", "HA service":"高可用服务", "This service is no longer listed.":"这个服务已不在列表中。", "Service IP":"服务 IP", "Runs on":"运行于", "Kept up by":"保障方", "Units":"单元", "Live":"在线", "Not answering":"没有响应", "Only in the settings":"仅在设置中", "Running":"运行中", "Running, no standby ready":"运行中，没有就绪的备用", "Down":"已停止", "Coding CLIs":"编程 CLI", "via {name}":"经由 {name}", "Nothing runs here that this core knows of.":"本核心不知道这里运行着什么。", "standby, ready":"备用，已就绪", "standby, not ready":"备用，未就绪", "No hosts.":"没有主机。", "Pods":"Pod",
  "today":"今日", "all time":"累计", "tokens":"tokens", "calls":"调用", "mcp":"MCP", "memory":"记忆", "orders":"指令", "coding":"编程", "workers busy":"忙碌的工作节点", "failed today":"今日失败",
};
// English is the keys; the other languages are looked up, and a string a
// language has no translation for yet stays English.
const dicts: Partial<Record<Language, Record<string, string>>> = { "zh-CN": zh, de, fr, es, ja, ko };
export function translate(key:string, values:Record<string,string|number> = {}) {
  let value = dicts[language]?.[key] ?? key;
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
