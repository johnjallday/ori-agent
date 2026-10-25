import test from 'node:test';
import assert from 'node:assert/strict';
import { createReviewVisit } from './personal-assistant-review-visit.js';

function fixture(id = '') {
  let current = {
    id,
    text: 'Keep my Albums question',
    folder: {
      generation: 3,
      preview: true,
      observation: { id: 'selection' },
      focusIDs: ['album-5']
    },
    link: { generation: 4, active: true, id: 'link' },
    focus: 'composer'
  };
  const original = structuredClone(current);
  const calls = [];
  let intercept = () => {};
  const visit = createReviewVisit({
    capture: () => structuredClone(current),
    navigate: async (next, guard) => {
      await intercept();
      if (!guard()) return false;
      calls.push(['navigate', next]);
      current = {
        id: next,
        text: '',
        folder: { generation: 5, preview: false },
        link: { generation: 6, active: false }
      };
      return true;
    },
    fresh: () => {
      calls.push(['fresh']);
      current = { id: '', text: '' };
      return true;
    },
    restore: saved => {
      calls.push(['restore']);
      current = saved;
    },
    focus: () => calls.push(['focus']),
    changed: () => {},
    notify: message => calls.push(['notice', message])
  });
  return {
    visit,
    calls,
    original,
    current: () => current,
    change: patch => Object.assign(current, patch),
    intercept: fn => {
      intercept = fn;
    }
  };
}

for (const id of ['', 'saved-albums']) {
  test(`visiting Album-5 preserves and restores the ${id ? 'saved' : 'unsaved'} draft, link, folder, edits and focus`, async () => {
    const f = fixture(id);
    assert.equal(await f.visit.open('album-5-review'), true);
    assert.deepEqual(f.visit.current(), f.original);
    assert.equal(await f.visit.back(), true);
    assert.deepEqual(f.current(), f.original);
    assert.equal(f.visit.current(), null);
    assert.equal(f.calls.filter(call => call[0] === 'restore').length, 1);
  });
}

test('same-conversation review focuses locally without saving or navigating', async () => {
  const f = fixture('album-5-review');
  assert.equal(await f.visit.open('album-5-review'), true);
  assert.deepEqual(f.calls, [['focus']]);
  assert.equal(f.visit.current(), null);
});

test('new typing, replacement or another navigation while fetching prevents leaving the draft', async () => {
  for (const mode of ['typing', 'folder', 'link', 'clear']) {
    const f = fixture();
    f.intercept(() => {
      if (mode === 'typing') f.change({ text: 'Newer words' });
      if (mode === 'folder') f.change({ folder: { generation: 9 } });
      if (mode === 'link') f.change({ link: { generation: 9 } });
      if (mode === 'clear') f.visit.clear();
    });
    assert.equal(await f.visit.open('album-5-review'), false, mode);
    assert.equal(f.visit.current(), null);
    assert.equal(f.current().id, '');
  }
});

test('failed navigation and overlapping visits keep the original draft', async () => {
  const f = fixture();
  let release;
  f.intercept(
    () =>
      new Promise(resolve => {
        release = resolve;
      })
  );
  const pending = f.visit.open('album-5-review');
  assert.equal(await f.visit.open('other'), false);
  f.visit.clear();
  release();
  assert.equal(await pending, false);
  assert.deepEqual(f.current(), f.original);
});

test('a new draft in the visited review is never overwritten by Return', async () => {
  const f = fixture();
  await f.visit.open('album-5-review');
  f.change({ text: 'New question in this review' });
  assert.equal(await f.visit.back(), false);
  assert.equal(f.current().text, 'New question in this review');
  assert.deepEqual(f.visit.current(), f.original);
  assert.equal(await f.visit.open('third-review'), false);
});
