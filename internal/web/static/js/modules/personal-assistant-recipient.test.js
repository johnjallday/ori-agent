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
  createRecipientMenu,
  NEW_CONVERSATION_MARK,
  entryAgentPlaceholder,
  entrySessionRows,
  loadEntryAgent,
  loadEntrySession,
  menuKeyIntent,
  nextMenuIndex,
  readEntrySession,
  readPick,
  recipientButtonLabel,
  recipientMenuRows,
  resolveRecipient,
  switchLockMessage,
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

test('session messages map to user and assistant rows in order', () => {
  assert.deepEqual(
    entrySessionRows({
      messages: [
        { role: 'user', content: 'Add a reverb bus' },
        { role: 'tool', content: '{"ok":true}' },
        { role: 'assistant', content: 'Done.' },
        { role: 'system', content: 'hidden' },
        { role: 'assistant', content: '   ' },
        { role: 'user', content: 'Route the vocal' }
      ]
    }),
    [
      { role: 'user', content: 'Add a reverb bus' },
      { role: 'assistant', content: 'Done.' },
      { role: 'user', content: 'Route the vocal' }
    ]
  );
  assert.deepEqual(entrySessionRows(null), []);
  assert.deepEqual(entrySessionRows({}), []);
});

const latestRoute =
  '/api/sessions?folder_id=ws-1&agent_name=REAPER+Assistant&sort=updated_desc&limit=1';
const mira = name => ({
  id: name,
  agent_name: 'REAPER Assistant',
  title: 't-' + name,
  messages: []
});

test('the stored session wins over the latest one (FR20)', async () => {
  const fetchImpl = fakeFetch({
    '/api/sessions/sess-stored': { body: mira('sess-stored') },
    [latestRoute]: { body: { sessions: [{ id: 'sess-latest', agent_name: 'REAPER Assistant' }] } }
  });
  const out = await loadEntrySession({
    fetchImpl,
    workspaceId: 'ws-1',
    agentName: 'REAPER Assistant',
    storedId: 'sess-stored'
  });
  assert.equal(out.session.id, 'sess-stored');
  assert.deepEqual(fetchImpl.seen, ['/api/sessions/sess-stored']);
});

test('without a stored session the latest one with the entry agent is used', async () => {
  const fetchImpl = fakeFetch({
    [latestRoute]: { body: { sessions: [{ id: 'sess-latest', agent_name: 'REAPER Assistant' }] } },
    '/api/sessions/sess-latest': { body: mira('sess-latest') }
  });
  const out = await loadEntrySession({
    fetchImpl,
    workspaceId: 'ws-1',
    agentName: 'REAPER Assistant',
    storedId: ''
  });
  assert.equal(out.session.id, 'sess-latest');
  assert.equal(out.stored, false);
  assert.deepEqual(fetchImpl.seen, [latestRoute, '/api/sessions/sess-latest']);
});

test('none is a new conversation; a gone or reassigned stored session falls back', async () => {
  const none = fakeFetch({ [latestRoute]: { body: { sessions: [] } } });
  assert.deepEqual(
    await loadEntrySession({
      fetchImpl: none,
      workspaceId: 'ws-1',
      agentName: 'REAPER Assistant',
      storedId: ''
    }),
    { session: null, stored: false, error: false }
  );
  const reassigned = fakeFetch({
    '/api/sessions/sess-old': { body: { id: 'sess-old', agent_name: 'Old Commander' } },
    [latestRoute]: { body: { sessions: [] } }
  });
  const out = await loadEntrySession({
    fetchImpl: reassigned,
    workspaceId: 'ws-1',
    agentName: 'REAPER Assistant',
    storedId: 'sess-old'
  });
  assert.equal(out.session, null);
  assert.equal(out.error, false);
  const gone = fakeFetch({ [latestRoute]: { body: { sessions: [] } } });
  const goneOut = await loadEntrySession({
    fetchImpl: gone,
    workspaceId: 'ws-1',
    agentName: 'REAPER Assistant',
    storedId: 'sess-deleted'
  });
  assert.equal(goneOut.error, false);
});

test('a server failure is an error that keeps the stored session', async () => {
  const down = fakeFetch({ '/api/sessions/sess-1': { status: 500, body: {} } });
  const out = await loadEntrySession({
    fetchImpl: down,
    workspaceId: 'ws-1',
    agentName: 'REAPER Assistant',
    storedId: 'sess-1'
  });
  assert.deepEqual(out, { session: null, stored: true, error: true });
  const offline = fakeFetch({ [latestRoute]: new Error('offline') });
  assert.equal(
    (
      await loadEntrySession({
        fetchImpl: offline,
        workspaceId: 'ws-1',
        agentName: 'REAPER Assistant',
        storedId: ''
      })
    ).error,
    true
  );
});

test('after New the latest session is not offered again until a message creates one', async () => {
  const fetchImpl = fakeFetch({
    ...reaperRoutes,
    [latestRoute]: { body: { sessions: [{ id: 'sess-latest', agent_name: 'REAPER Assistant' }] } },
    '/api/sessions/sess-latest': { body: mira('sess-latest') }
  });
  const storage = memoryStorage();
  const controller = createRecipientController({
    fetchImpl,
    storage,
    pageContext: () => ({ surface: 'workspace_detail', workspace_id: 'ws-1' })
  });
  const sessions = [];
  controller.onSession(session => sessions.push(session));
  await controller.refresh();
  const first = await controller.loadSession();
  assert.equal(first.session.id, 'sess-latest');
  assert.deepEqual(controller.session(), {
    workspaceId: 'ws-1',
    id: 'sess-latest',
    title: 't-sess-latest'
  });
  assert.equal(controller.sessionId(), 'sess-latest');

  controller.forgetSession();
  assert.equal(storage.getItem(SESSION_KEY_PREFIX + 'ws-1'), NEW_CONVERSATION_MARK);
  assert.equal(controller.sessionId(), '');
  const seenBefore = fetchImpl.seen.length;
  const fresh = await controller.loadSession();
  assert.equal(fresh.session, null);
  assert.equal(fresh.fresh, true);
  // No server read at all, and the mark is kept.
  assert.equal(fetchImpl.seen.length, seenBefore);
  assert.equal(storage.getItem(SESSION_KEY_PREFIX + 'ws-1'), NEW_CONVERSATION_MARK);

  controller.rememberSession('ws-1', { id: 'sess-new', title: 'Route the vocal' });
  assert.equal(controller.sessionId(), 'sess-new');
  assert.equal(controller.session().title, 'Route the vocal');
  assert.deepEqual(
    sessions.map(session => session.id),
    ['sess-latest', '', 'sess-new']
  );
});

test('loading a session is a no-op unless the entry agent is the ready recipient', async () => {
  const { controller, fetchImpl } = controllerFor({ surface: 'home', workspace_id: '' }, {});
  await controller.refresh();
  assert.deepEqual(await controller.loadSession(), { session: null, error: false });
  assert.deepEqual(fetchImpl.seen, []);
});

test('the pick survives a reload and a mode change of the same workspace (FR7)', async () => {
  const storage = memoryStorage();
  let ctx = { surface: 'workspace_detail', workspace_id: 'ws-1' };
  const first = createRecipientController({
    fetchImpl: fakeFetch(reaperRoutes),
    storage,
    pageContext: () => ctx
  });
  await first.refresh();
  first.pick(RECIPIENT_PERSONAL);
  // Map → Details → Dashboard → Tickets keep the surface and the workspace id.
  await first.refresh();
  assert.equal(first.current().kind, RECIPIENT_PERSONAL);
  // A reload is a new controller over the same tab storage.
  const reloaded = createRecipientController({
    fetchImpl: fakeFetch(reaperRoutes),
    storage,
    pageContext: () => ctx
  });
  await reloaded.refresh();
  assert.equal(reloaded.current().kind, RECIPIENT_PERSONAL);
  // The canvas of the same workspace is still the same visit.
  ctx = { surface: 'workspace_canvas', workspace_id: 'ws-1' };
  await reloaded.refresh();
  assert.equal(reloaded.current().kind, RECIPIENT_PERSONAL);
  // Leaving for an app-wide page drops it; coming back starts at the default.
  ctx = { surface: 'workspace_hub', workspace_id: '' };
  await reloaded.refresh();
  ctx = { surface: 'workspace_detail', workspace_id: 'ws-1' };
  await reloaded.refresh();
  assert.equal(reloaded.current().kind, RECIPIENT_WORKSPACE_ENTRY);
});

test('a workspace agent is not addressable until the hired assistant status is known (FR5)', () => {
  const waiting = resolveRecipient({ ...page, personal: { known: false } });
  assert.equal(waiting.current.kind, RECIPIENT_WORKSPACE_ENTRY);
  assert.equal(waiting.current.loading, true);
  assert.equal(waiting.current.pending, true);
  assert.equal(waiting.current.available, false);
  // A vacancy is already certain without the status.
  const vacant = resolveRecipient({
    ...page,
    entry: { status: 'vacant' },
    personal: { known: false }
  });
  assert.equal(vacant.current.kind, RECIPIENT_PERSONAL);
  assert.equal(vacant.options[0].pending, false);
  // Once known, the same page addresses it.
  assert.equal(resolveRecipient(page).current.available, true);
});

test('the Personal HQ is recognised from this tab before the status answers', async () => {
  const storage = memoryStorage();
  storage.setItem('ori.personalAssistant.hqWorkspaceId', 'hq-1');
  const hqRoutes = {
    '/api/workspaces/hq-1': {
      body: { id: 'hq-1', name: 'My HQ', entry_agent_name: 'Atlas HQ Agent' }
    },
    '/api/agents?name=Atlas%20HQ%20Agent': { body: { name: 'Atlas HQ Agent', role: 'x' } }
  };
  const controller = createRecipientController({
    fetchImpl: fakeFetch(hqRoutes),
    storage,
    pageContext: () => ({ surface: 'workspace_detail', workspace_id: 'hq-1' }),
    waitForPersonal: true
  });
  await controller.refresh();
  assert.equal(controller.resolution().pickerVisible, false);
  assert.equal(controller.current().kind, RECIPIENT_PERSONAL);
  // A status that could not be read keeps the HQ id; a real one replaces it.
  controller.setPersonal({ name: 'Atlas', available: false });
  assert.equal(controller.resolution().pickerVisible, false);
  controller.setPersonal({ name: 'Atlas', available: true, hqWorkspaceId: 'hq-2' });
  assert.equal(storage.getItem('ori.personalAssistant.hqWorkspaceId'), 'hq-2');
  assert.equal(controller.current().kind, RECIPIENT_WORKSPACE_ENTRY);
  assert.equal(controller.current().name, 'Atlas HQ Agent');

  // Without a remembered HQ, the HQ's own agent waits for the status.
  const fresh = createRecipientController({
    fetchImpl: fakeFetch(hqRoutes),
    storage: memoryStorage(),
    pageContext: () => ({ surface: 'workspace_detail', workspace_id: 'hq-1' }),
    waitForPersonal: true
  });
  await fresh.refresh();
  assert.equal(fresh.current().available, false);
  fresh.setPersonal({ name: 'Atlas', available: true, hqWorkspaceId: 'hq-1' });
  assert.equal(fresh.current().kind, RECIPIENT_PERSONAL);
  assert.equal(fresh.resolution().pickerVisible, false);
});

test('switching is refused while a reply or a review is pending (FR14, FR15)', () => {
  const mira = { name: 'Mira' };
  assert.equal(switchLockMessage(mira, {}), '');
  assert.equal(
    switchLockMessage(mira, { busy: true }),
    'Wait for Mira to reply before switching. Your message is kept.'
  );
  assert.equal(
    switchLockMessage(mira, { hasPendingConfirmation: true }),
    'Answer the review Mira is waiting on before switching. Your message is kept.'
  );
  assert.match(switchLockMessage(mira, { hasPlanning: true }), /review Mira is waiting on/);
  assert.match(switchLockMessage(mira, { loading: true }), /conversation to open/);
  // Busy wins over a review: the reply is what the user is waiting for.
  assert.match(switchLockMessage(mira, { busy: true, hasPlanning: true }), /^Wait for Mira/);
});

test('menu rows list the entry agent first and mark the current recipient (FR9)', () => {
  const rows = recipientMenuRows(resolveRecipient(page));
  assert.deepEqual(
    rows.map(row => [row.kind, row.name, row.role, row.checked, row.disabled]),
    [
      [RECIPIENT_WORKSPACE_ENTRY, 'REAPER Assistant', 'Commander · test_reaper', true, false],
      [RECIPIENT_PERSONAL, 'Mina', 'Personal Assistant', false, false]
    ]
  );
  // Locked: only the other row is disabled, and it says why when chosen.
  const locked = recipientMenuRows(resolveRecipient(page), { locked: true });
  assert.equal(locked[0].disabled, false);
  assert.equal(locked[1].disabled, true);
  assert.equal(locked[1].locked, true);
  // A vacant Commander's row is disabled and offers the Roles link (FR4).
  const vacant = recipientMenuRows(resolveRecipient({ ...page, entry: { status: 'vacant' } }), {
    fillHref: '/workspaces/test-reaper?mode=details'
  });
  assert.equal(vacant[0].disabled, true);
  assert.equal(vacant[0].role, 'No Commander yet');
  assert.equal(vacant[0].fillHref, '/workspaces/test-reaper?mode=details');
  assert.equal(vacant[1].checked, true);
  // An unset assistant can still be chosen; its row says it is not set up (FR6).
  const unhired = recipientMenuRows(
    resolveRecipient({ ...page, personal: { name: 'Assistant', available: false } })
  );
  assert.equal(unhired[1].disabled, false);
  assert.equal(unhired[1].role, 'Personal Assistant · not set up');
  assert.deepEqual(recipientMenuRows(resolveRecipient({ ...page, surface: 'home' })), []);
});

test('the menu keys and wrap-around navigation (FR12)', () => {
  assert.equal(menuKeyIntent('ArrowDown'), 'next');
  assert.equal(menuKeyIntent('ArrowUp'), 'prev');
  assert.equal(menuKeyIntent('Home'), 'first');
  assert.equal(menuKeyIntent('End'), 'last');
  assert.equal(menuKeyIntent('Enter'), 'choose');
  assert.equal(menuKeyIntent(' '), 'choose');
  assert.equal(menuKeyIntent('Escape'), 'close');
  assert.equal(menuKeyIntent('Tab'), 'tab');
  assert.equal(menuKeyIntent('a'), null);
  assert.equal(nextMenuIndex(0, 2, 'next'), 1);
  assert.equal(nextMenuIndex(1, 2, 'next'), 0);
  assert.equal(nextMenuIndex(0, 2, 'prev'), 1);
  assert.equal(nextMenuIndex(-1, 3, 'next'), 0);
  assert.equal(nextMenuIndex(-1, 3, 'prev'), 2);
  assert.equal(nextMenuIndex(1, 3, 'first'), 0);
  assert.equal(nextMenuIndex(0, 3, 'last'), 2);
  assert.equal(nextMenuIndex(0, 0, 'next'), -1);
});

test('the menu button is named after the selected recipient', () => {
  const resolved = resolveRecipient(page);
  assert.equal(
    recipientButtonLabel(resolved.current),
    'REAPER Assistant, Commander · test_reaper. Choose who to talk to'
  );
  assert.equal(
    recipientButtonLabel(resolved.options[1]),
    'Mina, Personal Assistant. Choose who to talk to'
  );
});

// A minimal DOM for the menu controller: enough elements, events and focus to
// drive it from the keyboard the way a browser would.
function fakeDocument() {
  const doc = { activeElement: null, listeners: {} };
  function element(tag) {
    const el = {
      tagName: tag.toUpperCase(),
      children: [],
      parent: null,
      attributes: {},
      dataset: {},
      listeners: {},
      hidden: false,
      textContent: '',
      innerHTML: '',
      setAttribute(name, value) {
        el.attributes[name] = String(value);
      },
      getAttribute(name) {
        return name in el.attributes ? el.attributes[name] : null;
      },
      append(...nodes) {
        for (const node of nodes) {
          node.parent = el;
          el.children.push(node);
        }
      },
      replaceChildren() {
        el.children = [];
      },
      addEventListener(type, fn) {
        (el.listeners[type] ||= []).push(fn);
      },
      dispatch(type, init = {}) {
        const event = {
          type,
          key: init.key,
          target: init.target || el,
          defaultPrevented: false,
          propagationStopped: false,
          preventDefault() {
            this.defaultPrevented = true;
          },
          stopPropagation() {
            this.propagationStopped = true;
          }
        };
        for (let node = el; node && !event.propagationStopped; node = node.parent) {
          for (const fn of node.listeners[type] || []) fn(event);
        }
        if (!event.propagationStopped) for (const fn of doc.listeners[type] || []) fn(event);
        return event;
      },
      click() {
        return el.dispatch('click');
      },
      focus() {
        doc.activeElement = el;
      },
      contains(node) {
        for (let current = node; current; current = current.parent) if (current === el) return true;
        return false;
      },
      querySelectorAll() {
        return el.children.filter(child =>
          ['menuitemradio', 'menuitem'].includes(child.getAttribute('role'))
        );
      }
    };
    return el;
  }
  doc.createElement = element;
  doc.addEventListener = (type, fn) => (doc.listeners[type] ||= []).push(fn);
  return doc;
}

function mountMenu({ locked = false } = {}) {
  const doc = fakeDocument();
  const button = doc.createElement('button');
  const menu = doc.createElement('div');
  menu.hidden = true;
  const chosen = [];
  const refused = [];
  let resolution = resolveRecipient(page);
  const controller = createRecipientMenu({
    button,
    menu,
    doc,
    rows: () => recipientMenuRows(resolution, { locked }),
    choose: kind => {
      chosen.push(kind);
      resolution = resolveRecipient({ ...page, pick: kind });
    },
    refuse: row => refused.push(row.kind),
    avatar: () => ''
  });
  return { doc, button, menu, controller, chosen, refused };
}

test('the menu opens, moves, chooses and closes from the keyboard', () => {
  const { doc, button, menu, controller, chosen } = mountMenu();
  // Enter on the button activates it like a click.
  button.click();
  assert.equal(controller.isOpen(), true);
  assert.equal(menu.hidden, false);
  assert.equal(button.getAttribute('aria-expanded'), 'true');
  const [entry, personal] = menu.children;
  assert.equal(entry.getAttribute('role'), 'menuitemradio');
  assert.equal(entry.getAttribute('aria-checked'), 'true');
  assert.equal(personal.getAttribute('aria-checked'), 'false');
  // Focus starts on the checked row.
  assert.equal(doc.activeElement, entry);
  menu.dispatch('keydown', { key: 'ArrowDown', target: entry });
  assert.equal(doc.activeElement, personal);
  menu.dispatch('keydown', { key: 'ArrowDown', target: personal });
  assert.equal(doc.activeElement, entry, 'wraps');
  menu.dispatch('keydown', { key: 'End' });
  assert.equal(doc.activeElement, personal);
  menu.dispatch('keydown', { key: 'Enter' });
  assert.deepEqual(chosen, [RECIPIENT_PERSONAL]);
  assert.equal(controller.isOpen(), false);
  assert.equal(doc.activeElement, button, 'focus returns to the button');
  // Choosing the current recipient only closes.
  button.click();
  menu.dispatch('keydown', { key: ' ' });
  assert.deepEqual(chosen, [RECIPIENT_PERSONAL]);
});

test('Escape closes only the menu, and a click outside closes it', () => {
  const { doc, button, menu, controller } = mountMenu();
  button.click();
  const escape = menu.dispatch('keydown', { key: 'Escape' });
  assert.equal(escape.defaultPrevented, true);
  assert.equal(escape.propagationStopped, true, 'the drawer never sees it');
  assert.equal(controller.isOpen(), false);
  assert.equal(doc.activeElement, button);
  // Escape on the button while open does the same.
  button.click();
  const onButton = button.dispatch('keydown', { key: 'Escape' });
  assert.equal(onButton.propagationStopped, true);
  assert.equal(controller.isOpen(), false);
  // ArrowUp on the closed button opens on the last row.
  button.dispatch('keydown', { key: 'ArrowUp' });
  assert.equal(controller.isOpen(), true);
  assert.equal(doc.activeElement, menu.children[1]);
  const outside = doc.createElement('div');
  for (const fn of doc.listeners.click) fn({ target: outside });
  assert.equal(controller.isOpen(), false);
});

test('a locked row explains itself instead of switching', () => {
  const { button, menu, chosen, refused, controller } = mountMenu({ locked: true });
  button.click();
  const personal = menu.children[1];
  assert.equal(personal.getAttribute('aria-disabled'), 'true');
  personal.click();
  assert.deepEqual(chosen, []);
  assert.deepEqual(refused, [RECIPIENT_PERSONAL]);
  assert.equal(controller.isOpen(), true, 'the menu stays open for another choice');
});
