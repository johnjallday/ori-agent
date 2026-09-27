import test from 'node:test';
import assert from 'node:assert/strict';
import { ProjectLibraryPanel, libraryQuery } from './project-library.js';

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

test('libraryQuery includes only bounded search fields and cursor', () => {
  const params = new URLSearchParams(
    libraryQuery({
      text: '  Mix  ',
      stage: 'mixing',
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
  panel.refresh = async () => {};
  panel.status = () => {};
  await panel.scanRootFlow('root-1', null, 'server-scope-id');
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
