import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import {
  WORK_LABELS,
  RETIRED_WORK_TERMS,
  retiredWorkTermsIn,
  startFromBacklogLabel
} from './work-labels.js';
import { HQ_LABELS } from './hq-labels.js';
import { WorkspaceCommandView } from './workspace-command.js';

// Same headless-fixture pattern as workspace-command-map-backlog.test.js: the
// constructor only touches the DOM through getElementById (stubbed to null).
globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.window = { Toast: null, location: { search: '' } };

const here = dirname(fileURLToPath(import.meta.url));

// ---------- module shape ----------

test('WORK_LABELS names a work item a ticket and keeps Backlog as its waiting state', () => {
  assert.equal(WORK_LABELS.ticket, 'Ticket');
  assert.equal(WORK_LABELS.tickets, 'Tickets');
  assert.equal(WORK_LABELS.newTicket, 'New ticket');
  assert.equal(WORK_LABELS.openTickets, 'Open tickets');
  assert.equal(WORK_LABELS.backlog, 'Backlog');
  assert.equal(WORK_LABELS.backlogSubtitle, 'Saved for later');
  assert.equal(WORK_LABELS.backlogEmpty, 'Backlog clear');
  assert.equal(WORK_LABELS.start, 'Start');
  assert.equal(WORK_LABELS.open, 'Open');
  assert.equal(WORK_LABELS.resolve, 'Resolve');
  assert.equal(WORK_LABELS.track, 'Track');
  assert.equal(WORK_LABELS.giveWork, 'Give work');
  assert.equal(WORK_LABELS.runs, 'Runs');
  assert.equal(WORK_LABELS.runHistory, 'Run history');
  assert.ok(Object.isFrozen(WORK_LABELS));
});

test('every WORK_LABELS value is itself free of retired terms', () => {
  for (const [key, value] of Object.entries(WORK_LABELS)) {
    assert.deepEqual(retiredWorkTermsIn(value), [], key + ' = ' + value);
  }
});

test('HQ_LABELS takes its work words from WORK_LABELS', () => {
  assert.equal(HQ_LABELS.tasks, WORK_LABELS.tickets);
  assert.equal(HQ_LABELS.openTasks, WORK_LABELS.openTickets);
  assert.equal(HQ_LABELS.backlog, WORK_LABELS.backlog);
  assert.equal(HQ_LABELS.newTask, WORK_LABELS.newTicket);
  assert.equal(HQ_LABELS.giveTask, WORK_LABELS.giveWork);
});

test('RETIRED_WORK_TERMS lists every retired name for a work item', () => {
  for (const term of [
    'Quest Board',
    'Accept Quest',
    'Start Quest',
    'Resolve Quest',
    'Track Quest',
    'Open Quest',
    'Quest',
    'Objective',
    'Objectives',
    'All Tasks',
    'Session Tickets',
    'Task',
    'Tasks'
  ]) {
    assert.ok(RETIRED_WORK_TERMS.includes(term), term);
  }
  assert.ok(Object.isFrozen(RETIRED_WORK_TERMS));
});

test('retiredWorkTermsIn matches whole words only', () => {
  assert.deepEqual(retiredWorkTermsIn('Start'), []);
  assert.deepEqual(retiredWorkTermsIn('Taskbar and questions'), []);
  assert.deepEqual(retiredWorkTermsIn('class="is-quest-empty"'), []);
  assert.ok(retiredWorkTermsIn('Failed to create task').includes('Task'));
  assert.ok(retiredWorkTermsIn('Backlog · Quest Board').includes('Quest Board'));
});

test('startFromBacklogLabel spells out the action in the accessible name', () => {
  assert.equal(startFromBacklogLabel('Ship it'), 'Start — move Ship it into work');
});

// ---------- the guard (FR13) ----------

/**
 * Everything a person can read in an HTML fragment: text nodes plus the
 * aria-label, title and placeholder attributes. Class names, ids and data-*
 * attributes are not read, so identifiers stay free to say "task".
 */
function readableParts(html) {
  const parts = [];
  const source = String(html || '').replace(/<!--[\s\S]*?-->/g, '');
  const attrRe = /\b(aria-label|title|placeholder)="([^"]*)"/g;
  let m;
  while ((m = attrRe.exec(source))) parts.push(m[1] + ': ' + m[2]);
  const text = source
    .replace(/<script[\s\S]*?<\/script>/g, ' ')
    .replace(/<style[\s\S]*?<\/style>/g, ' ')
    .replace(/<[^>]*>/g, '\n')
    .split('\n')
    .map(s => s.trim())
    .filter(Boolean);
  for (const t of text) parts.push('text: ' + t);
  return parts;
}

function assertNoRetiredTerms(surface, html) {
  const offenders = readableParts(html)
    .map(part => ({ part, terms: retiredWorkTermsIn(part) }))
    .filter(hit => hit.terms.length);
  assert.deepEqual(
    offenders,
    [],
    surface + ' shows a retired work-item word:\n' + offenders.map(o => '  ' + o.part).join('\n')
  );
}

function backlogItem(id, description, status) {
  return {
    task: { id, description, status },
    owning_workspace_id: 'w1',
    owning_workspace_name: 'Alpha'
  };
}

function makeView(extraPage = {}) {
  const view = new WorkspaceCommandView(null);
  view.page = {
    backlogItems: [],
    backlogSync: null,
    backlogIncludeDescendants: false,
    workspaceId: 'w1',
    tasks: [],
    ...extraPage
  };
  view.isPersonalHQ = () => false;
  view.render = () => {};
  return view;
}

test('guard: the non-HQ Map belt never says Quest Board', () => {
  const view = makeView();
  assertNoRetiredTerms('Map belt', view.renderMapToolTray());
  const backlog = view.mapWindowOptions().find(o => o.key === 'backlog');
  assert.equal(backlog.label, WORK_LABELS.backlog);
});

test('guard: the non-HQ Backlog window reads Backlog / Saved for later with Start buttons', () => {
  const view = makeView({
    backlogItems: [
      backlogItem('a', 'Draft the release notes', 'backlog'),
      backlogItem('b', 'Pick a cover image', 'needs_input')
    ]
  });
  const html = view.renderMapBacklogPanel();
  assertNoRetiredTerms('Backlog window', html);
  assert.match(html, /<strong>Backlog<\/strong>/);
  assert.match(html, /Saved for later/);
  assert.match(html, /aria-label="Start — move Draft the release notes into work"/);
  assert.match(html, /<span>Start<\/span>/);
});

test('guard: the empty non-HQ Backlog window says Backlog clear', () => {
  const html = makeView().renderMapBacklogPanel();
  assertNoRetiredTerms('Backlog window (empty)', html);
  assert.match(html, /Backlog clear/);
});

test('guard: the Map Tickets window and its empty state never say quest or objective', () => {
  const view = makeView();
  view.openMapTasks = () => [
    { id: 't1', description: 'Mix the chorus', status: 'pending' },
    { id: 't2', description: 'Answer the label', status: 'needs_input' }
  ];
  assertNoRetiredTerms('Map Tickets window', view.renderMapTasksPanel());
  view.openMapTasks = () => [];
  assertNoRetiredTerms('Map Tickets window (empty)', view.renderMapTasksPanel());
});

test('guard: ticket row action words are Resolve / Track / Open', () => {
  const view = makeView();
  const labels = ['needs_input', 'blocked', 'running', 'failed', 'cancelled', 'unknown'].map(
    status => view.mapTaskCommandLabel({ id: 'x', status })
  );
  for (const label of labels) assert.deepEqual(retiredWorkTermsIn(label), [], label);
  assert.equal(view.mapTaskCommandLabel({ id: 'x', status: 'needs_input' }), WORK_LABELS.resolve);
  assert.equal(view.mapTaskCommandLabel({ id: 'x', status: 'running' }), WORK_LABELS.track);
});

function fixtureAgent(task) {
  return {
    name: 'Mira',
    encodedName: 'Mira',
    tone: 'is-idle',
    entry: false,
    role: { label: 'Writer' },
    status: { label: 'Idle', detail: '' },
    skills: { count: 1 },
    mcpNames: [],
    instanceCount: 1,
    tasks: task ? [task] : [],
    currentTask: task || null
  };
}

function stubAgentSheet(view) {
  view.agentCharacterHTML = () => '';
  view.renderMapAgentModelCard = () => '';
  view.renderMapAgentLoadout = () => '';
  view.renderCapabilityInspector = () => '';
  view.agentActivitySummary = () => null;
  view.agentDetailTarget = () => null;
  view.latestAgentSession = () => null;
}

test('guard: the agent sheet and its command menu say ticket, Start and Assign a first ticket', () => {
  for (const task of [
    null,
    { id: 'p1', description: 'Queued idea', status: 'pending' },
    { id: 'n1', description: 'Needs an answer', status: 'needs_input' }
  ]) {
    const view = makeView();
    stubAgentSheet(view);
    view.priorityTaskForAgent = () => task;
    const agent = fixtureAgent(task);
    assertNoRetiredTerms(
      'Agent sheet (' + (task ? task.status : 'no ticket') + ')',
      view.renderMapInspector(agent)
    );
  }
});

test('guard: the Tickets page header in workspace-detail.tmpl says Tickets', () => {
  const tmpl = readFileSync(join(here, '../../../templates/pages/workspace-detail.tmpl'), 'utf8');
  const start = tmpl.indexOf('id="workspace-detail-tickets-surface"');
  const end = tmpl.indexOf('id="workspace-detail-shared-hosts"');
  assert.ok(start > 0 && end > start, 'the Tickets surface block is found');
  const block = tmpl.slice(start, end).replace(/\{\{[\s\S]*?\}\}/g, ' ');
  assertNoRetiredTerms('Tickets page', block);
  assert.match(block, /<h3 class="hub-tickets-title">Tickets<\/h3>/);
});

test('guard: starting a ticket that is no longer pending toasts the ticket word', async () => {
  const messages = [];
  const view = makeView({
    executeTask: async () => {},
    loadTasks: async () => {}
  });
  globalThis.window.Toast = { info: m => messages.push(m), error: m => messages.push(m) };
  try {
    await view.startMapQuest('gone');
  } finally {
    globalThis.window.Toast = null;
  }
  assert.deepEqual(messages, [WORK_LABELS.noLongerPending]);
});
