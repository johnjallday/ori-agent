import test from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';
import { workspaceUpdates } from './workspace-updates.js';
import { folderAgentReportView } from './folder-agent-card.js';

globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
function view(tasks, extra = {}) {
  const result = new WorkspaceCommandView(null);
  result.page = { workspaceId: 'w', workspaceSlug: 'desk', tasks, ...extra };
  result.janitorActionState = () =>
    extra.janitorState || {
      workspaceId: 'w',
      applies: false,
      status: { state: 'not_applicable' }
    };
  return result;
}
const running = { id: 'run', status: 'in_progress', description: 'Routine work' };
const completed = {
  id: 'done',
  status: 'completed',
  description: 'Available result',
  result: 'Done',
  updated_at: '2026-10-01T00:00:00Z'
};

test('attention precedes routine running while preserving canonical priority within attention', () => {
  const failed = { id: 'failure', status: 'failed' };
  const input = { id: 'input', status: 'in_progress', context: { execution_step_waiting: true } };
  const command = view([running, failed, completed]);
  assert.equal(command.nextWork().next.id, failed.id);
  assert.equal(command.nextWork().current.id, running.id);
  assert.equal(command.nextWork().result.id, completed.id);
  command.page.tasks.push(input);
  assert.equal(command.nextWork().next.id, input.id);
  assert.equal(command.nextWork().attention, 2);
});

for (const [task, action] of [
  [{ status: 'pending' }, 'assign_start'],
  [{ status: 'pending', to: 'Writer' }, 'start'],
  [{ status: 'in_progress' }, 'track'],
  [{ status: 'waiting_for_choice' }, 'respond'],
  [{ status: 'blocked' }, 'inspect'],
  [
    {
      status: 'blocked',
      context: { human_loop: { repair: { label: 'Connect', url: '/agents' } } }
    },
    'repair'
  ],
  [{ status: 'failed' }, 'retry'],
  [{ status: 'timeout' }, 'retry'],
  [completed, 'view_result'],
  [{ status: 'future_status' }, 'inspect']
]) {
  test(`next action reuses the ${action} owner for ${task.status}`, () => {
    const command = view([{ ...task, id: 'task' }]);
    assert.match(command.renderWorkFirst(), new RegExp(`data-cmd-work-action="${action}"`));
    const opened = [];
    command.openTicketsView = id => opened.push(id);
    command.page.executeTask = () =>
      assert.fail('a next action opens the ticket; it never runs it');
    command.runWorkAction(action, 'task', null);
    assert.deepEqual(opened, ['task'], 'FR-32: it lands on the ticket');
  });
}

test('loading and failure never masquerade as an idle workspace; Retry has one owner', () => {
  assert.match(view(undefined).renderWorkFirst(), /Tickets are loading/);
  assert.match(view([], { tasksLoading: true }).renderWorkFirst(), /Tickets are loading/);
  const failed = view([], { tasksLoadFailed: true });
  assert.match(failed.renderWorkFirst(), /Retry workspace status/);
  assert.doesNotMatch(failed.renderWorkFirst(), /Give work|No open work/);
  assert.match(view([]).renderWorkFirst(), /No open work/);
  assert.doesNotMatch(
    view([]).renderWorkFirst(),
    /data-cmd-next-action="create"/,
    'idle uses shared-header Give work'
  );
});

test('closed and Backlog records do not invent work or a missing result', () => {
  const command = view([
    { id: 'b', status: 'backlog' },
    { id: 'c', status: 'completed' }
  ]);
  assert.equal(command.nextWork().state, 'idle');
  assert.doesNotMatch(command.renderWorkFirst(), /View Result|data-cmd-work-action="start"/);
  assert.equal(view([], { workspace: { kind: 'group' } }).renderWorkFirst(), '');
});

test('refresh replaces recommendations without changing explicit task and agent selection', () => {
  const command = view([running]);
  command.taskDrawerSelectedId = running.id;
  command.activeAgentName = 'writer';
  command.page.tasks = [completed];
  assert.equal(command.nextWork().next.id, completed.id);
  assert.equal(command.taskDrawerSelectedId, running.id);
  assert.equal(command.activeAgentName, 'writer');
});

test('task titles stay text and configuration is a named secondary disclosure', () => {
  const command = view([{ ...running, description: '<img onerror=alert(1)>' }]);
  assert.match(command.renderWorkFirst(), /&lt;img/);
  assert.doesNotMatch(command.renderWorkFirst(), /<img/);
  assert.match(command.renderWorkspaceAdmin(), /<summary>Workspace details &amp; settings/);
});

const reviewState = () => ({
  workspaceId: 'w',
  generation: 1,
  applies: true,
  configured: true,
  readiness: 'ready',
  status: { state: 'ready' },
  batch: { state: 'ready' },
  history: { state: 'ready' },
  pendingCount: 4,
  rootId: 'root',
  batchId: 'batch',
  mode: 'manual'
});

function bindPrimary(command) {
  const html = command.renderWorkFirst();
  const key = html.match(/data-cmd-next-action="([^"]+)"/)?.[1];
  let click;
  const root = {
    addEventListener: (type, handler) => {
      if (type === 'click') click = handler;
    }
  };
  command.container = { querySelector: () => root };
  command.bindWorkFirst();
  const button = {
    id: 'workspaceNextAction',
    hasAttribute: name => name === 'data-cmd-next-action',
    getAttribute: name => (name === 'data-cmd-next-action' ? key : null)
  };
  return { html, button, click: () => click({ target: { closest: () => button } }) };
}

test('Map preview invitation uses the manager face and escapes folder and manager names', t => {
  const janitorState = {
    ...reviewState(),
    configured: false,
    linkedFolderName: 'Loose <Files>'
  };
  const command = view([], {
    janitorState,
    workspace: { entry_agent_name: 'My <Manager>' },
    commanderFaceMarkup: name => {
      assert.equal(name, 'My <Manager>');
      return '<img src="/characters/operations-keeper/portrait.svg" alt="">';
    }
  });
  assert.match(command.renderWorkFirst(), /Review permission/);
  command.viewMode = 'map';
  const calls = [];
  globalThis.window = { FileJanitorConsole: { focusSetup: (...args) => calls.push(args) } };
  t.after(() => delete globalThis.window);
  const primary = bindPrimary(command);
  assert.match(primary.html, /A note from My &lt;Manager&gt;/);
  assert.match(primary.html, /operations-keeper\/portrait.svg/);
  assert.match(primary.html, /Prepare a filing preview for Loose &lt;Files&gt;\?/);
  assert.match(primary.html, />Review folder access<\/button>/);
  assert.match(primary.html, /What access is needed\?/);
  assert.doesNotMatch(primary.html, /All tickets/);
  assert.equal((primary.html.match(/is-primary/g) || []).length, 1);
  primary.click();
  assert.deepEqual(calls, [[primary.button, { source: 'workspace-next-action' }]]);

  delete command.page.commanderFaceMarkup;
  delete command.page.workspace.entry_agent_name;
  assert.match(command.renderWorkFirst(), /A note from Ori/);
  assert.match(command.renderWorkFirst(), /ori-guide\/portrait.svg/);
  command.page.tasks = [{ id: 'urgent', status: 'blocked' }];
  assert.match(command.renderWorkFirst(), /Open ticket/);
  assert.doesNotMatch(command.renderWorkFirst(), /Review folder access/);
  command.page.tasks = [];
  janitorState.linkedFolderName = '';
  assert.match(command.renderWorkFirst(), /Choose the folder to file/);
  assert.doesNotMatch(command.renderWorkFirst(), /A note from/);
});

function updateData(batch = 'current') {
  return {
    workspace_id: 'w',
    name: 'Desktop <script>',
    review: {
      state: 'ready',
      root_id: 'root',
      batch_id: batch,
      pending_count: 10,
      batch_completed_at: '2026-01-01T00:00:00Z',
      observed_at: '2026-01-02T00:00:00Z'
    },
    welcome: {
      state: 'ready',
      session_id: 'saved-chat',
      agent_instance_id: 'lead',
      agent_name: 'Mira <img>',
      unread_count: 1
    }
  };
}

test('only the matching review-ready station replaces the routine Map card', () => {
  const command = view([], { janitorState: reviewState() });
  command.updatesView = () => ({ state: 'ready', data: updateData('batch') });
  command.viewMode = 'map';
  const state = { reviewBatchId: 'batch', reviewCount: 10, description: 'Nothing moved.' };
  command.workspaceStationRegistry = () => [
    { key: 'file-janitor', label: 'Filing', state: () => state }
  ];
  command.mapStationRegistry = command.workspaceStationRegistry;
  assert.equal(command.renderWorkFirst(), '');
  assert.match(command.renderMapHQStations(), /Review 10 files/);
  state.reviewBatchId = 'older';
  assert.match(command.renderWorkFirst(), /Next action/);
  state.reviewBatchId = 'batch';
  command.viewMode = 'details';
  assert.match(command.renderWorkFirst(), /Next action/);
});

test('shared projection renders one primary, actual-lead Ask and escaped Source details', () => {
  const command = view([], { janitorState: reviewState() });
  command.updatesView = () => ({ state: 'ready', data: updateData() });
  const html = command.renderWorkFirst();
  assert.equal((html.match(/is-primary/g) || []).length, 1);
  assert.match(html, /10 files ready for review/);
  assert.match(html, /Ask Mira &lt;img&gt; · New message/);
  assert.match(html, /Desktop &lt;script&gt;/);
  assert.doesNotMatch(html, /<script>|<img>/);
  assert.match(html, /Source details/);
  assert.match(html, /Nothing moves without your approval/);
});

test('update Review binding re-reads and opens the current batch without any work request', async t => {
  workspaceUpdates.reset();
  const command = view([], { janitorState: reviewState() });
  command.active = true;
  command.updatesView = () => ({ state: 'ready', data: updateData('old') });
  const calls = [];
  const opened = [];
  const previousFetch = globalThis.fetch;
  globalThis.window = {
    location: { search: '' },
    FileJanitorConsole: {
      openLatestReview: options => {
        opened.push(options);
        return true;
      }
    }
  };
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return { ok: true, json: async () => updateData('new') };
  };
  t.after(() => {
    delete globalThis.window;
    globalThis.fetch = previousFetch;
    workspaceUpdates.reset();
  });
  const primary = bindPrimary(command);
  primary.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/workspaces/w/updates');
  assert.equal(calls[0].options.method, undefined);
  assert.deepEqual(opened, [
    { source: 'workspace-updates', trigger: primary.button, batchId: 'new' }
  ]);
});

test('the map handoff waits for the overlay to close and marks each prepared batch once', t => {
  let reducedMotion = false;
  globalThis.window = { location: { search: '' }, matchMedia: () => ({ matches: reducedMotion }) };
  t.after(() => delete globalThis.window);
  const command = view([]);
  command.viewMode = 'map';
  const classes = [];
  const node = { classList: { add: name => classes.push(name) } };
  command.container = { querySelector: () => node };
  const preview = { id: 'new', rootId: 'root', batchId: 'batch', consoleOpen: true };
  let action = {};
  command.nextAction = () => action;
  command.markFilingPreviewHandoff();
  action = { filingPreview: preview };
  command.markFilingPreviewHandoff();
  assert.deepEqual(classes, [], 'no hydration or animation behind the console');
  preview.consoleOpen = false;
  command.markFilingPreviewHandoff();
  command.markFilingPreviewHandoff();
  preview.consoleOpen = true;
  command.markFilingPreviewHandoff();
  preview.consoleOpen = false;
  command.markFilingPreviewHandoff();
  assert.deepEqual(classes, ['is-preview-handoff', 'is-preview-arriving']);
  reducedMotion = true;
  preview.batchId = 'second';
  command.markFilingPreviewHandoff();
  reducedMotion = false;
  command.markFilingPreviewHandoff();
  assert.equal(classes.length, 2, 'reduced motion suppresses rather than postpones the effect');
});

test('mounted Command shares one disposable watcher and ignores unchanged observations', async t => {
  workspaceUpdates.reset();
  const priorDocument = globalThis.document;
  const previousFetch = globalThis.fetch;
  globalThis.document = new EventTarget();
  globalThis.document.getElementById = () => null;
  globalThis.document.visibilityState = 'visible';
  globalThis.window = new EventTarget();
  globalThis.window.location = { search: '' };
  globalThis.window.FileJanitorConsole = { subscribe: () => () => {} };
  globalThis.fetch = async () => ({ ok: true, json: async () => updateData() });
  const command = view([], { janitorState: reviewState() });
  command.active = true;
  let renders = 0;
  command.render = () => renders++;
  t.after(() => {
    command.disposeNextAction();
    globalThis.document = priorDocument;
    delete globalThis.window;
    globalThis.fetch = previousFetch;
    workspaceUpdates.reset();
  });
  command.ensureNextActionSubscription();
  command.ensureNextActionSubscription();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(command.nextAction().title, '10 files ready for review');
  assert.equal(renders, 1);
  await workspaceUpdates.refresh('w');
  assert.equal(renders, 1);
  command.disposeNextAction();
  await workspaceUpdates.refresh('w');
  assert.equal(renders, 1);
});

test('Details renders one review CTA and real binding dispatches only console navigation', () => {
  const command = view([running], { janitorState: reviewState() });
  const opened = [];
  globalThis.window = {
    location: { search: '' },
    FileJanitorConsole: { open: options => opened.push(options) }
  };
  globalThis.fetch = () => assert.fail('navigation cannot issue a scan or operation');
  const primary = bindPrimary(command);
  assert.equal((primary.html.match(/is-primary/g) || []).length, 1);
  assert.match(primary.html, /4 files ready for review/);
  assert.match(primary.html, /Running: Routine work/);
  assert.match(primary.html, /All tickets/);
  primary.click();
  assert.deepEqual(opened, [
    { source: 'workspace-next-action', trigger: primary.button, tab: 'review' }
  ]);
  delete globalThis.window;
});

test('an old button cannot navigate to a new batch or start a task after priority changes', () => {
  const snapshot = reviewState();
  const command = view([], { janitorState: snapshot });
  const primary = bindPrimary(command);
  command.render = () => {};
  globalThis.window = {
    location: { search: '' },
    FileJanitorConsole: { open: () => assert.fail('old batch') }
  };
  snapshot.batchId = 'replacement';
  primary.click();
  assert.match(command.workNotice, /changed/);
  const taskCommand = view([{ id: 'start', status: 'pending', to: 'Writer' }], {
    janitorState: { ...snapshot, pendingCount: 0 }
  });
  const taskButton = bindPrimary(taskCommand);
  taskCommand.render = () => {};
  taskCommand.page.tasks.push({ id: 'urgent', status: 'blocked' });
  taskCommand.runWorkAction = () => assert.fail('old priority cannot execute');
  taskButton.click();
  assert.match(taskCommand.workNotice, /changed/);
  delete globalThis.window;
});

test('console subscription is identity scoped, deduplicated and disposable', () => {
  const snapshot = reviewState();
  const command = view([]);
  command.janitorActionState = () => snapshot;
  command.active = true;
  command.viewMode = 'details';
  let listener;
  let subscriptions = 0;
  let removals = 0;
  let renders = 0;
  const controller = {
    subscribe: fn => {
      listener = fn;
      subscriptions++;
      return () => removals++;
    }
  };
  globalThis.window = { location: { search: '' }, FileJanitorConsole: controller };
  command.render = () => renders++;
  command.ensureNextActionSubscription();
  command.ensureNextActionSubscription();
  assert.equal(subscriptions, 1);
  snapshot.pendingCount++;
  listener({ workspaceId: 'other' });
  assert.equal(renders, 0);
  listener({ workspaceId: 'w' });
  listener({ workspaceId: 'w' });
  assert.equal(renders, 1);
  command.disposeNextAction();
  snapshot.pendingCount++;
  listener({ workspaceId: 'w' });
  assert.equal(renders, 1);
  assert.equal(removals, 1);
  delete globalThis.window;
});

test('Map shell and Details render the identical action without replacing stations or all tasks', () => {
  const command = view([], { janitorState: reviewState() });
  command.viewMode = 'details';
  const details = command.renderWorkFirst();
  command.viewMode = 'map';
  const map = command.renderOperationsMap();
  // The same action; on the Map only its layout class differs (one row).
  assert.ok(
    map.includes(details.replace('class="ws-cmd-work-first', 'class="ws-cmd-work-first is-map'))
  );
  assert.match(map, /Workspace operations map/);
  assert.match(map, /data-cmd-open-tickets/);
  assert.match(map, /All tickets/);
  assert.equal((map.match(/id="workspaceNextAction"/g) || []).length, 1);
  assert.equal((map.match(/ws-cmd-agent-action is-primary/g) || []).length, 1);
});

for (const [variant, patch, destination] of [
  ['setup', { configured: false }, 'setup'],
  ['repair', { readiness: 'needs_attention' }, 'review'],
  ['consent', { consentRequired: true }, 'settings'],
  [
    'history',
    {
      pendingCount: 0,
      latestReceipt: { id: 'receipt', completedAt: '2026-10-01T00:00:00Z', result: 'applied' }
    },
    'history'
  ]
]) {
  test(`${variant} binding uses the existing console destination without operating`, () => {
    const command = view([], { janitorState: { ...reviewState(), ...patch } });
    const calls = [];
    globalThis.window = {
      location: { search: '' },
      FileJanitorConsole: {
        open: options => calls.push(options.tab),
        focusSetup: () => calls.push('setup')
      }
    };
    globalThis.fetch = () => assert.fail('navigation must not operate');
    bindPrimary(command).click();
    assert.deepEqual(calls, [destination]);
    delete globalThis.window;
  });
}

test('idle readiness is separate from queued work and missing roles', () => {
  const command = view([]);
  assert.equal(
    command.mapAgentStatus({ tasks: [], status: { key: 'idle' } }).label,
    'Ready to help'
  );
  for (const status of ['pending', 'queued', 'assigned', 'ready']) {
    assert.equal(
      command.mapAgentStatus({ tasks: [{ status, to: 'Curator' }], status: { key: 'idle' } }).key,
      'waiting'
    );
  }
  assert.equal(
    command.mapAgentStatus({ tasks: [{ status: 'failed' }], status: { key: 'idle' } }).label,
    'Needs attention'
  );
  assert.match(command.renderMapCommandRepair(), /No Commander/);
  assert.match(command.commandNodeOrchestrationCopy(0), /Works directly/);
});

test('stable live region announces changes, not polling starts or unchanged refreshes', () => {
  const previous = globalThis.document;
  const writes = [];
  globalThis.document = {
    getElementById: id =>
      id === 'workspaceNextActionStatus'
        ? {
            set textContent(text) {
              writes.push(text);
            }
          }
        : null
  };
  try {
    const snapshot = reviewState();
    const command = view([], { janitorState: snapshot });
    command.active = true;
    command.announceNextAction();
    command.announceNextAction();
    snapshot.batch.state = 'loading';
    command.announceNextAction();
    snapshot.batch.state = 'ready';
    command.viewMode = 'map';
    command.announceNextAction();
    assert.equal(writes.length, 1);
    snapshot.pendingCount = 5;
    command.announceNextAction();
    assert.match(writes[1], /5 files ready for review/);
    command.page.tasksLoadFailed = true;
    command.announceNextAction();
    assert.match(writes[2], /Tickets are unavailable/);
  } finally {
    globalThis.document = previous;
  }
});

for (const kind of ['station', 'detachment']) {
  test(`${kind} drag retains DOM during notifications and flushes the latest action on release`, async () => {
    const previous = globalThis.document;
    const handlers = {};
    globalThis.document = {
      getElementById: () => null,
      createElement: () => ({
        addEventListener: (name, fn) => {
          handlers[name] = fn;
        }
      })
    };
    let listener;
    globalThis.window = {
      location: { search: '' },
      FileJanitorConsole: {
        subscribe: fn => {
          listener = fn;
          return () => {};
        }
      }
    };
    try {
      const snapshot = reviewState();
      const command = view([], { janitorState: snapshot });
      command.active = true;
      command.viewMode = 'map';
      command.publishWorkspaceViewMode = () => {};
      command.container = {
        querySelector: () => assert.fail('dragged DOM cannot be read or replaced')
      };
      command.ensureNextActionSubscription();
      if (kind === 'station') command._stationDragActive = true;
      else {
        command.ensureDetachmentHost();
        handlers.pointerdown();
      }
      snapshot.pendingCount = 5;
      listener({ workspaceId: 'w' });
      assert.equal(command._renderDeferredByDrag, true);
      let painted = '';
      command.render = () => {
        painted = command.renderWorkFirst();
        command._renderDeferredByDrag = false;
      };
      if (kind === 'station') {
        command._stationDrag = {
          moved: true,
          el: { classList: { remove() {} }, releasePointerCapture() {} }
        };
        command.handleStationPointerUp({ type: 'pointercancel' });
      } else {
        handlers.pointerup();
        await Promise.resolve();
      }
      assert.match(painted, /5 files ready for review/);
      if (kind === 'detachment') {
        painted = '';
        command._renderDeferredByDrag = true;
        handlers.pointerup();
        command.disposeNextAction();
        await Promise.resolve();
        assert.equal(painted, '', 'teardown cancels a queued repaint');
      }
      command.disposeNextAction();
    } finally {
      globalThis.document = previous;
      delete globalThis.window;
    }
  });
}

test('HQ and group journeys do not acquire ordinary workspace guidance', () => {
  assert.equal(view([], { workspace: { designation: 'personal_hq' } }).renderWorkFirst(), '');
  assert.equal(view([], { workspace: { kind: 'group' } }).renderWorkFirst(), '');
});

// The folder's Commander waiting on the person (folder-question-said-once
// FR13 to FR16): the page hands its view to the command, the strip and the
// Commander card say the same thing, and the strip's button goes to the card.
const folderRaw = (overrides = {}) => ({
  workspace_id: 'w',
  workspace_name: 'Albums',
  folder_name: 'Albums',
  agent_name: 'Albums Manager',
  state: 'reported',
  actions: ['set_up', 'keep', 'look_further'],
  premise: {
    state: 'asked',
    sentence: 'This looks like a music folder.',
    confirm: 'Yes, show me the plan',
    other: "It's something else"
  },
  chat: { session_id: 'thread-1', message_id: 'question-1', unread: true },
  ...overrides
});
const folderEntry = (overrides = {}, review = () => true) => {
  const raw = folderRaw(overrides);
  return { view: folderAgentReportView(raw), raw, review };
};

function commandWithFolderAgent(tasks = []) {
  const command = view(tasks);
  command.active = true;
  command.viewMode = 'map';
  command.renders = 0;
  command.render = () => {
    command.renders += 1;
  };
  return command;
}

test('the strip names the waiting question and redraws only when what it waits on changes', () => {
  const command = commandWithFolderAgent();
  command.setFolderAgent('w', folderEntry());
  assert.equal(command.renders, 1);
  const html = command.renderWorkFirst();
  assert.match(html, /<h3>Albums Manager has a question about Albums<\/h3>/);
  assert.match(html, />Answer<\/button>/);
  assert.doesNotMatch(html, /New message from/);
  // No empty description line under the title.
  assert.doesNotMatch(html, /<\/h3><p>/);
  assert.equal(command.nextAction().kind, 'folder_agent');

  // The page hands over a view on every draw (the look's progress included):
  // the same words do not redraw the command.
  command.setFolderAgent('w', folderEntry({ progress: { entries: 40 } }));
  command.setFolderAgent('w', folderEntry({ updated_at: '2026-11-17T10:00:00Z' }));
  assert.equal(command.renders, 1);

  // The words change (the question is answered, the plan is ready): it redraws.
  command.setFolderAgent(
    'w',
    folderEntry({ premise: { state: 'confirmed', sentence: 'A music folder.' } })
  );
  assert.equal(command.renders, 2);
  assert.match(command.renderWorkFirst(), /Albums Manager has a plan for Albums/);
  assert.match(command.renderWorkFirst(), />Review<\/button>/);

  // The agent goes on working: the strip lets go of it.
  command.setFolderAgent(
    'w',
    folderEntry({ state: 'setting_up', setup: { status: 'running', lines: [] } })
  );
  assert.equal(command.renders, 3);
  assert.notEqual(command.nextAction().kind, 'folder_agent');

  // Another workspace's view, or none, is ignored.
  command.setFolderAgent('other', folderEntry());
  assert.equal(command.renders, 3);
  command.setFolderAgent('w', folderEntry());
  command.setFolderAgent('w', null);
  assert.notEqual(command.nextAction().kind, 'folder_agent');
});

test("the strip's button opens the Commander's chat at the message, else does what Review does (FR15)", () => {
  const opened = [];
  globalThis.window = {
    location: { search: '' },
    PersonalAssistantPanel: {
      openFolderReport: (id, ref, trigger) => {
        opened.push([id, ref.sessionID, ref.messageID, trigger]);
        return Promise.resolve(true);
      }
    }
  };
  try {
    // The question has a message in the chat: it opens there.
    const command = commandWithFolderAgent();
    let reviewed = 0;
    command.setFolderAgent(
      'w',
      folderEntry({}, () => (reviewed += 1) > 0)
    );
    bindPrimary(command).click();
    assert.equal(opened.length, 1);
    assert.deepEqual(opened[0].slice(0, 3), ['w', 'thread-1', 'question-1']);
    assert.equal(reviewed, 0, 'Review is only the fallback');

    // No message in the chat (nothing was posted): Review's own behaviour.
    const bare = commandWithFolderAgent();
    let review = 0;
    bare.setFolderAgent(
      'w',
      folderEntry({ chat: undefined }, () => (review += 1) > 0)
    );
    bindPrimary(bare).click();
    assert.equal(review, 1);
    assert.equal(opened.length, 1, 'nothing opened in the chat');
    // The button opens nothing about a ticket and starts no work.
    assert.equal(bare.openFolderAgent(), true);
  } finally {
    delete globalThis.window;
  }
});

test('the Commander card says what it waits on, unless a ticket itself needs the person (FR16)', () => {
  const working = { id: 'look', status: 'in_progress', description: 'Look around Albums' };
  const done = { id: 'look', status: 'completed', description: 'Look around Albums', result: 'x' };
  const command = commandWithFolderAgent([working]);
  // No view yet: the status comes from tickets, as before.
  assert.equal(
    command.mapAgentStatus({ tasks: [working], name: 'Albums Manager' }).label,
    'Working'
  );

  command.setFolderAgent('w', folderEntry());
  const status = command.mapAgentStatus({ tasks: [working], name: 'Albums Manager' });
  assert.deepEqual(status, {
    key: 'needs-you',
    label: 'Has a question for you',
    detail: 'Albums Manager has a question about Albums'
  });
  // The same words after the ticket finished (no stale "Ready to help").
  assert.equal(
    command.mapAgentStatus({ tasks: [done], name: 'Albums Manager' }).label,
    'Has a question for you'
  );
  // It reads as one that asks the person, not as work.
  assert.equal(command.statusTone(status.key, status.label), 'needs-input');
  // Names match case-insensitively; another agent's card is its tickets' status.
  assert.equal(
    command.mapAgentStatus({ tasks: [], name: ' albums manager ' }).label,
    'Has a question for you'
  );
  assert.equal(command.mapAgentStatus({ tasks: [working], name: 'Someone Else' }).label, 'Working');

  // A ticket that itself needs the person keeps its own status.
  const blocked = { id: 'b', status: 'blocked' };
  const needsInput = { id: 'n', status: 'in_progress', context: { execution_step_waiting: true } };
  assert.equal(
    command.mapAgentStatus({ tasks: [blocked], name: 'Albums Manager' }).label,
    'Blocked'
  );
  assert.equal(
    command.mapAgentStatus({ tasks: [needsInput], name: 'Albums Manager' }).label,
    'Needs input'
  );

  // Each other waiting moment has its own status, from the same words.
  for (const [overrides, label] of [
    [{ premise: { state: 'confirmed', sentence: 'x' } }, 'Has a plan for you'],
    [
      { premise: { state: 'something_else', sentence: 'x', withdrawn: true } },
      'Nothing planned yet'
    ],
    [{ state: 'failed', premise: undefined }, 'Could not look']
  ]) {
    command.setFolderAgent('w', folderEntry(overrides));
    assert.equal(command.mapAgentStatus({ tasks: [], name: 'Albums Manager' }).label, label);
  }

  // Not waiting (it is setting up): the tickets say it, as before.
  command.setFolderAgent(
    'w',
    folderEntry({
      state: 'setting_up',
      premise: undefined,
      setup: { status: 'running', lines: [] }
    })
  );
  assert.equal(
    command.mapAgentStatus({ tasks: [working], name: 'Albums Manager' }).label,
    'Working'
  );
});

test('the linked manager waits quietly for Filing permission only on the Map', () => {
  const command = commandWithFolderAgent();
  const receipt = { state: 'set_up', setup: { status: 'done' }, premise: undefined };
  const status = name => command.mapAgentStatus({ name, tasks: [] }).label;
  command.setFolderAgent('w', folderEntry({ ...receipt, filing_permission_required: true }));
  assert.equal(status('Albums Manager'), 'Waiting for permission');
  assert.equal(status('Someone Else'), 'Ready to help');
  command.viewMode = 'details';
  assert.equal(status('Albums Manager'), 'Ready to help');
  command.viewMode = 'map';
  command.setFolderAgent('w', folderEntry(receipt));
  assert.equal(status('Albums Manager'), 'Ready to help');
  assert.equal(command.renders, 2);
});

test('on the Map the look the Commander narrates is not the next action; Overview lists it (map-above-the-fold)', () => {
  const look = { id: 'look', status: 'in_progress', description: 'Look around Downloads' };
  const command = view([look], { commanderNarratedTicket: 'look' });
  command.viewMode = 'details';
  const overview = command.renderWorkFirst();
  assert.match(overview, /Look around Downloads/);
  assert.doesNotMatch(overview, /is-map/);
  // Nothing else to do: the Map says nothing beside the Commander's line.
  command.viewMode = 'map';
  assert.equal(command.renderWorkFirst(), '');
  // Other work still leads, as one row.
  command.page.tasks = [look, { ...running }];
  const map = command.renderWorkFirst();
  assert.match(map, /class="ws-cmd-work-first is-map"/);
  assert.match(map, /Routine work/);
  assert.doesNotMatch(map, /Look around Downloads/);
  // Without a narrated ticket the Map shows the look like any ticket.
  command.page.commanderNarratedTicket = '';
  command.page.tasks = [look];
  assert.match(command.renderWorkFirst(), /Look around Downloads/);
});
