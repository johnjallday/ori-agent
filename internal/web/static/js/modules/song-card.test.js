// Tests for song-card.js — the read-only Song card on a linked song's Details
// page and in the Home map's selected context.
//   node --test internal/web/static/js/modules/song-card.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  factsAbsentText,
  FACTS_OFF_TEXT,
  NOT_LINKED_TEXT,
  NO_KEEPER_TEXT,
  loadSongCard,
  renderSongCard,
  safeWorkspaceRoute,
  songCardLinks,
  songCardURL,
  songCardView
} from './song-card.js';

const NOW = new Date(2026, 5, 10, 12, 0, 0);

// The words the Music Project Management 0.5.0 candidate declares; the server
// sends them on the card response (PRD #792 FR7).
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

function linked(overrides = {}) {
  return {
    linked: true,
    words: MUSIC_WORDS,
    home_id: 'home-1',
    home_name: 'Music Production',
    entry_id: 'entry-1',
    title: 'Night Drive',
    facts: { tempo_bpm: 92, length_seconds: 221, track_count: 14 },
    facts_state: 'on',
    last_saved_at: new Date(2026, 5, 7, 9, 0, 0).toISOString(),
    keeper_agent_name: 'Studio Keeper',
    status_line: '14 tracks · 92 BPM · 3:41; saved 3 days ago; no open work',
    open_work_counts: { backlog: 0, ready: 0, in_progress: 0, review: 0, done: 0, cancelled: 0 },
    home_route: '/workspaces/music-home',
    home_entry_route: '/workspaces/music-home/assistant#projectLibraryPanel',
    ...overrides
  };
}

// ---- Minimal DOM: text-only nodes; innerHTML throws so no server text is
// ever parsed as HTML.
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

function find(root, className) {
  let found = null;
  walk(root, node => {
    if (
      !found &&
      typeof node.className === 'string' &&
      node.className.split(' ').includes(className)
    ) {
      found = node;
    }
  });
  return found;
}

function tags(root, tagName) {
  const out = [];
  walk(root, node => {
    if (node.tagName === tagName) out.push(node);
  });
  return out;
}

test('a non-song workspace has no card and the container stays hidden and empty', () => {
  assert.equal(songCardView({ linked: false }), null);
  assert.equal(songCardView(null), null);
  const container = makeDocument().createElement('div');
  container.children.push({ textContent: 'stale' });
  assert.equal(renderSongCard(container, null), null);
  assert.equal(container.hidden, true);
  assert.equal(container.children.length, 0);
});

test('a linked song with facts words facts, save, keeper and status like the shelf', () => {
  const view = songCardView(linked(), { now: NOW });
  assert.equal(view.title, 'Night Drive');
  assert.deepEqual(view.home, { label: 'Music Production', href: '/workspaces/music-home' });
  assert.equal(view.factsText, '14 tracks · 92 BPM · 3:41');
  assert.equal(view.factsNote, '');
  assert.equal(view.savedText, 'Saved 3 days ago');
  assert.equal(view.keeperText, 'Studio Keeper');
  assert.equal(view.statusLine, '14 tracks · 92 BPM · 3:41; saved 3 days ago; no open work');
  assert.deepEqual(view.openInHome, {
    label: 'Open in Home',
    href: '/workspaces/music-home/assistant#projectLibraryPanel'
  });
  assert.deepEqual(
    songCardLinks(view).map(link => link.label),
    ['Music Production', 'Open in Home']
  );
});

test('the rendered card uses native anchors only, in the PRD order, and no buttons', () => {
  const container = makeDocument().createElement('div');
  container.hidden = true;
  renderSongCard(container, songCardView(linked(), { now: NOW }));
  assert.equal(container.hidden, false);
  const card = container.children[0];
  assert.equal(card.getAttribute('aria-label'), 'Song: Night Drive');
  assert.deepEqual(
    card.children.map(child => child.className),
    [
      'song-card-head',
      'song-card-facts',
      'song-card-saved',
      'song-card-keeper',
      'song-card-status',
      'song-card-open'
    ]
  );
  assert.equal(find(card, 'song-card-keeper').textContent, 'Kept by Studio Keeper');
  const anchors = tags(card, 'A');
  assert.deepEqual(
    anchors.map(a => [a.textContent, a.getAttribute('href')]),
    [
      ['Music Production', '/workspaces/music-home'],
      ['Open in Home', '/workspaces/music-home/assistant#projectLibraryPanel']
    ]
  );
  assert.equal(tags(card, 'BUTTON').length, 0);
});

test('facts off says so and links to the Home switch; it never offers to toggle', () => {
  const view = songCardView(
    linked({
      facts: undefined,
      facts_state: 'off',
      facts_switch_route: '/workspaces/music-home/assistant#projectLibrarySongDetails'
    }),
    { now: NOW }
  );
  assert.equal(view.factsText, '');
  assert.equal(view.factsNote, FACTS_OFF_TEXT);
  assert.deepEqual(view.factsSwitch, {
    label: 'Turn on project details',
    href: '/workspaces/music-home/assistant#projectLibrarySongDetails'
  });
  const container = makeDocument().createElement('div');
  renderSongCard(container, view);
  const facts = find(container, 'song-card-facts');
  assert.equal(facts.textContent, `${FACTS_OFF_TEXT} Turn on project details`);
  assert.equal(tags(container, 'A').length, 3);
});

test('facts absent says the song was not read yet, without a switch link', () => {
  for (const data of [
    linked({ facts: undefined, facts_state: 'absent' }),
    linked({ facts: {}, facts_state: 'on' })
  ]) {
    const view = songCardView(data, { now: NOW });
    assert.equal(view.factsNote, 'Project details not read yet for this song.');
    assert.equal(view.factsNote, factsAbsentText(MUSIC_WORDS));
    assert.equal(view.factsSwitch, null);
  }
});

const WORDS_WITH_FIELDS = {
  ...MUSIC_WORDS,
  fields: [
    { id: 'key', label: 'Key', kind: 'text' },
    { id: 'references', label: 'Reference tracks', kind: 'list' }
  ]
};

const FULL_FIELDS = {
  stage: 'mixing',
  status: 'on_hold',
  next_action: 'Send stems to the mixer',
  blockers: ['Vocal comp', 'Bass DI'],
  deliverables: ['Stems v2'],
  milestones: [
    { id: 'm1', label: 'Mix approved', due_date: '2026-07-01', complete: false },
    { id: 'm2', label: 'Rough mix', complete: true },
    { id: 'm3', label: 'Master', due_date: '2027-01-15' }
  ],
  // Out of declared order on purpose; "retired" is not declared.
  extra: { references: ['Song A', 'Song B'], key: ['F minor'], retired: ['ignored'] },
  revision: 4
};

test('the details block lists every set field in the PRD order, then plugin fields as declared (PRD #792 FR12)', () => {
  const view = songCardView(linked({ words: WORDS_WITH_FIELDS, fields: FULL_FIELDS }), {
    now: NOW
  });
  assert.deepEqual(
    view.details.map(row => [row.label, row.lines.map(line => line.text ?? line)]),
    [
      ['Stage', ['Mixing']],
      ['Status', ['On hold']],
      ['Next action', ['Send stems to the mixer']],
      ['Blockers', ['Vocal comp', 'Bass DI']],
      ['Deliverables', ['Stems v2']],
      ['Milestones', ['Mix approved · due Jul 1', 'Rough mix · done', 'Master · due Jan 15, 2027']],
      ['Key', ['F minor']],
      ['Reference tracks', ['Song A', 'Song B']]
    ]
  );
  assert.equal(view.details[5].lines[1].done, true);

  const container = makeDocument().createElement('div');
  renderSongCard(container, view);
  const card = container.children[0];
  assert.deepEqual(
    card.children.map(child => child.className),
    [
      'song-card-head',
      'song-card-facts',
      'song-card-saved',
      'song-card-keeper',
      'song-card-status',
      'song-card-details',
      'song-card-open'
    ]
  );
  const details = find(card, 'song-card-details');
  assert.equal(tags(details, 'DT').length, 8);
  assert.equal(
    tags(details, 'LI').length,
    8,
    'blockers, deliverables, milestones and the list field'
  );
  assert.equal(find(details, 'song-card-detail-done').textContent, 'Rough mix · done');
  assert.doesNotMatch(details.textContent, /ignored|retired/);
  // FR13: one more navigation, Edit details, to the shelf entry; no buttons.
  assert.deepEqual(
    songCardLinks(view).map(link => link.label),
    ['Music Production', 'Edit details', 'Open in Home']
  );
  assert.equal(find(details, 'song-card-edit').getAttribute('href'), linked().home_entry_route);
  assert.equal(tags(card, 'BUTTON').length, 0);
});

test('unset rows are left out, and a card with no details says so in one line', () => {
  const sparse = songCardView(
    linked({ fields: { next_action: 'Mix', blockers: [], revision: 1 } })
  );
  assert.deepEqual(
    sparse.details.map(row => row.label),
    ['Next action']
  );
  const empty = songCardView(linked({ fields: { revision: 0 } }), { now: NOW });
  assert.deepEqual(empty.details, []);
  const container = makeDocument().createElement('div');
  renderSongCard(container, empty);
  assert.equal(find(container, 'song-card-details-empty').textContent, 'No details yet.');
  // An unknown stage or status has no label and is left out.
  assert.deepEqual(songCardView(linked({ fields: { stage: 'touring', status: 'x' } })).details, []);
  // Plugin fields need the Home's words: neutral words declare none.
  assert.deepEqual(
    songCardView(linked({ words: undefined, fields: { extra: { key: ['F'] } } })).details,
    []
  );
});

test('a broken link shows no details block and no Edit details; four navigations at most', () => {
  const broken = songCardView(linked({ entry_id: '', fields: FULL_FIELDS }), { now: NOW });
  assert.equal(broken.details, null);
  assert.equal(broken.editDetails, null);
  const container = makeDocument().createElement('div');
  renderSongCard(container, broken);
  assert.equal(find(container, 'song-card-details'), null);
  const all = songCardView(
    linked({
      words: WORDS_WITH_FIELDS,
      fields: FULL_FIELDS,
      facts: undefined,
      facts_state: 'off',
      facts_switch_route: '/workspaces/music-home/assistant#projectLibrarySongDetails'
    })
  );
  assert.equal(songCardLinks(all).length, 4);
});

test('without package words the card reads the neutral Project words (PRD #792 FR6, FR8)', () => {
  const data = linked({ words: undefined, title: '' });
  const view = songCardView(data, { now: NOW });
  assert.equal(view.noun, 'Project');
  assert.equal(view.title, 'Untitled project');
  assert.equal(view.factsText, 'Tracks 14 · Tempo 92 · 3:41');
  assert.equal(
    songCardView(linked({ words: { source: 'neutral' }, facts_state: 'absent' })).factsNote,
    'Project details not read yet for this project.'
  );
  const container = makeDocument().createElement('div');
  renderSongCard(container, songCardView(linked({ words: undefined }), { now: NOW }));
  const card = container.children[0];
  assert.equal(card.getAttribute('aria-label'), 'Project: Night Drive');
  assert.equal(find(container, 'song-card-eyebrow').textContent, 'Project');
  // FR11: nothing the card itself words names a song, BPM or tracks. (The
  // status line is the server's and is checked in Go.)
  const rendered = [
    card.getAttribute('aria-label'),
    find(container, 'song-card-eyebrow').textContent,
    find(container, 'song-card-facts').textContent,
    view.title,
    songCardView(linked({ words: undefined, facts_state: 'absent' })).factsNote
  ].join(' | ');
  assert.equal(/\b(songs?|bpm)\b/i.test(rendered) || /\btracks?\b/.test(rendered), false, rendered);
  // Words passed by a caller are used when the response carries none.
  assert.equal(songCardView(linked({ words: undefined }), { words: MUSIC_WORDS }).noun, 'Song');
});

test('a broken or diverged link shows the card without facts or Open in Home', () => {
  const view = songCardView(
    linked({
      entry_id: '',
      facts: undefined,
      facts_state: '',
      last_saved_at: undefined,
      home_entry_route: ''
    }),
    { now: NOW }
  );
  assert.equal(view.factsNote, NOT_LINKED_TEXT);
  assert.equal(view.openInHome, null);
  assert.equal(view.savedText, '');
  // Even a stray entry route is ignored without an entry.
  assert.equal(songCardView(linked({ entry_id: '' })).openInHome, null);
});

test('a song without a keeper says so; a missing title still reads as a song', () => {
  const view = songCardView(linked({ keeper_agent_name: '', title: '' }), { now: NOW });
  assert.equal(view.keeperText, NO_KEEPER_TEXT);
  assert.equal(view.title, 'Untitled song');
  const container = makeDocument().createElement('div');
  renderSongCard(container, view);
  assert.equal(find(container, 'song-card-keeper').textContent, NO_KEEPER_TEXT);
});

test('only same-origin workspace routes become links', () => {
  assert.equal(safeWorkspaceRoute('/workspaces/a/assistant#x'), '/workspaces/a/assistant#x');
  for (const bad of [
    '//evil.example/workspaces/a',
    'https://evil.example',
    'javascript:alert(1)',
    '/settings',
    ''
  ]) {
    assert.equal(safeWorkspaceRoute(bad), '', bad);
  }
  const view = songCardView(linked({ home_route: 'https://evil.example', home_entry_route: '/x' }));
  assert.equal(view.home.href, '');
  assert.equal(view.openInHome, null);
  assert.deepEqual(songCardLinks(view), []);
});

test('loadSongCard fetches the endpoint and hides the card on any failure', async () => {
  assert.equal(songCardURL('a b'), '/api/workspaces/a%20b/song-card');
  const doc = makeDocument();
  const container = doc.createElement('div');
  let asked = '';
  const view = await loadSongCard(container, 'ws-1', {
    now: NOW,
    fetchImpl: async url => {
      asked = url;
      return { ok: true, json: async () => linked() };
    }
  });
  assert.equal(asked, '/api/workspaces/ws-1/song-card');
  assert.equal(view.title, 'Night Drive');
  assert.equal(container.hidden, false);

  const failed = await loadSongCard(container, 'ws-1', {
    fetchImpl: async () => ({ ok: false, status: 404, json: async () => ({}) })
  });
  assert.equal(failed, null);
  assert.equal(container.hidden, true);
  assert.equal(container.children.length, 0);
});
