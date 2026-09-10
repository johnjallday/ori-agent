import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  managementView,
  followUpActionFor,
  followUpIDFromSearch,
  focusManagementCard,
  renderManagementPanel,
  renderManagementCard,
  wireWorkspaceFollowUps
} from './workspace-followups.js';

// --- a tiny DOM stub: enough for createElement + append + click ------------
function fakeDoc() {
  const make = tag => ({
    tagName: tag,
    className: '',
    textContent: '',
    type: '',
    href: '',
    hidden: false,
    dataset: {},
    children: [],
    listeners: {},
    attributes: {},
    appendChild(c) {
      this.children.push(c);
      return c;
    },
    append(...cs) {
      cs.forEach(c => this.children.push(c));
    },
    addEventListener(ev, fn) {
      this.listeners[ev] = fn;
    },
    setAttribute(name, value) {
      this.attributes[name] = value;
    },
    scrollIntoView(options) {
      this.scrolledWith = options;
    },
    focus(options) {
      this.focusedWith = options;
    },
    set innerHTML(v) {
      if (v === '') this.children = [];
    },
    get innerHTML() {
      return '';
    }
  });
  return { createElement: make };
}

function collectButtons(node, out = []) {
  if (!node) return out;
  if (node.tagName === 'button') out.push(node);
  (node.children || []).forEach(c => collectButtons(c, out));
  return out;
}

test('managementView maps items and gates visibility', () => {
  const empty = managementView([]);
  assert.equal(empty.show, false);
  assert.equal(empty.count, 0);

  const v = managementView([
    { id: 'f1', title: 'Reply to landlord', category: 'i_owe', status: 'active' },
    { id: 'f2', title: 'Waiting on quote', category: 'waiting_on', status: 'candidate' }
  ]);
  assert.equal(v.show, true);
  assert.equal(v.count, 2);
  assert.equal(v.items[0].title, 'Reply to landlord');
  assert.equal(v.items[1].isCandidate, true);
});

test('followUpIDFromSearch accepts only one exact bounded deep-link identity', () => {
  assert.equal(followUpIDFromSearch('?follow_up=follow-1'), 'follow-1');
  assert.equal(followUpIDFromSearch('?other=x&follow_up=follow_2'), 'follow_2');
  for (const search of [
    '',
    '?follow_up=',
    '?follow_up=one&follow_up=two',
    '?follow_up=../foreign',
    '?follow_up=%2Fforeign',
    `?follow_up=${'x'.repeat(201)}`
  ]) {
    assert.equal(followUpIDFromSearch(search), '', search);
  }
});

test('followUpActionFor maps kinds to the shared mutation endpoints', () => {
  assert.equal(followUpActionFor('complete').url, '/api/personal-hq/followups/complete');
  assert.equal(followUpActionFor('dismiss').url, '/api/personal-hq/followups/dismiss');
  assert.equal(followUpActionFor('snooze').url, '/api/personal-hq/followups/snooze');
  assert.equal(followUpActionFor('confirm').url, '/api/personal-hq/followups/confirm');
  assert.equal(followUpActionFor('nope'), null);
});

test('renderManagementCard wires Done to a complete action; candidates get Track/Dismiss', () => {
  const doc = fakeDoc();
  const fired = [];
  const active = renderManagementCard(
    doc,
    { id: 'f1', title: 'X', category: 'You owe', isCandidate: false },
    (kind, v) => fired.push([kind, v.id])
  );
  const btns = collectButtons(active);
  assert.deepEqual(
    btns.map(b => b.textContent),
    ['Done', 'Snooze 1 day']
  );
  btns[0].listeners.click();
  assert.deepEqual(fired, [['complete', 'f1']]);

  const cand = renderManagementCard(
    doc,
    { id: 'f2', title: 'Y', category: 'Waiting on', isCandidate: true },
    () => {}
  );
  assert.deepEqual(
    collectButtons(cand).map(b => b.textContent),
    ['Track this', 'Not a follow-up']
  );
});

test('focusManagementCard highlights and focuses only an exact rendered card', () => {
  const doc = fakeDoc();
  const card = doc.createElement('div');
  card.className = 'hq-followup-card';
  assert.equal(focusManagementCard(card, 'Waiting for agreement'), true);
  assert.match(card.className, /is-deep-linked/);
  assert.equal(card.tabIndex, -1);
  assert.equal(card.attributes['aria-label'], 'Selected follow-up: Waiting for agreement');
  assert.deepEqual(card.scrolledWith, { block: 'center' });
  assert.deepEqual(card.focusedWith, { preventScroll: true });
  assert.equal(focusManagementCard(null), false);
});

test('renderManagementPanel hides when empty, shows cards otherwise', () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');

  renderManagementPanel(doc, mount, managementView([]), () => {});
  assert.equal(mount.hidden, true);

  renderManagementPanel(
    doc,
    mount,
    managementView([{ id: 'f1', title: 'X', category: 'i_owe', status: 'active' }]),
    () => {}
  );
  assert.equal(mount.hidden, false);
  assert.ok(collectButtons(mount).length >= 1);
});

test('wireWorkspaceFollowUps focuses an exact owner-local deep link without mutating', async () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  const posted = [];
  await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'eo-1',
    mount,
    locationSearch: '?follow_up=f2',
    fetchImpl: async () => ({
      ok: true,
      json: async () => ({
        followups: [
          { id: 'f1', title: 'First', category: 'i_owe', status: 'active' },
          { id: 'f2', title: 'Exact record', category: 'waiting_on', status: 'active' }
        ]
      })
    }),
    postImpl: async (...args) => posted.push(args)
  });
  const cards = mount.children[1].children;
  assert.doesNotMatch(cards[0].className, /is-deep-linked/);
  assert.match(cards[1].className, /is-deep-linked/);
  assert.equal(cards[1].focusedWith.preventScroll, true);
  assert.deepEqual(posted, []);

  const untouched = doc.createElement('div');
  await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'eo-1',
    mount: untouched,
    locationSearch: '?follow_up=missing',
    fetchImpl: async () => ({
      ok: true,
      json: async () => ({
        followups: [{ id: 'f1', title: 'First', category: 'i_owe', status: 'active' }]
      })
    }),
    postImpl: async (...args) => posted.push(args)
  });
  assert.doesNotMatch(untouched.children[1].children[0].className, /is-deep-linked/);
  assert.deepEqual(posted, []);
});

test('wireWorkspaceFollowUps loads the workspace list and re-fetches after a mutation', async () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  const fetched = [];
  const posted = [];
  let payload = {
    followups: [{ id: 'f1', title: 'Reply to landlord', category: 'i_owe', status: 'active' }]
  };
  const fetchImpl = async url => {
    fetched.push(url);
    return { ok: true, json: async () => payload };
  };
  const postImpl = async (url, body) => {
    posted.push([url, body.id]);
    payload = { followups: [] }; // completing empties the list
    return {};
  };

  await wireWorkspaceFollowUps({ doc, workspaceId: 'eo-1', mount, fetchImpl, postImpl });
  assert.equal(fetched[0], '/api/workspaces/eo-1/followups');
  assert.equal(mount.hidden, false);

  // Click "Done" → posts complete, then re-fetches (now empty → panel hides).
  const doneBtn = collectButtons(mount).find(b => b.textContent === 'Done');
  await doneBtn.listeners.click();
  assert.deepEqual(posted, [['/api/personal-hq/followups/complete', 'f1']]);
  assert.equal(fetched.length, 2);
  assert.equal(mount.hidden, true);
});
