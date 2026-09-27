import test from 'node:test';
import assert from 'node:assert/strict';
import { ProjectLibraryPanel, libraryQuery, readActivationQueue } from './project-library.js';

test('canceling a reviewed scan or disconnect refreshes the Home revision before retry', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { revision: 1 };
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  panel.confirm = async () => false;
  const calls = [];
  panel.post = async (path, body) => {
    calls.push({ path, body });
    if (!path.endsWith('/review')) throw new Error('cancel must never commit');
    return {
      token: 'inert-review',
      root_path: '/sandbox/Documents',
      entry_count: 5,
      max_entries: 5000
    };
  };
  panel.refresh = async () => {
    panel.state.revision++;
  };
  await panel.revokeRoot({ id: 'root', path: '/sandbox/Documents' });
  await panel.scanRootFlow('root');
  assert.deepEqual(calls, [
    { path: '/roots/root/revoke/review', body: { if_revision: 1 } },
    { path: '/roots/root/scans/review', body: { if_revision: 2 } }
  ]);
  assert.equal(panel.state.revision, 3);
});

test('pending direct links require a separate review and never commit on cancellation', async () => {
  const original = globalThis.document;
  const elements = new Map();
  const makeNode = tag => ({
    tag,
    children: [],
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener(_event, fn) {
      this.click = fn;
    }
  });
  elements.set('projectLibraryPendingLinks', makeNode('section'));
  elements.set('projectLibraryPendingRows', makeNode('div'));
  globalThis.document = {
    createElement: makeNode,
    getElementById: id => elements.get(id) || null
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.state = { provider_read_only: false };
    panel.run = async (_trigger, _message, work) => work();
    const calls = [];
    let refreshes = 0;
    panel.refresh = async () => {
      refreshes++;
    };
    panel.status = () => {};
    panel.request = async path => {
      assert.equal(path, '/linked-projects/pending');
      return { revision: 6, total: 1, rows: [{ name: 'Song', workspace_id: 'child' }] };
    };
    panel.post = async (path, body) => {
      calls.push({ path, body });
      if (path.endsWith('/review'))
        return { token: 'review-token', project_name: 'Song', link_only: true };
      return { entry_id: 'association' };
    };
    panel.confirm = async () => false;
    await panel.renderPendingLinks();
    assert.equal(elements.get('projectLibraryPendingLinks').hidden, false);
    elements.get('projectLibraryPendingRows').children[0].children[1].click();
    await new Promise(setImmediate);
    assert.deepEqual(calls, [{ path: '/linked-projects/child/review', body: { revision: 6 } }]);
    assert.equal(refreshes, 1); // Canceled reviews still advance the Home revision.
    panel.confirm = async () => true;
    await panel.renderPendingLinks();
    elements.get('projectLibraryPendingRows').children[0].children[1].click();
    await new Promise(setImmediate);
    assert.equal(calls[2].path, '/linked-projects/child/commit');
    assert.equal(calls[2].body.review_token, 'review-token');
    assert.equal(calls[2].body.confirm, true);
    assert.match(calls[2].body.idempotency_key, /^library-associate-linked-project-/);
    assert.equal(refreshes, 2);
  } finally {
    globalThis.document = original;
  }
});

test('serial queue recovery retains only bounded Home-scoped navigation and exact pending retry', () => {
  const now = Date.now();
  const key = 'ori:library-queue:home';
  const values = new Map();
  const storage = { getItem: id => values.get(id) || null };
  const valid = {
    home_id: 'home',
    ids: ['one', 'two'],
    index: 1,
    created_at: now,
    pending: { id: 'two', token: 'review-token', key: 'confirmed-key' }
  };
  values.set(key, JSON.stringify(valid));
  assert.deepEqual(readActivationQueue('home', storage, now), valid);
  assert.equal(readActivationQueue('foreign', storage, now), null);
  for (const changed of [
    { ids: ['one', 'one'] },
    { ids: Array.from({ length: 101 }, (_, i) => `id-${i}`) },
    { index: 2 },
    { pending: { id: 'one', token: 'review-token', key: 'confirmed-key' } },
    { created_at: now - 25 * 60 * 60 * 1000 }
  ]) {
    values.set(key, JSON.stringify({ ...valid, ...changed }));
    assert.equal(readActivationQueue('home', storage, now), null);
  }
});

test('serial activation pauses after a skip without issuing even a review request', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home',
    ids: ['first', 'second'],
    index: 0,
    created_at: Date.now(),
    pending: null
  };
  panel.run = async (_trigger, _message, work) => work();
  panel.saveQueue = () => {};
  panel.status = () => {};
  const requests = [];
  panel.request = async path => {
    requests.push(path);
    return path.endsWith('/activation')
      ? { state: 'review_available' }
      : {
          row: {
            id: path.split('/').at(-1),
            name: path.split('/').at(-1),
            connection: 'catalog_only'
          }
        };
  };
  panel.post = async () => {
    throw new Error('skip/pause must not create a review or project');
  };
  const actions = ['skip', 'pause'];
  panel.queueChoice = async () => actions.shift();
  await panel.continueQueue();
  assert.equal(panel.queue.index, 1);
  assert.equal(panel.queue.pending, null);
  assert.deepEqual(requests, [
    '/projects/first',
    '/projects/first/activation',
    '/projects/second',
    '/projects/second/activation'
  ]);
});

test('serial activation persists a distinct confirmed key before each creator commit', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home',
    ids: ['first', 'second'],
    index: 0,
    created_at: Date.now(),
    pending: null
  };
  panel.run = async (_trigger, _message, work) => work();
  panel.status = () => {};
  panel.refresh = async () => {};
  panel.queueChoice = async () => 'review';
  panel.activationInput = async detail => ({
    workspace_name: detail.row.name,
    project_file: 'Song.rpp',
    if_revision: detail.revision
  });
  panel.confirm = async () => true;
  const connected = new Set();
  panel.request = async path =>
    path.endsWith('/activation')
      ? { state: 'review_available' }
      : {
          revision: 1,
          row: {
            id: path.split('/').at(-1),
            name: path.split('/').at(-1),
            connection: connected.has(path.split('/').at(-1)) ? 'connected' : 'catalog_only'
          }
        };
  const storedBeforeCommit = [];
  let lastSaved = null;
  panel.saveQueue = () => {
    lastSaved = panel.queue && structuredClone(panel.queue);
  };
  panel.post = async (path, body) => {
    if (path.endsWith('/review'))
      return {
        token: path,
        workspace_name: path.split('/')[2],
        project_file: 'Song.rpp',
        statement: 'File-only'
      };
    storedBeforeCommit.push(lastSaved?.pending);
    assert.equal(lastSaved?.pending?.key, body.idempotency_key);
    connected.add(path.split('/')[2]);
    return { workspace_id: 'child' };
  };
  await panel.continueQueue();
  assert.equal(panel.queue, null);
  assert.equal(connected.size, 2);
  assert.equal(storedBeforeCommit.length, 2);
  assert.notEqual(storedBeforeCommit[0].key, storedBeforeCommit[1].key);
});

test('serial queue resumes after a lost creator response without creating the connected song twice', async () => {
  const previousStorage = globalThis.sessionStorage;
  const values = new Map();
  globalThis.sessionStorage = {
    getItem: key => values.get(key) || null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key)
  };
  try {
    let connected = false;
    let commits = 0;
    let refreshes = 0;
    let lastStatus = '';
    const configure = panel => {
      panel.state = { provider_read_only: false };
      panel.run = async (_trigger, _message, work) => work();
      panel.status = message => {
        lastStatus = message;
      };
      panel.refresh = async () => {
        refreshes++;
      };
      panel.renderQueueControls = () => {};
      panel.request = async path =>
        path.endsWith('/activation')
          ? { state: 'review_available' }
          : {
              row: {
                id: path.split('/')[2],
                name: 'Album',
                connection: connected ? 'connected' : 'catalog_only'
              }
            };
    };
    const first = new ProjectLibraryPanel({ workspaceId: 'home' });
    configure(first);
    first.queue = {
      home_id: 'home',
      ids: ['first', 'second'],
      index: 0,
      created_at: Date.now(),
      pending: null
    };
    first.queueChoice = async () => 'review';
    first.activationInput = async () => ({ workspace_name: 'Album', project_file: 'Song.rpp' });
    first.confirm = async () => true;
    first.post = async path => {
      if (path.endsWith('/review'))
        return { token: 'exact-review', workspace_name: 'Album', project_file: 'Song.rpp' };
      commits++;
      connected = true;
      throw new Error('response lost after creator committed');
    };
    await first.continueQueue();
    assert.equal(commits, 1, lastStatus);
    assert.equal(first.queue.index, 0);
    assert.equal(refreshes, 1);
    assert.match(lastStatus, /resume to reconcile this confirmed song/);
    const persisted = readActivationQueue('home', globalThis.sessionStorage);
    assert.equal(persisted.pending.id, 'first');
    assert.equal(persisted.pending.token, 'exact-review');

    const resumed = new ProjectLibraryPanel({ workspaceId: 'home' });
    configure(resumed);
    resumed.queueChoice = async () => 'skip';
    resumed.post = async () => {
      throw new Error('no creator or review request permitted on replay');
    };
    await resumed.continueQueue();
    assert.equal(commits, 1);
    assert.equal(connected, true);
    assert.equal(resumed.queue, null);
    assert.equal(values.has('ori:library-queue:home'), false);
  } finally {
    globalThis.sessionStorage = previousStorage;
  }
});

test('serial queue retries the exact confirmed operation after creator success but absent Home association', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.queue = {
    home_id: 'home',
    ids: ['first', 'second'],
    index: 0,
    created_at: Date.now(),
    pending: { id: 'first', token: 'review-1', key: 'first-confirmed-key' }
  };
  panel.run = async (_trigger, _message, work) => work();
  panel.saveQueue = () => {};
  panel.status = () => {};
  panel.refresh = async () => {};
  panel.request = async path =>
    path.endsWith('/activation')
      ? { state: 'review_available' }
      : { row: { id: path.split('/')[2], name: 'Album', connection: 'catalog_only' } };
  panel.queueChoice = async () => 'pause';
  const attempts = [];
  panel.post = async (path, body) => {
    attempts.push({ path, body });
    return { workspace_id: 'creator-child' };
  };
  await panel.continueQueue();
  assert.deepEqual(attempts, [
    {
      path: '/projects/first/activation/commit',
      body: { review_token: 'review-1', idempotency_key: 'first-confirmed-key', confirm: true }
    }
  ]);
  assert.equal(panel.queue.index, 1);
  assert.equal(panel.queue.pending, null);
});

test('Home resume renders only bounded saved-user cards with trusted detail navigation', async () => {
  const previousDocument = globalThis.document;
  const makeElement = tag => ({
    tag,
    children: [],
    textContent: '',
    hidden: false,
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener(name, callback) {
      this[name] = callback;
    }
  });
  const section = makeElement('section');
  const container = makeElement('div');
  globalThis.document = {
    createElement: makeElement,
    getElementById(id) {
      return { projectLibraryResume: section, projectLibraryResumeCards: container }[id];
    }
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    const requests = [];
    panel.request = async path => {
      requests.push(path);
      return {
        cards: Array.from({ length: 4 }, (_, index) => ({
          entry_id: `id-${index}`,
          name: `<Song ${index}>`,
          project_next_action: 'Next',
          session: { goal: 'Listen', recap: 'Saved by user', updated_at: '2026-09-27T00:00:00Z' }
        }))
      };
    };
    panel.details = id => requests.push(`open:${id}`);
    await panel.renderResume();
    assert.deepEqual(requests, ['/resume']);
    assert.equal(section.hidden, false);
    assert.equal(container.children.length, 3);
    assert.equal(container.children[0].children[0].textContent, '<Song 0>');
    const button = container.children[0].children.at(-1);
    button.click();
    assert.deepEqual(requests, ['/resume', 'open:id-0']);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('resume workspace action refuses navigation after the exact link changes', async () => {
  const previousDocument = globalThis.document;
  const makeElement = tag => ({
    tag,
    children: [],
    textContent: '',
    hidden: false,
    append(...items) {
      this.children.push(...items);
    },
    replaceChildren() {
      this.children = [];
    },
    addEventListener(name, callback) {
      this[name] = callback;
    }
  });
  const section = makeElement('section');
  const container = makeElement('div');
  globalThis.document = {
    createElement: makeElement,
    getElementById(id) {
      return { projectLibraryResume: section, projectLibraryResumeCards: container }[id];
    }
  };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.run = async (_trigger, _message, work) => work();
    const messages = [];
    panel.status = message => messages.push(message);
    const requests = [];
    panel.request = async path => {
      requests.push(path);
      if (path.endsWith('/activation'))
        return { state: 'link_needs_review', workspace_id: 'stale-child' };
      return {
        cards: [
          {
            entry_id: 'saved-song',
            name: 'Song',
            workspace_id: 'stale-child',
            session: { goal: 'Listen', updated_at: '2026-09-27T00:00:00Z' }
          }
        ]
      };
    };
    await panel.renderResume();
    const workspace = container.children[0].children.at(-1);
    assert.equal(workspace.textContent, 'Open Song workspace');
    workspace.click();
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(requests, ['/resume', '/projects/saved-song/activation', '/resume']);
    assert.match(messages.at(-1), /link changed/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('libraryQuery includes only bounded search fields and cursor', () => {
  const params = new URLSearchParams(
    libraryQuery({
      text: '  Mix  ',
      stage: 'mixing',
      format: 'reaper',
      rootID: 'server-root-id',
      availability: 'available',
      priority: '0',
      sort: 'scanned_at',
      direction: 'desc',
      cursor: 'next'
    })
  );
  assert.equal(params.get('text'), 'Mix');
  assert.equal(params.get('page_size'), '25');
  assert.equal(params.get('stage'), 'mixing');
  assert.equal(params.get('format'), 'reaper');
  assert.equal(params.get('root_id'), 'server-root-id');
  assert.equal(params.get('availability'), 'available');
  assert.equal(params.get('priority'), '0');
  assert.equal(params.get('sort'), 'scanned_at');
  assert.equal(params.get('direction'), 'desc');
  assert.equal(params.get('cursor'), 'next');
  assert.equal(params.has('path'), false);
});

test('out-of-order search responses and double-clicked More cannot mix or duplicate pages', async () => {
  const priorDocument = globalThis.document;
  const elements = {
    projectLibraryRows: { replaceChildren() {} },
    projectLibraryMore: { hidden: false, disabled: false },
    projectLibraryCount: { textContent: '' },
    projectLibrarySearch: { value: 'first' },
    projectLibraryStage: { value: '' },
    projectLibraryStatusFilter: { value: '' },
    projectLibraryConnection: { value: '' },
    projectLibraryAvailability: { value: '' },
    projectLibraryPriority: { value: '' },
    projectLibrarySort: { value: 'name' }
  };
  globalThis.document = { getElementById: id => elements[id] };
  try {
    const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
    panel.state = { provider_read_only: false };
    panel.renderRows = () => {};
    panel.status = () => {};
    const requests = [];
    panel.request = path =>
      new Promise(resolve => {
        requests.push({ path, resolve });
      });
    const first = panel.search(false);
    elements.projectLibrarySearch.value = 'second';
    const second = panel.search(false);
    assert.equal(requests.length, 2);
    requests[1].resolve({ rows: [{ id: 'new' }], total: 1, next_cursor: 'next' });
    await second;
    requests[0].resolve({ rows: [{ id: 'old' }], total: 1, next_cursor: 'old-next' });
    await first;
    assert.deepEqual(
      panel.rows.map(row => row.id),
      ['new']
    );
    assert.equal(panel.cursor, 'next');
    assert.match(requests[1].path, /text=second/);

    const more = panel.search(true);
    const duplicate = panel.search(true);
    assert.equal(requests.length, 3, 'only one cursor request may be in flight');
    assert.equal(elements.projectLibraryMore.disabled, true);
    requests[2].resolve({ rows: [{ id: 'next' }], total: 2 });
    await Promise.all([more, duplicate]);
    assert.deepEqual(
      panel.rows.map(row => row.id),
      ['new', 'next']
    );
    assert.equal(elements.projectLibraryMore.disabled, false);
    assert.equal(elements.projectLibraryMore.hidden, true);
  } finally {
    globalThis.document = priorDocument;
  }
});

test('folder selection and consent never scan after a canceled second review', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 1 };
  const calls = [];
  panel.run = async (_, __, fn) => fn();
  panel.post = async (path, body) => {
    calls.push([path, body]);
    if (path === '/roots/pick') return { selection_token: 'picker' };
    if (path === '/roots/review')
      return { token: 'review', root_path: '/chosen', scope: 'metadata only' };
    return { root_id: 'root-1' };
  };
  panel.confirm = async () => calls.filter(([path]) => path === '/roots/commit').length === 0;
  panel.refresh = async () => {
    panel.state.revision = 3;
  };
  panel.scanRootFlow = async () => calls.push(['/scan', null]);
  await panel.addFolder(null);
  assert.deepEqual(
    calls.map(([path]) => path),
    ['/roots/pick', '/roots/review', '/roots/commit']
  );
  assert.equal(calls[2][1].confirm, true);
  assert.equal(calls[1][1].selection_token, 'picker');
  assert.equal(Object.hasOwn(calls[1][1], 'path'), false);
});

test('resolved portfolio source uses the offer ID, not a browser path or another picker', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 1 };
  panel.offerID = 'resolved-offer';
  panel.run = async (_, __, fn) => fn();
  const calls = [];
  panel.post = async (path, body) => {
    calls.push([path, body]);
    if (path === '/roots/pick-offer') return { selection_token: 'scoped-token' };
    if (path === '/roots/review')
      return { token: 'review', root_path: '/trusted', scope: 'metadata only' };
    return { root_id: 'new-root' };
  };
  panel.confirm = async () => calls.filter(([path]) => path === '/roots/commit').length === 0;
  panel.refresh = async () => {
    panel.state = { revision: 3, initialized: true };
  };
  await panel.addFolder(null);
  assert.deepEqual(
    calls.map(([path]) => path),
    ['/roots/pick-offer', '/roots/review', '/roots/commit']
  );
  assert.deepEqual(calls[0][1], { offer_id: 'resolved-offer' });
  assert.equal(panel.offerID, '');
});

test('declining the first review leaves neither a root nor a scan', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home', program: { is_station: true } });
  panel.state = { revision: 1 };
  const calls = [];
  panel.run = async (_, __, fn) => fn();
  panel.post = async path => {
    calls.push(path);
    return {
      selection_token: 'picker',
      token: 'review',
      root_path: '/chosen',
      scope: 'metadata only'
    };
  };
  panel.confirm = async () => false;
  await panel.addFolder(null);
  assert.deepEqual(calls, ['/roots/pick', '/roots/review']);
});

test('root paging never merges a page from a different library revision', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = {
    revision: 7,
    total_roots: 21,
    next_offset: 20,
    provider_read_only: false,
    roots: [{ id: 'first' }]
  };
  panel.run = async (_, __, fn) => fn();
  const calls = [];
  panel.renderRoots = () => calls.push('render');
  panel.refresh = async () => {
    calls.push('refresh');
    panel.state = { revision: 8, roots: [] };
  };
  panel.status = message => calls.push(message);
  panel.request = async path => {
    assert.equal(path, '/roots?offset=20');
    return {
      revision: 7,
      total_roots: 21,
      next_offset: 0,
      provider_read_only: false,
      roots: [{ id: 'last' }]
    };
  };
  await panel.moreRoots(null);
  assert.deepEqual(
    panel.state.roots.map(root => root.id),
    ['first', 'last']
  );
  assert.equal(panel.state.next_offset, 0);
  assert.deepEqual(calls, ['render']);
  panel.state.next_offset = 20;
  panel.request = async () => ({
    revision: 8,
    total_roots: 21,
    next_offset: 0,
    provider_read_only: false,
    roots: [{ id: 'unsafe' }]
  });
  await panel.moreRoots(null);
  assert.deepEqual(panel.state.roots, []);
  assert.equal(calls[1], 'refresh');
});

test('a saved narrower scope requires its own review and never sends a browser path', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { revision: 7 };
  const calls = [];
  panel.post = async (path, body) => {
    calls.push([path, body]);
    return {
      token: 'scope-review',
      relative_folder: 'Track/Extra',
      root_path: '/reviewed',
      scope: 'metadata only',
      max_entries: 5000
    };
  };
  panel.confirm = async () => false;
  panel.refresh = async () => {
    panel.state.revision++;
  };
  panel.status = () => {};
  await panel.scanRootFlow('root-1', null, 'server-scope-id');
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0], [
    '/roots/root-1/scans/review',
    { if_revision: 7, scope_id: 'server-scope-id' }
  ]);
  panel.confirm = async () => true;
  panel.post = async (path, body) => {
    calls.push([path, body]);
    if (path.endsWith('/review'))
      return {
        token: 'scope-review',
        relative_folder: 'Track/Extra',
        root_path: '/reviewed',
        scope: 'metadata only',
        max_entries: 5000
      };
    return { status: 'complete', entries_seen: 1, skipped_links: 0, skipped_other: 0 };
  };
  await panel.scanRootFlow('root-1', null, 'server-scope-id');
  assert.deepEqual(calls[1][1], { if_revision: 8, scope_id: 'server-scope-id' });
  assert.equal(calls[2][0], '/roots/root-1/scans/commit');
  assert.equal(calls[2][1].scope_id, 'server-scope-id');
  assert.equal(calls[2][1].confirm, true);
  assert.equal(Object.hasOwn(calls[2][1], 'relative_folder'), false);
});

test('manual edit review cannot commit without a second user confirmation', async () => {
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  const calls = [];
  panel.run = async (_, __, fn) => fn();
  panel.post = async path => {
    calls.push(path);
    return { token: 'review' };
  };
  panel.confirm = async () => false;
  await panel.saveFields(
    { row: { id: 'entry-1', name: 'Track', fields_revision: 0 } },
    { next_action: 'Review mix' },
    null
  );
  assert.deepEqual(calls, ['/projects/entry-1/fields/review']);
});
