// The 3D world's travel: who changed host between two plans, and the path a
// robot walks from its old host to its new one.
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const src = fs.readFileSync(path.resolve(__dirname, '../src/world/travel.ts'), 'utf8');
const out = ts.transpileModule(src, {compilerOptions: {target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.CommonJS}}).outputText;
const mod = {exports: {}};
new Function('module', 'exports', out)(mod, mod.exports);
const {movesOf, along, tripTime, turn} = mod.exports;

const at = (key, host, x = 0, z = 0) => ({key, host, x, y: 0, z});

test('an HA agent on another host is a move', () => {
  const m = movesOf([at('agent:openclaw', 'sds-a', 10, 0), at('superai:w1', 'sds-a')],
    [at('agent:openclaw', 'sds-b', -10, 4), at('superai:w1', 'sds-a')]);
  assert.equal(m.length, 1);
  assert.equal(m[0].key, 'agent:openclaw');
  assert.equal(m[0].from.host, 'sds-a');
  assert.equal(m[0].to.host, 'sds-b');
  assert.equal(m[0].from.x, 10);
});

test('several agents moving at once all move', () => {
  const m = movesOf([at('agent:openclaw', 'sds-a'), at('agent:hermes', 'sds-e')],
    [at('agent:openclaw', 'sds-b'), at('agent:hermes', 'sds-a')]);
  assert.deepEqual(m.map((x) => x.key).sort(), ['agent:hermes', 'agent:openclaw']);
});

test('staying, arriving and leaving are not moves', () => {
  assert.deepEqual(movesOf([at('agent:openclaw', 'sds-a')], [at('agent:openclaw', 'sds-a')]), []);
  assert.deepEqual(movesOf([], [at('agent:openclaw', 'sds-b')]), []);
  assert.deepEqual(movesOf([at('agent:openclaw', 'sds-a')], []), []);
});

test('the same name of another kind is another agent', () => {
  assert.deepEqual(movesOf([at('cli:openclaw', 'sds-a')], [at('agent:openclaw', 'sds-b')]), []);
});

test('an agent on more than one host is not one robot', () => {
  assert.deepEqual(movesOf([at('cli:claude', 'mac'), at('cli:claude', 'dell')], [at('cli:claude', 'hp')]), []);
  assert.deepEqual(movesOf([at('cli:claude', 'mac')], [at('cli:claude', 'dell'), at('cli:claude', 'hp')]), []);
});

test('a trip takes two to four seconds', () => {
  assert.equal(tripTime(at('a', 'x', 0, 0), at('a', 'y', 1, 0)), 2);
  assert.equal(tripTime(at('a', 'x', 0, 0), at('a', 'y', 500, 0)), 4);
  const mid = tripTime(at('a', 'x', 0, 0), at('a', 'y', 24, 0));
  assert.ok(mid > 2 && mid < 4);
});

test('the path starts at the old spot, ends on the new one, and stays near the ground', () => {
  const from = {x: 0, y: 0, z: 0}, to = {x: 20, y: 0.2, z: -10};
  assert.deepEqual(along(from, to, 0), {x: 0, y: 0, z: 0});
  assert.deepEqual(along(from, to, 1), {x: 20, y: 0.2, z: -10});
  let prev = -1;
  for (let i = 0; i <= 50; i++) {
    const p = along(from, to, i / 50);
    assert.ok(p.x >= prev - 1e-9, 'never walks back');
    assert.ok(p.y >= -1e-9 && p.y <= 0.6, 'hops, not flies');
    prev = p.x;
  }
  // Eased: the first tenth of the time covers well under a tenth of the way.
  assert.ok(along(from, to, 0.1).x < 2);
});

test('turning goes the short way round', () => {
  assert.ok(Math.abs(turn(3, -3, 1) - (3 + (2 * Math.PI - 6))) < 1e-9);
  assert.equal(turn(0, 1, 0), 0);
  assert.equal(turn(0, 1, 2), 1);
  assert.equal(turn(0, 1, -1), 0);
});
