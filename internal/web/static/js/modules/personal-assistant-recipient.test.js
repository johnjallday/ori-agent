import test from 'node:test';
import assert from 'node:assert/strict';

import {
  PICK_KEY_PREFIX,
  RECIPIENT_PERSONAL,
  RECIPIENT_WORKSPACE_ENTRY,
  SESSION_KEY_PREFIX,
  clearOtherPicks,
  commanderLabel,
  createRecipientController,
  entryAgentPlaceholder,
  loadEntryAgent,
  readEntrySession,
  readPick,
  resolveRecipient,
  writeEntrySession,
  writePick
} from './personal-assistant-recipient.js';

function memoryStorage() {
  const data = new Map();
  return {
    get length() {
      return data.size;
    },
    key: index => [...data.keys()][index] ?? null,
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: key => data.delete(key),
    data
  };
}

const personal = { name: 'Mina', available: true, state: 'ready', subline: 'Next check-in · 9:00' };
const reaper = { status: 'ready', name: 'REAPER Assistant', role: 'orchestrator' };
const page = {
  surface: 'workspace_detail',
  workspaceId: 'ws-1',
  workspaceName: 'test_reaper',
  entry: reaper,
  hqWorkspaceId: 'hq-1',
  personal
};

test('the Commander label matches the map command node', () => {
  assert.equal(commanderLabel('orchestrator'), 'Commander');
  assert.equal(commanderLabel(' Orchestrator '), 'Commander');
  assert.equal(commanderLabel('specialist'), 'Acting Commander');
  assert.equal(commanderLabel(''), 'Acting Commander');
});

test('a workspace page with a Commander defaults to the entry agent (FR2)', () => {
  for (const surface of ['workspace_detail', 'workspace_canvas', 'workspace_task']) {
    const out = resolveRecipient({ ...page, surface });
    assert.equal(out.pickerVisible, true, surface);
    assert.equal(out.current.kind, RECIPIENT_WORKSPACE_ENTRY, surface);
    assert.equal(out.current.name, 'REAPER Assistant');
    assert.equal(out.current.workspaceId, 'ws-1');
    assert.equal(out.current.workspaceName, 'test_reaper');
    assert.equal(out.current.subline, 'Commander · test_reaper');
    assert.deepEqual(
      out.options.map(option => option.kind),
      [RECIPIENT_WORKSPACE_ENTRY, RECIPIENT_PERSONAL]
    );
    assert.equal(out.options[0].role, 'Commander · test_reaper');
    assert.equal(out.options[1].role, 'Personal Assistant');
  }
});

test('a non-orchestrator holding the slot is the Acting Commander', () => {
  const out = resolveRecipient({ ...page, entry: { ...reaper, role: 'specialist' } });
  assert.equal(out.current.subline, 'Acting Commander · test_reaper');
});

test('app-wide pages only ever have the Personal Assistant (FR3)', () => {
  for (const surface of ['home', 'workspace_hub', 'app', '']) {
    const out = resolveRecipient({ ...page, surface });
    assert.equal(out.pickerVisible, false, surface);
    assert.equal(out.current.kind, RECIPIENT_PERSONAL, surface);
    assert.equal(out.current.name, 'Mina');
    assert.deepEqual(out.options, []);
  }
  // A workspace route whose body publication was stale has no workspace id.
  assert.equal(resolveRecipient({ ...page, workspaceId: '' }).pickerVisible, false);
  // Home's selected tile never changes the recipient, even with a pick stored.
  assert.equal(
    resolveRecipient({ ...page, surface: 'home', pick: RECIPIENT_WORKSPACE_ENTRY }).current.kind,
    RECIPIENT_PERSONAL
  );
});

test('a vacant Commander defaults to the Personal Assistant with a disabled row (FR4)', () => {
  for (const entry of [{ status: 'vacant' }, { status: 'ready', name: '  ' }]) {
    const out = resolveRecipient({ ...page, entry, pick: RECIPIENT_WORKSPACE_ENTRY });
    assert.equal(out.pickerVisible, true);
    assert.equal(out.current.kind, RECIPIENT_PERSONAL);
    assert.equal(out.options[0].disabled, true);
    assert.equal(out.options[0].vacant, true);
    assert.equal(out.options[0].role, 'No Commander yet');
    assert.equal(out.options[0].name, '');
  }
});

test('the Personal HQ has no picker, decided by id rather than name (FR5)', () => {
  const hq = resolveRecipient({ ...page, workspaceId: 'hq-1', workspaceName: 'Personal HQ' });
  assert.equal(hq.pickerVisible, false);
  assert.equal(hq.current.kind, RECIPIENT_PERSONAL);
  // Same display name as an HQ, different id: still a workspace with a picker.
  const lookalike = resolveRecipient({ ...page, workspaceName: 'Personal HQ' });
  assert.equal(lookalike.pickerVisible, true);
  assert.equal(lookalike.current.kind, RECIPIENT_WORKSPACE_ENTRY);
  // An unknown HQ id never matches an empty workspace id.
  assert.equal(resolveRecipient({ ...page, hqWorkspaceId: '' }).pickerVisible, true);
});

test('an unavailable Personal Assistant still offers the entry agent (FR6)', () => {
  for (const state of ['needs_hire', 'needs_hq', 'provisioning_hq', 'repair_needed']) {
    const out = resolveRecipient({
      ...page,
      personal: { name: 'Assistant', available: false, state }
    });
    assert.equal(out.current.kind, RECIPIENT_WORKSPACE_ENTRY, state);
    assert.equal(out.current.available, true);
    assert.equal(out.options[1].available, false);
    assert.equal(out.options[1].state, state);
    // Choosing it is allowed; the panel then shows the setup copy.
    const picked = resolveRecipient({
      ...page,
      personal: { name: 'Assistant', available: false, state },
      pick: RECIPIENT_PERSONAL
    });
    assert.equal(picked.current.kind, RECIPIENT_PERSONAL);
    assert.equal(picked.current.available, false);
  }
});

test('an explicit pick overrides the default (FR7)', () => {
  assert.equal(resolveRecipient({ ...page, pick: RECIPIENT_PERSONAL }).current.kind, 'personal');
  assert.equal(
    resolveRecipient({ ...page, pick: RECIPIENT_WORKSPACE_ENTRY }).current.kind,
    RECIPIENT_WORKSPACE_ENTRY
  );
  assert.equal(
    resolveRecipient({ ...page, pick: 'bogus' }).current.kind,
    RECIPIENT_WORKSPACE_ENTRY
  );
});

test('while the entry agent is still loading it stays the default', () => {
  const out = resolveRecipient({ ...page, entry: { status: 'loading' } });
  assert.equal(out.current.kind, RECIPIENT_WORKSPACE_ENTRY);
  assert.equal(out.current.loading, true);
  assert.equal(out.current.available, false);
  assert.equal(out.current.name, '');
  assert.equal(out.current.disabled, false);
});

test('the pick is stored per workspace under its own key', () => {
  const storage = memoryStorage();
  writePick('ws-1', RECIPIENT_PERSONAL, storage);
  assert.equal(storage.getItem(PICK_KEY_PREFIX + 'ws-1'), 'personal');
  assert.equal(readPick('ws-1', storage), RECIPIENT_PERSONAL);
  assert.equal(readPick('ws-2', storage), '');
  writePick('ws-1', 'bogus', storage);
  assert.equal(readPick('ws-1', storage), '');
  assert.equal(storage.data.has(PICK_KEY_PREFIX + 'ws-1'), false);
  writePick('', RECIPIENT_PERSONAL, storage);
  assert.equal(storage.length, 0);
  // Never the personal conversation's key.
  writePick('ws-1', RECIPIENT_PERSONAL, storage);
  assert.equal(storage.data.has('ori.personalAssistant.conversation'), false);
});

test('arriving on another workspace or an app-wide page drops other picks', () => {
  const storage = memoryStorage();
  writePick('ws-1', RECIPIENT_PERSONAL, storage);
  writePick('ws-2', RECIPIENT_PERSONAL, storage);
  storage.setItem('ori.personalAssistant.conversation', '{"id":"c1"}');
  writeEntrySession('ws-1', 'sess-1', storage);
  clearOtherPicks('ws-2', storage);
  assert.equal(readPick('ws-1', storage), '');
  assert.equal(readPick('ws-2', storage), RECIPIENT_PERSONAL);
  clearOtherPicks('', storage);
  assert.equal(readPick('ws-2', storage), '');
  // Only picks are cleared.
  assert.equal(storage.getItem('ori.personalAssistant.conversation'), '{"id":"c1"}');
  assert.equal(readEntrySession('ws-1', storage), 'sess-1');
});

test('the entry session id is stored per workspace and can be forgotten', () => {
  const storage = memoryStorage();
  writeEntrySession('ws-1', 'sess-1', storage);
  assert.equal(storage.getItem(SESSION_KEY_PREFIX + 'ws-1'), 'sess-1');
  assert.equal(readEntrySession('ws-1', storage), 'sess-1');
  assert.equal(readEntrySession('ws-2', storage), '');
  writeEntrySession('ws-1', '', storage);
  assert.equal(readEntrySession('ws-1', storage), '');
  assert.equal(storage.length, 0);
});

test('storage that throws never breaks resolution', () => {
  const broken = {
    length: 0,
    key: () => null,
    getItem: () => {
      throw new Error('denied');
    },
    setItem: () => {
      throw new Error('denied');
    },
    removeItem: () => {
      throw new Error('denied');
    }
  };
  assert.equal(readPick('ws-1', broken), '');
  writePick('ws-1', RECIPIENT_PERSONAL, broken);
  clearOtherPicks('ws-1', broken);
  assert.equal(readEntrySession('ws-1', broken), '');
  writeEntrySession('ws-1', 'x', broken);
});

test('the entry agent placeholder reuses the workspace copy', () => {
  assert.equal(
    entryAgentPlaceholder({ name: 'REAPER Assistant' }),
    'Ask REAPER Assistant about this workspace… (/task creates a task)'
  );
  assert.equal(entryAgentPlaceholder({ loading: true }), 'Finding this workspace’s Commander…');
});

// A fake server for the lookups: each route answers { status, body }.
function fakeFetch(routes) {
  const seen = [];
  const impl = async url => {
    seen.push(url);
    const route = routes[url];
    if (route instanceof Error) throw route;
    const { status = 200, body = null } = route || { status: 404, body: { error: 'not found' } };
    return { ok: status >= 200 && status < 300, status, json: async () => body };
  };
  impl.seen = seen;
  return impl;
}

const workspaceRoute = '/api/workspaces/ws-1';
const agentRoute = '/api/agents?name=REAPER%20Assistant';
const copiesRoute = '/api/workspaces/ws-1/agents';

test('a Commander the agent store knows is ready with its role and appearance', async () => {
  const fetchImpl = fakeFetch({
    [workspaceRoute]: {
      body: { id: 'ws-1', name: 'test_reaper', entry_agent_name: 'REAPER Assistant' }
    },
    [agentRoute]: {
      body: { name: 'REAPER Assistant', role: 'orchestrator', appearance: { mode: 'generated' } }
    }
  });
  const out = await loadEntryAgent('ws-1', { fetchImpl });
  assert.equal(out.workspaceName, 'test_reaper');
  assert.deepEqual(out.entry, {
    status: 'ready',
    name: 'REAPER Assistant',
    role: 'orchestrator',
    appearance: { mode: 'generated' }
  });
});

test('the loaded detail page is used instead of fetching the workspace', async () => {
  const fetchImpl = fakeFetch({ [agentRoute]: { body: { name: 'REAPER Assistant', role: 'x' } } });
  const out = await loadEntryAgent('ws-1', {
    fetchImpl,
    loadedWorkspace: {
      id: 'ws-1',
      name: 'test_reaper',
      shared_data: { entry_agent_name: 'REAPER Assistant' }
    }
  });
  assert.equal(out.entry.status, 'ready');
  assert.deepEqual(fetchImpl.seen, [agentRoute]);
  // Another workspace's detail is never borrowed.
  const other = fakeFetch({ [workspaceRoute]: { body: { id: 'ws-1', name: 'n' } } });
  await loadEntryAgent('ws-1', { fetchImpl: other, loadedWorkspace: { id: 'ws-9' } });
  assert.equal(other.seen[0], workspaceRoute);
});

test('a workspace-owned copy counts as runnable; a missing agent is a vacancy (FR4)', async () => {
  const withCopy = fakeFetch({
    [workspaceRoute]: {
      body: { workspace: { id: 'ws-1', name: 'w', entry_agent_name: 'REAPER Assistant' } }
    },
    [copiesRoute]: { body: { agents: [{ name: 'reaper assistant', role: 'orchestrator' }] } }
  });
  assert.equal((await loadEntryAgent('ws-1', { fetchImpl: withCopy })).entry.status, 'ready');

  const missing = fakeFetch({
    [workspaceRoute]: { body: { id: 'ws-1', name: 'w', entry_agent_name: 'REAPER Assistant' } },
    [copiesRoute]: { body: { agents: [{ name: 'Mixer' }] } }
  });
  assert.equal((await loadEntryAgent('ws-1', { fetchImpl: missing })).entry.status, 'vacant');

  const unassigned = fakeFetch({ [workspaceRoute]: { body: { id: 'ws-1', name: 'w' } } });
  assert.equal((await loadEntryAgent('ws-1', { fetchImpl: unassigned })).entry.status, 'vacant');
});

test('a failed lookup is never reported as a vacancy', async () => {
  const down = fakeFetch({ [workspaceRoute]: new Error('offline') });
  assert.equal((await loadEntryAgent('ws-1', { fetchImpl: down })).entry.status, 'error');
  // With the name known, an agent read failure still lets the server decide.
  const flaky = fakeFetch({
    [workspaceRoute]: { body: { id: 'ws-1', name: 'w', entry_agent_name: 'REAPER Assistant' } },
    [agentRoute]: { status: 500, body: {} }
  });
  const out = await loadEntryAgent('ws-1', { fetchImpl: flaky });
  assert.equal(out.entry.status, 'ready');
  assert.equal(out.entry.name, 'REAPER Assistant');
  // And the resolver shows the error as a disabled row, not "No Commander yet".
  const resolved = resolveRecipient({ ...page, entry: { status: 'error' } });
  assert.equal(resolved.current.kind, RECIPIENT_PERSONAL);
  assert.equal(resolved.options[0].disabled, true);
  assert.equal(resolved.options[0].vacant, false);
  assert.equal(resolved.options[0].role, 'Commander could not be loaded');
});

function controllerFor(context, routes, storage = memoryStorage()) {
  let ctx = context;
  const fetchImpl = fakeFetch(routes);
  const controller = createRecipientController({
    fetchImpl,
    storage,
    pageContext: () => ctx
  });
  return { controller, storage, fetchImpl, setContext: next => (ctx = next) };
}

const reaperRoutes = {
  [workspaceRoute]: {
    body: { id: 'ws-1', name: 'test_reaper', entry_agent_name: 'REAPER Assistant' }
  },
  [agentRoute]: { body: { name: 'REAPER Assistant', role: 'orchestrator' } }
};

test('the controller resolves the page, the pick and the hired assistant', async () => {
  const { controller, storage } = controllerFor(
    { surface: 'workspace_detail', workspace_id: 'ws-1' },
    reaperRoutes
  );
  const changes = [];
  controller.onChange(resolution =>
    changes.push(resolution.current.kind + ':' + resolution.current.name)
  );
  const pending = controller.refresh();
  // Synchronously the entry agent is the (loading) default.
  assert.equal(controller.current().kind, RECIPIENT_WORKSPACE_ENTRY);
  assert.equal(controller.current().loading, true);
  await pending;
  assert.equal(controller.current().name, 'REAPER Assistant');
  assert.equal(controller.current().subline, 'Commander · test_reaper');

  controller.setPersonal({ name: 'Mina', available: true, hqWorkspaceId: 'hq-1' });
  controller.pick(RECIPIENT_PERSONAL);
  assert.equal(controller.current().name, 'Mina');
  assert.equal(readPick('ws-1', storage), RECIPIENT_PERSONAL);
  controller.pick(RECIPIENT_WORKSPACE_ENTRY);
  assert.equal(controller.current().kind, RECIPIENT_WORKSPACE_ENTRY);
  assert.ok(changes.length >= 4);
});

test('the controller drops a pick and a slow lookup when the tab leaves the workspace', async () => {
  const storage = memoryStorage();
  writePick('ws-1', RECIPIENT_PERSONAL, storage);
  let release;
  const gate = new Promise(resolve => (release = resolve));
  let ctx = { surface: 'workspace_detail', workspace_id: 'ws-1' };
  const controller = createRecipientController({
    storage,
    pageContext: () => ctx,
    fetchImpl: async url => {
      await gate;
      const route = reaperRoutes[url];
      return { ok: Boolean(route), status: route ? 200 : 404, json: async () => route?.body };
    }
  });
  const slow = controller.refresh();
  ctx = { surface: 'home', workspace_id: '' };
  await controller.refresh();
  release();
  await slow;
  assert.equal(controller.current().kind, RECIPIENT_PERSONAL);
  assert.equal(controller.resolution().pickerVisible, false);
  assert.equal(readPick('ws-1', storage), '');
});

test('the controller keeps the entry session for this workspace in this tab', async () => {
  const { controller, storage } = controllerFor(
    { surface: 'workspace_canvas', workspace_id: 'ws-1' },
    reaperRoutes
  );
  await controller.refresh();
  assert.equal(controller.sessionId(), '');
  controller.rememberSession('ws-1', { id: 'sess-1' });
  assert.equal(controller.sessionId(), 'sess-1');
  assert.equal(readEntrySession('ws-1', storage), 'sess-1');
  controller.forgetSession();
  assert.equal(controller.sessionId(), '');
});

test('the Personal HQ id reaches the resolver through the hired assistant', async () => {
  const { controller } = controllerFor({ surface: 'workspace_detail', workspace_id: 'hq-1' }, {});
  controller.setPersonal({ name: 'Mina', available: true, hqWorkspaceId: 'hq-1' });
  await controller.refresh();
  assert.equal(controller.resolution().pickerVisible, false);
  assert.equal(controller.current().name, 'Mina');
});
