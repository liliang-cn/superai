package backend

// noted is where the person's own data lives: to-dos, calendar, notes,
// projects, goals and objectives, time logging and investment holdings. It is
// an MCP server like any other (mcpServers.json, usually remote over HTTP with
// "Authorization": "Bearer ${NOTED_TOKEN}"), and SuperAI draws its answers —
// in chat with card / timeline / stat blocks, on the home screen through the
// Noted* RPCs.

// NotedServer is the name noted is configured under in mcpServers.json.
const NotedServer = "noted"

// notedSection tells the model that personal data lives in noted when it is
// connected. It comes after the persona on purpose: the persona's own line
// sends plans and notes to add_schedule / add_record, and with noted present
// that store is not where the person looks for them any more.
func notedSection(configured bool) string {
	if !configured {
		return ""
	}
	return `

The person's own data lives in noted (tools mcp_noted_*): to-dos, calendar events, notes, projects, goals and objectives, time logging (timers, log_time, time_summary) and investment holdings. For any of these use the noted tools — instead of add_schedule / add_record, and never from memory: a plan or appointment→create_event, a to-do→create_task, a note→create_note, "what's on"→get_overview. Work relative dates out from the "Now:" line of the turn and pass RFC 3339 with the offset. Look ids up with the list_* tools, never guess one. Show what noted returned with card / timeline / stat blocks (ui_block_rules has the format): copy each item's id and noted:// link from the result, and add a button for an obvious next step (complete, start a timer) that calls the noted tool directly.` + notedAlarmsSection
}

// notedAlarmsSection is the part about alarms (闹钟). They live in noted and
// the phone rings them with AlarmKit, so they replace the old SmartAlarm app.
// The person's own standing rules (who is fetched when) are in memory, not
// here: this only makes sure the model can write any of them down.
const notedAlarmsSection = `

Alarms (闹钟) are noted alarms; the phone rings them at the exact time even when silenced. 定个闹钟 / 叫我 / 准时响 / 必须提醒到 → create_alarm with mode "alarm". A plain reminder (提醒我…, only a notification) → create_alarm with mode "reminder", or a reminder on the event when it belongs to one. Resolve every relative date with resolve_datetime first — noted resolves nothing relative. schedule is either {"kind":"oneShot","at":"<RFC 3339>","timeZone":"Asia/Shanghai"} or {"kind":"recurring","anchor":"YYYY-MM-DD","time":"HH:MM","timeZone":"Asia/Shanghai","recurrence":{"freq":"daily"|"weekly"|"monthly"|"hourly"|"minutely","interval":N,"byDay":["MON",…],"byMonthDay":D,"byTime":["HH:MM",…],"until":"YYYY-MM-DD","count":N}}. anchor is the first day it may ring. Every other week (隔周 / 单双周) is weekly with interval 2: it rings in the anchor's week and every second week after. A rule that alternates by week is one alarm per part — the on-week part anchored in a week it rings, the off-week part anchored one week later — and days that are the same every week are a plain weekly alarm (interval 1) with byDay listing them; e.g. "隔周周一 15:20, other workdays 17:15" is Monday 15:20 interval 2, Monday 17:15 interval 2 anchored a week later, and TUE-FRI 17:15 interval 1, each with holiday_policy workdayOnly. holiday_policy: "none", "skipHoliday" (节假日不响), "workdayOnly" (仅工作日, follows 调休), "followStatutory"; get_holidays has the calendar. If the person has standing schedule rules (pickups, rotations), recall them from memory rather than asking again. To change one, get_alarm, edit, update_alarm (schedule is replaced whole); to switch one off, update_alarm with enabled false. After creating or changing an alarm show a card: the label as title, the schedule in words as subtitle (e.g. 隔周周一 15:20，节假日不响), a field with the first of nextFires as a wall-clock time in its timeZone (or 不再响 when nextFires is empty), its noted://alarm link, and actions 关闭 ({"call":{"server":"noted","tool":"update_alarm","args":{"id":"<id>","enabled":false}}}) and 删除 ({"call":{"server":"noted","tool":"delete_alarm","args":{"id":"<id>"}},"confirm":"删除闹钟「<label>」？","style":"destructive"}).`
