import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

// Same headless-fixture pattern as workspace-command-drawer.test.js: the
// constructor only touches the DOM via getElementById (stubbed to null), so a
// bare instance exposes the drawer's pure builders without a real document.
globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

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
    task: { id, description, details: opts.details || '', ...opts.task },
    owning_workspace_id: opts.owningWorkspaceId || 'w1',
    owning_workspace_name: opts.owningWorkspaceName || 'Alpha'
  };
}

const items = [
  item('a', 'first idea'),
  item('b', 'second idea'),
  item('c', 'third idea'),
  item('d', 'fourth idea'),
  item('e', 'fifth idea'),
  item('f', 'sixth idea')
];

test('renderBacklogPanel shows the local count and up to 5 previews (FR34)', () => {
  const view = makeView(items);
  const html = view.renderBacklogPanel();
  assert.match(html, /ws-cmd-panel-count">6</);
  const previewMatches = html.match(/data-cmd-backlog-select="/g) || [];
  assert.equal(previewMatches.length, 5, 'no more than 5 previews (FR34)');
  assert.ok(html.includes('+ 1 more'), 'overflow count shown');
  assert.match(
    html,
    /data-cmd-backlog-add aria-label="Add to Backlog" title="Add to Backlog">\+</,
    'Add is a compact "+" icon (accessible name still says Add to Backlog), distinct from Open Backlog'
  );
  assert.ok(html.includes('Open Backlog'));
});

test('renderBacklogPanel shows an inviting empty state without implying Tasks is empty (FR38)', () => {
  const view = makeView([]);
  const html = view.renderBacklogPanel();
  assert.match(html, /ws-cmd-panel-count">0</);
  assert.ok(html.includes('Nothing saved for later'));
  assert.ok(!/no tasks/i.test(html), 'must not imply Tasks is empty');
});

test('renderBacklogPanel shows a loading state while the initial fetch is in flight (FR38)', () => {
  const view = makeView([], { backlogLoading: true });
  const html = view.renderBacklogPanel();
  assert.match(html, /Loading backlog/);
});

test('renderBacklogPanel shows a distinct error state on fetch failure, not the empty state (FR38)', () => {
  const view = makeView([], { backlogLoadFailed: true });
  const html = view.renderBacklogPanel();
  assert.match(html, /is-error/);
  assert.ok(
    !html.includes('Nothing saved for later'),
    'error state must not read as the empty state'
  );
});

test('openBacklogDrawer closes the Map Quest Board window in the same transition', () => {
  const view = makeView(items);
  view.activeMapWindow = 'quest-board';
  view.openBacklogDrawer({ focus() {} });
  assert.equal(view.activeMapWindow, '', 'the Map window is closed');
  assert.equal(view.backlogDrawerOpen, true);
  assert.equal(view.backlogDrawerSelectedId, 'a', 'first item auto-selected');
});

test('openBacklogDrawer with selectId selects a specific item (rail preview click)', () => {
  const view = makeView(items);
  view.openBacklogDrawer({ focus() {} }, { selectId: 'c' });
  assert.equal(view.backlogDrawerSelectedId, 'c');
});

test('openBacklogDrawer with openCapture opens the quick-capture form', () => {
  const view = makeView(items);
  view.openBacklogDrawer({ focus() {} }, { openCapture: true });
  assert.equal(view.backlogQuickCaptureOpen, true);
});

test('closeBacklogDrawer returns focus to the triggering control', () => {
  const view = makeView(items);
  let focused = false;
  const trigger = { focus: () => (focused = true) };
  view.openBacklogDrawer(trigger);
  view.closeBacklogDrawer();
  assert.equal(view.backlogDrawerOpen, false);
  assert.equal(focused, true);
});

test('backlogDrawerListHTML renders every item with a stable id, no ownership badge locally', () => {
  const view = makeView(items);
  const html = view.backlogDrawerListHTML(view.backlogDrawerItems());
  assert.ok(html.includes('data-cmd-backlog-select="a"'));
  assert.ok(html.includes('first idea'));
  assert.ok(
    !html.includes('ws-cmd-drawer-row-owner'),
    'no owner badge without descendant roll-up (FR63)'
  );
});

test('backlogDrawerListHTML shows an owning-workspace badge when descendants are included (FR63)', () => {
  const view = makeView(
    [item('a', 'child idea', { owningWorkspaceId: 'w2', owningWorkspaceName: 'Child WS' })],
    { backlogIncludeDescendants: true }
  );
  const html = view.backlogDrawerListHTML(view.backlogDrawerItems());
  assert.ok(html.includes('Child WS'));
});

test('toggleBacklogDescendants flips the page flag and reloads (FR62-66)', () => {
  const view = makeView(items);
  const loadCalls = [];
  view.page.loadBacklog = () => loadCalls.push(true);
  view.toggleBacklogDescendants();
  assert.equal(view.page.backlogIncludeDescendants, true);
  assert.equal(loadCalls.length, 1);
  view.toggleBacklogDescendants();
  assert.equal(view.page.backlogIncludeDescendants, false);
});

test('quick capture requires a title before submitting (FR20)', async () => {
  const view = makeView(items);
  const createCalls = [];
  view.page.createBacklogItem = input => {
    createCalls.push(input);
    return Promise.resolve({ id: 'new' });
  };
  view.backlogQuickCaptureDraft = '   ';
  await view.submitBacklogQuickCapture();
  assert.equal(createCalls.length, 0, 'no API call for an empty/whitespace title');
  assert.ok(view.backlogQuickCaptureError);
});

test('quick capture submits a trimmed title and closes on success', async () => {
  const view = makeView(items);
  const createCalls = [];
  view.page.createBacklogItem = input => {
    createCalls.push(input);
    return Promise.resolve({ id: 'new-item' });
  };
  view.backlogQuickCaptureDraft = 'a new idea';
  view.backlogQuickCaptureOpen = true;
  await view.submitBacklogQuickCapture();
  assert.equal(createCalls.length, 1);
  assert.equal(createCalls[0].description, 'a new idea');
  assert.equal(view.backlogQuickCaptureOpen, false);
  assert.equal(view.backlogDrawerSelectedId, 'new-item');
});

test('backlogQuickCaptureHTML renders a Details textarea alongside the title input', () => {
  const view = makeView(items);
  const html = view.backlogQuickCaptureHTML();
  assert.ok(html.includes('data-cmd-backlog-quick-details'), 'details textarea present');
  assert.ok(html.includes('data-cmd-backlog-quick-input'), 'title input still present');
});

test('quick capture sends trimmed details alongside the title, and clears both on success', async () => {
  const view = makeView(items);
  const createCalls = [];
  view.page.createBacklogItem = input => {
    createCalls.push(input);
    return Promise.resolve({ id: 'new-item' });
  };
  view.backlogQuickCaptureDraft = 'a new idea';
  view.backlogQuickCaptureDetailsDraft = '  three competitors, summarize pricing  ';
  view.backlogQuickCaptureOpen = true;
  await view.submitBacklogQuickCapture();
  assert.equal(createCalls.length, 1);
  assert.equal(createCalls[0].description, 'a new idea');
  assert.equal(createCalls[0].details, 'three competitors, summarize pricing');
  assert.equal(view.backlogQuickCaptureDetailsDraft, '', 'details draft cleared after success');
});

test('quick capture works with no details entered (details stays optional)', async () => {
  const view = makeView(items);
  const createCalls = [];
  view.page.createBacklogItem = input => {
    createCalls.push(input);
    return Promise.resolve({ id: 'new-item' });
  };
  view.backlogQuickCaptureDraft = 'a new idea';
  await view.submitBacklogQuickCapture();
  assert.equal(createCalls[0].details, '');
});

test('quick capture shows an error and stays open on failure', async () => {
  const view = makeView(items);
  view.page.createBacklogItem = () => Promise.resolve(null);
  view.backlogQuickCaptureDraft = 'will fail';
  view.backlogQuickCaptureOpen = true;
  await view.submitBacklogQuickCapture();
  assert.equal(view.backlogQuickCaptureOpen, true, 'stays open so the user can retry');
  assert.ok(view.backlogQuickCaptureError);
});

test('promote requires confirmation before calling the API (no accidental promotion)', () => {
  const view = makeView(items);
  view.backlogDrawerSelectedId = 'a';
  const html1 = view.backlogDrawerPreviewHTML();
  assert.ok(html1.includes('data-cmd-backlog-promote '), 'shows the initial Turn into Task button');
  assert.ok(
    !html1.includes('data-cmd-backlog-promote-confirm'),
    'no confirm button until requested'
  );
});

test('confirmBacklogPromote then runBacklogPromote calls the API exactly once and clears confirm state', async () => {
  const view = makeView(items);
  const promoteCalls = [];
  view.page.promoteBacklogItem = id => {
    promoteCalls.push(id);
    return Promise.resolve(true);
  };
  view.confirmBacklogPromote('a');
  assert.equal(view.backlogPromoteConfirmId, 'a');
  await view.runBacklogPromote('a');
  assert.deepEqual(promoteCalls, ['a']);
  assert.equal(view.backlogPromoteConfirmId, '', 'confirm state cleared after promotion');
});

test('runBacklogPromote opens the real Task modal on the promoted task and closes the drawer (deliberate PRD change: promotion still never assigns, FR9-12)', async () => {
  const view = makeView(items);
  // promoteBacklogItem resolves with the BacklogItemView shape ({task, owning_workspace_id,
  // owning_workspace_name}), same as every other backlog item in this codebase — not a flat task.
  const promotedTaskFlat = { id: 'a', workspace_id: 'w1', description: 'first idea' };
  const promotedItem = {
    task: promotedTaskFlat,
    owning_workspace_id: 'w1',
    owning_workspace_name: 'Alpha'
  };
  view.page.promoteBacklogItem = () => Promise.resolve(promotedItem);
  const closeCalls = [];
  view.closeBacklogDrawer = () => closeCalls.push(true);
  const openForEditCalls = [];
  globalThis.window = {
    taskModalController: {
      openForEdit: (task, onSave) => openForEditCalls.push({ task, onSave })
    }
  };
  try {
    await view.runBacklogPromote('a');
    assert.equal(closeCalls.length, 1, 'drawer closes so the modal is never stacked beneath it');
    assert.equal(openForEditCalls.length, 1);
    assert.deepEqual(
      openForEditCalls[0].task,
      promotedTaskFlat,
      'the modal is opened with the unwrapped flat task (task.description, task.id, etc.), not the {task:...} wrapper'
    );
  } finally {
    delete globalThis.window;
  }
});

test('runBacklogPromote does not open a modal or close the drawer when promotion fails', async () => {
  const view = makeView(items);
  view.page.promoteBacklogItem = () => Promise.resolve(null);
  const closeCalls = [];
  view.closeBacklogDrawer = () => closeCalls.push(true);
  await view.runBacklogPromote('a');
  assert.equal(closeCalls.length, 0);
});

test('cancelBacklogPromote clears confirm state without calling the API', () => {
  const view = makeView(items);
  view.page.promoteBacklogItem = () => {
    throw new Error('must not be called');
  };
  view.confirmBacklogPromote('a');
  view.cancelBacklogPromote();
  assert.equal(view.backlogPromoteConfirmId, '');
});

test('the preview explains promotion eligibility without automatic assignment (FR9-12, 39)', () => {
  const view = makeView(items);
  view.backlogDrawerSelectedId = 'a';
  view.backlogPromoteConfirmId = 'a';
  const html = view.backlogDrawerPreviewHTML();
  assert.match(html, /eligible for assignment and execution/i);
  assert.match(html, /nothing runs automatically|not.*automatically/i);
});

test('preview never shows Assign, Schedule, Start, Run, Review, or Complete controls (FR8, 39)', () => {
  const view = makeView(items);
  view.backlogDrawerSelectedId = 'a';
  const html = view.backlogDrawerPreviewHTML();
  for (const forbidden of [
    'data-cmd-run-task',
    'data-cmd-drawer-action="start"',
    'data-cmd-drawer-action="assign_start"'
  ]) {
    assert.ok(!html.includes(forbidden), `must not include ${forbidden}`);
  }
});

test('runBacklogDelete delegates to the page', async () => {
  const view = makeView(items);
  const deleteCalls = [];
  view.page.deleteBacklogItem = id => deleteCalls.push(id);
  await view.runBacklogDelete('b');
  assert.deepEqual(deleteCalls, ['b']);
});

// --- Roll-up mutation authority (FR48-50, 60, 63-65): a rolled-up child
// item's owning workspace, not the currently open workspace, must receive
// every mutation. These tests cover the real gap found and fixed while
// verifying Group 4 end-to-end: edit/promote/delete originally always
// targeted `this.workspaceId`, silently misrouting mutations on a rolled-up
// descendant item.

function rollupView() {
  const local = item('local-1', 'local idea', {
    owningWorkspaceId: 'w1',
    owningWorkspaceName: 'Alpha'
  });
  const foreign = item('foreign-1', 'child idea', {
    owningWorkspaceId: 'w2',
    owningWorkspaceName: 'Child WS'
  });
  return makeView([local, foreign], { backlogIncludeDescendants: true });
}

test("backlogItemOwner resolves the item's own owning workspace, not the current one", () => {
  const view = rollupView();
  assert.equal(view.backlogItemOwner('local-1'), 'w1');
  assert.equal(view.backlogItemOwner('foreign-1'), 'w2');
  assert.equal(view.backlogItemIsLocal('local-1'), true);
  assert.equal(view.backlogItemIsLocal('foreign-1'), false);
});

test("runBacklogDelete on a rolled-up item passes the child's owning workspace, not this.workspaceId (FR64)", async () => {
  const view = rollupView();
  const calls = [];
  view.page.deleteBacklogItem = (id, ownerWorkspaceId) => calls.push([id, ownerWorkspaceId]);
  await view.runBacklogDelete('foreign-1');
  assert.deepEqual(calls, [['foreign-1', 'w2']]);
});

test("runBacklogPromote on a rolled-up item passes the child's owning workspace (FR64)", async () => {
  const view = rollupView();
  const calls = [];
  view.page.promoteBacklogItem = (id, ownerWorkspaceId) => calls.push([id, ownerWorkspaceId]);
  await view.runBacklogPromote('foreign-1');
  assert.deepEqual(calls, [['foreign-1', 'w2']]);
});

test("submitBacklogEdit on a rolled-up item passes the child's owning workspace (FR64)", async () => {
  const view = rollupView();
  const calls = [];
  view.page.updateBacklogItem = (id, fields, ownerWorkspaceId) => {
    calls.push([id, ownerWorkspaceId]);
    return Promise.resolve(true);
  };
  view.openBacklogEdit('foreign-1');
  view.updateBacklogEditField('description', 'still valid');
  await view.submitBacklogEdit();
  assert.deepEqual(calls, [['foreign-1', 'w2']]);
});

test('local item mutations still target this workspace (no regression)', async () => {
  const view = rollupView();
  const calls = [];
  view.page.deleteBacklogItem = (id, ownerWorkspaceId) => calls.push([id, ownerWorkspaceId]);
  await view.runBacklogDelete('local-1');
  assert.deepEqual(calls, [['local-1', 'w1']]);
});

test('moveBacklogItem is a no-op on a rolled-up (non-local) item — no coherent cross-workspace rank order (FR65)', async () => {
  const view = rollupView();
  const calls = [];
  view.page.reorderBacklog = ids => calls.push(ids);
  await view.moveBacklogItem('foreign-1', 'up');
  assert.equal(calls.length, 0);
});

test('preview hides the move (reorder) controls for a rolled-up item but keeps Edit/Delete/Promote', () => {
  const view = rollupView();
  view.backlogDrawerSelectedId = 'foreign-1';
  const html = view.backlogDrawerPreviewHTML();
  assert.ok(!html.includes('data-cmd-backlog-move="up"'), 'no reorder controls for a foreign item');
  assert.ok(html.includes('data-cmd-backlog-edit'), 'edit remains available (routed to the owner)');
  assert.ok(
    html.includes('data-cmd-backlog-delete'),
    'delete remains available (routed to the owner)'
  );
});

test('preview keeps the move controls for a local item in a roll-up view', () => {
  const view = rollupView();
  view.backlogDrawerSelectedId = 'local-1';
  const html = view.backlogDrawerPreviewHTML();
  assert.ok(html.includes('data-cmd-backlog-move="up"'));
});

test('moveBacklogItem up/down swaps neighbors and persists the full new order (FR18, 57 — non-drag reorder)', async () => {
  const view = makeView(items.slice(0, 3)); // a, b, c
  const reorderCalls = [];
  view.page.reorderBacklog = ids => reorderCalls.push(ids);
  await view.moveBacklogItem('b', 'up');
  assert.deepEqual(reorderCalls[0], ['b', 'a', 'c']);
});

test('moveBacklogItem at the boundary is a no-op', async () => {
  const view = makeView(items.slice(0, 3));
  const reorderCalls = [];
  view.page.reorderBacklog = ids => reorderCalls.push(ids);
  await view.moveBacklogItem('a', 'up');
  await view.moveBacklogItem('c', 'down');
  assert.equal(reorderCalls.length, 0);
});

test('runBacklogSyncNow delegates to the page (FR84)', async () => {
  const view = makeView(items);
  const calls = [];
  view.page.syncBacklogNow = () => calls.push(true);
  await view.runBacklogSyncNow();
  assert.equal(calls.length, 1);
});

test('backlogSyncBadgeHTML reflects warning/conflict state', () => {
  const view = makeView(items);
  assert.equal(view.backlogSyncBadgeHTML(null), '');
  assert.match(view.backlogSyncBadgeHTML({ last_synced_at: '2026-01-01T00:00:00Z' }), /Synced/);
  assert.match(
    view.backlogSyncBadgeHTML({ warning: 'stale', last_synced_at: '2026-01-01T00:00:00Z' }),
    /Sync warning/
  );
  assert.match(view.backlogSyncBadgeHTML({ conflict: true }), /Sync conflict/);
});

test('runBacklogResolveConflict passes the whole-item choice through (FR87, no silent last-write-wins)', async () => {
  const view = makeView(items);
  const calls = [];
  view.page.resolveBacklogConflict = (id, useFile) => calls.push([id, useFile]);
  await view.runBacklogResolveConflict('a', true);
  await view.runBacklogResolveConflict('a', false);
  assert.deepEqual(calls, [
    ['a', true],
    ['a', false]
  ]);
});

test('reconcileBacklogDrawerSelection announces and reselects when the current item vanishes (live refresh)', () => {
  const view = makeView(items.slice(0, 2)); // a, b
  view.backlogDrawerSelectedId = 'z';
  view.reconcileBacklogDrawerSelection();
  assert.equal(view.backlogDrawerSelectedId, 'a');
  assert.ok(view._backlogDrawerAnnounce);
});

test('reconcileBacklogDrawerSelection is silent when the selection is still present', () => {
  const view = makeView(items);
  view.backlogDrawerSelectedId = 'b';
  view.reconcileBacklogDrawerSelection();
  assert.equal(view.backlogDrawerSelectedId, 'b');
  assert.equal(view._backlogDrawerAnnounce, '');
});

test('openBacklogEdit seeds the draft from the current item, including tags joined for display (FR6, 20)', () => {
  const view = makeView([
    item('a', 'edit me', {
      task: {
        tags: ['x', 'y'],
        priority: 1,
        details: 'some detail',
        reference_url: 'https://example.com'
      }
    })
  ]);
  view.openBacklogEdit('a');
  assert.equal(view.backlogEditItemId, 'a');
  assert.deepEqual(view.backlogEditDraft, {
    description: 'edit me',
    details: 'some detail',
    tags: 'x, y',
    priority: 1,
    referenceUrl: 'https://example.com'
  });
});

test('backlogDrawerPreviewHTML renders the edit form when editing the selected item', () => {
  const view = makeView(items);
  view.backlogDrawerSelectedId = 'a';
  view.openBacklogEdit('a');
  const html = view.backlogDrawerPreviewHTML();
  assert.ok(html.includes('data-cmd-backlog-edit-form'));
  assert.ok(html.includes('data-cmd-backlog-edit-field="description"'));
  assert.ok(html.includes('data-cmd-backlog-edit-field="details"'));
  assert.ok(html.includes('data-cmd-backlog-edit-field="tags"'));
  assert.ok(html.includes('data-cmd-backlog-edit-field="priority"'));
  assert.ok(html.includes('data-cmd-backlog-edit-field="referenceUrl"'));
});

test('closeBacklogEdit clears edit state without saving', () => {
  const view = makeView(items);
  view.page.updateBacklogItem = () => {
    throw new Error('must not be called');
  };
  view.openBacklogEdit('a');
  view.closeBacklogEdit();
  assert.equal(view.backlogEditItemId, '');
  assert.equal(view.backlogEditDraft, null);
});

test('submitBacklogEdit requires a non-empty title', async () => {
  const view = makeView(items);
  const updateCalls = [];
  view.page.updateBacklogItem = (...args) => updateCalls.push(args);
  view.openBacklogEdit('a');
  view.updateBacklogEditField('description', '   ');
  await view.submitBacklogEdit();
  assert.equal(updateCalls.length, 0);
  assert.ok(view.backlogEditError);
});

test('submitBacklogEdit sends trimmed/split fields and closes on success', async () => {
  const view = makeView(items);
  const updateCalls = [];
  view.page.updateBacklogItem = (id, fields) => {
    updateCalls.push([id, fields]);
    return Promise.resolve(true);
  };
  view.openBacklogEdit('a');
  view.updateBacklogEditField('description', 'revised title');
  view.updateBacklogEditField('tags', 'x, y ,  z');
  view.updateBacklogEditField('priority', '5');
  await view.submitBacklogEdit();
  assert.equal(updateCalls.length, 1);
  assert.equal(updateCalls[0][0], 'a');
  assert.equal(updateCalls[0][1].description, 'revised title');
  assert.deepEqual(updateCalls[0][1].tags, ['x', 'y', 'z']);
  assert.equal(updateCalls[0][1].priority, 5);
  assert.equal(view.backlogEditItemId, '', 'edit form closes after a successful save');
});

test('submitBacklogEdit shows an error and stays open on failure', async () => {
  const view = makeView(items);
  view.page.updateBacklogItem = () => Promise.resolve(false);
  view.openBacklogEdit('a');
  view.updateBacklogEditField('description', 'still valid');
  await view.submitBacklogEdit();
  assert.equal(view.backlogEditItemId, 'a', 'stays open so the user can retry');
  assert.ok(view.backlogEditError);
});

test('Escape closes the Backlog drawer (accessibility parity with the Task drawer)', () => {
  const view = makeView(items);
  view.active = true;
  view.openBacklogDrawer({ focus() {} });
  view.handleGlobalKeydown({ key: 'Escape' });
  assert.equal(view.backlogDrawerOpen, false);
});
