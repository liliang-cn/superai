// 日程安排's pure logic: grouping, ring summaries, mode labels and the
// CallTool calls an edit turns into.
process.env.TZ = 'Asia/Shanghai';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const root = path.resolve(__dirname, '..');
const cache = new Map();
const stored = new Map();
const document = {documentElement: {}};
function load(file) {
  const full = path.resolve(root, file);
  if (cache.has(full)) return cache.get(full).exports;
  const module = {exports: {}};
  cache.set(full, module);
  const resolve = (name) => {
    if (!name.startsWith('.')) return require(name);
    const base = path.resolve(path.dirname(full), name);
    return load(path.relative(root, [base + '.ts', base + '.tsx', base].find((p) => fs.existsSync(p))));
  };
  const out = ts.transpileModule(fs.readFileSync(full, 'utf8'), {compilerOptions: {target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS}}).outputText;
  vm.runInNewContext(out, {module, exports: module.exports, require: resolve, window: {addEventListener() {}}, document,
    localStorage: {getItem: (k) => stored.get(k) ?? null, setItem: (k, v) => stored.set(k, v)}, Date, Intl, console}, {filename: full});
  return module.exports;
}
const i18n = load('src/lib/i18n.ts');
const s = load('src/lib/schedule.ts');
// Values made inside the vm have its prototypes; compare them as data.
const same = (a, b) => assert.deepStrictEqual(JSON.parse(JSON.stringify(a)), b);
const zh = (fn) => { i18n.setLanguage('zh-CN'); try { return fn(); } finally { i18n.setLanguage('en'); } };

const item = (o) => ({kind: 'event', source: 'event', id: 'e1', title: 'Stand-up', startTime: '2026-10-18T09:30:00+08:00', ...o});

test('mode labels are the same four marks, and an unknown or missing mode reads safely', () => {
  same(zh(() => s.NOTIFY_MODES.map(s.notifyLabel)), ['闹钟', '提醒', '通知', '不提醒']);
  assert.equal(s.notifyOf({notify: 'alarm'}), 'alarm');
  assert.equal(s.notifyOf({notify: 'shout'}), 'reminder');
  assert.equal(s.notifyOf({remindBeforeMinutes: 0}), 'reminder');
  assert.equal(s.notifyOf({}), 'none');
});

test('days are grouped from the first day, all-day first, then by time, empty days kept', () => {
  const items = [
    item({id: 'b', startTime: '2026-10-19T15:00:00+08:00'}),
    item({id: 'a', startTime: '2026-10-19T08:00:00+08:00'}),
    item({id: 'all', allDay: true, startTime: '2026-10-19T00:00:00+08:00'}),
    item({id: 'early', startTime: '2026-10-17T22:00:00+08:00'}),
    item({id: 'late', startTime: '2026-10-25T08:00:00+08:00'}),
  ];
  const days = s.groupByDay(items, '2026-10-18', 3);
  same(days.map((d) => d.date), ['2026-10-18', '2026-10-19', '2026-10-20']);
  same(days[0].items.map((i) => i.id), ['early']);
  same(days[1].items.map((i) => i.id), ['all', 'a', 'b']);
  assert.equal(days[2].items.length, 0);
  assert.equal(s.addDays('2026-10-31', 1), '2026-11-01');
});

test('rings on one day are summarised', () => {
  assert.equal(s.ringSummary(['07:00']), '');
  assert.equal(zh(() => s.ringSummary(['07:00', '07:10', '07:20', '07:30'])), '07:00 起 每10分钟');
  assert.equal(zh(() => s.ringSummary(['09:00', '10:00', '11:00'])), '09:00 起 每1小时');
  assert.equal(zh(() => s.ringSummary(['08:00', '12:00', '18:00'])), '08:00 等 3 次');
  assert.equal(s.ringSummary(['07:00', '07:05']), '07:00 on, every 5 min');
});

test('time column and repeat wording', () => {
  assert.equal(s.timeLabel(item({})), '09:30');
  assert.equal(zh(() => s.timeLabel(item({allDay: true}))), '全天');
  const weekday = item({source: 'alarm', schedule: {kind: 'recurring', anchor: '2026-10-01', time: '07:00', recurrence: {freq: 'daily'}}, holidayPolicy: 'workdayOnly'});
  assert.equal(zh(() => s.repeatLabel(weekday)), '每天 · 仅工作日');
  assert.equal(zh(() => s.repeatLabel(item({rrule: 'FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE'}))), '每2周 周一、三');
  assert.equal(zh(() => s.leadLabel({notify: 'reminder', remindBeforeMinutes: 10})), '提前10分钟');
  assert.equal(s.leadLabel({notify: 'none', remindBeforeMinutes: 10}), '');
});

test('changing only the mode is one update_event', () => {
  same(s.modeCall(item({}), 'alarm').args, {id: 'e1', notify: 'alarm'});
  assert.equal(s.modeCall(item({}), 'alarm').tool, 'update_event');
  same(s.taskModeCall({id: 't1', title: 'x'}, 'none'), {tool: 'update_task', args: {id: 't1', notify: 'none'}, label: 'x: No alert'});
});

test('an edit becomes update_event for time, mode and lead; nothing changed is no call', () => {
  const ev = item({notify: 'reminder', remindBeforeMinutes: 10});
  same(s.editCalls(ev, s.draftOf(ev)), []);
  const d = {...s.draftOf(ev), title: 'Stand-up!', time: '10:00', notify: 'notification', lead: 5};
  const calls = s.editCalls(ev, d);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].tool, 'update_event');
  same(calls[0].args, {id: 'e1', title: 'Stand-up!', notify: 'notification', remind_before_minutes: 5, start: '2026-10-18T10:00:00+08:00'});
});

test('an alarm edit puts schedule, holidays and on/off through update_alarm', () => {
  const al = item({source: 'alarm', id: 'a1', title: '起床', notify: 'alarm', startTime: '2026-10-18T07:00:00+08:00',
    schedule: {kind: 'recurring', anchor: '2026-10-01', time: '07:00', timeZone: 'Asia/Shanghai', recurrence: {freq: 'daily', until: '2026-12-31'}}, holidayPolicy: 'none'});
  const base = s.draftOf(al);
  assert.equal(base.time, '07:00');
  assert.equal(base.date, '2026-10-01');
  const calls = s.editCalls(al, {...base, time: '06:45', holidayPolicy: 'workdayOnly', enabled: false});
  assert.equal(calls.length, 1);
  assert.equal(calls[0].tool, 'update_alarm');
  same(calls[0].args, {id: 'a1', holiday_policy: 'workdayOnly', enabled: false,
    schedule: {kind: 'recurring', anchor: '2026-10-01', time: '06:45', timeZone: 'Asia/Shanghai', recurrence: {freq: 'daily', until: '2026-12-31'}}});
  const weekly = s.editCalls(al, {...base, freq: 'weekly', byDay: ['MON', 'FRI'], notify: 'reminder'});
  same(weekly.map((c) => c.tool), ['update_event', 'update_alarm']);
  same(weekly[0].args, {id: 'a1', notify: 'reminder'});
  same(weekly[1].args.schedule.recurrence, {freq: 'weekly', byDay: ['MON', 'FRI']});
  const once = s.editCalls(al, {...base, freq: 'once', date: '2026-10-20'});
  same(once[0].args.schedule, {kind: 'oneShot', at: '2026-10-20T07:00:00+08:00'});
});

test('a repeating calendar event is not moved from the panel', () => {
  const ev = item({rrule: 'FREQ=DAILY'});
  assert.equal(s.timeEditable(ev), false);
  same(s.editCalls(ev, {...s.draftOf(ev), time: '11:00'}), []);
});

test('new 日程 / 闹钟 / 提醒 / 待办 build the contract calls', () => {
  const now = new Date('2026-10-18T08:20:00+08:00');
  const ev = s.createCall('event', {...s.newDraft('event', '2026-10-18', now), title: '看牙', location: '口腔医院'});
  same(ev, {tool: 'create_event', label: 'New Event: 看牙',
    args: {title: '看牙', start: '2026-10-18T09:00:00+08:00', notify: 'reminder', location: '口腔医院', remind_before_minutes: 10}});
  const allDay = s.createCall('event', {...s.newDraft('event', '2026-10-20', now), title: '生日', allDay: true, notify: 'none'});
  same(allDay.args, {title: '生日', start: '2026-10-20T00:00:00+08:00', notify: 'none', all_day: true});
  const alarm = s.createCall('alarm', {...s.newDraft('alarm', '2026-10-19', now), title: '起床', freq: 'daily', holidayPolicy: 'workdayOnly'});
  same(alarm.args, {label: '起床', notify: 'alarm', holiday_policy: 'workdayOnly',
    schedule: {kind: 'recurring', anchor: '2026-10-19', time: '07:00', recurrence: {freq: 'daily'}}});
  const rem = s.createCall('reminder', {...s.newDraft('reminder', '2026-10-19', now), title: '吃药', time: '20:00'});
  same(rem, {tool: 'create_alarm', label: 'New Reminder: 吃药', args: {label: '吃药', notify: 'reminder', schedule: {kind: 'oneShot', at: '2026-10-19T20:00:00+08:00'}}});
  same(s.createCall('todo', {...s.newDraft('todo', '2026-10-18', now), title: '买牛奶', time: ''}).args, {title: '买牛奶'});
  const timed = s.createCall('todo', {...s.newDraft('todo', '2026-10-18', now), title: '交报告', time: '17:00', notify: 'reminder', lead: 30});
  same(timed.args, {title: '交报告', due: '2026-10-18T17:00:00+08:00', notify: 'reminder', remind_at: '2026-10-18T16:30:00+08:00'});
  const weekly = s.createCall('event', {...s.newDraft('event', '2026-10-19', now), title: '周会', freq: 'weekly', byDay: ['MON']});
  assert.equal(weekly.args.rrule, 'FREQ=WEEKLY;BYDAY=MO');
});

test('delete asks for confirmation and picks the right tool; to-dos complete and reschedule', () => {
  same(s.deleteCall(item({source: 'alarm', id: 'a1'})), {tool: 'delete_alarm', args: {id: 'a1'}, label: 'Delete Stand-up', confirmed: true});
  assert.equal(s.deleteCall(item({kind: 'task', source: 'task'})).tool, 'delete_event');
  same(s.completeCall('t1', 'x').args, {id: 't1'});
  same(s.taskEditCalls({id: 't1', title: 'x'}, {title: 'x', notify: 'none', date: '2026-10-18', time: ''}), []);
  same(s.taskEditCalls({id: 't1', title: 'x'}, {title: 'y', notify: 'alarm', date: '2026-10-18', time: '08:00'})[0].args,
    {id: 't1', title: 'y', notify: 'alarm', due: '2026-10-18T08:00:00+08:00'});
});

test('an older noted answer still reads', () => {
  const r = s.readSchedule({events: [{id: 'x', title: 'Old', startTime: '2026-10-18T09:00:00+08:00', remindBeforeMinutes: 15}, {nope: 1}]});
  assert.equal(r.items.length, 1);
  assert.equal(s.notifyOf(r.items[0]), 'reminder');
  same(r.unscheduled, []);
  same(s.readSchedule(null), {items: [], unscheduled: []});
});

test('deeplinks to the schedule open 日程安排', () => {
  const routes = load('src/lib/routes.ts');
  assert.equal(routes.deeplinkPath('superai://schedule'), '/schedule');
  assert.equal(routes.deeplinkPath('noted://alarm/a1'), '/schedule');
  assert.equal(routes.deeplinkPath('https://example.com'), '');
  assert.equal(routes.viewOf('/schedule'), 'schedule');
});
