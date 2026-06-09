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
    add: (...classes) => classes.forEach((className) => values.add(className)),
    remove: (...classes) => classes.forEach((className) => values.delete(className)),
    contains: (className) => values.has(className),
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
    addEventListener: () => {},
    blur: () => {},
    focus: () => {},
    querySelector: () => null,
    querySelectorAll: () => [],
    removeAttribute: (name) => attributes.delete(name),
    setAttribute: (name, value) => attributes.set(name, String(value)),
    getAttribute: (name) => attributes.get(name) || null
  };
}

function flattenWorkspaces(workspaces, depth = 0, parentId = '') {
  const flattened = [];
  (workspaces || []).forEach((workspace) => {
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
    (nodes || []).forEach((node) => {
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
    (nodes || []).forEach((node) => {
      if (!node || !node.id) return;
      map.set(node.id, node);
      if (node.children) walk(node.children);
    });
  })(tree);
  return map;
}

function createKeyboardRow({ id, kind = 'workspace', expanded = null, hidden = false, parentRow = null } = {}) {
  const listeners = new Map();
  const row = {
    dataset: { workspaceId: id, workspaceKind: kind },
    focused: false,
    tabIndex: 0,
    addEventListener: (type, handler) => listeners.set(type, handler),
    closest: (selector) => {
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
    getAttribute: (name) => (name === 'aria-expanded' ? expanded : null)
  };

  const parentChildren = parentRow
    ? { closest: (selector) => (selector === '.launcher-tree-node' ? parentRow._node : null) }
    : null;

  row._node = {
    parentElement: {
      closest: (selector) => (selector === '.launcher-tree-children' ? parentChildren : null)
    },
    querySelector: (selector) => (selector === ':scope > .launcher-tree-row' ? row : null)
  };

  return row;
}

function loadWorkspaceHub(overrides = {}) {
  const source = readFileSync(new URL('./workspace-hub.js', import.meta.url), 'utf8');
  const elements = new Map();
  const hubEl = createElement('workspaceHub');
  const launcherGrid = createElement('launcherGrid');
  const launcherEmpty = createElement('launcherEmptyState');
  const launcherViewCards = createElement('launcherViewCards');
  const launcherViewTree = createElement('launcherViewTree');

  elements.set('workspaceHub', hubEl);
  elements.set('launcherGrid', launcherGrid);
  elements.set('launcherEmptyState', launcherEmpty);
  elements.set('launcherViewCards', launcherViewCards);
  elements.set('launcherViewTree', launcherViewTree);

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
  const window = {
    CSS: { escape: (value) => String(value).replace(/"/g, '\\"') },
    EventBus: null,
    Toast: { error: () => {}, success: () => {} },
    WorkspaceHubFiles: {
      bindFileUploadEvents: () => {},
      clearFileAttachmentState: () => {},
      loadFiles: async () => {},
      renderFiles: () => {}
    },
    WorkspaceHubModals: { bindModalEvents: () => {} },
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
      formatDate: (value) => String(value || '')
    },
    addEventListener: () => {},
    clearTimeout,
    confirm: () => false,
    localStorage: {
      getItem: (key) => storage.get(key) || null,
      setItem: (key, value) => storage.set(key, String(value))
    },
    location: { href: '' },
    sessionStorage: {
      getItem: (key) => sessionStorageMap.get(key) || null,
      removeItem: (key) => sessionStorageMap.delete(key),
      setItem: (key, value) => sessionStorageMap.set(key, String(value))
    },
    setTimeout
  };

  const document = {
    activeElement: null,
    addEventListener: () => {},
    getElementById: (id) => elements.get(id) || null,
    querySelector: (selector) => selector === '.workspace-hub-header .hub-title-text' ? createElement('headerTitle') : null,
    querySelectorAll: () => []
  };

  const context = {
    bootstrap: {},
    clearTimeout,
    console,
    document,
    escapeHtml,
    fetch: async (url) => {
      if (String(url).includes('/api/workspaces?tree=true')) {
        return { ok: true, json: async () => ({ folders: state.workspaces }) };
      }
      return { ok: true, json: async () => ({ path: '/tmp/workspaces' }), text: async () => '' };
    },
    localStorage: window.localStorage,
    sessionStorage: window.sessionStorage,
    setTimeout,
    window
  };

  vm.runInNewContext(source, context, { filename: 'workspace-hub.js' });

  return {
    helpers: window.WorkspaceHub.__test,
    launcherEmpty,
    launcherGrid,
    launcherViewCards,
    launcherViewTree,
    state,
    storage,
    window
  };
}

test('launcher view preference defaults to cards and persists valid values', () => {
  const { helpers, storage } = loadWorkspaceHub();

  assert.equal(helpers.normalizeLauncherView('tree'), 'tree');
  assert.equal(helpers.normalizeLauncherView('invalid'), 'cards');
  assert.equal(helpers.getLauncherViewPreference(), 'cards');
  assert.equal(helpers.setLauncherViewPreference('tree'), 'tree');
  assert.equal(storage.get('oriWorkspaceHubLauncherView'), 'tree');
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
  assert.doesNotMatch(launcherGrid.innerHTML, /No description yet/);
  assert.equal(state.workspaces, workspaces);
});

test('launcher cards render always-available workspace checkboxes without select-mode attributes', () => {
  const workspaces = [
    {
      id: 'group-1',
      kind: 'group',
      name: 'Platform',
      children: [{ id: 'workspace-1', kind: 'workspace', name: 'API', description: 'Backend work', parent_id: 'group-1' }]
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
  assert.match(launcherGrid.innerHTML, /launcher-card-item launcher-group-header has-selection-checkbox/);
  assert.match(launcherGrid.innerHTML, /data-workspace-checkbox="group-1"/);
  assert.doesNotMatch(launcherGrid.innerHTML, /data-select-mode/);
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
    getAttribute: (name) => (name === 'data-workspace-checkbox' ? 'workspace-1' : null)
  });
  const checkboxShell = listenerElement();
  const { helpers, launcherGrid, state, window } = loadWorkspaceHub();

  launcherGrid.querySelector = () => null;
  launcherGrid.querySelectorAll = (selector) => {
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
  assert.equal(window.location.href, '/workspaces/workspace-1');
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
    getAttribute: (name) => (name === 'data-group-toggle' ? 'group-1' : null)
  });

  const { helpers, launcherGrid, state, window } = loadWorkspaceHub({
    state: { workspaces: [{ id: 'group-1', kind: 'group', name: 'Clients', children: [] }] }
  });

  launcherGrid.querySelector = () => null;
  launcherGrid.querySelectorAll = (selector) => {
    if (selector === '[data-workspace-id]') return [groupRow];
    if (selector === '[data-group-toggle]') return [caretBtn];
    return [];
  };

  helpers.bindLauncherInteractions();

  // Clicking the group's name/body opens its details page.
  groupRow.dispatch('click', {});
  assert.equal(window.location.href, '/workspaces/group-1');

  // The caret toggles collapse, stops propagation, and must not navigate.
  window.location.href = '';
  const caretClick = {
    prevented: false,
    stopped: false,
    preventDefault() { this.prevented = true; },
    stopPropagation() { this.stopped = true; }
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
  launcherGrid.querySelectorAll = (selector) => {
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
    closest: (selector) => selector.includes('.modal.show') ? { className: 'modal show' } : null
  });
  assert.equal(modalEvent.prevented, false);
});

test('launcher tree drop intent maps edge and middle regions', () => {
  const { helpers } = loadWorkspaceHub();
  const row = {
    classList: { contains: (className) => className === 'launcher-tree-row' },
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
    JSON.stringify({ type: 'before', targetParentId: 'group-parent', insertBeforeId: 'group-target' })
  );
  assert.equal(
    JSON.stringify(helpers.getLauncherTreeDropIntent(row, { clientY: 115 })),
    JSON.stringify({ type: 'into', targetParentId: 'group-target', insertBeforeId: '' })
  );
  assert.equal(
    JSON.stringify(helpers.getLauncherTreeDropIntent(row, { clientY: 129 })),
    JSON.stringify({ type: 'after', targetParentId: 'group-parent', insertBeforeId: 'workspace-next' })
  );

  row.dataset.nextSiblingId = '';
  assert.equal(
    JSON.stringify(helpers.getLauncherTreeDropIntent(row, { clientY: 129 })),
    JSON.stringify({ type: 'after', targetParentId: 'group-parent', insertBeforeId: '' })
  );
});
