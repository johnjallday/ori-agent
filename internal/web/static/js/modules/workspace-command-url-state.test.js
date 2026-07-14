import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceCommandView } from './workspace-command.js';

/**
 * A minimal, mutable fake of window.history + window.location good enough to
 * exercise push/replace/popstate semantics without a real DOM (FR86-FR90).
 */
function makeHistoryHarness(initialSearch = '') {
  const calls = [];
  const location = { pathname: '/workspaces/ws-1', search: initialSearch, href: '' };
  location.href = location.pathname + location.search;
  let state = null;
  const listeners = [];
  const history = {
    get state() {
      return state;
    },
    pushState(nextState, _title, url) {
      calls.push(['push', url]);
      state = nextState;
      applyUrl(url);
    },
    replaceState(nextState, _title, url) {
      calls.push(['replace', url]);
      state = nextState;
      applyUrl(url);
    }
  };
  function applyUrl(url) {
    const u = String(url || '');
    const qIndex = u.indexOf('?');
    location.pathname = qIndex === -1 ? u : u.slice(0, qIndex);
    location.search = qIndex === -1 ? '' : u.slice(qIndex);
    location.href = location.pathname + location.search;
  }
  const win = {
    location,
    history,
    addEventListener(type, fn) {
      if (type === 'popstate') listeners.push(fn);
    },
    Toast: { info() {} }
  };
  return {
    win,
    calls,
    fireExternalNavigation(search, historyState) {
      // Simulate the browser moving Back/Forward: it updates location+state
      // itself (no history.pushState call from us) before firing popstate.
      location.search = search;
      location.href = location.pathname + location.search;
      state = historyState || null;
      listeners.forEach(fn => fn({ state }));
    }
  };
}

function makeContainer() {
  const nodes = {};
  return {
    hidden: true,
    innerHTML: '',
    children: [],
    appendChild(el) {
      this.children.push(el);
    },
    querySelector(sel) {
      return nodes[sel] || null;
    },
    querySelectorAll() {
      return [];
    },
    _register(sel, el) {
      nodes[sel] = el;
    }
  };
}

function makePage(over = {}) {
  return {
    tasks: [
      { id: 'task-1', status: 'pending', to: 'writer' },
      { id: 'task-2', status: 'in_progress', to: 'writer' }
    ],
    workspaceId: 'ws-1',
    buildAgentGroups() {
      return [{ key: 'writer', name: 'Writer', isWorkspaceAgent: true, instanceCount: 1, roles: ['Agent'], tasks: [] }];
    },
    isWorkspaceEntryAgent: () => false,
    getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' }),
    ...over
  };
}

function withHarness(initialSearch, fn) {
  const originalDocument = globalThis.document;
  const originalWindow = globalThis.window;
  const originalLocalStorage = globalThis.localStorage;
  const harness = makeHistoryHarness(initialSearch);
  const container = makeContainer();
  try {
    globalThis.document = {
      getElementById: id => (id === 'workspaceCommandView' ? container : null),
      addEventListener() {},
      activeElement: null
    };
    globalThis.window = harness.win;
    globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
    fn({ harness, container });
  } finally {
    globalThis.document = originalDocument;
    globalThis.window = originalWindow;
    globalThis.localStorage = originalLocalStorage;
  }
}

test('boot with valid mode+panel+task+agent applies all of them (FR80, FR82-84)', () => {
  withHarness('?mode=map&panel=tasks&task=task-1&agent=writer', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    assert.equal(view.viewMode, 'map');
    assert.equal(view.taskDrawerOpen, true);
    assert.equal(view.taskDrawerSelectedId, 'task-1');
    assert.equal(view.selectedAgentKey, 'writer');
    // The incoming URL was already fully valid and canonical, so boot makes
    // NO history calls — proving it doesn't needlessly rewrite an accurate URL.
    assert.deepEqual(harness.calls, []);
  });
});

test('boot drops a stale task id; a stale agent falls back to the default selection (FR91)', () => {
  withHarness('?panel=tasks&task=deleted-task&agent=ghost-agent', () => {
    const view = new WorkspaceCommandView(makePage());
    assert.equal(view.taskDrawerSelectedId, '', 'stale task id dropped, not applied');
    assert.equal(view.taskDrawerOpen, true, 'panel=tasks alone is still honored');
    // The stale agent is dropped rather than applied; normal default-selection
    // (first/entry agent) takes over exactly as if no agent had been in the URL.
    assert.equal(view.selectedAgentKey, 'writer');
  });
});

test('a details-mode boot with an empty query string never rewrites the clean URL', () => {
  // No agents at all, so reconcileAgentSelection has nothing to auto-select —
  // isolates the "nothing meaningfully changed" invariant from agent defaulting.
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage({ buildAgentGroups: () => [] }));
    assert.ok(view);
    assert.deepEqual(harness.calls, [], 'no history call for an already-clean URL at the default mode');
  });
});

test('selecting a different agent pushes a new history entry with the agent param (FR86, FR88)', () => {
  const twoAgents = () => [
    { key: 'writer', name: 'Writer', isWorkspaceAgent: true, instanceCount: 1, roles: ['Agent'], tasks: [] },
    { key: 'editor', name: 'Editor', isWorkspaceAgent: true, instanceCount: 1, roles: ['Agent'], tasks: [] }
  ];
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage({ buildAgentGroups: twoAgents }));
    assert.equal(view.selectedAgentKey, 'writer', 'the first agent auto-selects at boot');
    harness.calls.length = 0; // clear the boot normalization call
    view.selectAgent('Editor', { focus: false });
    const pushed = harness.calls.find(([kind]) => kind === 'push');
    assert.ok(pushed, 'selecting a different agent pushes a history entry');
    assert.match(pushed[1], /agent=editor/);
  });
});

test('re-selecting the same agent does not push a duplicate entry (FR86 no-op dedup)', () => {
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    assert.equal(view.selectedAgentKey, 'writer', 'auto-selected at boot');
    harness.calls.length = 0;
    view.selectAgent('Writer', { focus: false }); // reselecting the same agent
    assert.deepEqual(harness.calls, []);
  });
});

test('collapsing the tray is presentational: no push/replace with a new URL, only history.state updates (FR87-FR88)', () => {
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    view.execController = { getSelectedTaskId: () => '' };
    const searchBefore = harness.win.location.search;
    harness.calls.length = 0;
    view.toggleTrayCollapsed();
    assert.equal(harness.calls.length, 1, 'exactly one replaceState for the presentation-state capture');
    assert.equal(harness.calls[0][0], 'replace');
    assert.equal(harness.win.location.search, searchBefore, 'the URL query itself is unchanged by a presentational toggle');
    assert.equal(harness.win.history.state.trayCollapsed, true);
  });
});

test('Back/Forward (popstate) restores mode, panel, task, and agent (FR88)', () => {
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    harness.fireExternalNavigation('?mode=map&panel=tasks&task=task-2&agent=writer', {
      drawerScroll: 0,
      focusSelector: null,
      trayCollapsed: false
    });
    assert.equal(view.viewMode, 'map');
    assert.equal(view.taskDrawerOpen, true);
    assert.equal(view.taskDrawerSelectedId, 'task-2');
    assert.equal(view.selectedAgentKey, 'writer');
  });
});

test('popstate restores the captured trayCollapsed presentation flag from history.state (FR88-FR89)', () => {
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    view.trayCollapsed = false;
    harness.fireExternalNavigation('', { trayCollapsed: true });
    assert.equal(view.trayCollapsed, true);
  });
});

test('taskHrefWithReturn builds a safe, workspace-scoped href with a validated return param (FR92-93)', () => {
  withHarness('', () => {
    const view = new WorkspaceCommandView(makePage());
    view.selectedAgentKey = 'writer';
    const href = view.taskHrefWithReturn('task-1');
    assert.match(href, /^\/workspaces\/ws-1\/task\/task-1\?return=/);
    const returnParam = decodeURIComponent(href.split('return=')[1]);
    assert.match(returnParam, /^\/workspaces\/ws-1(\?|$)/, 'return target is relative and workspace-scoped');
    assert.ok(!returnParam.startsWith('//'), 'not protocol-relative');
    assert.ok(!/^https?:/.test(returnParam), 'not an absolute URL');
  });
});

test('boot restoration survives the real async-load race: tasks/agents are empty at construction and arrive via a later refresh() (regression)', () => {
  // Reproduces a live-browser finding: WorkspaceCommandView is constructed
  // synchronously right after the page kicks off its async data load, so
  // page.tasks/agent groups are still [] on the first applyBootURLState() pass.
  // A naive `Array.isArray(page.tasks)` gate treated that empty array as
  // "loaded", sanitized the boot URL against zero valid ids, and permanently
  // dropped the task+agent before the real data ever arrived.
  withHarness('?mode=map&panel=tasks&task=task-1&agent=writer', ({ harness }) => {
    let tasks = []; // starts empty, exactly like the real page constructor
    const page = {
      tasks,
      workspaceId: 'ws-1',
      buildAgentGroups: () => [], // no agents loaded yet either
      isWorkspaceEntryAgent: () => false,
      getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' })
    };
    const view = new WorkspaceCommandView(page);

    // First pass (construction/activate()): data isn't here yet, nothing applied.
    assert.equal(view.taskDrawerSelectedId, '');
    assert.equal(view.selectedAgentKey, '');

    // Real data arrives; the page mutates `page.tasks` in place and swaps in a
    // real buildAgentGroups, then calls refresh() exactly as workspace-detail.js
    // does in loadTasks()'s finally block.
    tasks.push({ id: 'task-1', status: 'pending', to: 'writer' });
    page.buildAgentGroups = () => [
      { key: 'writer', name: 'Writer', isWorkspaceAgent: true, instanceCount: 1, roles: ['Agent'], tasks: [] }
    ];
    harness.calls.length = 0;
    view.refresh();

    assert.equal(view.taskDrawerOpen, true);
    assert.equal(view.taskDrawerSelectedId, 'task-1', 'the task now resolves once real data has loaded');
    assert.equal(view.selectedAgentKey, 'writer', 'the agent now resolves once real data has loaded');
  });
});

test('a boot reference to a task/agent that never appears is eventually dropped, not retried forever (FR91)', () => {
  withHarness('?panel=tasks&task=ghost-task&agent=ghost-agent', () => {
    const page = { tasks: [], workspaceId: 'ws-1', buildAgentGroups: () => [] };
    const view = new WorkspaceCommandView(page);
    // Simulate many refreshes of a workspace that genuinely never gets this
    // task/agent (deleted, or a bad link) — it must give up eventually.
    for (let i = 0; i < 25; i++) view.refresh();
    assert.equal(view.taskDrawerSelectedId, '');
    assert.equal(view.selectedAgentKey, '');
  });
});

test('opening the drawer syncs the URL; closing it removes panel/task from the URL', () => {
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    harness.calls.length = 0;
    view.openTaskDrawer(null);
    let pushed = harness.calls.find(([kind]) => kind === 'push');
    assert.ok(pushed);
    assert.match(pushed[1], /panel=tasks/);

    harness.calls.length = 0;
    view.closeTaskDrawer();
    pushed = harness.calls.find(([kind]) => kind === 'push');
    assert.ok(pushed);
    assert.ok(!pushed[1].includes('panel=tasks'), 'panel/task cleared from the URL on close');
  });
});
