import test from 'node:test';
import assert from 'node:assert/strict';

import {
  NEEDS_REVEAL_KEY,
  claimNeedsItem,
  needsItemClaimed,
  needsRevealDecision,
  readNeedsReveal,
  reportRevealKey,
  revealReportCard,
  takeNeedsReveal,
  writeNeedsReveal
} from './personal-assistant-needs-reveal.js';

function memoryStorage(seed = {}) {
  const values = new Map(Object.entries(seed));
  return {
    values,
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value))
  };
}

const throwingStorage = {
  getItem() {
    throw new Error('SecurityError');
  },
  setItem() {
    throw new Error('QuotaExceededError');
  }
};

test('only an item this tab claimed opens the strip, and only once (FR14)', () => {
  const own = ['hire:h1', 'folder:t1'];
  assert.equal(needsRevealDecision({ key: 'hire:h1', own, revealed: [] }), true);
  // From another tab, another device or an earlier session: never claimed here.
  assert.equal(needsRevealDecision({ key: 'hire:h2', own, revealed: [] }), false);
  // Already opened once; a later poll of the same report leaves the user's Hide.
  assert.equal(needsRevealDecision({ key: 'hire:h1', own, revealed: ['hire:h1'] }), false);
  assert.equal(needsRevealDecision({ key: '', own, revealed: [] }), false);
  assert.equal(needsRevealDecision({}), false);
});

test('a claim survives in the tab record and is taken exactly once', () => {
  const storage = memoryStorage();
  assert.equal(needsItemClaimed('hire:h1', storage), false);
  assert.equal(takeNeedsReveal('hire:h1', storage), false);
  assert.equal(claimNeedsItem('hire:h1', storage), true);
  assert.equal(claimNeedsItem('hire:h1', storage), true);
  assert.deepEqual(readNeedsReveal(storage).own, ['hire:h1']);
  assert.equal(needsItemClaimed('hire:h1', storage), true);
  assert.equal(takeNeedsReveal('hire:h1', storage), true);
  assert.equal(takeNeedsReveal('hire:h1', storage), false);
  assert.deepEqual(readNeedsReveal(storage), { own: ['hire:h1'], revealed: ['hire:h1'] });
  // An empty key is never claimed.
  assert.equal(claimNeedsItem('', storage), false);
  assert.equal(claimNeedsItem('  ', storage), false);
});

test('the tab record is bounded and tolerates bad or missing storage', () => {
  const storage = memoryStorage();
  for (let i = 0; i < 50; i++) claimNeedsItem(`hire:h${i}`, storage);
  const record = readNeedsReveal(storage);
  assert.equal(record.own.length, 40);
  assert.equal(record.own[0], 'hire:h10');
  assert.equal(record.own.at(-1), 'hire:h49');

  for (const bad of ['not json', '{"own":"x"}', '[1,2]', '{"own":[1,"",null,"ok"]}']) {
    const read = readNeedsReveal(memoryStorage({ [NEEDS_REVEAL_KEY]: bad }));
    assert.ok(Array.isArray(read.own) && Array.isArray(read.revealed), bad);
  }
  assert.deepEqual(
    readNeedsReveal(memoryStorage({ [NEEDS_REVEAL_KEY]: '{"own":[1,"",null,"ok"]}' })).own,
    ['ok']
  );
  assert.deepEqual(readNeedsReveal(throwingStorage), { own: [], revealed: [] });
  assert.deepEqual(readNeedsReveal(null), { own: [], revealed: [] });
  assert.equal(writeNeedsReveal({ own: ['a'] }, throwingStorage), false);
  assert.equal(claimNeedsItem('hire:h1', throwingStorage), false);
  assert.equal(takeNeedsReveal('hire:h1', throwingStorage), false);
});

test('a report card is known by its kind and look id, as first-look-report.js draws it', () => {
  const card = (lookId, reportKind) => ({ dataset: { lookId, reportKind } });
  assert.equal(reportRevealKey(card('h1', 'hire')), 'hire:h1');
  assert.equal(reportRevealKey(card('t1', 'folder')), 'folder:t1');
  assert.equal(reportRevealKey(card('t1', '')), 'folder:t1');
  assert.equal(reportRevealKey(card('', 'hire')), '');
  assert.equal(reportRevealKey(null), '');
});

test('"Needs you" in the conversation opens the strip at the report card', () => {
  const cards = [{ dataset: { lookId: 'other' } }, { dataset: { lookId: 't1' } }];
  const doc = { querySelectorAll: () => cards };
  const calls = [];
  globalThis.PersonalAssistantToday = {
    reveal: (reason, options) => {
      calls.push({ reason, item: options.item });
      return true;
    }
  };
  try {
    assert.equal(revealReportCard('t1', doc), true);
    assert.deepEqual(calls, [{ reason: 'conversation', item: cards[1] }]);
    // Not drawn yet: the strip still opens, with nothing to scroll to.
    revealReportCard('missing', doc);
    assert.equal(calls.at(-1).item, null);
  } finally {
    delete globalThis.PersonalAssistantToday;
  }
  // Off Home there is no strip: the card is brought into view where it is.
  let scrolled = false;
  cards[1].scrollIntoView = () => {
    scrolled = true;
  };
  assert.equal(revealReportCard('t1', doc), true);
  assert.equal(scrolled, true);
  assert.equal(revealReportCard('missing', doc), false);
});
