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
    removeEventListener(type, fn) {
      if (type !== 'popstate') return;
      const index = listeners.indexOf(fn);
      if (index !== -1) listeners.splice(index, 1);
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
    backlogItems: [{ task: { id: 'backlog-1', description: 'Ship it' } }],
    workspaceId: 'ws-1',
    buildAgentGroups() {
      return [
        {
          key: 'writer',
          name: 'Writer',
          isWorkspaceAgent: true,
          instanceCount: 1,
          roles: ['Agent'],
          tasks: []
        }
      ];
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
  const subscribe = WorkspaceCommandView.prototype.ensureNextActionSubscription;
  const mounted = new Set();
  WorkspaceCommandView.prototype.ensureNextActionSubscription = function () {
    mounted.add(this);
    return subscribe.call(this);
  };
  try {
    globalThis.document = {
      getElementById: id => (id === 'workspaceCommandView' ? container : null),
      addEventListener() {},
      removeEventListener() {},
      activeElement: null
    };
    globalThis.window = harness.win;
    globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
    fn({ harness, container });
  } finally {
    // A browser tears down the page on navigation. This mutable fake must
    // explicitly release real summary timers/subscriptions before replacing it.
    for (const view of mounted) view.disposeNextAction();
    WorkspaceCommandView.prototype.ensureNextActionSubscription = subscribe;
    globalThis.document = originalDocument;
    globalThis.window = originalWindow;
    globalThis.localStorage = originalLocalStorage;
  }
}

// Old links to the retired task drawer keep working: they land on the board, on
// that ticket (PRD 760 FR-24, FR-33).
function withTicketsSpy(run) {
  const original = WorkspaceCommandView.prototype.openTicketsView;
  const opened = [];
  WorkspaceCommandView.prototype.openTicketsView = function (id) {
    opened.push(id);
  };
  try {
    run(opened);
  } finally {
    WorkspaceCommandView.prototype.openTicketsView = original;
  }
}

test('an old panel=tasks link opens that ticket on the board, and keeps the agent', () => {
  withHarness('?mode=map&panel=tasks&task=task-1&agent=writer', () => {
    withTicketsSpy(opened => {
      const view = new WorkspaceCommandView(makePage());
      assert.deepEqual(opened, ['task-1']);
      assert.equal(view.selectedAgentKey, 'writer');
    });
  });
});

test('an old link waits for the task list before reading its ticket as gone', () => {
  withHarness('?panel=tasks&task=task-1', () => {
    withTicketsSpy(opened => {
      const tasks = [];
      const page = { tasks, workspaceId: 'ws-1', buildAgentGroups: () => [] };
      const view = new WorkspaceCommandView(page);
      assert.deepEqual(opened, [], 'an empty list is not loaded yet');
      tasks.push({ id: 'task-1', status: 'pending' });
      view.refresh();
      assert.deepEqual(opened, ['task-1']);
    });
  });
});

test('an old link to a ticket that is gone still lands on the board; a stale agent falls back', () => {
  withHarness('?panel=tasks&task=deleted-task&agent=ghost-agent', () => {
    withTicketsSpy(opened => {
      const view = new WorkspaceCommandView(makePage());
      assert.deepEqual(opened, [''], 'the stale id is dropped, the board still opens');
      // The stale agent is dropped rather than applied; normal default-selection
      // (first/entry agent) takes over exactly as if no agent had been in the URL.
      assert.equal(view.selectedAgentKey, 'writer');
    });
  });
});

test('a details-mode boot with an empty query string never rewrites the clean URL', () => {
  // No agents at all, so reconcileAgentSelection has nothing to auto-select —
  // isolates the "nothing meaningfully changed" invariant from agent defaulting.
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage({ buildAgentGroups: () => [] }));
    assert.ok(view);
    assert.deepEqual(
      harness.calls,
      [],
      'no history call for an already-clean URL at the default mode'
    );
  });
});

test('selecting a different agent pushes a new history entry with the agent param (FR86, FR88)', () => {
  const twoAgents = () => [
    {
      key: 'writer',
      name: 'Writer',
      isWorkspaceAgent: true,
      instanceCount: 1,
      roles: ['Agent'],
      tasks: []
    },
    {
      key: 'editor',
      name: 'Editor',
      isWorkspaceAgent: true,
      instanceCount: 1,
      roles: ['Agent'],
      tasks: []
    }
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
    assert.equal(
      harness.calls.length,
      1,
      'exactly one replaceState for the presentation-state capture'
    );
    assert.equal(harness.calls[0][0], 'replace');
    assert.equal(
      harness.win.location.search,
      searchBefore,
      'the URL query itself is unchanged by a presentational toggle'
    );
    assert.equal(harness.win.history.state.trayCollapsed, true);
  });
});

test('Back/Forward (popstate) restores mode and agent, and an old panel=tasks entry opens the board (FR88)', () => {
  withHarness('', ({ harness }) => {
    withTicketsSpy(opened => {
      const view = new WorkspaceCommandView(makePage());
      harness.fireExternalNavigation('?mode=map&panel=tasks&task=task-2&agent=writer', {
        focusSelector: null,
        trayCollapsed: false
      });
      assert.equal(view.selectedAgentKey, 'writer');
      assert.deepEqual(opened, ['task-2']);
    });
  });
});

test('Back from an explicit Map entry restores the clean Details entry', () => {
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    assert.equal(view.viewMode, 'details');

    view.setCommandViewMode('map', { focus: false });
    assert.equal(view.viewMode, 'map');
    assert.match(harness.win.location.search, /mode=map/);

    // A clean workspace URL is the canonical Details entry. The Map preference
    // was just persisted, so resolving this popstate through local preference
    // incorrectly leaves the rendered map active even though Back removed mode.
    harness.fireExternalNavigation('?agent=writer', {
      focusSelector: null,
      trayCollapsed: false
    });
    assert.equal(view.viewMode, 'details');
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
    assert.match(
      returnParam,
      /^\/workspaces\/ws-1(\?|$)/,
      'return target is relative and workspace-scoped'
    );
    assert.ok(!returnParam.startsWith('//'), 'not protocol-relative');
    assert.ok(!/^https?:/.test(returnParam), 'not an absolute URL');
  });
});

test('boot restoration survives the real async-load race: tasks/agents are empty at construction and arrive via a later refresh', () => {
  // Reproduces a live-browser finding: WorkspaceCommandView is constructed
  // synchronously right after the page kicks off its async data load, so the
  // agent groups are still empty on the first applyBootURLState() pass.
  withHarness('?mode=map&panel=tasks&task=task-1&agent=writer', ({ harness }) => {
    withTicketsSpy(opened => {
      const tasks = [];
      const page = {
        tasks,
        workspaceId: 'ws-1',
        buildAgentGroups: () => [],
        isWorkspaceEntryAgent: () => false,
        getAgentRosterStatus: () => ({ key: 'idle', label: 'Idle' })
      };
      const view = new WorkspaceCommandView(page);
      assert.equal(view.selectedAgentKey, '');
      assert.deepEqual(opened, [], 'nothing is applied before the data is here');

      tasks.push({ id: 'task-1', status: 'pending', to: 'writer' });
      page.buildAgentGroups = () => [
        {
          key: 'writer',
          name: 'Writer',
          isWorkspaceAgent: true,
          instanceCount: 1,
          roles: ['Agent'],
          tasks: []
        }
      ];
      harness.calls.length = 0;
      view.refresh();

      assert.deepEqual(opened, ['task-1'], 'the ticket resolves once real data has loaded');
      assert.equal(view.selectedAgentKey, 'writer');
    });
  });
});

test('a boot reference to a task/agent that never appears is eventually dropped, not retried forever (FR91)', () => {
  withHarness('?panel=tasks&task=ghost-task&agent=ghost-agent', () => {
    withTicketsSpy(opened => {
      const page = { tasks: [], workspaceId: 'ws-1', buildAgentGroups: () => [] };
      const view = new WorkspaceCommandView(page);
      for (let i = 0; i < 25; i++) view.refresh();
      assert.deepEqual(opened, [''], 'it gives up once, on the board');
      assert.equal(view.selectedAgentKey, '');
    });
  });
});

test('boot with panel=backlog + a valid backlog item id opens the shared drawer with that item selected (FR59)', () => {
  withHarness('?panel=backlog&task=backlog-1&agent=writer', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    assert.equal(view.backlogDrawerOpen, true);
    assert.equal(view.backlogDrawerSelectedId, 'backlog-1');
    // The incoming URL was already fully valid and canonical (agent included),
    // so boot makes no history calls.
    assert.deepEqual(harness.calls, []);
  });
});

test('boot with panel=backlog validates task against backlog items, not Ready tasks (FR59)', () => {
  withHarness('?panel=backlog&task=task-1', () => {
    // task-1 is a real Ready task id but not a Backlog item id — it must not
    // leak across the two disjoint id spaces.
    const view = new WorkspaceCommandView(makePage());
    assert.equal(view.backlogDrawerOpen, true, 'panel=backlog alone is still honored');
    assert.equal(
      view.backlogDrawerSelectedId,
      '',
      'a Ready task id is not a valid backlog item id'
    );
  });
});

test('opening the Backlog drawer via openBacklogDrawer() syncs panel=backlog into the URL', () => {
  withHarness('', ({ harness }) => {
    const view = new WorkspaceCommandView(makePage());
    harness.calls.length = 0;
    view.openBacklogDrawer(null);
    const pushed = harness.calls.find(([kind]) => kind === 'push');
    assert.ok(pushed);
    assert.match(pushed[1], /panel=backlog/);
  });
});

const homeRoster = (state = 'empty') => ({
  workspace_id: 'ws-1',
  group_workspace_id: 'ws-1',
  roles: [
    {
      role_id: 'portfolio_manager',
      label: 'Music Portfolio Manager',
      scope: 'home',
      required: true,
      primary: true,
      state,
      agent: state === 'filled' ? { name: 'Portfolio Manager' } : undefined
    },
    { role_id: 'producer', label: 'Producer', scope: 'project', state: 'empty', read_only: true }
  ]
});

function withRoleRoster(win) {
  win.WorkspaceRoleRoster = {
    rowsFrom: roster =>
      roster.roles.map(role => ({
        roleId: role.role_id,
        label: role.label,
        scope: role.scope,
        state: role.state === 'filled' && role.agent ? 'filled' : 'empty',
        readOnly: Boolean(role.read_only),
        needsClear: false,
        proposed: role.proposed || null
      }))
  };
}

test('a role link opens that empty role once after the roster loads, then leaves the URL', () => {
  withHarness('?mode=map&role=portfolio_manager', ({ harness }) => {
    let stale = 0;
    harness.win.Toast = { info: () => (stale += 1) };
    withRoleRoster(harness.win);
    const view = new WorkspaceCommandView(makePage());
    assert.equal(stale, 0, 'the role key is never reported as out of date before its roster loads');
    const opened = [];
    view.openRoleCreateModal = (roleId, row) => opened.push([roleId, row.label]);

    assert.equal(view.applyBootRole(), false, 'waits for the roster');
    view.roleRoster = homeRoster();
    assert.equal(view.applyBootRole(), true);
    assert.deepEqual(opened, [['portfolio_manager', 'Music Portfolio Manager']]);
    assert.equal(harness.win.location.search.includes('role='), false);
    assert.equal(view.applyBootRole(), false, 'never twice');
    assert.equal(opened.length, 1);
  });
});

test('a role link to a filled, read-only, or unknown role opens nothing but is still removed', () => {
  for (const [search, roster] of [
    ['?role=portfolio_manager', homeRoster('filled')],
    ['?role=producer', homeRoster()],
    ['?role=deleted_role', homeRoster()]
  ]) {
    withHarness(search, ({ harness }) => {
      withRoleRoster(harness.win);
      const view = new WorkspaceCommandView(makePage());
      let opened = 0;
      view.openRoleCreateModal = () => (opened += 1);
      view.roleRoster = roster;
      assert.equal(view.applyBootRole(), false, search);
      assert.equal(opened, 0, search);
      assert.equal(harness.win.location.search.includes('role='), false, search);
    });
  }
});

test('only a program Home role without a proposal uses its template instructions', () => {
  withHarness('', () => {
    const view = new WorkspaceCommandView(makePage());
    view.roleRoster = homeRoster();
    assert.equal(view.roleUsesTemplateInstructions({ scope: 'home', proposed: null }), true);
    assert.equal(
      view.roleUsesTemplateInstructions({ scope: 'home', proposed: { system_prompt: 'x' } }),
      false
    );
    assert.equal(view.roleUsesTemplateInstructions({ scope: 'project', proposed: null }), false);
    view.roleRoster = { ...homeRoster(), group_workspace_id: 'another-home' };
    assert.equal(
      view.roleUsesTemplateInstructions({ scope: 'home', proposed: null }),
      false,
      'a project linked to a group is not the Home'
    );
  });
});

test('a Create form without a prompt sends no system_prompt; one with a prompt still does', async () => {
  const originalWindow = globalThis.window;
  const originalDocument = globalThis.document;
  const originalBootstrap = globalThis.bootstrap;
  globalThis.window = { location: { search: '', pathname: '/workspaces/ws-1' } };
  globalThis.document = { getElementById: () => null, addEventListener() {} };
  globalThis.bootstrap = { Modal: { getOrCreateInstance: () => ({ hide() {} }) } };
  try {
    const view = new WorkspaceCommandView(makePage());
    const bodies = [];
    view.fillRole = async (_roleId, body) => {
      bodies.push(body);
      return { ok: true };
    };
    view.roleCreateRoleId = 'portfolio_manager';
    view.roleCreateForm = {
      extract: () => ({ valid: true, values: { name: 'PM', model: '', provider: '' } })
    };
    await view.submitRoleCreateModal();
    view.roleCreateRoleId = 'lead';
    view.roleCreateForm = {
      extract: () => ({
        valid: true,
        values: { name: 'Lead', model: '', provider: '', systemPrompt: 'Lead well.' }
      })
    };
    await view.submitRoleCreateModal();
    assert.equal('system_prompt' in bodies[0], false);
    assert.equal(bodies[1].system_prompt, 'Lead well.');
  } finally {
    globalThis.window = originalWindow;
    globalThis.document = originalDocument;
    globalThis.bootstrap = originalBootstrap;
  }
});
