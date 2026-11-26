import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  ARRIVAL_INVALID_LINE,
  ARRIVAL_STALE_LINE,
  myNotesArrivalNote,
  myNotesNoteURL,
  validNoteLinkName
} from './my-notes-arrival.js';
import { NotebookState } from './my-notes-browser.js';

const REF = 'c'.repeat(64);
const OWNED = {
  status: 'connected',
  source: { name: 'Demo Notes', origin: 'created_by_ori', kind: 'obsidian' },
  browse_ref: REF
};

// What arrival reads: the fragment after `#`.
const arrive = (url, view = OWNED) =>
  myNotesArrivalNote(new URLSearchParams(new URL(url, 'http://x').hash.slice(1)), view);

test('a link for each kind round-trips to its own folder and note, names encoded', () => {
  const cases = [
    ['project', 'House', ['Projects', 'House'], 'README.md'],
    ['project', 'Kitchen & Bath #2 — 50%', ['Projects', 'Kitchen & Bath #2 — 50%'], 'README.md'],
    ['daily', '2026-11-25.md', ['Daily'], '2026-11-25.md'],
    [
      'inbox',
      '20261126-1012-ask-about-the-roof-warranty.md',
      ['Inbox'],
      '20261126-1012-ask-about-the-roof-warranty.md'
    ],
    ['inbox', 'café plan?.md', ['Inbox'], 'café plan?.md']
  ];
  for (const [kind, name, folder, note] of cases) {
    const url = myNotesNoteURL({ ref: REF, kind, name });
    assert.ok(url.startsWith('/my-notes#'), url);
    assert.ok(!url.includes('?'), 'no navigation query: ' + url);
    assert.deepEqual(arrive(url), { kind, name, folder, note });
  }
});

test('the older Inbox capture receipt still opens its note', () => {
  const receipt = '/my-notes#' + new URLSearchParams({ ref: REF, note: '20261126-1012-x.md' });
  assert.deepEqual(arrive(receipt), {
    kind: 'inbox',
    name: '20261126-1012-x.md',
    folder: ['Inbox'],
    note: '20261126-1012-x.md'
  });
});

test('no note link is no arrival note; a plain /my-notes stays plain', () => {
  assert.equal(arrive('/my-notes'), null);
  assert.equal(arrive('/my-notes#'), null);
  assert.equal(myNotesArrivalNote(null, OWNED), null);
});

test('a link for another connection or notebook is stale and falls back', () => {
  const url = myNotesNoteURL({ ref: REF, kind: 'project', name: 'House' });
  const stale = { problem: 'stale', message: ARRIVAL_STALE_LINE };
  assert.deepEqual(arrive(url, { ...OWNED, browse_ref: 'd'.repeat(64) }), stale);
  assert.deepEqual(
    arrive(url, { ...OWNED, source: { name: 'Demo Notes', origin: 'connected' } }),
    stale,
    'a same-named folder Ori did not set up is not substituted'
  );
  assert.deepEqual(arrive(url, { status: 'not_connected' }), stale);
  assert.deepEqual(arrive(url, null), stale);
});

test('malformed links never become a path: they fall back with a sentence', () => {
  const invalid = { problem: 'invalid', message: ARRIVAL_INVALID_LINE };
  const bad = [
    { ref: REF, kind: 'project', name: '../Secrets' },
    { ref: REF, kind: 'project', name: 'House/Plans' },
    { ref: REF, kind: 'project', name: 'C:\\House' },
    { ref: REF, kind: 'project', name: '.obsidian' },
    { ref: REF, kind: 'project', name: ' House' },
    { ref: REF, kind: 'project', name: 'House.' },
    { ref: REF, kind: 'project', name: 'Ho\u0000use' },
    { ref: REF, kind: 'project', name: 'x'.repeat(256) },
    { ref: REF, kind: 'inbox', name: 'README.md' },
    { ref: REF, kind: 'daily', name: 'notes.txt' },
    { ref: REF, kind: 'inbox', name: '../../etc/passwd.md' },
    { ref: REF, kind: 'people', name: 'Maya.md' },
    { ref: REF, kind: 'project' },
    { ref: REF, name: 'House' }
  ];
  for (const parts of bad) {
    const hash = new URLSearchParams(
      Object.fromEntries(Object.entries(parts).filter(([, v]) => v !== undefined))
    );
    assert.deepEqual(arrive('/my-notes#' + hash), invalid, JSON.stringify(parts));
    assert.equal(myNotesNoteURL(parts), '', 'no link is built: ' + JSON.stringify(parts));
  }
  assert.equal(myNotesNoteURL({ ref: 'short', kind: 'project', name: 'House' }), '');
  assert.equal(validNoteLinkName('project', 'House'), true);
  assert.equal(validNoteLinkName('inbox', 'note.markdown'), true);
});

test('a note link whose folder is gone tells its owner before opening the top', async () => {
  const missing = [];
  const requests = [];
  const state = new NotebookState({
    request: async path => {
      requests.push(path);
      if (path.includes('part=Projects')) {
        const error = new Error('gone');
        error.status = 404;
        throw error;
      }
      return { directory: { entries: [] } };
    },
    onLandingMissing: parts => missing.push(parts.join('/'))
  });
  state.start(OWNED, ['Projects', 'Gone']);
  for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(missing, ['Projects/Gone']);
  assert.deepEqual(state.folder, [], 'the notebook’s top is open');
});
