// Tests for home-tree-sources.js — the loaders behind the Home file tree.
//
// `fetch` is stubbed, so these run under plain Node with no server:
//   node --test internal/web/static/js/modules/home-tree-sources.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SECTION_IDS,
  SECTION_ROW_LIMIT,
  SECTION_LOADING,
  SECTION_READY,
  SECTION_FAILED,
  itemKey,
  sectionKey,
  parseItemKey,
  sectionOfKind,
  notesToRows,
  ticketsToRows,
  filesToRows,
  memoryToRows,
  agentsToRows,
  countFiles,
  isHiddenPath,
  capSection,
  loadSection,
  loadSections,
  loadNote,
  saveNoteContent,
  createNote,
  createTicket,
  uploadFile,
  loadTicket,
  loadMemory,
  loadFilePreview,
  filePreviewKind,
  workspaceFileURL,
  openWorkspaceFile,
  ticketStateLabel,
  ticketSourceLabel,
  FILE_PREVIEW_LIMIT,
  responseErrorMessage
} from './home-tree-sources.js';

// A fetch stand-in: `routes` maps a URL to a JSON body, to a string for a file's
// raw text, to `{ status, body }` for a failure, or to a function for anything
// else. Every call is recorded; `impl.requests` also keeps the options.
function stubFetch(routes) {
  const calls = [];
  const requests = [];
  const impl = async (url, options) => {
    calls.push(url);
    requests.push({ url, options });
    const route = routes[url];
    if (route === undefined) {
      return { ok: false, status: 404, text: async () => '', json: async () => ({}) };
    }
    const value = typeof route === 'function' ? await route() : route;
    if (value && value.status && value.status >= 400) {
      const body = typeof value.body === 'string' ? value.body : JSON.stringify(value.body || '');
      return { ok: false, status: value.status, text: async () => body, json: async () => ({}) };
    }
    return {
      ok: true,
      status: 200,
      text: async () => (typeof value === 'string' ? value : JSON.stringify(value)),
      json: async () => value
    };
  };
  impl.calls = calls;
  impl.requests = requests;
  return impl;
}

const COMMON_KEYS = ['children', 'id', 'kind', 'label', 'meta', 'workspaceId'];

// ---------------------------------------------------------------------------
// Row keys
// ---------------------------------------------------------------------------

test('a row key starts with its workspace id and round-trips through parseItemKey', () => {
  assert.equal(itemKey('ws1', 'note', 'n9'), 'ws1/n/n9');
  assert.equal(sectionKey('ws1', 'notes'), 'ws1/s/notes');
  assert.equal(itemKey('ws1', 'memory'), 'ws1/m');
  assert.deepEqual(parseItemKey('ws1/n/n9'), { workspaceId: 'ws1', kind: 'note', itemId: 'n9' });
  assert.deepEqual(parseItemKey('ws1/m'), { workspaceId: 'ws1', kind: 'memory', itemId: '' });
  // A file path keeps its own slashes.
  assert.deepEqual(parseItemKey(itemKey('ws1', 'file', 'docs/2026/plan.md')), {
    workspaceId: 'ws1',
    kind: 'file',
    itemId: 'docs/2026/plan.md'
  });
  // A key with no slash is the workspace or group row itself.
  assert.deepEqual(parseItemKey('ws1'), { workspaceId: 'ws1', kind: 'workspace', itemId: '' });
});

test('parseItemKey rejects what this module never produced', () => {
  assert.equal(parseItemKey(''), null);
  assert.equal(parseItemKey('ws1/zz/thing'), null);
  assert.equal(parseItemKey('/n/n9'), null);
  assert.throws(() => itemKey('ws1', 'spaceship', 'x'), /unknown row kind/);
});

test('sectionOfKind says which section holds a row', () => {
  assert.equal(sectionOfKind('note'), 'notes');
  assert.equal(sectionOfKind('ticket'), 'backlog');
  assert.equal(sectionOfKind('file'), 'files');
  assert.equal(sectionOfKind('folder'), 'files');
  assert.equal(sectionOfKind('memory'), 'memory');
  assert.equal(sectionOfKind('agent'), 'agents');
  assert.equal(sectionOfKind('workspace'), '');
});

// ---------------------------------------------------------------------------
// Shaping
// ---------------------------------------------------------------------------

test('every loader returns rows of the one common shape', () => {
  const samples = [
    notesToRows('ws1', { notes: [{ id: 'n1', name: 'Plan' }] }).rows,
    ticketsToRows('ws1', { tickets: [{ id: 't1', title: 'Ship', state: 'ready' }] }).rows,
    filesToRows('ws1', { files: [{ relative_path: 'docs/a.md', name: 'a.md' }] }).rows,
    agentsToRows('ws1', { agents: [{ name: 'Scout', role: 'researcher', model: 'm' }] }).rows
  ];
  samples.forEach(rows => {
    assert.ok(rows.length > 0);
    rows.forEach(entry => {
      assert.deepEqual(Object.keys(entry).sort(), COMMON_KEYS);
      assert.equal(entry.workspaceId, 'ws1');
      assert.ok(Array.isArray(entry.children));
    });
  });
});

test('notes are sorted by name, ignoring case, with numbers in order (FR9)', () => {
  const { rows, count } = notesToRows('ws1', {
    notes: [
      { id: 'c', name: 'weekly 10' },
      { id: 'a', name: 'Weekly 2' },
      { id: 'b', name: 'arrangement' },
      { id: 'd', name: '' }
    ]
  });
  assert.deepEqual(
    rows.map(r => r.label),
    ['arrangement', 'Untitled', 'Weekly 2', 'weekly 10']
  );
  assert.equal(count, 4);
  assert.equal(rows[0].id, 'ws1/n/b');
  assert.equal(rows[0].meta.noteId, 'b');
});

test('tickets keep the API order and carry their state; finished ones are marked (FR11)', () => {
  const { rows, count } = ticketsToRows('ws1', {
    tickets: [
      { id: 't2', title: 'Zebra', state: 'in_progress', state_label: 'In progress' },
      { id: 't1', title: 'Apple', state: 'done' },
      { id: 't3', title: 'Mango', state: 'cancelled' },
      { id: 't4', title: '', state: 'backlog' }
    ],
    total: 4
  });
  assert.deepEqual(
    rows.map(r => r.label),
    ['Zebra', 'Apple', 'Mango', 'Untitled ticket']
  );
  assert.deepEqual(
    rows.map(r => r.meta.stateLabel),
    ['In progress', 'Done', 'Cancelled', 'Backlog']
  );
  assert.deepEqual(
    rows.map(r => r.meta.finished),
    [false, true, true, false]
  );
  assert.equal(count, 4);
});

test('a ticket total above the rows returned is kept as the count', () => {
  const { rows, count } = ticketsToRows('ws1', {
    tickets: [{ id: 't1', title: 'One', state: 'ready' }],
    total: 240
  });
  assert.equal(rows.length, 1);
  assert.equal(count, 240);
});

test('the flat files list becomes nested folders, folders first, each level by name (FR12)', () => {
  const { rows } = filesToRows('ws1', {
    files: [
      { relative_path: 'zeta.txt', name: 'zeta.txt', url: '/u/zeta', size: 3 },
      { relative_path: 'BACKLOG.md', name: 'BACKLOG.md' },
      { relative_path: 'docs', name: 'docs', is_dir: true },
      { relative_path: 'docs/plan.md', name: 'plan.md' },
      { relative_path: 'docs/2026/q1.md', name: 'q1.md' },
      { relative_path: 'docs/2026/q2.md', name: 'q2.md' },
      { relative_path: 'art', name: 'art', is_dir: true }
    ]
  });
  assert.deepEqual(
    rows.map(r => `${r.kind}:${r.label}`),
    ['folder:art', 'folder:docs', 'file:BACKLOG.md', 'file:zeta.txt']
  );
  const docs = rows[1];
  assert.deepEqual(
    docs.children.map(r => `${r.kind}:${r.label}`),
    ['folder:2026', 'file:plan.md']
  );
  assert.deepEqual(
    docs.children[0].children.map(r => r.label),
    ['q1.md', 'q2.md']
  );
  assert.equal(docs.children[0].id, 'ws1/d/docs/2026');
  assert.equal(docs.children[0].children[0].id, 'ws1/f/docs/2026/q1.md');
  assert.equal(rows[3].meta.url, '/u/zeta');
  assert.equal(rows[3].meta.size, 3);
});

test('files are counted inside sub-folders, and empty folders count for nothing (FR10)', () => {
  const { rows, count } = filesToRows('ws1', {
    files: [
      { relative_path: 'a.txt' },
      { relative_path: 'docs/b.txt' },
      { relative_path: 'docs/deep/c.txt' },
      { relative_path: 'empty', is_dir: true }
    ]
  });
  assert.equal(count, 3);
  assert.equal(countFiles(rows), 3);
  const docs = rows.find(r => r.label === 'docs');
  assert.equal(docs.meta.fileCount, 2);
  assert.equal(rows.find(r => r.label === 'empty').meta.fileCount, 0);
});

test('a folder that is only implied by a file path is still created', () => {
  const { rows } = filesToRows('ws1', { files: [{ relative_path: 'stems/vocals/lead.wav' }] });
  assert.equal(rows.length, 1);
  assert.equal(rows[0].kind, 'folder');
  assert.equal(rows[0].children[0].kind, 'folder');
  assert.equal(rows[0].children[0].children[0].label, 'lead.wav');
});

test('hidden and internal files never become rows (FR15)', () => {
  ['.ori/state.json', '.DS_Store', 'docs/.hidden', 'workspace.json', 'notes/x.lock', ''].forEach(
    path => assert.equal(isHiddenPath(path), true, path)
  );
  ['BACKLOG.md', 'docs/plan.md', 'package-lock.json', 'my.workspace.json.md'].forEach(path =>
    assert.equal(isHiddenPath(path), false, path)
  );
  const { rows, count } = filesToRows('ws1', {
    files: [
      { relative_path: '.ori', is_dir: true },
      { relative_path: '.ori/index.json' },
      { relative_path: 'workspace.json' },
      { relative_path: 'keep.md' }
    ]
  });
  assert.deepEqual(
    rows.map(r => r.label),
    ['keep.md']
  );
  assert.equal(count, 1);
});

test('memory has no rows; its entries feed the pane and its count the badge', () => {
  const shaped = memoryToRows('ws1', {
    entries: [{ index: 0, type: 'decision', date: '2026-10-01', text: 'Ship on Fridays.' }],
    managed_learnings: [{ id: 'l1', type: 'preference', text: 'Keep replies short.' }],
    unstructured: ['# Workspace Memory', 'A loose line.']
  });
  assert.deepEqual(shaped.rows, []);
  // Entries, then approved learnings. The file's other lines — its heading,
  // loose prose — are not entries, as on the workspace's own Memory tab.
  assert.equal(shaped.count, 2);
  assert.deepEqual(shaped.entries, [
    { text: 'Ship on Fridays.', type: 'decision', date: '2026-10-01' },
    { text: 'Keep replies short.', type: 'preference', date: '' }
  ]);
  assert.equal(memoryToRows('ws1', {}).count, 0);
  // A workspace whose MEMORY.md holds only its heading has no memory yet.
  assert.equal(memoryToRows('ws1', { entries: [], unstructured: ['# Workspace Memory'] }).count, 0);
});

test('ticket states use the PRD wording; an unknown state falls back to the server label', () => {
  assert.equal(ticketStateLabel('in_progress', 'In Progress'), 'In progress');
  assert.equal(ticketStateLabel('review', ''), 'Review');
  assert.equal(ticketStateLabel('triage', 'Needs triage'), 'Needs triage');
  assert.equal(ticketStateLabel('triage', ''), 'triage');
  assert.equal(ticketStateLabel('', ''), 'Unknown');
});

test('ticket sources are described in words; an unknown source is shown as it is', () => {
  assert.equal(ticketSourceLabel('manual'), 'Added by you');
  assert.equal(ticketSourceLabel('assistant'), 'Suggested by the assistant');
  assert.equal(ticketSourceLabel('home_quick_capture'), 'Quick capture on Home');
  assert.equal(ticketSourceLabel('some_new_source'), 'some_new_source');
  assert.equal(ticketSourceLabel(''), 'Unknown');
});

test('agents keep the API order and carry role and model', () => {
  const { rows, count } = agentsToRows('ws1', {
    agents: [
      { name: 'Zed', role: 'orchestrator', model: 'gpt-5-nano', provider: 'openai' },
      { name: 'Amy', role: 'researcher', model: 'claude-sonnet-5-5' },
      { name: '' }
    ]
  });
  assert.deepEqual(
    rows.map(r => r.label),
    ['Zed', 'Amy']
  );
  assert.equal(count, 2);
  assert.equal(rows[0].id, 'ws1/a/Zed');
  assert.deepEqual(rows[0].meta, {
    name: 'Zed',
    role: 'orchestrator',
    model: 'gpt-5-nano',
    provider: 'openai'
  });
});

test('a malformed payload shapes to nothing rather than throwing', () => {
  [null, undefined, {}, { notes: 'nope' }].forEach(payload => {
    assert.deepEqual(notesToRows('ws1', payload), { rows: [], count: 0 });
    assert.deepEqual(agentsToRows('ws1', payload), { rows: [], count: 0 });
    assert.deepEqual(filesToRows('ws1', payload), { rows: [], count: 0 });
    assert.equal(ticketsToRows('ws1', payload).count, 0);
  });
});

// ---------------------------------------------------------------------------
// The 100-row cap (FR14)
// ---------------------------------------------------------------------------

function manyNotes(n) {
  return {
    notes: Array.from({ length: n }, (_, i) => ({
      id: `n${i}`,
      name: `Note ${String(i).padStart(3, '0')}`
    }))
  };
}

test('a section at the limit is left alone', () => {
  const capped = capSection('ws1', 'notes', notesToRows('ws1', manyNotes(SECTION_ROW_LIMIT)));
  assert.equal(capped.rows.length, SECTION_ROW_LIMIT);
  assert.equal(
    capped.rows.some(r => r.kind === 'more'),
    false
  );
});

test('a section over the limit shows the first 100 and an "Open workspace" row', () => {
  const capped = capSection('ws1', 'notes', notesToRows('ws1', manyNotes(137)));
  assert.equal(capped.rows.length, SECTION_ROW_LIMIT + 1);
  assert.equal(capped.count, 137);
  const last = capped.rows[capped.rows.length - 1];
  assert.equal(last.kind, 'more');
  assert.equal(last.label, 'Open workspace to see all 137');
  assert.equal(last.id, 'ws1/more/notes');
  assert.deepEqual(last.meta, { section: 'notes', total: 137 });
  assert.equal(capped.rows[0].label, 'Note 000');
  assert.equal(capped.rows[SECTION_ROW_LIMIT - 1].label, 'Note 099');
});

test('a list the server cut short also ends with the "Open workspace" row', () => {
  const capped = capSection(
    'ws1',
    'backlog',
    ticketsToRows('ws1', { tickets: [{ id: 't1', title: 'One', state: 'ready' }], total: 60 })
  );
  assert.equal(capped.rows.length, 2);
  assert.equal(capped.rows[1].label, 'Open workspace to see all 60');
});

test('the files cap counts files, not folders, and keeps the folders that lead to them', () => {
  const files = [];
  for (let i = 0; i < 80; i += 1) files.push({ relative_path: `a/f${String(i).padStart(3, '0')}` });
  for (let i = 0; i < 80; i += 1) files.push({ relative_path: `b/g${String(i).padStart(3, '0')}` });
  const capped = capSection('ws1', 'files', filesToRows('ws1', { files }));
  assert.equal(capped.count, 160);
  const [a, b, more] = capped.rows;
  assert.equal(a.children.length, 80);
  assert.equal(b.children.length, 20);
  assert.equal(countFiles([a, b]), SECTION_ROW_LIMIT);
  assert.equal(more.label, 'Open workspace to see all 160');
});

test('memory is never capped: it is one row, not a list', () => {
  const entries = Array.from({ length: 150 }, (_, i) => ({ text: `Entry ${i}` }));
  const capped = capSection('ws1', 'memory', memoryToRows('ws1', { entries }));
  assert.deepEqual(capped.rows, []);
  assert.equal(capped.count, 150);
});

// ---------------------------------------------------------------------------
// Loading (FR17, FR18, FR19)
// ---------------------------------------------------------------------------

const ROUTES = {
  '/api/workspaces/ws1/notes': { notes: [{ id: 'n1', name: 'Plan' }] },
  '/api/workspaces/ws1/tickets': { tickets: [{ id: 't1', title: 'Ship', state: 'ready' }] },
  '/api/workspaces/ws1/files/tree': { files: [{ relative_path: 'BACKLOG.md' }] },
  '/api/workspaces/ws1/memory': { entries: [] },
  '/api/workspaces/ws1/agents': { agents: [{ name: 'Scout' }] }
};

test('loadSection fetches one section and returns its rows', async () => {
  const fetchImpl = stubFetch(ROUTES);
  const notes = await loadSection('ws1', 'notes', { fetchImpl });
  assert.deepEqual(fetchImpl.calls, ['/api/workspaces/ws1/notes']);
  assert.equal(notes.rows[0].label, 'Plan');
  assert.equal(notes.count, 1);
});

test('loadSection encodes the workspace id and rejects an unknown section', async () => {
  const fetchImpl = stubFetch({ '/api/workspaces/a%2Fb/agents': { agents: [] } });
  await loadSection('a/b', 'agents', { fetchImpl });
  assert.deepEqual(fetchImpl.calls, ['/api/workspaces/a%2Fb/agents']);
  await assert.rejects(() => loadSection('ws1', 'outputs', { fetchImpl }), /unknown section/);
});

test('loadSections loads every section in parallel and reports each one twice', async () => {
  const fetchImpl = stubFetch(ROUTES);
  const seen = [];
  const states = await loadSections(
    { id: 'ws1' },
    { fetchImpl, onSection: (id, state) => seen.push(`${id}:${state.status}`) }
  );
  assert.deepEqual([...fetchImpl.calls].sort(), Object.keys(ROUTES).sort());
  // All five are reported as loading before any of them answers.
  assert.deepEqual(
    seen.slice(0, SECTION_IDS.length),
    SECTION_IDS.map(id => `${id}:${SECTION_LOADING}`)
  );
  assert.deepEqual(
    seen.slice(SECTION_IDS.length).sort(),
    SECTION_IDS.map(id => `${id}:${SECTION_READY}`).sort()
  );
  assert.deepEqual(Object.keys(states).sort(), [...SECTION_IDS].sort());
  assert.equal(states.notes.count, 1);
  assert.equal(states.memory.count, 0);
});

test('one failing section never blocks the others, and carries the server reason (FR19)', async () => {
  const fetchImpl = stubFetch({
    ...ROUTES,
    '/api/workspaces/ws1/tickets': { status: 500, body: { message: 'Backlog store is locked' } }
  });
  const states = await loadSections({ id: 'ws1' }, { fetchImpl });
  assert.equal(states.backlog.status, SECTION_FAILED);
  assert.equal(states.backlog.error, 'Backlog store is locked');
  assert.equal(states.backlog.count, null);
  ['notes', 'files', 'memory', 'agents'].forEach(id =>
    assert.equal(states[id].status, SECTION_READY, id)
  );
});

test('a slow section does not hold back a fast one', async () => {
  let release;
  const gate = new Promise(resolve => {
    release = resolve;
  });
  const fetchImpl = stubFetch({
    ...ROUTES,
    '/api/workspaces/ws1/notes': async () => {
      await gate;
      return { notes: [] };
    }
  });
  const ready = [];
  const done = loadSections(
    { id: 'ws1' },
    {
      fetchImpl,
      onSection: (id, state) => {
        if (state.status === SECTION_READY) ready.push(id);
      }
    }
  );
  // Let the four fast sections answer while Notes is still waiting.
  await new Promise(resolve => setTimeout(resolve, 5));
  assert.equal(ready.includes('notes'), false);
  assert.equal(ready.length, 4);
  release();
  await done;
  assert.equal(ready.includes('notes'), true);
});

test('a thrown fetch (network down) is a failed section, not a rejected load', async () => {
  const fetchImpl = async () => {
    throw new Error('Failed to fetch');
  };
  const states = await loadSections({ id: 'ws1' }, { fetchImpl });
  SECTION_IDS.forEach(id => {
    assert.equal(states[id].status, SECTION_FAILED);
    assert.equal(states[id].error, 'Failed to fetch');
  });
});

test('loadSections can load only the sections asked for (retry, reload after create)', async () => {
  const fetchImpl = stubFetch(ROUTES);
  const states = await loadSections({ id: 'ws1' }, { fetchImpl, sections: ['notes'] });
  assert.deepEqual(fetchImpl.calls, ['/api/workspaces/ws1/notes']);
  assert.deepEqual(Object.keys(states), ['notes']);
});

test('loadSections with no workspace fetches nothing', async () => {
  const fetchImpl = stubFetch(ROUTES);
  assert.deepEqual(await loadSections(null, { fetchImpl }), {});
  assert.deepEqual(fetchImpl.calls, []);
});

test('an over-100 section arrives already capped', async () => {
  const fetchImpl = stubFetch({ '/api/workspaces/ws1/notes': manyNotes(101) });
  const states = await loadSections({ id: 'ws1' }, { fetchImpl, sections: ['notes'] });
  assert.equal(states.notes.rows.length, SECTION_ROW_LIMIT + 1);
  assert.equal(states.notes.count, 101);
  assert.equal(states.notes.rows[SECTION_ROW_LIMIT].kind, 'more');
});

test('responseErrorMessage prefers message, then error, then the body, then the fallback', async () => {
  const res = body => ({ text: async () => body });
  assert.equal(
    await responseErrorMessage(res('{"message":"No such workspace"}'), 'x'),
    'No such workspace'
  );
  assert.equal(await responseErrorMessage(res('{"error":"Locked"}'), 'x'), 'Locked');
  assert.equal(await responseErrorMessage(res('plain reason'), 'x'), 'plain reason');
  assert.equal(await responseErrorMessage(res(''), 'HTTP 500'), 'HTTP 500');
  assert.equal(await responseErrorMessage(res('<html>'.repeat(60)), 'HTTP 502'), 'HTTP 502');
});

test('loadNote returns the whole note for the pane', async () => {
  const fetchImpl = stubFetch({
    '/api/notes/n1': {
      id: 'n1',
      workspace_id: 'ws1',
      name: 'Plan',
      content: '# Plan\n\nShip it.',
      tags: ['review', ' '],
      updated_at: '2026-10-04T10:00:00Z'
    }
  });
  assert.deepEqual(await loadNote('n1', { fetchImpl }), {
    id: 'n1',
    workspaceId: 'ws1',
    name: 'Plan',
    content: '# Plan\n\nShip it.',
    tags: ['review'],
    updatedAt: '2026-10-04T10:00:00Z'
  });
  await assert.rejects(() => loadNote('gone', { fetchImpl }), /HTTP 404/);
});

test('loadTicket returns what the ticket tab shows', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/tickets/t1': {
      id: 't1',
      display_number: '#7',
      title: 'Reply to the mastering engineer',
      state: 'in_progress',
      state_label: 'In Progress',
      description: '  Which mix to master?  ',
      tags: ['mix', ''],
      source: 'home_quick_capture'
    }
  });
  assert.deepEqual(await loadTicket('ws1', 't1', { fetchImpl }), {
    id: 't1',
    title: 'Reply to the mastering engineer',
    number: '#7',
    state: 'in_progress',
    stateLabel: 'In progress',
    finished: false,
    description: 'Which mix to master?',
    tags: ['mix'],
    source: 'home_quick_capture',
    sourceLabel: 'Quick capture on Home'
  });
  await assert.rejects(() => loadTicket('ws1', 'gone', { fetchImpl }), /HTTP 404/);
});

test('loadMemory returns the entries the Memory tab lists', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/memory': {
      entries: [{ text: 'Ship on Fridays.', type: 'decision', date: '2026-10-01' }],
      unstructured: ['# Workspace Memory']
    }
  });
  assert.deepEqual(await loadMemory('ws1', { fetchImpl }), {
    entries: [{ text: 'Ship on Fridays.', type: 'decision', date: '2026-10-01' }]
  });
});

// ---------------------------------------------------------------------------
// Files (FR34)
// ---------------------------------------------------------------------------

test('a file is previewed by what its extension says it is', () => {
  ['BACKLOG.md', 'docs/Plan.MARKDOWN', 'a/b/c.mdx'].forEach(path =>
    assert.equal(filePreviewKind(path), 'markdown', path)
  );
  ['tempo.csv', 'settings.json', 'notes.txt', 'run.log', 'a.yaml', 'main.go'].forEach(path =>
    assert.equal(filePreviewKind(path), 'text', path)
  );
  ['cover.png', 'photo.JPG', 'icon.svg', 'anim.gif'].forEach(path =>
    assert.equal(filePreviewKind(path), 'image', path)
  );
  ['Night Drive.rpp', 'lead-vocal.wav', 'archive.zip', 'README', '.gitignore', ''].forEach(path =>
    assert.equal(filePreviewKind(path), 'none', path)
  );
});

test('a file address encodes each segment, so odd names still resolve', () => {
  assert.equal(
    workspaceFileURL('ws1', 'briefs/2026-10-04.md'),
    '/api/workspaces/ws1/files/briefs/2026-10-04.md'
  );
  assert.equal(
    workspaceFileURL('ws1', 'my stems/take #2?.wav'),
    '/api/workspaces/ws1/files/my%20stems/take%20%232%3F.wav'
  );
});

test('a Markdown or text file is fetched as text for its preview', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/files/briefs/today.md': '# Daily brief\n',
    '/api/workspaces/ws1/files/tempo.csv': 'song,bpm\nNight Drive,92\n'
  });
  assert.deepEqual(await loadFilePreview('ws1', 'briefs/today.md', { fetchImpl }), {
    kind: 'markdown',
    url: '/api/workspaces/ws1/files/briefs/today.md',
    text: '# Daily brief\n',
    tooLarge: false
  });
  const csv = await loadFilePreview('ws1', 'tempo.csv', { size: 24, fetchImpl });
  assert.equal(csv.kind, 'text');
  assert.equal(csv.text, 'song,bpm\nNight Drive,92\n');
});

test('an image or an unknown file is never fetched: there is nothing to read as text', async () => {
  const fetchImpl = stubFetch({});
  assert.deepEqual(await loadFilePreview('ws1', 'cover.png', { fetchImpl }), {
    kind: 'image',
    url: '/api/workspaces/ws1/files/cover.png',
    text: '',
    tooLarge: false
  });
  assert.equal((await loadFilePreview('ws1', 'take.wav', { fetchImpl })).kind, 'none');
  assert.deepEqual(fetchImpl.calls, []);
});

test('a text file over the preview limit is not fetched', async () => {
  const fetchImpl = stubFetch({});
  const preview = await loadFilePreview('ws1', 'huge.log', {
    size: FILE_PREVIEW_LIMIT + 1,
    fetchImpl
  });
  assert.equal(preview.tooLarge, true);
  assert.equal(preview.text, '');
  assert.deepEqual(fetchImpl.calls, []);
});

test('a text file that turns out larger than its listing said is still not shown', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/files/grew.log': 'x'.repeat(FILE_PREVIEW_LIMIT + 10)
  });
  const preview = await loadFilePreview('ws1', 'grew.log', { size: 10, fetchImpl });
  assert.equal(preview.tooLarge, true);
  assert.equal(preview.text, '');
});

test('a file that cannot be read fails with the server reason', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/files/gone.md': { status: 404, body: { message: 'File not found' } }
  });
  await assert.rejects(() => loadFilePreview('ws1', 'gone.md', { fetchImpl }), /File not found/);
});

test('Open and Reveal post the path inside the workspace', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/files/open': { message: 'ok' },
    '/api/workspaces/ws1/files/reveal': { message: 'ok' }
  });
  await openWorkspaceFile('ws1', 'briefs/today.md', { fetchImpl });
  await openWorkspaceFile('ws1', 'briefs/today.md', { reveal: true, fetchImpl });
  assert.deepEqual(fetchImpl.calls, [
    '/api/workspaces/ws1/files/open',
    '/api/workspaces/ws1/files/reveal'
  ]);
  assert.equal(fetchImpl.requests[0].options.method, 'POST');
  assert.deepEqual(JSON.parse(fetchImpl.requests[0].options.body), {
    relative_path: 'briefs/today.md'
  });
});

test('a failed Open reports the server reason', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/files/open': {
      status: 500,
      body: { message: 'Failed to open file: desktop opening is unavailable' }
    }
  });
  await assert.rejects(
    () => openWorkspaceFile('ws1', 'a.md', { fetchImpl }),
    /desktop opening is unavailable/
  );
});

// ---------------------------------------------------------------------------
// Saving a note from the pane (FR42)
// ---------------------------------------------------------------------------

test('saving a note PUTs only its text, so the name and tags are left alone', async () => {
  const fetchImpl = stubFetch({
    '/api/notes/n1': { success: true, note: { id: 'n1', updated_at: '2026-10-04T11:00:00Z' } }
  });
  const result = await saveNoteContent('n1', '# Plan\n\nEdited.', { fetchImpl });
  assert.deepEqual(result, { updatedAt: '2026-10-04T11:00:00Z' });
  const { options } = fetchImpl.requests[0];
  assert.equal(options.method, 'PUT');
  assert.deepEqual(JSON.parse(options.body), { content: '# Plan\n\nEdited.' });
  assert.equal(options.keepalive, false);
});

test('a refused save rejects with the server reason, for the save line to show', async () => {
  const fetchImpl = stubFetch({
    '/api/notes/n1': { status: 500, body: { message: 'Failed to update note' } }
  });
  await assert.rejects(() => saveNoteContent('n1', 'x', { fetchImpl }), /Failed to update note/);
  await assert.rejects(() => saveNoteContent('gone', 'x', { fetchImpl }), /HTTP 404/);
});

test('a keepalive save is sent without waiting for an answer', async () => {
  const fetchImpl = stubFetch({});
  // No route: an awaited save would reject with HTTP 404. This one does not.
  assert.equal(await saveNoteContent('n1', 'last words', { keepalive: true, fetchImpl }), null);
  assert.equal(fetchImpl.requests[0].options.keepalive, true);
  assert.deepEqual(JSON.parse(fetchImpl.requests[0].options.body), { content: 'last words' });
});

// ---------------------------------------------------------------------------
// Creating from the tree (FR47-FR49, FR51)
// ---------------------------------------------------------------------------

test('a new note is created with its name and comes back as a tree row', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/notes': { success: true, note: { id: 'n9', name: 'Mix notes' } }
  });
  const created = await createNote('ws1', '  Mix notes  ', { fetchImpl });
  assert.deepEqual(created, {
    id: 'ws1/n/n9',
    kind: 'note',
    label: 'Mix notes',
    meta: { noteId: 'n9', updatedAt: '' },
    children: [],
    workspaceId: 'ws1'
  });
  const { options } = fetchImpl.requests[0];
  assert.equal(options.method, 'POST');
  assert.deepEqual(JSON.parse(options.body), { name: 'Mix notes', content: '' });
});

test('a note created with an empty name is called "Untitled" (FR47)', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/notes': { success: true, note: { id: 'n9', name: 'Untitled' } }
  });
  await createNote('ws1', '   ', { fetchImpl });
  assert.equal(JSON.parse(fetchImpl.requests[0].options.body).name, 'Untitled');
});

test('a new ticket starts in the backlog with the manual source (FR48, D13)', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/tickets': {
      id: 't9',
      title: 'Book studio time',
      state: 'backlog',
      state_label: 'Backlog',
      display_number: '#9'
    }
  });
  const created = await createTicket('ws1', 'Book studio time', { fetchImpl });
  assert.equal(created.id, 'ws1/t/t9');
  assert.equal(created.label, 'Book studio time');
  assert.deepEqual(created.meta, {
    ticketId: 't9',
    state: 'backlog',
    stateLabel: 'Backlog',
    finished: false,
    number: '#9'
  });
  assert.deepEqual(JSON.parse(fetchImpl.requests[0].options.body), {
    title: 'Book studio time',
    state: 'backlog',
    source: 'manual'
  });
});

test('an upload sends the file and its folder, and returns the row for the stored name', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/files': {
      attachment: {
        file_meta: {
          name: 'take.wav',
          size: 68,
          url: '/api/workspaces/ws1/files/stems/9d8ce3d6_take.wav',
          relative_path: 'stems/9d8ce3d6_take.wav'
        }
      }
    }
  });
  const file = new Blob(['RIFF'], { type: 'audio/wav' });
  const created = await uploadFile('ws1', file, '/stems/', { fetchImpl });
  // The server stores an upload under a prefixed name; the tree must show
  // (and open) that one, not the name that was picked.
  assert.equal(created.id, 'ws1/f/stems/9d8ce3d6_take.wav');
  assert.equal(created.label, '9d8ce3d6_take.wav');
  assert.deepEqual(created.meta, {
    path: 'stems/9d8ce3d6_take.wav',
    url: '/api/workspaces/ws1/files/stems/9d8ce3d6_take.wav',
    size: 68
  });
  const { options } = fetchImpl.requests[0];
  assert.equal(options.method, 'POST');
  assert.ok(options.body instanceof FormData);
  assert.ok(options.body.get('file') instanceof Blob);
  assert.equal(options.body.get('folder_path'), 'stems');
  // No Content-Type header: the browser must set the multipart boundary itself.
  assert.equal(options.headers, undefined);
});

test('an upload to the top of files/ sends no folder at all', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/files': {
      attachment: { file_meta: { relative_path: 'ab12cd34_a.txt', size: 1 } }
    }
  });
  await uploadFile('ws1', new Blob(['x']), '', { fetchImpl });
  assert.equal(fetchImpl.requests[0].options.body.has('folder_path'), false);
});

test('a refused create or upload rejects with the server reason, for the tree to show (FR51)', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/notes': { status: 404, body: { message: 'Workspace not found' } },
    '/api/workspaces/ws1/tickets': {
      status: 400,
      body: { code: 'validation_error', message: 'title is required' }
    },
    '/api/workspaces/ws1/files': {
      status: 400,
      body: { message: 'File too large. Maximum size is 100 MB' }
    }
  });
  await assert.rejects(() => createNote('ws1', 'x', { fetchImpl }), /Workspace not found/);
  await assert.rejects(() => createTicket('ws1', '', { fetchImpl }), /title is required/);
  await assert.rejects(
    () => uploadFile('ws1', new Blob(['x']), '', { fetchImpl }),
    /File too large\. Maximum size is 100 MB/
  );
});

test('an answer without the new item is treated as a failure, not as an empty row', async () => {
  const fetchImpl = stubFetch({
    '/api/workspaces/ws1/notes': { success: true },
    '/api/workspaces/ws1/tickets': {},
    '/api/workspaces/ws1/files': { attachment: {} }
  });
  await assert.rejects(() => createNote('ws1', 'x', { fetchImpl }), /did not return the new note/);
  await assert.rejects(
    () => createTicket('ws1', 'x', { fetchImpl }),
    /did not return the new ticket/
  );
  await assert.rejects(
    () => uploadFile('ws1', new Blob(['x']), '', { fetchImpl }),
    /did not return the uploaded file/
  );
});
