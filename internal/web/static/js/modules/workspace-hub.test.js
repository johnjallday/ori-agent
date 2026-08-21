import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function createClassList() {
  const values = new Set();
  return {
    add: (...classes) => classes.forEach(className => values.add(className)),
    remove: (...classes) => classes.forEach(className => values.delete(className)),
    contains: className => values.has(className),
    toggle: (className, force) => {
      const enabled = typeof force === 'boolean' ? force : !values.has(className);
      if (enabled) values.add(className);
      else values.delete(className);
      return enabled;
    },
    toString: () => Array.from(values).join(' ')
  };
}

function createElement(id = '') {
  const attributes = new Map();
  // Listeners are recorded so a test can drive a real control (the workspace
  // directory editor's Save/Reset buttons) instead of calling internals.
  const listeners = new Map();
  return {
    id,
    classList: createClassList(),
    dataset: {},
    disabled: false,
    hidden: false,
    innerHTML: '',
    style: {},
    textContent: '',
    value: '',
    listeners,
    dispatch: (type, event = {}) => {
      const handler = listeners.get(type);
      if (!handler) throw new Error(`no ${type} listener registered on #${id}`);
      return handler(event);
    },
    addEventListener: (type, handler) => listeners.set(type, handler),
    blur: () => {},
    focus: () => {},
    select: () => {},
    querySelector: () => null,
    querySelectorAll: () => [],
    removeAttribute: name => attributes.delete(name),
    setAttribute: (name, value) => attributes.set(name, String(value)),
    getAttribute: name => attributes.get(name) || null
  };
}

function flattenWorkspaces(workspaces, depth = 0, parentId = '') {
  const flattened = [];
  (workspaces || []).forEach(workspace => {
    if (!workspace) return;
    const row = { ...workspace, depth, parent_id: workspace.parent_id || parentId };
    flattened.push(row);
    if (Array.isArray(workspace.children)) {
      flattened.push(...flattenWorkspaces(workspace.children, depth + 1, workspace.id));
    }
  });
  return flattened;
}

// Faithful port of WorkspaceHubUtils.collectWorkspaceDescendantIds so cascade /
// tri-state selection logic can be exercised against a real subtree.
function collectWorkspaceDescendantIds(workspaces, rootId, { includeRoot = false } = {}) {
  const ids = [];
  if (!rootId) return ids;
  function walk(nodes, inSubtree) {
    (nodes || []).forEach(node => {
      if (!node || !node.id) return;
      const isRoot = node.id === rootId;
      const nextInSubtree = inSubtree || isRoot;
      if (nextInSubtree) {
        if (isRoot) {
          if (includeRoot) ids.push(node.id);
        } else {
          ids.push(node.id);
        }
      }
      if (node.children && node.children.length > 0) walk(node.children, nextInSubtree);
    });
  }
  walk(workspaces, false);
  return ids;
}

// Build the id -> node map the hub keeps in state.workspaceMap, used by
// parent/ancestor lookups in the selection helpers.
function buildWorkspaceMap(tree) {
  const map = new Map();
  (function walk(nodes) {
    (nodes || []).forEach(node => {
      if (!node || !node.id) return;
      map.set(node.id, node);
      if (node.children) walk(node.children);
    });
  })(tree);
  return map;
}

function createKeyboardRow({
  id,
  kind = 'workspace',
  expanded = null,
  hidden = false,
  parentRow = null
} = {}) {
  const listeners = new Map();
  const row = {
    dataset: { workspaceId: id, workspaceKind: kind },
    focused: false,
    tabIndex: 0,
    addEventListener: (type, handler) => listeners.set(type, handler),
    closest: selector => {
      if (selector === '[hidden]') return hidden ? { hidden: true } : null;
      if (selector === '.launcher-tree-node') return row._node;
      return null;
    },
    dispatchKey: (key, target = row) => {
      const event = {
        currentTarget: row,
        key,
        prevented: false,
        target,
        preventDefault() {
          this.prevented = true;
        }
      };
      const handler = listeners.get('keydown');
      if (handler) handler(event);
      return event;
    },
    focus: () => {
      row.focused = true;
    },
    getAttribute: name => (name === 'aria-expanded' ? expanded : null)
  };

  const parentChildren = parentRow
    ? { closest: selector => (selector === '.launcher-tree-node' ? parentRow._node : null) }
    : null;

  row._node = {
    parentElement: {
      closest: selector => (selector === '.launcher-tree-children' ? parentChildren : null)
    },
    querySelector: selector => (selector === ':scope > .launcher-tree-row' ? row : null)
  };

  return row;
}

function loadWorkspaceHub(overrides = {}) {
  const source = readFileSync(new URL('./workspace-hub.js', import.meta.url), 'utf8');
  const elements = new Map();
  const hubEl = createElement('workspaceHub');
  const launcherGrid = createElement('launcherGrid');
  const launcherEmpty = createElement('launcherEmptyState');
  const launcherTagFilterbar = createElement('launcherTagFilterbar');
  const launcherTagFilterChips = createElement('launcherTagFilterChips');
  const launcherTagFilterClear = createElement('launcherTagFilterClear');
  const launcherViewCards = overrides.includeCardsToggle
    ? createElement('launcherViewCards')
    : null;
  const launcherViewTree = createElement('launcherViewTree');
  const launcherViewMap = createElement('launcherViewMap');
  const launcherMap = createElement('launcherMap');
  const workspaceTags = createElement('hubWorkspaceTags');
  const mapMounts = [];

  // Workspace Directory editor controls (Issue #353). Registered for every
  // load so the launcher's root editor behaves the way the real page does.
  const workspaceRootElements = {};
  for (const id of [
    'launcherWorkspaceRootPath',
    'launcherWorkspaceRootSummary',
    'launcherWorkspaceRootMeta',
    'launcherWorkspaceRootBadge',
    'launcherWorkspaceRootEditBtn',
    'launcherWorkspaceRootEditor',
    'launcherWorkspaceRootInput',
    'launcherWorkspaceRootBrowseBtn',
    'launcherWorkspaceRootSaveBtn',
    'launcherWorkspaceRootResetBtn',
    'launcherWorkspaceRootCancelBtn'
  ]) {
    const element = createElement(id);
    workspaceRootElements[id] = element;
    elements.set(id, element);
  }

  if (!overrides.omitHubEl) elements.set('workspaceHub', hubEl);
  elements.set('launcherGrid', launcherGrid);
  elements.set('launcherEmptyState', launcherEmpty);
  elements.set('launcherTagFilterbar', launcherTagFilterbar);
  elements.set('launcherTagFilterChips', launcherTagFilterChips);
  elements.set('launcherTagFilterClear', launcherTagFilterClear);
  if (launcherViewCards) elements.set('launcherViewCards', launcherViewCards);
  elements.set('launcherViewTree', launcherViewTree);
  elements.set('launcherViewMap', launcherViewMap);
  elements.set('launcherMap', launcherMap);
  elements.set('hubWorkspaceTags', workspaceTags);

  const state = {
    launcherCollapsedGroups: new Set(),
    launcherJustExpandedGroups: new Set(),
    selectedWorkspaces: new Set(),
    workspaceMap: new Map(),
    workspaces: [],
    ...overrides.state
  };

  const storage = new Map(Object.entries(overrides.localStorage || {}));
  const sessionStorageMap = new Map();
  const toasts = [];
  const window = {
    CSS: { escape: value => String(value).replace(/"/g, '\\"') },
    EventBus: overrides.EventBus || null,
    Toast: {
      error: message => toasts.push({ level: 'error', message }),
      success: message => toasts.push({ level: 'success', message }),
      warning: message => toasts.push({ level: 'warning', message })
    },
    WorkspaceHubFiles: {
      bindFileUploadEvents: () => {},
      clearFileAttachmentState: () => {},
      loadFiles: async () => {},
      renderFiles: () => {}
    },
    WorkspaceHubModals: { bindModalEvents: () => {} },
    OriWorkspaceMap: overrides.OriWorkspaceMap || {
      mount: (container, mapState) => {
        mapMounts.push({ container, state: mapState });
      },
      unmount: () => {}
    },
    WorkspaceHubNotes: {
      createNewNote: () => {},
      loadNotes: async () => {},
      renderNotes: () => {}
    },
    WorkspaceHubSelection: { toggleSelectionMode: () => {} },
    WorkspaceHubSessions: {
      loadSessions: async () => {},
      renderSessions: () => {}
    },
    WorkspaceHubSmartInput: {
      bindEvents: () => {},
      clearField: () => {},
      resetPrompt: () => {},
      setEnabled: () => {},
      setStatus: () => {}
    },
    WorkspaceHubState: {
      getState: () => state,
      getStorageKey: () => 'oriWorkspaceHubSelectedId',
      getTasksAbortController: () => null,
      initElements: () => {},
      setTasksAbortController: () => {},
      setUIState: () => {},
      shouldRefreshForEvent: () => false,
      stopRealtime: () => {}
    },
    WorkspaceHubTasks: {
      bulkDeleteTasks: () => {},
      loadTasks: async () => {},
      renderStats: () => {},
      renderTasksList: () => {},
      resetTaskFilters: () => {}
    },
    WorkspaceHubUtils: {
      collectWorkspaceDescendantIds,
      flattenWorkspaces,
      formatDate: value => String(value || '')
    },
    addEventListener: () => {},
    clearTimeout,
    confirm: () => false,
    localStorage: {
      getItem: key => storage.get(key) || null,
      setItem: (key, value) => storage.set(key, String(value))
    },
    location: { href: '' },
    sessionStorage: {
      getItem: key => sessionStorageMap.get(key) || null,
      removeItem: key => sessionStorageMap.delete(key),
      setItem: (key, value) => sessionStorageMap.set(key, String(value))
    },
    setTimeout
  };

  const documentListeners = new Map();
  const document = {
    activeElement: null,
    addEventListener: (type, handler) => documentListeners.set(type, handler),
    removeEventListener: (type, handler) => {
      if (documentListeners.get(type) === handler) documentListeners.delete(type);
    },
    elementFromPoint: overrides.elementFromPoint || (() => null),
    getElementById: id => elements.get(id) || null,
    querySelector: selector =>
      selector === '.workspace-hub-header .hub-title-text' ? createElement('headerTitle') : null,
    querySelectorAll: () => []
  };

  const defaultFetch = async url => {
    if (String(url).includes('/api/workspaces?tree=true')) {
      return { ok: true, json: async () => ({ folders: state.workspaces }) };
    }
    return { ok: true, json: async () => ({ path: '/tmp/workspaces' }), text: async () => '' };
  };

  const context = {
    // The workspace-root read is time-boxed with an AbortController, so the
    // context needs one for the directory editor to load at all.
    AbortController,
    bootstrap: {},
    clearTimeout,
    console: overrides.console || console,
    document,
    // Mirrors browser global-scope semantics, where `window.X` and a bare
    // `X` reference are the same binding - workspace-hub.js's EventBus
    // subscription block relies on that (checks window.EventBus, then
    // calls the bare EventBus.on(...)).
    EventBus: overrides.EventBus || undefined,
    escapeHtml,
    fetch: overrides.fetch || defaultFetch,
    localStorage: window.localStorage,
    sessionStorage: window.sessionStorage,
    setTimeout,
    URLSearchParams,
    window
  };

  vm.runInNewContext(source, context, { filename: 'workspace-hub.js' });

  return {
    helpers: window.WorkspaceHub?.__test,
    launcherEmpty,
    launcherGrid,
    launcherMap,
    launcherTagFilterbar,
    launcherTagFilterChips,
    launcherTagFilterClear,
    launcherViewCards,
    launcherViewMap,
    launcherViewTree,
    mapMounts,
    documentListeners,
    state,
    storage,
    toasts,
    window,
    workspaceRootElements,
    workspaceTags
  };
}

test('workspace-hub returns early, without initializing or logging, when the hub element is missing', () => {
  const logCalls = [];
  const { helpers, window } = loadWorkspaceHub({
    omitHubEl: true,
    console: { ...console, log: (...args) => logCalls.push(args) }
  });

  assert.equal(helpers, undefined, 'initialization past the early return must not run');
  assert.equal(window.WorkspaceHub, undefined, 'window.WorkspaceHub must not be exposed');
  assert.deepEqual(logCalls, [], 'no console.log calls expected when the hub element is missing');
});

test('workspace-hub normal initialization, including EventBus listener registration, emits no console.log output', () => {
  const logCalls = [];
  const registered = [];
  const eventBus = {
    on: (name, _handler, namespace) => registered.push({ name, namespace })
  };

  const { helpers } = loadWorkspaceHub({
    EventBus: eventBus,
    console: { ...console, log: (...args) => logCalls.push(args) }
  });

  assert.ok(helpers, 'module should finish initializing with a real hub element');
  assert.deepEqual(logCalls, [], 'no console.log calls expected during normal initialization');
  assert.ok(registered.length > 0, 'EventBus.on should be called when window.EventBus is present');
  assert.ok(
    registered.every(entry => entry.namespace === 'workspaceHub'),
    'all workspace-hub EventBus subscriptions should be namespaced'
  );
});

test('launcher view preference defaults to map and persists valid values', () => {
  const { helpers, storage } = loadWorkspaceHub();

  assert.equal(helpers.normalizeLauncherView('tree'), 'tree');
  assert.equal(helpers.normalizeLauncherView('invalid'), 'map');
  assert.equal(helpers.getLauncherViewPreference(), 'map');
  assert.equal(helpers.setLauncherViewPreference('tree'), 'tree');
  assert.equal(storage.get('oriWorkspaceHubLauncherView'), 'tree');
});

test('launcher view preference migrates saved cards to map', () => {
  const { helpers, storage } = loadWorkspaceHub({
    localStorage: { oriWorkspaceHubLauncherView: 'cards' }
  });

  assert.equal(helpers.getLauncherViewPreference(), 'map');
  assert.equal(storage.get('oriWorkspaceHubLauncherView'), 'map');
  assert.equal(helpers.setLauncherViewPreference('cards'), 'map');
});

test('launcher view registers the map view and persists it', () => {
  const { helpers, storage } = loadWorkspaceHub();

  assert.equal(helpers.normalizeLauncherView('map'), 'map');
  assert.equal(helpers.normalizeLauncherView('MAP'), 'map');
  assert.equal(helpers.setLauncherViewPreference('map'), 'map');
  assert.equal(storage.get('oriWorkspaceHubLauncherView'), 'map');
});

test('getLauncherViewFromURL only trusts an explicit, valid ?view= param', () => {
  const { helpers, window } = loadWorkspaceHub();

  window.location.search = '?view=map';
  assert.equal(helpers.getLauncherViewFromURL(), 'map');

  window.location.search = '?view=tree';
  assert.equal(helpers.getLauncherViewFromURL(), 'tree');

  window.location.search = '?view=cards';
  assert.equal(helpers.getLauncherViewFromURL(), 'cards');

  // Garbage should not silently fall back to the default — that would let a typo'd
  // param mask the user's saved localStorage preference.
  window.location.search = '?view=bogus';
  assert.equal(helpers.getLauncherViewFromURL(), null);

  window.location.search = '';
  assert.equal(helpers.getLauncherViewFromURL(), null);
});

test('syncLauncherViewToURL omits the param for map and sets non-default views via replaceState', () => {
  const { helpers, window } = loadWorkspaceHub();
  window.location.pathname = '/workspaces';
  window.location.search = '';
  const calls = [];
  window.history = { replaceState: (state, title, url) => calls.push(url) };

  helpers.syncLauncherViewToURL('map');
  assert.deepEqual(calls, ['/workspaces']);

  window.location.search = '';
  helpers.syncLauncherViewToURL('tree');
  assert.deepEqual(calls, ['/workspaces', '/workspaces?view=tree']);

  window.location.search = '?view=tree';
  helpers.syncLauncherViewToURL('cards');
  assert.deepEqual(calls, ['/workspaces', '/workspaces?view=tree', '/workspaces?view=cards']);
});

test('setLauncherViewMode syncs the URL for real toggles but not for the initial bootstrap', () => {
  const { helpers, window } = loadWorkspaceHub();
  window.location.pathname = '/workspaces';
  window.location.search = '';
  const calls = [];
  window.history = { replaceState: (state, title, url) => calls.push(url) };

  // Bootstrap (initLauncherViewState's call shape): syncUrl:false, render:false.
  helpers.setLauncherViewMode('map', { persist: false, render: false, syncUrl: false });
  assert.deepEqual(calls, []);

  // A real toggle-button click uses the defaults and should update the URL.
  helpers.setLauncherViewMode('tree', { render: false });
  assert.deepEqual(calls, ['/workspaces?view=tree']);

  helpers.setLauncherViewMode('cards', { persist: false, render: false });
  assert.deepEqual(calls, ['/workspaces?view=tree', '/workspaces?view=cards']);

  helpers.setLauncherViewMode('cards', { render: false });
  assert.deepEqual(calls, ['/workspaces?view=tree', '/workspaces?view=cards', '/workspaces']);
});

test('launcher tree renders minimal hierarchy with always-available workspace checkboxes', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Platform',
      children: [
        { id: 'workspace-1', kind: 'workspace', name: 'API', parent_id: 'group-1' },
        { id: 'group-2', kind: 'group', name: 'Empty Group', parent_id: 'group-1', children: [] }
      ]
    }
  ];
  const flattened = flattenWorkspaces(workspaces);
  const { helpers, launcherEmpty, launcherGrid, state } = loadWorkspaceHub({
    state: {
      launcherCollapsedGroups: new Set(['group-2']),
      selectedWorkspaces: new Set(['workspace-1']),
      workspaces
    }
  });

  helpers.renderLauncherTree(flattened);

  assert.equal(launcherEmpty.style.display, 'none');
  assert.match(launcherGrid.innerHTML, /class="launcher-tree"/);
  assert.match(launcherGrid.innerHTML, /role="treeitem"/);
  assert.match(launcherGrid.innerHTML, /data-workspace-id="group-1"/);
  assert.match(launcherGrid.innerHTML, /aria-expanded="true"/);
  assert.match(launcherGrid.innerHTML, /class="launcher-tree-checkbox"/);
  assert.match(launcherGrid.innerHTML, /data-workspace-checkbox="workspace-1" checked/);
  // Groups are now selectable too: their rows render a real checkbox, not a placeholder.
  assert.match(launcherGrid.innerHTML, /data-workspace-checkbox="group-1"/);
  assert.match(launcherGrid.innerHTML, /data-workspace-checkbox="group-2"/);
  assert.doesNotMatch(launcherGrid.innerHTML, /launcher-tree-checkbox-placeholder/);
  assert.doesNotMatch(launcherGrid.innerHTML, /data-select-mode/);
  assert.match(launcherGrid.innerHTML, /Drop workspaces here/);
  assert.match(launcherGrid.innerHTML, /data-tree-root-drop/);
  assert.doesNotMatch(launcherGrid.innerHTML, /No description yet/);
  assert.equal(state.workspaces, workspaces);
});

test('launcher cards render always-available workspace checkboxes without select-mode attributes', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Platform',
      children: [
        {
          id: 'workspace-1',
          kind: 'workspace',
          name: 'API',
          description: 'Backend work',
          parent_id: 'group-1'
        }
      ]
    },
    { id: 'workspace-2', kind: 'workspace', name: 'UI' }
  ];
  const flattened = flattenWorkspaces(workspaces);
  const { helpers, launcherEmpty, launcherGrid } = loadWorkspaceHub({
    state: {
      selectedWorkspaces: new Set(['workspace-2']),
      workspaces
    }
  });

  helpers.renderLauncherCards(flattened);

  assert.equal(launcherEmpty.style.display, 'none');
  assert.match(launcherGrid.innerHTML, /launcher-card-item has-selection-checkbox/);
  assert.match(launcherGrid.innerHTML, /class="launcher-card-checkbox"/);
  assert.match(launcherGrid.innerHTML, /data-workspace-checkbox="workspace-1"/);
  assert.match(launcherGrid.innerHTML, /data-workspace-checkbox="workspace-2" checked/);
  // Group headers are selectable too: the header card gets a checkbox.
  assert.match(
    launcherGrid.innerHTML,
    /launcher-card-item launcher-group-header has-selection-checkbox/
  );
  assert.match(launcherGrid.innerHTML, /data-workspace-checkbox="group-1"/);
  assert.doesNotMatch(launcherGrid.innerHTML, /data-select-mode/);
});

test('launcher cards show the Idle/Working LED (from enriched active) instead of the workspace.Status chip', () => {
  const workspaces = [
    {
      id: 'workspace-1',
      kind: 'workspace',
      name: 'API',
      status: 'active',
      active: true,
      agent_count: 2,
      open_task_count: 1
    },
    {
      id: 'workspace-2',
      kind: 'workspace',
      name: 'UI',
      status: 'active',
      active: false,
      agent_count: 1,
      open_task_count: 0
    }
  ];
  const flattened = flattenWorkspaces(workspaces);
  const { helpers, launcherGrid } = loadWorkspaceHub({ state: { workspaces } });

  helpers.renderLauncherCards(flattened);

  assert.doesNotMatch(launcherGrid.innerHTML, /launcher-card-status/);
  assert.match(
    launcherGrid.innerHTML,
    /launcher-card-led-status is-working"[^>]*>\s*<span class="launcher-card-led"[^>]*><\/span>Working/
  );
  assert.match(
    launcherGrid.innerHTML,
    /launcher-card-led-status"[^>]*>\s*<span class="launcher-card-led"[^>]*><\/span>Idle/
  );
  // Canonical "N agents · M open tasks" summary, same shape as the Map tile meta.
  assert.match(launcherGrid.innerHTML, /launcher-card-summary">2 agents · 1 open task</);
  assert.match(launcherGrid.innerHTML, /launcher-card-summary">1 agent · 0 open tasks</);
});

test('launcher tree rows carry the same canonical summary as the deprecated Cards fallback, but group rows do not', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Platform',
      children: [
        {
          id: 'workspace-1',
          kind: 'workspace',
          name: 'API',
          parent_id: 'group-1',
          agent_count: 3,
          open_task_count: 2
        }
      ]
    }
  ];
  const flattened = flattenWorkspaces(workspaces);
  const { helpers, launcherGrid } = loadWorkspaceHub({ state: { workspaces } });

  helpers.renderLauncherTree(flattened);

  assert.match(launcherGrid.innerHTML, /launcher-tree-meta">3 agents · 2 open tasks</);
  // Only one tree-meta span should exist — the group row itself gets none.
  const metaCount = (launcherGrid.innerHTML.match(/launcher-tree-meta"/g) || []).length;
  assert.equal(metaCount, 1);
});

test('renderLauncherTaskBadge reads counts straight off the workspace object', () => {
  const { helpers } = loadWorkspaceHub();

  assert.equal(helpers.renderLauncherTaskBadge(null), '');
  assert.equal(helpers.renderLauncherTaskBadge({ open_task_count: 0 }), '');

  const openOnly = helpers.renderLauncherTaskBadge({
    open_task_count: 3,
    needs_attention_count: 0
  });
  assert.match(openOnly, /class="launcher-task-badge"/);
  assert.doesNotMatch(openOnly, /is-attention/);
  assert.match(openOnly, />3 open</);

  const withAttention = helpers.renderLauncherTaskBadge({
    open_task_count: 2,
    needs_attention_count: 1
  });
  assert.match(withAttention, /class="launcher-task-badge is-attention"/);
  assert.match(withAttention, /1 need attention/);
});

test('launcher cards badge sources counts from enriched workspace fields, not a fetched map', () => {
  const workspaces = [
    {
      id: 'workspace-1',
      kind: 'workspace',
      name: 'API',
      open_task_count: 2,
      needs_attention_count: 1
    },
    { id: 'workspace-2', kind: 'workspace', name: 'UI', open_task_count: 0 }
  ];
  const flattened = flattenWorkspaces(workspaces);
  const { helpers, launcherGrid } = loadWorkspaceHub({ state: { workspaces } });

  helpers.renderLauncherCards(flattened);

  assert.match(launcherGrid.innerHTML, /launcher-task-badge is-attention"[^>]*>2 open</);
});

test('launcher cards render tag chips with filter and remove controls', () => {
  const workspaces = [
    {
      id: 'workspace-1',
      kind: 'workspace',
      name: 'Song',
      tags: ['music', 'reaper', 'client:acme', 'archive']
    }
  ];
  const flattened = flattenWorkspaces(workspaces);
  const { helpers, launcherGrid } = loadWorkspaceHub({ state: { workspaces } });

  helpers.renderLauncherCards(flattened);

  assert.match(launcherGrid.innerHTML, /class="launcher-card-tags"/);
  assert.match(launcherGrid.innerHTML, /data-launcher-tag-filter="music"/);
  assert.match(launcherGrid.innerHTML, /data-workspace-tag-remove="workspace-1"/);
  assert.match(launcherGrid.innerHTML, /data-workspace-tag="reaper"/);
  assert.match(launcherGrid.innerHTML, /\+1 more/);
});

test('launcher tag filters use AND logic and retain matching groups', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Music',
      children: [
        {
          id: 'song-a',
          kind: 'workspace',
          name: 'Song A',
          parent_id: 'group-1',
          tags: ['music', 'reaper']
        },
        { id: 'song-b', kind: 'workspace', name: 'Song B', parent_id: 'group-1', tags: ['music'] }
      ]
    },
    { id: 'client', kind: 'workspace', name: 'Client', tags: ['client:acme'] }
  ];
  const { helpers, mapMounts, state } = loadWorkspaceHub({
    state: {
      launcherActiveTags: new Set(['music', 'reaper']),
      workspaces
    }
  });

  helpers.renderLauncherActiveView(flattenWorkspaces(workspaces));

  assert.equal(state.launcherActiveTags.has('music'), true);
  const mounted = mapMounts[mapMounts.length - 1].state;
  assert.deepEqual(
    mounted.workspaces.map(workspace => workspace.id),
    ['group-1', 'song-a']
  );
  assert.equal(mounted.metadata.groupPreviewById['group-1'].childCount, 1);
});

test('launcher tag filters drop stale active tags', () => {
  const workspaces = [{ id: 'workspace-1', kind: 'workspace', name: 'API', tags: ['backend'] }];
  const { helpers, state } = loadWorkspaceHub({
    state: {
      launcherActiveTags: new Set(['missing']),
      workspaces
    }
  });

  helpers.renderLauncherActiveView(flattenWorkspaces(workspaces));

  assert.equal(state.launcherActiveTags.size, 0);
});

test('launcher tag removal rolls back when patch fails', async () => {
  const workspace = { id: 'workspace-1', name: 'API', tags: ['music', 'reaper'], children: [] };
  let toastMessage = '';
  const { helpers, state, window } = loadWorkspaceHub({
    state: {
      workspaceMap: new Map([[workspace.id, workspace]]),
      workspaces: [workspace]
    },
    fetch: async (url, options = {}) => {
      if (String(url).includes('/api/workspaces?tree=true')) {
        return { ok: true, json: async () => ({ folders: [workspace] }) };
      }
      if (options.method === 'PATCH') {
        return { ok: false, text: async () => 'tag update failed' };
      }
      return { ok: true, json: async () => ({ path: '/tmp/workspaces' }), text: async () => '' };
    }
  });
  window.Toast.error = message => {
    toastMessage = message;
  };

  await helpers.removeWorkspaceTag('workspace-1', 'music');

  assert.deepEqual(Array.from(state.workspaceMap.get('workspace-1').tags), ['music', 'reaper']);
  assert.equal(toastMessage, 'tag update failed');
});

test('selected workspace summary renders read-only header tags', () => {
  const { helpers, workspaceTags } = loadWorkspaceHub();

  helpers.renderWorkspaceSummary({ id: 'workspace-1', name: 'Song', tags: ['music', 'reaper'] });

  assert.equal(workspaceTags.hidden, false);
  assert.match(workspaceTags.innerHTML, /hub-workspace-tag-chip/);
  assert.match(workspaceTags.innerHTML, /music/);

  helpers.renderWorkspaceSummary({ id: 'workspace-1', name: 'Song', tags: [] });
  assert.equal(workspaceTags.hidden, true);
  assert.equal(workspaceTags.innerHTML, '');
});

test('launcher checkbox click handlers select without triggering workspace navigation', () => {
  const listenerElement = (overrides = {}) => {
    const listeners = new Map();
    return {
      addEventListener: (type, handler) => listeners.set(type, handler),
      dispatch: (type, event = {}) => listeners.get(type)?.(event),
      listeners,
      ...overrides
    };
  };
  const workspaceRow = listenerElement({
    dataset: { workspaceId: 'workspace-1', workspaceKind: 'workspace' }
  });
  const checkbox = listenerElement({
    checked: true,
    getAttribute: name => (name === 'data-workspace-checkbox' ? 'workspace-1' : null)
  });
  const checkboxShell = listenerElement();
  const { helpers, launcherGrid, state, window } = loadWorkspaceHub({
    state: {
      workspaces: [
        {
          id: 'workspace-1',
          folder_slug: 'marketing-site',
          kind: 'workspace',
          name: 'Marketing Site'
        }
      ]
    }
  });

  launcherGrid.querySelector = () => null;
  launcherGrid.querySelectorAll = selector => {
    if (selector === '[data-workspace-id]') return [workspaceRow];
    if (selector === '[data-workspace-checkbox]') return [checkbox];
    if (selector === '.launcher-card-checkbox, .launcher-tree-checkbox') return [checkboxShell];
    return [];
  };

  helpers.bindLauncherInteractions();

  const shellClick = {
    stopped: false,
    stopPropagation() {
      this.stopped = true;
    }
  };
  checkboxShell.dispatch('click', shellClick);
  assert.equal(shellClick.stopped, true);
  assert.equal(window.location.href, '');

  checkbox.dispatch('change', { target: checkbox });
  assert.equal(state.selectedWorkspaces.has('workspace-1'), true);
  assert.equal(window.location.href, '');

  workspaceRow.dispatch('click', {});
  assert.equal(window.location.href, '/workspaces/marketing-site');
});

test('selecting a group cascades to its subtree and reconciles tri-state', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Platform',
      children: [
        { id: 'ws-a', kind: 'workspace', name: 'A', parent_id: 'group-1' },
        { id: 'ws-b', kind: 'workspace', name: 'B', parent_id: 'group-1' }
      ]
    },
    { id: 'ws-c', kind: 'workspace', name: 'C' }
  ];
  const { helpers, state } = loadWorkspaceHub({
    state: {
      workspaces,
      workspaceMap: buildWorkspaceMap(workspaces),
      selectedWorkspaces: new Set()
    }
  });

  // Checking a group selects the whole branch; top-level dedupes to the group.
  helpers.toggleLauncherWorkspaceSelection('group-1', { force: true });
  assert.equal(state.selectedWorkspaces.has('group-1'), true);
  assert.equal(state.selectedWorkspaces.has('ws-a'), true);
  assert.equal(state.selectedWorkspaces.has('ws-b'), true);
  assert.equal(helpers.groupHasSelectedDescendant('group-1'), true);
  assert.deepEqual([...helpers.getTopLevelSelectedIds()].sort(), ['group-1']);

  // Unchecking a child drops the group out of the set (renders indeterminate).
  helpers.toggleLauncherWorkspaceSelection('ws-a', { force: false });
  assert.equal(state.selectedWorkspaces.has('group-1'), false);
  assert.equal(state.selectedWorkspaces.has('ws-a'), false);
  assert.equal(state.selectedWorkspaces.has('ws-b'), true);
  assert.equal(helpers.groupHasSelectedDescendant('group-1'), true);
  assert.deepEqual([...helpers.getTopLevelSelectedIds()].sort(), ['ws-b']);

  // Re-checking the last missing child fills the branch, so the group is whole
  // again and reconciles back to selected.
  helpers.toggleLauncherWorkspaceSelection('ws-b', { force: true });
  helpers.toggleLauncherWorkspaceSelection('ws-a', { force: true });
  assert.equal(state.selectedWorkspaces.has('group-1'), true);
  assert.deepEqual([...helpers.getTopLevelSelectedIds()].sort(), ['group-1']);

  // Unchecking the group clears the whole branch.
  helpers.toggleLauncherWorkspaceSelection('group-1', { force: false });
  assert.equal(state.selectedWorkspaces.size, 0);
  assert.deepEqual([...helpers.getTopLevelSelectedIds()], []);
});

test('select-all toggles the whole tree and reports the all-selected state', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Platform',
      children: [{ id: 'ws-a', kind: 'workspace', name: 'A', parent_id: 'group-1' }]
    },
    { id: 'ws-c', kind: 'workspace', name: 'C' }
  ];
  const { helpers, state } = loadWorkspaceHub({
    state: {
      workspaces,
      workspaceMap: buildWorkspaceMap(workspaces),
      selectedWorkspaces: new Set()
    }
  });

  assert.equal(helpers.areAllLauncherWorkspacesSelected(), false);

  helpers.toggleSelectAllLauncherWorkspaces();
  assert.equal(state.selectedWorkspaces.has('group-1'), true);
  assert.equal(state.selectedWorkspaces.has('ws-a'), true);
  assert.equal(state.selectedWorkspaces.has('ws-c'), true);
  assert.equal(helpers.areAllLauncherWorkspacesSelected(), true);
  // Top-level dedupes the group's child away.
  assert.deepEqual([...helpers.getTopLevelSelectedIds()].sort(), ['group-1', 'ws-c']);

  // Toggling again clears everything.
  helpers.toggleSelectAllLauncherWorkspaces();
  assert.equal(state.selectedWorkspaces.size, 0);
  assert.equal(helpers.areAllLauncherWorkspacesSelected(), false);
});

test('launcher group row opens details on click while the caret toggles collapse', () => {
  const listenerElement = (overrides = {}) => {
    const listeners = new Map();
    return {
      addEventListener: (type, handler) => listeners.set(type, handler),
      dispatch: (type, event = {}) => listeners.get(type)?.(event),
      listeners,
      ...overrides
    };
  };

  const groupRow = listenerElement({
    dataset: { workspaceId: 'group-1', workspaceKind: 'group' }
  });
  const caretBtn = listenerElement({
    getAttribute: name => (name === 'data-group-toggle' ? 'group-1' : null)
  });

  const { helpers, launcherGrid, state, window } = loadWorkspaceHub({
    state: {
      workspaces: [
        { id: 'group-1', folder_slug: 'clients', kind: 'group', name: 'Clients', children: [] }
      ]
    }
  });

  launcherGrid.querySelector = () => null;
  launcherGrid.querySelectorAll = selector => {
    if (selector === '[data-workspace-id]') return [groupRow];
    if (selector === '[data-group-toggle]') return [caretBtn];
    return [];
  };

  helpers.bindLauncherInteractions();

  // Clicking the group's name/body opens its details page.
  groupRow.dispatch('click', {});
  assert.equal(window.location.href, '/workspaces/clients');

  // The caret toggles collapse, stops propagation, and must not navigate.
  window.location.href = '';
  const caretClick = {
    prevented: false,
    stopped: false,
    preventDefault() {
      this.prevented = true;
    },
    stopPropagation() {
      this.stopped = true;
    }
  };
  caretBtn.dispatch('click', caretClick);
  assert.equal(caretClick.stopped, true);
  assert.equal(window.location.href, '');
  assert.equal(state.launcherCollapsedGroups.has('group-1'), true);
});

test('launcher tree keyboard helpers move focus and ignore embedded controls', () => {
  const { helpers, launcherGrid, state } = loadWorkspaceHub({
    state: {
      launcherCollapsedGroups: new Set(['group-1'])
    }
  });
  const groupRow = createKeyboardRow({ id: 'group-1', kind: 'group', expanded: 'false' });
  const workspaceRow = createKeyboardRow({ id: 'workspace-1', parentRow: groupRow });
  const rows = [groupRow, workspaceRow];
  launcherGrid.querySelectorAll = selector => {
    if (selector === '.launcher-tree-row[data-workspace-id]') return rows;
    return [];
  };

  helpers.bindLauncherTreeKeyboardEvents();

  assert.equal(groupRow.tabIndex, 0);
  assert.equal(workspaceRow.tabIndex, -1);

  const downEvent = groupRow.dispatchKey('j');
  assert.equal(downEvent.prevented, true);
  assert.equal(groupRow.tabIndex, -1);
  assert.equal(workspaceRow.tabIndex, 0);
  assert.equal(workspaceRow.focused, true);

  const upEvent = workspaceRow.dispatchKey('ArrowUp');
  assert.equal(upEvent.prevented, true);
  assert.equal(groupRow.tabIndex, 0);
  assert.equal(workspaceRow.tabIndex, -1);
  assert.equal(groupRow.focused, true);

  const parentEvent = workspaceRow.dispatchKey('h');
  assert.equal(parentEvent.prevented, true);
  assert.equal(groupRow.tabIndex, 0);

  const expandEvent = groupRow.dispatchKey('l');
  assert.equal(expandEvent.prevented, true);
  assert.equal(state.launcherCollapsedGroups.has('group-1'), false);

  const inputEvent = groupRow.dispatchKey('j', { tagName: 'INPUT', closest: () => null });
  assert.equal(inputEvent.prevented, false);
  assert.equal(groupRow.tabIndex, 0);

  const modalEvent = groupRow.dispatchKey('k', {
    tagName: 'SPAN',
    closest: selector => (selector.includes('.modal.show') ? { className: 'modal show' } : null)
  });
  assert.equal(modalEvent.prevented, false);
});

test('launcher tree drop intent maps edge and middle regions', () => {
  const { helpers } = loadWorkspaceHub();
  const row = {
    classList: { contains: className => className === 'launcher-tree-row' },
    dataset: {
      nextSiblingId: 'workspace-next',
      parentId: 'group-parent',
      workspaceId: 'group-target',
      workspaceKind: 'group'
    },
    getBoundingClientRect: () => ({ top: 100, height: 30 })
  };

  assert.equal(
    JSON.stringify(helpers.getLauncherTreeDropIntent(row, { clientY: 101 })),
    JSON.stringify({
      type: 'before',
      targetParentId: 'group-parent',
      insertBeforeId: 'group-target'
    })
  );
  assert.equal(
    JSON.stringify(helpers.getLauncherTreeDropIntent(row, { clientY: 115 })),
    JSON.stringify({ type: 'into', targetParentId: 'group-target', insertBeforeId: '' })
  );
  assert.equal(
    JSON.stringify(helpers.getLauncherTreeDropIntent(row, { clientY: 129 })),
    JSON.stringify({
      type: 'after',
      targetParentId: 'group-parent',
      insertBeforeId: 'workspace-next'
    })
  );

  row.dataset.nextSiblingId = '';
  assert.equal(
    JSON.stringify(helpers.getLauncherTreeDropIntent(row, { clientY: 129 })),
    JSON.stringify({ type: 'after', targetParentId: 'group-parent', insertBeforeId: '' })
  );
});

test('launcher card drop intent moves into group cards and before workspace cards', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Clients',
      children: [{ id: 'workspace-1', kind: 'workspace', name: 'Campaign', parent_id: 'group-1' }]
    }
  ];
  const { helpers } = loadWorkspaceHub({
    state: {
      workspaces,
      workspaceMap: buildWorkspaceMap(workspaces)
    }
  });

  const groupCard = {
    classList: { contains: className => className === 'launcher-card-item' },
    dataset: { workspaceId: 'group-1', workspaceKind: 'group' }
  };
  const workspaceCard = {
    classList: { contains: className => className === 'launcher-card-item' },
    dataset: { workspaceId: 'workspace-1', workspaceKind: 'workspace' }
  };

  assert.equal(
    JSON.stringify(helpers.getLauncherCardDropIntent(groupCard)),
    JSON.stringify({ type: 'into', targetParentId: 'group-1', insertBeforeId: '' })
  );
  assert.equal(
    JSON.stringify(helpers.getLauncherCardDropIntent(workspaceCard)),
    JSON.stringify({ type: 'before', targetParentId: 'group-1', insertBeforeId: 'workspace-1' })
  );
});

test('launcher dragstart is not blocked by checkbox selection', () => {
  const listeners = new Map();
  const workspaceRow = {
    classList: createClassList(),
    dataset: { workspaceId: 'workspace-1', workspaceKind: 'workspace' },
    addEventListener: (type, handler) => listeners.set(type, handler),
    closest: () => null
  };
  const { helpers, launcherGrid, state } = loadWorkspaceHub({
    state: {
      selectedWorkspaces: new Set(['workspace-2'])
    }
  });
  launcherGrid.querySelector = () => null;
  launcherGrid.querySelectorAll = selector => {
    if (selector === '[data-workspace-id]') return [workspaceRow];
    if (selector === '[data-workspace-id][draggable="true"]') return [workspaceRow];
    return [];
  };

  helpers.bindLauncherInteractions();

  const dragData = new Map();
  const event = {
    currentTarget: workspaceRow,
    dataTransfer: {
      effectAllowed: '',
      setData: (key, value) => dragData.set(key, value)
    },
    prevented: false,
    preventDefault() {
      this.prevented = true;
    }
  };
  listeners.get('dragstart')(event);

  assert.equal(state.selectedWorkspaces.has('workspace-2'), true);
  assert.equal(event.prevented, false);
  assert.equal(dragData.get('text/plain'), 'workspace-1');
  assert.equal(workspaceRow.classList.contains('is-dragging'), true);
});

test('pointer dragging a tree row can move it to top level', async () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Clients',
      children: [
        { id: 'workspace-1', kind: 'workspace', name: 'Campaign', parent_id: 'group-1' },
        { id: 'workspace-2', kind: 'workspace', name: 'Planning', parent_id: 'group-1' }
      ]
    }
  ];
  const listeners = new Map();
  const workspaceRow = {
    classList: createClassList(),
    dataset: { workspaceId: 'workspace-1', workspaceKind: 'workspace' },
    addEventListener: (type, handler) => listeners.set(type, handler),
    closest: () => null
  };
  const rootDrop = {
    classList: createClassList(),
    addEventListener: () => {},
    closest: selector => (selector === '[data-tree-root-drop]' ? rootDrop : null)
  };
  const patches = [];
  const { helpers, launcherGrid, documentListeners } = loadWorkspaceHub({
    state: {
      workspaces,
      workspaceMap: buildWorkspaceMap(workspaces)
    },
    elementFromPoint: () => rootDrop,
    fetch: async (url, options = {}) => {
      if (String(url).includes('/api/workspaces?tree=true')) {
        return { ok: true, json: async () => ({ folders: workspaces }), text: async () => '' };
      }
      if (options.method === 'PATCH') {
        patches.push({ url: String(url), body: JSON.parse(options.body) });
      }
      return { ok: true, json: async () => ({ path: '/tmp/workspaces' }), text: async () => '' };
    }
  });
  launcherGrid.querySelector = () => null;
  launcherGrid.querySelectorAll = selector => {
    if (selector === '[data-workspace-id]') return [workspaceRow];
    if (selector === '[data-workspace-id][draggable="true"]') return [workspaceRow];
    if (selector === '[data-tree-root-drop]') return [rootDrop];
    return [];
  };

  helpers.bindLauncherInteractions();

  listeners.get('pointerdown')({
    button: 0,
    clientX: 0,
    clientY: 0,
    pointerId: 7,
    target: workspaceRow
  });
  documentListeners.get('pointermove')({
    clientX: 12,
    clientY: 0,
    pointerId: 7,
    preventDefault() {}
  });
  documentListeners.get('pointerup')({
    pointerId: 7,
    preventDefault() {},
    stopPropagation() {}
  });

  await new Promise(resolve => setTimeout(resolve, 20));

  const movedWorkspace = patches.find(patch => patch.url.endsWith('/api/workspaces/workspace-1'));
  assert.equal(movedWorkspace.body.parent_id, '');
});

test('launcher tree root drop target moves workspace to top level', async () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Clients',
      children: [
        { id: 'workspace-1', kind: 'workspace', name: 'Campaign', parent_id: 'group-1' },
        { id: 'workspace-2', kind: 'workspace', name: 'Planning', parent_id: 'group-1' }
      ]
    }
  ];
  const listeners = new Map();
  const rootDrop = {
    classList: createClassList(),
    addEventListener: (type, handler) => listeners.set(type, handler)
  };
  const patches = [];
  const { helpers, launcherGrid } = loadWorkspaceHub({
    state: {
      workspaces,
      workspaceMap: buildWorkspaceMap(workspaces)
    },
    fetch: async (url, options = {}) => {
      if (String(url).includes('/api/workspaces?tree=true')) {
        return { ok: true, json: async () => ({ folders: workspaces }), text: async () => '' };
      }
      if (options.method === 'PATCH') {
        patches.push({ url: String(url), body: JSON.parse(options.body) });
      }
      return { ok: true, json: async () => ({ path: '/tmp/workspaces' }), text: async () => '' };
    }
  });

  launcherGrid.querySelector = () => null;
  launcherGrid.querySelectorAll = selector => {
    if (selector === '[data-tree-root-drop]') return [rootDrop];
    return [];
  };

  helpers.bindLauncherInteractions();
  listeners.get('drop')({
    dataTransfer: { getData: () => 'workspace-1' },
    preventDefault() {},
    stopPropagation() {}
  });

  await new Promise(resolve => setTimeout(resolve, 20));

  const movedWorkspace = patches.find(patch => patch.url.endsWith('/api/workspaces/workspace-1'));
  assert.equal(movedWorkspace.body.parent_id, '');
});

// --- Workspace Directory editor (Issue #353) --------------------------------

// loadWorkspaceRootEditor wires the launcher with a scripted workspace-root
// endpoint so a Save/Reset click can be driven end to end and every request the
// module makes is recorded in order.
async function loadWorkspaceRootEditor({
  configuredRoot = '/roots/current',
  refresh = {},
  savePostOk = true,
  listOk = true
} = {}) {
  const requests = [];
  const harness = loadWorkspaceHub({
    state: { workspaces: [] },
    fetch: async (url, options = {}) => {
      const method = options.method || 'GET';
      const target = String(url);
      requests.push({ method, url: target, body: options.body ? JSON.parse(options.body) : null });

      if (target.includes('/api/settings/workspace-root')) {
        if (method === 'POST') {
          if (!savePostOk) {
            return { ok: false, text: async () => 'workspace directory is not a directory' };
          }
          return {
            ok: true,
            json: async () => ({
              success: true,
              workspace_root: JSON.parse(options.body).workspace_root,
              effective_workspace_root: JSON.parse(options.body).workspace_root || '/roots/default',
              source: JSON.parse(options.body).workspace_root ? 'settings' : 'default',
              confirmed: true,
              refresh
            })
          };
        }
        return {
          ok: true,
          json: async () => ({
            workspace_root: configuredRoot,
            effective_workspace_root: configuredRoot,
            default_workspace_root: '/roots/default',
            source: 'settings',
            confirmed: true
          })
        };
      }

      if (target.includes('/api/workspaces?tree=true')) {
        if (!listOk) return { ok: false, text: async () => 'boom' };
        return { ok: true, json: async () => ({ folders: [] }) };
      }

      return { ok: true, json: async () => ({}), text: async () => '' };
    }
  });

  harness.helpers.bindLauncherInteractions();
  // The module reads the current directory on load; let that settle so a test
  // drives the editor from the same state a user would see.
  await settle();
  requests.length = 0;

  // Open the editor the way the user does, so the draft is treated as theirs
  // and is not overwritten by a background directory read.
  const openEditor = () =>
    harness.workspaceRootElements.launcherWorkspaceRootEditBtn.dispatch('click');

  return { ...harness, requests, openEditor };
}

const settle = () => new Promise(resolve => setTimeout(resolve, 20));

test('describeWorkspaceRootRefresh reports each outcome without the Rescan missing-folder wording', () => {
  const { helpers } = loadWorkspaceHub();
  const describe = helpers.describeWorkspaceRootRefresh;

  const unchanged = describe({ imported: 0, warnings: [] }, 'Workspace directory saved.');
  assert.equal(unchanged.message, 'Workspace directory saved. Your workspace list is unchanged.');
  assert.equal(unchanged.level, 'success');

  const changed = describe(
    { imported: 2, restored: 1, orphaned: 3, reparented: 1, warnings: [] },
    'Workspace directory saved.'
  );
  assert.match(changed.message, /2 workspaces added/);
  assert.match(changed.message, /1 workspace restored/);
  assert.match(changed.message, /3 workspaces hidden/);
  assert.match(changed.message, /1 workspace re-grouped/);
  assert.match(changed.message, /Hidden workspaces stay on disk and return if you switch back\./);
  // Those folders are still exactly where they were, so the Rescan phrasing
  // for a genuinely vanished folder must not be reused here.
  assert.doesNotMatch(changed.message, /missing on disk/i);

  const warned = describe(
    { imported: 1, warnings: ['Failed to import Notes'] },
    'Workspace directory saved.'
  );
  assert.equal(warned.level, 'warning');
  assert.match(warned.message, /1 warning: Failed to import Notes/);

  // A response from an older server with no refresh block still reads cleanly.
  assert.equal(
    describe(undefined, 'Custom workspace directory cleared.').message,
    'Custom workspace directory cleared. Your workspace list is unchanged.'
  );
});

test('saving the workspace directory posts once, reloads the list, and never calls rescan', async () => {
  const { workspaceRootElements, requests, toasts, openEditor } = await loadWorkspaceRootEditor({
    refresh: { imported: 2, orphaned: 1, warnings: [] }
  });

  openEditor();
  workspaceRootElements.launcherWorkspaceRootInput.value = '  /roots/next  ';
  await workspaceRootElements.launcherWorkspaceRootSaveBtn.dispatch('click');
  await settle();

  const posts = requests.filter(
    request => request.method === 'POST' && request.url.includes('/api/settings/workspace-root')
  );
  assert.equal(posts.length, 1, 'the directory must be saved exactly once');
  assert.equal(posts[0].body.workspace_root, '/roots/next', 'the draft is trimmed before saving');

  const postIndex = requests.indexOf(posts[0]);
  const reload = requests
    .slice(postIndex + 1)
    .find(request => request.url.includes('/api/workspaces?tree=true'));
  assert.ok(reload, 'the workspace list must be reloaded after the save');

  // The POST already applied and reconciled the directory server-side.
  assert.equal(
    requests.filter(request => request.url.includes('/api/workspaces/rescan')).length,
    0,
    'a redundant rescan must not be issued'
  );

  assert.equal(workspaceRootElements.launcherWorkspaceRootEditor.hidden, true, 'editor closes');
  assert.deepEqual(toasts.at(-1), {
    level: 'success',
    message:
      'Workspace directory saved. 2 workspaces added, 1 workspace hidden. ' +
      'Hidden workspaces stay on disk and return if you switch back.'
  });
});

test('a workspace directory refresh with warnings is reported as a warning, not a clean success', async () => {
  const { workspaceRootElements, toasts, openEditor } = await loadWorkspaceRootEditor({
    refresh: { imported: 1, warnings: ['Failed to import Notes'] }
  });

  openEditor();
  workspaceRootElements.launcherWorkspaceRootInput.value = '/roots/next';
  await workspaceRootElements.launcherWorkspaceRootSaveBtn.dispatch('click');
  await settle();

  assert.equal(toasts.at(-1).level, 'warning');
  assert.match(toasts.at(-1).message, /1 warning: Failed to import Notes/);
});

test('clearing the workspace directory reloads the list and summarizes the refresh', async () => {
  const { workspaceRootElements, requests, toasts, openEditor } = await loadWorkspaceRootEditor({
    refresh: { restored: 2, orphaned: 1, warnings: [] }
  });

  openEditor();
  await workspaceRootElements.launcherWorkspaceRootResetBtn.dispatch('click');
  await settle();

  const posts = requests.filter(
    request => request.method === 'POST' && request.url.includes('/api/settings/workspace-root')
  );
  assert.equal(posts.length, 1);
  assert.equal(posts[0].body.workspace_root, '', 'clearing sends an empty directory');
  assert.ok(
    requests
      .slice(requests.indexOf(posts[0]) + 1)
      .some(request => request.url.includes('/api/workspaces?tree=true')),
    'the workspace list must be reloaded after clearing'
  );
  assert.equal(
    requests.filter(request => request.url.includes('/api/workspaces/rescan')).length,
    0
  );
  assert.equal(toasts.at(-1).level, 'success');
  assert.match(toasts.at(-1).message, /Custom workspace directory cleared\. 2 workspaces restored/);
});

test('a failed workspace directory save keeps the editor open and reports no success', async () => {
  const { workspaceRootElements, requests, toasts, openEditor } = await loadWorkspaceRootEditor({
    savePostOk: false
  });

  openEditor();
  workspaceRootElements.launcherWorkspaceRootInput.value = '/roots/not-a-directory';
  await workspaceRootElements.launcherWorkspaceRootSaveBtn.dispatch('click');
  await settle();

  assert.equal(
    workspaceRootElements.launcherWorkspaceRootEditor.hidden,
    false,
    'the editor must stay open so the path can be corrected'
  );
  assert.equal(
    workspaceRootElements.launcherWorkspaceRootInput.value,
    '/roots/not-a-directory',
    'the draft must be preserved'
  );
  assert.equal(toasts.at(-1).level, 'error');
  assert.match(toasts.at(-1).message, /Failed to save workspace directory/);
  assert.ok(
    !toasts.some(toast => toast.level === 'success'),
    'a failed save must never report success'
  );

  // A save that never applied must not claim to have reloaded anything.
  const posts = requests.filter(
    request => request.method === 'POST' && request.url.includes('/api/settings/workspace-root')
  );
  assert.equal(
    requests
      .slice(requests.indexOf(posts[0]) + 1)
      .filter(request => request.url.includes('/api/workspaces?tree=true')).length,
    0,
    'no list reload after a failed save'
  );
});

test('a failed list refresh after a successful save stays recoverable', async () => {
  const { workspaceRootElements, toasts, openEditor } = await loadWorkspaceRootEditor({
    listOk: false,
    refresh: { imported: 1, warnings: [] }
  });

  openEditor();
  workspaceRootElements.launcherWorkspaceRootInput.value = '/roots/next';
  await workspaceRootElements.launcherWorkspaceRootSaveBtn.dispatch('click');
  await settle();

  assert.equal(
    workspaceRootElements.launcherWorkspaceRootEditor.hidden,
    false,
    'the editor stays open so the user has somewhere to retry from'
  );
  assert.equal(toasts.at(-1).level, 'error');
  assert.match(toasts.at(-1).message, /could not be reloaded/);
  assert.ok(
    !toasts.some(toast => toast.level === 'success'),
    'a list that never refreshed must not be reported as a completed refresh'
  );
});
