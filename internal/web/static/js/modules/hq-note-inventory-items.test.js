import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  INVENTORY_SECTIONS,
  InventoryListings,
  NotePreview,
  PROJECT_CHECK_CONCURRENCY,
  combinedCount,
  folderItems,
  inventoryItem,
  itemArt,
  itemPlace,
  previewTarget,
  sectionCount,
  sectionItems
} from './hq-note-inventory-items.js';

const REF = 'a'.repeat(64);
const REF_B = 'b'.repeat(64);
const OWNED = {
  status: 'connected',
  source: { name: 'Demo Notes', origin: 'created_by_ori', kind: 'obsidian' },
  browse_ref: REF
};
const EXTERNAL = {
  status: 'connected',
  source: { name: 'Field Notebook', origin: 'connected', kind: 'obsidian' },
  browse_ref: REF
};
const PLAIN = {
  status: 'connected',
  source: { name: 'Plain Folder', origin: 'connected', kind: 'markdown' },
  browse_ref: REF
};

const section = key => INVENTORY_SECTIONS.find(entry => entry.key === key);
const files = (...names) => names.map(name => ({ name, folder: false }));
const dirs = (...names) => names.map(name => ({ name, folder: true }));
const tick = () => new Promise(resolve => setImmediate(resolve));

// A fake notebook: every request is "METHOD path", answered from a folder map.
function notebook(folders, { fail = {}, hold = null } = {}) {
  const log = [];
  const held = [];
  const request = async (path, options = {}) => {
    log.push(path);
    if (hold && hold(path)) {
      await new Promise((resolve, reject) => {
        held.push({ path, resolve, reject });
        options.signal?.addEventListener?.('abort', () => {
          const error = new Error('aborted');
          error.name = 'AbortError';
          reject(error);
        });
      });
    }
    if (path.startsWith('/browse?')) {
      const params = new URLSearchParams(path.slice('/browse?'.length));
      const key = params.getAll('part').join('/');
      if (fail[key]) {
        const error = new Error('refused');
        error.status = fail[key];
        throw error;
      }
      const entry = folders[key];
      if (!entry) {
        const error = new Error('gone');
        error.status = 404;
        throw error;
      }
      return { directory: Array.isArray(entry) ? { entries: entry } : entry };
    }
    throw new Error('unexpected ' + path);
  };
  return { request, log, held };
}

// ---- items -------------------------------------------------------------------

test('Inbox and Journal list notes without their layout README; Projects lists folders', () => {
  const inbox = sectionItems(section('inbox'), [
    ...files('README.md', 'readme.md', '20261126-1012-ask-about-the-roof-warranty.md'),
    ...files('call-the-plumber.md', 'picture.png'),
    ...dirs('attachments')
  ]);
  assert.deepEqual(
    inbox.map(item => item.name),
    ['call-the-plumber.md', '20261126-1012-ask-about-the-roof-warranty.md'],
    'README is not a note; folders and non-Markdown files are not slots'
  );
  const roof = inbox.find(item => item.name.startsWith('2026'));
  assert.equal(roof.title, 'Ask about the roof warranty');
  assert.equal(roof.detail, 'Inbox · 26 Nov · 10:12');
  assert.equal(roof.id, 'inbox:20261126-1012-ask-about-the-roof-warranty.md');

  const projects = sectionItems(section('projects'), [
    ...files('README.md'),
    ...dirs('House', 'Studio')
  ]);
  assert.deepEqual(
    projects.map(item => [item.title, item.check, item.detail]),
    [
      ['House', 'unchecked', 'Project folder'],
      ['Studio', 'unchecked', 'Project folder']
    ],
    'a folder is a project folder until its note is seen'
  );

  const journal = sectionItems(
    section('journal'),
    files('2026-11-24.md', '2026-11-25.md', 'README.md')
  );
  assert.deepEqual(
    journal.map(item => item.title),
    ['Wed 25 Nov', 'Tue 24 Nov'],
    'newest first'
  );
});

test('identity is kind + name, never the title: duplicates and long names stay distinct', () => {
  const a = inventoryItem('inbox', 'House.md', ['Inbox']);
  const b = inventoryItem('inbox', 'House.markdown', ['Inbox']);
  const c = inventoryItem('project', 'House', ['Projects']);
  assert.equal(a.title, b.title);
  assert.equal(a.title, c.title);
  assert.equal(new Set([a.id, b.id, c.id]).size, 3);
  const long = 'a'.repeat(240) + '.md';
  assert.equal(inventoryItem('inbox', long, ['Inbox']).name, long);
  assert.equal(
    inventoryItem('daily', 'not-a-date.md', ['Daily']).title,
    'Not-a-date'.toLowerCase()
  );
});

test('a connected folder lists exactly its own names', () => {
  const items = folderItems(['Field Log 2026'], [...dirs('Maps'), ...files('Week 47.md')]);
  assert.deepEqual(
    items.map(item => [item.kind, item.title, item.parts.join('/')]),
    [
      ['folder', 'Maps', 'Field Log 2026'],
      ['file', 'Week 47', 'Field Log 2026']
    ]
  );
});

test('counts are exact, lower bounds when the server stopped, or unknown', () => {
  const ready = (n, partial = false) => ({ status: 'ready', items: Array(n).fill({}), partial });
  assert.deepEqual(sectionCount(ready(0)), { text: '0', exact: true, known: true, n: 0 });
  assert.deepEqual(sectionCount(ready(200, true)), {
    text: '200+',
    exact: false,
    known: true,
    n: 200
  });
  assert.equal(sectionCount({ status: 'loading', items: [] }).known, false);
  assert.equal(sectionCount({ status: 'error', items: [] }).known, false);
  assert.equal(combinedCount([ready(0), ready(4), ready(2)]).text, '6');
  assert.equal(combinedCount([ready(0), ready(4, true), ready(2)]).text, '6+');
  assert.equal(
    combinedCount([ready(0), { status: 'error', items: [] }, ready(2)]).text,
    '2+',
    'a missing section makes the total a lower bound'
  );
  assert.equal(combinedCount([{ status: 'loading' }, { status: 'loading' }]).known, false);
});

test('art comes from the kind and a stable pick from a project’s name', () => {
  assert.equal(itemArt(inventoryItem('inbox', 'x.md', ['Inbox'])), 'letter');
  assert.equal(itemArt(inventoryItem('daily', '2026-11-25.md', ['Daily'])), 'calendar');
  assert.equal(itemArt(inventoryItem('folder', 'Maps', [])), 'folder');
  const house = inventoryItem('project', 'House', ['Projects']);
  assert.equal(itemArt(house), itemArt({ ...house }), 'the same project, the same picture');
  assert.equal(itemArt(null), 'page');
  assert.deepEqual(itemPlace(house, 'Demo Notes'), ['Demo Notes', 'Projects', 'House']);
});

// ---- the request budget ------------------------------------------------------

test('opening an Ori notebook lists three folders, whatever its size', async () => {
  const many = Array.from({ length: 500 }, (_, i) => ({ name: 'P' + i, folder: true }));
  const { request, log } = notebook({
    Inbox: files('README.md'),
    Projects: { entries: many.slice(0, 200), partial: true },
    Daily: files('2026-11-25.md')
  });
  const listings = new InventoryListings({ request });
  listings.start(OWNED, { owned: true });
  await tick();
  assert.equal(log.length, 3);
  assert.ok(log.every(path => path.startsWith('/browse?ref=' + REF)));
  assert.ok(log.some(path => path.endsWith('&part=Inbox')));
  assert.ok(log.some(path => path.endsWith('&part=Projects')));
  assert.ok(log.some(path => path.endsWith('&part=Daily')));
  assert.equal(listings.section('inbox').items.length, 0, 'Inbox empty');
  assert.equal(sectionCount(listings.section('projects')).text, '200+');
  assert.equal(listings.section('journal').items.length, 1);
});

test('only the projects on screen are checked, a few at a time, once each', async () => {
  const projects = Array.from({ length: 30 }, (_, i) => 'Project ' + String(i).padStart(2, '0'));
  const folders = { Inbox: [], Daily: [], Projects: dirs(...projects) };
  projects.forEach((name, i) => {
    folders['Projects/' + name] = i === 1 ? files('notes.md') : files('README.md');
  });
  let inFlight = 0;
  let peak = 0;
  const base = notebook(folders);
  const request = async (path, options) => {
    inFlight += 1;
    peak = Math.max(peak, inFlight);
    await tick();
    try {
      return await base.request(path, options);
    } finally {
      inFlight -= 1;
    }
  };
  const listings = new InventoryListings({ request });
  listings.start(OWNED, { owned: true });
  for (let i = 0; i < 6; i++) await tick();
  assert.ok(INVENTORY_SECTIONS.every(entry => listings.section(entry.key).status === 'ready'));
  assert.equal(base.log.length, 3);
  const visible = listings.section('projects').items.slice(0, 4);
  base.log.length = 0;
  peak = 0;
  listings.checkProjects(visible);
  listings.checkProjects(visible);
  for (let i = 0; i < 10; i++) await tick();
  assert.equal(base.log.length, 4, 'one look inside each visible project, never all 30');
  assert.ok(peak <= PROJECT_CHECK_CONCURRENCY, 'at most ' + PROJECT_CHECK_CONCURRENCY + ' at once');
  const checked = listings.section('projects').items.slice(0, 4);
  assert.deepEqual(
    checked.map(item => item.check),
    ['note', 'none', 'note', 'note']
  );
  assert.equal(checked[1].detail, 'No project note', 'no note is invented for a folder');
  assert.equal(listings.section('projects').items[4].check, 'unchecked');
});

test('one section that cannot be listed leaves the others usable', async () => {
  const { request } = notebook({ Inbox: files('a.md'), Projects: dirs('House') });
  const listings = new InventoryListings({ request });
  listings.start(OWNED, { owned: true });
  await tick();
  assert.equal(listings.section('journal').status, 'error');
  assert.match(listings.section('journal').error, /no Daily folder/);
  assert.equal(listings.section('inbox').items.length, 1);
  assert.equal(listings.section('projects').items.length, 1);
});

test('a changed connection is reported once and nothing from it is applied after clear', async () => {
  let stale = 0;
  const { request } = notebook({ Inbox: [], Projects: [], Daily: [] }, { fail: { Inbox: 409 } });
  const listings = new InventoryListings({ request, onStale: () => (stale += 1) });
  listings.start(OWNED, { owned: true });
  await tick();
  assert.equal(stale, 1);

  const slow = notebook({ Inbox: files('late.md'), Projects: [], Daily: [] }, { hold: () => true });
  const later = new InventoryListings({ request: slow.request });
  later.start(OWNED, { owned: true });
  later.clear();
  slow.held.forEach(entry => entry.resolve());
  await tick();
  assert.equal(later.section('inbox').status, 'idle', 'a cleared listing shows nothing');
});

test('a connected folder lists its root, then only folders a person opens', async () => {
  const { request, log } = notebook({
    '': [...dirs('Expeditions', 'Field Log 2026'), ...files('Welcome.md')],
    Expeditions: files('Canyon.md')
  });
  const listings = new InventoryListings({ request });
  listings.start(EXTERNAL, { owned: false });
  await tick();
  assert.deepEqual(log, ['/browse?ref=' + REF]);
  assert.deepEqual(
    listings.folder([]).items.map(item => item.title),
    ['Expeditions', 'Field Log 2026', 'Welcome']
  );
  listings.checkProjects(listings.folder([]).items);
  void listings.loadFolder(['Expeditions']);
  await tick();
  assert.equal(log.length, 2, 'nothing is checked or listed that was not opened');
  assert.deepEqual(
    listings.folder(['Expeditions']).items.map(item => item.title),
    ['Canyon']
  );
});

// ---- previews ----------------------------------------------------------------

test('only Ori’s own notes have a preview; a folder without a README has none', () => {
  const inbox = inventoryItem('inbox', 'x.md', ['Inbox']);
  assert.equal(previewTarget(OWNED, inbox).preview, '/preview/inbox/x.md?ref=' + REF);
  const house = { ...inventoryItem('project', 'House', ['Projects']), check: 'note' };
  assert.equal(previewTarget(OWNED, house).preview, '/preview/project/House?ref=' + REF);
  assert.equal(
    previewTarget(OWNED, { ...house, check: 'none' }).preview,
    '',
    'no README seen, nothing to read'
  );
  const day = inventoryItem('daily', '2026-11-25.md', ['Daily']);
  assert.equal(previewTarget(OWNED, day).preview, '/preview/daily/2026-11-25.md?ref=' + REF);
  const encoded = inventoryItem('inbox', 'a b#c.md', ['Inbox']);
  assert.equal(previewTarget(OWNED, encoded).preview, '/preview/inbox/a%20b%23c.md?ref=' + REF);

  const canyon = inventoryItem('file', 'Canyon.md', ['Expeditions']);
  const external = previewTarget(EXTERNAL, canyon);
  assert.equal(external.preview, '', 'a connected folder never previews');
  assert.equal(
    external.external,
    'obsidian://open?vault=Field%20Notebook&file=Expeditions%2FCanyon',
    'the vault-relative Obsidian handoff, never a machine path'
  );
  assert.deepEqual(previewTarget(PLAIN, canyon), { preview: '', external: '' });
  assert.deepEqual(previewTarget(OWNED, inventoryItem('folder', 'Maps', [])), {
    preview: '',
    external: ''
  });
});

test('fast A→B selection shows only B; closing mid-read shows nothing', async () => {
  const replies = new Map();
  const request = (path, options) =>
    new Promise((resolve, reject) => {
      replies.set(path, resolve);
      options?.signal?.addEventListener?.('abort', () => {
        const error = new Error('aborted');
        error.name = 'AbortError';
        reject(error);
      });
    });
  const preview = new NotePreview({ request });
  const a = inventoryItem('inbox', 'a.md', ['Inbox']);
  const b = inventoryItem('inbox', 'b.md', ['Inbox']);
  const first = preview.select(OWNED, a);
  assert.equal(preview.state.status, 'loading');
  const second = preview.select(OWNED, b);
  replies.get('/preview/inbox/a.md?ref=' + REF)({ preview: { title: 'A', body: 'from A' } });
  replies.get('/preview/inbox/b.md?ref=' + REF)({ preview: { title: 'B', body: 'from B' } });
  await Promise.all([first, second]);
  assert.equal(preview.state.item.id, b.id);
  assert.equal(preview.state.preview.body, 'from B');

  const third = preview.select(OWNED, a);
  preview.clear();
  replies.get('/preview/inbox/a.md?ref=' + REF)({ preview: { title: 'A', body: 'late' } });
  await third;
  assert.equal(preview.state, null);
});

test('a refused, moved or stale note says so; the key carries the connection', async () => {
  let stale = 0;
  const fail = status => async () => {
    const error = new Error(status === 403 ? 'That note is yours; open it in your editor.' : 'x');
    error.status = status;
    throw error;
  };
  const house = { ...inventoryItem('project', 'House', ['Projects']), check: 'note' };
  const preview = new NotePreview({ request: fail(403), onStale: () => (stale += 1) });
  await preview.select(OWNED, house);
  assert.equal(preview.state.status, 'yours');
  assert.match(preview.state.message, /yours/);
  assert.ok(preview.state.target.external.startsWith('obsidian://open?vault=Demo%20Notes'));
  preview.request = fail(404);
  await preview.select(OWNED, house);
  assert.equal(preview.state.status, 'missing');
  preview.request = fail(409);
  await preview.select(OWNED, house);
  assert.equal(stale, 1);
  assert.equal(preview.state.key, REF + '|project:House');
  preview.request = async () => ({ preview: { title: 'House', body: 'x' } });
  await preview.select({ ...OWNED, browse_ref: REF_B }, house);
  assert.equal(preview.state.key, REF_B + '|project:House');
});
