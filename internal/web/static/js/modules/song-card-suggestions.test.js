// Tests for song-card-suggestions.js — fields suggestions on the project card
// with the owner's Accept and Dismiss (PRD #792 FR24).
//   node --test internal/web/static/js/modules/song-card-suggestions.test.js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  acceptLines,
  acceptSuggestion,
  dismissSuggestion,
  proposalURL,
  relativeTime,
  renderSuggestions,
  suggestionHandlers,
  suggestionRows,
  suggestionView
} from './song-card-suggestions.js';
import { renderSongCard, songCardView } from './song-card.js';

const NOW = new Date('2026-11-08T12:00:00Z');
const WORDS = {
  source: 'package',
  noun: 'Song',
  noun_plural: 'Songs',
  roster_title: 'Songs and assistants',
  facts: [],
  fields: [
    { id: 'key', label: 'Key', kind: 'text' },
    { id: 'references', label: 'Reference tracks', kind: 'list' }
  ]
};
const SUGGESTION = {
  id: 'p1',
  agent_name: 'REAPER Assistant',
  proposer_kind: 'keeper',
  created_at: '2026-11-08T11:55:00Z',
  proposed_fields: {
    next_action: 'Send stems to the mixer',
    blockers: ['Vocal comp', 'Bass DI'],
    deliverables: [],
    milestones: [{ id: 'm1', label: 'Mix approved', due_date: '2026-12-01' }],
    extra: { key: ['F minor'], references: ['Song A'], retired: ['hidden'] }
  },
  reason: 'Bounced <b>stems</b>'
};

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
    hidden: false,
    disabled: false,
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
    addEventListener(type, fn) {
      listeners[type] = fn;
    },
    fire(type) {
      return listeners[type]?.();
    }
  };
}

function all(root, predicate, out = []) {
  if (predicate(root)) out.push(root);
  for (const child of root.children || []) all(child, predicate, out);
  return out;
}

const byClass = (root, name) =>
  all(root, n => typeof n.className === 'string' && n.className.split(' ').includes(name));

test('relative times read in plain words', () => {
  assert.equal(relativeTime('2026-11-08T11:59:45Z', NOW), 'just now');
  assert.equal(relativeTime('2026-11-08T11:55:00Z', NOW), '5 minutes ago');
  assert.equal(relativeTime('2026-11-08T09:00:00Z', NOW), '3 hours ago');
  assert.equal(relativeTime('2026-11-07T10:00:00Z', NOW), 'yesterday');
  assert.equal(relativeTime('2026-11-04T12:00:00Z', NOW), '4 days ago');
  assert.equal(relativeTime('nope', NOW), '');
});

test('a suggestion reads one line per proposed field, lists expanded, plugin fields under their labels', () => {
  const view = suggestionView(SUGGESTION, WORDS, NOW);
  assert.equal(view.byline, 'Suggested by REAPER Assistant · 5 minutes ago');
  assert.deepEqual(
    view.rows.map(row => [row.label, row.value, row.lines || null]),
    [
      ['Next action', 'Send stems to the mixer', null],
      ['Blockers', '2 lines', ['Vocal comp', 'Bass DI']],
      ['Deliverables', 'None', []],
      ['Milestones', '1 milestone', ['Mix approved · due 2026-12-01']],
      ['Key', 'F minor', null],
      ['Reference tracks', '1 line', ['Song A']]
    ]
  );
  // Without words no plugin field is shown; a retired id never is.
  assert.equal(
    suggestionRows(SUGGESTION.proposed_fields).some(row => row.label === 'Key'),
    false
  );
  assert.doesNotMatch(JSON.stringify(view.rows), /retired|hidden/);
});

test('the block renders native Accept and Dismiss only when the host passes handlers, and marks the reason untrusted', () => {
  const doc = makeDocument();
  const view = suggestionView(SUGGESTION, WORDS, NOW);
  const readOnly = renderSuggestions(doc, [view]);
  assert.equal(all(readOnly, n => n.tagName === 'BUTTON').length, 0);
  const calls = [];
  const block = renderSuggestions(doc, [view], {
    onAccept: s => calls.push(['accept', s.id]),
    onDismiss: s => calls.push(['dismiss', s.id])
  });
  const buttons = all(block, n => n.tagName === 'BUTTON');
  assert.deepEqual(
    buttons.map(b => [b.textContent, b.getAttribute('type')]),
    [
      ['Accept', 'button'],
      ['Dismiss', 'button']
    ]
  );
  buttons[0].fire('click');
  buttons[1].fire('click');
  assert.deepEqual(calls, [
    ['accept', 'p1'],
    ['dismiss', 'p1']
  ]);
  assert.equal(
    byClass(block, 'song-card-suggestion-reason')[0].textContent,
    'Reason (untrusted note): Bounced <b>stems</b>'
  );
  assert.match(block.textContent, /Next action → Send stems to the mixer/);
  assert.match(block.textContent, /Blockers → 2 linesVocal compBass DI/);
  assert.equal(renderSuggestions(doc, []), null);
});

test('the card carries this song’s suggestions under the details block; a broken link carries none', () => {
  const data = {
    linked: true,
    home_id: 'home-1',
    entry_id: 'entry-1',
    title: 'Night Drive',
    words: WORDS,
    fields: { revision: 2 },
    suggestions: [SUGGESTION, { id: '', proposed_fields: {} }],
    home_entry_route: '/workspaces/music-home/assistant#projectLibraryPanel'
  };
  const view = songCardView(data, { now: NOW });
  assert.equal(view.homeID, 'home-1');
  assert.equal(view.suggestions.length, 1, 'a row without an id or fields is left out');
  const container = makeDocument().createElement('div');
  renderSongCard(container, view, { onAccept() {}, onDismiss() {} });
  const card = container.children[0];
  const classes = card.children.map(child => child.className);
  assert.ok(
    classes.indexOf('song-card-suggestions') === classes.indexOf('song-card-details') + 1,
    classes.join(',')
  );
  assert.equal(
    all(card, n => n.tagName === 'BUTTON').length,
    2,
    'Accept and Dismiss are the card’s only buttons'
  );
  assert.deepEqual(songCardView({ ...data, entry_id: '' }).suggestions, []);
});

test('the Accept lines show before and after for every proposed field, from the Home’s review', () => {
  const review = {
    token: 't',
    before: {
      next_action: '',
      blockers: ['Old'],
      deliverables: ['Mix v1'],
      milestones: [],
      extra: { references: ['Old ref'] }
    },
    after: {
      next_action: 'Send stems to the mixer',
      blockers: ['Vocal comp', 'Bass DI'],
      deliverables: [],
      milestones: [{ id: 'm1', label: 'Mix approved', due_date: '2026-12-01' }],
      extra: { key: ['F minor'], references: ['Song A'] }
    }
  };
  assert.deepEqual(acceptLines(review, suggestionView(SUGGESTION, WORDS, NOW), WORDS), [
    'Next action: Not set → Send stems to the mixer',
    'Blockers: Old → Vocal comp; Bass DI',
    'Deliverables: Mix v1 → None',
    'Milestones: None → Mix approved · due 2026-12-01',
    'Key: Not set → F minor',
    'Reference tracks: Old ref → Song A',
    'Reason (untrusted note): Bounced <b>stems</b>',
    'Only these Home notes change. No project file, ticket or agent is opened or changed.'
  ]);
});

function fakeFetch(responses) {
  const calls = [];
  const fetchImpl = async (url, options) => {
    calls.push({
      url,
      body: options?.body ? JSON.parse(options.body) : null,
      method: options?.method
    });
    const next = responses.shift() || { ok: true, body: {} };
    return { ok: next.ok !== false, status: next.status || 200, json: async () => next.body };
  };
  return { calls, fetchImpl };
}

test('Accept reviews the saved suggestion, asks, then commits with the review token; cancel commits nothing', async () => {
  const suggestion = suggestionView(SUGGESTION, WORDS, NOW);
  const accepted = fakeFetch([
    { body: { token: 'review-1', before: {}, after: {} } },
    { body: {} }
  ]);
  let asked = null;
  const outcome = await acceptSuggestion({
    homeID: 'home 1',
    suggestion,
    words: WORDS,
    title: 'Night Drive',
    fetchImpl: accepted.fetchImpl,
    confirm: async options => {
      asked = options;
      return true;
    }
  });
  assert.equal(outcome, 'accepted');
  assert.equal(asked.title, 'Save this suggestion for Night Drive?');
  assert.deepEqual(
    accepted.calls.map(call => call.url),
    [proposalURL('home 1', 'p1', 'review'), proposalURL('home 1', 'p1', 'commit')]
  );
  assert.equal(
    accepted.calls[0].url,
    '/api/workspaces/home%201/assistant-program/library/proposals/p1/review'
  );
  assert.deepEqual(accepted.calls[0].body, {}, 'the browser never sends a patch');
  assert.deepEqual(Object.keys(accepted.calls[1].body).sort(), [
    'confirm',
    'idempotency_key',
    'review_token'
  ]);
  assert.equal(accepted.calls[1].body.review_token, 'review-1');
  assert.equal(accepted.calls[1].body.confirm, true);

  const canceled = fakeFetch([{ body: { token: 'review-2', before: {}, after: {} } }]);
  assert.equal(
    await acceptSuggestion({
      homeID: 'h',
      suggestion,
      words: WORDS,
      fetchImpl: canceled.fetchImpl,
      confirm: async () => false
    }),
    'canceled'
  );
  assert.equal(canceled.calls.length, 1, 'no commit after Cancel');

  const refused = fakeFetch([
    { ok: false, status: 409, body: { error: 'This suggestion is no longer current' } }
  ]);
  await assert.rejects(
    acceptSuggestion({
      homeID: 'h',
      suggestion,
      words: WORDS,
      fetchImpl: refused.fetchImpl,
      confirm: async () => true
    }),
    /no longer current/
  );
});

test('Dismiss confirms once and posts confirm with an idempotency key', async () => {
  const suggestion = suggestionView(SUGGESTION, WORDS, NOW);
  const dismissed = fakeFetch([{ body: {} }]);
  assert.equal(
    await dismissSuggestion({
      homeID: 'h',
      suggestion,
      fetchImpl: dismissed.fetchImpl,
      confirm: async () => true
    }),
    'dismissed'
  );
  assert.equal(dismissed.calls[0].url, proposalURL('h', 'p1', 'dismiss'));
  assert.equal(dismissed.calls[0].body.confirm, true);
  assert.ok(dismissed.calls[0].body.idempotency_key);
  const kept = fakeFetch([]);
  assert.equal(
    await dismissSuggestion({
      homeID: 'h',
      suggestion,
      fetchImpl: kept.fetchImpl,
      confirm: async () => false
    }),
    'canceled'
  );
  assert.equal(kept.calls.length, 0);
});

test('the mount handlers re-fetch the card after an answer, not after a cancel, and report failures', async () => {
  const view = songCardView(
    {
      linked: true,
      home_id: 'h',
      entry_id: 'e',
      title: 'Night Drive',
      words: WORDS,
      suggestions: [SUGGESTION]
    },
    { now: NOW }
  );
  const changed = [];
  const errors = [];
  const ok = fakeFetch([{ body: {} }]);
  const handlers = suggestionHandlers(view, {
    fetchImpl: ok.fetchImpl,
    confirm: async () => true,
    onChanged: outcome => changed.push(outcome),
    onError: err => errors.push(err)
  });
  const button = { disabled: false };
  await handlers.onDismiss(view.suggestions[0], button);
  assert.deepEqual(changed, ['dismissed']);
  assert.equal(button.disabled, false);
  const no = suggestionHandlers(view, {
    fetchImpl: ok.fetchImpl,
    confirm: async () => false,
    onChanged: o => changed.push(o)
  });
  await no.onAccept(view.suggestions[0], button);
  assert.deepEqual(changed, ['dismissed'], 'a cancel re-fetches nothing');
  const failing = fakeFetch([{ ok: false, status: 409, body: { error: 'stale' } }]);
  const bad = suggestionHandlers(view, {
    fetchImpl: failing.fetchImpl,
    confirm: async () => true,
    onError: err => errors.push(err)
  });
  await bad.onAccept(view.suggestions[0], button);
  assert.equal(errors.length, 1);
  assert.deepEqual(suggestionHandlers({ homeID: '', suggestions: [] }), {});
});
