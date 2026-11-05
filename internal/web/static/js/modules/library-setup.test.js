import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  SETUP_KINDS,
  bucketProject,
  createLibraryClient,
  loadSetupRows,
  runSetup,
  setupLabel,
  setupSubtitle,
  setupTotals
} from './library-setup.js';

const one = { state: 'review_available', project_files: ['Album-1.rpp'] };
const two = { state: 'file_choice_required', project_files: ['Album-3.rpp', 'Album-3 v2.rpp'] };

test('bucketProject puts every eligibility state in one of four buckets', () => {
  assert.equal(bucketProject({ id: 'a' }, one).kind, SETUP_KINDS.SETUP);
  assert.deepEqual(bucketProject({ id: 'a' }, one).files, ['Album-1.rpp']);
  assert.equal(bucketProject({ id: 'a' }, two).kind, SETUP_KINDS.FILE_CHOICE);
  assert.equal(
    bucketProject({ id: 'a' }, { state: 'review_available', project_files: ['x.rpp', 'y.rpp'] })
      .kind,
    SETUP_KINDS.FILE_CHOICE,
    'several files is a choice even when the server says review_available'
  );
  const connected = bucketProject({ id: 'a' }, { state: 'connected', workspace_id: 'ws-4' });
  assert.equal(connected.kind, SETUP_KINDS.CONNECTED);
  assert.equal(connected.workspaceId, 'ws-4');
  assert.equal(
    bucketProject({ id: 'a', connection: 'connected' }, { state: 'review_available' }).kind,
    SETUP_KINDS.CONNECTED,
    'the list row wins when it already says connected'
  );
  for (const state of [
    'project_provider_unavailable',
    'provider_ambiguous',
    'home_provider_unavailable',
    'unsupported_format',
    'folder_owned',
    'link_needs_review',
    'revoked_source',
    'unavailable',
    ''
  ]) {
    const bucket = bucketProject({ id: 'a' }, { state, reason: 'why', project_files: ['x'] });
    assert.equal(bucket.kind, SETUP_KINDS.DASHBOARD, state || 'empty');
    assert.equal(bucket.reason, 'why');
  }
  assert.equal(
    bucketProject({ id: 'a' }, { state: 'review_available', project_files: [] }).kind,
    SETUP_KINDS.DASHBOARD,
    'review_available with no file cannot be set up here'
  );
  assert.equal(bucketProject(null, null).kind, SETUP_KINDS.DASHBOARD);
});

test('labels never say "ready"', () => {
  for (const kind of Object.values(SETUP_KINDS)) assert.doesNotMatch(setupLabel(kind), /ready/i);
  assert.equal(setupLabel('setup'), 'to set up');
  assert.equal(setupLabel('connected'), 'on the map');
  assert.equal(setupLabel('bogus'), 'needs the dashboard');
  const source = readFileSync(new URL('./library-setup.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source.replace(/\/\/.*$/gm, ''), /'[^']*\bready\b[^']*'/i);
});

test('totals and subtitle leave zero parts out', () => {
  const rows = [
    { kind: 'setup' },
    { kind: 'setup' },
    { kind: 'file_choice' },
    { kind: 'connected' },
    { kind: 'dashboard' },
    { kind: 'nonsense' },
    null
  ];
  const totals = setupTotals(rows);
  assert.deepEqual(totals, { found: 5, setup: 2, file_choice: 1, connected: 1, dashboard: 1 });
  assert.equal(
    setupSubtitle(totals),
    '5 found by the last scan · 1 already on the map · 1 needs the dashboard'
  );
  assert.equal(
    setupSubtitle({ found: 3, dashboard: 2 }),
    '3 found by the last scan · 2 need the dashboard'
  );
  assert.equal(setupSubtitle(setupTotals([])), '0 found by the last scan');
});

function fakeClient(handlers) {
  const calls = [];
  return {
    calls,
    request: async path => {
      calls.push(['GET', path]);
      const handler = handlers[path];
      if (!handler) throw new Error(`unexpected GET ${path}`);
      return typeof handler === 'function' ? handler() : handler;
    },
    post: async (path, body) => {
      calls.push(['POST', path, body]);
      const handler = handlers[`POST ${path}`];
      if (!handler) throw new Error(`unexpected POST ${path}`);
      return typeof handler === 'function' ? handler(body) : handler;
    }
  };
}

test('loadSetupRows keeps list order, asks eligibility per row and never drops a row', async () => {
  const client = fakeClient({
    '/projects?sort=last_saved&direction=desc&page_size=25': {
      provider_read_only: false,
      rows: [
        { id: 'e1', name: 'Album-1' },
        { id: 'e4', name: 'Album-4', connection: 'connected' },
        { id: 'e3', name: 'Album-3' },
        { id: 'bad', name: 'Broken' },
        { id: '', name: 'no id' },
        null
      ]
    },
    '/projects/e1/activation': one,
    '/projects/e4/activation': { state: 'connected', workspace_id: 'ws-4' },
    '/projects/e3/activation': two,
    '/projects/bad/activation': () => {
      throw new Error('boom');
    }
  });
  const result = await loadSetupRows(client);
  assert.deepEqual(
    result.rows.map(row => [row.id, row.kind]),
    [
      ['e1', 'setup'],
      ['e4', 'connected'],
      ['e3', 'file_choice'],
      ['bad', 'dashboard']
    ]
  );
  assert.equal(result.rows[3].reason, 'boom');
  assert.equal(result.rows[1].workspaceId, 'ws-4');
  assert.equal(result.readOnly, false);
  assert.deepEqual(result.totals, {
    found: 4,
    setup: 1,
    file_choice: 1,
    connected: 1,
    dashboard: 1
  });
});

test('runSetup reviews and commits each item in order with the confirmed file', async () => {
  const progress = [];
  const client = fakeClient({
    '/projects/e1': { revision: 7, row: { name: 'Album-1', connection: 'new' } },
    '/projects/e1/activation': one,
    'POST /projects/e1/activation/review': body => ({
      token: 't1',
      project_file: body.project_file
    }),
    'POST /projects/e1/activation/commit': { workspace_id: 'ws-1' },
    '/projects/e3': { revision: 7, row: { name: 'Album-3', connection: 'new' } },
    '/projects/e3/activation': two,
    'POST /projects/e3/activation/review': body => ({
      token: 't3',
      project_file: body.project_file
    }),
    'POST /projects/e3/activation/commit': { workspace_id: 'ws-3' }
  });
  const result = await runSetup({
    client,
    items: [
      { id: 'e1', name: 'Album-1', file: 'Album-1.rpp' },
      { id: 'e3', name: 'Album-3', file: 'Album-3 v2.rpp' }
    ],
    onProgress: (item, status) => progress.push(`${item.id}:${status}`),
    operationKey: action => `key-${action}`
  });
  assert.equal(result.failed, null);
  assert.deepEqual(result.remaining, []);
  assert.deepEqual(
    result.done.map(item => [item.id, item.workspaceId]),
    [
      ['e1', 'ws-1'],
      ['e3', 'ws-3']
    ]
  );
  assert.deepEqual(progress, ['e1:working', 'e1:done', 'e3:working', 'e3:done']);
  const review = client.calls.find(call => call[1] === '/projects/e3/activation/review');
  assert.deepEqual(review[2], {
    workspace_name: 'Album-3',
    project_file: 'Album-3 v2.rpp',
    if_revision: 7
  });
  const commit = client.calls.find(call => call[1] === '/projects/e1/activation/commit');
  assert.deepEqual(commit[2], {
    review_token: 't1',
    idempotency_key: 'key-batch-activation',
    confirm: true
  });
});

test('runSetup without staffing only connects: no staffing, Open or role request', async () => {
  const client = fakeClient({
    '/projects/e1': { revision: 1, row: { name: 'Album-1', connection: 'new' } },
    '/projects/e1/activation': one,
    'POST /projects/e1/activation/review': { token: 't1', project_file: 'Album-1.rpp' },
    'POST /projects/e1/activation/commit': { workspace_id: 'ws-1' }
  });
  const result = await runSetup({
    client,
    items: [{ id: 'e1', name: 'Album-1', file: 'Album-1.rpp' }],
    operationKey: () => 'k'
  });
  assert.equal(result.failed, null);
  assert.deepEqual(
    client.calls.filter(call => call[0] === 'POST').map(call => call[1]),
    ['/projects/e1/activation/review', '/projects/e1/activation/commit']
  );
});

test('runSetup stops at the first failure, keeps earlier commits and names the rest', async () => {
  const client = fakeClient({
    '/projects/e1': { revision: 1, row: { name: 'Album-1', connection: 'new' } },
    '/projects/e1/activation': one,
    'POST /projects/e1/activation/review': { token: 't1', project_file: 'Album-1.rpp' },
    'POST /projects/e1/activation/commit': { workspace_id: 'ws-1' },
    '/projects/e2': { revision: 1, row: { name: 'Album-2', connection: 'new' } },
    // The file the person confirmed is no longer offered: not set up.
    '/projects/e2/activation': { state: 'review_available', project_files: ['Other.rpp'] },
    '/projects/e5': () => {
      throw new Error('never reached');
    }
  });
  const result = await runSetup({
    client,
    items: [
      { id: 'e1', name: 'Album-1', file: 'Album-1.rpp' },
      { id: 'e2', name: 'Album-2', file: 'Album-2.rpp' },
      { id: 'e5', name: 'Album-5', file: 'Album-5.rpp' }
    ],
    operationKey: () => 'k'
  });
  assert.deepEqual(
    result.done.map(item => item.id),
    ['e1']
  );
  assert.equal(result.failed.id, 'e2');
  assert.match(result.failed.message, /changed after you confirmed/);
  assert.deepEqual(
    result.remaining.map(item => item.id),
    ['e5']
  );
  assert.ok(!client.calls.some(call => call[1].startsWith('/projects/e5')), 'e5 was left alone');
  assert.ok(
    !client.calls.some(call => call[1] === '/projects/e2/activation/review'),
    'no review was requested for the changed project'
  );
});

test('runSetup skips an item that connected meanwhile and fails a review that names another file', async () => {
  const client = fakeClient({
    '/projects/e4': {
      revision: 1,
      row: { name: 'Album-4', connection: 'connected', workspace_id: 'ws-4' }
    },
    '/projects/e1': { revision: 1, row: { name: 'Album-1', connection: 'new' } },
    '/projects/e1/activation': one,
    'POST /projects/e1/activation/review': { token: 't1', project_file: 'Elsewhere.rpp' }
  });
  const result = await runSetup({
    client,
    items: [
      { id: 'e4', name: 'Album-4', file: 'Album-4.rpp' },
      { id: 'e1', name: 'Album-1', file: 'Album-1.rpp' }
    ],
    operationKey: () => 'k'
  });
  assert.deepEqual(
    result.done.map(item => [item.id, item.workspaceId]),
    [['e4', 'ws-4']]
  );
  assert.equal(result.failed.id, 'e1');
  assert.match(result.failed.message, /reviewed file differs/);
  assert.ok(!client.calls.some(call => call[1] === '/projects/e1/activation/commit'));
});

test('createLibraryClient targets the Home library and throws the server message', async () => {
  const seen = [];
  const client = createLibraryClient('home 1', async (url, init) => {
    seen.push([url, init.method || 'GET', init.headers['Content-Type'] || '']);
    if (url.endsWith('/projects/x')) {
      return {
        ok: false,
        status: 409,
        json: async () => ({ error: 'stale', reason: 'stale_review' })
      };
    }
    return { ok: true, json: async () => ({ fine: true }) };
  });
  assert.deepEqual(await client.post('/projects/y/activation/commit', { confirm: true }), {
    fine: true
  });
  await assert.rejects(client.request('/projects/x'), error => {
    assert.equal(error.message, 'stale');
    assert.equal(error.reason, 'stale_review');
    assert.equal(error.status, 409);
    return true;
  });
  assert.deepEqual(seen, [
    [
      '/api/workspaces/home%201/assistant-program/library/projects/y/activation/commit',
      'POST',
      'application/json'
    ],
    ['/api/workspaces/home%201/assistant-program/library/projects/x', 'GET', '']
  ]);
});
