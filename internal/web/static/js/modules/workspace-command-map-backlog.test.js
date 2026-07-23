import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

// Same headless-fixture pattern as workspace-command-backlog.test.js: the
// constructor only touches the DOM via getElementById (stubbed to null), so a
// bare instance exposes the Quest Board's pure builders without a real document.
globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.window = { Toast: null, location: { search: '' } };

function makeView(backlogItems, extraPage = {}) {
  const view = new WorkspaceCommandView(null);
  view.page = {
    backlogItems,
    backlogSync: null,
    backlogIncludeDescendants: false,
    workspaceId: 'w1',
    ...extraPage
  };
  view.render = () => {};
  view.ensureBacklogDrawer = () => null;
  view.renderBacklogDrawerBody = () => {};
  return view;
}

function item(id, description, opts = {}) {
  return {
    task: { id, description, ...opts.task },
    owning_workspace_id: opts.owningWorkspaceId || 'w1',
    owning_workspace_name: opts.owningWorkspaceName || 'Alpha'
  };
}

const sixItems = [
  item('a', 'first idea'),
  item('b', 'second idea'),
  item('c', 'third idea'),
  item('d', 'fourth idea'),
  item('e', 'fifth idea'),
  item('f', 'sixth idea')
];

test('mapWindowOptions includes a Backlog entry whose accessible name says Backlog, not only Quest Board (FR50)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const options = view.mapWindowOptions();
  const backlog = options.find(o => o.key === 'backlog');
  assert.ok(backlog, 'a backlog tool-belt entry exists');
  assert.match(backlog.label, /Backlog/, 'accessible name/title mentions Backlog explicitly');
});

test('renderMapWindow dispatches the backlog key to renderMapBacklogPanel', () => {
  const view = makeView([]);
  view.activeMapWindow = 'backlog';
  const html = view.renderMapWindow(null);
  assert.match(html, /Quest Board/);
  assert.match(html, /ws-cmd-map-window-backlog/);
});

test('renderMapBacklogPanel shows the local count and up to 4 ranked items (FR51)', () => {
  const view = makeView(sixItems);
  const html = view.renderMapBacklogPanel();
  assert.match(html, /Quest Board/);
  // kicker carries "Backlog" as supporting copy even though the title says Quest Board.
  assert.match(html, /<span>Backlog<\/span>/);
  const rowMatches = html.match(/data-cmd-map-open-backlog="/g) || [];
  assert.equal(rowMatches.length, 4, 'no more than 4 ranked items shown (FR51)');
  // No separate Add action here — it's redundant with the drawer's own Add
  // once Open Backlog gets you there, and previously read as a near-duplicate
  // of "Open Backlog" sitting right next to it (user feedback).
  assert.ok(!html.includes('data-cmd-backlog-add'), 'no Add action in the Quest Board window');
  assert.ok(html.includes('Open Backlog'));
});

test('renderMapBacklogPanel shows an inviting empty state (FR51)', () => {
  const view = makeView([]);
  const html = view.renderMapBacklogPanel();
  assert.match(html, /Quest Board clear/);
  assert.ok(!/no tasks/i.test(html), 'must not imply Tasks is empty');
});

test('renderMapBacklogPanel shows loading and error states distinctly (FR51)', () => {
  const loading = makeView([], { backlogLoading: true });
  assert.match(loading.renderMapBacklogPanel(), /Loading/);

  const failed = makeView([], { backlogLoadFailed: true });
  const html = failed.renderMapBacklogPanel();
  assert.match(html, /is-error/);
  assert.ok(!html.includes('Quest Board clear'), 'error state must not read as the empty state');
});

test('renderMapBacklogPanel surfaces the sync warning badge (FR91)', () => {
  const view = makeView([], { backlogSync: { warning: 'stale', last_synced_at: null } });
  const html = view.renderMapBacklogPanel();
  assert.match(html, /ws-cmd-panel-sync is-warning/);
});

test('renderMapBacklogPanel rows expose Accept Quest with Promote to Ready spelled out in the accessible name (FR9, 31, 53)', () => {
  const view = makeView([item('a', 'Ship the thing')]);
  const html = view.renderMapBacklogPanel();
  assert.match(html, /data-cmd-map-accept-quest="a"/);
  assert.match(html, /Accept Quest — Promote Ship the thing to Ready/);
  assert.match(html, />Accept Quest</, 'visible label reads Accept Quest');
});

test('renderMapBacklogPanel items are never draggable or wired as agent/map units (FR39-40, 55)', () => {
  const view = makeView(sixItems);
  const html = view.renderMapBacklogPanel();
  assert.ok(!/draggable/.test(html), 'no draggable spatial nodes');
  assert.ok(!/data-cmd-map-select-agent/.test(html), 'not wired as an agent unit');
});

test('runMapAcceptQuest calls promoteBacklogItem with the item owning-workspace id and toggles busy state', async () => {
  const calls = [];
  const view = makeView([item('a', 'Ship it', { owningWorkspaceId: 'child-1' })], {
    promoteBacklogItem: async (id, ownerId) => {
      calls.push([id, ownerId]);
      return true;
    }
  });
  const promise = view.runMapAcceptQuest('a');
  assert.equal(view.mapAcceptQuestBusyId, 'a', 'busy while the promotion is in flight');
  await promise;
  assert.deepEqual(calls, [['a', 'child-1']]);
  assert.equal(view.mapAcceptQuestBusyId, '', 'busy state clears after promotion resolves');
});

test('runMapAcceptQuest is a no-op with no item id or missing page hook', async () => {
  const view = makeView([]);
  await view.runMapAcceptQuest('');
  assert.equal(view.mapAcceptQuestBusyId, '');
});

test('runMapAcceptQuest opens the real Task modal on the promoted task and closes the Quest Board window', async () => {
  // promoteBacklogItem resolves with the BacklogItemView shape ({task, owning_workspace_id,
  // owning_workspace_name}), same as every other backlog item in this codebase — not a flat task.
  const promotedTaskFlat = { id: 'a', workspace_id: 'w1', description: 'Ship it' };
  const promotedItem = {
    task: promotedTaskFlat,
    owning_workspace_id: 'w1',
    owning_workspace_name: 'Alpha'
  };
  const view = makeView([item('a', 'Ship it')], {
    promoteBacklogItem: async () => promotedItem
  });
  view.activeMapWindow = 'backlog';
  const openForEditCalls = [];
  window.taskModalController = {
    openForEdit: (task, onSave) => openForEditCalls.push({ task, onSave })
  };
  try {
    await view.runMapAcceptQuest('a');
    assert.equal(
      view.activeMapWindow,
      '',
      'Quest Board window closes so the modal is never stacked on top'
    );
    assert.equal(openForEditCalls.length, 1);
    assert.deepEqual(
      openForEditCalls[0].task,
      promotedTaskFlat,
      'the modal is opened with the unwrapped flat task, not the {task:...} wrapper'
    );
  } finally {
    delete window.taskModalController;
  }
});

test('opening the shared drawer from the Quest Board closes the Map window first (FR52)', () => {
  const view = makeView(sixItems);
  view.activeMapWindow = 'backlog';
  view.openBacklogDrawer({ focus() {} }, { selectId: 'c' });
  assert.equal(view.activeMapWindow, '', 'the Map window is closed before the drawer opens');
  assert.equal(view.backlogDrawerOpen, true);
  assert.equal(view.backlogDrawerSelectedId, 'c');
});

test('closing the drawer opened from the Map returns focus to the triggering Map control (FR52)', () => {
  const view = makeView(sixItems);
  view.activeMapWindow = 'backlog';
  let focused = false;
  const trigger = { focus: () => (focused = true) };
  view.openBacklogDrawer(trigger, { selectId: 'a' });
  view.closeBacklogDrawer();
  assert.equal(view.backlogDrawerOpen, false);
  assert.equal(focused, true);
});

// ---------- New Quest intent choice (FR56) ----------

function makeComposerView(extraPage = {}) {
  const view = new WorkspaceCommandView(null);
  view.page = { workspaceId: 'w1', ...extraPage };
  view.render = () => {};
  return view;
}

test('openTaskComposer resets the intent to ready (direct Ready creation remains the default)', () => {
  const view = makeComposerView();
  view.taskComposerIntent = 'backlog';
  view.openTaskComposer();
  assert.equal(view.taskComposerIntent, 'ready');
});

test('renderMapQuickTask shows Create & Start only for the Ready intent, not for Add to Backlog (FR56)', () => {
  const view = makeComposerView();
  view.taskComposerOpen = true;
  view.taskComposerIntent = 'ready';
  assert.match(view.renderMapQuickTask(), /data-cmd-map-quest-start/);

  view.taskComposerIntent = 'backlog';
  const html = view.renderMapQuickTask();
  assert.ok(
    !html.includes('data-cmd-map-quest-start'),
    'no Create & Start when saving to the backlog'
  );
  assert.match(html, /Add to Backlog/);
});

test('renderMapQuickTask shows the commitment consequence text for each intent (FR56)', () => {
  const view = makeComposerView();
  view.taskComposerOpen = true;

  view.taskComposerIntent = 'ready';
  assert.match(view.renderMapQuickTask(), /Commits now/);

  view.taskComposerIntent = 'backlog';
  assert.match(view.renderMapQuickTask(), /Saves the idea without committing it/);
});

test('setTaskComposerIntent toggles state and clears a stale error', () => {
  const view = makeComposerView();
  view.taskComposerOpen = true;
  view.taskComposerError = 'Enter a quest description.';
  view.setTaskComposerIntent('backlog');
  assert.equal(view.taskComposerIntent, 'backlog');
  assert.equal(view.taskComposerError, '');
});

test('submitTaskComposer with backlog intent calls createBacklogItem, not createTask (FR11-12, 56)', async () => {
  const calls = { backlog: [], task: [] };
  const view = makeComposerView({
    createBacklogItem: async input => {
      calls.backlog.push(input);
      return { id: 'new-1' };
    },
    createTask: async (...args) => {
      calls.task.push(args);
      return { id: 'ready-1' };
    }
  });
  view.taskComposerOpen = true;
  view.taskComposerIntent = 'backlog';
  view.taskComposerDraft = 'A rough idea';
  await view.submitTaskComposer({ start: false });
  assert.deepEqual(calls.backlog, [{ description: 'A rough idea' }]);
  assert.equal(calls.task.length, 0, 'createTask is never called for the backlog intent');
  assert.equal(view.taskComposerOpen, false, 'composer closes on success');
  assert.equal(view.taskComposerDraft, '');
});

test('submitTaskComposer with backlog intent surfaces an error and keeps the composer open on failure', async () => {
  const view = makeComposerView({ createBacklogItem: async () => null });
  view.taskComposerOpen = true;
  view.taskComposerIntent = 'backlog';
  view.taskComposerDraft = 'A rough idea';
  await view.submitTaskComposer({ start: false });
  assert.equal(view.taskComposerOpen, true, 'stays open so the draft is not lost');
  assert.match(view.taskComposerError, /Could not add to the backlog/);
});

test('submitTaskComposer with the default ready intent still calls createTask unchanged (regression)', async () => {
  const calls = { backlog: [], task: [] };
  const view = makeComposerView({
    createBacklogItem: async input => {
      calls.backlog.push(input);
      return { id: 'new-1' };
    },
    createTask: async (...args) => {
      calls.task.push(args);
      return { id: 'ready-1', to: 'writer' };
    }
  });
  view.taskComposerOpen = true;
  view.taskComposerIntent = 'ready';
  view.taskComposerDraft = 'Ship the release';
  await view.submitTaskComposer({ start: false });
  assert.equal(calls.backlog.length, 0);
  assert.equal(calls.task.length, 1);
});
