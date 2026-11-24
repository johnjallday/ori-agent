// Tests for home-folder-search.js — a linked folder's search state on its row.
//   node --test internal/web/static/js/modules/home-folder-search.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FOLDER_ACTION_CONNECT,
  searchOnlySources,
  startingPageChoice,
  FOLDER_ACTION_MAKE_SEARCHABLE,
  FOLDER_ACTION_REFRESH,
  FOLDER_ACTION_REMOVE,
  FOLDER_ACTION_SEARCH_IN,
  FOLDER_VERIFY,
  folderActions,
  indexOutcome,
  isIndexing,
  linkKey,
  FOLDER_INDEXING,
  FOLDER_NOT_SEARCHABLE,
  FOLDER_PARTIAL,
  FOLDER_SEARCHABLE,
  FOLDER_STATUS_UNKNOWN,
  FOLDER_UNAVAILABLE,
  SEARCH_STATUS_FAILED,
  SEARCH_STATUS_LOADING,
  SEARCH_STATUS_OFF,
  canSearchFolder,
  emptyFolderSearch,
  failedFolderSearch,
  filenameCount,
  folderSearchFromSources,
  folderSearchState,
  folderStatusLines,
  indexedAt,
  sameFolderSearch
} from './home-folder-search.js';
import { readSources } from './home-files-search.js';

// What GET /api/file-search/sources answers, as internal/filesearch writes it.
const apiSource = (id, state, extra = {}) => ({
  id,
  label: 'Album masters',
  generation: 1,
  state,
  count: 1284,
  mapped_at: 1795338840, // 2026-11-22T09:14:00Z
  ...extra
});
const connected = (workspaceId, link, name = 'Albums') => ({
  workspace: { id: workspaceId, link, name, slug: name.toLowerCase(), connected: true }
});
const linkedRow = (workspaceId, dirId, name = 'Album masters') => ({
  id: `${workspaceId}/linked/${dirId}`,
  kind: 'linked',
  name,
  workspaceId,
  meta: { dirId, path: `/Users/someone/${name}` }
});
const index = (...sources) => folderSearchFromSources(readSources(sources));
const UTC = { locale: 'en-GB', timeZone: 'UTC', now: Date.UTC(2026, 10, 24) };

test("every index state reads as one of the row's states", () => {
  const cases = [
    ['ready', FOLDER_SEARCHABLE, 'Searchable', 'on'],
    ['queued', FOLDER_INDEXING, 'Indexing…', 'busy'],
    ['mapping', FOLDER_INDEXING, 'Indexing…', 'busy'],
    ['partial', FOLDER_PARTIAL, 'Partly indexed', 'part'],
    ['interrupted', FOLDER_PARTIAL, 'Partly indexed', 'part'],
    ['unavailable', FOLDER_UNAVAILABLE, 'Folder unavailable', 'alert']
  ];
  cases.forEach(([serverState, key, label, tone]) => {
    const state = folderSearchState(
      linkedRow('ws1', 'dir1'),
      index(apiSource('s1', serverState, connected('ws1', 'dir1')))
    );
    assert.equal(state.key, key, serverState);
    assert.equal(state.label, label, serverState);
    assert.equal(state.tone, tone, serverState);
    assert.equal(state.source.id, 's1');
  });
});

test('a state the index has never sent is unknown, not searchable', () => {
  const state = folderSearchState(
    linkedRow('ws1', 'dir1'),
    index(apiSource('s1', 'something-new', connected('ws1', 'dir1')))
  );
  assert.equal(state.key, FOLDER_STATUS_UNKNOWN);
  assert.equal(canSearchFolder(state), false);
});

test('a linked folder with no source is not searchable', () => {
  const state = folderSearchState(linkedRow('ws1', 'dir1'), index());
  assert.equal(state.key, FOLDER_NOT_SEARCHABLE);
  assert.equal(state.label, 'Not searchable');
  assert.equal(state.source, null);
  assert.equal(canSearchFolder(state), false);
});

test('a source matches by workspace id AND link id, never by name', () => {
  // Same link id under another workspace, and the same name with another link.
  const all = index(
    apiSource('a', 'ready', connected('ws2', 'dir1')),
    apiSource('b', 'ready', connected('ws1', 'dir9'))
  );
  assert.equal(folderSearchState(linkedRow('ws1', 'dir1'), all).key, FOLDER_NOT_SEARCHABLE);
  assert.equal(folderSearchState(linkedRow('ws2', 'dir1'), all).source.id, 'a');
  assert.equal(folderSearchState(linkedRow('ws1', 'dir9'), all).source.id, 'b');
});

test('two workspaces linking the same folder: only the connected link is searchable', () => {
  // One folder is one source, connected to one link (filename_workspace_links
  // keeps `source` unique). The other workspace's link to the same folder has
  // its own id and is not that source's connection.
  const all = index(apiSource('s1', 'ready', connected('albums', 'link-a')));
  const first = folderSearchState(linkedRow('albums', 'link-a'), all);
  const second = folderSearchState(linkedRow('archive', 'link-b'), all);
  assert.equal(first.key, FOLDER_SEARCHABLE);
  assert.equal(second.key, FOLDER_NOT_SEARCHABLE);
});

test('a renamed workspace keeps its mark: the name in the source is not compared', () => {
  const all = index(apiSource('s1', 'ready', connected('ws1', 'dir1', 'Old name')));
  const state = folderSearchState(linkedRow('ws1', 'dir1', 'New folder name'), all);
  assert.equal(state.key, FOLDER_SEARCHABLE);
});

test('a source whose link no longer exists marks no row, and a search-only folder none', () => {
  const all = index(
    apiSource('gone', 'ready', connected('ws1', 'deleted-link')),
    apiSource('solo', 'ready')
  );
  assert.equal(all.byLink.size, 1);
  assert.equal(folderSearchState(linkedRow('ws1', 'dir1'), all).key, FOLDER_NOT_SEARCHABLE);
  assert.equal(folderSearchState(linkedRow('ws1', 'dir1'), all).source, null);
});

test('nothing is said away from the local Mac, or before the list answers', () => {
  assert.equal(emptyFolderSearch().status, SEARCH_STATUS_OFF);
  assert.equal(emptyFolderSearch({ local: true }).status, SEARCH_STATUS_LOADING);
  assert.equal(folderSearchState(linkedRow('ws1', 'dir1'), emptyFolderSearch()), null);
  assert.equal(
    folderSearchState(linkedRow('ws1', 'dir1'), emptyFolderSearch({ local: true })),
    null
  );
  assert.equal(folderSearchState(linkedRow('ws1', 'dir1'), undefined), null);
});

test('a list that could not be read is never "Not searchable"', () => {
  const failed = failedFolderSearch();
  assert.equal(failed.status, SEARCH_STATUS_FAILED);
  const state = folderSearchState(linkedRow('ws1', 'dir1'), failed);
  assert.equal(state.key, FOLDER_STATUS_UNKNOWN);
  assert.equal(state.label, 'Status unavailable');
  assert.notEqual(state.label, 'Not searchable');
  assert.equal(canSearchFolder(state), false);
  assert.equal(folderStatusLines(state).title, 'Search status unavailable');
});

test('only linked-folder rows have a search state', () => {
  const all = index(apiSource('s1', 'ready', connected('ws1', 'dir1')));
  ['workspace', 'group', 'folder', 'linkedFolder', 'linkedFile', 'section'].forEach(kind => {
    assert.equal(
      folderSearchState({ kind, workspaceId: 'ws1', meta: { dirId: 'dir1' } }, all),
      null,
      kind
    );
  });
  assert.equal(
    folderSearchState({ kind: 'linked', workspaceId: 'ws1', meta: {} }, all).key,
    FOLDER_NOT_SEARCHABLE
  );
});

test('Search in this folder is offered while the index has filenames to search', () => {
  const offered = state =>
    canSearchFolder(
      folderSearchState(
        linkedRow('ws1', 'dir1'),
        index(apiSource('s1', state, connected('ws1', 'dir1')))
      )
    );
  assert.equal(offered('ready'), true);
  assert.equal(offered('partial'), true);
  assert.equal(offered('mapping'), true);
  assert.equal(offered('interrupted'), true);
  assert.equal(offered('queued'), false); // a refresh cleared it; nothing yet
  assert.equal(offered('unavailable'), false);
});

test('the status line says the count and when the folder was last indexed', () => {
  const state = s =>
    folderSearchState(linkedRow('ws1', 'dir1'), index({ ...s, ...connected('ws1', 'dir1') }));
  assert.deepEqual(folderStatusLines(state(apiSource('s1', 'ready')), UTC), {
    title: 'Searchable',
    detail: '1,284 filenames · indexed 22 Nov, 09:14',
    reason: ''
  });
  const partial = apiSource('s1', 'partial', {
    count: 1,
    reason: 'Some folders or filenames could not be read.'
  });
  assert.deepEqual(folderStatusLines(state(partial), UTC), {
    title: 'Partly indexed',
    detail: '1 filename · indexed 22 Nov, 09:14',
    reason: 'Some folders or filenames could not be read.'
  });
  assert.equal(
    folderStatusLines(state(apiSource('s1', 'mapping', { count: 40, mapped_at: 0 })), UTC).detail,
    '40 filenames so far'
  );
  assert.equal(
    folderStatusLines(state(apiSource('s1', 'queued', { count: 0 })), UTC).detail,
    'Waiting to start'
  );
  const gone = apiSource('s1', 'unavailable', {
    count: 0,
    reason: 'Reconnect the original folder and refresh, or remove it and choose another.'
  });
  assert.deepEqual(folderStatusLines(state(gone), UTC), {
    title: 'Folder unavailable',
    detail: 'Reconnect the original folder and refresh, or remove it and choose another.',
    reason: ''
  });
  assert.equal(
    folderStatusLines(folderSearchState(linkedRow('ws1', 'dir1'), index()), UTC).title,
    'Not searchable'
  );
});

test('a folder never fully indexed says only its count; an older index says its year', () => {
  assert.equal(indexedAt(0, UTC), '');
  assert.equal(indexedAt(undefined, UTC), '');
  assert.equal(indexedAt(1795338840, UTC), '22 Nov, 09:14');
  assert.equal(indexedAt(1767312000, { ...UTC, now: Date.UTC(2027, 0, 5) }), '2 Jan 2026, 00:00');
  assert.equal(filenameCount(0, 'en-GB'), '0 filenames');
  assert.equal(filenameCount(1, 'en-GB'), '1 filename');
  assert.equal(filenameCount(25000, 'en-GB'), '25,000 filenames');
});

test('an answer that would draw the same marks is the same; a count change is not', () => {
  const one = index(apiSource('s1', 'ready', connected('ws1', 'dir1')));
  const again = index(apiSource('s1', 'ready', connected('ws1', 'dir1')));
  const more = index(apiSource('s1', 'ready', { count: 1300, ...connected('ws1', 'dir1') }));
  const partial = index(apiSource('s1', 'partial', connected('ws1', 'dir1')));
  assert.equal(sameFolderSearch(one, again), true);
  assert.equal(sameFolderSearch(one, more), false);
  assert.equal(sameFolderSearch(one, partial), false);
  assert.equal(sameFolderSearch(one, failedFolderSearch()), false);
  assert.equal(sameFolderSearch(failedFolderSearch(), failedFolderSearch()), true);
});

test('each state offers only the actions that apply to it (requirement 33)', () => {
  const actions = serverState =>
    folderActions(
      folderSearchState(
        linkedRow('ws1', 'dir1'),
        index(apiSource('s1', serverState, connected('ws1', 'dir1')))
      )
    );
  assert.deepEqual(actions('ready'), [
    FOLDER_ACTION_SEARCH_IN,
    FOLDER_ACTION_REFRESH,
    FOLDER_ACTION_REMOVE
  ]);
  assert.deepEqual(actions('partial'), [
    FOLDER_ACTION_SEARCH_IN,
    FOLDER_ACTION_REFRESH,
    FOLDER_ACTION_REMOVE
  ]);
  assert.deepEqual(actions('interrupted'), [
    FOLDER_ACTION_SEARCH_IN,
    FOLDER_ACTION_REFRESH,
    FOLDER_ACTION_REMOVE
  ]);
  // Being indexed: no second scan; queued has nothing to search yet.
  assert.deepEqual(actions('mapping'), [FOLDER_ACTION_SEARCH_IN, FOLDER_ACTION_REMOVE]);
  assert.deepEqual(actions('queued'), [FOLDER_ACTION_REMOVE]);
  assert.deepEqual(actions('unavailable'), [FOLDER_ACTION_REFRESH, FOLDER_ACTION_REMOVE]);
  assert.deepEqual(folderActions(folderSearchState(linkedRow('ws1', 'dir1'), index())), [
    FOLDER_ACTION_MAKE_SEARCHABLE
  ]);
  assert.deepEqual(
    folderActions(folderSearchState(linkedRow('ws1', 'dir1'), failedFolderSearch())),
    []
  );
  assert.deepEqual(folderActions(null), []);
});

test('a link the host said needs verifying reads "Needs verifying" until it is searchable', () => {
  const pending = folderSearchFromSources([], { verify: [linkKey('ws1', 'dir1')] });
  const state = folderSearchState(linkedRow('ws1', 'dir1'), pending);
  assert.equal(state.key, FOLDER_VERIFY);
  assert.equal(state.label, 'Needs verifying');
  assert.deepEqual(folderActions(state), [FOLDER_ACTION_MAKE_SEARCHABLE]);
  // Another link of the same workspace is not affected.
  assert.equal(folderSearchState(linkedRow('ws1', 'dir2'), pending).key, FOLDER_NOT_SEARCHABLE);
  // Once the folder is searchable through that link, the source wins.
  const done = folderSearchFromSources(
    readSources([apiSource('s1', 'ready', connected('ws1', 'dir1'))]),
    { verify: [linkKey('ws1', 'dir1')] }
  );
  assert.equal(folderSearchState(linkedRow('ws1', 'dir1'), done).key, FOLDER_SEARCHABLE);
  assert.equal(sameFolderSearch(pending, folderSearchFromSources([])), false);
});

test('what is said when a followed folder finishes indexing', () => {
  const said = (state, extra = {}) =>
    indexOutcome('Album masters', { state, count: 1284, reason: '', ...extra }, 'en-GB');
  assert.equal(said('ready'), 'Album masters is searchable: 1,284 filenames.');
  assert.equal(
    said('partial', { reason: 'Not every entry fit within the search limits.' }),
    'Album masters is partly indexed: 1,284 filenames. Not every entry fit within the search limits.'
  );
  assert.equal(
    said('unavailable', { count: 0, reason: 'Cannot read this folder.' }),
    'Album masters is unavailable. Cannot read this folder.'
  );
  assert.equal(said('mapping'), '');
  assert.equal(isIndexing({ state: 'queued' }), true);
  assert.equal(isIndexing({ state: 'mapping' }), true);
  assert.equal(isIndexing({ state: 'ready' }), false);
  assert.equal(isIndexing(null), false);
});

// --- Search-only folders (requirement 38) -----------------------------------

const searchOnlyRow = sourceId => ({
  id: `search-only/${sourceId}`,
  kind: 'searchOnly',
  name: 'Old projects',
  workspaceId: '',
  meta: { sourceId }
});

test('search-only folders are the searchable ones with no workspace, in list order', () => {
  const all = index(
    apiSource('solo-b', 'ready'),
    apiSource('linked', 'ready', connected('ws1', 'dir1')),
    apiSource('solo-a', 'partial')
  );
  assert.deepEqual(
    searchOnlySources(all).map(source => source.id),
    ['solo-b', 'solo-a']
  );
  assert.deepEqual(searchOnlySources(index()), []);
  assert.deepEqual(searchOnlySources(failedFolderSearch()), []);
  assert.deepEqual(searchOnlySources(emptyFolderSearch({ local: true })), []);
  assert.deepEqual(searchOnlySources(emptyFolderSearch()), []);
});

test("a search-only folder's row says its state and offers Connect to workspace", () => {
  const state = serverState =>
    folderSearchState(searchOnlyRow('s1'), index(apiSource('s1', serverState)));
  assert.equal(state('ready').key, FOLDER_SEARCHABLE);
  assert.equal(state('ready').searchOnly, true);
  assert.deepEqual(folderActions(state('ready')), [
    FOLDER_ACTION_SEARCH_IN,
    FOLDER_ACTION_CONNECT,
    FOLDER_ACTION_REFRESH,
    FOLDER_ACTION_REMOVE
  ]);
  assert.deepEqual(folderActions(state('partial')), [
    FOLDER_ACTION_SEARCH_IN,
    FOLDER_ACTION_CONNECT,
    FOLDER_ACTION_REFRESH,
    FOLDER_ACTION_REMOVE
  ]);
  assert.deepEqual(folderActions(state('mapping')), [
    FOLDER_ACTION_SEARCH_IN,
    FOLDER_ACTION_CONNECT,
    FOLDER_ACTION_REMOVE
  ]);
  // A folder that is not there cannot be reviewed for a workspace.
  assert.deepEqual(folderActions(state('unavailable')), [
    FOLDER_ACTION_REFRESH,
    FOLDER_ACTION_REMOVE
  ]);
  assert.deepEqual(folderActions(state('something-new')), []);
  // Never Make searchable: it is searchable already.
  assert.ok(!folderActions(state('ready')).includes(FOLDER_ACTION_MAKE_SEARCHABLE));
  // A row whose source has gone (connected or removed meanwhile) says nothing.
  assert.equal(folderSearchState(searchOnlyRow('gone'), index(apiSource('s1', 'ready'))), null);
  assert.equal(folderSearchState(searchOnlyRow('s1'), failedFolderSearch()), null);
});

test('connecting or adding a search-only folder is a change worth a redraw', () => {
  const before = index(apiSource('s1', 'ready'));
  const connectedNow = index(apiSource('s1', 'ready', connected('ws1', 'dir1')));
  const another = index(apiSource('s1', 'ready'), apiSource('s2', 'ready'));
  assert.equal(sameFolderSearch(before, index(apiSource('s1', 'ready'))), true);
  assert.equal(sameFolderSearch(before, connectedNow), false);
  assert.equal(sameFolderSearch(before, another), false);
  assert.equal(
    sameFolderSearch(another, index(apiSource('s2', 'ready'), apiSource('s1', 'ready'))),
    false
  );
});

test("Files' starting-page control offers the other choice, and sends the current one back", () => {
  assert.deepEqual(startingPageChoice('search'), {
    label: 'Start on Map instead',
    next: 'later',
    done: 'Ori will start on the Map.',
    after: 'Make Files my starting page'
  });
  for (const current of ['later', 'first', 'legacy']) {
    const choice = startingPageChoice(current);
    assert.equal(choice.label, 'Make Files my starting page', current);
    assert.equal(choice.next, 'search', current);
    assert.equal(choice.after, 'Start on Map instead', current);
  }
});

test('readSources keeps the reason, the count and when the folder was indexed', () => {
  const [source] = readSources([
    apiSource('s1', 'partial', { reason: 'Not every entry fit within the search limits.' })
  ]);
  assert.equal(source.reason, 'Not every entry fit within the search limits.');
  assert.equal(source.count, 1284);
  assert.equal(source.mappedAt, 1795338840);
  const [bare] = readSources([{ id: 's2', state: 'queued' }]);
  assert.equal(bare.reason, '');
  assert.equal(bare.count, 0);
  assert.equal(bare.mappedAt, 0);
});
