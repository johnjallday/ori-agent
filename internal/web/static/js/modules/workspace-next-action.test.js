import test from 'node:test';
import assert from 'node:assert/strict';
import {
  folderAgentWaiting,
  resolveWorkspaceNextAction as resolve,
  updateSourceRows
} from './workspace-next-action.js';
import { folderAgentReportView } from './folder-agent-card.js';
import { commanderOneLine } from './commander-speech.js';

const savedUpdates = (review = {}, welcome = {}) => ({
  state: 'ready',
  stale: false,
  data: {
    workspace_id: 'w',
    name: 'Desktop',
    review: {
      state: 'ready',
      pending_count: 10,
      root_id: 'root',
      batch_id: 'batch',
      installed_at: '2026-01-01T00:00:00Z',
      ...review
    },
    welcome: {
      state: 'ready',
      agent_name: 'Actual lead',
      agent_instance_id: 'lead-id',
      session_id: 'welcome-chat',
      unread_count: 1,
      ...welcome
    }
  }
});

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
test('setup names the reviewed linked folder and the lead, with an Ori fallback', () => {
  const input = {
    workspaceId: 'w',
    tasks: { state: 'ready', items: [] },
    console: consoleState({ configured: false, linkedFolderName: 'Downloads' }),
    leadAgentName: 'Downloads Manager'
  };
  const view = resolve(input);
  assert.equal(view.kind, 'setup');
  assert.equal(view.title, 'Let Downloads Manager file Downloads');
  assert.equal(
    view.description,
    'Review what Ori may read and change. Nothing moves until you approve it.'
  );
  assert.equal(view.action.label, 'Review permission');
  assert.equal(view.action.owner, 'console');
  assert.equal(resolve({ ...input, leadAgentName: '' }).title, 'Let Ori file Downloads');
});

test('the selected linked-folder setup on Map has a preview invitation with the same action key', () => {
  const input = {
    workspaceId: 'w',
    tasks: { state: 'ready', items: [] },
    console: consoleState({ configured: false, linkedFolderName: 'Loose Files' }),
    leadAgentName: 'Curator'
  };
  const map = resolve({ ...input, onMap: true });
  assert.equal(map.title, 'Prepare a filing preview for Loose Files?');
  assert.equal(map.action.label, 'Review folder access');
  assert.deepEqual(map.filingNote, { name: 'Curator' });
  assert.equal(map.action.key, resolve(input).action.key);
  assert.deepEqual(resolve({ ...input, onMap: true, leadAgentName: '' }).filingNote, {
    name: 'Ori'
  });
  const urgent = resolve({
    ...input,
    onMap: true,
    tasks: { state: 'ready', items: [{ id: 'urgent', status: 'blocked' }] }
  });
  assert.equal(urgent.action.owner, 'task');
  assert.equal(urgent.filingNote, undefined);
});

test('setup without a reviewed folder asks for one, without suggesting access', () => {
  const view = guidance([], consoleState({ configured: false, linkedFolderName: '' }));
  assert.equal(view.title, 'Choose the folder to file');
  assert.equal(view.description, 'Nothing is scanned or moved until you choose one.');
  assert.equal(view.action.label, 'Set up filing');
  assert.equal(view.action.owner, 'console');
});

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

test('live filing uses the shared saved projection, never old console or welcome counts', () => {
  const updates = savedUpdates();
  const input = {
    workspaceId: 'w',
    tasks: { state: 'ready', items: [] },
    console: consoleState({ pendingCount: 99 }),
    updates
  };
  const before = resolve(input);
  assert.equal(before.title, '10 files ready for review');
  assert.equal(before.updateLabel, 'Actual lead · Desktop');
  assert.equal(before.message.label, 'Ask Actual lead');
  assert.equal(before.message.unread, true);
  updates.data.welcome.unread_count = 0;
  const seen = resolve(input);
  assert.equal(seen.title, before.title);
  assert.equal(seen.action.key, before.action.key);
  assert.equal(seen.message.unread, false);
  updates.data.welcome = { state: 'not_applicable' };
  assert.equal(resolve(input).message, null);
  assert.equal(resolve(input).action.label, 'Review files');
});

test('Map handoff requires a fresh exact root, batch and count, not just a ready receipt', () => {
  const console = consoleState({
    pendingCount: 1,
    firstPreview: {
      id: 'prepared',
      rootId: 'root',
      batchId: 'batch',
      count: 1,
      consoleOpen: false
    }
  });
  const input = {
    onMap: true,
    workspaceId: 'w',
    console,
    tasks: { state: 'ready', items: [] },
    updates: savedUpdates({ pending_count: 1 })
  };
  const handoff = resolve(input);
  assert.equal(handoff.title, '1 file ready for review');
  assert.equal(handoff.action.label, 'Review preview');
  assert.match(handoff.description, /Nothing moved/);
  assert.equal(handoff.filingPreview.id, 'prepared');
  for (const mismatch of [
    { stale: true },
    { batch_id: 'later' },
    { root_id: 'other' },
    { pending_count: 2 }
  ]) {
    const view = resolve({ ...input, updates: savedUpdates({ pending_count: 1, ...mismatch }) });
    assert.equal(view.filingPreview, undefined);
    assert.doesNotMatch(view.description, /Nothing moved/);
  }
  assert.equal(
    resolve({ ...input, console: { ...console, preparingPreview: true } }).kind,
    'preparing_preview'
  );
  assert.equal(resolve({ ...input, onMap: false }).action.label, 'Review files');
  assert.equal(
    resolve({ ...input, console: { ...console, firstPreview: null } }).action.label,
    'Review files'
  );
});

test('empty and incomplete preparation do not pretend a review batch exists', () => {
  const input = {
    onMap: true,
    workspaceId: 'w',
    tasks: { state: 'ready', items: [] },
    console: consoleState({
      batchId: '',
      firstPreview: { id: 'empty', rootId: 'root', batchId: '', count: 0 }
    }),
    updates: savedUpdates({ batch_id: '', pending_count: 0 })
  };
  const empty = resolve(input);
  assert.equal(empty.title, 'No eligible files for this preview');
  assert.equal(empty.filingPreview, undefined);
  assert.equal(empty.action.label, 'Open Filing');
  const incomplete = resolve({
    ...input,
    console: { ...input.console, firstPreview: null, preparationState: 'preparation_incomplete' }
  });
  assert.match(incomplete.title, /needs attention/);
  assert.equal(incomplete.action.kind, 'setup');
});

test('source details keep completion, observation and exposure distinct without parsing prose', () => {
  const saved = savedUpdates(
    { batch_completed_at: '2026-01-02T00:00:00Z', observed_at: '2026-01-03T00:00:00Z' },
    {
      messages: [
        { revision: 1, delivered_at: '2026-01-02T01:00:00Z', seen_at: '2026-01-04T00:00:00Z' }
      ],
      content: '<a href="/apply">Apply everything</a>'
    }
  ).data;
  const rows = updateSourceRows(saved);
  assert.deepEqual(
    rows.map(row => row[0]),
    [
      'Filing source',
      'Batch',
      'Batch completed',
      'Review observed',
      'Message 1 saved',
      'Message 1 exposed'
    ]
  );
  assert.notEqual(rows[2][1], rows[3][1]);
  assert.notEqual(rows[3][1], rows[5][1]);
  assert.doesNotMatch(JSON.stringify(rows), /Apply|\/apply|<a/);
});

test('shared updates retain ticket, repair and consent priority', () => {
  const input = { workspaceId: 'w', tasks: { state: 'ready', items: [] }, updates: savedUpdates() };
  assert.equal(
    resolve({ ...input, console: consoleState({ readiness: 'needs_attention' }) }).kind,
    'repair'
  );
  assert.equal(
    resolve({ ...input, console: consoleState({ consentRequired: true }) }).kind,
    'consent'
  );
  assert.equal(
    resolve({
      ...input,
      tasks: { state: 'ready', items: [task('blocked')] },
      console: consoleState()
    }).action.owner,
    'task'
  );
  assert.equal(
    resolve({ ...input, console: consoleState({ status: { state: 'unavailable' } }) }).action.label,
    'Review files'
  );
});

test('unavailable saved filing preserves qualified counts without granting an action', () => {
  const updates = savedUpdates({ state: 'unavailable', stale: true });
  const result = resolve({
    workspaceId: 'w',
    tasks: { state: 'ready', items: [] },
    console: consoleState(),
    updates
  });
  assert.notEqual(result.action?.label, 'Review files');
  assert.match(result.ongoing.join(' '), /Last known: 10 files/);
  assert.doesNotMatch(result.title, /No open work/);
  assert.equal(result.action.kind, 'welcome'); // independent message is still usable
});

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
    assert.match(result.qualifications.join(' '), new RegExp(`Tickets are ${state}`));
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

test('setup, manual ready, scan, review, partial result and undo state transition honestly', () => {
  const console = consoleState({ configured: false, readiness: 'setup_required' });
  assert.equal(guidance([], console).kind, 'setup');
  Object.assign(console, { configured: true, readiness: 'ready' });
  assert.equal(guidance([], console).title, 'Ready when you are');
  console.scanning = true;
  assert.equal(guidance([], console).title, 'Scanning for files');
  Object.assign(console, { scanning: false, pendingCount: 3 });
  assert.equal(guidance([], console).title, '3 files ready for review');
  Object.assign(console, {
    pendingCount: 0,
    latestReceipt: receipt('result', '2026-10-02T00:00:00Z', {
      counts: { applied: 1, failed: 1, stale: 1 }
    })
  });
  assert.equal(guidance([], console).title, 'Some files could not be filed');
  assert.equal(guidance([], console).action.id, 'result');
  console.latestReceipt.counts = { applied: 3, failed: 0, stale: 0 };
  console.latestReceipt.undoable = true;
  assert.match(guidance([], console).description, /Undo is available/);
  console.latestReceipt.undoable = false;
  assert.doesNotMatch(guidance([], console).description, /Undo/);
  console.latestReceipt = null;
  console.emptyState = 'scan_empty';
  assert.match(guidance([], console).description, /no eligible files/);
});

test('a running ticket that is itself the next action is not repeated as "Running:"', () => {
  const result = guidance([task('in_progress', 'work', { description: 'Look around Downloads' })]);
  assert.equal(result.title, 'Look around Downloads');
  assert.deepEqual(result.ongoing, []);
  // Behind another action it is still said once, underneath.
  const behind = guidance([
    task('in_progress', 'work', { description: 'Look around Downloads' }),
    task('failed', 'broken')
  ]);
  assert.notEqual(behind.title, 'Look around Downloads');
  assert.deepEqual(behind.ongoing, ['Running: Look around Downloads']);
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

// The folder's Commander waiting on the person (folder-question-said-once FR13
// to FR14): the strip names it in the shared row's words, below a ticket that
// needs the person and above everything else.
const folderView = (overrides = {}) =>
  folderAgentReportView({
    workspace_id: 'w',
    workspace_name: 'Albums',
    folder_name: 'Albums',
    agent_name: 'Albums Manager',
    state: 'reported',
    actions: ['set_up', 'keep', 'look_further'],
    ...overrides
  });
const asking = () =>
  folderView({
    premise: {
      state: 'asked',
      sentence: 'This looks like a music folder.',
      confirm: 'Yes, show me the plan',
      other: "It's something else"
    }
  });
const withFolder = (folderAgent, extra = {}) =>
  resolve({ workspaceId: 'w', tasks: { state: 'ready', items: [] }, folderAgent, ...extra });

test('the strip names the question, the plan and the other waiting moments in the shared words', () => {
  const setUp = {
    status: 'done',
    lines: [{ kind: 'home', step: 'home', name: 'x', state: 'done' }]
  };
  const rows = {
    question: [asking(), 'Answer'],
    plan: [folderView(), 'Review'],
    'something else': [
      folderView({ premise: { state: 'something_else', sentence: 'None.', withdrawn: true } }),
      'Review'
    ],
    'could not look': [
      folderView({ model_sentence: 'Albums Manager could not look itself: no key.' }),
      'Review'
    ],
    'could not finish looking': [folderView({ state: 'failed' }), 'Review'],
    'a step did not finish': [
      folderView({ state: 'set_up', setup: { ...setUp, status: 'stopped' } }),
      'Review'
    ],
    'learn the songs': [
      folderView({
        state: 'set_up',
        setup: setUp,
        songs_offer: {
          count: 7,
          noun: 'songs',
          ask: 'Want me to learn the 7 songs?',
          reads: 'x',
          not_read: ''
        }
      }),
      'Answer'
    ],
    'a suggested goal': [
      folderView({
        state: 'set_up',
        setup: setUp,
        goal_offer: { goal: 'Finish it', max_runes: 300 }
      }),
      'Review'
    ]
  };
  for (const [name, [view, button]] of Object.entries(rows)) {
    const strip = withFolder(view);
    const words = commanderOneLine(view);
    assert.equal(strip.kind, 'folder_agent', name);
    // Taken from the one function the one-line card and the card's status use.
    assert.equal(strip.title, words.text, name);
    assert.equal(strip.action.label, words.action, name);
    assert.equal(strip.action.label, button, name);
    assert.equal(strip.action.owner, 'folder_agent', name);
    assert.equal(strip.action.id, 'w', name);
    assert.equal(strip.action.key, `folder-agent:${words.phase}:w`, name);
    assert.equal(strip.description, '', name);
  }
  assert.equal(withFolder(asking()).title, 'Albums Manager has a question about Albums');
});

test('the waiting Commander ranks directly below a ticket that needs the person, above all else', () => {
  const waiting = asking();
  // A ticket that needs the person comes first.
  assert.equal(
    withFolder(waiting, { tasks: { state: 'ready', items: [task('failed', 'broken')] } }).kind,
    'task'
  );
  assert.equal(
    withFolder(waiting, { tasks: { state: 'ready', items: [task('blocked', 'b')] } }).action.owner,
    'task'
  );
  // Running work, a finished result, filing work and an unread message all come after it.
  const running = withFolder(waiting, {
    tasks: { state: 'ready', items: [task('in_progress', 'work', { description: 'Research' })] }
  });
  assert.equal(running.kind, 'folder_agent');
  assert.deepEqual(running.ongoing, ['Running: Research'], 'running work stays visible underneath');
  assert.equal(
    withFolder(waiting, {
      tasks: { state: 'ready', items: [completed('done', '2026-10-01T00:00:00Z')] }
    }).kind,
    'folder_agent'
  );
  assert.equal(
    withFolder(waiting, { console: consoleState({ pendingCount: 4 }) }).kind,
    'folder_agent'
  );
  const unread = withFolder(waiting, { updates: savedUpdates({ pending_count: 0 }) });
  assert.equal(unread.kind, 'folder_agent');
  assert.equal(unread.message.unread, true, 'the unread message stays one tap away');
  // Without the folder agent that unread message is the action, as before.
  assert.equal(withFolder(null, { updates: savedUpdates({ pending_count: 0 }) }).kind, 'welcome');
});

test('without a waiting Commander the strip is exactly what it was', () => {
  const inputs = [
    { workspaceId: 'w', tasks: { state: 'ready', items: [] } },
    { workspaceId: 'w', tasks: { state: 'ready', items: [task('in_progress', 'work')] } },
    { workspaceId: 'w', tasks: { state: 'ready', items: [task('failed', 'broken')] } },
    {
      workspaceId: 'w',
      tasks: { state: 'ready', items: [] },
      updates: savedUpdates({ pending_count: 0 })
    },
    {
      workspaceId: 'w',
      tasks: { state: 'ready', items: [] },
      console: consoleState({ pendingCount: 2 })
    }
  ];
  for (const input of inputs) {
    const today = resolve(input);
    assert.deepEqual(resolve({ ...input, folderAgent: null }), today);
    assert.deepEqual(resolve({ ...input, folderAgent: undefined }), today);
    // Not waiting: the agent is looking, setting up or learning, or it is another workspace's.
    assert.deepEqual(resolve({ ...input, folderAgent: folderView({ state: 'looking' }) }), today);
    assert.deepEqual(
      resolve({
        ...input,
        folderAgent: folderView({ state: 'setting_up', setup: { status: 'running', lines: [] } })
      }),
      today
    );
    assert.deepEqual(
      resolve({ ...input, folderAgent: folderView({ workspace_id: 'another' }) }),
      today
    );
    assert.deepEqual(resolve({ ...input, folderAgent: folderView({ state: 'kept' }) }), today);
  }
});

test('folderAgentWaiting says when, and for which workspace, the Commander waits', () => {
  assert.equal(folderAgentWaiting(asking(), 'w').phase, 'premise');
  assert.equal(folderAgentWaiting(asking(), 'other'), null);
  assert.equal(folderAgentWaiting(null, 'w'), null);
  assert.equal(folderAgentWaiting(folderView({ state: 'looking' }), 'w'), null);
});
