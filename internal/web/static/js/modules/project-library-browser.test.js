import test from 'node:test';
import assert from 'node:assert/strict';
import {
  LibraryBrowserState,
  ReadEpoch,
  navigationIntent,
  readNavigation,
  saveNavigation,
  rowException,
  savedMetadataTime
} from './project-library-browser.js';

import { ProjectLibraryPanel } from './project-library.js';

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
const record = id => ({ row: { id, name: id } });

test('narrow inspection scrolls its focused heading below the navbar and honors an explicit tab', async t => {
  const previousDocument = globalThis.document;
  const previousMatchMedia = globalThis.matchMedia;
  t.after(() => {
    globalThis.document = previousDocument;
    globalThis.matchMedia = previousMatchMedia;
  });
  const calls = [];
  const header = { scrollIntoView: options => calls.push(['scroll-header', options]) };
  const heading = {
    focus: options => calls.push(['focus', options]),
    closest: selector => {
      assert.equal(selector, '.project-library-pane-head');
      return header;
    }
  };
  globalThis.document = {
    getElementById: id => (id === 'projectLibraryPaneTitle' ? heading : null)
  };
  globalThis.matchMedia = () => ({ matches: true });
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.browser.select = async (id, tab) => calls.push(['select', id, tab]);
  await panel.details('entry', null, 'sessions');
  assert.deepEqual(calls, [
    ['select', 'entry', 'sessions'],
    ['focus', { preventScroll: true }],
    ['scroll-header', { block: 'start', behavior: 'instant' }]
  ]);
});

test('rapid selection cancels A; a late A cannot overwrite B or invoke mutations', async () => {
  const pending = [],
    changes = [];
  const state = new LibraryBrowserState({
    home: 'home',
    request: (path, options) => {
      const read = deferred();
      pending.push({ ...read, path, options });
      return read.promise;
    },
    changed: state => changes.push([state.selected, state.status])
  });
  const a = state.select('a');
  const b = state.select('b');
  assert.equal(state.selected, 'b');
  assert.equal(state.status, 'loading', 'feedback is synchronous, before any response');
  assert.ok(pending[0].options.signal.aborted);
  pending[1].resolve(record('b'));
  await b;
  pending[0].resolve(record('a'));
  await a;
  assert.equal(state.detail.row.id, 'b');
  assert.deepEqual(
    pending.map(read => read.path),
    ['/projects/a', '/projects/b']
  );
  assert.deepEqual(changes.at(-1), ['b', 'ready']);
});

test('failed detail clears old actions; wrong or removed entry never becomes ready', async () => {
  const state = new LibraryBrowserState({ home: 'home', request: async () => record('a') });
  await state.select('a');
  await state.select('b');
  assert.equal(state.status, 'error');
  assert.equal(state.detail, null);
  state.request = async () => {
    throw new Error('Removed');
  };
  await state.select('b');
  assert.equal(state.error, 'Removed');
  state.clear();
  assert.equal(state.selected, '');
});

test('Home switch and unmount invalidate all detail and tab work', async () => {
  const wait = deferred();
  const state = new LibraryBrowserState({ home: 'one', request: () => wait.promise });
  const done = state.select('a', 'files');
  const tab = state.tabRead.begin();
  state.setHome('two');
  assert.equal(tab.current(), false);
  wait.resolve(record('a'));
  await done;
  assert.equal(state.selected, '');
  assert.equal(state.detail, null);
  assert.equal(state.tab, 'overview');
  let calls = 0;
  state.request = async () => {
    calls++;
    return record('b');
  };
  state.destroy();
  await state.select('b');
  assert.equal(calls, 0);
});

test('changing tabs cancels prior work without starting implicit reads', async () => {
  let calls = 0;
  const state = new LibraryBrowserState({
    home: 'home',
    request: async () => {
      calls++;
      return record('a');
    }
  });
  await state.select('a');
  const read = state.tabRead.begin();
  state.changeTab('sessions');
  assert.equal(read.current(), false);
  assert.equal(calls, 1);
  state.changeTab('invented');
  assert.equal(state.tab, 'sessions');
});

test('list epochs also drop a stale error after a fresh result', () => {
  const read = new ReadEpoch();
  const first = read.begin(),
    second = read.begin();
  assert.equal(first.current(), false);
  assert.equal(second.current(), true);
  read.cancel();
  assert.equal(second.current(), false);
});

test('navigation allowlist is bounded and drops content, paths, tokens and unknown filters', () => {
  const intent = navigationIntent({
    pages: 200,
    selected: '/private/source',
    tab: 'evil',
    query: {
      text: '/private/project',
      rootID: '/private/root',
      sort: 'bogus',
      stage: 'writing',
      token: 'secret'
    },
    detail: { contents: 'private' },
    cursor: 'old',
    token: 'secret',
    scroll: -1
  });
  assert.deepEqual(intent, {
    version: 1,
    query: { stage: 'writing', sort: 'name', direction: 'asc' },
    pages: 20,
    selected: '',
    tab: 'overview',
    scroll: 0
  });
  assert.equal(navigationIntent({ query: { text: '界'.repeat(41) } }).query.text, undefined);
});

test('storage is per-user/Home intent only and tolerates malformed or blocked storage', () => {
  const values = new Map();
  const storage = {
    getItem: key => values.get(key),
    setItem: (key, value) => values.set(key, value)
  };
  saveNavigation(storage, 'local', 'home', {
    selected: 'entry',
    tab: 'sessions',
    pages: 4,
    query: { text: 'Zebra' }
  });
  assert.equal(readNavigation(storage, 'local', 'home').selected, 'entry');
  assert.equal(readNavigation(storage, 'other', 'home'), null);
  assert.equal(readNavigation(storage, 'local', 'other'), null);
  saveNavigation(storage, '', 'home', { selected: 'no' });
  assert.equal(values.size, 1);
  assert.equal(readNavigation({ getItem: () => '{' }, 'local', 'home'), null);
  assert.equal(readNavigation({ getItem: () => '{"version":2}' }, 'local', 'home'), null);
  assert.equal(
    readNavigation(
      {
        getItem: () => {
          throw new Error('blocked');
        }
      },
      'local',
      'home'
    ),
    null
  );
});

test('bulk checkboxes and inspection buttons have separate state and never invoke open', async t => {
  const prior = globalThis.document;
  t.after(() => {
    globalThis.document = prior;
  });
  const element = tag => ({
    tag,
    children: [],
    dataset: {},
    listeners: {},
    append(...children) {
      this.children.push(...children);
    },
    replaceChildren() {
      this.children = [];
    },
    setAttribute() {},
    addEventListener(event, fn) {
      this.listeners[event] = fn;
    }
  });
  const rows = element('tbody');
  globalThis.document = {
    createElement: element,
    getElementById: id => (id === 'projectLibraryRows' ? rows : null)
  };
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.state = { provider_read_only: false };
  panel.rows = [{ id: 'entry', name: 'Project', connection: 'connected', fields_revision: 1 }];
  panel.renderQueueControls = () => {};
  panel.openRow = () => {
    throw new Error('inspection must never open or activate');
  };
  panel.details = async id => {
    panel.browser.selected = id;
  };
  panel.renderRows();
  const row = rows.children[0];
  const checkbox = row.children[0].children[0];
  checkbox.checked = true;
  checkbox.listeners.change();
  assert.equal(panel.selectedProjects.has('entry'), true);
  assert.equal(panel.browser.selected, '');
  row.children[1].children[0].listeners.click();
  assert.equal(panel.browser.selected, 'entry');
  assert.equal(panel.selectedProjects.size, 1);
  checkbox.checked = false;
  checkbox.listeners.change();
  assert.equal(panel.selectedProjects.size, 0);
  assert.equal(panel.browser.selected, 'entry');
});

test('unset Go timestamps are unknown, not year-one activity', () => {
  assert.equal(savedMetadataTime('0001-01-01T00:00:00Z'), '');
  assert.equal(savedMetadataTime('invalid'), '');
  assert.equal(savedMetadataTime(undefined), '');
  assert.notEqual(savedMetadataTime('2026-10-01T12:00:00Z'), '');
});

test('fresh restoration replays loaded pages, not old cursors, and handles a removed selection', async t => {
  const prior = globalThis.document;
  t.after(() => {
    globalThis.document = prior;
  });
  const scroll = { scrollTop: 0 };
  globalThis.document = {
    getElementById: id => (id === 'projectLibraryListScroll' ? scroll : null)
  };
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.browser.changed = () => {};
  panel.saveBrowserNavigation = () => {};
  panel.pendingBrowserIntent = { pages: 3, selected: 'b', tab: 'sessions', scroll: 90, query: {} };
  const calls = [];
  panel.search = async more => {
    calls.push(more);
    panel.rows = [{ id: 'b' }];
    panel.cursor = 'fresh-page';
    return true;
  };
  panel.browser.request = async () => record('b');
  await panel.restoreBrowserResults();
  assert.deepEqual(calls, [false, true, true]);
  assert.equal(panel.browser.selected, 'b');
  assert.equal(panel.browser.tab, 'sessions');
  assert.equal(scroll.scrollTop, 90);
  panel.status = text => calls.push(text);
  panel.pendingBrowserIntent = { pages: 1, selected: 'removed', query: {} };
  await panel.restoreBrowserResults();
  assert.equal(panel.browser.selected, '');
  assert.match(calls.at(-1), /no longer in these results/);
});

test('a user search interrupts restoration before old selection or additional pages can return', async t => {
  const prior = globalThis.document;
  t.after(() => {
    globalThis.document = prior;
  });
  globalThis.document = { getElementById: () => null };
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.browser.changed = () => {};
  panel.pendingBrowserIntent = { pages: 4, selected: 'old', query: {} };
  const wait = deferred();
  let calls = 0;
  panel.search = () => {
    calls++;
    return wait.promise;
  };
  const done = panel.restoreBrowserResults();
  panel.restoreRead.cancel();
  wait.resolve();
  await done;
  assert.equal(panel.browser.selected, '');
  assert.equal(calls, 1);
});

test('a stale paging cursor keeps loaded rows and removes the misleading More action', async t => {
  const prior = globalThis.document;
  t.after(() => {
    globalThis.document = prior;
  });
  const more = { disabled: false, hidden: false };
  globalThis.document = { getElementById: id => (id === 'projectLibraryMore' ? more : null) };
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.rows = [{ id: 'kept' }];
  panel.cursor = 'stale';
  panel.renderAppliedFilters = () => {};
  const messages = [];
  panel.status = text => messages.push(text);
  panel.request = async () => {
    throw new Error('Library changed; refresh');
  };
  await panel.search(true);
  assert.deepEqual(panel.rows, [{ id: 'kept' }]);
  assert.equal(panel.cursor, '');
  assert.equal(more.hidden, true);
  assert.equal(more.disabled, false);
  assert.match(messages.at(-1), /Library changed/);
});

test('rows surface actionable saved exceptions, not routine connection or invented activity', () => {
  assert.equal(
    rowException({ connection: 'catalog_only', last_observed_availability: 'not_scanned' }),
    ''
  );
  assert.equal(
    rowException({ connection: 'connected', last_observed_availability: 'available' }),
    ''
  );
  assert.match(rowException({ connection: 'needs_review' }), /link needs review/);
  assert.match(rowException({ last_observed_availability: 'revoked_source' }), /consent ended/);
});
