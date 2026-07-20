import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  managementView,
  followUpActionFor,
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
  const active = renderManagementCard(doc, { id: 'f1', title: 'X', category: 'You owe', isCandidate: false }, (kind, v) =>
    fired.push([kind, v.id])
  );
  const btns = collectButtons(active);
  assert.deepEqual(btns.map(b => b.textContent), ['Done', 'Snooze 1 day']);
  btns[0].listeners.click();
  assert.deepEqual(fired, [['complete', 'f1']]);

  const cand = renderManagementCard(doc, { id: 'f2', title: 'Y', category: 'Waiting on', isCandidate: true }, () => {});
  assert.deepEqual(collectButtons(cand).map(b => b.textContent), ['Track this', 'Not a follow-up']);
});

test('renderManagementPanel hides when empty, shows cards otherwise', () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');

  renderManagementPanel(doc, mount, managementView([]), () => {});
  assert.equal(mount.hidden, true);

  renderManagementPanel(doc, mount, managementView([{ id: 'f1', title: 'X', category: 'i_owe', status: 'active' }]), () => {});
  assert.equal(mount.hidden, false);
  assert.ok(collectButtons(mount).length >= 1);
});

test('wireWorkspaceFollowUps loads the workspace list and re-fetches after a mutation', async () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  const fetched = [];
  const posted = [];
  let payload = { followups: [{ id: 'f1', title: 'Reply to landlord', category: 'i_owe', status: 'active' }] };
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
