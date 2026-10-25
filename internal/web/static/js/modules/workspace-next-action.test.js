import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveWorkspaceNextAction as resolve } from './workspace-next-action.js';

const ready = { state: 'ready', stale: false };
const absent = { workspaceId: 'w', applies: false, status: { state: 'not_applicable' } };
function consoleState(overrides = {}) {
  return {
    workspaceId: 'w',
    generation: 1,
    applies: true,
    status: ready,
    batch: ready,
    history: ready,
    configured: true,
    readiness: 'ready',
    mode: 'manual',
    rootId: 'root',
    batchId: 'batch',
    pendingCount: 0,
    scanning: false,
    emptyState: 'unscanned',
    latestReceipt: null,
    ...overrides
  };
}
function guidance(tasks = [], console = absent, taskState = 'ready') {
  return resolve({ workspaceId: 'w', tasks: { state: taskState, items: tasks }, console });
}
const task = (status, id = status, overrides = {}) => ({ id, status, to: 'Curator', ...overrides });
const receipt = (id, completedAt, extra = {}) => ({
  id,
  completedAt,
  result: 'applied',
  operation: 'move',
  undoable: false,
  ...extra
});
const completed = (id, at) =>
  task('completed', id, { result: 'Confirmed output', completed_at: at });

for (const [name, tasks, console, kind, id] of [
  [
    'attention before repair and running',
    [task('in_progress'), task('failed')],
    consoleState({ readiness: 'needs_attention' }),
    'task',
    'failed'
  ],
  [
    'repair before setup',
    [],
    consoleState({ configured: false, readiness: 'needs_attention' }),
    'repair'
  ],
  ['setup before consent', [], consoleState({ configured: false, consentRequired: true }), 'setup'],
  [
    'consent before review',
    [],
    consoleState({ pendingCount: 2, consentRequired: true }),
    'consent'
  ],
  ['review before running', [task('in_progress')], consoleState({ pendingCount: 2 }), 'review'],
  ['review before scan', [], consoleState({ pendingCount: 2, scanning: true }), 'review'],
  [
    'running before scan',
    [task('in_progress')],
    consoleState({ scanning: true }),
    'task',
    'in_progress'
  ],
  ['scan before actionable', [task('pending')], consoleState({ scanning: true }), 'review'],
  [
    'actionable before unknown',
    [task('future'), task('pending')],
    consoleState(),
    'task',
    'pending'
  ],
  ['unknown before result', [completed('done'), task('future')], consoleState(), 'task', 'future'],
  ['result before idle', [completed('done')], consoleState(), 'task', 'done'],
  ['generic idle requires known sources', [], absent, 'idle']
]) {
  test(name, () => {
    const result = guidance(tasks, console);
    assert.equal(result.kind, kind);
    if (id) assert.equal(result.action.id, id);
  });
}

test('task attention retains canonical state, activity and ID ordering', () => {
  const tasks = [
    task('failed', 'z'),
    task('blocked', 'b'),
    task('waiting_for_choice', 'z'),
    task('waiting_for_choice', 'a'),
    task('waiting_for_choice', 'new', { updated_at: '2026-10-02T00:00:00Z' })
  ];
  assert.equal(guidance(tasks).action.id, 'new');
  assert.equal(guidance(tasks.slice(0, -1)).action.id, 'a');
  assert.equal(
    guidance([task('in_progress'), task('pending', 'unassigned', { to: '' })]).action.kind,
    'assign_start'
  );
});

test('pending review keeps both running tasks and active scans visible as secondary status', () => {
  const result = guidance(
    [task('in_progress', 'work', { description: 'Research' })],
    consoleState({ pendingCount: 17, scanning: true })
  );
  assert.equal(result.title, '17 files ready for review');
  assert.deepEqual(result.ongoing, [
    'Running: Research',
    'Scanning for files. Nothing moves without approval.'
  ]);
});

for (const state of ['loading', 'unavailable']) {
  test(`${state} tasks cannot hide usable file review`, () => {
    const result = guidance([], consoleState({ pendingCount: 2 }), state);
    assert.equal(result.kind, 'review');
    assert.match(result.qualifications.join(' '), new RegExp(`Tasks are ${state}`));
  });
  test(`${state} file status cannot hide a task action or assert capability absence`, () => {
    const console = consoleState({ status: { state, stale: true } });
    const result = guidance([task('pending')], console);
    assert.equal(result.action.owner, 'task');
    assert.match(result.qualifications.join(' '), /Last known/);
    assert.equal(guidance([], console).kind, state);
  });
  test(`${state} review cannot recommend stale counts as actionable`, () => {
    const result = guidance([], consoleState({ pendingCount: 12, batch: { state, stale: true } }));
    assert.equal(result.kind, state);
    assert.doesNotMatch(result.title, /12|No open work/);
  });
}

test('cold, mismatched and absent snapshots are not interchangeable', () => {
  assert.equal(guidance([], null).kind, 'loading');
  assert.equal(
    guidance([], consoleState({ workspaceId: 'other', pendingCount: 9 })).kind,
    'loading'
  );
  assert.equal(guidance([], absent).kind, 'idle');
  assert.equal(guidance([], null, 'unavailable').kind, 'unavailable');
});

test('completion timestamps win over activity timestamps across tasks and receipts', () => {
  const old = { ...completed('old', '2026-10-01T00:00:00Z'), updated_at: '2099-01-01T00:00:00Z' };
  const newer = completed('new', '2026-10-02T00:00:00Z');
  const console = consoleState({ latestReceipt: receipt('file', '2026-10-03T00:00:00Z') });
  assert.equal(guidance([old, newer], console).kind, 'history');
  console.latestReceipt.completedAt = '2026-10-01T00:00:00Z';
  assert.equal(guidance([old, newer], console).action.id, 'new');
});

test('completion ties use stable source and ID, missing times are never invented', () => {
  const at = '2026-10-02T00:00:00Z';
  assert.equal(guidance([completed('z', at), completed('a', at)]).action.id, 'a');
  assert.equal(
    guidance([completed('a', at)], consoleState({ latestReceipt: receipt('file', at) })).kind,
    'history'
  );
  assert.equal(
    guidance([completed('unknown')], consoleState({ latestReceipt: receipt('file', at) })).kind,
    'history'
  );
  assert.equal(
    guidance([completed('unknown')]).action.id,
    'unknown',
    'a recorded task result is still accessible without inventing its date'
  );
  assert.notEqual(
    guidance([], consoleState({ latestReceipt: receipt('not-complete', '0001-01-01T00:00:00Z') }))
      .kind,
    'history'
  );
});

test('partial or stale histories are qualified without replacing other valid results', () => {
  const result = guidance(
    [completed('task', '2026-10-02T00:00:00Z')],
    consoleState({
      history: { state: 'unavailable', stale: true },
      latestReceipt: receipt('old', '2026-10-03T00:00:00Z')
    })
  );
  assert.equal(result.action.id, 'task');
  assert.match(result.qualifications.join(' '), /Filing history is unavailable/);
});

test('receipt failures and undo eligibility are evidence based', () => {
  const console = consoleState({
    latestReceipt: receipt('file', '2026-10-03T00:00:00Z', { result: 'failed' })
  });
  assert.match(guidance([], console).title, /could not be filed/);
  assert.doesNotMatch(guidance([], console).description, /Undo/);
  console.latestReceipt.result = 'applied';
  console.latestReceipt.undoable = true;
  assert.match(guidance([], console).description, /Undo is available in History/);
  console.pendingCount = 1;
  assert.equal(guidance([], console).title, '1 file ready for review');
});

test('manual, paused, watching, empty scan and real queued work stay distinct', () => {
  assert.match(guidance([], consoleState()).description, /Manual scans only/);
  assert.match(guidance([], consoleState({ mode: 'paused' })).description, /paused/);
  assert.match(guidance([], consoleState({ mode: 'watching' })).description, /Watching/);
  assert.match(
    guidance([], consoleState({ emptyState: 'scan_empty' })).description,
    /no eligible files/
  );
  assert.equal(guidance([task('queued')], consoleState()).action.owner, 'task');
});

test('starter tasks and user edits are never mutated, renamed or filtered by provenance or title', () => {
  const starter = Object.freeze({
    ...task('pending', 'starter'),
    description: 'Review files',
    context: Object.freeze({
      template_id: 'file-janitor',
      template_starter_task: true,
      template_setup: true
    })
  });
  const tasks = Object.freeze([starter]);
  const before = JSON.stringify(tasks);
  assert.equal(guidance(tasks, consoleState({ pendingCount: 2 })).kind, 'review');
  assert.equal(guidance(tasks, consoleState()).action.id, 'starter');
  assert.equal(JSON.stringify(tasks), before);
});

test('navigation keys change on workspace generation, root, batch and receipt identity', () => {
  const console = consoleState({ pendingCount: 2 });
  const before = guidance([], console).action.key;
  for (const change of [{ generation: 2 }, { rootId: 'new' }, { batchId: 'new' }]) {
    assert.notEqual(guidance([], { ...console, ...change }).action.key, before);
  }
  assert.equal(
    guidance([], { ...console, pendingCount: 3 }).action.key,
    before,
    'same batch count refresh does not change ownership'
  );
});
