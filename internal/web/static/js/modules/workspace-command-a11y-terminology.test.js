import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

globalThis.document = { getElementById: () => null };
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };

function makeView(over = {}) {
  const view = Object.create(WorkspaceCommandView.prototype);
  Object.assign(
    view,
    {
      active: true,
      statModalSection: '',
      identityEditMode: false,
      taskDrawerOpen: false,
      backlogDrawerOpen: false,
      trayOpen: false,
      trayCollapsed: false,
      taskComposerOpen: false,
      viewMode: 'map',
      activeMapWindow: '',
      mapInventoryOpen: false,
      activeRailSection: '',
      render() {},
      renderTrayBody() {},
      closeTaskDrawer() {
        this.taskDrawerOpen = false;
      },
      closeBacklogDrawer() {
        this.backlogDrawerOpen = false;
      },
      captureHistoryPresentationState() {}
    },
    over
  );
  return view;
}

function esc(view) {
  view.handleGlobalKeydown({ key: 'Escape' });
}

test('terminology: Map window labels are presentation-only Mission/Tasks, keys unchanged (FR3-5, FR5a)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const options = view.mapWindowOptions();
  const objective = options.find(o => o.key === 'objective');
  const objectives = options.find(o => o.key === 'objectives');
  assert.equal(objective.label, 'Workspace Mission');
  assert.equal(objectives.label, 'Tasks');
  // Panel keys are untouched — activeMapWindow state and every reference to
  // them elsewhere keeps working without a rename.
  assert.equal(objective.key, 'objective');
  assert.equal(objectives.key, 'objectives');
});

test('terminology: the Map Backlog tool-belt entry names Backlog even though its window is presented as Quest Board (FR50, 53, 57)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const options = view.mapWindowOptions();
  const backlog = options.find(o => o.key === 'backlog');
  assert.ok(backlog, 'a dedicated Backlog map window option exists');
  assert.match(backlog.label, /Backlog/, 'accessible name/title never says only "Quest Board"');
});

test('terminology: the Quest Board window body carries "Backlog" as supporting copy alongside the Quest Board title (FR50)', () => {
  const view = makeView({
    page: {
      backlogItems: [],
      backlogSync: null,
      backlogIncludeDescendants: false,
      workspaceId: 'w1'
    }
  });
  const html = view.renderMapBacklogPanel();
  assert.match(html, /Quest Board/, 'presentation title is Quest Board');
  assert.match(html, /<span>Backlog<\/span>/, 'kicker names the real Backlog lifecycle');
});

test('terminology: Accept Quest always spells out Promote to Ready in its accessible name (FR9, 31, 53)', () => {
  const view = makeView({
    page: {
      backlogItems: [{ task: { id: 'b1', description: 'Ship it' } }],
      backlogSync: null,
      backlogIncludeDescendants: false,
      workspaceId: 'w1'
    }
  });
  const html = view.renderMapBacklogPanel();
  assert.match(html, />Accept Quest</, 'visible label may use the Quest metaphor');
  assert.match(
    html,
    /aria-label="Accept Quest — Promote Ship it to Ready"/,
    'accessible name spells out the real action'
  );
});

test('Escape prioritizes the Backlog drawer over the Tasks drawer (only one drawer is ever open, but Escape must still close it first)', () => {
  const view = makeView({
    backlogDrawerOpen: true,
    taskDrawerOpen: false,
    trayOpen: true,
    trayCollapsed: false
  });
  esc(view);
  assert.equal(view.backlogDrawerOpen, false);
  assert.equal(
    view.trayCollapsed,
    false,
    'tray is untouched once the Backlog drawer handled Escape'
  );
});

test('Escape collapses an expanded tray rather than closing it (FR123)', () => {
  const view = makeView({ trayOpen: true, trayCollapsed: false });
  esc(view);
  assert.equal(view.trayCollapsed, true, 'tray collapses');
  assert.equal(view.trayOpen, true, 'tray is not closed/hidden by Escape');
});

test('Escape on an already-collapsed tray falls through to the next priority tier', () => {
  const view = makeView({ trayOpen: true, trayCollapsed: true, activeMapWindow: 'objectives' });
  esc(view);
  assert.equal(view.activeMapWindow, '', 'falls through to close the map window');
});

test('Escape prioritizes the task drawer over the tray (drawer closes first)', () => {
  const view = makeView({ taskDrawerOpen: true, trayOpen: true, trayCollapsed: false });
  esc(view);
  assert.equal(view.taskDrawerOpen, false);
  assert.equal(view.trayCollapsed, false, 'tray is untouched once the drawer handled Escape');
});

test('Escape no-ops while a stat modal or identity edit is active (dialog tier takes precedence)', () => {
  const view = makeView({ trayOpen: true, trayCollapsed: false, statModalSection: 'tasks' });
  esc(view);
  assert.equal(view.trayCollapsed, false, 'the tray is not touched while a modal is open');
});

test('trayAnnounceText only changes on a real state transition, not on every render (FR127)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  const run = (taskId, state) => ({
    taskId,
    task: { description: 'Export the master' },
    presentation: { state, label: state === 'running' ? 'Running' : 'Completed' }
  });

  const first = view.trayAnnounceText(run('t1', 'running'));
  assert.match(first, /Export the master: Running/);

  // Same state again (e.g. a poll tick with no change) — same text, not a
  // fresh announcement each render.
  const second = view.trayAnnounceText(run('t1', 'running'));
  assert.equal(second, first);

  // A genuine transition updates the text.
  const third = view.trayAnnounceText(run('t1', 'completed'));
  assert.match(third, /Export the master: Completed/);
  assert.notEqual(third, first);
});

test('trayHTML never marks the raw activity log as a live region (FR127)', () => {
  const view = Object.create(WorkspaceCommandView.prototype);
  view.execController = {
    getSelected: () => ({
      taskId: 't1',
      task: { description: 'Export the master' },
      presentation: { state: 'running', label: 'Running', tone: 'active', primaryAction: null },
      activity: [{ label: 'Running' }],
      startedAt: Date.now()
    }),
    getRuns: () => [],
    getAttentionRuns: () => []
  };
  view.trayCollapsed = false;
  view.trayElapsedLabel = () => '5s';
  const html = view.trayHTML();
  assert.ok(
    !html.includes('class="ws-cmd-tray-log" role="log" aria-live'),
    'the raw log is not a live region'
  );
  assert.match(
    html,
    /ws-cmd-tray-live sr-only.*aria-live="polite"/s,
    'a small dedicated live region exists instead'
  );
});
