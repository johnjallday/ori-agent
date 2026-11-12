// Tests for song-learned.js — the "Learned from the project file" section of a
// song (learn-the-songs FR16) and its place on the song card.
//   node --test internal/web/static/js/modules/song-learned.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  LEARNED_COLLAPSE_ABOVE,
  LEARNED_HEADING,
  NOTHING_FOUND_TEXT,
  NOT_READ_TEXT,
  learnedView,
  markerTimeLabel,
  renderLearned
} from './song-learned.js';
import { renderSongCard, songCardView } from './song-card.js';

// A minimal DOM: text-only nodes. innerHTML throws, so a name is never parsed
// as markup; there is no dataset, as in the card's own tests.
function makeDocument() {
  const doc = {
    createElement: tag => makeNode(tag, doc),
    createTextNode: value => ({ nodeType: 3, textContent: String(value) })
  };
  return doc;
}

function makeNode(tag, doc) {
  const attributes = {};
  return {
    tagName: tag.toUpperCase(),
    ownerDocument: doc,
    children: [],
    className: '',
    hidden: false,
    _text: '',
    get textContent() {
      return this.children.length ? this.children.map(c => c.textContent).join('') : this._text;
    },
    set textContent(value) {
      this.children = [];
      this._text = String(value);
    },
    set innerHTML(_value) {
      throw new Error('innerHTML must not be used');
    },
    append(...nodes) {
      this.children.push(...nodes);
    },
    replaceChildren(...nodes) {
      this.children = nodes;
      this._text = '';
    },
    setAttribute(k, v) {
      attributes[k] = String(v);
    },
    getAttribute(k) {
      return attributes[k] ?? null;
    }
  };
}

function walk(node, visit) {
  visit(node);
  for (const child of node.children || []) walk(child, visit);
}

function all(root, className) {
  const out = [];
  walk(root, node => {
    if (typeof node.className === 'string' && node.className.split(' ').includes(className)) {
      out.push(node);
    }
  });
  return out;
}

const find = (root, className) => all(root, className)[0] ?? null;
const draw = raw => renderLearned(makeDocument(), learnedView(raw));
const names = count => Array.from({ length: count }, (_, i) => `Track ${i + 1}`);

test('a time is m:ss, and h:mm:ss from an hour on; anything else is nothing', () => {
  for (const [seconds, want] of [
    [0, '0:00'],
    [5, '0:05'],
    [65, '1:05'],
    [221.9, '3:41'],
    [600, '10:00'],
    [3599, '59:59'],
    [3600, '1:00:00'],
    [3725, '1:02:05'],
    ['30', '0:30']
  ]) {
    assert.equal(markerTimeLabel(seconds), want, String(seconds));
  }
  for (const bad of [-1, NaN, Infinity, 'soon', null, undefined, {}]) {
    assert.equal(markerTimeLabel(bad), '', String(bad));
  }
});

test('the view keeps names in order, words a region with both times, and carries what was left out', () => {
  const view = learnedView({
    tracks: ['Kick', '  Bass  ', '', 7],
    tracks_left_out: 3,
    markers: [
      { name: 'Intro', at: 0 },
      { name: 'Chorus', at: 30, end: 45.5 },
      { name: '', at: 90 },
      { name: 'Backwards', at: 50, end: 40 },
      { name: 'No time', at: 'x' }
    ],
    markers_left_out: 2
  });
  assert.deepEqual(view.tracks, ['Kick', 'Bass', '7']);
  assert.equal(view.tracksMore, 3);
  assert.deepEqual(view.markers, [
    { name: 'Intro', time: '0:00' },
    { name: 'Chorus', time: '0:30–0:45' },
    { name: '', time: '1:30' },
    { name: 'Backwards', time: '0:50' }
  ]);
  assert.equal(view.markersMore, 2);
  assert.equal(view.notRead, null);
});

test('nothing is a learned section but a song that was read or was not read', () => {
  for (const nothing of [null, undefined, 'text', 4, []]) {
    // An array is an object but holds no learned data: it reads as an empty section.
    if (Array.isArray(nothing)) continue;
    assert.equal(learnedView(nothing), null, String(nothing));
    assert.equal(renderLearned(makeDocument(), learnedView(nothing)), null);
  }
  // Read, but the file held no names: a section that says so.
  const empty = draw({});
  assert.equal(find(empty, 'song-learned-title').textContent, LEARNED_HEADING);
  assert.equal(find(empty, 'song-learned-note').textContent, NOTHING_FOUND_TEXT);
  assert.equal(all(empty, 'song-learned-list').length, 0);
  // Garbage in the lists is dropped, not drawn.
  const messy = learnedView({
    tracks: 'Kick',
    markers: { at: 1 },
    tracks_left_out: -4,
    markers_left_out: 'many'
  });
  assert.deepEqual(
    [messy.tracks, messy.markers, messy.tracksMore, messy.markersMore],
    [[], [], 0, 0]
  );
});

test('a song that was not read says why, and shows no lists', () => {
  for (const [reason, text] of Object.entries(NOT_READ_TEXT)) {
    const section = draw({
      not_read: reason,
      tracks: ['Never shown'],
      markers: [{ name: 'x', at: 1 }]
    });
    assert.equal(section.getAttribute('data-not-read'), reason);
    assert.equal(find(section, 'song-learned-note').textContent, text);
    assert.equal(all(section, 'song-learned-list').length, 0, reason);
    assert.ok(!section.textContent.includes('Never shown'));
  }
  assert.equal(NOT_READ_TEXT.no_grammar, "Ori can't read this kind of project file.");
  // A reason the page does not know still says it was not read, in plain words.
  assert.match(
    find(draw({ not_read: 'something_new' }), 'song-learned-note').textContent,
    /not read/
  );
});

test('lists are open up to about a dozen entries and collapsed above, with "and K more"', () => {
  const small = draw({ tracks: names(LEARNED_COLLAPSE_ABOVE) });
  assert.equal(find(small, 'song-learned-tracks').open, true, 'twelve tracks start open');
  const big = draw({ tracks: names(LEARNED_COLLAPSE_ABOVE + 1), tracks_left_out: 5 });
  const block = find(big, 'song-learned-tracks');
  assert.notEqual(block.open, true, 'thirteen tracks start collapsed');
  assert.equal(
    find(block, 'song-learned-summary').textContent,
    `Tracks · ${LEARNED_COLLAPSE_ABOVE + 1 + 5}`
  );
  assert.equal(all(block, 'song-learned-name').length, LEARNED_COLLAPSE_ABOVE + 1);
  assert.equal(find(block, 'song-learned-more').textContent, 'and 5 more');
  // No "and K more" when nothing was left out.
  assert.equal(find(small, 'song-learned-more'), null);
  // Markers collapse the same way and are listed with their times.
  const marked = draw({
    markers: Array.from({ length: 14 }, (_, i) => ({ name: `Mark ${i}`, at: i * 10 })),
    markers_left_out: 2
  });
  const markers = find(marked, 'song-learned-markers');
  assert.notEqual(markers.open, true);
  assert.equal(find(markers, 'song-learned-summary').textContent, 'Markers and regions · 16');
  assert.equal(all(markers, 'song-learned-time')[1].textContent, '0:10');
  assert.equal(find(markers, 'song-learned-more').textContent, 'and 2 more');
  // A cap that left out everything still says so.
  const onlyCounts = draw({ tracks_left_out: 40 });
  assert.equal(find(onlyCounts, 'song-learned-summary').textContent, 'Tracks · 40');
  assert.equal(find(onlyCounts, 'song-learned-more').textContent, 'and 40 more');
});

test('names are drawn as text, never as markup, and a section is read-only', () => {
  const hostile = '<img src=x onerror=alert(1)> "Night" & Day';
  const section = draw({
    tracks: [hostile],
    markers: [{ name: '<b>Verse</b>', at: 12 }]
  });
  const track = find(section, 'song-learned-tracks');
  const item = all(track, 'song-learned-name')[0];
  assert.equal(item.textContent, hostile);
  assert.equal(item.children.length, 0, 'the name is a text value, not an element');
  assert.equal(
    all(find(section, 'song-learned-markers'), 'song-learned-name')[0].textContent,
    '<b>Verse</b>'
  );
  // Read-only: no buttons, inputs or links.
  const tagsSeen = new Set();
  walk(section, node => tagsSeen.add(node.tagName));
  for (const control of ['BUTTON', 'INPUT', 'A', 'TEXTAREA', 'SELECT', 'FORM']) {
    assert.ok(!tagsSeen.has(control), `${control} in a read-only section`);
  }
  assert.equal(section.getAttribute('aria-label'), LEARNED_HEADING);
});

test('an unnamed marker still shows its time', () => {
  const section = draw({ markers: [{ at: 75 }] });
  const item = find(section, 'song-learned-marker');
  assert.equal(find(item, 'song-learned-time').textContent, '1:15');
  assert.equal(find(item, 'song-learned-name'), null);
});

// ---- On the song card ----

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

const linked = overrides => ({
  linked: true,
  words: MUSIC_WORDS,
  home_id: 'home-1',
  home_name: 'Music Production',
  entry_id: 'entry-1',
  title: 'Night Drive',
  facts: { tempo_bpm: 92, length_seconds: 221, track_count: 4 },
  facts_state: 'on',
  keeper_agent_name: 'Studio Keeper',
  open_work_counts: { backlog: 0, ready: 0, in_progress: 0, review: 0, done: 0, cancelled: 0 },
  home_route: '/workspaces/music-home',
  home_entry_route: '/workspaces/music-home/assistant#projectLibraryPanel',
  ...overrides
});

const renderCard = data => {
  const container = makeDocument().createElement('div');
  renderSongCard(container, songCardView(data, { now: new Date(2026, 5, 10, 12) }));
  return container.children[0];
};

test('the card draws the section under its facts line when the server sends it', () => {
  const card = renderCard(
    linked({
      learned: {
        tracks: ['Kick', 'Bass', 'Synth Pad', 'Lead Vocal'],
        markers: [{ name: 'Chorus', at: 30, end: 45 }]
      }
    })
  );
  const order = card.children.map(child => child.className);
  const facts = order.indexOf('song-card-facts');
  const learned = order.indexOf('song-learned');
  assert.ok(facts >= 0 && learned === facts + 1, `the section follows the facts line: ${order}`);
  const section = card.children[learned];
  assert.deepEqual(
    all(find(section, 'song-learned-tracks'), 'song-learned-name').map(n => n.textContent),
    ['Kick', 'Bass', 'Synth Pad', 'Lead Vocal']
  );
  assert.equal(all(section, 'song-learned-time')[0].textContent, '0:30–0:45');
  // The numbers line is unchanged beside it.
  assert.equal(find(card, 'song-card-facts').textContent, '4 tracks · 92 BPM · 3:41');
});

test('the card has no section when the server sends none, and says why a song was not read', () => {
  assert.equal(find(renderCard(linked()), 'song-learned'), null);
  assert.equal(find(renderCard(linked({ learned: null })), 'song-learned'), null);
  const notRead = renderCard(
    linked({ learned: { not_read: 'no_grammar' }, facts: null, facts_state: 'on' })
  );
  assert.equal(
    find(find(notRead, 'song-learned'), 'song-learned-note').textContent,
    "Ori can't read this kind of project file."
  );
  // A card for a song that is not linked to the Home shows no section either.
  assert.equal(
    songCardView({ linked: true, title: 'X', learned: { tracks: ['Kick'] } }).learned,
    null
  );
});
