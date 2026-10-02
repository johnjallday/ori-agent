import test from 'node:test';
import assert from 'node:assert/strict';
import {
  lastSavedLabel,
  openLibrarySong,
  RECENT_SONGS_QUERY,
  recentSongs,
  savedWithFacts,
  songFactsLabel
} from './library-open.js';
import * as library from './project-library.js';

// Local calendar times, so the buckets hold in any time zone the tests run in.
const now = new Date(2026, 9, 2, 15, 30);
const ago = (days, hour = 10) =>
  new Date(now.getFullYear(), now.getMonth(), now.getDate() - days, hour).toISOString();

test('the saved label speaks in calendar days, weeks, months, then a month and year', () => {
  assert.equal(lastSavedLabel(ago(0, 1), now), 'Saved today');
  assert.equal(
    lastSavedLabel(ago(0, 23), now),
    'Saved today',
    'a clock ahead of ours is still today'
  );
  assert.equal(lastSavedLabel(ago(-3), now), 'Saved today', 'a future time is never "in 3 days"');
  assert.equal(lastSavedLabel(ago(1, 23), now), 'Saved yesterday');
  assert.equal(lastSavedLabel(ago(2), now), 'Saved 2 days ago');
  assert.equal(lastSavedLabel(ago(3), now), 'Saved 3 days ago');
  assert.equal(lastSavedLabel(ago(13), now), 'Saved 13 days ago');
  assert.equal(lastSavedLabel(ago(14), now), 'Saved 2 weeks ago');
  assert.equal(lastSavedLabel(ago(44), now), 'Saved 6 weeks ago');
  assert.equal(lastSavedLabel(ago(45), now), 'Saved 1 month ago');
  assert.equal(lastSavedLabel(ago(122), now), 'Saved 4 months ago');
  assert.equal(lastSavedLabel(ago(334), now), 'Saved 11 months ago');
  assert.equal(
    lastSavedLabel(new Date(2025, 9, 31, 12).toISOString(), now),
    'Saved Oct 2025',
    'about eleven months and older names the month'
  );
  assert.equal(lastSavedLabel(new Date(2024, 2, 9, 12).toISOString(), now), 'Saved Mar 2024');
});

test('the saved label is empty for a missing, unreadable or unknown time', () => {
  for (const value of [undefined, null, '', 'not a date', 42, '0001-01-01T00:00:00Z']) {
    assert.equal(lastSavedLabel(value, now), '', String(value));
  }
});

test('both recent-song lists read one page of the library, newest save first', () => {
  assert.equal(RECENT_SONGS_QUERY, 'sort=last_saved&direction=desc&page_size=25');
});

test('recent songs keep openable, dated rows in the order given, six at most', () => {
  const dated = ago(1);
  const rows = [
    { id: 'a', can_open: true, last_saved_at: dated },
    { id: 'b', can_open: false, last_saved_at: dated },
    { id: 'c', can_open: true },
    { id: 'd', can_open: true, last_saved_at: '0001-01-01T00:00:00Z' },
    ...['e', 'f', 'g', 'h', 'i', 'j'].map(id => ({ id, can_open: true, last_saved_at: dated }))
  ];
  assert.deepEqual(
    recentSongs(rows).map(row => row.id),
    ['a', 'e', 'f', 'g', 'h', 'i']
  );
  assert.deepEqual(
    recentSongs(rows, 2).map(row => row.id),
    ['a', 'e']
  );
  assert.deepEqual(recentSongs(null), []);
  assert.deepEqual(recentSongs([null, {}]), []);
});

function fakeFetch(status, body) {
  const calls = [];
  const fetchImpl = async (url, options) => {
    calls.push({ url, options, body: JSON.parse(options.body) });
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => {
        if (body === undefined) throw new Error('no body');
        return body;
      }
    };
  };
  return { calls, fetchImpl };
}

test('opening a song posts the existing open request and returns its result', async () => {
  const { calls, fetchImpl } = fakeFetch(200, { route: '/workspaces/song-1', created: true });
  const result = await openLibrarySong('home 1', 'entry/1', { requestID: 'open-1', fetchImpl });
  assert.deepEqual(result, { route: '/workspaces/song-1', created: true });
  assert.equal(
    calls[0].url,
    '/api/workspaces/home%201/assistant-program/library/projects/entry%2F1/open'
  );
  assert.equal(calls[0].options.method, 'POST');
  assert.deepEqual(calls[0].body, { request_id: 'open-1' });

  await openLibrarySong('home', 'entry', { requestID: 'open-1', selectedFile: 'B.rpp', fetchImpl });
  assert.deepEqual(calls[1].body, { request_id: 'open-1', selected_file: 'B.rpp' });
});

test('a song with several project files throws needs_choice with the file list', async () => {
  const body = {
    error: 'Choose which project file to open',
    reason: 'needs_choice',
    needs_choice: true,
    project_files: ['A.rpp', 'B.rpp']
  };
  const { fetchImpl } = fakeFetch(409, body);
  await assert.rejects(
    openLibrarySong('home', 'entry', { requestID: 'open-1', fetchImpl }),
    error => {
      assert.equal(error.message, 'Choose which project file to open');
      assert.equal(error.status, 409);
      assert.equal(error.reason, 'needs_choice');
      assert.deepEqual(error.payload, body);
      assert.deepEqual(library.libraryOpenChoices(error), ['A.rpp', 'B.rpp']);
      return true;
    }
  );
});

test('any other failure carries the server message, or a plain one when there is none', async () => {
  const conflict = fakeFetch(409, { error: 'Set up a model first', reason: 'needs_model' });
  await assert.rejects(
    openLibrarySong('home', 'entry', { requestID: 'r', fetchImpl: conflict.fetchImpl }),
    { message: 'Set up a model first', reason: 'needs_model' }
  );
  const empty = fakeFetch(502, undefined);
  await assert.rejects(
    openLibrarySong('home', 'entry', { requestID: 'r', fetchImpl: empty.fetchImpl }),
    { message: 'Library request failed (502)', reason: '' }
  );
});

test('song facts read as tracks, tempo and length, leaving out what is unknown', () => {
  assert.equal(
    songFactsLabel({ track_count: 14, tempo_bpm: 92, length_seconds: 221 }),
    '14 tracks · 92 BPM · 3:41'
  );
  assert.equal(songFactsLabel({ track_count: 1 }), '1 track');
  assert.equal(songFactsLabel({ tempo_bpm: 92, tempo_varies: true }), '92 BPM, varies');
  assert.equal(songFactsLabel({ tempo_bpm: 128.5 }), '128.5 BPM');
  assert.equal(songFactsLabel({ length_seconds: 59.6 }), '1:00', 'rounded to the second');
  assert.equal(songFactsLabel({ length_seconds: 3725.5 }), '1:02:06', 'h:mm:ss from an hour on');
  assert.equal(songFactsLabel({ length_seconds: 3600 }), '1:00:00');
  assert.equal(songFactsLabel({ length_seconds: 7 }), '0:07');
  assert.equal(songFactsLabel({ tempo_bpm: 92, length_seconds: 200 }), '92 BPM · 3:20');
  for (const none of [
    undefined,
    null,
    {},
    'facts',
    { track_count: 0, tempo_bpm: 0, length_seconds: 0 },
    { track_count: -2, tempo_bpm: Number.NaN, length_seconds: Infinity },
    { track_count: '14', tempo_bpm: '92' },
    { track_count: 1.5 },
    { length_seconds: 0.2 },
    { tempo_varies: true }
  ]) {
    assert.equal(songFactsLabel(none), '', JSON.stringify(none));
  }
});

test('a song line joins its save time and its facts', () => {
  const facts = { track_count: 14, tempo_bpm: 92, length_seconds: 221 };
  assert.equal(
    savedWithFacts({ last_saved_at: ago(3), facts }, now),
    'Saved 3 days ago · 14 tracks · 92 BPM · 3:41'
  );
  assert.equal(savedWithFacts({ last_saved_at: ago(3) }, now), 'Saved 3 days ago');
  assert.equal(savedWithFacts({ facts }, now), '14 tracks · 92 BPM · 3:41');
  assert.equal(savedWithFacts({}, now), '');
  assert.equal(savedWithFacts(null, now), '');
});

test('the Home library still exports the open helpers it always had', () => {
  for (const name of [
    'lastSavedLabel',
    'libraryOpenAction',
    'libraryOpenChoices',
    'libraryOpenRoute',
    'libraryOpenedMessage'
  ]) {
    assert.equal(typeof library[name], 'function', name);
  }
});
