// Tests for home-song-roster.js — the "Songs and assistants" section of a
// program Home's Details.
//   node --test internal/web/static/js/modules/home-song-roster.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  EMPTY_ACTION_TEXT,
  emptyText,
  NO_KEEPER_TEXT,
  NO_SESSION_TEXT,
  ROSTER_VISIBLE_ROWS,
  renderRoster,
  rosterURL,
  rosterView,
  cutOnWord,
  sessionDate,
  songFactsSnapshot
} from './home-song-roster.js';

const NOW = new Date(2026, 10, 7, 12, 0, 0);

// The words the Music Project Management 0.5.0 candidate declares; the roster
// response carries them (PRD #792 FR7).
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

function row(n, overrides = {}) {
  return {
    workspace_id: `ws-${n}`,
    entry_id: `entry-${n}`,
    title: `Song ${String(n).padStart(3, '0')}`,
    keeper_agent_name: 'Studio Keeper',
    last_session: {
      accepted_at: new Date(2026, 10, 5, 10, 0, 0).toISOString(),
      first_sentence: 'Tracked the bass'
    },
    open_work_counts: { backlog: 1, ready: 2, in_progress: 0, review: 1, done: 3, cancelled: 0 },
    workspace_route: `/workspaces/song-${n}`,
    ...overrides
  };
}

// ---- Minimal DOM (innerHTML throws).
function makeDocument() {
  const doc = {
    createElement: tag => makeNode(tag, doc),
    createTextNode: value => ({ textContent: String(value), children: [] })
  };
  return doc;
}

function makeNode(tag, doc) {
  const attributes = {};
  const listeners = {};
  return {
    tagName: tag.toUpperCase(),
    ownerDocument: doc,
    children: [],
    className: '',
    hidden: true,
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
    },
    setAttribute(k, v) {
      attributes[k] = String(v);
    },
    getAttribute(k) {
      return attributes[k] ?? null;
    },
    addEventListener(type, handler) {
      (listeners[type] ||= []).push(handler);
    },
    fire(type) {
      for (const handler of listeners[type] || []) handler({ type, preventDefault() {} });
    }
  };
}

function all(root, predicate, out = []) {
  if (predicate(root)) out.push(root);
  for (const child of root.children || []) all(child, predicate, out);
  return out;
}

test('a row words keeper, last session and open work; the title links to the song', () => {
  const view = rosterView([row(1)], { now: NOW, words: MUSIC_WORDS });
  assert.equal(view.title, 'Songs and assistants');
  assert.equal(view.total, 1);
  assert.equal(
    rosterView([row(1, { title: '' })], { now: NOW, words: MUSIC_WORDS }).rows[0].title,
    'Untitled song'
  );
  assert.deepEqual(view.rows[0], {
    id: 'ws-1',
    title: 'Song 001',
    stageText: '—',
    stageLabel: 'Stage not set',
    nextActionText: '—',
    nextActionLabel: 'No next action',
    href: '/workspaces/song-1',
    keeperText: 'Studio Keeper',
    hasKeeper: true,
    sessionText: 'Nov 5 — Tracked the bass',
    hasSession: true,
    openWorkText: '1 in review · 2 ready'
  });
  assert.equal(view.showAll, null);
  assert.equal(view.empty, null);
});

test('fallbacks are sentences: no assistant, no sessions, and no line for unknown counts', () => {
  const view = rosterView(
    [row(1, { keeper_agent_name: '', last_session: null, open_work_counts: undefined })],
    { now: NOW }
  );
  assert.equal(view.rows[0].keeperText, NO_KEEPER_TEXT);
  assert.equal(view.rows[0].sessionText, NO_SESSION_TEXT);
  assert.equal(view.rows[0].openWorkText, '');
  const zero = rosterView([row(2, { open_work_counts: { ready: 0 } })], { now: NOW });
  assert.equal(zero.rows[0].openWorkText, 'No open work');
});

test('ten rows show in server order; Show all (N) expands in place', () => {
  const rows = Array.from({ length: 13 }, (_, i) => row(i + 1));
  const collapsed = rosterView(rows, { now: NOW });
  assert.equal(collapsed.rows.length, ROSTER_VISIBLE_ROWS);
  assert.deepEqual(
    collapsed.rows.map(r => r.id),
    rows.slice(0, 10).map(r => r.workspace_id)
  );
  assert.deepEqual(collapsed.showAll, { label: 'Show all (13)' });
  const expanded = rosterView(rows, { now: NOW, expanded: true });
  assert.equal(expanded.rows.length, 13);
  assert.equal(expanded.showAll, null);
});

test('an empty Home says so in one line linking to the library sheet', () => {
  const view = rosterView([], {
    now: NOW,
    words: MUSIC_WORDS,
    sheet: { href: '/workspaces/home/assistant#projectLibraryPanel' }
  });
  assert.deepEqual(view.empty, {
    text: emptyText(MUSIC_WORDS),
    action: { label: EMPTY_ACTION_TEXT, href: '/workspaces/home/assistant#projectLibraryPanel' }
  });
  const container = makeDocument().createElement('div');
  let opened = 0;
  renderRoster(container, view, { onOpenSheet: () => opened++ });
  const link = all(container, n => n.tagName === 'A')[0];
  assert.equal(link.getAttribute('href'), '/workspaces/home/assistant#projectLibraryPanel');
  assert.match(
    container.textContent,
    /No songs are linked yet\. Set up projects from the map’s library badge\./
  );
  link.fire('click');
  assert.equal(opened, 1);
});

test('each row shows its stage label and next action, cut on a word at 80 characters (PRD #792 FR15)', () => {
  const long =
    'Send the stems to the mixer and ask for a second revision of the chorus vocal balance today please';
  const view = rosterView([row(1, { stage: 'mixing', next_action: long })], { now: NOW });
  const first = view.rows[0];
  assert.equal(first.stageText, 'Mixing');
  assert.equal(first.stageLabel, '');
  assert.ok(first.nextActionText.endsWith('…'));
  assert.ok([...first.nextActionText].length <= 80, first.nextActionText);
  assert.ok(long.startsWith(first.nextActionText.slice(0, -1)), 'cut on a word');
  assert.equal(cutOnWord('Mix'), 'Mix');
  assert.equal(cutOnWord('a'.repeat(100)).length, 80);

  const container = makeDocument().createElement('div');
  renderRoster(
    container,
    rosterView([row(1), row(2, { stage: 'idea', next_action: 'Demo' })], { now: NOW })
  );
  const stages = all(container, n => String(n.className || '').startsWith('song-roster-stage'));
  const nexts = all(container, n => String(n.className || '').startsWith('song-roster-next'));
  assert.deepEqual(
    stages.map(n => [n.textContent, n.getAttribute('aria-label')]),
    [
      ['—', 'Stage not set'],
      ['Idea', null]
    ]
  );
  assert.deepEqual(
    nexts.map(n => [n.textContent, n.getAttribute('aria-label')]),
    [
      ['—', 'No next action'],
      ['Demo', null]
    ]
  );
  // An unknown stage is unset, never echoed.
  assert.equal(rosterView([row(3, { stage: 'touring' })], { now: NOW }).rows[0].stageText, '—');
});

test('without package words the roster reads the neutral Project words (PRD #792 FR6, FR8, FR11)', () => {
  const view = rosterView([row(1, { title: '' })], { now: NOW });
  assert.equal(view.title, 'Projects and assistants');
  assert.equal(view.rows[0].title, 'Untitled project');
  const empty = rosterView([], { now: NOW });
  assert.equal(empty.empty.text, 'No projects are linked yet.');
  const container = makeDocument().createElement('div');
  renderRoster(container, empty);
  const section = container.children[0];
  assert.equal(section.getAttribute('aria-label'), 'Projects and assistants');
  const rendered = `${section.getAttribute('aria-label')} ${container.textContent} ${view.rows[0].title}`;
  assert.equal(/\b(songs?|bpm)\b/i.test(rendered) || /\btracks?\b/.test(rendered), false, rendered);
});

test('tile facts use each Home’s own words, leaving out the track count and the varies note', () => {
  const rosters = {
    music: [
      {
        workspace_id: 'a',
        facts: { tempo_bpm: 92, length_seconds: 132, track_count: 3, tempo_varies: true }
      }
    ],
    plain: [{ workspace_id: 'b', facts: { tempo_bpm: 96, length_seconds: 61 } }],
    tracksOnly: [{ workspace_id: 'c', facts: { track_count: 4 } }]
  };
  assert.deepEqual(songFactsSnapshot(rosters, { music: MUSIC_WORDS, tracksOnly: MUSIC_WORDS }), {
    a: '92 BPM · 2:12',
    b: 'Tempo 96 · 1:01',
    c: '4 tracks'
  });
  assert.deepEqual(songFactsSnapshot(rosters), {
    a: 'Tempo 92 · 2:12',
    b: 'Tempo 96 · 1:01',
    c: 'Tracks 4'
  });
});

test('rendering uses a native link per song, keeper as text, and one Show all button', () => {
  const rows = Array.from({ length: 11 }, (_, i) => row(i + 1));
  const container = makeDocument().createElement('div');
  let expanded = 0;
  renderRoster(container, rosterView(rows, { now: NOW }), { onShowAll: () => expanded++ });
  assert.equal(container.hidden, false);
  const anchors = all(container, n => n.tagName === 'A');
  assert.equal(anchors.length, 10);
  assert.equal(anchors[0].getAttribute('href'), '/workspaces/song-1');
  const keepers = all(container, n => String(n.className).startsWith('song-roster-keeper'));
  assert.ok(
    keepers.every(k => k.tagName === 'SPAN'),
    'agent names are text, not links'
  );
  const buttons = all(container, n => n.tagName === 'BUTTON');
  assert.equal(buttons.length, 1);
  assert.equal(buttons[0].getAttribute('type'), 'button');
  assert.equal(buttons[0].textContent, 'Show all (11)');
  buttons[0].fire('click');
  assert.equal(expanded, 1);
});

test('a row without a safe route shows the title as text', () => {
  const view = rosterView([row(1, { workspace_route: 'https://evil.example' })], { now: NOW });
  assert.equal(view.rows[0].href, '');
  const container = makeDocument().createElement('div');
  renderRoster(container, view);
  assert.equal(all(container, n => n.tagName === 'A').length, 0);
});

test('session dates and URL', () => {
  assert.equal(sessionDate(new Date(2025, 2, 4).toISOString(), NOW), 'Mar 4, 2025');
  assert.equal(sessionDate('', NOW), '');
  assert.equal(sessionDate('0001-01-01T00:00:00Z', NOW), '');
  assert.equal(rosterURL('h 1'), '/api/workspaces/h%201/assistant-program/library/roster');
});
