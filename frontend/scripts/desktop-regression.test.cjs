// Run the real TypeScript helpers and rendered screens without a live account.
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const ts = require('typescript');
const React = require('react');
const {renderToStaticMarkup} = require('react-dom/server');
const {MemoryRouter} = require('react-router-dom');
const root = path.resolve(__dirname,'..');
const cache = new Map();
const stored = new Map();
const fakeWindow = {addEventListener(){},removeEventListener(){},go:{app:{App:{}}},runtime:{}};
const document = {documentElement:{},activeElement:null};
function load(file) {
  const full = path.resolve(root,file);
  if (cache.has(full)) return cache.get(full).exports;
  const module = {exports:{}}; cache.set(full,module);
  function resolve(name) {
    if (name.endsWith('.css')) return {};
    if (!name.startsWith('.') && !name.startsWith('@/')) return require(name);
    const base = name.startsWith('@/') ? path.resolve(root,'src',name.slice(2)) : path.resolve(path.dirname(full),name);
    const target = [base,base+'.ts',base+'.tsx',base+'.js',path.join(base,'index.ts'),path.join(base,'runtime.js')].find(p=>fs.existsSync(p)&&fs.statSync(p).isFile());
    if (!target) throw new Error('Cannot resolve '+name+' from '+full);
    return load(path.relative(root,target));
  }
  const compiled = ts.transpileModule(fs.readFileSync(full,'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2020,module:ts.ModuleKind.CommonJS,jsx:ts.JsxEmit.ReactJSX}}).outputText;
  vm.runInNewContext(compiled,{module,exports:module.exports,require:resolve,React,window:fakeWindow,document,navigator:{language:'en-US'},localStorage:{getItem:k=>stored.get(k)??null,setItem:(k,v)=>stored.set(k,v)},Date,Set,Map,setTimeout,clearTimeout,setInterval,clearInterval,console},{filename:full});
  return module.exports;
}
const {summarizeAttention} = load('src/lib/attentionSummary.ts');
const {attentionPath,viewOf} = load('src/lib/routes.ts');
const i18n = load('src/lib/i18n.ts');
const {whenOf} = load('src/canvas/attention.tsx');

test('repeated reports keep newest update and each independent agent, without swallowing approvals',()=>{
 const input = [
  {kind:'report',ref:'bee-a',title:'old',at:'2026-10-03T08:00:00Z'},
  {kind:'approval',ref:'ask-1',title:'Approve me'},
  {kind:'report',ref:'bee-b',title:'different issue',at:'2026-10-03T09:00:00Z'},
  {kind:'report',ref:'bee-a',title:'new',at:'2026-10-03T10:00:00Z'},
  {kind:'report',title:'unknown owner'},
  {kind:'failed',ref:'failed-1'},
 ];
 const result=summarizeAttention(input);
 assert.equal(result.length,5);assert.equal(result[0].title,'new');assert.equal(result[0].updates,2);
 assert.equal(result[1].ref,'ask-1');assert.equal(result[2].ref,'bee-b');assert.equal(input[0].title,'old');assert.equal(input[0].updates,undefined);
});
test('attention links preserve target identity and reminder tab',()=>{
 assert.equal(attentionPath({kind:'report',open:'agents',ref:'a/b'}),'/agents?agent=a%2Fb');
 assert.equal(attentionPath({kind:'run',open:'coding',ref:'run 1'}),'/coding?run=run%201');
 assert.equal(attentionPath({kind:'failed',open:'hive',ref:'failed-1'}),'/hive/tasks/failed-1');
 assert.equal(attentionPath({kind:'reminder',open:'records'}),'/records?tab=reminders');
 assert.equal(viewOf('/mcp'),'mcp');
});
test('language changes persist and preserve user-written content',()=>{
 i18n.setLanguage('zh-CN');assert.equal(i18n.translate('Extensions'),'扩展');assert.equal(i18n.translate('{count} updates · latest shown',{count:3}),'共 3 条更新 · 显示最新一条');
 assert.equal(stored.get('superleo-language'),'zh-CN');assert.equal(document.documentElement.lang,'zh-CN');assert.equal(i18n.translate('A user report'),'A user report');
 i18n.setLanguage('en');assert.equal(i18n.translate('Extensions'),'Extensions');
});
test('relative reminder dates cross daylight saving correctly and reject invalid timestamps',()=>{
 const original=process.env.TZ;process.env.TZ='America/New_York';i18n.setLanguage('en');
 try {
  assert.match(whenOf({level:'soon',kind:'reminder',at:'2026-03-09T00:30:00-04:00'},new Date('2026-03-08T08:00:00-04:00')),/^Tomorrow /);
  assert.equal(whenOf({level:'soon',at:'invalid'}),'');
 } finally {if(original===undefined)delete process.env.TZ;else process.env.TZ=original;}
});
test('extensions renders one selected tab and the matching management view',()=>{
 const Extensions=load('src/views/ExtensionsView.tsx').default;
 for(const route of ['/skills','/mcp']) {
  const html=renderToStaticMarkup(React.createElement(MemoryRouter,{initialEntries:[route]},React.createElement(Extensions)));
  assert.equal((html.match(/aria-selected="true"/g)||[]).length,1);
  assert.match(html,new RegExp('aria-labelledby="extension-tab-'+(route==='/skills'?'skills':'mcp')+'"'));
  assert.match(html,/role="tabpanel"/);
 }
});

test('calendar agenda renders stored meetings with time, location and participants',()=>{
 i18n.setLanguage('zh-CN');
 const Agenda=load('src/views/RecordsView.tsx').CalendarAgenda;
 const html=renderToStaticMarkup(React.createElement(Agenda,{items:[{id:'meeting',title:'与 Rene 的会议',start_at:'2026-10-06T09:00:00+02:00',location:'Zoom',participants:['Rene']}]}));
 assert.match(html,/与 Rene 的会议/);assert.match(html,/Zoom/);assert.match(html,/参会人/);assert.match(html,/datetime="2026-10-06T09:00:00\+02:00"/i);
 i18n.setLanguage('en');
});

test('status bar distinguishes connecting, not-ready and active coding runs',()=>{
 i18n.setLanguage('en');
 const Status=load('src/desk/DeskStatusBar.tsx').default;
 const ready={ready:true,error:'',skills:[],memoryMode:'file',mcp:2,mcpTools:103,avatarPort:0,scheduler:true,schedulerError:''};
 const running=renderToStaticMarkup(React.createElement(Status,{status:ready,loading:false,codingRuns:2}));
 assert.match(running,/2 running/);assert.match(running,/103 tools/);assert.match(running,/aria-label="Status bar"/);
 const connecting=renderToStaticMarkup(React.createElement(Status,{status:null,loading:true,codingRuns:0}));assert.match(connecting,/Connecting…/);
 const unavailable=renderToStaticMarkup(React.createElement(Status,{status:{...ready,ready:false,error:'test unavailable'},loading:false,codingRuns:0}));assert.match(unavailable,/Not ready/);assert.match(unavailable,/test unavailable/);
});

test('human-attention screen provides approval decisions and reminders without task checkboxes',()=>{
 i18n.setLanguage('zh-CN');
 const Tasks=load('src/views/TasksView.tsx').default;
 const props={attention:{items:[
  {kind:'approval',level:'needs',title:'QA asks to run something',ref:'ask',open:'approval'},
  {kind:'report',level:'needs',title:'QA watch',ref:'bee',open:'agents',detail:'Review report',updates:4},
  {kind:'reminder',level:'soon',title:'QA reminder',ref:'reminder',open:'records',at:'2026-10-06T09:00:00+08:00'},
 ],loading:false,error:'',refresh(){}},approvals:[{id:'ask',command:'printf qa',session:'qa',tool:'Bash',args:{},by:'QA',expiresAt:''}],onResolve(){},onOpenConversation(){}};
 const html=renderToStaticMarkup(React.createElement(MemoryRouter,{initialEntries:['/tasks']},React.createElement(Tasks,props)));
 assert.match(html,/允许一次/);assert.match(html,/拒绝/);assert.match(html,/QA reminder/);assert.match(html,/查看提醒/);assert.match(html,/共 4 条更新/);assert.doesNotMatch(html,/type="checkbox"/);
 i18n.setLanguage('en');
});

test('calendar aliases and timezone-equivalent meeting times do not duplicate appointments',()=>{
 const {calendarEvents}=load('src/lib/calendarEvents.ts');
 const meetings=[
  {id:'a',title:'与 Rene 的会议',start_at:'2026-10-06T09:00:00+02:00',participants:['Rene']},
  {id:'b',title:'和 Rene 开会',start_at:'2026-10-06T15:00:00+08:00',participants:['Rene']},
  {id:'c',title:'Different meeting',start_at:'2026-10-06T15:00:00+08:00',participants:['Alice']},
  {id:'d',title:'Legacy entry',start_at:'next week'},
 ];
 const result=calendarEvents(meetings);assert.equal(result.length,3);assert.equal(result[0].id,'a');assert.equal(result[1].id,'c');assert.equal(meetings.length,4);
 assert.equal(calendarEvents([{title:'A',start_at:'2026-10-06T09:00:00Z'},{title:'B',start_at:'2026-10-06T09:00:00Z'}]).length,2);
});

test('one queen conversation on every device; the old per-device consoles are listed by device',async()=>{
 const qc=load('src/lib/queenConsole.ts');
 // A core without QueenConsole (and a window whose bindings lack it): the shared constant.
 assert.equal(await qc.loadQueenSession(true),'hive-console');assert.equal(qc.queenSession(),'hive-console');
 fakeWindow.go.app.App.QueenConsole=async()=>({session:'hive-console-shared-x'});
 assert.equal(await qc.loadQueenSession(true),'hive-console-shared-x');
 fakeWindow.go.app.App.QueenConsole=async()=>{throw new Error('404 no such method');};
 assert.equal(await qc.loadQueenSession(true),'hive-console');
 delete fakeWindow.go.app.App.QueenConsole;
 const devices=[{id:'d4388704550c2bd9',name:'iPad'},{id:'p1',name:'iPhone 17'}];
 assert.equal(qc.legacyConsoleTitle('hive-console-d4388704550c2bd9',devices),'蜂群指令 · iPad');
 assert.equal(qc.legacyConsoleTitle('hive-console-web',devices),'蜂群指令 · 网页');
 assert.equal(qc.legacyConsoleTitle('hive-console-gone',devices),'蜂群指令 · 旧');
 const keep=['abc','hive-console-web','hive-console-p1'];
 const hide=['hive-console','hive:order-1','hive-mail:w1','hive-order-1','tile-k2','dashboard:x'];
 for (const id of keep) assert.ok(qc.isPersonalConversation({id,title:'t'},'hive-console'),id);
 for (const id of hide) assert.ok(!qc.isPersonalConversation({id,title:'t'},'hive-console'),id);
 assert.ok(!qc.isPersonalConversation({id:'u1',title:'You hold a standing responsibility: x'},'hive-console'));
 fakeWindow.go.app.App.PairedDevices=async()=>devices;
 const titled=await qc.withConsoleTitles([{id:'abc',title:'hi'},{id:'hive-console-p1',title:'old words'}]);
 assert.deepEqual(titled.map(s=>s.title),['hi','蜂群指令 · iPhone 17']);
 delete fakeWindow.go.app.App.PairedDevices;
});
test('world embed takes partial host data and rejects what is not a message',()=>{
 const {normalize,parse}=load('src/world-embed/protocol.ts');
 assert.equal(parse('not json'),null);assert.equal(parse({no:'type'}),null);assert.equal(parse('{"type":"camera","op":"zoomIn"}').op,'zoomIn');
 const empty=normalize(undefined);assert.equal(empty.hive.members.length,0);assert.equal(empty.meter.total.tokens,0);assert.equal(empty.busy,false);
 const d=normalize({hive:{members:[{name:'w1',state:'live'}],tasks:[{id:'t',worker:'w1',prompt:'p',state:'running'}]},linked:[{name:'mac',os:'darwin'}],meter:{members:[{name:'queen',tokens:5,live:true},{name:'w1',tokens:7}]}});
 assert.equal(d.hive.tasks[0].tools,0);assert.equal(d.linked[0].clis.length,0);assert.equal(d.meter.total.tokens,12);assert.equal(d.meter.total.live,true);
});
