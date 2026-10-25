import test from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

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
    const calls = [];
    command.openTaskDrawer = () => calls.push(['drawer', command.taskDrawerSelectedId]);
    command.runDrawerAction = (...args) => calls.push(args);
    command.runWorkAction(action, 'task', null);
    assert.deepEqual(calls, [
      ['drawer', 'task'],
      [action, 'task']
    ]);
  });
}

test('loading and failure never masquerade as an idle workspace; Retry has one owner', () => {
  assert.match(view(undefined).renderWorkFirst(), /Tasks are loading/);
  assert.match(view([], { tasksLoading: true }).renderWorkFirst(), /Tasks are loading/);
  const failed = view([], { tasksLoadFailed: true });
  assert.match(failed.renderWorkFirst(), /Retry workspace status/);
  assert.doesNotMatch(failed.renderWorkFirst(), /Give Task|No open work/);
  assert.match(failed.drawerListHTML(), /Tasks unavailable/);
  assert.match(failed.taskDrawerHTML(), /—/);
  assert.match(view([]).renderWorkFirst(), /Give Task/);
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

test('stale or newly repair-gated primary actions cannot start execution', () => {
  const command = view([{ id: 't', status: 'blocked' }]);
  command.openTaskDrawer = () => {};
  command.runDrawerAction = () => assert.fail('must review the changed task first');
  command.runWorkAction('start', 't');
  assert.match(command.workNotice, /changed/);
  command.runWorkAction('retry', 'deleted');
  assert.equal(command.taskDrawerSelectedId, 'deleted');
});

test('explicit deep-link selection survives loading and a failed read', () => {
  const command = view([], { tasksLoading: true });
  command.taskDrawerOpen = true;
  command.taskDrawerSelectedId = 'requested';
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'requested');
  command.page.tasksLoading = false;
  command.page.tasksLoadFailed = true;
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'requested');
  command.page.tasksLoadFailed = false;
  command.reconcileDrawerSelection();
  assert.match(command._drawerAnnounce, /no longer available/);
});

test('summary-only drawer links acquire data without overriding an explicit task', () => {
  const command = view([]);
  command.taskDrawerOpen = true;
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, '');
  command.page.tasks = [running, completed];
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'run');
  command.taskDrawerSelectedId = 'done';
  command.reconcileDrawerSelection();
  assert.equal(command.taskDrawerSelectedId, 'done');
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
  assert.match(primary.html, /All Tasks/);
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
  assert.ok(map.includes(details));
  assert.match(map, /Workspace operations map/);
  assert.match(map, /data-cmd-open-task-drawer/);
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
    assert.match(writes[2], /Tasks are unavailable/);
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
