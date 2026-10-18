import test from 'node:test';
import assert from 'node:assert/strict';
import {
  collectionReviewPage,
  describeEffect,
  mountCollectionReview
} from './home-collection-review.js';
import { exactName } from './home-collection-candidates.js';

test('review lines name each change and member in words, not internal identifiers', () => {
  const effect = (kind, fields) => ({
    kind,
    before_name: '',
    after_name: '',
    before_members: 0,
    after_members: 0,
    ...fields
  });
  assert.equal(
    describeEffect(effect('create_collection', { after_name: 'Client work', after_members: 2 })),
    'New collection “Client work” · 2 members'
  );
  assert.equal(
    describeEffect(
      effect('rename_collection', {
        before_name: 'Old',
        after_name: 'New',
        before_members: 1,
        after_members: 1
      })
    ),
    'Rename collection “Old” to “New” · 1 member'
  );
  assert.equal(
    describeEffect(
      effect('set_members', { after_name: 'Mixes', before_members: 3, after_members: 5 })
    ),
    'Change the members of “Mixes” · 3 → 5 members'
  );
  assert.equal(
    describeEffect(effect('remove_collection', { before_name: 'Old', before_members: 4 })),
    'Remove collection “Old” · its 4 members stay in the catalog'
  );
  assert.equal(
    describeEffect(effect('select_activation', { after_members: 1 })),
    'Select 1 project for separate activation reviews'
  );
  // A name is shown alone unless another entry in the same list shares it.
  const members = [
    { id: 'entry-aaaaaaaa-1', name: 'Song' },
    { id: 'entry-bbbbbbbb-2', name: 'Song' },
    { id: 'entry-cccccccc-3', name: 'Intro' }
  ];
  assert.deepEqual(
    members.map(member => exactName(member, members)),
    ['Song (#entry-aa)', 'Song (#entry-bb)', 'Intro']
  );
});

const page = () => ({
  arrangement_id: 'arrangement-a',
  review_token: 'token-a',
  catalog_revision: 2,
  expires_at: '2050-01-01T00:00:00Z',
  goal: 'Keep references',
  rationale: 'Related metadata',
  unchanged: 0,
  needs_review: [],
  unknowns: [],
  coverage: { note: 'Saved catalog only.', partial: true },
  effects: [
    {
      kind: 'create_collection',
      collection_id: 'collection-a',
      before_name: '',
      after_name: '<img src=x>',
      before_members: 0,
      after_members: 0
    }
  ],
  operation: 0,
  offset: 0,
  total: 0,
  members: []
});
function dom() {
  const document = {
    createElement: tag => ({
      tag,
      ownerDocument: document,
      children: [],
      events: {},
      textContent: '',
      attributes: {},
      isConnected: true,
      append(...nodes) {
        this.children.push(...nodes);
      },
      replaceChildren(...nodes) {
        this.children = nodes;
        this.textContent = '';
      },
      setAttribute(key, value) {
        this.attributes[key] = value;
      },
      addEventListener(key, fn) {
        this.events[key] = fn;
      }
    })
  };
  return document.createElement('div');
}
const nodes = root => [root, ...root.children.flatMap(nodes)];
const find = (root, text) => nodes(root).find(n => n.textContent === text);
const choice = root => nodes(root).find(n => n.type === 'checkbox');
function mount(request, isCurrent = () => true, options = {}) {
  const root = dom();
  let reloads = 0;
  mountCollectionReview(root, {
    ...options,
    ref: { id: 'arrangement-a', revision: 1, catalog_revision: 1 },
    request,
    isCurrent,
    reload: async () => {
      reloads++;
    }
  });
  return {
    root,
    get reloads() {
      return reloads;
    }
  };
}

test('collection page accepts bounded canonical data, not foreign, incomplete or executable effects', () => {
  assert.ok(collectionReviewPage(page(), 'arrangement-a'));
  for (const value of [
    { ...page(), arrangement_id: 'foreign' },
    { ...page(), total: 1 },
    { ...page(), offset: -1 },
    { ...page(), review_token: 'foreign' },
    { ...page(), effects: [{ ...page().effects[0], kind: 'activate' }] },
    { ...page(), next_offset: 0 },
    { ...page(), needs_review: [{ reason: 'missing count' }] }
  ]) {
    assert.equal(collectionReviewPage(value, 'arrangement-a', 'token-a'), null);
  }
});
test('collection confirmation is consumed before sending and uncertain outcomes never retry', async () => {
  const calls = [];
  let rejectCommit;
  const ui = mount(async (path, options) => {
    calls.push([path, options]);
    if (path.endsWith('/collection-review')) return page();
    return new Promise((_, reject) => {
      rejectCommit = reject;
    });
  });
  assert.equal(calls.length, 0);
  await find(ui.root, 'Review collection metadata').events.click();
  await find(ui.root, 'Review collection metadata').events.click(); // stale ref cannot create another review
  assert.equal(calls.length, 1);
  assert.ok(nodes(ui.root).some(n => n.textContent.includes('<img src=x>'))); // literal text, no HTML sink
  const confirm = find(ui.root, 'Apply collection metadata');
  await confirm.events.click();
  assert.equal(calls.length, 1);
  const check = choice(ui.root);
  check.checked = true;
  check.events.change();
  const applying = confirm.events.click();
  assert.equal(check.checked, false);
  await confirm.events.click();
  assert.equal(calls.length, 2);
  assert.deepEqual(JSON.parse(calls[1][1].body).confirm, true);
  rejectCommit(new Error('raw private provider failure'));
  await applying;
  await confirm.events.click();
  assert.equal(calls.length, 2);
  assert.ok(
    nodes(ui.root).some(n => n.textContent.includes('Reopen saved history before trying again'))
  );
  assert.ok(!nodes(ui.root).some(n => n.textContent.includes('raw private')));
  await find(ui.root, 'Reopen saved history').events.click();
  assert.equal(ui.reloads, 1);
});
test('paging exact membership clears confirmation and preserves the review token', async () => {
  const member = i => ({
    id: `entry-${i}`,
    name: `Project ${i}`,
    availability: 'not_scanned',
    before: false,
    after: true
  });
  const first = {
    ...page(),
    total: 51,
    next_offset: 50,
    members: Array.from({ length: 50 }, (_, i) => member(i))
  };
  first.effects[0].after_members = 51;
  const second = { ...first, offset: 50, members: [member(50)], next_offset: undefined };
  const calls = [];
  const ui = mount(async path => {
    calls.push(path);
    return calls.length === 1 ? first : second;
  });
  await find(ui.root, 'Review collection metadata').events.click();
  const oldChoice = choice(ui.root);
  oldChoice.checked = true;
  oldChoice.events.change();
  find(ui.root, 'Next members').events.click();
  assert.equal(oldChoice.checked, false);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(!!choice(ui.root).checked, false);
  assert.ok(calls[1].includes('/token-a?operation=0&offset=50'));
  assert.ok(nodes(ui.root).some(n => n.textContent.includes('51–51 of 51 exact members')));
  await find(ui.root, 'Apply collection metadata').events.click();
  assert.equal(calls.length, 2);
});

test('unsaved corrections block Apply/paging and uncertain saves preserve entered changes without retry', async () => {
  const calls = [];
  const ui = mount(async (path, options) => {
    calls.push([path, options]);
    if (path.endsWith('/collection-review'))
      return {
        ...page(),
        effects: [{ ...page().effects[0], after_members: 1 }],
        total: 1,
        members: [
          { id: 'entry-a', name: 'Client', availability: 'available', before: false, after: true }
        ]
      };
    throw new Error('private failure');
  });
  await find(ui.root, 'Review collection metadata').events.click();
  const name = nodes(ui.root).find(n => n.type === 'text');
  name.value = 'Owner correction';
  name.events.input();
  const exclusion = nodes(ui.root).filter(n => n.type === 'checkbox')[1];
  exclusion.checked = true;
  exclusion.events.change();
  choice(ui.root).checked = true;
  choice(ui.root).events.change();
  await find(ui.root, 'Apply collection metadata').events.click();
  assert.equal(calls.length, 1);
  await find(ui.root, 'Save correction — not apply').events.click();
  assert.equal(calls.length, 2);
  assert.deepEqual(JSON.parse(calls[1][1].body), {
    review_token: 'token-a',
    operation: 0,
    exclude_ids: ['entry-a'],
    name: 'Owner correction'
  });
  assert.equal(name.value, 'Owner correction');
  assert.equal(name.readOnly, true);
  assert.equal(exclusion.checked, true);
  assert.equal(exclusion.disabled, true);
  assert.ok(
    nodes(ui.root).some(n =>
      n.textContent.includes('Your entered correction remains shown, not approved')
    )
  );
  await find(ui.root, 'Save correction — not apply').events.click();
  assert.equal(calls.length, 2);
});

test('saved-member additions are explicit, inhibit Apply and preserve uncertain corrections', async () => {
  const calls = [];
  const ui = mount(async (path, options) => {
    calls.push({ path, options });
    if (calls.length === 1) return page();
    if (calls.length === 2)
      return {
        arrangement_id: 'arrangement-a',
        review_token: 'token-a',
        catalog_revision: 2,
        operation: 0,
        query: 'song',
        offset: 0,
        total: 1,
        members: [
          { id: 'saved-song', name: '<script>song</script>', availability: 'revoked_source' }
        ]
      };
    throw new Error('lost correction response');
  });
  await find(ui.root, 'Review collection metadata').events.click();
  assert.equal(calls.length, 1, 'picker must not query automatically');
  nodes(ui.root).find(n => n.type === 'search').value = 'song';
  find(ui.root, 'Find saved members').events.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.match(calls[1].path, /\/candidates\?operation=0&offset=0&query=song$/);
  const addition = nodes(ui.root).find(n => n.value === 'saved-song');
  addition.checked = true;
  addition.events.change();
  assert.ok(find(ui.root, '<script>song</script> '), 'name is text, not HTML');
  choice(ui.root).checked = true;
  await find(ui.root, 'Apply collection metadata').events.click();
  assert.equal(calls.length, 2, 'dirty additions cannot apply');
  await find(ui.root, 'Save correction — not apply').events.click();
  assert.deepEqual(JSON.parse(calls[2].options.body), {
    review_token: 'token-a',
    operation: 0,
    exclude_ids: [],
    add_ids: ['saved-song']
  });
  assert.equal(addition.checked, true, 'uncertain save retains the entered selection');
  await find(ui.root, 'Save correction — not apply').events.click();
  assert.equal(calls.length, 3, 'uncertain addition is not retried');
});

test('activation selection discloses independent confirmation and never claims a created project', async () => {
  const selected = {
    ...page(),
    effects: [
      {
        kind: 'select_activation',
        collection_id: '',
        queue_id: 'queue-a',
        before_name: '',
        after_name: 'Projects awaiting separate activation review',
        before_members: 0,
        after_members: 1
      }
    ],
    total: 1,
    members: [
      { id: 'entry-a', name: 'Song', availability: 'available', before: false, after: true }
    ]
  };
  assert.ok(collectionReviewPage(selected, 'arrangement-a'));
  assert.equal(
    collectionReviewPage(
      { ...selected, effects: [{ ...selected.effects[0], after_members: 101 }] },
      'arrangement-a'
    ),
    null
  );
  const calls = [];
  const ui = mount(async (path, options) => {
    calls.push({ path, options });
    if (calls.length === 1) return selected;
    throw new Error('lost preparation response');
  });
  await find(ui.root, 'Review collection metadata').events.click();
  assert.ok(
    nodes(ui.root).some(n =>
      n.textContent.includes('Each project still needs its own exact file choice')
    )
  );
  assert.equal(find(ui.root, 'Apply collection metadata'), undefined);
  choice(ui.root).checked = true;
  await find(ui.root, 'Save metadata and prepare activation reviews').events.click();
  assert.equal(calls.length, 2);
  assert.ok(calls[1].path.endsWith('/collection-commit'));
  assert.ok(!nodes(ui.root).some(n => n.textContent.includes('selection saved')));
});

test('decline is a model-free canonical action and never retries uncertain outcomes', async () => {
  const calls = [];
  const ui = mount(async (path, options) => {
    calls.push({ path, options });
    return {
      kind: 'proposal',
      catalog_current: true,
      arrangement: {
        id: 'arrangement-a',
        revision: 1,
        state: 'cancelled',
        origin: 'model',
        review_available: false,
        goal: 'Keep references',
        rationale: '',
        explicit_unchanged: 0,
        explicit_needs_review: 0,
        operations: 0,
        proposed_operations: [],
        review_questions: [],
        unknowns: [],
        coverage: { note: 'Saved metadata only' }
      }
    };
  });
  await find(ui.root, 'Decline proposal').events.click();
  assert.equal(calls.length, 1);
  assert.ok(calls[0].path.endsWith('/decline'));
  assert.deepEqual(JSON.parse(calls[0].options.body), {
    expected_revision: 1,
    expected_record_revision: 1
  });
  assert.ok(find(ui.root, 'Declined arrangement — not applied'));
  await find(ui.root, 'Review collection metadata').events.click();
  await find(ui.root, 'Decline proposal').events.click();
  assert.equal(calls.length, 1);
  let requests = 0;
  const uncertain = mount(async (_path, options) => {
    requests++;
    if (requests === 1) return page();
    assert.equal(JSON.parse(options.body).expected_revision, 2);
    throw new Error('private decline error');
  });
  await find(uncertain.root, 'Review collection metadata').events.click();
  await find(uncertain.root, 'Decline proposal').events.click();
  await find(uncertain.root, 'Decline proposal').events.click();
  assert.equal(requests, 2);
  assert.ok(
    nodes(uncertain.root).some(n =>
      n.textContent.includes('Decline outcome could not be confirmed')
    )
  );
  assert.ok(!nodes(uncertain.root).some(n => n.textContent.includes('private decline error')));
});

test('unresolved discussion can be declined but cannot prepare effects', async () => {
  let calls = 0;
  const ui = mount(
    async () => {
      calls++;
      throw new Error('uncertain decline');
    },
    () => true,
    { reviewAllowed: false }
  );
  assert.equal(find(ui.root, 'Review collection metadata').hidden, true);
  await find(ui.root, 'Review collection metadata').events.click();
  assert.equal(calls, 0);
  await find(ui.root, 'Decline proposal').events.click();
  assert.equal(calls, 1);
});

test('late review and expired confirmation cannot become authority', async () => {
  let active = true,
    finish;
  const ui = mount(
    () =>
      new Promise(resolve => {
        finish = resolve;
      }),
    () => active
  );
  const loading = find(ui.root, 'Review collection metadata').events.click();
  active = false;
  finish(page());
  await loading;
  assert.equal(choice(ui.root), undefined);
  let calls = 0;
  const expired = mount(async () => {
    calls++;
    return { ...page(), expires_at: '2000-01-01T00:00:00Z' };
  });
  await find(expired.root, 'Review collection metadata').events.click();
  choice(expired.root).checked = true;
  await find(expired.root, 'Apply collection metadata').events.click();
  assert.equal(calls, 1);
  assert.equal(choice(expired.root), undefined);
});

test('intent changes save typed revisions only: conversion, question resolution, release', async () => {
  const calls = [];
  const reviewPage = {
    ...page(),
    unchanged: 2,
    needs_review: [{ reason: 'Which take is authoritative?', count: 1 }]
  };
  const revised = {
    kind: 'proposal',
    catalog_current: true,
    arrangement: {
      id: 'arrangement-a',
      revision: 2,
      state: 'needs_more_evidence',
      origin: 'model',
      goal: 'Keep references',
      rationale: 'Related metadata',
      unknowns: [],
      operations: 2,
      proposed_operations: [
        { kind: 'create_collection', name: 'References', members: 2 },
        { kind: 'select_activation', members: 2 }
      ],
      explicit_unchanged: 2,
      explicit_needs_review: 1,
      review_questions: [{ reason: 'Which take is authoritative?', members: 1 }],
      coverage: { origin: 'saved_home_catalog', note: 'Saved catalog only.' },
      review_available: false,
      created_at: '2026-01-01T00:00:00Z'
    },
    collection_correction: {
      revision: 2,
      operation: 0,
      renamed: false,
      excluded: 0,
      converted: 'select_activation',
      at: '2026-01-01T00:00:00Z'
    }
  };
  const ui = mount(async (path, options) => {
    calls.push([path, options && JSON.parse(options.body || 'null')]);
    if (path.endsWith('/collection-review')) return reviewPage;
    return revised;
  });
  await find(ui.root, 'Review collection metadata').events.click();
  for (const label of [
    'Also select catalog-only members for activation reviews',
    'Keep question 1 items unchanged',
    'Dismiss question 1',
    'Release 2 explicitly unchanged items'
  ])
    assert.ok(find(ui.root, label), label);
  assert.equal(find(ui.root, 'Keep these projects catalog-only instead'), undefined);
  await find(ui.root, 'Also select catalog-only members for activation reviews').events.click();
  assert.deepEqual(calls.at(-1), [
    '/arrangements/arrangement-a/collection-correction',
    { review_token: 'token-a', operation: 0, convert: 'select_activation' }
  ]);
  const text = nodes(ui.root)
    .map(n => n.textContent)
    .join('\n');
  assert.match(text, /Correction saved as a new proposal revision/);
  assert.match(text, /collection members also selected for independent activation reviews/);
  // The saved revision is history: no further intent change or apply is live.
  const before = calls.length;
  await find(ui.root, 'Review collection metadata').events.click();
  assert.equal(calls.length, before);
});
