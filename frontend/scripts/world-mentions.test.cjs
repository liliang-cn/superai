// The 3D world's command bar: whom @ offers, from the hosts the world draws.
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const src = fs.readFileSync(path.resolve(__dirname, '../src/world/mentions.ts'), 'utf8');
const out = ts.transpileModule(src, {compilerOptions: {target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS}}).outputText;
const mod = {exports: {}};
new Function('module', 'exports', 'require', out)(mod, mod.exports, () => ({}));
const {mentionsOf} = mod.exports;

const host = (name, agents) => ({id: name, name, kind: 'machine', state: 'live', agents});

// "This Mac only": the core on the Mac with its CLIs, named agents on ssh hosts.
const mac = [
  host('Mac', [
    {name: 'core', kind: 'superleo', role: 'standalone', state: 'live'},
    {name: 'codex.core', kind: 'cli', state: 'idle', via: 'core'},
    {name: 'claude.core', kind: 'cli', state: 'working', via: 'core'},
  ]),
  host('sds-e', [{name: 'openclaw', kind: 'agent', state: 'configured', about: 'OpenClaw gateway'}]),
  host('sds-a', [{name: 'hermes', kind: 'agent', state: 'configured'}, {name: 'pi', kind: 'agent', state: 'configured'}]),
];

test('alone on a Mac, @ offers every worker the status bar counts, and not the core', () => {
  const m = mentionsOf(mac);
  assert.deepEqual(m.map((a) => a.name).sort(), ['claude', 'codex', 'hermes', 'openclaw', 'pi']);
  assert.match(m.find((a) => a.name === 'claude').about, /working/);
  assert.match(m.find((a) => a.name === 'openclaw').about, /OpenClaw gateway/);
});

test('a CLI on another machine keeps its machine, and is found by its bare name', () => {
  const m = mentionsOf([...mac, host('sds-b', [
    {name: 'sds-b', kind: 'superleo', role: 'machine', state: 'live'},
    {name: 'codex.sds-b', kind: 'cli', state: 'installed', via: 'sds-b'},
  ])]);
  const far = m.find((a) => a.name === 'codex.sds-b');
  assert.ok(far);
  assert.equal(far.alias, 'codex');
  assert.ok(!m.some((a) => a.name === 'sds-b'));
});

test('hive workers are offered with their short names; the queen is not', () => {
  const m = mentionsOf([
    host('node1', [
      {name: 'queen', kind: 'superleo', role: 'queen', state: 'live'},
      {name: 'claude.queen', kind: 'cli', state: 'idle', via: 'queen'},
    ]),
    host('node2', [{name: 'superleo-worker-3', kind: 'superleo', role: 'worker', state: 'live'}]),
  ]);
  assert.deepEqual(m.map((a) => a.name).sort(), ['claude', 'superleo-worker-3']);
  assert.equal(m.find((a) => a.name === 'superleo-worker-3').alias, 'w3');
});

test('an HA agent drawn on several hosts is offered once, where it runs', () => {
  const m = mentionsOf([
    host('sds-a', [{name: 'openclaw', kind: 'agent', state: 'standby'}]),
    host('sds-b', [{name: 'openclaw', kind: 'agent', state: 'live'}]),
  ]);
  assert.equal(m.length, 1);
  assert.match(m[0].about, /sds-b/);
});

test('no hosts yet is an empty list, not a crash', () => {
  assert.deepEqual(mentionsOf(null), []);
  assert.deepEqual(mentionsOf(undefined), []);
});

test('the command bar wires the @ menu to its input', () => {
  const cmd = fs.readFileSync(path.resolve(__dirname, '../src/world/Command.tsx'), 'utf8');
  assert.match(cmd, /<AgentMenu/);
  assert.match(cmd, /mentions\.onKeyDown\(e\)/);
  assert.match(cmd, /mentions\.update\(/);
  const world = fs.readFileSync(path.resolve(__dirname, '../src/world/World.tsx'), 'utf8');
  assert.match(world, /<Command .*hosts={topo\.hosts}/);
});
