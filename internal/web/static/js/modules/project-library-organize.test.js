import test from 'node:test';
import assert from 'node:assert/strict';
import { ProjectLibraryPanel, libraryQuery } from './project-library.js';
import { navigationIntent } from './project-library-browser.js';

function environment(t, values = {}) {
  const prior = globalThis.document;
  t.after(() => {
    globalThis.document = prior;
  });
  const elements = new Map(Object.entries(values));
  globalThis.document = {
    getElementById: id => elements.get(id) || null,
    querySelectorAll: () => []
  };
  const panel = new ProjectLibraryPanel({ workspaceId: 'home' });
  panel.browser.changed = () => {};
  panel.saveBrowserNavigation = () => {};
  const messages = [];
  panel.status = text => messages.push(text);
  return { panel, elements, messages };
}

test('collection/view intent is serialized without cached membership or revision authority', () => {
  const params = new URLSearchParams(
    libraryQuery({
      view: 'needs_attention',
      collectionID: 'collection-a',
      collectionRevision: 3,
      text: 'late'
    })
  );
  assert.equal(params.get('view'), 'needs_attention');
  assert.equal(params.get('collection_id'), 'collection-a');
  assert.equal(params.get('collection_revision'), '3');
  const intent = navigationIntent({
    query: {
      view: 'needs_attention',
      collectionID: 'collection-a',
      collectionRevision: 3,
      members: ['unseen']
    }
  });
  assert.equal(intent.query.view, 'needs_attention');
  assert.equal(intent.query.collectionID, 'collection-a');
  assert.equal(intent.query.collectionRevision, undefined);
  assert.equal(intent.query.members, undefined);
});

test('changing a filter clears bulk picks with feedback but does not select or mutate inspection', t => {
  const events = {};
  const { panel, elements } = environment(t, {
    projectLibrarySearch: {
      addEventListener: (name, fn) => {
        events[name] = fn;
      }
    },
    projectLibrarySelectionNotice: { textContent: '' }
  });
  panel.selectedProjects.add('picked');
  panel.selectedRows.set('picked', { id: 'picked' });
  panel.browser.selected = 'inspected';
  panel.renderRows = () => {};
  panel.renderQueueControls = () => {};
  panel.initBrowser();
  events.input();
  assert.equal(panel.selectedProjects.size, 0);
  assert.equal(panel.selectedRows.size, 0);
  assert.equal(panel.browser.selected, 'inspected');
  assert.match(elements.get('projectLibrarySelectionNotice').textContent, /Selection cleared/);
});

test('bulk refresh rechecks selected metadata and pages without restarting Files', async t => {
  const { panel } = environment(t, {
    projectLibraryBrowser: {},
    projectLibraryListScroll: { scrollTop: 40 }
  });
  panel.browser.selected = 'entry-a';
  panel.browser.tab = 'files';
  panel.browser.status = 'ready';
  panel.loadedPages = 2;
  const pages = [],
    reads = [];
  panel.search = async more => {
    pages.push(more);
    panel.rows = [{ id: 'entry-a' }];
    panel.cursor = more ? '' : 'fresh';
    return true;
  };
  panel.request = async path => {
    reads.push(path);
    return { row: { id: 'entry-a' }, fields: { stage: 'mixing' } };
  };
  panel.renderBrowser = () => {
    throw new Error('must not refresh Files implicitly');
  };
  await panel.refreshBrowserMetadata();
  assert.deepEqual(pages, [false, true]);
  assert.deepEqual(reads, ['/projects/entry-a']);
  assert.equal(panel.browser.tab, 'files');
  assert.equal(panel.browser.detail.fields.stage, 'mixing');
  panel.search = async () => {
    panel.rows = [];
    panel.cursor = '';
    return true;
  };
  await panel.refreshBrowserMetadata();
  assert.equal(panel.browser.selected, '');
});

test('stale collection paging clears stale scope, never silently falls back to All', async t => {
  const { panel, elements, messages } = environment(t, {
    projectLibraryCollection: { value: 'collection-a' },
    projectLibraryRows: { replaceChildren() {} },
    projectLibraryMore: { disabled: false, hidden: false },
    projectLibraryCount: { textContent: '' },
    projectLibrarySelectionNotice: { textContent: '' }
  });
  panel.collectionScope = { id: 'collection-a', revision: 4, name: 'Collection' };
  panel.rows = [{ id: 'old' }];
  panel.cursor = 'stale';
  panel.selectedProjects.add('old');
  panel.renderAppliedFilters = () => {};
  panel.renderQueueControls = () => {};
  const reads = [];
  panel.request = async path => {
    reads.push(path);
    throw new Error('stale');
  };
  assert.equal(await panel.search(true), false);
  assert.equal(panel.rows.length, 0);
  assert.equal(panel.selectedProjects.size, 0);
  assert.equal(elements.get('projectLibraryCollection').value, 'collection-a');
  assert.equal(reads.length, 1);
  assert.match(reads[0], /collection_id=collection-a/);
  assert.match(messages.at(-1), /Refresh membership/);
});
