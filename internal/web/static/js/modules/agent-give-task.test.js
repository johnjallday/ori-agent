import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const read = name => readFileSync(new URL(name, import.meta.url), 'utf8');

function loadGiveTask() {
  const context = {};
  context.globalThis = context;
  vm.runInNewContext(read('./agent-give-task.js'), context);
  return context.OriAgentGiveTask;
}

const giveTask = loadGiveTask();
const plain = value => JSON.parse(JSON.stringify(value));

test('an agent in one workspace goes there with the task form open for it', () => {
  const target = plain(
    giveTask.giveTaskTarget('Rosa', [{ name: 'Compare Apps', slug: 'compare-apps' }])
  );
  assert.deepEqual(target, { kind: 'workspace', href: '/workspaces/compare-apps?newTaskFor=Rosa' });
  assert.equal(
    giveTask.newTaskHref('compare-apps', 'Mix Master'),
    '/workspaces/compare-apps?newTaskFor=Mix%20Master'
  );
});

test('an agent in several workspaces picks one first', () => {
  const target = plain(
    giveTask.giveTaskTarget('Rosa', [
      { name: 'Compare Apps', slug: 'compare-apps' },
      { name: 'Home Composting', slug: 'home-composting' }
    ])
  );
  assert.equal(target.kind, 'choose');
  assert.deepEqual(
    target.workspaces.map(ws => [ws.name, ws.href]),
    [
      ['Compare Apps', '/workspaces/compare-apps?newTaskFor=Rosa'],
      ['Home Composting', '/workspaces/home-composting?newTaskFor=Rosa']
    ]
  );
});

test('an agent with no workspace is offered the Hire card, with a note saying why', () => {
  assert.deepEqual(plain(giveTask.giveTaskTarget('Rosa', [])), {
    kind: 'hire',
    note: 'This agent has no workspace yet'
  });
  // A ref with no usable slug is no place to go.
  assert.equal(
    giveTask.giveTaskTarget('Rosa', [
      { name: 'Odd', slug: '../x' },
      { name: '', slug: 'x' }
    ]).kind,
    'hire'
  );
  assert.equal(giveTask.newTaskHref('compare-apps', ''), '');
});

test('the roster offers Give a task… and the workspace page opens the task form for the named agent', () => {
  const roster = read('../agents-roster.js');
  assert.match(roster, /label: 'Give a task…',\s*action: 'give-task'/);
  assert.match(roster, /case 'give-task':\s*giveTask\(name, vm\);/);
  assert.match(roster, /prefill: \{ role: vm\.role, note: target\.note \}/);
  assert.match(roster, /slug: String\(\(w && w\.folder_slug\) \|\| ''\)\.trim\(\)/);
  const template = read('../../../templates/pages/agents-roster.tmpl');
  assert.match(template, /agent-give-task\.js/);
  const workspace = read('./workspace-detail.js');
  assert.match(workspace, /params\.get\('newTaskFor'\)/);
  assert.match(workspace, /this\.showAddTaskModalForAgent\(encodeURIComponent\(agentName\)\)/);
  assert.equal(giveTask.NEW_TASK_FOR_PARAM, 'newTaskFor');
});
