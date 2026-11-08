import test from 'node:test';
import assert from 'node:assert/strict';
import {
  lastSavedLabel,
  libraryOpenedMessage,
  lowerNoun,
  NEUTRAL_WORDS,
  openLibrarySong,
  projectWords,
  RECENT_SONGS_QUERY,
  recentSongs,
  songFactsLabel,
  songLineText
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

// The words the Music Project Management 0.5.0 candidate declares.
const MUSIC_WORDS = {
  source: 'package',
  noun: 'Song',
  noun_plural: 'Songs',
  roster_title: 'Songs and assistants',
  facts: [
    { kind: 'track_count', unit: 'tracks', unit_one: 'track' },
    { kind: 'tempo', unit: 'BPM' },
    { kind: 'length' }
  ],
  fields: []
};

test('song facts read as tracks, tempo and length under the package words, leaving out what is unknown', () => {
  const label = facts => songFactsLabel(facts, MUSIC_WORDS);
  assert.equal(
    label({ track_count: 14, tempo_bpm: 92, length_seconds: 221 }),
    '14 tracks · 92 BPM · 3:41'
  );
  assert.equal(label({ track_count: 1 }), '1 track');
  assert.equal(label({ tempo_bpm: 92, tempo_varies: true }), '92 BPM, varies');
  assert.equal(label({ tempo_bpm: 128.5 }), '128.5 BPM');
  assert.equal(label({ length_seconds: 59.6 }), '1:00', 'rounded to the second');
  assert.equal(label({ length_seconds: 3725.5 }), '1:02:06', 'h:mm:ss from an hour on');
  assert.equal(label({ length_seconds: 3600 }), '1:00:00');
  assert.equal(label({ length_seconds: 7 }), '0:07');
  assert.equal(label({ tempo_bpm: 92, length_seconds: 200 }), '92 BPM · 3:20');
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
    assert.equal(songFactsLabel(none, MUSIC_WORDS), '', JSON.stringify(none));
    assert.equal(songFactsLabel(none), '', `neutral ${JSON.stringify(none)}`);
  }
});

test('song facts under the neutral words are label-and-value pairs in the host order (PRD #792 FR6)', () => {
  const facts = { track_count: 14, tempo_bpm: 92, length_seconds: 221 };
  assert.equal(songFactsLabel(facts), 'Tracks 14 · Tempo 92 · 3:41');
  assert.equal(songFactsLabel(facts, NEUTRAL_WORDS), 'Tracks 14 · Tempo 92 · 3:41');
  assert.equal(songFactsLabel({ tempo_bpm: 92, tempo_varies: true }), 'Tempo 92, varies');
  assert.equal(songFactsLabel({ track_count: 1 }), 'Tracks 1');
  // Incomplete words fall back to neutral, never a blank.
  assert.equal(songFactsLabel(facts, { noun: 'Song' }), 'Tracks 14 · Tempo 92 · 3:41');
  // A package that declares the words but no facts gets the neutral facts.
  assert.equal(songFactsLabel(facts, { ...MUSIC_WORDS, facts: [] }), 'Tracks 14 · Tempo 92 · 3:41');
  // Declared order wins; a kind left out is not shown; unknown kinds are ignored.
  assert.equal(
    songFactsLabel(facts, {
      ...MUSIC_WORDS,
      facts: [{ kind: 'length' }, { kind: 'tempo', unit: 'bpm' }, { kind: 'key', unit: 'x' }]
    }),
    '3:41 · 92 bpm'
  );
  assert.equal(projectWords(null), NEUTRAL_WORDS);
  assert.equal(projectWords(MUSIC_WORDS).noun, 'Song');
  assert.equal(lowerNoun(MUSIC_WORDS, { plural: true }), 'songs');
  assert.equal(lowerNoun(undefined), 'project');
});

// FR11: the card, roster, tile and status-line words carry no domain word
// under the neutral words. "Tracks" is the host's label for the track_count
// kind (FR6), so the lower-case unit words are what is checked for tracks.
function hasDomainWord(text) {
  return /\b(songs?|bpm)\b/i.test(text) || /\btracks?\b/.test(text);
}

test('neutral facts carry no domain word (PRD #792 FR11)', () => {
  for (const facts of [
    { track_count: 14, tempo_bpm: 92, length_seconds: 221 },
    { track_count: 1, tempo_bpm: 92, tempo_varies: true }
  ]) {
    assert.equal(hasDomainWord(songFactsLabel(facts)), false, songFactsLabel(facts));
  }
  assert.equal(hasDomainWord(songFactsLabel({ track_count: 2 }, MUSIC_WORDS)), true);
});

test('a song line keeps each fact whole and wraps only between facts', () => {
  assert.equal(
    songLineText('Saved 3 days ago', '14 tracks · 92 BPM, varies · 3:41'),
    'Saved 3 days ago · 14\xa0tracks · 92\xa0BPM,\xa0varies · 3:41'
  );
  assert.equal(songLineText('Saved today', ''), 'Saved today', 'no facts: the save time unchanged');
  assert.equal(songLineText('', '1 track'), '1\xa0track');
  assert.equal(songLineText('', ''), '');
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

test('an opened song without its assistant is never called ready', () => {
  assert.match(
    libraryOpenedMessage('Album-1', { staffing: 'added', agent_name: 'REAPER Assistant' }),
    /^Album-1 is ready\. REAPER Assistant was added/
  );
  for (const staffing of [
    'off',
    'consent_stale',
    'assistant_missing',
    'needs_model',
    'reconcile',
    'unsettled',
    'failed'
  ]) {
    const message = libraryOpenedMessage('Album-1', { staffing, created: true });
    assert.doesNotMatch(message, /\bready\b/, staffing);
    assert.match(message, /^Album-1 is open/, staffing);
  }
});
