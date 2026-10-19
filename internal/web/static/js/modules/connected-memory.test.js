import assert from 'node:assert/strict';
import test from 'node:test';

import {
  changesPhrase,
  entriesLine,
  exclusionSync,
  exportFactCount,
  folderSuggestions,
  generalEntryCount,
  mirrorSignature,
  needsPoll,
  pasteState,
  sourceCardModel,
  sourceCardState,
  trimValue
} from './connected-memory.js';

const NOW = Date.parse('2026-10-19T12:00:00Z');

const codex = overrides => ({
  id: 'codex',
  label: 'Codex',
  kind: 'files',
  enabled: true,
  available: true,
  location: '/Users/me/.codex/memories',
  location_display: '~/.codex/memories',
  exclusions: [],
  entry_count: 109,
  folder_count: 5,
  folders: [{ folder: '/Users/me/Projects/chart-lab', display: '~/Projects/chart-lab', count: 43 }],
  last_sync_finished: '2026-10-19T11:55:00Z',
  last_result: 'ok',
  skipped_files: 0,
  last_changes: { added: 109, updated: 0, removed: 0 },
  next_due: '2026-10-20T11:55:00Z',
  ...overrides
});

test('an unavailable source is dimmed and names the path Ori looked for', () => {
  const off = sourceCardModel(codex({ available: false, enabled: false }), { now: NOW });
  assert.equal(off.state, 'unavailable');
  assert.equal(off.dimmed, true);
  assert.match(off.status, /Not found on this computer\. Ori looked in ~\/\.codex\/memories\./);
  const waiting = sourceCardModel(codex({ available: false }), { now: NOW });
  assert.match(waiting.status, /will sync when that folder appears/);
  assert.equal(waiting.enabled, true, 'enabling an unavailable source is allowed');
});

test('an available source that is off says nothing is read', () => {
  const model = sourceCardModel(
    codex({ enabled: false, last_sync_finished: undefined, next_due: undefined }),
    { now: NOW }
  );
  assert.equal(model.state, 'off');
  assert.equal(model.dimmed, false);
  assert.match(model.status, /Nothing is read until you switch it on/);
  assert.equal(model.lastSync, 'Never');
  assert.equal(model.nextSync, '');
  assert.equal(model.canSync, false);
});

test('a running sync shows syncing and disables Sync now', () => {
  const model = sourceCardModel(codex(), { sync: { running: true }, now: NOW });
  assert.equal(model.state, 'syncing');
  assert.equal(model.status, 'Syncing…');
  assert.equal(model.canSync, false);
});

test('a synced source shows counts, the last sync with its changes, and the next due time', () => {
  const model = sourceCardModel(codex(), { now: NOW });
  assert.equal(model.state, 'synced');
  assert.equal(model.entries, '109 entries across 5 folders');
  assert.equal(model.lastSync, '5m ago (109 added)');
  assert.equal(model.nextSync, 'in 24h');
  assert.match(model.status, /labeled as external/);
  assert.equal(model.canSync, true);
  const partial = sourceCardModel(codex({ last_result: 'partial', skipped_files: 3 }), {
    now: NOW
  });
  assert.match(partial.status, /3 files skipped, see the server log\./);
  const full = sourceCardModel(codex({ store_full: true }), { now: NOW });
  assert.match(full.status, /mirror is full/);
});

test('a failed sync says the mirror was left as it was', () => {
  const model = sourceCardModel(
    codex({ last_result: 'error', last_error: 'codex memory folder not found' }),
    { now: NOW }
  );
  assert.equal(model.state, 'error');
  assert.match(model.status, /^The last sync failed: codex memory folder not found\./);
  assert.match(model.lastSync, /\(failed\)$/);
});

test('an enabled source waiting for its first sync is pending and keeps the page polling', () => {
  const pending = codex({ last_sync_finished: undefined, last_result: undefined });
  assert.equal(sourceCardState(pending, {}), 'pending');
  assert.equal(needsPoll({ sources: [pending], sync: { running: false } }), true);
  assert.equal(needsPoll({ sources: [codex()], sync: { running: true } }), true);
  assert.equal(needsPoll({ sources: [codex()], sync: { running: false } }), false);
  assert.equal(
    needsPoll({ sources: [codex({ available: false, last_sync_finished: undefined })] }),
    false,
    'an unavailable source never finishes a first sync; do not poll for it'
  );
});

test('entry and change phrases', () => {
  assert.equal(entriesLine({ entry_count: 0 }), 'No entries yet');
  assert.equal(entriesLine({ entry_count: 1, folder_count: 0 }), '1 entry');
  assert.equal(entriesLine({ entry_count: 2, folder_count: 1 }), '2 entries across 1 folder');
  assert.equal(changesPhrase({ added: 0, updated: 0, removed: 0 }), 'no changes');
  assert.equal(
    changesPhrase({ added: 2, updated: 1, removed: 3 }),
    '2 added, 1 updated, 3 removed'
  );
});

test('an unparseable sync time falls back to the raw value, never a blank', () => {
  const model = sourceCardModel(codex({ last_sync_finished: 'garbled', next_due: 'garbled' }), {
    now: NOW
  });
  assert.equal(model.lastSync, 'garbled (109 added)');
  assert.equal(model.nextSync, 'soon');
});

test('the exclusion editor suggests the card folders and keeps path case', () => {
  assert.deepEqual(
    folderSuggestions([
      { folder: '/Users/me/Projects/BTC', display: '~/Projects/BTC', count: 4 },
      { folder: '/x', count: 1 },
      null
    ]),
    [{ name: '~/Projects/BTC', total: 4 }]
  );
  assert.equal(trimValue('  ~/Projects/BTC '), '~/Projects/BTC');
});

test('a refresh carrying the old list never wipes a chip the user just added', () => {
  // First render applies the server list.
  let state = exclusionSync({ lastServer: null, server: [], shown: [] });
  assert.deepEqual(state, { apply: false, lastServer: '' });
  // The user adds a chip; the save is in flight and a refresh still says [].
  state = exclusionSync({ lastServer: state.lastServer, server: [], shown: ['~/Projects/notes'] });
  assert.equal(state.apply, false);
  // The save answers with the new list: nothing to change, the chip stays.
  state = exclusionSync({
    lastServer: state.lastServer,
    server: ['~/Projects/notes'],
    shown: ['~/Projects/notes']
  });
  assert.equal(state.apply, false);
  // Another tab changed the list: the editor follows the server.
  state = exclusionSync({ lastServer: state.lastServer, server: [], shown: ['~/Projects/notes'] });
  assert.equal(state.apply, true);
  // A refused save puts the saved list back even though the server did not change.
  const refused = exclusionSync({
    lastServer: '',
    server: [],
    shown: ['relative/path'],
    force: true
  });
  assert.equal(refused.apply, true);
});

test('entries with no folder are counted as general', () => {
  assert.equal(
    generalEntryCount(codex({ entry_count: 23, folders: [{ count: 10 }, { count: 5 }] })),
    8
  );
  assert.equal(generalEntryCount(codex({ entry_count: 2, folders: [{ count: 5 }] })), 0);
  assert.equal(sourceCardModel(codex({ entry_count: 50 }), { now: NOW }).generalCount, 7);
});

test('an empty GEMINI.md says so after a sync', () => {
  const gemini = { id: 'gemini_cli', entry_count: 0, last_sync_finished: '2026-10-19T11:00:00Z' };
  assert.equal(entriesLine(gemini), '0 entries (GEMINI.md is empty)');
  assert.equal(entriesLine({ ...gemini, last_sync_finished: undefined }), 'No entries yet');
});

test('the browser reloads only when what a reader sees may have changed', () => {
  const base = { sources: [codex()] };
  const same = mirrorSignature({ sources: [codex()] });
  assert.equal(mirrorSignature(base), same);
  assert.notEqual(mirrorSignature({ sources: [codex({ enabled: false })] }), same);
  assert.notEqual(mirrorSignature({ sources: [codex({ entry_count: 110 })] }), same);
  assert.notEqual(mirrorSignature({ sources: [codex({ exclusions: ['~/x'] })] }), same);
  assert.notEqual(
    mirrorSignature({ sources: [codex({ last_sync_finished: '2026-10-19T12:00:00Z' })] }),
    same
  );
  assert.equal(mirrorSignature(null), '');
});

test('the Claude account card speaks about pastes, never a missing folder', () => {
  const account = overrides => ({
    id: 'claude_account',
    label: 'Claude account',
    kind: 'paste',
    available: false,
    enabled: false,
    entry_count: 0,
    ...overrides
  });
  const empty = sourceCardModel(account(), { now: NOW });
  assert.equal(empty.state, 'empty');
  assert.equal(empty.dimmed, false);
  assert.match(empty.status, /No paste yet/);
  assert.doesNotMatch(empty.status, /Not found/);
  assert.equal(empty.lastLabel, 'Last pasted');
  assert.equal(empty.lastSync, 'Never');
  const on = sourceCardModel(
    account({ available: true, enabled: true, entry_count: 7, paste_date: '2026-10-19T11:00:00Z' }),
    { now: NOW }
  );
  assert.equal(on.state, 'synced');
  assert.equal(on.lastSync, '1h ago');
  assert.equal(on.entries, '7 entries');
  assert.equal(on.nextSync, '');
  assert.equal(on.canSync, false);
  const off = sourceCardModel(account({ available: true }), { now: NOW });
  assert.match(off.status, /Your paste is kept/);
});

test('the paste box counts kilobytes and refuses empty or oversized text', () => {
  assert.deepEqual(pasteState(''), { counter: '0 / 256 KB', valid: false, tooLarge: false });
  assert.equal(pasteState('  \n ').valid, false);
  assert.equal(pasteState('- a fact').valid, true);
  const korean = '가'.repeat(1024); // 3 KB
  assert.equal(pasteState(korean).counter, '3 / 256 KB');
  const huge = pasteState('x'.repeat(256 * 1024 + 1));
  assert.equal(huge.valid, false);
  assert.equal(huge.tooLarge, true);
});

test('the export summary counts dated lines only', () => {
  const text =
    '## Instructions\n[2026-09-30] - Lead with the result\n## Identity\n[unknown] - Name: Sam\n## Career\n';
  assert.equal(exportFactCount(text), 2);
  assert.equal(exportFactCount('## Instructions\n## Identity\n'), 0);
  assert.equal(exportFactCount(undefined), 0);
});

test('the Claude account card has no file controls', () => {
  const model = sourceCardModel(
    {
      id: 'claude_account',
      label: 'Claude account',
      kind: 'paste',
      available: true,
      enabled: true
    },
    { now: NOW }
  );
  assert.equal(model.isFiles, false);
});
