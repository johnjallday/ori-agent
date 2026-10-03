import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceMembersPanel } from './workspace-detail-members.js';

globalThis.window = { location: { search: '', pathname: '/workspaces/workspace-slug' } };
globalThis.document = {};
const { WorkspaceDetailPage } = await import('./workspace-detail.js');

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const response = body => ({ ok: true, status: 200, json: async () => body });
const flush = () => new Promise(resolve => setImmediate(resolve));

function workspace(label) {
  return {
    id: 'ws',
    name: label,
    kind: 'workspace',
    folder_slug: 'workspace-slug',
    directory_references: [{ id: label, name: label, path: `/${label}` }],
    mcp_bindings: [{ id: label }],
    agent_mcp_access: [{ id: label }],
    project_path: `/${label}`,
    primary_directory_id: label,
    shared_data: { label },
    attachments: [],
    agent_instances: []
  };
}

function makePage(t, options = {}) {
  const page = new WorkspaceDetailPage('ws', 'workspace-slug', {
    suppressSetupPrompts: true,
    ...options
  });
  const view = { renders: [], errors: [], warnings: [], refreshes: 0 };
  page.workspace = workspace('cached');
  for (const method of [
    'renderWorkspaceInfo',
    'renderGroupRequirementStatus',
    'syncProjectActionState',
    'renderWorkspaceMCPBindings',
    'renderWorkspaceSettings',
    'renderWorkspaceSkillBindings',
    'renderWorkspacePluginBindings',
    'renderAgentGroups',
    'refreshHomeAssistantQuickPrompts',
    'renderWorkspaceHealth',
    'renderChildren',
    'renderTasks',
    'renderWorkspaceConfigSummary',
    'renderWorkspaceWorkflowLinks',
    'restoreTaskAssistPageFromRoute',
    'renderSessions',
    'renderFiles',
    'renderNotes',
    'updateCopyNotesButtonState',
    'renderDirectories',
    'renderSchedules',
    'renderWorkspacePlanningPolicy',
    'renderBoard'
  ])
    page[method] = () => view.renders.push(method);
  page.loadAvailableSkills = async () => [];
  page.loadWorkspacePlanningPolicy = async () => null;
  page.membersPanel.syncWorkspace = async () => false;
  page.ensureAgentOptions = async () => [];
  page.syncWorkspaceFilesFromDisk = async () => {};
  window.workspaceCommand = { refresh: () => view.refreshes++, handleActivityEvent: () => {} };
  t.mock.method(console, 'error', (...args) => view.errors.push(args));
  t.mock.method(console, 'warn', (...args) => view.warnings.push(args));
  return { page, view };
}

const panels = [
  {
    method: 'loadTasks',
    resource: 'tasks',
    snapshot: label => ({ tasks: [{ id: label }] }),
    value: page => page.tasks[0]?.id,
    loading: 'tasksLoading',
    failed: 'tasksLoadFailed'
  },
  {
    method: 'loadBacklog',
    resource: 'backlog',
    snapshot: label => ({ items: [{ id: label }], sync: { label } }),
    value: page => page.backlogItems[0]?.id,
    loading: 'backlogLoading',
    failed: 'backlogLoadFailed'
  }
];

for (const panel of panels) {
  test(`${panel.method} delegates transport to the injected loader with explicit request inputs`, async t => {
    const calls = [];
    const snapshot = Object.freeze({
      ...panel.snapshot('loaded'),
      workspace: workspace('must not publish'),
      notes: [{ id: 'must not publish' }]
    });
    const dataLoader = {
      [panel.method]: async request => {
        calls.push(request);
        return snapshot;
      }
    };
    const { page, view } = makePage(t, { dataLoader });
    const cachedWorkspace = page.workspace;
    page.workspaceId = 'workspace/uuid with spaces';
    page.backlogIncludeDescendants = true;
    t.mock.method(globalThis, 'fetch', async () => {
      assert.fail('the controller must not fetch panel data');
    });
    await page[panel.method]();
    assert.equal(page.dataLoader, dataLoader);
    assert.equal(panel.value(page), 'loaded');
    assert.equal(page[panel.loading], false);
    assert.equal(page[panel.failed], false);
    assert.equal(calls.length, 1);
    const signal = page._resourceRequests.get(panel.resource).controller.signal;
    assert.deepEqual(calls[0], {
      workspaceId: 'workspace/uuid with spaces',
      signal,
      ...(panel.resource === 'backlog' ? { includeDescendants: true } : {})
    });
    assert.equal(signal.aborted, false);
    assert.equal(page.workspace, cachedWorkspace);
    assert.deepEqual(page.notes, []);
    assert.equal(view.errors.length, 0);
    assert.equal(view.refreshes, 1);
  });
}

function seedPanel(page, panel, label) {
  if (panel.resource === 'tasks') page.tasks = panel.snapshot(label).tasks;
  else {
    page.backlogItems = panel.snapshot(label).items;
    page.backlogSync = panel.snapshot(label).sync;
  }
}

for (const panel of panels) {
  for (const outcome of ['response', 'failure']) {
    test(`${panel.method} rejects a superseded ${outcome} from a cancellation-ignoring loader`, async t => {
      const pending = deferred();
      const requests = [];
      const { page, view } = makePage(t, {
        dataLoader: {
          [panel.method]: request => {
            requests.push(request);
            return requests.length === 1 ? pending.promise : Promise.resolve(panel.snapshot('new'));
          }
        }
      });
      const oldRead = page[panel.method]();
      await page[panel.method]();
      const renders = view.renders.length;
      const refreshes = view.refreshes;
      assert.equal(requests[0].signal.aborted, true);
      assert.equal(requests[1].signal.aborted, false);
      if (outcome === 'response') pending.resolve(panel.snapshot('old'));
      else pending.reject(new Error('obsolete loader failure'));
      await oldRead;
      assert.equal(panel.value(page), 'new');
      if (panel.resource === 'backlog') assert.deepEqual(page.backlogSync, { label: 'new' });
      assert.equal(page[panel.loading], false);
      assert.equal(page[panel.failed], false);
      assert.equal(view.renders.length, renders);
      assert.equal(view.refreshes, refreshes);
      assert.equal(view.errors.length, 0);
    });

    test(`${panel.method} obsolete loader ${outcome} cannot finish a newer loading indicator`, async t => {
      const first = deferred();
      const second = deferred();
      let calls = 0;
      const { page, view } = makePage(t, {
        dataLoader: { [panel.method]: () => (++calls === 1 ? first.promise : second.promise) }
      });
      seedPanel(page, panel, 'cached');
      const oldRead = page[panel.method]();
      const newRead = page[panel.method]();
      const renders = view.renders.length;
      if (outcome === 'response') first.resolve(panel.snapshot('old'));
      else first.reject(new Error('obsolete loader failure'));
      await oldRead;
      assert.equal(page[panel.loading], true);
      assert.equal(page[panel.failed], false);
      assert.equal(panel.value(page), 'cached');
      assert.equal(view.renders.length, renders);
      assert.equal(view.refreshes, 0);
      assert.equal(view.errors.length, 0);
      second.resolve(panel.snapshot('new'));
      await newRead;
      assert.equal(page[panel.loading], false);
      assert.equal(panel.value(page), 'new');
      assert.equal(view.refreshes, 1);
    });
  }

  for (const invalidation of ['destroy', 'workspace identity']) {
    test(`${panel.method} loader publication is fenced by ${invalidation}`, async t => {
      const pending = deferred();
      let signal;
      const { page, view } = makePage(t, {
        dataLoader: {
          [panel.method]: request => {
            signal = request.signal;
            return pending.promise;
          }
        }
      });
      seedPanel(page, panel, 'cached');
      const read = page[panel.method]();
      if (invalidation === 'destroy') page.destroy();
      else page.workspaceId = 'another-workspace';
      const renders = view.renders.length;
      pending.resolve(panel.snapshot('late'));
      await read;
      assert.equal(panel.value(page), 'cached');
      if (panel.resource === 'backlog') assert.deepEqual(page.backlogSync, { label: 'cached' });
      assert.equal(view.renders.length, renders);
      assert.equal(view.refreshes, 0);
      assert.equal(view.errors.length, 0);
      if (invalidation === 'destroy') assert.equal(signal.aborted, true);
    });
  }

  test(`${panel.method} current loader failures preserve controller-owned fallback and presentation`, async t => {
    const failure = new Error('current loader failure');
    const { page, view } = makePage(t, {
      dataLoader: {
        [panel.method]: async () => {
          throw failure;
        }
      }
    });
    seedPanel(page, panel, 'cached');
    await page[panel.method]();
    assert.equal(page[panel.loading], false);
    assert.equal(page[panel.failed], true);
    assert.equal(panel.value(page), undefined);
    if (panel.resource === 'backlog') assert.deepEqual(page.backlogSync, { label: 'cached' });
    else assert.ok(view.renders.includes('renderTasks'));
    assert.equal(view.errors.length, 1);
    assert.equal(view.errors[0][1], failure);
    assert.equal(view.refreshes, 1);
  });

  test(`${panel.method} cannot invoke an injected loader after teardown`, async t => {
    let calls = 0;
    const { page, view } = makePage(t, {
      dataLoader: {
        [panel.method]: async () => {
          calls++;
          return panel.snapshot('late');
        }
      }
    });
    page.destroy();
    await page[panel.method]();
    assert.equal(calls, 0);
    assert.equal(view.refreshes, 0);
    assert.equal(view.renders.length, 0);
  });
}

test('injected task and backlog reads retain independent owners', async t => {
  const tasks = deferred();
  const oldBacklog = deferred();
  const taskRequests = [];
  const backlogRequests = [];
  const { page } = makePage(t, {
    dataLoader: {
      loadTasks: request => {
        taskRequests.push(request);
        return tasks.promise;
      },
      loadBacklog: request => {
        backlogRequests.push(request);
        return backlogRequests.length === 1
          ? oldBacklog.promise
          : Promise.resolve({ items: [{ id: 'new backlog' }], sync: null });
      }
    }
  });
  const taskRead = page.loadTasks();
  const backlogRead = page.loadBacklog();
  await page.loadBacklog();
  assert.equal(taskRequests[0].signal.aborted, false);
  assert.equal(backlogRequests[0].signal.aborted, true);
  assert.equal(backlogRequests[1].signal.aborted, false);
  assert.equal(page.tasksLoading, true);
  oldBacklog.resolve({ items: [{ id: 'old backlog' }], sync: { stale: true } });
  await backlogRead;
  tasks.resolve({ tasks: [{ id: 'task' }] });
  await taskRead;
  assert.deepEqual(page.tasks, [{ id: 'task' }]);
  assert.deepEqual(page.backlogItems, [{ id: 'new backlog' }]);
  assert.equal(page.backlogSync, null);
});

test('a backlog filter change passes the new scope explicitly and suppresses the older scope', async t => {
  const pending = deferred();
  const requests = [];
  const { page } = makePage(t, {
    dataLoader: {
      loadBacklog: request => {
        requests.push(request);
        return requests.length === 1
          ? pending.promise
          : Promise.resolve({ items: [{ id: 'descendant' }], sync: null });
      }
    }
  });
  const oldRead = page.loadBacklog();
  page.backlogIncludeDescendants = true;
  await page.loadBacklog();
  assert.deepEqual(
    requests.map(request => request.includeDescendants),
    [false, true]
  );
  pending.resolve({ items: [{ id: 'local only' }], sync: { stale: true } });
  await oldRead;
  assert.deepEqual(page.backlogItems, [{ id: 'descendant' }]);
  assert.equal(page.backlogSync, null);
});

test('task snapshot publication retains board rendering, accessible counts, and route restoration', async t => {
  const { page, view } = makePage(t, {
    dataLoader: { loadTasks: async () => ({ tasks: [{ id: 'first' }, { id: 'second' }] }) }
  });
  page.currentView = 'board';
  page.boardConfig = {};
  const attributes = {};
  page.elements.taskCount = { setAttribute: (name, value) => (attributes[name] = value) };
  await page.loadTasks();
  assert.equal(page.elements.taskCount.textContent, 2);
  assert.deepEqual(attributes, { 'aria-busy': 'false', 'aria-label': '2 tasks' });
  for (const method of [
    'renderAgentGroups',
    'renderBoard',
    'renderTasks',
    'renderWorkspaceConfigSummary',
    'renderWorkspaceWorkflowLinks',
    'restoreTaskAssistPageFromRoute',
    'refreshHomeAssistantQuickPrompts'
  ]) {
    assert.equal(view.renders.filter(render => render === method).length, 1, method);
  }
  assert.equal(view.refreshes, 1);
});

const resources = [
  { method: 'loadWorkspace', body: workspace, value: page => page.workspace?.name },
  {
    method: 'loadChildren',
    body: label => ({ folders: [{ id: 'ws', children: [{ name: label }] }] }),
    value: page => page.children[0]?.name
  },
  {
    method: 'loadTasks',
    body: label => ({ tasks: [{ id: label }] }),
    value: page => page.tasks[0]?.id,
    loading: 'tasksLoading'
  },
  {
    method: 'loadBacklog',
    body: label => ({ items: [{ id: label }], sync: { label } }),
    value: page => page.backlogItems[0]?.id,
    loading: 'backlogLoading'
  },
  {
    method: 'loadSessions',
    body: label => ({ sessions: [{ id: label }] }),
    value: page => page.sessions[0]?.id,
    loading: 'sessionsLoading'
  },
  {
    method: 'loadFiles',
    body: label => ({ attachments: [{ id: label, file_meta: { name: label } }] }),
    value: page => page.files[0]?.id
  },
  {
    method: 'loadNotes',
    body: label => ({ notes: [{ id: label }] }),
    value: page => page.notes[0]?.id
  },
  { method: 'loadDirectories', body: workspace, value: page => page.directories[0]?.id },
  {
    method: 'loadSchedules',
    body: label => ({ tasks: [{ id: label, schedule: { type: 'daily' } }] }),
    value: page => page.schedules[0]?.id
  },
  {
    method: 'loadWorkspacePlanningPolicy',
    body: label => ({ policy: { label } }),
    value: page => page.workspacePlanningPolicy?.label
  },
  {
    method: 'loadWorkspaceAgentSnapshots',
    body: label => ({ agents: [{ name: label }] }),
    value: page => page.workspaceAgentProfiles.keys().next().value
  },
  {
    method: 'loadAgentCatalog',
    body: label => ({ agents: [{ name: label }] }),
    value: page => page.agentCatalog[0]?.name
  },
  {
    method: 'loadBoard',
    body: label => ({ board: { label } }),
    value: page => page.boardConfig?.label
  }
];

for (const resource of resources) {
  for (const outcome of ['response', 'failure']) {
    test(`${resource.method} rejects an obsolete ${outcome}, including stale finally rendering`, async t => {
      const { page, view } = makePage(t);
      if (resource.method === 'loadWorkspacePlanningPolicy')
        page.loadWorkspacePlanningPolicy =
          WorkspaceDetailPage.prototype.loadWorkspacePlanningPolicy;
      const pending = deferred();
      const calls = [];
      t.mock.method(globalThis, 'fetch', (_url, options) => {
        calls.push(options);
        return calls.length === 1
          ? pending.promise
          : Promise.resolve(response(resource.body('new')));
      });
      const args = resource.method === 'loadAgentCatalog' ? [true] : [];
      const oldRead = page[resource.method](...args);
      await flush(); // Files performs its sync stage before starting the read.
      await page[resource.method](...args);
      const renders = view.renders.length;
      const refreshes = view.refreshes;
      if (outcome === 'response') pending.resolve(response(resource.body('old')));
      else pending.reject(new Error('obsolete failure'));
      await oldRead;
      assert.equal(resource.value(page), 'new');
      assert.equal(view.renders.length, renders, 'obsolete continuations must not render');
      assert.equal(view.refreshes, refreshes, 'obsolete finally must not refresh Command');
      assert.equal(view.errors.length, 0);
      assert.equal(calls[0]?.signal?.aborted, true);
      if (resource.loading) assert.equal(page[resource.loading], false);
    });
  }

  test(`destroy invalidates in-flight ${resource.method}`, async t => {
    const { page, view } = makePage(t);
    if (resource.method === 'loadWorkspacePlanningPolicy')
      page.loadWorkspacePlanningPolicy = WorkspaceDetailPage.prototype.loadWorkspacePlanningPolicy;
    const pending = deferred();
    let signal;
    t.mock.method(globalThis, 'fetch', (_url, options) => {
      signal = options?.signal;
      return pending.promise;
    });
    const read = page[resource.method]();
    await flush();
    page.destroy();
    const previous = resource.value(page);
    const renders = view.renders.length;
    const refreshes = view.refreshes;
    pending.resolve(response(resource.body('late')));
    await read;
    assert.equal(resource.value(page), previous);
    assert.equal(view.renders.length, renders);
    assert.equal(view.refreshes, refreshes);
    assert.equal(signal?.aborted, true);
  });
}

test('independent task and note requests do not cancel each other', async t => {
  const { page } = makePage(t);
  const pending = deferred();
  let taskSignal;
  t.mock.method(globalThis, 'fetch', (url, options) => {
    if (url.includes('/tasks?')) {
      taskSignal = options?.signal;
      return pending.promise;
    }
    return Promise.resolve(response({ notes: [{ id: 'note' }] }));
  });
  const tasks = page.loadTasks();
  await page.loadNotes();
  assert.equal(taskSignal?.aborted, false);
  pending.resolve(response({ tasks: [{ id: 'task' }] }));
  await tasks;
  assert.equal(page.tasks[0].id, 'task');
  assert.equal(page.notes[0].id, 'note');
});

test('an obsolete failure cannot finish a newer task loading indicator', async t => {
  const { page } = makePage(t);
  const first = deferred();
  const second = deferred();
  let count = 0;
  t.mock.method(globalThis, 'fetch', () => (++count === 1 ? first.promise : second.promise));
  const oldRead = page.loadTasks();
  const newRead = page.loadTasks();
  first.reject(new Error('obsolete'));
  await oldRead;
  assert.equal(page.tasksLoading, true);
  second.resolve(response({ tasks: [{ id: 'new' }] }));
  await newRead;
  assert.equal(page.tasksLoading, false);
});

test('current task failures still report and clear their loading state', async t => {
  const { page, view } = makePage(t);
  t.mock.method(globalThis, 'fetch', async () => {
    throw new Error('current failure');
  });
  await page.loadTasks();
  assert.equal(page.tasksLoading, false);
  assert.equal(page.tasksLoadFailed, true);
  assert.equal(view.errors.length, 1);
  assert.ok(view.renders.includes('renderTasks'));
});

test('workspace identity change invalidates an outstanding panel read', async t => {
  const { page, view } = makePage(t);
  const pending = deferred();
  t.mock.method(globalThis, 'fetch', () => pending.promise);
  const read = page.loadNotes();
  const renders = view.renders.length;
  page.workspaceId = 'another-workspace';
  pending.resolve(response({ notes: [{ id: 'old workspace' }] }));
  await read;
  assert.deepEqual(page.notes, []);
  assert.equal(view.renders.length, renders);
});

for (const boundary of ['skills', 'workspaceInfo', 'members']) {
  test(`workspace continuation is invalidated while awaiting ${boundary}`, async t => {
    const { page, view } = makePage(t);
    const pending = deferred();
    let count = 0;
    const method =
      boundary === 'skills'
        ? 'loadAvailableSkills'
        : boundary === 'workspaceInfo'
          ? 'renderWorkspaceInfo'
          : 'syncWorkspace';
    const target = boundary === 'members' ? page.membersPanel : page;
    target[method] = () => (++count === 1 ? pending.promise : Promise.resolve());
    let reads = 0;
    t.mock.method(globalThis, 'fetch', async () =>
      response(workspace(++reads === 1 ? 'old' : 'new'))
    );
    const oldRead = page.loadWorkspace();
    await flush();
    await page.loadWorkspace();
    const renders = view.renders.length;
    const refreshes = view.refreshes;
    pending.resolve();
    await oldRead;
    assert.equal(page.workspace.name, 'new');
    assert.equal(view.renders.length, renders);
    assert.equal(view.refreshes, refreshes);
  });
}

for (const olderResource of ['workspace', 'directories']) {
  test(`older ${olderResource} data cannot roll back shared directory/MCP metadata`, async t => {
    const { page } = makePage(t);
    const pending = deferred();
    t.mock.method(globalThis, 'fetch', url => {
      const isWorkspace = url.includes('/orchestration/workspace?');
      return isWorkspace === (olderResource === 'workspace')
        ? pending.promise
        : Promise.resolve(response(workspace('new')));
    });
    const oldRead = olderResource === 'workspace' ? page.loadWorkspace() : page.loadDirectories();
    await (olderResource === 'workspace' ? page.loadDirectories() : page.loadWorkspace());
    pending.resolve(response(workspace('old')));
    await oldRead;
    assert.equal(page.workspace.project_path, '/new');
    assert.equal(page.workspace.directory_references[0].id, 'new');
    assert.equal(page.workspace.mcp_bindings[0].id, 'new');
    assert.equal(page.workspace.shared_data.label, 'new');
    if (olderResource === 'directories') assert.equal(page.directories[0].id, 'new');
  });
}

test('realtime subscription is idempotent and late callbacks cannot revive reads', async t => {
  const { page, view } = makePage(t);
  let subscribed = 0;
  let unsubscribed = 0;
  let callback;
  window.workspaceRealtime = {
    subscribeToWorkspace: (_id, listener) => {
      subscribed++;
      callback = listener;
      return () => unsubscribed++;
    }
  };
  t.after(() => delete window.workspaceRealtime);
  let reads = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    reads++;
    return response({ tasks: [] });
  });
  page.setupRealtime();
  page.setupRealtime();
  assert.equal(subscribed, 1);
  page.destroy();
  page.destroy();
  const refreshes = view.refreshes;
  callback({ type: 'task.started', data: { task_id: 'task' } });
  page.setupRealtime();
  await page.loadTasks();
  assert.equal(unsubscribed, 1);
  assert.equal(reads, 0);
  assert.equal(view.refreshes, refreshes);
});

test('realtime backlog promotion refreshes both independent resources', async t => {
  const { page } = makePage(t);
  page.handleTaskExecutionRealtimeEvent = () => {};
  t.mock.method(globalThis, 'fetch', async url =>
    response(
      url.includes('/backlog?') ? { items: [{ id: 'backlog' }] } : { tasks: [{ id: 'task' }] }
    )
  );
  page.handleRealtimeEvent({ type: 'task.backlog.promoted' });
  await flush();
  assert.equal(page.tasks[0]?.id, 'task');
  assert.equal(page.backlogItems[0]?.id, 'backlog');
});

test('character catalog subscription is cleaned up and ignores late notifications', t => {
  const { page, view } = makePage(t);
  let notifications;
  let subscriptions = 0;
  let removed = 0;
  window.CharacterCatalog = {
    onChange: callback => {
      notifications = callback;
      subscriptions++;
      return () => removed++;
    },
    load: () => {}
  };
  t.after(() => delete window.CharacterCatalog);
  page.watchCharacterCatalog();
  page.watchCharacterCatalog();
  page.destroy();
  const renders = view.renders.length;
  notifications();
  page.watchCharacterCatalog();
  assert.equal(subscriptions, 1);
  assert.equal(removed, 1);
  assert.equal(view.renders.length, renders);
});

test('init stops after teardown during the initial workspace load', async t => {
  const { page } = makePage(t);
  const pending = deferred();
  const calls = [];
  for (const method of [
    'cacheElements',
    'ensureScrollablePanelAccessibility',
    'bindEvents',
    'setupFilesPanelVaultDrop',
    'setupNotesPanelVaultDrop',
    'setupPageDragAndDrop',
    'consumeProjectOpenFailureNotice',
    'watchCharacterCatalog',
    'loadAgentCatalog',
    'loadWorkspaceAgentSnapshots',
    'activateWorkspace',
    'setupRealtime'
  ]) {
    page[method] = () => calls.push(method);
  }
  page.fileModalManager.setupFileModal = () => {};
  page.loadWorkspace = () => pending.promise;
  const init = page.init();
  page.destroy();
  const atDestroy = [...calls];
  pending.resolve();
  await init;
  await page.init();
  assert.deepEqual(calls, atDestroy);
});

test('destroy stops task activity timers and prevents restarting them', t => {
  const { page } = makePage(t);
  const timers = new Map();
  let next = 0;
  window.setInterval = callback => {
    const id = ++next;
    timers.set(id, callback);
    return id;
  };
  window.clearInterval = id => timers.delete(id);
  page.ensureTaskActivityTick();
  assert.equal(timers.size, 1);
  page.destroy();
  page.ensureTaskActivityTick();
  assert.equal(timers.size, 0);
});

test('destroy during an execution poll cannot publish or restart its interval', async t => {
  const { page, view } = makePage(t);
  const pending = deferred();
  const intervals = [];
  t.mock.method(globalThis, 'setInterval', callback => {
    intervals.push(callback);
    return 123;
  });
  t.mock.method(globalThis, 'fetch', () => pending.promise);
  page.updateFirstTaskBanner = () => view.renders.push('banner');
  page.updateTaskExecutionMeta = () => view.renders.push('meta');
  const read = page.startExecutionMonitor('task');
  page.destroy();
  const renders = view.renders.length;
  pending.resolve(response({ id: 'task', status: 'in_progress' }));
  await read;
  assert.equal(view.renders.length, renders);
  assert.equal(intervals.length, 0);
});

for (const resource of resources) {
  test(`${resource.method} rejects an obsolete JSON body, not just an obsolete response header`, async t => {
    const { page, view } = makePage(t);
    if (resource.method === 'loadWorkspacePlanningPolicy')
      page.loadWorkspacePlanningPolicy = WorkspaceDetailPage.prototype.loadWorkspacePlanningPolicy;
    const pending = deferred();
    let count = 0;
    t.mock.method(globalThis, 'fetch', async () =>
      ++count === 1 ? { ok: true, json: () => pending.promise } : response(resource.body('new'))
    );
    const args = resource.method === 'loadAgentCatalog' ? [true] : [];
    const oldRead = page[resource.method](...args);
    await flush();
    await page[resource.method](...args);
    const renders = view.renders.length;
    pending.resolve(resource.body('old'));
    await oldRead;
    assert.equal(resource.value(page), 'new');
    assert.equal(view.renders.length, renders);
  });
}

test('request identity rejects old responses even when abort throws', async t => {
  const { page, view } = makePage(t);
  const pending = deferred();
  let calls = 0;
  t.mock.method(globalThis, 'fetch', () =>
    ++calls === 1 ? pending.promise : Promise.resolve(response({ tasks: [{ id: 'new' }] }))
  );
  const oldRead = page.loadTasks();
  page._resourceRequests.get('tasks').controller.abort = () => {
    throw new Error('broken abort');
  };
  await page.loadTasks();
  const renders = view.renders.length;
  pending.resolve(response({ tasks: [{ id: 'old' }] }));
  await oldRead;
  assert.equal(page.tasks[0].id, 'new');
  assert.equal(view.renders.length, renders);
  assert.equal(view.errors.length, 0);
});

test('destroy during file sync cancels it and prevents the subsequent workspace fetch', async t => {
  const { page } = makePage(t);
  page.syncWorkspaceFilesFromDisk = WorkspaceDetailPage.prototype.syncWorkspaceFilesFromDisk;
  const pending = deferred();
  const calls = [];
  t.mock.method(globalThis, 'fetch', (url, options) => {
    calls.push({ url, options });
    return pending.promise;
  });
  const read = page.loadFiles();
  page.destroy();
  pending.resolve(response({}));
  await read;
  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.cache, 'no-store');
  assert.equal(calls[0].options.signal.aborted, true);
  assert.deepEqual(page.files, []);
});

test('superseding workspace reads also invalidate dependent children and policy reads', async t => {
  const { page, view } = makePage(t);
  page.loadWorkspacePlanningPolicy = WorkspaceDetailPage.prototype.loadWorkspacePlanningPolicy;
  const parent = page.beginResourceRequest('workspace');
  const pending = deferred();
  const signals = [];
  t.mock.method(globalThis, 'fetch', (_url, options) => {
    signals.push(options.signal);
    return pending.promise;
  });
  const children = page.loadChildren(parent);
  const policy = page.loadWorkspacePlanningPolicy('', parent);
  page.beginResourceRequest('workspace');
  pending.resolve(
    response({ folders: [{ id: 'ws', children: [{ name: 'old' }] }], policy: { label: 'old' } })
  );
  await Promise.all([children, policy]);
  assert.ok(signals.every(signal => signal.aborted));
  assert.deepEqual(page.children, []);
  assert.equal(page.workspacePlanningPolicy, null);
  assert.equal(view.renders.length, 0);
});

test('a later deliberate metadata clear survives an earlier workspace response', async t => {
  const { page } = makePage(t);
  const pending = deferred();
  const cleared = {
    ...workspace('new'),
    directory_references: [],
    mcp_bindings: [],
    agent_mcp_access: [],
    primary_directory_id: '',
    project_path: '',
    shared_data: {}
  };
  t.mock.method(globalThis, 'fetch', url =>
    url.includes('/orchestration/workspace?') ? pending.promise : Promise.resolve(response(cleared))
  );
  const read = page.loadWorkspace();
  await page.loadDirectories();
  const old = workspace('old');
  pending.resolve(response(old));
  await read;
  assert.deepEqual(page.workspace.directory_references, []);
  assert.deepEqual(page.workspace.mcp_bindings, []);
  assert.deepEqual(page.workspace.agent_mcp_access, []);
  assert.equal(page.workspace.primary_directory_id, '');
  assert.equal(page.workspace.project_path, '');
  assert.deepEqual(page.workspace.shared_data, {});
  assert.equal(old.project_path, '/old', 'merging must not mutate the transport payload');
});

for (const failure of ['http', 'network', 'json']) {
  test(`an older directory ${failure} failure cannot erase fresher workspace metadata`, async t => {
    const { page, view } = makePage(t);
    const pending = deferred();
    t.mock.method(globalThis, 'fetch', url =>
      url.includes('/orchestration/workspace?')
        ? Promise.resolve(response(workspace('new')))
        : failure === 'json'
          ? Promise.resolve({ ok: true, json: () => pending.promise })
          : pending.promise
    );
    const read = page.loadDirectories();
    await flush();
    await page.loadWorkspace();
    if (failure === 'http') pending.resolve({ ...response({}), ok: false, status: 500 });
    else pending.reject(new Error(`old ${failure} failure`));
    await read;
    assert.equal(page.directories[0]?.name, 'new');
    assert.equal(view.errors.length, 0);
  });
}

function membersPanel() {
  const panel = new WorkspaceMembersPanel('ws');
  const view = { members: 0, header: 0, rollups: 0 };
  panel.cacheElements = () => {};
  panel.bindControlsOnce = () => {};
  panel.renderMembers = () => view.members++;
  panel.applyHeaderIdentity = () => view.header++;
  panel.renderRollups = () => view.rollups++;
  return { panel, view };
}
const groupTree = { folders: [{ id: 'ws', kind: 'group', children: [{ id: 'member' }] }] };

for (const invalidation of ['destroy', 'parent']) {
  test(`members cannot activate after ${invalidation} invalidates the tree lookup`, async t => {
    const { panel, view } = membersPanel();
    const pending = deferred();
    let signal;
    let valid = true;
    t.mock.method(globalThis, 'fetch', (_url, options) => {
      signal = options.signal;
      return pending.promise;
    });
    const read = panel.syncWorkspace({ id: 'ws', kind: 'group' }, { isCurrent: () => valid });
    if (invalidation === 'destroy') panel.destroy();
    else valid = false;
    pending.resolve(response(groupTree));
    await read;
    assert.equal(panel.active, false);
    assert.equal(panel.group, null);
    assert.equal(view.members, 0);
    assert.equal(view.header, 0);
    if (invalidation === 'destroy') assert.equal(signal.aborted, true);
  });
}

test('switching from group to workspace suppresses an older group activation', async t => {
  const { panel, view } = membersPanel();
  const pending = deferred();
  t.mock.method(globalThis, 'fetch', () => pending.promise);
  const read = panel.syncWorkspace({ id: 'ws', kind: 'group' });
  await panel.syncWorkspace({ id: 'ws', kind: 'workspace' });
  pending.resolve(response(groupTree));
  await read;
  assert.equal(panel.active, false);
  assert.equal(view.members, 0);
});

test('current group reads still activate the members panel', async t => {
  const { panel, view } = membersPanel();
  t.mock.method(globalThis, 'fetch', async () => response(groupTree));
  await panel.syncWorkspace({ id: 'ws', kind: 'group' });
  assert.equal(panel.active, true);
  assert.equal(view.members, 1);
  panel.destroy();
});

test('members teardown disconnects its observer and aborts pending rollups', async t => {
  const { panel, view } = membersPanel();
  panel.group = groupTree.folders[0];
  panel.els.rollups = { innerHTML: '' };
  const pending = deferred();
  const signals = [];
  let disconnected = 0;
  panel.rollupObserver = { disconnect: () => disconnected++ };
  t.mock.method(globalThis, 'fetch', (_url, options) => {
    signals.push(options.signal);
    return pending.promise;
  });
  const read = panel.loadRollups();
  panel.destroy();
  panel.destroy();
  pending.resolve(response({ tasks: [{ id: 'late' }], notes: [{ id: 'late' }] }));
  await read;
  assert.equal(disconnected, 1);
  assert.equal(view.rollups, 0);
  assert.deepEqual(panel.allTasks, []);
  assert.equal(panel.memberNotes, undefined);
  assert.ok(signals.every(signal => signal.aborted));
});

test('skill refreshes retain the newer promise and cache when superseded', async t => {
  const { page } = makePage(t);
  const manager = page.skillsManager;
  const first = deferred();
  const second = deferred();
  const calls = [];
  t.mock.method(globalThis, 'fetch', (_url, options) => {
    calls.push(options);
    return calls.length === 1 ? first.promise : second.promise;
  });
  const oldRead = manager.loadAvailableSkills(true);
  const newRead = manager.loadAvailableSkills(true);
  const latest = manager.availableSkillsPromise;
  first.resolve(response({ skills: [{ name: 'old' }] }));
  assert.deepEqual(await oldRead, []);
  assert.equal(manager.availableSkillsPromise, latest);
  assert.equal(calls[0].signal.aborted, true);
  const sharedRead = manager.loadAvailableSkills();
  second.resolve(response({ skills: [{ name: 'new' }] }));
  await Promise.all([newRead, sharedRead]);
  assert.equal(calls.length, 2);
  assert.equal(manager.availableSkills[0].name, 'new');
  assert.equal(manager.availableSkillsPromise, null);
});

test('skills cannot repopulate their cache after page teardown', async t => {
  const { page } = makePage(t);
  const pending = deferred();
  let signal;
  t.mock.method(globalThis, 'fetch', (_url, options) => {
    signal = options.signal;
    return pending.promise;
  });
  const read = page.skillsManager.loadAvailableSkills();
  page.destroy();
  pending.resolve(response({ skills: [{ name: 'late' }] }));
  assert.deepEqual(await read, []);
  assert.deepEqual(page.skillsManager.availableSkills, []);
  assert.equal(signal.aborted, true);
});

function stubExecution(page, view) {
  page.getTaskExecutionState = task => task.status;
  for (const method of [
    'updateFirstTaskBanner',
    'updateTaskExecutionMeta',
    'setExecutionModalStatus',
    'updateTaskExecutionControls',
    'appendExecutionLog',
    'setExecutionViewResultEnabled'
  ]) {
    page[method] = (...args) => view.renders.push([method, args]);
  }
  page.refreshExecutionBreakdown = async () => {};
  page.getTaskStatusPresentation = () => ({ label: 'Status' });
  page.isTaskAwaitingNextStep = () => false;
  page.loadTasks = async () => {};
}

test('a terminal initial execution poll does not start an interval', async t => {
  const { page, view } = makePage(t);
  stubExecution(page, view);
  const intervals = [];
  t.mock.method(globalThis, 'setInterval', callback => {
    intervals.push(callback);
    return 123;
  });
  t.mock.method(globalThis, 'fetch', async () => response({ id: 'task', status: 'completed' }));
  await page.startExecutionMonitor('task');
  assert.equal(intervals.length, 0);
  assert.equal(page.executionMonitorTimer, null);
});

test('replacing an in-flight execution poll leaves only the replacement interval', async t => {
  const { page, view } = makePage(t);
  stubExecution(page, view);
  const pending = deferred();
  const timers = new Map();
  let next = 0;
  t.mock.method(globalThis, 'setInterval', callback => {
    const id = ++next;
    timers.set(id, callback);
    return id;
  });
  t.mock.method(globalThis, 'clearInterval', id => timers.delete(id));
  let reads = 0;
  t.mock.method(globalThis, 'fetch', async () =>
    ++reads === 1 ? pending.promise : response({ id: 'new-task', status: 'in_progress' })
  );
  const oldRead = page.startExecutionMonitor('old-task');
  await page.startExecutionMonitor('new-task');
  const renders = view.renders.length;
  pending.resolve(response({ id: 'old-task', status: 'completed' }));
  await oldRead;
  assert.equal(view.renders.length, renders);
  assert.equal(page.currentExecutionTaskId, 'new-task');
  assert.equal(timers.size, 1);
  page.destroy();
  assert.equal(timers.size, 0);
});

for (const related of [true, false]) {
  test(`${related ? 'relevant' : 'unrelated'} realtime progress ${related ? 'invalidates' : 'preserves'} an in-flight execution poll`, async t => {
    const { page, view } = makePage(t);
    stubExecution(page, view);
    const pending = deferred();
    const timers = new Map();
    t.mock.method(globalThis, 'setInterval', callback => {
      timers.set(1, callback);
      return 1;
    });
    t.mock.method(globalThis, 'clearInterval', id => timers.delete(id));
    let signal;
    t.mock.method(globalThis, 'fetch', (_url, options) => {
      signal = options.signal;
      return pending.promise;
    });
    const read = page.startExecutionMonitor('task');
    page.handleTaskExecutionRealtimeEvent({
      type: 'task.progress',
      data: { task_id: related ? 'task' : 'other', waiting_for_next_step: true }
    });
    const renders = view.renders.length;
    pending.resolve(response({ id: 'task', status: 'in_progress' }));
    await read;
    assert.equal(signal.aborted, related);
    if (related) assert.equal(view.renders.length, renders);
    else assert.ok(view.renders.length > renders);
    assert.equal(timers.size, 1, 'invalidating a read must not stop the live monitor');
    page.destroy();
    assert.equal(timers.size, 0);
  });
}

test('realtime progress also fences an execution poll awaiting its subtask breakdown', async t => {
  const { page, view } = makePage(t);
  stubExecution(page, view);
  page.refreshExecutionBreakdown = WorkspaceDetailPage.prototype.refreshExecutionBreakdown;
  page.getSubtasksForParent = () => [{ id: 'sub', parent_task_id: 'task' }];
  page.renderTaskExecutionBreakdown = () => view.renders.push('breakdown');
  const pending = deferred();
  let signal;
  t.mock.method(globalThis, 'setInterval', () => 123);
  t.mock.method(globalThis, 'fetch', (url, options) => {
    if (url.includes('workspace_id=')) {
      signal = options.signal;
      return pending.promise;
    }
    return Promise.resolve(response({ id: 'task', status: 'in_progress' }));
  });
  const read = page.startExecutionMonitor('task');
  await flush();
  page.handleTaskExecutionRealtimeEvent({
    type: 'task.progress',
    data: { task_id: 'task', waiting_for_next_step: true }
  });
  const renders = view.renders.length;
  pending.resolve(response({ tasks: [{ id: 'sub', parent_task_id: 'task' }] }));
  await read;
  assert.equal(signal.aborted, true);
  assert.equal(view.renders.length, renders);
  page.destroy();
});

for (const phase of ['loadAgentCatalog', 'loadWorkspaceAgentSnapshots', 'loadTasks']) {
  test(`init cannot proceed after teardown while awaiting ${phase}`, async t => {
    const { page } = makePage(t);
    for (const method of [
      'cacheElements',
      'ensureScrollablePanelAccessibility',
      'bindEvents',
      'setupFilesPanelVaultDrop',
      'setupNotesPanelVaultDrop',
      'setupPageDragAndDrop',
      'consumeProjectOpenFailureNotice',
      'watchCharacterCatalog'
    ])
      page[method] = () => {};
    page.fileModalManager.setupFileModal = () => {};
    for (const { method } of resources) page[method] = async () => {};
    let subscribed = 0;
    page.setupRealtime = () => subscribed++;
    const pending = deferred();
    let started = false;
    page[phase] = () => {
      started = true;
      return pending.promise;
    };
    const init = page.init();
    await flush();
    assert.equal(started, true);
    page.destroy();
    pending.resolve();
    await init;
    assert.equal(subscribed, 0);
  });
}

test('owned global listeners and delayed callbacks are removed and ignore late dispatch', t => {
  const { page } = makePage(t);
  const listeners = new Map();
  const target = {
    addEventListener: (type, listener) => listeners.set(type, listener),
    removeEventListener: type => listeners.delete(type)
  };
  let calls = 0;
  page.listenToPage(target, 'popstate', () => calls++);
  const listener = listeners.get('popstate');
  listener({});
  const timers = new Map();
  window.setTimeout = callback => {
    timers.set(1, callback);
    return 1;
  };
  window.clearTimeout = id => timers.delete(id);
  page.schedulePageCallback(() => calls++, 100);
  const callback = timers.get(1);
  page.destroy();
  listener({});
  callback();
  assert.equal(listeners.size, 0);
  assert.equal(timers.size, 0);
  assert.equal(calls, 1);
});
