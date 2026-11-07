// Tests for song-card.js — the read-only Song card on a linked song's Details
// page and in the Home map's selected context.
//   node --test internal/web/static/js/modules/song-card.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FACTS_ABSENT_TEXT,
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

function linked(overrides = {}) {
  return {
    linked: true,
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
    assert.equal(view.factsNote, FACTS_ABSENT_TEXT);
    assert.equal(view.factsSwitch, null);
  }
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
